"""Offline checks for download integrity, relocation and complete runtime inventories."""
import hashlib
import json
from pathlib import Path
import stat
import tempfile
import unittest
from unittest.mock import patch
import zipfile

import node_toolchain
import screener_bundle as bundle


def fixture(directory, target="linux-amd64"):
    directory.mkdir(parents=True)
    for name in bundle.required_files(target):
        path = directory / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(b"Pinned runtime fixture")
    for name, version in (("dukascopy-node", bundle.DUKASCOPY_VERSION), ("playwright-core", bundle.PLAYWRIGHT_VERSION),
                          ("trading-signals", bundle.TRADING_SIGNALS_VERSION), ("csv-parse", bundle.CSV_PARSE_VERSION)):
        (directory / f"node_modules/{name}/package.json").write_text(json.dumps({"version": version}))
    for name in (bundle.PINS[target]["executable"], bundle.browser_path(target)):
        (directory / name).chmod(0o755)
    files = {path.relative_to(directory).as_posix(): {"bytes": path.stat().st_size, "sha256": bundle.sha256(path)}
             for path in directory.rglob("*") if path.is_file()}
    manifest = {"target": target, "nodeVersion": bundle.NODE_VERSION, "dukascopyVersion": bundle.DUKASCOPY_VERSION,
                "tradingSignalsVersion": bundle.TRADING_SIGNALS_VERSION, "csvParseVersion": bundle.CSV_PARSE_VERSION,
                "playwrightVersion": bundle.PLAYWRIGHT_VERSION, "chromiumVersion": bundle.CHROMIUM_VERSION,
                "chromiumRevision": bundle.CHROMIUM_REVISION, "files": files}
    (directory / bundle.MANIFEST).write_text(json.dumps(manifest))
    return manifest


class ScreenerBundleTests(unittest.TestCase):
    def test_enrichment_worker_catalog_libraries_and_licenses_are_required_on_both_targets(self):
        for target in bundle.PINS:
            required = bundle.required_files(target)
            self.assertTrue({"enrich-worker.cjs", "refresh-worker.cjs", "provider-session.cjs", "provider-transport.cjs", "indicators.json", "licenses/trading-signals-LICENSE",
                             "node_modules/trading-signals/dist/index.js", "node_modules/csv-parse/LICENSE"}.issubset(required))
            for dependency, expected in (("trading-signals", bundle.TRADING_SIGNALS_VERSION), ("csv-parse", bundle.CSV_PARSE_VERSION)):
                with self.subTest(target=target, dependency=dependency), tempfile.TemporaryDirectory() as temporary:
                    directory = Path(temporary) / "runtime"
                    manifest = fixture(directory, target)
                    path = directory / f"node_modules/{dependency}/package.json"
                    path.write_text(json.dumps({"version": "0.0.0"}))
                    # Updating its hash cannot conceal an incorrect package version.
                    manifest["files"][path.relative_to(directory).as_posix()] = {"bytes": path.stat().st_size, "sha256": bundle.sha256(path)}
                    (directory / bundle.MANIFEST).write_text(json.dumps(manifest))
                    with self.assertRaisesRegex(ValueError, "Incorrect packaged Screener dependency"):
                        bundle.validate_directory(directory, target)

    def test_private_node_pin_matches_the_build_toolchain(self):
        self.assertEqual(bundle.NODE_VERSION, node_toolchain.NODE_VERSION)

    def test_verified_download_reuses_only_untampered_cache(self):
        data = b"verified upstream archive"
        checksum = hashlib.sha256(data).hexdigest()
        with tempfile.TemporaryDirectory() as temporary:
            cache = Path(temporary) / "cache with spaces"
            with patch.object(bundle, "download"):
                calls = []
                def fetch(url, destination):
                    calls.append(url)
                    destination.write_bytes(data)
                result = bundle.verified_download(cache, "node.tar.xz", "https://nodejs.org/pinned", checksum, fetch)
                self.assertEqual(result.read_bytes(), data)
                self.assertEqual(bundle.verified_download(cache, result.name, "https://nodejs.org/pinned", checksum, fetch), result)
                self.assertEqual(len(calls), 1)
                result.write_bytes(b"changed archive")
                with self.assertRaisesRegex(ValueError, "SHA-256"):
                    bundle.verified_download(cache, result.name, "https://nodejs.org/pinned", checksum, fetch)
                self.assertEqual(len(calls), 1)

    def test_checksum_failure_does_not_publish_archive_or_runtime(self):
        with tempfile.TemporaryDirectory() as temporary:
            cache = Path(temporary) / "cache"
            def fetch(url, path):
                path.write_bytes(b"wrong download")
            with self.assertRaisesRegex(ValueError, "SHA-256"):
                bundle.verified_download(cache, "browser.zip", "https://cdn.playwright.dev/pinned", "0" * 64, fetch)
            self.assertEqual(list(cache.iterdir()), [])

    def test_zip_retains_executable_mode_and_rejects_traversal_and_symlinks(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / "browser.zip"
            with zipfile.ZipFile(archive, "w") as output:
                binary = zipfile.ZipInfo("chrome-linux64/chrome")
                binary.external_attr = (stat.S_IFREG | 0o755) << 16
                output.writestr(binary, b"browser")
                output.writestr("chrome-linux64/ABOUT", b"notice")
            bundle.extract_zip(archive, root / "runtime")
            self.assertEqual((root / "runtime/chrome-linux64/chrome").stat().st_mode & 0o777, 0o755)
            for name, mode in (("../outside", stat.S_IFREG), ("/absolute", stat.S_IFREG), ("chrome/linked", stat.S_IFLNK)):
                with self.subTest(name=name):
                    with zipfile.ZipFile(archive, "w") as output:
                        entry = zipfile.ZipInfo(name)
                        entry.external_attr = mode << 16
                        output.writestr(entry, "target")
                    with self.assertRaisesRegex(ValueError, "Unsafe"):
                        bundle.extract_zip(archive, root / "unsafe")
            self.assertFalse((root / "outside").exists())

    def test_inventory_rejects_missing_extra_changed_unexecutable_and_wrong_version_files(self):
        for mutation in ("missing", "extra", "changed", "mode", "version", "symlink", "empty"):
            with self.subTest(mutation=mutation), tempfile.TemporaryDirectory() as temporary:
                directory = Path(temporary) / "runtime"
                manifest = fixture(directory)
                bundle.validate_directory(directory, "linux-amd64")
                node = directory / "node"
                if mutation == "missing":
                    (directory / "worker.cjs").unlink()
                elif mutation == "extra":
                    (directory / "unrecorded").write_bytes(b"extra")
                elif mutation == "changed":
                    node.write_bytes(b"changed executable")
                elif mutation == "mode":
                    node.chmod(0o644)
                elif mutation == "version":
                    manifest["dukascopyVersion"] = "0.0.0"
                    (directory / bundle.MANIFEST).write_text(json.dumps(manifest))
                elif mutation == "empty":
                    node.write_bytes(b"")
                else:
                    (directory / "linked").symlink_to(node)
                with self.assertRaisesRegex(ValueError, "Screener"):
                    bundle.validate_directory(directory, "linux-amd64")

    def test_copy_replaces_stale_runtime_and_failure_preserves_previous_installation(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source, destination = root / "source", root / "installed runtime"
            fixture(source)
            destination.mkdir()
            (destination / "old").write_text("previous runtime")
            with patch.object(bundle.shutil, "copytree", side_effect=OSError("disk full")):
                with self.assertRaisesRegex(OSError, "disk full"):
                    bundle.copy_runtime(source, destination, "linux-amd64")
            self.assertEqual((destination / "old").read_text(), "previous runtime")
            bundle.copy_runtime(source, destination, "linux-amd64")
            self.assertFalse((destination / "old").exists())
            bundle.validate_directory(destination, "linux-amd64")
            self.assertFalse(any(root.glob(".screener-copy-*")))

    def test_inventory_reads_each_file_once_in_supplied_physical_archive_order(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary) / "runtime"
            manifest = fixture(directory)
            names = sorted([*manifest["files"], bundle.MANIFEST], reverse=True)
            files = {name: ((directory / name).stat().st_size, True) for name in names}
            reads = []
            def read(name):
                reads.append(name)
                return (directory / name).read_bytes()
            bundle.validate_inventory(manifest, "linux-amd64", files, read)
            self.assertEqual(reads, [name for name in names if name != bundle.MANIFEST])

    def test_only_package_listing_can_allow_playwrights_empty_host_validation_marker(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary) / "runtime"
            fixture(directory)
            marker = directory / f"browsers/chromium-{bundle.CHROMIUM_REVISION}/DEPENDENCIES_VALIDATED"
            marker.write_bytes(b"")
            with self.assertRaisesRegex(ValueError, "inventory"):
                bundle.validate_directory(directory, "linux-amd64")
            bundle.validate_directory(directory, "linux-amd64", allow_runtime_state=True)
            marker.write_bytes(b"unexpected marker data")
            with self.assertRaisesRegex(ValueError, "inventory"):
                bundle.validate_directory(directory, "linux-amd64", allow_runtime_state=True)


if __name__ == "__main__":
    unittest.main()

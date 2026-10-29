"""Bridge release/installer integration, without a JDK, SDK download or login."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest
import zipfile

from jforex_test_fixture import bridge_files, write_bridge
import release
import test_app_icons as icon_tests
from test_release import release_inputs, rewrite_tar


def rewrite_archive(path, modify):
    if path.name.endswith(".tar.gz"):
        def mutate(entry, data):
            if data is None:
                return entry, data
            changed = modify(entry.name, data)
            return None if changed is None else (entry, changed)
        rewrite_tar(path, mutate)
    else:
        with zipfile.ZipFile(path) as archive:
            entries = [(entry, archive.read(entry)) for entry in archive.infolist()]
        with zipfile.ZipFile(path, "w") as archive:
            for entry, data in entries:
                changed = modify(entry.filename, data)
                if changed is not None:
                    archive.writestr(entry, changed)


class JForexPackagingTests(unittest.TestCase):
    def test_both_current_archives_validate_bridge_inventory_and_older_archives_remain_supported(self):
        for with_bridge in (False, True):
            with self.subTest(with_bridge=with_bridge), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                release_inputs(root, with_jforex=with_bridge)
                for target, suffix in release.TARGETS.items():
                    manifest = release.archive_manifest(root / f"cryptodesk-0.1.0-{target}{suffix}", target, "0.1.0")
                    self.assertEqual("jforex" in manifest, with_bridge)

    def test_missing_modified_or_unattributed_bridge_is_rejected_for_both_archive_formats(self):
        for target, suffix in release.TARGETS.items():
            for fault in ("missing-jar", "modified-license", "missing-metadata", "different-metadata"):
                with self.subTest(target=target, fault=fault), tempfile.TemporaryDirectory() as temporary:
                    root = Path(temporary)
                    release_inputs(root, with_jforex=True)
                    archive = root / f"cryptodesk-0.1.0-{target}{suffix}"
                    def modify(name, data):
                        if name.endswith("jforex-runtime/cryptodesk-jforex.jar") and fault == "missing-jar":
                            return None
                        if name.endswith("jforex-runtime/LICENSE") and fault == "modified-license":
                            return b"modified after packaging"
                        if name.endswith("/version.json") and fault in {"missing-metadata", "different-metadata"}:
                            metadata = json.loads(data)
                            if fault == "missing-metadata":
                                del metadata["jforex"]
                            else:
                                metadata["jforex"]["javaMinimum"] = 21
                            return json.dumps(metadata).encode()
                        return data
                    rewrite_archive(archive, modify)
                    with self.assertRaisesRegex(ValueError, "JForex"):
                        release.archive_manifest(archive, target, "0.1.0")

    def package(self, root, label=b"previous bridge"):
        package = icon_tests.AppIconTests().make_package(root)
        provenance = write_bridge(package / "jforex-runtime", label)
        (package / "version.json").write_text(json.dumps({"jforex": {key:value for key,value in provenance.items() if key != "files"}}))
        return package

    def environment(self, root):
        return dict(os.environ, XDG_DATA_HOME=str(root / "user data"), HOME=str(root / "home"), XDG_DESKTOP_DIR=str(root / "no desktop"))

    def test_installer_copies_complete_bridge_and_keeps_sdk_credentials_and_market_data_separate(self):
        with tempfile.TemporaryDirectory(prefix="jforex package ") as temporary:
            root = Path(temporary); package = self.package(root); env = self.environment(root)
            private = root / "user data/cryptodesk/dukascopy-jforex.json"
            private.parent.mkdir(parents=True); private.write_text("private user data")
            subprocess.run([str(package / "install-user.sh"), "--dry-run"], env=env, check=True, capture_output=True)
            installed = root / "user data/cryptodesk-app"
            self.assertFalse(installed.exists())
            subprocess.run([str(package / "install-user.sh")], env=env, check=True, capture_output=True)
            self.assertEqual(len(list((installed / "jforex-runtime").iterdir())), 5)
            self.assertEqual((installed / "jforex-runtime/cryptodesk-jforex.jar").read_bytes(), (package / "jforex-runtime/cryptodesk-jforex.jar").read_bytes())
            self.assertFalse((installed / "jforex-runtime/lib").exists())
            self.assertEqual(private.read_text(), "private user data")

    def test_failed_install_restores_previous_bridge_after_the_new_bridge_has_moved(self):
        with tempfile.TemporaryDirectory(prefix="jforex rollback ") as temporary:
            root = Path(temporary); package = self.package(root); env = self.environment(root)
            subprocess.run([str(package / "install-user.sh")], env=env, check=True, capture_output=True)
            installed = root / "user data/cryptodesk-app"
            previous = (installed / "jforex-runtime/cryptodesk-jforex.jar").read_bytes()
            files, manifest = bridge_files(b"new bridge")
            for name, data in files.items():
                (package / "jforex-runtime" / name).write_bytes(data)
            (package / "version.json").write_text(json.dumps({"jforex": {key:value for key,value in manifest.items() if key != "files"}}))
            fake_bin = root / "fake bin"; fake_bin.mkdir()
            import shlex
            (fake_bin / "python3").write_text('#!/bin/sh\nif [ "$2" = "$JFOREX_INSTALL_TEST_SOURCE" ]; then exec ' + shlex.quote(shutil.which("python3")) + ' "$@"; fi\nexit 73\n')
            (fake_bin / "python3").chmod(0o755)
            failing_env = dict(env, PATH=str(fake_bin) + os.pathsep + os.environ["PATH"], JFOREX_INSTALL_TEST_SOURCE=str(package))
            result = subprocess.run([str(package / "install-user.sh")], env=failing_env, capture_output=True)
            self.assertEqual(result.returncode, 73)
            self.assertEqual((installed / "jforex-runtime/cryptodesk-jforex.jar").read_bytes(), previous)
            self.assertFalse(any(installed.glob(".install.*")))

    def test_new_provenance_requires_bridge_and_incomplete_inventory_never_starts_installing(self):
        for missing in ("jforex-runtime", "jforex-runtime/SDK-ARTIFACTS.json"):
            with self.subTest(missing=missing), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary); package = self.package(root); path = package / missing
                if path.is_dir():
                    shutil.rmtree(path)
                else:
                    path.unlink()
                result = subprocess.run([str(package / "install-user.sh")], env=self.environment(root), capture_output=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(b"JForex", result.stderr)
                self.assertFalse((root / "user data/cryptodesk-app").exists())


if __name__ == "__main__":
    unittest.main()

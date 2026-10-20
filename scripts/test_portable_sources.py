"""Offline regressions for exact Ubuntu corresponding-source collection."""
import hashlib
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

import portable_sources as sources


def sha(data):
    return hashlib.sha256(data).hexdigest()


def source_fixture(name="demo-source", version="2:1.0-1", *, dsc_source=None, dsc_checksum=None):
    stem = name + "_" + version.split(":")[-1]
    files = {stem + ".orig.tar.xz": b"Explicit unit-test source archive\n",
             stem + ".debian.tar.xz": b"Explicit unit-test distribution patches\n",
             stem + ".orig.tar.xz.asc": b"Explicit unit-test upstream signature\n"}
    checksums = "\n".join(f" {dsc_checksum or sha(data)} {len(data)} {filename}" for filename, data in files.items())
    descriptor = ("-----BEGIN PGP SIGNED MESSAGE-----\nHash: SHA256\n\nFormat: 3.0 (quilt)\n"
                  f"Source: {dsc_source or name}\nVersion: {version}\nChecksums-Sha256:\n{checksums}\n"
                  "\n-----BEGIN PGP SIGNATURE-----\nExplicit unit-test signature; not a real signed archive\n-----END PGP SIGNATURE-----\n")
    files[stem + ".dsc"] = descriptor.encode()
    indexed = "\n".join(f" {sha(data)} {len(data)} {filename}" for filename, data in files.items())
    record = f"Package: {name}\nVersion: {version}\nDirectory: pool/main/d/{name}\nChecksums-Sha256:\n{indexed}\n"
    return files, record


class FakeAPT:
    """Models dpkg identities, authenticated-index lookup and source-only fetches."""
    def __init__(self, config_root, *, files=None, record=None):
        self.config_root = config_root
        self.files, self.record = source_fixture()
        if files is not None:
            self.files = files
        if record is not None:
            self.record = record
        self.downloads, self.updates, self.commands = 0, 0, []
        self.failure = None
        self.corrupt_download = False

    def __call__(self, command, *, env=None, cwd=None, timeout=600):
        self.commands.append((command, env, cwd))
        if command[0] == "dpkg-query":
            self.assert_package_query(command)
            package = command[-1]
            return f"{package}\t2:1.0-1+b1\tdemo-source\t2:1.0-1\tinstalled\n"
        if self.failure == command[-1] or self.failure == command[0]:
            raise RuntimeError("Explicit mocked APT command failure")
        config = Path(env["APT_CONFIG"])
        self.assert_isolation(config)
        if command[:2] == ["apt-get", "update"]:
            self.updates += 1
            lists = config.parent / "state/lists"
            (lists / "archive.ubuntu.com_ubuntu_dists_resolute_InRelease").write_text("Explicit test authenticated Release signature metadata\n")
            (lists / "archive.ubuntu.com_ubuntu_dists_resolute_main_source_Sources.lz4").write_text(self.record)
            return "Explicit mocked authenticated index refresh\n"
        if command[:3] == ["apt-cache", "--only-source", "showsrc"]:
            return self.record
        if command[:4] == ["apt-get", "--download-only", "--only-source", "--assume-yes"]:
            assert command[4:] == ["source", "--", "demo-source=2:1.0-1"], command
            self.downloads += 1
            for filename, data in self.files.items():
                (Path(cwd) / filename).write_bytes(data + (b"corruption" if self.corrupt_download else b""))
            return "Explicit mocked source archive download; no extraction\n"
        raise AssertionError(f"Unexpected command {command}")

    @staticmethod
    def assert_package_query(command):
        assert command[:2] == ["dpkg-query", "-W"]
        assert command[-2] == "--"
        for field in ("binary:Package", "Version", "source:Package", "source:Version", "db:Status-Status"):
            assert "${" + field + "}" in command[2], command

    @staticmethod
    def assert_isolation(config):
        content = config.read_text()
        for field in ("Dir::Etc::main", "Dir::Etc::parts", "Dir::State::lists", "Dir::Cache::archives", "Dir::Log"):
            assert field in content
        assert 'APT::Get::AllowUnauthenticated "false"' in content
        assert 'Acquire::AllowInsecureRepositories "false"' in content
        assert 'APT::Update::Error-Mode "any"' in content


def configuration(root):
    root.mkdir()
    parts = root / "sources.list.d"
    parts.mkdir()
    (root / "sources.list").write_text("# Existing configuration remains untouched\n")
    (parts / "ubuntu.sources").write_text("Types: deb\nURIs: https://archive.ubuntu.com/ubuntu/\nSuites: resolute resolute-updates\nComponents: main universe\nSigned-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg\n")
    (parts / "vendor.sources").write_text("Types: deb\nURIs: https://vendor.example.invalid/apt\nSuites: stable\nComponents: main\n")
    return root


class PortableSourcesTests(unittest.TestCase):
    def collect(self, root, apt, destination="output", **options):
        with mock.patch.object(sources, "_run", side_effect=apt):
            return sources.collect_sources(["libdemo:amd64", "libother:amd64", "libdemo:amd64"],
                                           root / destination, root / "cache", apt_config_root=apt.config_root, **options)

    def test_exact_sources_deduplicate_retain_signatures_and_reverify_cached_downloads(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            config = configuration(root / "system apt")
            before = {path: path.read_bytes() for path in config.rglob("*") if path.is_file()}
            apt = FakeAPT(config)
            first = self.collect(root, apt)
            second = self.collect(root, apt, "second")
            self.assertEqual(apt.downloads, 1)
            self.assertEqual(apt.updates, 2)
            self.assertEqual(len(first["sources"]), 1)
            self.assertEqual(len(first["binaryPackages"]), 2)
            self.assertEqual(first["binaryPackages"][0]["binaryVersion"], "2:1.0-1+b1")
            self.assertEqual(first["sources"][0]["version"], "2:1.0-1")
            self.assertEqual(first, second)
            self.assertEqual(before, {path: path.read_bytes() for path in config.rglob("*") if path.is_file()})
            self.assertEqual(json.loads((root / "output/portable-sources.json").read_text()), first)
            source_directory = root / "output" / first["sources"][0]["directory"]
            for filename, data in apt.files.items():
                self.assertEqual((source_directory / filename).read_bytes(), data)
            self.assertEqual((source_directory / "SOURCE-INDEX-RECORD.txt").read_text(), apt.record)
            self.assertEqual(len(first["aptMetadata"]), 2)
            self.assertTrue(any(item["name"].endswith("_InRelease") for item in first["aptMetadata"]))
            self.assertNotIn("vendor.example.invalid", (root / "cache/apt/sources.list.d/ubuntu.sources").read_text())

    def test_legacy_list_and_deb822_conversion_strip_insecure_overrides_and_disabled_repos(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            config = configuration(root / "apt")
            (config / "sources.list").write_text("deb [arch=amd64 trusted=yes signed-by=/usr/share/keyrings/ubuntu-archive-keyring.gpg] http://security.ubuntu.com/ubuntu resolute-security main # trailing comment\n")
            with (config / "sources.list.d/ubuntu.sources").open("a") as stream:
                stream.write("Trusted: yes\nAllow-Insecure: yes\nCheck-Valid-Until: no\n\nTypes: deb\nURIs: https://archive.ubuntu.com/ubuntu/\nSuites: disabled\nComponents: main\nEnabled: no\n")
            text, inputs = sources.ubuntu_source_configuration(config)
            self.assertEqual(text.count("Types: deb-src"), 2)
            self.assertIn("Suites: resolute-security", text)
            for unwanted in ("Trusted", "Allow-Insecure", "Check-Valid-Until", "disabled", "vendor", "trusted=yes", "arch=amd64"):
                self.assertNotIn(unwanted, text)
            self.assertEqual(len(inputs), 2)

    def test_official_github_runner_mirror_file_resolves_to_public_archive_and_retains_identity(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            config = configuration(root / "apt")
            mirrors = config / "apt-mirrors.txt"
            mirrors.write_text("http://azure.archive.ubuntu.com/ubuntu/\tpriority:1\nhttps://archive.ubuntu.com/ubuntu/\tpriority:2\nhttps://security.ubuntu.com/ubuntu/\tpriority:3\n")
            ubuntu = config / "sources.list.d/ubuntu.sources"
            ubuntu.write_text(ubuntu.read_text().replace("https://archive.ubuntu.com/ubuntu/", "mirror+file:" + str(mirrors)))
            text, inputs = sources.ubuntu_source_configuration(config)
            self.assertIn("URIs: https://archive.ubuntu.com/ubuntu/", text)
            self.assertNotIn("mirror+file:", text)
            identity = next(item for item in inputs if item.get("kind") == "mirror-list")
            self.assertEqual(identity["sha256"], sources.digest_file(mirrors))
            self.assertEqual(identity["originalURI"], "mirror+file:" + str(mirrors))
            self.assertEqual(identity["resolvedURI"], "https://archive.ubuntu.com/ubuntu/")
            self.assertEqual(len(identity["alternatives"]), 3)
            apt = FakeAPT(config)
            report = self.collect(root, apt)
            self.assertIn(identity, report["repositoryDefinitions"])

    def test_mirror_file_rejects_untrusted_path_host_credentials_key_and_symlink(self):
        for mode in ("outside path", "traversal", "foreign host", "credentials", "foreign key", "extra key", "symlink", "oversized", "empty"):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                config = configuration(root / "apt")
                mirrors = config / "apt-mirrors.txt"
                mirrors.write_text("https://archive.ubuntu.com/ubuntu/\tpriority:1\n")
                ubuntu = config / "sources.list.d/ubuntu.sources"
                uri = "mirror+file:" + str(mirrors)
                if mode == "outside path":
                    uri = "mirror+file:/tmp/untrusted-mirrors.txt"
                elif mode == "traversal":
                    uri = "mirror+file:" + str(config) + "/../apt/apt-mirrors.txt"
                elif mode == "foreign host":
                    mirrors.write_text("https://vendor.invalid/ubuntu/\tpriority:1\n")
                elif mode == "credentials":
                    mirrors.write_text("https://user:secret@archive.ubuntu.com/ubuntu/\tpriority:1\n")
                elif mode == "foreign key":
                    content = ubuntu.read_text().replace("/usr/share/keyrings/ubuntu-archive-keyring.gpg", "/untrusted/key.gpg")
                    ubuntu.write_text(content)
                elif mode == "extra key":
                    ubuntu.write_text(ubuntu.read_text().replace("ubuntu-archive-keyring.gpg", "ubuntu-archive-keyring.gpg /untrusted/key.gpg"))
                elif mode == "symlink":
                    original = root / "outside.txt"; original.write_text(mirrors.read_text())
                    mirrors.unlink(); mirrors.symlink_to(original)
                elif mode == "oversized":
                    mirrors.write_text("#" * 65537)
                elif mode == "empty":
                    mirrors.write_text("# no entries\n")
                ubuntu.write_text(ubuntu.read_text().replace("https://archive.ubuntu.com/ubuntu/", uri))
                with self.assertRaises((ValueError, RuntimeError)):
                    sources.ubuntu_source_configuration(config)

    def test_no_matching_archive_and_invalid_package_names_fail_before_network(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            root.joinpath("sources.list.d").mkdir()
            with self.assertRaisesRegex(RuntimeError, "No enabled Ubuntu"):
                sources.ubuntu_source_configuration(root)
            for packages in ([], ["--install"], ["bad;command"], ["../escape"], ["demo", 1], "demo"):
                with self.subTest(packages=packages), mock.patch.object(sources, "_run") as run:
                    with self.assertRaises(ValueError):
                        sources.installed_packages(packages)
                    run.assert_not_called()

    def test_ambiguous_architecture_and_uninstalled_or_invalid_dpkg_identity_rejected(self):
        for output in ("demo:amd64\t1.0\tdemo\t1.0\tinstalled\ndemo:i386\t1.0\tdemo\t1.0\tinstalled\n",
                       "demo\t1.0\tdemo\t1.0\tconfig-files\n", "demo\t1.0\t../escape\t1.0\tinstalled\n"):
            with self.subTest(output=output), mock.patch.object(sources, "_run", return_value=output):
                with self.assertRaises(RuntimeError):
                    sources.installed_packages(["demo"])

    def test_same_size_corrupt_cache_is_rejected_without_redownloading_or_publishing(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            apt = FakeAPT(configuration(root / "apt"))
            report = self.collect(root, apt)
            entry = report["sources"][0]
            path = root / "cache/downloads" / Path(entry["directory"]).name / entry["files"][0]["name"]
            data = bytearray(path.read_bytes()); data[0] ^= 1; path.write_bytes(data)
            with self.assertRaisesRegex(RuntimeError, "SHA-256/size"):
                self.collect(root, apt, "second")
            self.assertEqual(apt.downloads, 1)
            self.assertFalse((root / "second").exists())

    def test_missing_cache_file_is_downloaded_and_complete_inventory_reverified(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            apt = FakeAPT(configuration(root / "apt"))
            report = self.collect(root, apt)
            entry = report["sources"][0]
            (root / "cache/downloads" / Path(entry["directory"]).name / entry["files"][0]["name"]).unlink()
            self.collect(root, apt, "second")
            self.assertEqual(apt.downloads, 2)

    def test_bad_download_dsc_identity_and_dsc_checksum_never_publish(self):
        for mode in ("download hash", "descriptor identity", "descriptor checksum"):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                options = {"dsc_source": "wrong-source"} if mode == "descriptor identity" else {"dsc_checksum": "0" * 64} if mode == "descriptor checksum" else {}
                files, record = source_fixture(**options)
                apt = FakeAPT(configuration(root / "apt"), files=files, record=record)
                apt.corrupt_download = mode == "download hash"
                with self.assertRaises(RuntimeError):
                    self.collect(root, apt)
                self.assertFalse((root / "output").exists())
                self.assertEqual(list(root.glob("cache/downloads/*/*")), [])

    def test_unavailable_exact_version_and_conflicting_signed_records_fail_before_download(self):
        for mode in ("unavailable", "conflicting"):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                apt = FakeAPT(configuration(root / "apt"))
                if mode == "unavailable":
                    apt.record = apt.record.replace("Version: 2:1.0-1", "Version: 2:1.0-2")
                else:
                    apt.record += "\n" + apt.record.replace(sha(next(iter(apt.files.values()))), "0" * 64)
                with self.assertRaisesRegex(RuntimeError, "unavailable|Conflicting"):
                    self.collect(root, apt)
                self.assertEqual(apt.downloads, 0)
                self.assertFalse((root / "output").exists())

    def test_source_limit_is_checked_before_any_archive_download(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            apt = FakeAPT(configuration(root / "apt"))
            with self.assertRaisesRegex(RuntimeError, "limit; no source archives downloaded"):
                self.collect(root, apt, max_bytes=10)
            self.assertEqual(apt.downloads, 0)
            self.assertFalse((root / "output").exists())

    def test_apt_failures_cannot_produce_a_partial_source_offer(self):
        for failure in ("update", "apt-cache", "source"):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                apt = FakeAPT(configuration(root / "apt"))
                apt.failure = failure
                original_call = apt.__call__
                def run(command, **options):
                    if failure == "source" and "source" in command:
                        raise RuntimeError("Explicit source download failure")
                    return original_call(command, **options)
                with mock.patch.object(sources, "_run", side_effect=run):
                    with self.assertRaises(RuntimeError):
                        sources.collect_sources(["libdemo:amd64"], root / "output", root / "cache", apt_config_root=apt.config_root)
                self.assertFalse((root / "output").exists())

    def test_existing_destination_and_nested_cache_are_preserved(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            destination = root / "output"
            destination.mkdir(); (destination / "saved.txt").write_text("keep")
            with mock.patch.object(sources, "_run") as run:
                with self.assertRaises(ValueError):
                    sources.collect_sources(["demo"], destination, root / "cache")
                with self.assertRaises(ValueError):
                    sources.collect_sources(["demo"], root / "new", root / "new/cache")
                run.assert_not_called()
            self.assertEqual((destination / "saved.txt").read_text(), "keep")

    def test_unsafe_checksums_and_descriptors_are_rejected(self):
        for name in ("../escape", "/absolute", "a/b", "file;command", "-option"):
            with self.subTest(name=name), self.assertRaises(ValueError):
                sources.checksums_sha256(f"{'0' * 64} 10 {name}")
        for text in ("Package: first\nPackage: duplicate\n", " continuation without field\n"):
            with self.assertRaises(ValueError):
                sources.control_records(text)
        with self.assertRaises(ValueError):
            sources._dsc_payload("-----BEGIN PGP SIGNED MESSAGE-----\nHash: SHA256\n\nSource: missing-signature\n")


if __name__ == "__main__":
    unittest.main()

"""Check packaging boundaries without a network request or a Windows machine."""
import importlib.util
import json
import os
from pathlib import Path
import shutil
import struct
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch
import zipfile
import windows_webview_language

spec = importlib.util.spec_from_file_location("windows_build", Path(__file__).with_name("windows_build.py"))
build = importlib.util.module_from_spec(spec)
spec.loader.exec_module(build)


class WindowsPackagingTests(unittest.TestCase):
    def test_complete_producer_keeps_release_name_and_override_in_package_metadata(self):
        for override in (True, False):
            with self.subTest(environment_override=override):
                self.check_complete_producer(override)

    def test_wrong_or_missing_frontend_version_prevents_packaging(self):
        for compiled_version in (None, "2.3.3"):
            with self.subTest(compiled_version=compiled_version):
                self.check_complete_producer(True, compiled_version)

    def check_complete_producer(self, override, compiled_version="2.3.4"):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            files = {"VERSION": "0.1.0" if override else "2.3.4", "wails.json": json.dumps({"info": {"productVersion": "0.1.0"}}),
                     "scripts/webview2-runtime.json": "{}", "APP_README.md": "readme", "LICENSE": "license", "STATUS.md": "status",
                     "docs/WINDOWS_PORTABLE.md": "windows", "docs/THIRD_PARTY_NOTICES.md": "notices", "docs/licenses/example.txt": "license"}
            for name, content in files.items():
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(content)
            executable = bytearray(160)
            executable[:2] = b"MZ"
            struct.pack_into("<I", executable, 60, 64)
            executable[64:68] = b"PE\0\0"
            for offset, value in [(68, 0x8664), (88, 0x20B), (156, 2)]:
                struct.pack_into("<H", executable, offset, value)

            def run(command, **kwargs):
                if "build" in command and "windows/amd64" in command:
                    source = Path(kwargs["cwd"])
                    self.assertEqual(json.loads((source / "wails.json").read_text())["info"]["productVersion"], "2.3.4")
                    self.assertEqual(kwargs["env"]["CRYPTODESK_VERSION"], "2.3.4")
                    if compiled_version is not None:
                        metadata = source / "frontend/dist/app-version.json"
                        metadata.parent.mkdir(parents=True)
                        metadata.write_text(json.dumps({"version": compiled_version, "tag": f"v{compiled_version}"}))
                    binary = source / "build/bin/cryptodesk.exe"
                    binary.parent.mkdir(parents=True)
                    binary.write_bytes(executable)
                if any(str(value).endswith("webview2_runtime.py") for value in command):
                    runtime = Path(command[command.index("--destination") + 1])
                    (runtime / "Locales").mkdir(parents=True)
                    for name in [*build.ENGLISH_LOCALE_PAKS, "fr.pak"]:
                        (runtime / "Locales" / name).write_bytes(b"runtime")
                    (runtime / "WEBVIEW2-PROVENANCE.json").write_text("{}")
                    (runtime / "msedgewebview2.exe").write_bytes(executable)
                return subprocess.CompletedProcess(command, 0)

            environment = dict(os.environ)
            environment.pop("CRYPTODESK_VERSION", None)
            if override:
                environment["CRYPTODESK_VERSION"] = "2.3.4"
            with patch.dict(os.environ, environment, clear=True), \
                 patch.object(build.shutil, "which", return_value="/fixture/tool"), \
                 patch.object(build, "source_provenance", return_value={"gitRevision": "a" * 40, "workingTreeModified": False}), \
                 patch.object(build.subprocess, "run", side_effect=run), \
                 patch.object(build.subprocess, "check_output", return_value="tool version\n"), \
                 patch.object(windows_webview_language, "prepare_language_overlay", return_value={"goFlag": "-overlay=fixture", "manifest": {}}):
                if compiled_version != "2.3.4":
                    with self.assertRaises((ValueError, FileNotFoundError)):
                        build.run(root)
                    self.assertFalse((root / "cryptodesk-2.3.4-windows-amd64.zip").exists())
                    return
                build.run(root)
            archive = root / "cryptodesk-2.3.4-windows-amd64.zip"
            self.assertTrue(archive.is_file())
            self.assertFalse((root / "STATUS.md.zip").exists())
            with zipfile.ZipFile(archive) as result:
                manifest = json.loads(result.read("cryptodesk-2.3.4-windows-amd64/version.json"))
                self.assertEqual(manifest["version"], "2.3.4")
                self.assertFalse(manifest["workingTreeModified"])
                self.assertEqual(result.read("cryptodesk-2.3.4-windows-amd64/STATUS.md"), b"status")
            self.assertEqual(json.loads((root / "wails.json").read_text())["info"]["productVersion"], "0.1.0")
            self.assertIn(archive.name, (root / "cryptodesk-Windows-SHA256SUMS").read_text())

    def test_language_build_flags_do_not_leak_into_module_disabled_probes(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            go = root / "fake-go"
            go.write_text(f"#!{sys.executable}\nimport json,os,sys\nprint(json.dumps({{'args':sys.argv[1:], 'flags':os.environ.get('GOFLAGS')}}))\n")
            go.chmod(0o755)
            with patch.object(build.shutil, "which", return_value=str(go)):
                compiler = build.language_compiler(root, "-modfile=temporary.mod -overlay=temporary.json")
            env = dict(os.environ, GOFLAGS="-trimpath", GO111MODULE="off")
            probe = json.loads(subprocess.check_output([str(compiler), "env", "GOVERSION"], env=env, text=True))
            self.assertEqual(probe, {"args": ["env", "GOVERSION"], "flags": "-trimpath"})
            compiled = json.loads(subprocess.check_output([str(compiler), "build", "-o", "app.exe"], env=env, text=True))
            self.assertEqual(compiled["flags"], "-trimpath -modfile=temporary.mod -overlay=temporary.json")
            self.assertEqual(env["GOFLAGS"], "-trimpath")

    def test_english_package_keeps_shared_resources_and_full_cache_intact(self):
        with tempfile.TemporaryDirectory() as temporary:
            base = Path(temporary)
            cache = base / "cache"
            (cache / "Locales").mkdir(parents=True)
            for name in [*build.ENGLISH_LOCALE_PAKS, "fr.pak", "copilot_overlay_strings_ja.pak"]:
                (cache / "Locales" / name).write_bytes(name.encode())
            (cache / "Locales/NOTICE.txt").write_text("keep locale notices")
            (cache / "resources.pak").write_bytes(b"shared resources")
            (cache / "msedge.dll").write_bytes(b"binary")
            (cache / "WEBVIEW2-LICENSE.html").write_text("license")
            source = {"inventory_sha256": "full-source-hash", "file_count": 10}
            (cache / "WEBVIEW2-PROVENANCE.json").write_text(json.dumps(source))
            staged = base / "staging"
            shutil.copytree(cache, staged)
            profile = build.keep_english_locales(staged)
            self.assertEqual({p.name for p in (staged / "Locales").glob("*.pak")}, build.ENGLISH_LOCALE_PAKS)
            self.assertEqual((staged / "resources.pak").read_bytes(), b"shared resources")
            self.assertTrue((staged / "Locales/NOTICE.txt").exists())
            self.assertTrue((staged / "WEBVIEW2-LICENSE.html").exists())
            self.assertTrue((cache / "Locales/fr.pak").exists())
            self.assertEqual(json.loads((cache / "WEBVIEW2-PROVENANCE.json").read_text()), source)
            provenance = json.loads((staged / "WEBVIEW2-PROVENANCE.json").read_text())
            self.assertEqual(provenance["sourceRuntime"], source)
            self.assertEqual(provenance["packagedRuntime"], profile)
            self.assertNotEqual(profile["inventory_sha256"], source["inventory_sha256"])
            archive = base / "english.zip"
            build.package_zip(staged, archive)
            with zipfile.ZipFile(archive) as result:
                self.assertNotIn("staging/Locales/fr.pak", result.namelist())
                self.assertIn("staging/Locales/en-US.pak", result.namelist())
                self.assertIn("staging/resources.pak", result.namelist())

    def test_missing_english_pack_fails_before_deleting_any_locale(self):
        with tempfile.TemporaryDirectory() as temporary:
            runtime = Path(temporary)
            (runtime / "Locales").mkdir()
            (runtime / "Locales/fr.pak").write_bytes(b"French")
            with self.assertRaisesRegex(ValueError, "missing the English locale"):
                build.keep_english_locales(runtime)
            self.assertTrue((runtime / "Locales/fr.pak").exists())

    def test_snapshot_includes_unsaved_sources_but_excludes_build_outputs_and_private_state(self):
        with tempfile.TemporaryDirectory() as temporary:
            base = Path(temporary)
            root = base / "working tree"
            root.mkdir()
            files = {
                "main.go": "current uncommitted implementation",
                "frontend/src/App.tsx": "uncommitted UI",
                "frontend/wailsjs/go/main/App.js": "user binding edits",
                "examples/starter.json": "{}",
                "build/linux/icon.svg": "svg",
                ".tools/private": "private",
                ".git/config": "private",
                ".env": "private",
                "workspace.sqlite": "private workspace",
                "frontend/node_modules/package/file": "generated",
                "frontend/dist/index.html": "stale",
                "build/bin/cryptodesk": "old executable",
                "cryptodesk-0.1.0-windows-amd64.zip": "old ZIP",
                "release/old/file": "stale release",
            }
            for name, text in files.items():
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(text)
            build.source_snapshot(root, base / "copy")
            actual = {str(path.relative_to(base / "copy")) for path in (base / "copy").rglob("*") if path.is_file()}
            self.assertEqual(actual, {"main.go", "frontend/src/App.tsx", "frontend/wailsjs/go/main/App.js", "examples/starter.json", "build/linux/icon.svg"})
            self.assertEqual((base / "copy/main.go").read_text(), "current uncommitted implementation")
            self.assertEqual((root / "frontend/dist/index.html").read_text(), "stale")

    def test_only_x64_windows_gui_pe_is_publishable(self):
        with tempfile.TemporaryDirectory() as temporary:
            exe = Path(temporary) / "cryptodesk.exe"
            data = bytearray(160)
            data[:2] = b"MZ"
            struct.pack_into("<I", data, 60, 64)
            data[64:68] = b"PE\0\0"
            struct.pack_into("<H", data, 68, 0x8664)
            struct.pack_into("<H", data, 88, 0x20B)
            struct.pack_into("<H", data, 156, 2)
            exe.write_bytes(data)
            build.validate_executable(exe)
            for offset, value in [(68, 0xAA64), (156, 3)]:
                invalid = bytearray(data)
                struct.pack_into("<H", invalid, offset, value)
                exe.write_bytes(invalid)
                with self.assertRaises(ValueError):
                    build.validate_executable(exe)
            exe.write_bytes(b"ELF Linux binary")
            with self.assertRaises(ValueError):
                build.validate_executable(exe)

    def test_archive_retains_runtime_subdirectories_and_rejects_symlinks(self):
        with tempfile.TemporaryDirectory() as temporary:
            base = Path(temporary)
            package = base / "CryptoDesk"
            (package / "WebView2/locales").mkdir(parents=True)
            (package / "WebView2/locales/en-US.pak").write_bytes(b"runtime")
            (package / "portable.marker").write_text("portable")
            archive = base / "portable.zip"
            build.package_zip(package, archive)
            with zipfile.ZipFile(archive) as result:
                self.assertIsNone(result.testzip())
                self.assertEqual(result.read("CryptoDesk/WebView2/locales/en-US.pak"), b"runtime")
            (package / "linked-user-data").symlink_to(base)
            with self.assertRaises(ValueError):
                build.package_zip(package, archive)


if __name__ == "__main__":
    unittest.main()

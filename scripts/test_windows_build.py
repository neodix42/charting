"""Check packaging boundaries without a network request or a Windows machine."""
import importlib.util
from pathlib import Path
import struct
import tempfile
import unittest
import zipfile

spec = importlib.util.spec_from_file_location("windows_build", Path(__file__).with_name("windows_build.py"))
build = importlib.util.module_from_spec(spec)
spec.loader.exec_module(build)


class WindowsPackagingTests(unittest.TestCase):
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

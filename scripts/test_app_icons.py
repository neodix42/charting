"""Check native icon consistency and the per-user desktop integration."""
import importlib.util
import os
from pathlib import Path
import shutil
import struct
import subprocess
import tempfile
import unittest
import zlib
import time

spec = importlib.util.spec_from_file_location("app_icons", Path(__file__).with_name("app_icons.py"))
icons = importlib.util.module_from_spec(spec)
spec.loader.exec_module(icons)


def decode_png(content):
    if content[:8] != b"\x89PNG\r\n\x1a\n":
        raise ValueError("Not PNG")
    position, compressed = 8, bytearray()
    width = height = 0
    while position < len(content):
        length = struct.unpack_from(">I", content, position)[0]
        kind = content[position + 4:position + 8]
        data = content[position + 8:position + 8 + length]
        crc = struct.unpack_from(">I", content, position + 8 + length)[0]
        if crc != zlib.crc32(kind + data):
            raise ValueError("PNG CRC mismatch")
        if kind == b"IHDR":
            width, height, depth, color, *_ = struct.unpack(">IIBBBBB", data)
            if depth != 8 or color != 6:
                raise ValueError("Expected RGBA PNG")
        elif kind == b"IDAT":
            compressed.extend(data)
        position += length + 12
    raw = zlib.decompress(compressed)
    if len(raw) != height * (width * 4 + 1):
        raise ValueError("PNG dimensions mismatch")
    rows = []
    for row in range(height):
        start = row * (width * 4 + 1)
        if raw[start] != 0:
            raise ValueError("Expected PNG filter None")
        rows.append(raw[start + 1:start + width * 4 + 1])
    return width, height, b"".join(rows)


class AppIconTests(unittest.TestCase):
    def make_package(self, root):
        package = root / "package"
        package.mkdir()
        for name in ("cryptodesk.svg", "cryptodesk.png", "cryptodesk.desktop"):
            shutil.copy2(icons.ROOT / "build/linux" / name, package / name)
        shutil.copytree(icons.ROOT / "build/linux/icons", package / "icons")
        shutil.copy2(icons.ROOT / "scripts/install-user.sh", package / "install-user.sh")
        (package / "cryptodesk").write_bytes(b"preserved binary")
        return package

    def test_generated_native_assets_match_the_frontend_vector(self):
        for relative, expected in icons.assets().items():
            with self.subTest(path=str(relative)):
                self.assertEqual((icons.ROOT / relative).read_bytes(), expected,
                                 "Run python3 scripts/app_icons.py after editing the SVG")

    def test_native_png_contains_gold_diamond_and_transparent_corners(self):
        width, height, pixels = decode_png((icons.ROOT / "build/appicon.png").read_bytes())
        self.assertEqual((width, height), (512, 512))

        def pixel(x, y):
            index = (y * width + x) * 4
            return pixels[index:index + 4]

        self.assertEqual(pixel(0, 0), b"\0\0\0\0")
        self.assertEqual(pixel(256, 256), bytes.fromhex("e7ad51ff"))
        self.assertEqual(pixel(256, 100), bytes.fromhex("e7ad51ff"))
        self.assertEqual(pixel(256, 160), bytes.fromhex("111820ff"))
        self.assertEqual(pixel(40, 256), bytes.fromhex("111820ff"))
        self.assertTrue(any(0 < pixels[offset] < 255 for offset in range(3, len(pixels), 4)))

    def test_linux_window_icon_fits_x11_limits_and_packaged_icons_cover_desktop_sizes(self):
        main = (icons.ROOT / "main.go").read_text()
        self.assertIn('//go:embed build/linux/window-icon.png', main)
        width, height, pixels = decode_png((icons.ROOT / "build/linux/window-icon.png").read_bytes())
        self.assertEqual((width, height), (128, 128))
        self.assertLess(2 + width * height, 65500)
        center = (height // 2 * width + width // 2) * 4
        self.assertEqual(pixels[center:center + 4], bytes.fromhex("e7ad51ff"))
        for size in icons.LINUX_ICON_SIZES:
            image = icons.ROOT / f"build/linux/icons/hicolor/{size}x{size}/apps/cryptodesk.png"
            self.assertEqual(decode_png(image.read_bytes())[:2], (size, size))

    def test_windows_ico_has_taskbar_explorer_and_high_dpi_sizes(self):
        content = (icons.ROOT / "build/windows/icon.ico").read_bytes()
        reserved, kind, count = struct.unpack_from("<HHH", content)
        self.assertEqual((reserved, kind, count), (0, 1, len(icons.ICON_SIZES)))
        for index, size in enumerate(icons.ICON_SIZES):
            with self.subTest(size=size):
                width, height, palette, reserved, planes, bits, length, offset = struct.unpack_from("<BBBBHHII", content, 6 + index * 16)
                self.assertEqual((width or 256, height or 256, palette, reserved, planes, bits), (size, size, 0, 0, 1, 32))
                self.assertGreaterEqual(offset, 6 + count * 16)
                actual_width, actual_height, pixels = decode_png(content[offset:offset + length])
                self.assertEqual((actual_width, actual_height), (size, size))
                center = ((size // 2) * size + size // 2) * 4
                red, green, blue, alpha = pixels[center:center + 4]
                self.assertGreater(red, 200)
                self.assertGreater(green, 140)
                self.assertLess(blue, 100)
                self.assertEqual(alpha, 255)

    def test_launcher_matches_window_identity(self):
        desktop = (icons.ROOT / "build/linux/cryptodesk.desktop").read_text()
        fields = dict(line.split("=", 1) for line in desktop.splitlines() if "=" in line)
        self.assertEqual(fields["Icon"], "cryptodesk")
        self.assertEqual(fields["StartupWMClass"], "cryptodesk")
        self.assertEqual(fields["Exec"], "cryptodesk")
        self.assertIn('ProgramName: "cryptodesk"', (icons.ROOT / "main.go").read_text())
        if shutil.which("desktop-file-validate"):
            subprocess.run(["desktop-file-validate", str(icons.ROOT / "build/linux/cryptodesk.desktop")], check=True)

    def test_installer_registers_only_this_app_inside_supplied_user_data_home(self):
        with tempfile.TemporaryDirectory(prefix="cryptodesk-icon-install-") as temporary:
            root = Path(temporary)
            package = self.make_package(root)
            installer = package / "install-user.sh"
            data_home = root / 'user app data 100%'
            workspace = data_home / "cryptodesk/workspace.sqlite"
            workspace.parent.mkdir(parents=True)
            workspace.write_bytes(b"existing user workspace")
            env = dict(os.environ, XDG_DATA_HOME=str(data_home), HOME=str(root / "home"), XDG_DESKTOP_DIR=str(root / "no desktop"))
            subprocess.run([str(installer), "--dry-run"], env=env, check=True, capture_output=True)
            self.assertFalse((data_home / "applications").exists())
            subprocess.run([str(installer)], env=env, check=True, capture_output=True)
            installed = data_home / "cryptodesk-app/cryptodesk"
            self.assertEqual(installed.read_bytes(), b"preserved binary")
            self.assertTrue(installed.stat().st_mode & 0o111)
            self.assertEqual((data_home / "icons/hicolor/scalable/apps/cryptodesk.svg").read_bytes(), (icons.ROOT / icons.SOURCE).read_bytes())
            self.assertEqual((data_home / "cryptodesk-app/cryptodesk.png").read_bytes(), (package / "cryptodesk.png").read_bytes())
            desktop = (data_home / "applications/cryptodesk.desktop").read_text()
            self.assertIn(f'Exec="/usr/bin/env" "{str(installed).replace("%", "%%")}"', desktop)
            self.assertIn(f"Icon={data_home}/cryptodesk-app/cryptodesk.png\n", desktop)
            self.assertIn("StartupWMClass=cryptodesk\n", desktop)
            self.assertEqual(workspace.read_bytes(), b"existing user workspace")
            for size in icons.LINUX_ICON_SIZES:
                self.assertEqual((data_home / f"icons/hicolor/{size}x{size}/apps/cryptodesk.png").read_bytes(),
                                 (package / f"icons/hicolor/{size}x{size}/apps/cryptodesk.png").read_bytes())
            self.assertFalse(any((data_home / "cryptodesk-app").glob(".install.*")))
            subprocess.run([str(installer)], env=env, check=True, capture_output=True)
            self.assertEqual(workspace.read_bytes(), b"existing user workspace")

    def test_legacy_portable_installer_keeps_launcher_runtime_licenses_and_sources_together(self):
        with tempfile.TemporaryDirectory(prefix="cryptodesk-runtime-install-") as temporary:
            root = Path(temporary)
            package = self.make_package(root)
            (package / "cryptodesk").write_text('#!/bin/sh\ncd -- "$(dirname -- "$0")"\nexec ./runtime/usr/bin/cryptodesk "$@"\n')
            binary = package / "runtime/usr/bin/cryptodesk"
            binary.parent.mkdir(parents=True)
            binary.write_text('#!/bin/sh\nprintf "bundled-runtime\\n"\n')
            binary.chmod(0o755)
            for name in ("sources/pinned.tar.xz", "licenses/COPYING"):
                path = package / "runtime" / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(name)
            data_home = root / "app data"
            env = dict(os.environ, XDG_DATA_HOME=str(data_home), HOME=str(root / "home"), XDG_DESKTOP_DIR=str(root / "no desktop"))
            subprocess.run([str(package / "install-user.sh")], env=env, check=True, capture_output=True)
            installed = data_home / "cryptodesk-app"
            shutil.rmtree(package)
            result = subprocess.run([str(installed / "cryptodesk")], env=env, check=True, capture_output=True, text=True)
            self.assertEqual(result.stdout, "bundled-runtime\n")
            self.assertTrue((installed / "runtime/usr/bin/cryptodesk").stat().st_mode & 0o111)
            self.assertEqual((installed / "runtime/sources/pinned.tar.xz").read_text(), "sources/pinned.tar.xz")
            self.assertEqual((installed / "runtime/licenses/COPYING").read_text(), "licenses/COPYING")

    def test_upgrading_a_running_binary_preserves_its_process_and_unrelated_user_files(self):
        with tempfile.TemporaryDirectory(prefix="cryptodesk-atomic-install-") as temporary:
            root = Path(temporary)
            package = self.make_package(root)
            shutil.copy2("/bin/sh", package / "cryptodesk")
            data_home = root / "user data"
            env = dict(os.environ, XDG_DATA_HOME=str(data_home), HOME=str(root / "home"), XDG_DESKTOP_DIR=str(root / "no desktop"))
            subprocess.run([str(package / "install-user.sh")], env=env, check=True, capture_output=True)
            installed = data_home / "cryptodesk-app"
            preserved = installed / "my-notes.txt"
            preserved.write_text("user notes")
            process = subprocess.Popen([str(installed / "cryptodesk"), "-c", "read icon_test_line"], stdin=subprocess.PIPE)
            try:
                shutil.copy2(shutil.which("true"), package / "cryptodesk")
                subprocess.run([str(package / "install-user.sh")], env=env, check=True, capture_output=True)
                self.assertIsNone(process.poll())
                self.assertEqual((installed / "cryptodesk").read_bytes(), (package / "cryptodesk").read_bytes())
                self.assertEqual(preserved.read_text(), "user notes")
            finally:
                process.terminate()
                process.wait(timeout=5)
                process.stdin.close()

    def test_desktop_launcher_handles_shell_characters_and_percent_in_install_path(self):
        if not shutil.which("gio"):
            self.skipTest("GIO launcher unavailable")
        with tempfile.TemporaryDirectory(prefix="cryptodesk-desktop-escape-") as temporary:
            root = Path(temporary)
            package = self.make_package(root)
            (package / "cryptodesk").write_text('#!/bin/sh\nprintf "launched\\n" > "$CRYPTO_ICON_TEST_RECORD"\n')
            data_home = root / 'app data 100% $literal `literal` "quote" \\slash'
            record = root / "launch-record"
            env = dict(os.environ, XDG_DATA_HOME=str(data_home), HOME=str(root / "home"), XDG_DESKTOP_DIR=str(root / "no desktop"), CRYPTO_ICON_TEST_RECORD=str(record))
            subprocess.run([str(package / "install-user.sh")], env=env, check=True, capture_output=True)
            desktop = data_home / "applications/cryptodesk.desktop"
            if shutil.which("desktop-file-validate"):
                subprocess.run(["desktop-file-validate", str(desktop)], check=True, capture_output=True)
            subprocess.run(["gio", "launch", str(desktop)], env=env, check=True, capture_output=True)
            deadline = time.monotonic() + 3
            while not record.exists() and time.monotonic() < deadline:
                time.sleep(.01)
            self.assertEqual(record.read_text(), "launched\n")

    def test_incomplete_package_and_invalid_options_do_not_create_an_installation(self):
        with tempfile.TemporaryDirectory(prefix="cryptodesk-missing-icon-") as temporary:
            root = Path(temporary)
            package = self.make_package(root)
            (package / "cryptodesk.png").unlink()
            data_home = root / "user data"
            env = dict(os.environ, XDG_DATA_HOME=str(data_home), HOME=str(root / "home"), XDG_DESKTOP_DIR=str(root / "no desktop"))
            for arguments in ([], ["--dry-run"], ["--dry-run", "extra"]):
                result = subprocess.run([str(package / "install-user.sh"), *arguments], env=env, capture_output=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(data_home.exists())

    def test_existing_desktop_directory_gets_same_executable_shortcut_and_only_its_trust_metadata(self):
        with tempfile.TemporaryDirectory(prefix="cryptodesk-shortcut-") as temporary:
            root = Path(temporary)
            package = self.make_package(root)
            desktop_dir = root / "My Desktop"
            desktop_dir.mkdir()
            data_home = root / "user data"
            fake_bin = root / "test commands"
            fake_bin.mkdir()
            record = root / "trust record"
            gio = fake_bin / "gio"
            gio.write_text('#!/bin/sh\nprintf "%s\\n" "$@" >> "$CRYPTO_ICON_TEST_RECORD"\n')
            gio.chmod(0o755)
            env = dict(os.environ, XDG_DATA_HOME=str(data_home), HOME=str(root / "home"), XDG_DESKTOP_DIR=str(desktop_dir),
                       CRYPTO_ICON_TEST_RECORD=str(record), PATH=str(fake_bin) + os.pathsep + os.environ["PATH"])
            subprocess.run([str(package / "install-user.sh"), "--dry-run"], env=env, check=True, capture_output=True)
            self.assertEqual(list(desktop_dir.iterdir()), [])
            self.assertFalse(record.exists())
            subprocess.run([str(package / "install-user.sh")], env=env, check=True, capture_output=True)
            shortcut = desktop_dir / "cryptodesk.desktop"
            registered = data_home / "applications/cryptodesk.desktop"
            self.assertEqual(shortcut.read_bytes(), registered.read_bytes())
            self.assertTrue(shortcut.stat().st_mode & 0o100)
            self.assertEqual(record.read_text().splitlines(), ["set", "--type", "string", str(shortcut), "metadata::trusted", "true"])
            shortcut.write_text("Preserve this customized desktop launcher\n")
            record.unlink()
            subprocess.run([str(package / "install-user.sh")], env=env, check=True, capture_output=True)
            self.assertEqual(shortcut.read_text(), "Preserve this customized desktop launcher\n")
            self.assertFalse(record.exists())

    def test_failed_portable_upgrade_restores_previous_runtime_and_preserves_unrelated_temporary_file(self):
        with tempfile.TemporaryDirectory(prefix="cryptodesk-rollback-install-") as temporary:
            root = Path(temporary)
            package = self.make_package(root)
            binary = package / "runtime/usr/bin/cryptodesk"
            binary.parent.mkdir(parents=True)
            binary.write_text("previous runtime")
            binary.chmod(0o755)
            screener = package / "screener-runtime"
            screener.mkdir()
            for name in ("worker.cjs", "node", "package.json", "package-lock.json", "SCREENER-PROVENANCE.json"):
                (screener / name).write_text("previous downloader")
            (screener / "node").chmod(0o755)
            data_home = root / "app data"
            env = dict(os.environ, XDG_DATA_HOME=str(data_home), HOME=str(root / "home"), XDG_DESKTOP_DIR=str(root / "no desktop"))
            unrelated = data_home / "applications/cryptodesk.desktop.tmp"
            unrelated.parent.mkdir(parents=True)
            unrelated.write_text("unrelated temporary file")
            downloaded = data_home / "cryptodesk/screener/btcusd.csv"
            downloaded.parent.mkdir(parents=True)
            downloaded.write_text("saved historical prices")
            subprocess.run([str(package / "install-user.sh")], env=env, check=True, capture_output=True)
            self.assertEqual(unrelated.read_text(), "unrelated temporary file")
            binary.write_text("new runtime")
            (screener / "worker.cjs").write_text("new downloader")
            fake_bin = root / "failing commands"
            fake_bin.mkdir()
            python = fake_bin / "python3"
            python.write_text('#!/bin/sh\nexit 73\n')
            python.chmod(0o755)
            failed_env = dict(env, PATH=str(fake_bin) + os.pathsep + os.environ["PATH"])
            result = subprocess.run([str(package / "install-user.sh")], env=failed_env, capture_output=True)
            self.assertEqual(result.returncode, 73)
            installed = data_home / "cryptodesk-app"
            self.assertEqual((installed / "runtime/usr/bin/cryptodesk").read_text(), "previous runtime")
            self.assertEqual((installed / "screener-runtime/worker.cjs").read_text(), "previous downloader")
            self.assertEqual(downloaded.read_text(), "saved historical prices")
            self.assertEqual(unrelated.read_text(), "unrelated temporary file")
            self.assertFalse(any(installed.glob(".install.*")))


if __name__ == "__main__":
    unittest.main()

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
            package = root / "portable"
            package.mkdir()
            for name in ("cryptodesk.svg", "cryptodesk.desktop"):
                shutil.copy2(icons.ROOT / "build/linux" / name, package / name)
            installer = package / "install-user.sh"
            shutil.copy2(icons.ROOT / "scripts/install-user.sh", installer)
            (package / "cryptodesk").write_bytes(b"preserved binary")
            data_home = root / 'user app data 100%'
            workspace = data_home / "cryptodesk/workspace.sqlite"
            workspace.parent.mkdir(parents=True)
            workspace.write_bytes(b"existing user workspace")
            env = dict(os.environ, XDG_DATA_HOME=str(data_home), HOME=str(root / "home"))
            subprocess.run([str(installer), "--dry-run"], env=env, check=True, capture_output=True)
            self.assertFalse((data_home / "applications").exists())
            subprocess.run([str(installer)], env=env, check=True, capture_output=True)
            installed = data_home / "cryptodesk-app/cryptodesk"
            self.assertEqual(installed.read_bytes(), b"preserved binary")
            self.assertTrue(installed.stat().st_mode & 0o111)
            self.assertEqual((data_home / "icons/hicolor/scalable/apps/cryptodesk.svg").read_bytes(), (icons.ROOT / icons.SOURCE).read_bytes())
            desktop = (data_home / "applications/cryptodesk.desktop").read_text()
            self.assertIn(f'Exec="{str(installed).replace("%", "%%")}"', desktop)
            self.assertIn("Icon=cryptodesk\n", desktop)
            self.assertIn("StartupWMClass=cryptodesk\n", desktop)
            self.assertEqual(workspace.read_bytes(), b"existing user workspace")
            self.assertEqual(sorted(str(path.relative_to(data_home)) for path in data_home.rglob("*") if path.is_file()),
                             ["applications/cryptodesk.desktop", "cryptodesk-app/cryptodesk", "cryptodesk/workspace.sqlite", "icons/hicolor/scalable/apps/cryptodesk.svg"])


if __name__ == "__main__":
    unittest.main()

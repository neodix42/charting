#!/usr/bin/env python3
"""Generate native icons from the small vector logo without extra dependencies.

Only this logo's SVG shapes (a rounded rectangle and solid polygons) are
supported. Render with four samples per pixel in each direction for crisp
small taskbar icons. Run --check to detect stale generated assets.
"""
import argparse
import math
from pathlib import Path
import struct
import xml.etree.ElementTree as ET
import zlib

ROOT = Path(__file__).resolve().parents[1]
SOURCE = Path("frontend/src/assets/cryptodesk.svg")
ICON_SIZES = (16, 24, 32, 48, 64, 128, 256)


def color(value):
    if len(value) != 7 or value[0] != "#":
        raise ValueError("Logo fills must use six-digit hex colors")
    return bytes.fromhex(value[1:]) + b"\xff"


def chunk(kind, payload):
    return struct.pack(">I", len(payload)) + kind + payload + struct.pack(">I", zlib.crc32(kind + payload))


def render_png(svg, size):
    root = ET.fromstring(svg)
    if root.attrib["viewBox"] != "0 0 256 256":
        raise ValueError("Expected the CryptoDesk 256-unit viewBox")
    samples = 4
    dimension = size * samples
    scale = dimension / 256
    pixels = bytearray(dimension * dimension * 4)

    def span(row, left, right, fill):
        start = max(0, math.ceil(left * scale - 0.5))
        end = min(dimension, math.ceil(right * scale - 0.5))
        if end > start:
            offset = (row * dimension + start) * 4
            pixels[offset:offset + (end - start) * 4] = fill * (end - start)

    for shape in root:
        fill = color(shape.attrib["fill"])
        kind = shape.tag.rsplit("}", 1)[-1]
        if kind == "rect":
            x, y, width, height, radius = (float(shape.attrib[field]) for field in ("x", "y", "width", "height", "rx"))
            for row in range(dimension):
                center = (row + 0.5) / scale
                if not y <= center < y + height:
                    continue
                distance = max(y + radius - center, center - (y + height - radius), 0)
                inset = radius - math.sqrt(max(0, radius * radius - distance * distance))
                span(row, x + inset, x + width - inset, fill)
        elif kind == "polygon":
            points = [tuple(map(float, pair.split(","))) for pair in shape.attrib["points"].split()]
            edges = list(zip(points, points[1:] + points[:1]))
            for row in range(dimension):
                center = (row + 0.5) / scale
                crossings = sorted(x1 + (center - y1) * (x2 - x1) / (y2 - y1)
                                   for (x1, y1), (x2, y2) in edges if min(y1, y2) <= center < max(y1, y2))
                for left, right in zip(crossings[::2], crossings[1::2]):
                    span(row, left, right, fill)
        else:
            raise ValueError(f"Unsupported logo shape: {kind}")

    image = bytearray()
    for row in range(size):
        image.append(0)  # PNG filter None.
        for column in range(size):
            totals = [0, 0, 0, 0]
            for dy in range(samples):
                offset = ((row * samples + dy) * dimension + column * samples) * 4
                for dx in range(samples):
                    for channel in range(4):
                        totals[channel] += pixels[offset + dx * 4 + channel]
            # Average premultiplied RGB so transparent corners have clean edges.
            alpha = totals[3]
            image.extend((round(component * 255 / alpha) if alpha else 0) for component in totals[:3])
            image.append(round(alpha / (samples * samples)))
    return b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", size, size, 8, 6, 0, 0, 0)) + chunk(b"IDAT", zlib.compress(image, 9)) + chunk(b"IEND", b"")


def windows_icon(images):
    offset = 6 + len(images) * 16
    entries = []
    content = []
    for size, png in images:
        entries.append(struct.pack("<BBBBHHII", size % 256, size % 256, 0, 0, 1, 32, len(png), offset))
        content.append(png)
        offset += len(png)
    return struct.pack("<HHH", 0, 1, len(images)) + b"".join(entries + content)


def assets(root=ROOT):
    svg = (root / SOURCE).read_bytes()
    return {
        Path("build/linux/cryptodesk.svg"): svg,
        Path("build/appicon.png"): render_png(svg, 512),
        Path("build/windows/icon.ico"): windows_icon([(size, render_png(svg, size)) for size in ICON_SIZES]),
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true", help="fail if generated icons differ from the source logo")
    args = parser.parse_args()
    for relative, content in assets().items():
        path = ROOT / relative
        if args.check:
            if not path.is_file() or path.read_bytes() != content:
                parser.exit(1, f"Stale icon: {relative}; run python3 scripts/app_icons.py\n")
        else:
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(content)
            print(relative)


if __name__ == "__main__":
    main()

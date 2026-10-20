#!/usr/bin/env python3
"""One validated application version for local builds and automatic releases."""
import os
from pathlib import Path
import re
import sys

VERSION_PATTERN = re.compile(r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)")


def parse_version(value):
    if not isinstance(value, str) or not VERSION_PATTERN.fullmatch(value):
        raise ValueError("Version must be X.Y.Z, without a v prefix, leading zeros or suffixes")
    parts = tuple(int(part) for part in value.split("."))
    if any(part > 65535 for part in parts):
        raise ValueError("Each version component must be at most 65535 (Windows version-resource limit)")
    return parts


def app_version(root, environment=None):
    environment = os.environ if environment is None else environment
    value = environment.get("CRYPTODESK_VERSION", Path(root).joinpath("VERSION").read_text().strip())
    parse_version(value)
    return value


if __name__ == "__main__":
    try:
        print(app_version(Path(__file__).resolve().parent.parent))
    except (OSError, ValueError) as error:
        raise SystemExit(f"Invalid application version: {error}")

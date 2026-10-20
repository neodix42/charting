#!/usr/bin/env python3
"""One validated application version for local builds and automatic releases."""
import argparse
import json
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


def verify_frontend_version(root, version):
    """Reject packaging when the compiled UI belongs to another release."""
    parse_version(version)
    metadata = json.loads(Path(root).joinpath("frontend/dist/app-version.json").read_text())
    if not isinstance(metadata, dict) or metadata.get("version") != version or metadata.get("tag") != f"v{version}":
        raise ValueError(f"Compiled frontend version must match release v{version}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--verify-frontend", action="store_true", help="Verify the compiled frontend before packaging")
    arguments = parser.parse_args()
    try:
        root = Path(__file__).resolve().parent.parent
        version = app_version(root)
        if arguments.verify_frontend:
            verify_frontend_version(root, version)
        print(version)
    except (OSError, ValueError) as error:
        raise SystemExit(f"Invalid application version: {error}")

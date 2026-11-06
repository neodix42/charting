#!/usr/bin/env python3
"""Transfer an already verified SDK into the CI build cache, never a product archive."""
import argparse
import hashlib
import io
from pathlib import Path
import tarfile
import tempfile

import jforex_bundle as sdk


def export_cache(directory, destination):
    entries = sdk.artifacts()
    directory, destination = Path(directory), Path(destination)
    # Do not fetch, replace or delete anything in the last working SDK cache.
    data = {}
    for entry in entries:
        path = directory / entry["name"]
        if path.is_symlink() or not path.is_file() or path.stat().st_size != entry["bytes"] or sdk.sha256(path) != entry["sha256"]:
            raise ValueError(f"Verified local SDK artifact required: {entry['name']}")
        data[entry["name"]] = path.read_bytes()
    with destination.open("xb") as output, tarfile.open(fileobj=output, mode="w:gz") as archive:
        for name, content in sorted(data.items()):
            info = tarfile.TarInfo(name)
            info.size, info.mode, info.mtime = len(content), 0o600, 0
            archive.addfile(info, io.BytesIO(content))


def import_cache(source, directory, digest):
    source, directory = Path(source), Path(directory)
    entries = {entry["name"]: entry for entry in sdk.artifacts()}
    if source.is_symlink() or not source.is_file() or source.stat().st_size > 128 * 1024 * 1024 or sdk.sha256(source) != digest:
        raise ValueError("SDK cache transfer checksum/size mismatch")
    # Validate the complete inventory before touching an existing working copy.
    verified = {}
    with tarfile.open(source, "r:gz") as archive:
        for member in archive:
            entry = entries.get(member.name)
            if not entry or member.name in verified or not member.isfile() or member.size != entry["bytes"]:
                raise ValueError("Unexpected or unsafe SDK cache transfer entry")
            content = archive.extractfile(member).read(member.size + 1)
            if len(content) != entry["bytes"] or hashlib.sha256(content).hexdigest() != entry["sha256"]:
                raise ValueError("SDK cache artifact checksum mismatch")
            verified[member.name] = content
    if set(verified) != set(entries):
        raise ValueError("SDK cache transfer is incomplete")
    directory.mkdir(parents=True, exist_ok=True)
    for name in verified:
        path = directory / name
        if path.exists() or path.is_symlink():
            if path.is_symlink() or not path.is_file() or path.stat().st_size != entries[name]["bytes"] or sdk.sha256(path) != entries[name]["sha256"]:
                raise ValueError("Existing SDK cache artifact differs; original preserved")
    for name, content in verified.items():
        path = directory / name
        if path.exists():
            continue
        with tempfile.TemporaryDirectory(prefix=".sdk-transfer-", dir=directory) as temporary:
            pending = Path(temporary) / name
            pending.write_bytes(content)
            pending.chmod(0o600)
            pending.replace(path)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    modes = parser.add_mutually_exclusive_group(required=True)
    modes.add_argument("--export", type=Path)
    modes.add_argument("--import", dest="source", type=Path)
    parser.add_argument("--cache", type=Path, default=sdk.ROOT / ".tools/jforex-sdk/cache")
    parser.add_argument("--sha256")
    args = parser.parse_args()
    if args.export:
        export_cache(args.cache, args.export)
        print(sdk.sha256(args.export))
    elif args.sha256:
        import_cache(args.source, args.cache, args.sha256)
        print("Verified complete JForex SDK cache imported")
    else:
        parser.error("--import requires --sha256")


if __name__ == "__main__":
    main()

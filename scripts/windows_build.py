#!/usr/bin/env python3
"""Cross-build current working sources without letting Wails rewrite the checkout."""
import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import shutil
import struct
import subprocess
import sys
import tempfile
import zipfile

NAME = "cryptodesk-0.1.0-windows-amd64"


def sha256(path):
    with Path(path).open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def validate_executable(path):
    with Path(path).open("rb") as stream:
        header = stream.read(64)
        if len(header) != 64 or header[:2] != b"MZ":
            raise ValueError(f"Not a Windows executable: {path}")
        stream.seek(struct.unpack_from("<I", header, 60)[0])
        pe = stream.read(96)
    if len(pe) < 96 or pe[:4] != b"PE\0\0" or struct.unpack_from("<H", pe, 4)[0] != 0x8664:
        raise ValueError(f"Expected Windows AMD64 executable: {path}")
    if struct.unpack_from("<H", pe, 24)[0] != 0x20B or struct.unpack_from("<H", pe, 92)[0] != 2:
        raise ValueError(f"Expected a PE32+ Windows GUI application: {path}")


def source_snapshot(root, destination):
    excluded = {".git", ".tools", ".idea", "release", "node_modules", "__pycache__", ".env"}

    def ignore(directory, names):
        relative = Path(directory).relative_to(root)
        ignored = set(names) & excluded
        ignored |= {name for name in names if name.endswith((".sqlite", ".sqlite-wal", ".sqlite-shm", ".lockfile"))}
        if relative == Path("."):
            ignored |= {name for name in names if name == "cryptodesk" or name.startswith("cryptodesk-")}
        if relative == Path("frontend"):
            ignored |= {"dist", "test-results", "playwright-report"}
        if relative == Path("build"):
            ignored.add("bin")
        return ignored

    shutil.copytree(root, destination, ignore=ignore)


def package_zip(directory, archive):
    with zipfile.ZipFile(archive, "w", zipfile.ZIP_DEFLATED, compresslevel=6, allowZip64=True) as output:
        for path in sorted(directory.rglob("*")):
            if path.is_symlink():
                raise ValueError(f"Refusing a symlink in the Windows package: {path}")
            if path.is_file():
                output.write(path, f"{directory.name}/{path.relative_to(directory).as_posix()}")


def run(root, cab=None):
    root = root.resolve()
    if not (shutil.which("7z") or shutil.which("7zz")):
        raise RuntimeError("7z is required to extract Microsoft's runtime CAB (Ubuntu package: 7zip). No packages were installed.")
    pin = json.loads((root / "scripts/webview2-runtime.json").read_text())
    with tempfile.TemporaryDirectory(prefix="cryptodesk-windows-build-") as temporary:
        temporary = Path(temporary)
        source = temporary / "source"
        package = temporary / NAME
        source_snapshot(root, source)
        env = dict(os.environ, CGO_ENABLED="0", VITE_EXPLICIT_TEST_BRIDGE="0")
        # Frontend accesses the reflected Wails bridge directly. Existing JS wrappers
        # need no regeneration; cross-builds must not execute a Windows bindings binary.
        subprocess.run([str(root / ".tools/wails"), "build", "-platform", "windows/amd64",
                        "-skipbindings", "-webview2", "error", "-m", "-nosyncgomod",
                        "-trimpath", "-o", "cryptodesk.exe"], cwd=source, env=env, check=True)
        executable = source / "build/bin/cryptodesk.exe"
        validate_executable(executable)
        package.mkdir()
        shutil.copy2(executable, package / "cryptodesk.exe")
        (package / "portable.marker").write_text("CryptoDesk portable workspace; keep this file beside cryptodesk.exe.\n")
        command = [sys.executable, str(root / "scripts/webview2_runtime.py"),
                   "--cache-dir", str(root / ".tools/webview2"), "--destination", str(package / "WebView2")]
        if cab:
            command.extend(["--cab", str(cab)])
        subprocess.run(command, check=True)
        for name in ["APP_README.md", "LICENSE", "STATUS.md"]:
            shutil.copy2(root / name, package / name)
        shutil.copy2(root / "docs/WINDOWS_PORTABLE.md", package / "README-WINDOWS.md")
        (package / "docs").mkdir()
        shutil.copy2(root / "docs/THIRD_PARTY_NOTICES.md", package / "docs/THIRD_PARTY_NOTICES.md")
        shutil.copytree(root / "docs/licenses", package / "docs/licenses")
        shutil.copy2(root / "scripts/webview2-runtime.json", package / "webview2-runtime.json")
        manifest = {
            "app": "CryptoDesk", "version": "0.1.0", "target": "windows/amd64", "subsystem": "windowsgui",
            "builtAtUTC": datetime.datetime.now(datetime.timezone.utc).isoformat(),
            "go": subprocess.check_output(["go", "version"], text=True).strip(),
            "node": subprocess.check_output(["node", "--version"], text=True).strip(),
            "npm": subprocess.check_output(["npm", "--version"], text=True).strip(),
            "wails": "v2.17.0", "webview2": pin,
            "executableSHA256": sha256(package / "cryptodesk.exe"),
            "gitRevision": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
            "workingTreeModified": bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=root, text=True).strip()),
            "runtimeVerification": "Cross-compiled and package-verified on Ubuntu; Windows execution not verified by this build.",
        }
        (package / "version.json").write_text(json.dumps(manifest, indent=2) + "\n")
        with (package / "SHA256SUMS").open("w") as checksums:
            for path in sorted(package.rglob("*")):
                if path.is_file() and path.name != "SHA256SUMS":
                    checksums.write(f"{sha256(path)}  {path.relative_to(package).as_posix()}\n")
        archive = temporary / (NAME + ".zip")
        package_zip(package, archive)
        # Verify the complete ZIP before publishing, without overwriting Linux outputs.
        with zipfile.ZipFile(archive) as result:
            bad = result.testzip()
            if bad:
                raise ValueError(f"ZIP integrity failure: {bad}")
        destination = root / archive.name
        pending = root / (archive.name + ".tmp")
        try:
            shutil.copyfile(archive, pending)
            os.replace(pending, destination)
        finally:
            pending.unlink(missing_ok=True)
        (root / "cryptodesk-Windows-SHA256SUMS").write_text(f"{sha256(destination)}  {destination.name}\n")
        print(f"\nPortable Windows ZIP: {destination}\nSize: {destination.stat().st_size / 1024**2:.1f} MiB")
        print("Extract the complete ZIP to a writable local Windows folder and run cryptodesk.exe.")
        print("No Windows GUI execution was performed on this Ubuntu host.")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("root", type=Path)
    parser.add_argument("--webview2-cab", type=Path)
    args = parser.parse_args()
    try:
        run(args.root, args.webview2_cab)
    except (OSError, ValueError, RuntimeError, subprocess.CalledProcessError) as error:
        raise SystemExit(f"Windows build failed: {error}")

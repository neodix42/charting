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
from app_version import app_version, verify_frontend_version
from build_metadata import source_provenance
from screener_bundle import assemble as assemble_screener

ENGLISH_LOCALE_PAKS = frozenset({"en-US.pak", "en-GB.pak", "copilot_overlay_strings_en-US.pak", "copilot_overlay_strings_en-GB.pak"})


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


def keep_english_locales(runtime):
    """Reduce only the verified staging copy; never edit the source cache or CAB."""
    runtime = Path(runtime)
    locales = runtime / "Locales"
    if locales.is_symlink() or not locales.is_dir():
        raise ValueError("The staged runtime is missing its regular Locales directory")
    packs = sorted(path for path in locales.iterdir() if path.suffix.lower() == ".pak")
    if any(path.is_symlink() or not path.is_file() for path in packs):
        raise ValueError("Locale packs must be regular files")
    for name in ENGLISH_LOCALE_PAKS:
        if not (locales / name).is_file() or not (locales / name).stat().st_size:
            raise ValueError(f"The staged runtime is missing the English locale pack {name}")
    source = json.loads((runtime / "WEBVIEW2-PROVENANCE.json").read_text())
    removed = [{"name": path.name, "bytes": path.stat().st_size} for path in packs if path.name not in ENGLISH_LOCALE_PAKS]
    for item in removed:
        (locales / item["name"]).unlink()
    inventory = hashlib.sha256()
    count, total = 0, 0
    files = sorted((path.relative_to(runtime).as_posix(), path) for path in runtime.rglob("*") if path.is_file() and path.name != "WEBVIEW2-PROVENANCE.json")
    for name, path in files:
        size = path.stat().st_size
        inventory.update(f"{name}\0{size}\0{sha256(path)}\n".encode("utf-8"))
        count += 1
        total += size
    profile = {
        "profile": "english-only", "languages": ["en-US", "en-GB"],
        "browserUILanguage": "en-US", "keptLocalePaks": sorted(ENGLISH_LOCALE_PAKS),
        "removedLocalePaks": removed, "removedBytes": sum(item["bytes"] for item in removed),
        "file_count": count, "uncompressed_bytes": total, "inventory_sha256": inventory.hexdigest(),
    }
    (runtime / "WEBVIEW2-PROVENANCE.json").write_text(json.dumps({"sourceRuntime": source, "packagedRuntime": profile}, indent=2) + "\n")
    print(f"English locales: kept {len(ENGLISH_LOCALE_PAKS)}, removed {len(removed)} packs ({profile['removedBytes'] / 1024**2:.2f} MiB unpacked)")
    return profile


def language_compiler(directory, go_flag):
    """Scope module flags to compilation; Wails' version probes disable modules."""
    compiler = Path(directory) / "go-windows-english"
    go = shutil.which("go")
    if not go:
        raise RuntimeError("Go is required to build the Windows application")
    compiler.write_text("#!/usr/bin/env python3\n"
                        "import os, subprocess, sys\n"
                        "environment = os.environ.copy()\n"
                        "if sys.argv[1:2] == ['build']:\n"
                        f"    environment['GOFLAGS'] = ' '.join(part for part in (environment.get('GOFLAGS', ''), {go_flag!r}) if part)\n"
                        f"raise SystemExit(subprocess.call([{go!r}, *sys.argv[1:]], env=environment))\n")
    compiler.chmod(0o755)
    return compiler


def set_snapshot_version(source, version):
    """Set Windows file/product version in an isolated source snapshot only."""
    config_path = source / "wails.json"
    config = json.loads(config_path.read_text())
    config["info"]["productVersion"] = version
    config_path.write_text(json.dumps(config, indent=2) + "\n")


def run(root, cab=None):
    root = root.resolve()
    version = app_version(root)
    release_name = f"cryptodesk-{version}-windows-amd64"
    provenance = source_provenance(root)
    subprocess.run([sys.executable, str(root / "scripts/app_icons.py"), "--check"], check=True)
    if not (shutil.which("7z") or shutil.which("7zz")):
        raise RuntimeError("7z is required to extract Microsoft's runtime CAB (Ubuntu package: 7zip). No packages were installed.")
    pin = json.loads((root / "scripts/webview2-runtime.json").read_text())
    with tempfile.TemporaryDirectory(prefix="cryptodesk-windows-build-") as temporary:
        temporary = Path(temporary)
        source = temporary / "source"
        package = temporary / release_name
        source_snapshot(root, source)
        # Wails embeds this version in the executable's Windows resource. Update
        # only the temporary source snapshot, preserving the checkout's metadata.
        set_snapshot_version(source, version)
        env = dict(os.environ, CGO_ENABLED="0", VITE_EXPLICIT_TEST_BRIDGE="0", CRYPTODESK_VERSION=version)
        # Wails does not expose the browser language option. A build-local overlay
        # adds the supported WebView2 Language API call to the exact pinned loader.
        from windows_webview_language import prepare_language_overlay
        language = prepare_language_overlay(temporary / "webview-language")
        compiler = language_compiler(temporary, language["goFlag"])
        # Frontend accesses the reflected Wails bridge directly. Existing JS wrappers
        # need no regeneration; cross-builds must not execute a Windows bindings binary.
        subprocess.run([str(root / ".tools/wails"), "build", "-platform", "windows/amd64",
                        "-compiler", str(compiler),
                        "-skipbindings", "-webview2", "error", "-m", "-nosyncgomod",
                        "-trimpath", "-o", "cryptodesk.exe"], cwd=source, env=env, check=True)
        verify_frontend_version(source, version)
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
        locale_profile = keep_english_locales(package / "WebView2")
        screener = assemble_screener(root, "windows-amd64", package / "screener-runtime")
        for document_name in ["APP_README.md", "LICENSE", "STATUS.md"]:
            shutil.copy2(root / document_name, package / document_name)
        shutil.copy2(root / "docs/WINDOWS_PORTABLE.md", package / "README-WINDOWS.md")
        (package / "docs").mkdir()
        shutil.copy2(root / "docs/THIRD_PARTY_NOTICES.md", package / "docs/THIRD_PARTY_NOTICES.md")
        shutil.copytree(root / "docs/licenses", package / "docs/licenses")
        shutil.copy2(root / "scripts/webview2-runtime.json", package / "webview2-runtime.json")
        manifest = {
            "app": "CryptoDesk", "version": version, "target": "windows/amd64", "subsystem": "windowsgui",
            "builtAtUTC": datetime.datetime.now(datetime.timezone.utc).isoformat(),
            "go": subprocess.check_output(["go", "version"], text=True).strip(),
            "node": subprocess.check_output(["node", "--version"], text=True).strip(),
            "npm": subprocess.check_output(["npm", "--version"], text=True).strip(),
            "wails": "v2.17.0", "webview2": pin,
            "webview2Packaging": locale_profile, "webview2LanguageOverride": language["manifest"],
            "screener": {key: value for key, value in screener.items() if key != "files"},
            "executableSHA256": sha256(package / "cryptodesk.exe"),
            **provenance,
            "runtimeVerification": "Cross-compiled and package-verified on Ubuntu; Windows execution not verified by this build.",
        }
        (package / "version.json").write_text(json.dumps(manifest, indent=2) + "\n")
        with (package / "SHA256SUMS").open("w") as checksums:
            for path in sorted(package.rglob("*")):
                if path.is_file() and path.name != "SHA256SUMS":
                    checksums.write(f"{sha256(path)}  {path.relative_to(package).as_posix()}\n")
        archive = temporary / (release_name + ".zip")
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

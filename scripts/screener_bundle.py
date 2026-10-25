#!/usr/bin/env python3
"""Package the pinned downloader, private Node and Chromium without system installs."""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import shutil
import stat
import subprocess
import tarfile
import tempfile
import urllib.request
import zipfile

NODE_VERSION = "24.19.0"
DUKASCOPY_VERSION = "1.51.0"
PLAYWRIGHT_VERSION = "1.64.1"
CHROMIUM_VERSION = "156.0.8078.4"
CHROMIUM_REVISION = "1248"
MANIFEST = "SCREENER-PROVENANCE.json"
PINS = {
    "linux-amd64": {
        "nodeArchive": f"node-v{NODE_VERSION}-linux-x64.tar.xz",
        "nodeSHA256": "14b342e71204f811bde6153be8e04b62aef63c236fef92b55f9c83154b409647",
        "browserPlatform": "linux64",
        "browserSHA256": "43fd31766c7533a02dda2ded5ad50c2f861cd5400be7509c0abad71129f79d79",
        "nodePath": "bin/node", "executable": "node", "browserExecutable": "chrome-linux64/chrome",
    },
    "windows-amd64": {
        "nodeArchive": f"node-v{NODE_VERSION}-win-x64.zip",
        "nodeSHA256": "57f71ab3652e797d84acddc79c81cc9ff1c6ddb2a1974cdb83f00fee9bff4c73",
        "browserPlatform": "win64",
        "browserSHA256": "220021ecfdad08c116fc71a153e65e3861ba841c531afc4f7acc7bd5d44b41db",
        "nodePath": "node.exe", "executable": "node.exe", "browserExecutable": "chrome-win64/chrome.exe",
    },
}


def sha256(path):
    with Path(path).open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def download(url, destination):
    with urllib.request.urlopen(url, timeout=60) as response, destination.open("wb") as output:
        size = 0
        while block := response.read(1024 * 1024):
            size += len(block)
            if size > 512 * 1024 * 1024:
                raise ValueError("Screener archive exceeds the 512 MiB download limit")
            output.write(block)


def verified_download(cache, filename, url, checksum, fetch=download):
    """Never extract or execute an unchecked download, including cached archives."""
    cache.mkdir(parents=True, exist_ok=True)
    destination = cache / filename
    if destination.exists():
        if not destination.is_file() or destination.is_symlink() or sha256(destination) != checksum:
            raise ValueError(f"Screener archive SHA-256 mismatch: {destination}")
        return destination
    with tempfile.TemporaryDirectory(prefix=".download-", dir=cache) as temporary:
        pending = Path(temporary) / filename
        print(f"Downloading pinned Screener runtime: {url}", flush=True)
        fetch(url, pending)
        if sha256(pending) != checksum:
            raise ValueError(f"Screener archive SHA-256 mismatch: {filename}")
        pending.rename(destination)
    return destination


def extract_zip(archive, destination):
    """Retain executable permissions and refuse traversal and archive symlinks."""
    with zipfile.ZipFile(archive) as source:
        for entry in source.infolist():
            name = PurePosixPath(entry.filename)
            mode = entry.external_attr >> 16
            if name.is_absolute() or ".." in name.parts or "\\" in entry.filename or stat.S_ISLNK(mode):
                raise ValueError(f"Unsafe Screener ZIP entry: {entry.filename}")
            path = destination.joinpath(*name.parts)
            if entry.is_dir():
                path.mkdir(parents=True, exist_ok=True)
            else:
                path.parent.mkdir(parents=True, exist_ok=True)
                with source.open(entry) as input_file, path.open("wb") as output_file:
                    shutil.copyfileobj(input_file, output_file)
                path.chmod(0o755 if mode & 0o111 else 0o644)


def browser_path(target):
    return f"browsers/chromium-{CHROMIUM_REVISION}/{PINS[target]['browserExecutable']}"


def required_files(target):
    return {PINS[target]["executable"], "worker.cjs", "package.json", "package-lock.json", "NODE-LICENSE",
            "THIRD_PARTY_NOTICES.md", "node_modules/dukascopy-node/package.json", "node_modules/dukascopy-node/dist/index.js",
            "node_modules/dukascopy-node/LICENSE", "node_modules/playwright-core/package.json",
            "node_modules/playwright-core/LICENSE", "node_modules/playwright-core/NOTICE", "node_modules/playwright-core/ThirdPartyNotices.txt",
            "node_modules/playwright-core/browsers.json", browser_path(target), (Path(browser_path(target)).parent / "ABOUT").as_posix()}


def validate_inventory(manifest, target, files, read):
    """Check an unpacked directory or release archive against its complete inventory."""
    if (manifest.get("target") != target or manifest.get("nodeVersion") != NODE_VERSION
            or manifest.get("dukascopyVersion") != DUKASCOPY_VERSION or manifest.get("playwrightVersion") != PLAYWRIGHT_VERSION
            or manifest.get("chromiumVersion") != CHROMIUM_VERSION or manifest.get("chromiumRevision") != CHROMIUM_REVISION):
        raise ValueError("Screener runtime versions/target differ from the reviewed pins")
    inventory = manifest.get("files")
    if not isinstance(inventory, dict) or set(inventory) != set(files) - {MANIFEST} or not required_files(target).issubset(inventory):
        raise ValueError("Screener runtime inventory is incomplete or does not match the package")
    if any(files[name][0] <= 0 for name in required_files(target)):
        raise ValueError("Screener runtime contains an empty required file")
    versions = {"node_modules/dukascopy-node/package.json": DUKASCOPY_VERSION,
                "node_modules/playwright-core/package.json": PLAYWRIGHT_VERSION}
    # The archive reader supplies physical tar order, avoiding repeated backward
    # seeks/decompression across the much larger Node and browser binaries.
    for name, (size, executable) in files.items():
        if name == MANIFEST:
            continue
        record = inventory[name]
        data = read(name)
        if not isinstance(record, dict) or record.get("bytes") != size or hashlib.sha256(data).hexdigest() != record.get("sha256"):
            raise ValueError(f"Screener runtime inventory mismatch: {name}")
        if target == "linux-amd64" and name in {"node", browser_path(target)} and not executable:
            raise ValueError(f"Screener runtime file is not executable: {name}")
        if name in versions and json.loads(data).get("version") != versions[name]:
            raise ValueError(f"Incorrect packaged Screener dependency: {name}")


def validate_directory(directory, target, allow_runtime_state=False):
    files = {}
    for path in directory.rglob("*"):
        if path.is_symlink():
            raise ValueError(f"Screener package must not contain symlinks: {path}")
        if path.is_file():
            relative = path.relative_to(directory).as_posix()
            # Playwright records a successful host-library check after launch.
            # Listing a previously used development cache may ignore only this
            # empty marker; release inventory validation remains strict.
            if allow_runtime_state and relative == f"browsers/chromium-{CHROMIUM_REVISION}/DEPENDENCIES_VALIDATED" and path.stat().st_size == 0:
                continue
            files[relative] = (path.stat().st_size, bool(path.stat().st_mode & 0o111))
    manifest = json.loads((directory / MANIFEST).read_text())
    validate_inventory(manifest, target, files, lambda name: (directory / name).read_bytes())
    return manifest


def copy_runtime(source, destination, target):
    """Replace only an application-owned runtime, preserving a valid old copy on failure."""
    validate_directory(source, target)
    destination.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=".screener-copy-", dir=destination.parent) as temporary:
        stage = Path(temporary)
        shutil.copytree(source, stage / "runtime")
        previous = stage / "previous"
        if destination.exists():
            destination.rename(previous)
        try:
            (stage / "runtime").rename(destination)
        except OSError:
            if previous.exists():
                previous.rename(destination)
            raise


def assemble(root, target, destination):
    pin = PINS[target]
    source = root / "screener-runtime"
    if destination.resolve() == source.resolve():
        raise ValueError("Refusing to replace the Screener source directory")
    package = json.loads((source / "package.json").read_text())
    if package.get("dependencies", {}).get("dukascopy-node") != DUKASCOPY_VERSION:
        raise ValueError("Screener package must pin the reviewed dukascopy-node version")
    cache = root / ".tools/screener-downloads"
    node_url = f"https://nodejs.org/dist/v{NODE_VERSION}/{pin['nodeArchive']}"
    browser_archive = f"chrome-{pin['browserPlatform']}.zip"
    browser_url = f"https://cdn.playwright.dev/builds/cft/{CHROMIUM_VERSION}/{pin['browserPlatform']}/{browser_archive}"
    node_archive = verified_download(cache, pin["nodeArchive"], node_url, pin["nodeSHA256"])
    browser_archive = verified_download(cache, browser_archive, browser_url, pin["browserSHA256"])
    destination.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=".screener-stage-", dir=destination.parent) as temporary:
        stage = Path(temporary)
        runtime = stage / "runtime"
        runtime.mkdir()
        for name in ("worker.cjs", "package.json", "package-lock.json"):
            shutil.copy2(source / name, runtime / name)
        # Exact lockfile integrity is enforced by npm. No lifecycle hooks,
        # global installs, runtime npx or target-native addons are required.
        lock_hash = sha256(source / "package-lock.json")
        subprocess.run(["npm", "ci", "--omit=dev", "--ignore-scripts", "--bin-links=false", "--no-audit", "--no-fund",
                        "--cache", str(root / ".tools/screener-npm-cache")],
                       cwd=runtime, env=dict(os.environ, PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD="1"), check=True)
        if any((runtime / "node_modules").rglob("*.node")):
            raise ValueError("Screener dependency gained a native addon; review target-specific packaging")
        node_stage = stage / "node"
        if pin["nodeArchive"].endswith(".zip"):
            extract_zip(node_archive, node_stage)
            node_root = node_stage / pin["nodeArchive"].removesuffix(".zip")
        else:
            with tarfile.open(node_archive, "r:xz") as archive:
                archive.extractall(node_stage, filter="data")
            node_root = node_stage / pin["nodeArchive"].removesuffix(".tar.xz")
        shutil.copy2(node_root / pin["nodePath"], runtime / pin["executable"])
        shutil.copy2(node_root / "LICENSE", runtime / "NODE-LICENSE")
        extract_zip(browser_archive, runtime / "browsers" / f"chromium-{CHROMIUM_REVISION}")
        # npm retains each package's own LICENSE/NOTICE. Chromium's distribution
        # carries its full browser resources; chrome://credits exposes notices.
        (runtime / "THIRD_PARTY_NOTICES.md").write_text(
            "# Screener runtime notices\n\n"
            f"dukascopy-node {DUKASCOPY_VERSION}: https://github.com/Leo4815162342/dukascopy-node (MIT).\n"
            f"Node.js {NODE_VERSION}: {node_url}; license and dependency notices: NODE-LICENSE.\n"
            f"Playwright {PLAYWRIGHT_VERSION}: https://github.com/microsoft/playwright (Apache-2.0).\n"
            f"Chrome for Testing {CHROMIUM_VERSION}: {browser_url}. Chromium and third-party credits are embedded in the browser (chrome://credits).\n"
            "All npm package license/notice files are retained under node_modules.\n"
            "Runtime archives are SHA-256 pinned in scripts/screener_bundle.py; npm packages use package-lock.json integrity values.\n")
        files = {path.relative_to(runtime).as_posix(): {"bytes": path.stat().st_size, "sha256": sha256(path)}
                 for path in sorted(runtime.rglob("*")) if path.is_file()}
        manifest = {"target": target, "nodeVersion": NODE_VERSION, "dukascopyVersion": DUKASCOPY_VERSION,
                    "playwrightVersion": PLAYWRIGHT_VERSION, "chromiumVersion": CHROMIUM_VERSION,
                    "chromiumRevision": CHROMIUM_REVISION, "npmLockSHA256": lock_hash,
                    "nodeArchive": {"url": node_url, "sha256": pin["nodeSHA256"]},
                    "browserArchive": {"url": browser_url, "sha256": pin["browserSHA256"]}, "files": files}
        (runtime / MANIFEST).write_text(json.dumps(manifest, indent=2) + "\n")
        validate_directory(runtime, target)
        previous = stage / "previous"
        if destination.exists():
            destination.rename(previous)
        try:
            runtime.rename(destination)
        except OSError:
            if previous.exists():
                previous.rename(destination)
            raise
    print(f"Bundled preinstalled Screener: {destination}", flush=True)
    return manifest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parent.parent)
    parser.add_argument("--target", choices=sorted(PINS), required=True)
    parser.add_argument("--destination", type=Path, required=True)
    parser.add_argument("--verify", action="store_true")
    parser.add_argument("--copy-from", type=Path)
    arguments = parser.parse_args()
    try:
        if arguments.verify:
            validate_directory(arguments.destination, arguments.target)
        elif arguments.copy_from:
            copy_runtime(arguments.copy_from, arguments.destination, arguments.target)
        else:
            assemble(arguments.root.resolve(), arguments.target, arguments.destination)
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        parser.exit(1, f"Screener packaging failed: {error}\n")


if __name__ == "__main__":
    main()

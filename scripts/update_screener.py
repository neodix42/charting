#!/usr/bin/env python3
"""Check npm's latest stable downloader and publish only an offline-tested upgrade."""
import argparse
from contextlib import contextmanager
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import sys
import tempfile

import node_toolchain
import screener_bundle as bundle

SOURCE_FILES = ("screener-runtime/package.json", "screener-runtime/package-lock.json",
                "internal/screener/catalog.json")
PUBLISH_FILES = (*SOURCE_FILES, "screener-runtime/node_modules")
NPM_OPTIONS = ["--ignore-scripts", "--no-audit", "--no-fund", "--fetch-retries=0", "--fetch-timeout=15000"]
SUPPORTED_TIMEFRAMES = ["tick", "s1", "m1", "m5", "m15", "m30", "h1", "h4", "d1", "mn1"]


def stable_version(value):
    if not isinstance(value, str) or not re.fullmatch(r"(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)", value):
        raise ValueError(f"Expected an exact stable version, received {value!r}")
    return tuple(map(int, value.split(".")))


def read_json(path):
    return json.loads(Path(path).read_text())


def source_pin(root):
    """Fail before any registry request if the committed inputs disagree."""
    package = read_json(root / SOURCE_FILES[0])
    lock = read_json(root / SOURCE_FILES[1])
    version = package.get("dependencies", {}).get("dukascopy-node")
    stable_version(version)
    locked = lock.get("packages", {})
    if locked.get("", {}).get("dependencies") != package.get("dependencies"):
        raise ValueError("Screener package.json and package-lock.json dependencies differ")
    for name, expected in package["dependencies"].items():
        stable_version(expected)
        record = locked.get(f"node_modules/{name}", {})
        if record.get("version") != expected or not record.get("integrity"):
            raise ValueError(f"Screener lockfile does not integrity-pin {name} {expected}")
    catalog = read_json(root / SOURCE_FILES[2])
    if catalog.get("version") != version:
        raise ValueError("Screener package/lock/catalog versions differ; regenerate the catalog deliberately")
    catalog_ids(catalog)
    return version


def catalog_ids(catalog):
    ids = []
    for group in catalog.get("groups", []):
        if not group.get("name") or not group.get("instruments"):
            raise ValueError("Screener catalog has an empty group")
        ids.extend(item["id"] for item in group["instruments"])
    if not ids or len(set(ids)) != len(ids) or any(not isinstance(item, str) or not item for item in ids):
        raise ValueError("Screener catalog has missing or duplicate instruments")
    if catalog.get("timeframes") != SUPPORTED_TIMEFRAMES:
        raise ValueError("Downloader timeframes changed; review application and saved-data compatibility before upgrading")
    return set(ids)


def run(command, *, cwd, env=None, timeout=120):
    """Bound npm and test time, including child processes left by an interrupted command."""
    process = subprocess.Popen(command, cwd=cwd, env=env, text=True, stdout=subprocess.PIPE,
                               stderr=subprocess.STDOUT, start_new_session=True)
    try:
        output, _ = process.communicate(timeout=timeout)
    except BaseException:
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        process.communicate()
        raise
    if process.returncode:
        raise subprocess.CalledProcessError(process.returncode, command, output=output[-12000:])
    return output


@contextmanager
def update_lock(root):
    tools = root / ".tools"
    tools.mkdir(exist_ok=True)
    path = tools / "screener-update.lock"
    inherited = os.environ.get("CRYPTODESK_SCREENER_LOCK_FD")
    with path.open("a") as opened:
        fd = opened.fileno()
        if inherited:
            supplied = int(inherited)
            if not os.path.samestat(os.fstat(supplied), os.fstat(fd)):
                raise RuntimeError("The inherited Screener build lock does not match this checkout")
            fd = supplied
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as error:
            raise RuntimeError("Another CryptoDesk build or downloader update holds this checkout's lock") from error
        # Do not unlock an inherited descriptor: build.sh retains it through packaging.
        try:
            pending = list(tools.glob(".screener-update-*/PUBLISHING.json"))
            if pending:
                raise RuntimeError(f"An interrupted downloader publication needs recovery from {pending[0].parent}; originals are in previous/")
            yield
        finally:
            if not inherited:
                fcntl.flock(fd, fcntl.LOCK_UN)


def fingerprint(path):
    """Include local edits and symlink targets without following dependency links."""
    digest = hashlib.sha256()
    if not path.exists() and not path.is_symlink():
        return None
    entries = [path, *sorted(path.rglob("*"))] if path.is_dir() and not path.is_symlink() else [path]
    for entry in entries:
        digest.update(str(entry.relative_to(path.parent)).encode())
        digest.update(str(entry.lstat().st_mode).encode())
        if entry.is_symlink():
            digest.update(os.readlink(entry).encode())
        elif entry.is_file():
            with entry.open("rb") as stream:
                while block := stream.read(1024 * 1024):
                    digest.update(block)
    return digest.hexdigest()


def input_snapshot(root):
    paths = set(PUBLISH_FILES) | {"internal/screener/indicators.json"}
    paths |= {path.relative_to(root).as_posix() for path in (root / "screener-runtime").glob("*.cjs")}
    return {name: fingerprint(root / name) for name in sorted(paths)}


def stage_sources(root, stage):
    runtime = stage / "screener-runtime"
    runtime.mkdir(parents=True)
    (stage / "internal/screener").mkdir(parents=True)
    for path in (root / "screener-runtime").glob("*.cjs"):
        shutil.copy2(path, runtime / path.name)
    for name in (*SOURCE_FILES, "internal/screener/indicators.json"):
        source = root / name
        if source.is_symlink():
            raise ValueError(f"Cannot update a symlinked source file: {source}")
        shutil.copy2(source, stage / name)
    return runtime


def validation_toolchain(root, runner):
    """Candidate code must work on the Node version shipped in both release targets."""
    version = runner(["node", "--version"], cwd=root, timeout=15).strip()
    if version == f"v{bundle.NODE_VERSION}":
        return "node", "npm", dict(os.environ)
    # Existing compatible global Node may be newer than the private release Node.
    # Bootstrap only the reviewed local toolchain, never replace global tooling.
    platform = node_toolchain.platform.machine()
    if platform not in node_toolchain.ARCHIVES:
        raise ValueError(f"Upgrade validation requires the packaged Node {bundle.NODE_VERSION} on PATH")
    arch, checksum = node_toolchain.ARCHIVES[platform]
    archive = f"node-v{bundle.NODE_VERSION}-linux-{arch}.tar.xz"
    binary = root / ".tools" / archive.removesuffix(".tar.xz") / "bin"
    if not (binary / "node").is_file():
        binary = node_toolchain.install(root / ".tools", archive, checksum)
    env = dict(os.environ, PATH=str(binary) + os.pathsep + os.environ.get("PATH", ""))
    if runner([str(binary / "node"), "--version"], cwd=root, env=env, timeout=15).strip() != f"v{bundle.NODE_VERSION}":
        raise ValueError("The local pinned Node toolchain reports an unexpected version")
    return str(binary / "node"), str(binary / "npm"), env


def validate_candidate(root, stage, version, runner=run):
    runtime = stage_sources(root, stage)
    node, npm, env = validation_toolchain(root, runner)
    env = dict(env, PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD="1")
    runner([npm, "install", "--save-exact", "--engine-strict", "--bin-links=false", *NPM_OPTIONS,
            "--cache", str(stage / "npm-cache"), f"dukascopy-node@{version}"], cwd=runtime, env=env, timeout=180)
    installed = read_json(runtime / "node_modules/dukascopy-node/package.json")
    if installed.get("version") != version or installed.get("license") != "MIT":
        raise ValueError("Candidate downloader version/license differs; review upstream packaging before upgrading")
    original = read_json(root / SOURCE_FILES[0])
    candidate = read_json(stage / SOURCE_FILES[0])
    expected = dict(original["dependencies"], **{"dukascopy-node": version})
    if candidate != dict(original, dependencies=expected):
        raise ValueError("npm changed unrelated runtime package settings; original source preserved")
    playwright = runtime / "node_modules/playwright-core"
    chromium = [item for item in read_json(playwright / "browsers.json").get("browsers", []) if item.get("name") == "chromium"]
    if (read_json(playwright / "package.json").get("version") != bundle.PLAYWRIGHT_VERSION or len(chromium) != 1
            or str(chromium[0].get("revision")) != bundle.CHROMIUM_REVISION
            or chromium[0].get("browserVersion") != bundle.CHROMIUM_VERSION):
        raise ValueError("Candidate changes Playwright/Chromium; review matching browser versions, revision and Linux/Windows archive SHA-256 pins in scripts/screener_bundle.py before upgrading")
    if any((runtime / "node_modules").rglob("*.node")):
        raise ValueError("Candidate adds a native npm addon; review Linux/Windows packaging before upgrading")
    # Tests inject transport responses. Block accidental real sockets/fetches in
    # the test runner and inherited child workers, without contacting Dukascopy.
    guard = stage / "offline.cjs"
    guard.write_text("const deny=()=>{throw Error('Network disabled during downloader compatibility checks');};\n"
                     "require('node:net').Socket.prototype.connect=deny;global.fetch=deny;\n")
    offline = dict(env, NODE_OPTIONS=f'--require {json.dumps(str(guard))}')
    public_api = """
const assert=require('node:assert/strict'), api=require('dukascopy-node');
for(const key of ['validateConfigNode','normaliseDates','generateUrls','BufferFetcher','processData','formatOutput',
                  'RateLimitError','WafTokenError','WafChallengeError']) assert.equal(typeof api[key],'function',key);
assert.ok(api.instrumentMetaData && api.Timeframe);
assert.equal(require.resolve('playwright-core', {paths:[require.resolve('dukascopy-node')]}),require.resolve('playwright-core'),
             'Downloader and transport must share the reviewed Playwright copy');
"""
    runner([node, "-e", public_api], cwd=runtime, env=offline, timeout=30)
    runner([node, "generate-catalog.cjs"], cwd=runtime, env=offline, timeout=30)
    if source_pin(stage) != version:
        raise ValueError("Candidate package, lockfile and generated catalog do not agree")
    previous_ids = catalog_ids(read_json(root / SOURCE_FILES[2]))
    current_ids = catalog_ids(read_json(stage / SOURCE_FILES[2]))
    missing = previous_ids - current_ids
    if missing:
        raise ValueError(f"Candidate removes {len(missing)} saved-data instrument IDs (including {sorted(missing)[0]}); review migration before upgrading")
    def categories(catalog):
        return {item["id"]: group["name"] for group in catalog["groups"] for item in group["instruments"]}
    previous_categories = categories(read_json(root / SOURCE_FILES[2]))
    current_categories = categories(read_json(stage / SOURCE_FILES[2]))
    if any(current_categories.get(symbol) != category for symbol, category in previous_categories.items()):
        raise ValueError("Candidate changes existing instrument categories; review saved-condition migration before upgrading")
    test_files = sorted(path.name for path in runtime.glob("*.test.cjs"))
    if not test_files:
        raise ValueError("No production runtime compatibility tests found")
    print(f"Checking dukascopy-node {version} against production workers with offline transports...", flush=True)
    runner([node, "--test", "--test-reporter=dot", *test_files], cwd=runtime, env=offline, timeout=180)
    runner([node, "generate-catalog.cjs", "--check"], cwd=runtime, env=offline, timeout=30)


def publish(root, stage, transaction, snapshot):
    if input_snapshot(root) != snapshot:
        raise RuntimeError("Screener sources or installed dependencies changed during validation; rerun without overwriting those edits")
    for name in PUBLISH_FILES:
        if (root / name).is_symlink():
            raise ValueError(f"Cannot replace a symlinked source dependency: {name}")
    journal = transaction / "PUBLISHING.json"
    journal.write_text(json.dumps({"root": str(root), "files": list(PUBLISH_FILES)}, indent=2) + "\n")
    moved = []
    try:
        for name in PUBLISH_FILES:
            source, destination = stage / name, root / name
            previous = transaction / "previous" / name
            previous.parent.mkdir(parents=True, exist_ok=True)
            existed = destination.exists()
            # Record intent first. A signal can arrive after rename succeeds
            # but before Python executes the following statement.
            moved.append((name, existed))
            if existed:
                os.replace(destination, previous)
            os.replace(source, destination)
        source_pin(root)
    except BaseException as original:
        try:
            for name, existed in reversed(moved):
                destination = root / name
                previous = transaction / "previous" / name
                if existed and not previous.exists():
                    continue  # The original rename never happened.
                if destination.exists():
                    rejected = transaction / "rejected" / name
                    rejected.parent.mkdir(parents=True, exist_ok=True)
                    os.replace(destination, rejected)
                if existed:
                    os.replace(previous, destination)
        except BaseException as rollback:
            raise RuntimeError(f"Downloader publication and rollback failed; retain recovery backups at {transaction}: {rollback}") from original
        journal.unlink()
        raise
    journal.unlink()


def update(root, runner=run, validator=validate_candidate):
    root = root.resolve()
    with update_lock(root):
        version = source_pin(root)
        setting = os.environ.get("CRYPTODESK_UPDATE_SCREENER", "1")
        if setting not in {"0", "1"}:
            raise ValueError("CRYPTODESK_UPDATE_SCREENER must be 0 (pinned/offline) or 1 (check latest)")
        if setting == "0":
            print(f"Downloader update check disabled; using verified source pin {version}.", flush=True)
            return "skipped"
        try:
            result = runner(["npm", "view", "dukascopy-node@latest", "version", "--json", *NPM_OPTIONS], cwd=root, timeout=25)
        except (OSError, subprocess.SubprocessError):
            print(f"WARNING: npm registry unavailable; keeping verified source pin dukascopy-node {version}. No update was applied.", file=sys.stderr)
            return "offline"
        latest = json.loads(result)
        if stable_version(latest) <= stable_version(version):
            print(f"Downloader pin {version}; registry latest {latest}. No upgrade needed; downgrades are never automatic.", flush=True)
            return "current"
        print(f"New downloader release: {version} -> {latest}. Staging compatibility checks before changing source pins...", flush=True)
        snapshot = input_snapshot(root)
        transaction = Path(tempfile.mkdtemp(prefix=".screener-update-", dir=root / ".tools"))
        try:
            stage = transaction / "candidate"
            validator(root, stage, latest, runner)
            publish(root, stage, transaction, snapshot)
        finally:
            # A hard interruption or failed rollback leaves both journal and
            # backups intact. The next build refuses to overwrite that evidence.
            if not (transaction / "PUBLISHING.json").exists():
                shutil.rmtree(transaction)
        print(f"Updated dukascopy-node {version} -> {latest}; package, lockfile, catalog and source dependencies passed offline checks. Rebuilding private runtime next.", flush=True)
        return "updated"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parent.parent)
    arguments = parser.parse_args()
    try:
        update(arguments.root)
    except (OSError, ValueError, RuntimeError, subprocess.SubprocessError) as error:
        detail = error.output if isinstance(error, subprocess.CalledProcessError) else str(error)
        parser.exit(1, f"Downloader update failed; build stopped: {detail}\n")


if __name__ == "__main__":
    main()

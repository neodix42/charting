#!/usr/bin/env python3
"""Compile/package our bridge; download vendor SDK only into a private build cache.

The release contains no SDK JARs. The application materializes the exact reviewed
SDK-ARTIFACTS.json entries in its private user profile on first connection.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import subprocess
import tempfile
import urllib.parse
import urllib.request
import zipfile

ROOT = Path(__file__).resolve().parent.parent
SOURCE = ROOT / "jforex-bridge"
MANIFEST = "JFOREX-PROVENANCE.json"
ARTIFACTS = "SDK-ARTIFACTS.json"
JAR = "cryptodesk-jforex.jar"
MAIN_CLASS = "cryptodesk.jforex.Bridge"
SDK_VERSION = "3.6.51"
API_VERSION = "2.13.99"
REQUIRED = {JAR, ARTIFACTS, "LICENSE", "THIRD_PARTY_NOTICES.md"}


def sha256(path):
    with Path(path).open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def artifacts(source=SOURCE):
    manifest = json.loads((Path(source) / ARTIFACTS).read_text())
    if (manifest.get("version") != 1 or manifest.get("sdkVersion") != SDK_VERSION
            or manifest.get("apiVersion") != API_VERSION or manifest.get("javaMinimum") != 17
            or manifest.get("mainClass") != MAIN_CLASS):
        raise ValueError("JForex SDK manifest differs from the reviewed versions")
    entries = manifest.get("artifacts")
    if not isinstance(entries, list) or not entries or len(entries) > 100:
        raise ValueError("Invalid JForex SDK artifact manifest")
    names = set()
    for entry in entries:
        if not isinstance(entry, dict):
            raise ValueError("Invalid JForex SDK artifact")
        name, url = entry.get("name", ""), entry.get("url", "")
        parsed = urllib.parse.urlsplit(url)
        if (not isinstance(name, str) or not re.fullmatch(r"[a-zA-Z0-9_.-]+\.jar", name)
                or name in names or name.endswith("-sources.jar")
                or parsed.scheme != "https" or parsed.hostname not in {"www.dukascopy.com", "repo.maven.apache.org"}
                or parsed.query or parsed.fragment or parsed.username or parsed.password
                or not parsed.path.endswith("/" + name)
                or not re.fullmatch(r"[0-9a-f]{64}", entry.get("sha256", ""))
                or not isinstance(entry.get("bytes"), int) or not 0 < entry["bytes"] <= 64 * 1024 * 1024):
            raise ValueError("Unsafe JForex SDK artifact manifest entry")
        names.add(name)
    if not {f"DDS2-jClient-JForex-{SDK_VERSION}.jar", f"JForex-API-{API_VERSION}.jar"}.issubset(names):
        raise ValueError("JForex SDK is missing required client/API artifacts")
    return entries


def download(url, destination, size):
    request = urllib.request.Request(url, headers={"User-Agent": "CryptoDesk-JForex-bundle/1"})
    with urllib.request.urlopen(request, timeout=60) as response, Path(destination).open("xb") as output:
        if urllib.parse.urlsplit(response.url).scheme != "https":
            raise ValueError("JForex SDK download must remain HTTPS")
        written = 0
        while block := response.read(min(1024 * 1024, size + 1 - written)):
            written += len(block)
            if written > size:
                raise ValueError("JForex SDK artifact exceeds its pinned byte length")
            output.write(block)
        if written != size:
            raise ValueError("JForex SDK artifact length differs from the reviewed pin")


def materialize_sdk(directory, entries=None, fetch=download):
    """Verified build/development cache. Never used as a release SDK distributor."""
    directory = Path(directory)
    directory.mkdir(parents=True, exist_ok=True)
    for entry in entries if entries is not None else artifacts():
        destination = directory / entry["name"]
        if destination.exists():
            if destination.is_symlink() or not destination.is_file() or destination.stat().st_size != entry["bytes"] or sha256(destination) != entry["sha256"]:
                raise ValueError(f"JForex cached artifact checksum mismatch: {entry['name']}")
            continue
        with tempfile.TemporaryDirectory(prefix=".download-", dir=directory) as temporary:
            pending = Path(temporary) / entry["name"]
            print(f"Downloading pinned JForex build dependency: {entry['name']}", flush=True)
            fetch(entry["url"], pending, entry["bytes"])
            if pending.stat().st_size != entry["bytes"] or sha256(pending) != entry["sha256"]:
                raise ValueError(f"JForex artifact checksum mismatch: {entry['name']}")
            pending.replace(destination)
    return directory


def java_tool(name):
    home = os.environ.get("JAVA_HOME")
    candidate = Path(home) / "bin" / (name + (".exe" if os.name == "nt" else "")) if home else None
    if candidate and candidate.is_file():
        return str(candidate)
    executable = shutil.which(name)
    if not executable:
        raise ValueError(f"JForex bridge builds require JDK 17 or later: {name} was not found")
    return executable


def compile_classes(cache, destination, test=False):
    javac = java_tool("javac")
    result = subprocess.run([javac, "-version"], text=True, capture_output=True, check=True)
    version = result.stdout.strip() or result.stderr.strip()
    match = re.search(r"javac (\d+)", version)
    if not match or int(match.group(1)) < 17:
        raise ValueError("JForex bridge builds require JDK 17 or later")
    sources = sorted((SOURCE / "src").rglob("*.java"))
    if test:
        sources += sorted((SOURCE / "test").rglob("*.java"))
    subprocess.run([javac, "--release", "17", "-encoding", "UTF-8", "-g:source,lines", "-cp", str(cache / "*"), "-d", str(destination), *map(str, sources)], check=True)
    shutil.copyfile(SOURCE / "src/cryptodesk-log4j.properties", destination / "cryptodesk-log4j.properties")
    return version


def archive_jar(classes, destination):
    with zipfile.ZipFile(destination, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as archive:
        entries = {"META-INF/MANIFEST.MF": f"Manifest-Version: 1.0\r\nMain-Class: {MAIN_CLASS}\r\n\r\n".encode()}
        entries.update({path.relative_to(classes).as_posix(): path.read_bytes() for path in sorted(classes.rglob("*")) if path.is_file()})
        for name, data in entries.items():
            info = zipfile.ZipInfo(name, date_time=(2020, 1, 1, 0, 0, 0))
            info.compress_type = zipfile.ZIP_DEFLATED
            info.external_attr = 0o100644 << 16
            archive.writestr(info, data)


def validate_inventory(manifest, files, read):
    """Validate either an extracted runtime or an archive supplied by a release reader."""
    if (manifest.get("version") != 1 or manifest.get("sdkVersion") != SDK_VERSION
            or manifest.get("apiVersion") != API_VERSION or manifest.get("javaMinimum") != 17
            or manifest.get("mainClass") != MAIN_CLASS):
        raise ValueError("JForex bridge provenance versions differ from the reviewed pins")
    inventory = manifest.get("files")
    names = set(files)
    if not isinstance(inventory, dict) or set(inventory) != names - {MANIFEST} or names != REQUIRED | {MANIFEST}:
        raise ValueError("JForex bridge release inventory is incomplete or contains vendor SDK artifacts")
    for name, record in inventory.items():
        if PurePosixPath(name).is_absolute() or ".." in PurePosixPath(name).parts or "\\" in name:
            raise ValueError("Unsafe JForex bridge inventory name")
        data = read(name)
        if not isinstance(record, dict) or record.get("bytes") != len(data) or hashlib.sha256(data).hexdigest() != record.get("sha256"):
            raise ValueError(f"JForex bridge inventory checksum mismatch: {name}")
    if read(ARTIFACTS) != (SOURCE / ARTIFACTS).read_bytes():
        raise ValueError("JForex SDK artifact manifest differs from the reviewed source")
    with tempfile.TemporaryDirectory(prefix="cryptodesk-jforex-verify-") as temporary:
        jar = Path(temporary) / JAR
        jar.write_bytes(read(JAR))
        with zipfile.ZipFile(jar) as archive:
            if "cryptodesk/jforex/Bridge.class" not in archive.namelist() or any(name.startswith(("com/dukascopy/", "com/google/")) for name in archive.namelist()):
                raise ValueError("JForex bridge JAR is missing bridge classes or contains redistributed vendor classes")


def validate_directory(directory):
    directory = Path(directory)
    files = {}
    for path in directory.rglob("*"):
        if path.is_symlink():
            raise ValueError("JForex bridge package must not contain symlinks")
        if path.is_file():
            files[path.relative_to(directory).as_posix()] = path.stat().st_size
    manifest = json.loads((directory / MANIFEST).read_text())
    validate_inventory(manifest, files, lambda name: (directory / name).read_bytes())
    return manifest


def publish_directory(stage, output):
    """Replace only a validated previous bundle; roll back any failed promotion."""
    stage, output = Path(stage), Path(output)
    validate_directory(stage)
    previous = stage.parent / "previous-runtime"
    had_previous = output.exists()
    if had_previous:
        validate_directory(output)
        output.replace(previous)
    try:
        stage.replace(output)
        validate_directory(output)
    except BaseException:
        if output.exists():
            shutil.rmtree(output)
        if had_previous:
            previous.replace(output)
        raise
    if had_previous:
        shutil.rmtree(previous)


def copy_runtime(source, destination):
    """Copy a reviewed bridge bundle without re-downloading or compiling its SDK."""
    source, destination = Path(source).resolve(), Path(destination).resolve()
    validate_directory(source)
    if source == destination:
        return validate_directory(destination)
    if source in destination.parents or destination in source.parents:
        raise ValueError("JForex bridge copy source and destination must be separate")
    destination.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=".jforex-copy-", dir=destination.parent) as temporary:
        stage = Path(temporary) / "runtime"
        shutil.copytree(source, stage)
        publish_directory(stage, destination)
    return validate_directory(destination)


def bundle(output, cache):
    output, cache = Path(output).resolve(), Path(cache).resolve()
    if output == ROOT or output == SOURCE or SOURCE in output.parents or output == cache or output in cache.parents or cache in output.parents:
        raise ValueError("JForex bridge output must be separate from source and SDK cache")
    materialize_sdk(cache)
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=".jforex-build-", dir=output.parent) as temporary:
        stage = Path(temporary) / "runtime"
        classes = Path(temporary) / "classes"
        stage.mkdir(); classes.mkdir()
        compiler = compile_classes(cache, classes)
        archive_jar(classes, stage / JAR)
        shutil.copyfile(SOURCE / ARTIFACTS, stage / ARTIFACTS)
        shutil.copyfile(SOURCE / "THIRD_PARTY_NOTICES.md", stage / "THIRD_PARTY_NOTICES.md")
        shutil.copyfile(ROOT / "LICENSE", stage / "LICENSE")
        source_files = [*sorted((SOURCE / "src").rglob("*")), SOURCE / ARTIFACTS]
        manifest = dict(version=1, sdkVersion=SDK_VERSION, apiVersion=API_VERSION, javaMinimum=17,
                        mainClass=MAIN_CLASS, compiler=compiler,
                        sources={path.relative_to(ROOT).as_posix(): sha256(path) for path in source_files if path.is_file()},
                        files={path.name: dict(bytes=path.stat().st_size, sha256=sha256(path)) for path in sorted(stage.iterdir())})
        (stage / MANIFEST).write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n")
        validate_directory(stage)
        publish_directory(stage, output)
    return validate_directory(output)


def test_bridge(cache):
    cache = materialize_sdk(Path(cache).resolve())
    with tempfile.TemporaryDirectory(prefix=".jforex-test-", dir=cache.parent) as temporary:
        root = Path(temporary); classes = root / "classes"; classes.mkdir()
        compile_classes(cache, classes, test=True)
        command = [java_tool("java"), f"-Duser.home={root}", f"-Djava.util.prefs.userRoot={root / 'preferences'}",
                   "-Djava.awt.headless=true", "-cp", str(classes) + os.pathsep + str(cache / "*")]
        subprocess.run([*command, "cryptodesk.jforex.BridgeTest"], check=True, timeout=30, cwd=root)
        subprocess.run([*command, "cryptodesk.jforex.BridgeResponsivenessTest"], check=True, timeout=30, cwd=root)
        subprocess.run([*command, "cryptodesk.jforex.SdkBootstrapTest", str(root / "sdk-cache")], check=True, timeout=30, cwd=root)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--copy-from", type=Path)
    parser.add_argument("--cache", type=Path, default=ROOT / ".tools/jforex-sdk/cache")
    parser.add_argument("--verify", type=Path)
    parser.add_argument("--materialize-sdk", type=Path)
    parser.add_argument("--test", action="store_true")
    args = parser.parse_args()
    if not any((args.output, args.verify, args.materialize_sdk, args.test)):
        parser.error("choose --output, --verify, --materialize-sdk, or --test")
    if args.materialize_sdk:
        materialize_sdk(args.materialize_sdk)
    if args.copy_from and not args.output:
        parser.error("--copy-from requires --output")
    if args.output:
        if args.copy_from:
            copy_runtime(args.copy_from, args.output)
        else:
            bundle(args.output, args.cache)
        print(f"Packaged CryptoDesk JForex bridge: {args.output}")
    if args.verify:
        validate_directory(args.verify)
        print("JForex bridge release inventory verified")
    if args.test:
        test_bridge(args.cache)


if __name__ == "__main__":
    main()

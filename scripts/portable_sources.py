#!/usr/bin/env python3
"""Collect exact Ubuntu source packages for redistributed installed libraries.

APT reads the host's Ubuntu archive definitions and keyrings, but all configuration,
lists, downloads and logs written by this helper stay below the supplied cache.
No package installation, source extraction or system configuration change occurs.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
from urllib.parse import urlsplit

MAX_SOURCE_BYTES = 1536 * 1024 * 1024
PACKAGE_NAME = re.compile(r"[a-z0-9][a-z0-9+.-]*(?::[a-z0-9][a-z0-9-]*)?\Z")
SOURCE_NAME = re.compile(r"[a-z0-9][a-z0-9+.-]*\Z")
SOURCE_VERSION = re.compile(r"[0-9][A-Za-z0-9.+:~\-]*\Z")
FILENAME = re.compile(r"[A-Za-z0-9][A-Za-z0-9.+~_\-]*\Z")
SHA256 = re.compile(r"[a-fA-F0-9]{64}\Z")


def digest_file(path):
    with Path(path).open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def _run(command, *, env=None, cwd=None, timeout=600):
    environment = dict(os.environ, LC_ALL="C", DPKG_COLORS="never")
    environment.update(env or {})
    try:
        result = subprocess.run(command, check=True, text=True, stdout=subprocess.PIPE,
                                stderr=subprocess.PIPE, env=environment, cwd=cwd, timeout=timeout)
    except subprocess.CalledProcessError as error:
        detail = (error.stderr or error.stdout or str(error)).strip()[-3000:]
        raise RuntimeError(f"Source collection command failed ({command[0]}): {detail}") from error
    except subprocess.TimeoutExpired as error:
        raise RuntimeError(f"Source collection command timed out ({command[0]})") from error
    return result.stdout


def control_records(text):
    """Parse Debian control paragraphs, retaining each original checksum record."""
    records, fields, lines, previous = [], {}, [], None
    for line in [*text.splitlines(), ""]:
        if not line.strip():
            if fields:
                records.append((fields, "\n".join(lines) + "\n"))
            fields, lines, previous = {}, [], None
            continue
        if line.startswith("#"):
            continue
        lines.append(line)
        if line[0].isspace():
            if previous is None:
                raise ValueError("Malformed Debian control continuation")
            fields[previous] += "\n" + line[1:]
            continue
        key, separator, value = line.partition(":")
        key = key.lower()
        if not separator or not re.fullmatch(r"[a-z0-9][a-z0-9-]*", key) or key in fields:
            raise ValueError("Malformed or duplicated Debian control field")
        fields[key], previous = value.strip(), key
    return records


def _ubuntu_uri(uri):
    parsed = urlsplit(uri)
    host = parsed.hostname or ""
    return (parsed.scheme in ("http", "https") and not parsed.username and not parsed.password
            and not parsed.query and not parsed.fragment
            and (host in ("archive.ubuntu.com", "security.ubuntu.com", "ports.ubuntu.com", "old-releases.ubuntu.com")
                 or host.endswith(".archive.ubuntu.com")))


def _resolve_runner_mirror(uri, signed_by, config_root, inputs):
    """Resolve the official runner's one local mirror-list file without executing it."""
    archive_key = "/usr/share/keyrings/ubuntu-archive-keyring.gpg"
    path = Path(config_root).absolute() / "apt-mirrors.txt"
    if signed_by.split() != [archive_key] or uri != "mirror+file:" + str(path):
        raise ValueError("Ubuntu mirror+file sources require the Ubuntu archive key and the local apt-mirrors.txt path")
    if path.is_symlink() or not path.is_file() or path.stat().st_size > 65536:
        raise ValueError("Ubuntu apt-mirrors.txt must be a regular local file of at most 64 KiB")
    alternatives = []
    for line in path.read_text(encoding="utf-8").splitlines():
        columns = line.split("#", 1)[0].split()
        if not columns:
            continue
        if not _ubuntu_uri(columns[0]) or any(not re.fullmatch(r"priority:[0-9]+", option) for option in columns[1:]):
            raise ValueError("Ubuntu mirror list must contain only official Ubuntu HTTP(S) URLs without credentials")
        alternatives.append(columns[0])
    if not alternatives:
        raise ValueError("Ubuntu apt-mirrors.txt has no usable official archive URL")
    # Deb822's multiple URIs describe separate repositories, rather than mirror
    # failover. Use the official public archive when the runner supplies it, so
    # source collection does not depend on its Azure binary-package mirror.
    selected = next((candidate for candidate in alternatives if candidate == "https://archive.ubuntu.com/ubuntu/"), alternatives[0])
    identity = {"file": str(path), "sha256": digest_file(path), "kind": "mirror-list",
                "originalURI": uri, "alternatives": alternatives, "resolvedURI": selected}
    if identity not in inputs:
        inputs.append(identity)
    return selected


def _source_stanza(fields, config_root, mirror_inputs):
    if fields.get("enabled", "yes").lower() == "no" or not set(fields.get("types", "").split()) & {"deb", "deb-src"}:
        return None
    uris = fields.get("uris", "").split()
    signed_by = fields.get("signed-by", "")
    archive_key = "/usr/share/keyrings/ubuntu-archive-keyring.gpg"
    ubuntu_key = archive_key in signed_by.split()
    if not uris or not (ubuntu_key or all(_ubuntu_uri(uri) for uri in uris)):
        return None
    uris = [_resolve_runner_mirror(uri, signed_by, config_root, mirror_inputs) if uri.startswith("mirror+file:") else uri for uri in uris]
    if any(urlsplit(uri).scheme not in ("http", "https") or urlsplit(uri).username or urlsplit(uri).password for uri in uris):
        raise ValueError("Ubuntu source repositories must use HTTP(S) without embedded credentials")
    suites, components = fields.get("suites", "").split(), fields.get("components", "").split()
    if not suites or not components:
        raise ValueError("Ubuntu archive definition needs suites and components")
    # Copy only archive identity and its key restriction. In particular, never
    # inherit trusted=yes, insecure flags, targets or disabled freshness checks.
    key = "\n ".join((signed_by or archive_key).splitlines())
    return ("Types: deb-src\nURIs: " + " ".join(uris) + "\nSuites: " + " ".join(suites)
            + "\nComponents: " + " ".join(components) + "\nSigned-By: " + key + "\n")


def ubuntu_source_configuration(config_root):
    config_root = Path(config_root)
    files = [config_root / "sources.list"]
    parts = config_root / "sources.list.d"
    if parts.is_dir():
        files.extend(sorted(path for path in parts.iterdir() if path.suffix in (".sources", ".list") and path.is_file()))
    stanzas, inputs, mirror_inputs = [], [], []
    for path in files:
        if not path.is_file():
            continue
        text = path.read_text(encoding="utf-8")
        candidates = []
        if path.suffix == ".sources":
            candidates = [fields for fields, _ in control_records(text)]
        else:
            for line in text.splitlines():
                line = line.split("#", 1)[0].strip()
                if not line or not re.match(r"deb(?:-src)?\s", line):
                    continue
                match = re.fullmatch(r"(deb(?:-src)?)\s+(?:\[([^]]*)\]\s+)?(\S+)\s+(\S+)\s+(.+)", line)
                if not match:
                    raise ValueError(f"Malformed source list entry in {path.name}")
                kind, options, uri, suite, components = match.groups()
                fields = {"types": kind, "uris": uri, "suites": suite, "components": components}
                for option in (options or "").split():
                    key, _, value = option.partition("=")
                    if key.lower() == "signed-by":
                        fields["signed-by"] = value
                candidates.append(fields)
        included = False
        for fields in candidates:
            stanza = _source_stanza(fields, config_root, mirror_inputs)
            if stanza:
                included = True
                if stanza not in stanzas:
                    stanzas.append(stanza)
        if included:
            inputs.append({"file": str(path), "sha256": digest_file(path)})
    if not stanzas:
        raise RuntimeError("No enabled Ubuntu archive definitions found; exact matching sources cannot be collected")
    return "\n".join(stanzas), inputs + mirror_inputs


def _apt_quote(path):
    return str(path).replace("\\", "\\\\").replace('"', '\\"')


def prepare_apt(cache, config_root):
    apt = Path(cache) / "apt"
    for directory in ("config-parts", "sources.list.d", "state/lists/partial", "cache/archives/partial", "log"):
        (apt / directory).mkdir(parents=True, exist_ok=True)
    sources, inputs = ubuntu_source_configuration(config_root)
    (apt / "sources.list").write_text("")
    (apt / "sources.list.d/ubuntu.sources").write_text(sources)
    (apt / "empty.conf").write_text("")
    # APT_CONFIG is loaded before ordinary apt.conf.d files. Redirecting both
    # configuration locations here prevents host update hooks running at all.
    paths = {
        "Dir::Etc::main": apt / "empty.conf", "Dir::Etc::parts": apt / "config-parts",
        "Dir::Etc::sourcelist": apt / "sources.list", "Dir::Etc::sourceparts": apt / "sources.list.d",
        "Dir::Etc::trusted": Path(config_root) / "trusted.gpg",
        "Dir::Etc::trustedparts": Path(config_root) / "trusted.gpg.d",
        "Dir::State": apt / "state", "Dir::State::lists": apt / "state/lists",
        "Dir::State::status": Path("/var/lib/dpkg/status"),
        "Dir::Cache": apt / "cache", "Dir::Cache::archives": apt / "cache/archives",
        "Dir::Cache::pkgcache": apt / "cache/pkgcache.bin", "Dir::Cache::srcpkgcache": apt / "cache/srcpkgcache.bin",
        "Dir::Log": apt / "log",
    }
    configuration = "\n".join(f'{key} "{_apt_quote(value)}";' for key, value in paths.items())
    configuration += '''
APT::Get::AllowUnauthenticated "false";
Acquire::AllowInsecureRepositories "false";
Acquire::AllowDowngradeToInsecureRepositories "false";
Acquire::Languages "none";
Acquire::Retries "1";
Acquire::http::Timeout "30";
Acquire::https::Timeout "30";
APT::Update::Error-Mode "any";
'''
    config = apt / "portable.conf"
    config.write_text(configuration)
    return apt, {"APT_CONFIG": str(config)}, inputs


def installed_packages(packages):
    if not isinstance(packages, (list, tuple)) or not packages or len(packages) > 1024 or any(not isinstance(item, str) for item in packages):
        raise ValueError("Supply 1–1,024 exact installed binary package names")
    result = {}
    pattern = "${binary:Package}\t${Version}\t${source:Package}\t${source:Version}\t${db:Status-Status}\\n"
    for package in sorted(set(packages)):
        if not isinstance(package, str) or not PACKAGE_NAME.fullmatch(package):
            raise ValueError(f"Invalid binary package name: {package!r}")
        rows = _run(["dpkg-query", "-W", "-f=" + pattern, "--", package]).strip().splitlines()
        if len(rows) != 1:
            raise RuntimeError(f"Package {package} is ambiguous; supply its architecture-qualified installed name")
        fields = rows[0].split("\t")
        if len(fields) != 5 or fields[4] != "installed":
            raise RuntimeError(f"Package {package} is not installed")
        binary, version, source, source_version, _ = fields
        if (not PACKAGE_NAME.fullmatch(binary) or binary.split(":")[0] != package.split(":")[0]
                or (":" in package and binary != package) or not SOURCE_NAME.fullmatch(source)
                or not SOURCE_VERSION.fullmatch(version) or not SOURCE_VERSION.fullmatch(source_version)):
            raise RuntimeError(f"Could not resolve the exact installed source identity for {package}")
        result[binary] = {"binaryPackage": binary, "binaryVersion": version,
                          "sourcePackage": source, "sourceVersion": source_version}
    return sorted(result.values(), key=lambda item: item["binaryPackage"])


def checksums_sha256(value):
    result = {}
    for line in value.splitlines():
        if not line.strip():
            continue
        columns = line.split()
        if len(columns) != 3:
            raise ValueError("Malformed SHA-256 source file record")
        digest, size, name = columns
        if not SHA256.fullmatch(digest) or not size.isdecimal() or not FILENAME.fullmatch(name) or name in result:
            raise ValueError("Unsafe, duplicated or malformed source file record")
        result[name] = {"name": name, "bytes": int(size), "sha256": digest.lower()}
    if not result or any(item["bytes"] <= 0 for item in result.values()):
        raise ValueError("Source index has no complete SHA-256 file inventory")
    return result


def exact_source_record(name, version, environment):
    output = _run(["apt-cache", "--only-source", "showsrc", name], env=environment)
    matches = [(fields, raw) for fields, raw in control_records(output)
               if fields.get("package") == name and fields.get("version") == version]
    if not matches:
        raise RuntimeError(f"Exact Ubuntu source {name}={version} is unavailable in the configured archives; cannot redistribute its binary")
    inventories = [checksums_sha256(fields.get("checksums-sha256", "")) for fields, _ in matches]
    if any(inventory != inventories[0] for inventory in inventories[1:]):
        raise RuntimeError(f"Conflicting authenticated source records for {name}={version}")
    files = inventories[0]
    if sum(filename.endswith(".dsc") for filename in files) != 1:
        raise ValueError(f"Source {name}={version} must include one .dsc and its complete source archives")
    return {"package": name, "version": version, "files": files, "indexRecord": matches[0][1]}


def verify_file(path, expected):
    path = Path(path)
    if path.is_symlink() or not path.is_file() or path.stat().st_size != expected["bytes"] or digest_file(path) != expected["sha256"]:
        raise RuntimeError(f"Source SHA-256/size verification failed: {path.name}; preserve or remove this cache file before retrying")


def _dsc_payload(text):
    if text.startswith("-----BEGIN PGP SIGNED MESSAGE-----"):
        _, separator, body = text.partition("\n\n")
        if not separator or "\n-----BEGIN PGP SIGNATURE-----" not in body:
            raise ValueError("Malformed signed .dsc metadata")
        body = body.split("\n-----BEGIN PGP SIGNATURE-----", 1)[0]
        return "\n".join(line[2:] if line.startswith("- ") else line for line in body.splitlines())
    return text


def verify_source(directory, record):
    for item in record["files"].values():
        verify_file(directory / item["name"], item)
    descriptor = next(name for name in record["files"] if name.endswith(".dsc"))
    paragraphs = control_records(_dsc_payload((directory / descriptor).read_text(encoding="utf-8")))
    if len(paragraphs) != 1:
        raise ValueError("Source .dsc must contain one source descriptor")
    fields = paragraphs[0][0]
    if fields.get("source") != record["package"] or fields.get("version") != record["version"]:
        raise RuntimeError("Source .dsc identity does not match the installed library's exact source version")
    described = checksums_sha256(fields.get("checksums-sha256", ""))
    expected = {name: item for name, item in record["files"].items() if name != descriptor}
    if described != expected:
        raise RuntimeError("Source .dsc checksums do not match the authenticated APT Sources index")


def cached_source(record, cache, environment):
    identity = record["package"] + "=" + record["version"]
    directory = cache / "downloads" / (record["package"] + "-" + hashlib.sha256(identity.encode()).hexdigest()[:16])
    directory.mkdir(parents=True, exist_ok=True)
    missing = False
    for item in record["files"].values():
        path = directory / item["name"]
        if path.exists() or path.is_symlink():
            verify_file(path, item)
        else:
            missing = True
    if missing:
        print(f"Collecting exact Ubuntu source {identity}", flush=True)
        with tempfile.TemporaryDirectory(prefix=".download-", dir=directory.parent) as temporary:
            temporary = Path(temporary)
            for item in record["files"].values():
                if (directory / item["name"]).exists():
                    shutil.copyfile(directory / item["name"], temporary / item["name"])
            _run(["apt-get", "--download-only", "--only-source", "--assume-yes", "source", "--", identity],
                 env=environment, cwd=temporary)
            verify_source(temporary, record)
            for item in record["files"].values():
                os.replace(temporary / item["name"], directory / item["name"])
    verify_source(directory, record)
    return directory


def _metadata_files(apt):
    files = sorted(path for path in (apt / "state/lists").iterdir()
                   if path.is_file() and ("_Sources" in path.name or path.name.endswith(("_InRelease", "_Release", "_Release.gpg"))))
    if (not any("_Sources" in path.name for path in files)
            or not any(path.name.endswith(("_InRelease", "_Release")) for path in files)):
        raise RuntimeError("APT did not retain authenticated Ubuntu source-index and release metadata")
    if any(path.is_symlink() for path in files):
        raise RuntimeError("Unexpected symlink in the isolated APT metadata cache")
    return files


def collect_sources(packages, destination, cache, *, apt_config_root=Path("/etc/apt"), max_bytes=MAX_SOURCE_BYTES):
    """Return/write a manifest, publishing complete verified sources atomically.

    Destination must be absent or empty. A corrupt cached download is rejected
    explicitly instead of silently replacing evidence of a failed verification.
    """
    destination, cache = Path(destination).absolute(), Path(cache).absolute()
    if destination.is_symlink() or (destination.exists() and (not destination.is_dir() or any(destination.iterdir()))):
        raise ValueError("Source destination must be absent or an empty regular directory")
    if cache == destination or cache.is_relative_to(destination) or destination.is_relative_to(cache):
        raise ValueError("Source cache and package destination must be separate directories")
    if cache.is_symlink() or not isinstance(max_bytes, int) or not 0 < max_bytes <= MAX_SOURCE_BYTES:
        raise ValueError("Invalid source cache or source size limit (maximum 1.5 GiB)")
    binaries = installed_packages(packages)
    cache.mkdir(parents=True, exist_ok=True)
    apt, environment, definitions = prepare_apt(cache, apt_config_root)
    print("Refreshing isolated Ubuntu source indexes (no system APT changes)", flush=True)
    _run(["apt-get", "update"], env=environment, cwd=apt)
    metadata = _metadata_files(apt)
    records = [exact_source_record(name, version, environment)
               for name, version in sorted({(item["sourcePackage"], item["sourceVersion"]) for item in binaries})]
    expected_bytes = sum(path.stat().st_size for path in metadata) + sum(
        item["bytes"] for record in records for item in record["files"].values())
    if expected_bytes > max_bytes:
        raise RuntimeError(f"Corresponding source bundle exceeds the {max_bytes / 1024**2:.0f} MiB limit; no source archives downloaded")
    for record in records:
        record["cache"] = cached_source(record, cache, environment)
    destination.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=".portable-sources-", dir=destination.parent) as temporary:
        stage = Path(temporary) / "sources"
        stage.mkdir()
        manifest = {"formatVersion": 1, "distribution": "Ubuntu", "binaryPackages": binaries,
                    "verification": "Exact installed source versions; SHA-256 checked against authenticated APT Sources indexes and .dsc descriptors",
                    "repositoryDefinitions": definitions, "sources": [], "aptMetadata": []}
        for record in records:
            relative = Path("packages") / record["cache"].name
            target = stage / relative
            target.mkdir(parents=True)
            for item in record["files"].values():
                shutil.copyfile(record["cache"] / item["name"], target / item["name"])
            verify_source(target, record)
            raw = record["indexRecord"].encode()
            (target / "SOURCE-INDEX-RECORD.txt").write_bytes(raw)
            manifest["sources"].append({"package": record["package"], "version": record["version"],
                                        "directory": relative.as_posix(),
                                        "files": sorted(record["files"].values(), key=lambda item: item["name"]),
                                        "sourceIndexRecordSHA256": hashlib.sha256(raw).hexdigest()})
        (stage / "apt-metadata").mkdir()
        for source in metadata:
            target = stage / "apt-metadata" / source.name
            shutil.copyfile(source, target)
            manifest["aptMetadata"].append({"name": source.name, "bytes": target.stat().st_size, "sha256": digest_file(target)})
        manifest["sourceArchiveBytes"] = sum(item["bytes"] for record in records for item in record["files"].values())
        manifest["bundledBytesBeforeManifest"] = sum(path.stat().st_size for path in stage.rglob("*") if path.is_file())
        encoded = (json.dumps(manifest, indent=2) + "\n").encode()
        if manifest["bundledBytesBeforeManifest"] + len(encoded) > max_bytes:
            raise RuntimeError("Corresponding sources and verification metadata exceed the source bundle size limit")
        (stage / "portable-sources.json").write_bytes(encoded)
        if destination.exists():
            destination.rmdir()
        os.replace(stage, destination)
    return manifest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--destination", required=True, type=Path)
    parser.add_argument("--cache", required=True, type=Path)
    parser.add_argument("--max-bytes", default=MAX_SOURCE_BYTES, type=int)
    parser.add_argument("packages", nargs="+")
    arguments = parser.parse_args()
    try:
        report = collect_sources(arguments.packages, arguments.destination, arguments.cache, max_bytes=arguments.max_bytes)
    except (OSError, ValueError, RuntimeError) as error:
        raise SystemExit(f"Portable corresponding-source collection failed: {error}")
    print(f"Collected {len(report['sources'])} exact source packages ({report['sourceArchiveBytes'] / 1024**2:.1f} MiB archives)")


if __name__ == "__main__":
    main()

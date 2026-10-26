#!/usr/bin/env python3
"""Plan, verify and publish main-branch desktop releases.

Only the publish subcommand writes to GitHub. The workflow serializes the entire
plan/build/publish cycle; an existing tag is never moved and published assets are
never replaced. A failed draft can be resumed by rerunning its commit's workflow.
"""
import argparse
import hashlib
import json
import mimetypes
import os
from pathlib import Path
import re
import shutil
import struct
import subprocess
import tarfile
import urllib.error
import urllib.parse
import urllib.request
import zipfile

from app_version import app_version, parse_version
from screener_bundle import MANIFEST as SCREENER_MANIFEST, validate_inventory as validate_screener

TARGETS = {"linux-amd64": ".tar.gz", "windows-amd64": ".zip"}
REPOSITORY_PATTERN = re.compile(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+")
COMMIT_PATTERN = re.compile(r"[0-9a-f]{40}")
INTEGRITY_PATTERN = re.compile(r"<!-- cryptodesk-assets: (.+?) -->")


def sha256(path):
    with Path(path).open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def git(root, *arguments):
    return subprocess.check_output(["git", *arguments], cwd=root, text=True).strip()


def tag_version(tag):
    if not tag.startswith("v"):
        return None
    try:
        return parse_version(tag[1:])
    except ValueError:
        return None


def archive_names(version):
    parse_version(version)
    return [f"cryptodesk-{version}-{target}{suffix}" for target, suffix in TARGETS.items()]


def legacy_archive_names(version):
    """Recognize completed older releases without rewriting their assets."""
    parse_version(version)
    return [name for target in ("linux-amd64", "windows-amd64")
            for name in (f"cryptodesk-{version}-{target}{TARGETS[target]}", f"cryptodesk-{target}{TARGETS[target]}")]


def marker(tag, commit):
    return f"<!-- cryptodesk-release: {tag} {commit} -->"


def asset_integrity(paths):
    return {path.name: {"sha256": sha256(path), "bytes": path.stat().st_size} for path in paths}


def integrity_marker(integrity):
    return "<!-- cryptodesk-assets: " + json.dumps(integrity, sort_keys=True, separators=(",", ":")) + " -->"


def validate_published(release, tag, commit):
    if release.get("prerelease"):
        raise ValueError("Existing release is a prerelease and cannot provide stable latest downloads")
    if release["tag_name"] != tag or marker(tag, commit) not in (release.get("body") or ""):
        raise ValueError("Existing release was not created for this version and exact commit; refusing to replace it")
    assets = {asset["name"]: asset for asset in release.get("assets", [])}
    current_names = archive_names(tag[1:])
    # Completed releases from the retired three-archive format stay immutable
    # and checksum-verified. This does not make the old target publishable.
    bundled_names = [*current_names, f"cryptodesk-{tag[1:]}-linux-amd64-portable.tar.gz"]
    supported = [set(names) | {"SHA256SUMS", "release.json"}
                 for names in (current_names, bundled_names, legacy_archive_names(tag[1:]))]
    if set(assets) not in supported or len(assets) != len(release.get("assets", [])) or any(asset.get("state") != "uploaded" or asset.get("size", 0) <= 0 for asset in assets.values()):
        raise ValueError("Published release has incomplete or unexpected assets; refusing to modify a published release")
    expected = set(assets)
    matches = INTEGRITY_PATTERN.findall(release.get("body") or "")
    integrity = json.loads(matches[0]) if len(matches) == 1 else {}
    if set(integrity) != expected or any(not isinstance(integrity[name], dict)
            or not re.fullmatch(r"[0-9a-f]{64}", str(integrity[name].get("sha256", "")))
            or asset.get("digest") != "sha256:" + integrity[name]["sha256"]
            or asset["size"] != integrity[name].get("bytes") for name, asset in assets.items()):
        raise ValueError("Published asset checksums differ from the recorded release; refusing to overwrite published assets")


def choose_plan(baseline, commit, tags, releases):
    """Pure policy: reuse this commit's reservation; otherwise increment the maximum."""
    floor = parse_version(baseline)
    versions = {tag: version for tag in tags if (version := tag_version(tag)) is not None}
    by_tag = {}
    for release in releases:
        tag = release["tag_name"]
        if (version := tag_version(tag)) is not None:
            versions[tag] = version
            by_tag[tag] = release
    reservations = [tag for tag in versions if tags.get(tag) == commit]
    # Drafts created by this script retain the exact SHA, rather than a branch
    # name, even if tag creation/publishing was interrupted.
    reservations += [tag for tag, release in by_tag.items()
                     if release.get("target_commitish") == commit and tag not in tags
                     and marker(tag, commit) in (release.get("body") or "")]
    if reservations:
        tag = max(reservations, key=versions.get)
        release = by_tag.get(tag)
        published = bool(release and not release["draft"])
        if published:
            validate_published(release, tag, commit)
        return {"version": tag[1:], "tag": tag, "commit": commit, "published": published}
    maximum = max(versions.values(), default=None)
    if maximum is None or maximum < floor:
        version = baseline
    else:
        version = ".".join(map(str, (maximum[0], maximum[1], maximum[2] + 1)))
        parse_version(version)
    return {"version": version, "tag": f"v{version}", "commit": commit, "published": False}


class GitHub:
    def __init__(self, repository, token, base_url="https://api.github.com"):
        if not REPOSITORY_PATTERN.fullmatch(repository):
            raise ValueError("Repository must be owner/name")
        if not token:
            raise ValueError("GH_TOKEN or GITHUB_TOKEN is required")
        self.repository = repository
        self.token = token
        self.base_url = base_url.rstrip("/")
        class NoRedirect(urllib.request.HTTPRedirectHandler):
            def redirect_request(self, request, response, code, message, headers, new_url):
                raise ValueError("Unexpected redirect from GitHub API; credentials were not forwarded")
        self.opener = urllib.request.build_opener(NoRedirect())

    def request(self, method, path, payload=None, upload=None):
        url = path if path.startswith("https://") else f"{self.base_url}/repos/{self.repository}/{path}"
        parsed = urllib.parse.urlsplit(url)
        # Release upload URLs come from GitHub's API. Never send a token to an
        # arbitrary host or accept a redirect that forwards the token elsewhere.
        if parsed.scheme != "https" or parsed.hostname not in {"api.github.com", "uploads.github.com"}:
            raise ValueError("Unexpected GitHub API/upload host")
        headers = {"Authorization": f"Bearer {self.token}", "Accept": "application/vnd.github+json",
                   "X-GitHub-Api-Version": "2026-03-10", "User-Agent": "CryptoDesk-release"}
        if upload:
            path = Path(upload)
            headers.update({"Content-Type": mimetypes.guess_type(path.name)[0] or "application/octet-stream",
                            "Content-Length": str(path.stat().st_size)})
            # urllib/http.client streams a file object rather than allocating a
            # second copy of the 270+ MiB Windows portable archive in memory.
            with path.open("rb") as stream:
                request = urllib.request.Request(url, data=stream, method=method, headers=headers)
                with self.opener.open(request, timeout=600) as response:
                    return json.load(response)
        data = None if payload is None else json.dumps(payload).encode("utf-8")
        if data is not None:
            headers["Content-Type"] = "application/json"
        request = urllib.request.Request(url, data=data, method=method, headers=headers)
        with self.opener.open(request, timeout=60) as response:
            return None if response.status == 204 else json.load(response)

    def pages(self, path):
        result = []
        for page in range(1, 10001):
            items = self.request("GET", f"{path}?per_page=100&page={page}")
            result.extend(items)
            if len(items) < 100:
                return result
        raise ValueError("GitHub pagination exceeded its safety limit")

    def tag_commit(self, tag):
        try:
            value = self.request("GET", f"git/ref/tags/{urllib.parse.quote(tag, safe='')}")["object"]
        except urllib.error.HTTPError as error:
            if error.code == 404:
                return None
            raise
        for _ in range(10):
            if value["type"] == "commit":
                return value["sha"]
            if value["type"] != "tag":
                break
            value = self.request("GET", f"git/tags/{value['sha']}")["object"]
        raise ValueError(f"Tag {tag} does not resolve to a commit")

    def workflow_files(self, commit):
        def visit(path):
            try:
                entries = self.request("GET", f"contents/{path}?ref={commit}")
            except urllib.error.HTTPError as error:
                if error.code == 404:
                    return {}
                raise
            result = {}
            for entry in entries:
                if entry["type"] == "dir":
                    result.update(visit(entry["path"]))
                else:
                    result[entry["path"]] = entry["sha"]
            return result
        return visit(".github/workflows")


def main_workflows_differ(api, commit):
    # Checkout can reset origin/main to an older push's event SHA. Read the real
    # remote head rather than trusting that local tracking ref in queued runs.
    main_commit = api.request("GET", "git/ref/heads/main")["object"]["sha"]
    return main_commit != commit and api.workflow_files(main_commit) != api.workflow_files(commit)


def plan(root, api, commit):
    if not COMMIT_PATTERN.fullmatch(commit) or git(root, "rev-parse", "HEAD") != commit:
        raise ValueError("Release commit must be the checked-out full SHA")
    tags = {tag: git(root, "rev-parse", f"refs/tags/{tag}^{{commit}}")
            for tag in git(root, "tag", "--list", "v*").splitlines() if tag_version(tag) is not None}
    releases = api.pages("releases")
    result = choose_plan(app_version(root, {}), commit, tags, releases)
    if not result["published"] and main_workflows_differ(api, commit):
        # GITHUB_TOKEN cannot publish old workflow contents that differ from the
        # current default branch (GitHub's Workflows permission restriction).
        # The newer main push includes these changes and owns the next release.
        return dict(result, skip=True, superseded=True)
    if not result["published"]:
        # Queued runs start in FIFO waiting order, which can differ from push
        # order. An older event already included in a published descendant must
        # never replace that descendant as the latest download.
        for release in sorted(releases, key=lambda item: tag_version(item["tag_name"]) or (-1, -1, -1), reverse=True):
            tag = release["tag_name"]
            released_commit = tags.get(tag)
            if release["draft"] or not tag_version(tag) or not released_commit or released_commit == commit:
                continue
            comparison = subprocess.run(["git", "merge-base", "--is-ancestor", commit, released_commit], cwd=root)
            if comparison.returncode not in (0, 1):
                raise ValueError("Could not verify release commit ancestry")
            if comparison.returncode == 0:
                validate_published(release, tag, released_commit)
                return {"version": tag[1:], "tag": tag, "commit": commit, "published": True, "skip": True, "superseded": True}
    return dict(result, skip=result["published"], superseded=False)


def stage(root, target, version, directory):
    if target not in TARGETS:
        raise ValueError("Unknown release target")
    parse_version(version)
    directory.mkdir(parents=True, exist_ok=True)
    versioned = f"cryptodesk-{version}-{target}{TARGETS[target]}"
    source = root / versioned
    if not source.is_file() or source.is_symlink():
        raise ValueError(f"Missing release archive: {source}")
    shutil.copyfile(source, directory / versioned)


def archive_manifest(path, target, version):
    if target not in TARGETS:
        raise ValueError("Unknown release target")
    prefix = f"cryptodesk-{version}-{target}/"
    if target == "linux-amd64":
        with tarfile.open(path, "r:gz") as archive:
            names = archive.getnames()
            if any(not (name == prefix[:-1] or name.startswith(prefix)) or ".." in Path(name).parts for name in names):
                raise ValueError("Unsafe Linux archive paths")
            if len(names) != len(set(names)):
                raise ValueError("Duplicate Linux archive paths")
            if any(not (entry.isdir() or entry.isfile()) for entry in archive.getmembers()):
                raise ValueError("Linux release archive must contain regular files/directories only")
            def regular(name, executable=False):
                try:
                    entry = archive.getmember(prefix + name)
                except KeyError as error:
                    raise ValueError(f"Linux release file is missing: {name}") from error
                if not entry.isfile() or entry.size <= 0 or (executable and not entry.mode & 0o111):
                    raise ValueError(f"Linux release file is missing, empty or not executable: {name}")
                return entry

            def executable(name):
                with archive.extractfile(regular(name, executable=True)) as stream:
                    header = stream.read(20)
                if len(header) != 20 or header[:6] != b"\x7fELF\x02\x01" or struct.unpack_from("<H", header, 18)[0] != 62:
                    raise ValueError(f"Expected an x64 Linux ELF binary: {name}")

            executable("cryptodesk")
            screener_files = {entry.name.removeprefix(prefix + "screener-runtime/"): entry for entry in sorted(archive.getmembers(), key=lambda entry: entry.offset_data)
                              if entry.name.startswith(prefix + "screener-runtime/") and entry.isfile()}
            with archive.extractfile(regular("screener-runtime/" + SCREENER_MANIFEST)) as stream:
                screener_manifest = json.load(stream)
            def read_screener(name):
                with archive.extractfile(screener_files[name]) as stream:
                    return stream.read()
            validate_screener(screener_manifest, "linux-amd64",
                              {name: (entry.size, bool(entry.mode & 0o111)) for name, entry in screener_files.items()}, read_screener)
            with archive.extractfile(regular("version.json")) as stream:
                return json.load(stream)
    with zipfile.ZipFile(path) as archive:
        if archive.testzip():
            raise ValueError("Windows ZIP integrity failure")
        if any(not name.startswith(prefix) or ".." in Path(name).parts for name in archive.namelist()):
            raise ValueError("Unsafe Windows archive paths")
        for required in ("cryptodesk.exe", "portable.marker", "WebView2/msedgewebview2.exe", "WebView2/Locales/en-US.pak"):
            if not archive.getinfo(prefix + required).file_size:
                raise ValueError(f"Missing Windows portable file {required}")
        with archive.open(prefix + "cryptodesk.exe") as stream:
            header = stream.read(64)
            if len(header) != 64 or header[:2] != b"MZ":
                raise ValueError("Expected a Windows x64 GUI executable")
            stream.seek(struct.unpack_from("<I", header, 60)[0])
            pe = stream.read(96)
            if len(pe) != 96 or pe[:4] != b"PE\0\0" or struct.unpack_from("<H", pe, 4)[0] != 0x8664 or struct.unpack_from("<H", pe, 24)[0] != 0x20B or struct.unpack_from("<H", pe, 92)[0] != 2:
                raise ValueError("Expected a Windows x64 GUI executable")
        screener_prefix = prefix + "screener-runtime/"
        screener_files = {entry.filename.removeprefix(screener_prefix): (entry.file_size, True) for entry in archive.infolist()
                          if entry.filename.startswith(screener_prefix) and not entry.is_dir()}
        try:
            screener_manifest = json.loads(archive.read(screener_prefix + SCREENER_MANIFEST))
        except KeyError as error:
            raise ValueError("Windows package is missing the preinstalled Screener runtime") from error
        validate_screener(screener_manifest, "windows-amd64", screener_files, lambda name: archive.read(screener_prefix + name))
        return json.loads(archive.read(prefix + "version.json"))


def bundle(directory, version, commit):
    parse_version(version)
    if not COMMIT_PATTERN.fullmatch(commit):
        raise ValueError("Release commit must be a full SHA")
    names = archive_names(version)
    actual = {path.name for path in directory.iterdir() if path.name not in {"SHA256SUMS", "release.json"}}
    if actual != set(names):
        raise ValueError("Release inputs must contain exactly two versioned archives: Linux and Windows")
    for target, suffix in TARGETS.items():
        original = directory / f"cryptodesk-{version}-{target}{suffix}"
        if original.is_symlink() or not original.is_file():
            raise ValueError(f"Release archive must be a regular file: {target}")
        manifest = archive_manifest(original, target, version)
        expected_target = "linux/amd64" if target.startswith("linux-") else "windows/amd64"
        variant_matches = manifest.get("packageVariant") in (None, "standard")
        if manifest.get("app") != "CryptoDesk" or manifest.get("version") != version or manifest.get("gitRevision") != commit or manifest.get("target") != expected_target or not variant_matches:
            raise ValueError(f"Archive manifest does not match the requested release: {target}")
        if manifest.get("workingTreeModified") is not False:
            raise ValueError("CI releases must be built from an unmodified checkout")
    result = {"formatVersion": 1, "version": version, "tag": f"v{version}", "commit": commit,
              "assets": {name: {"sha256": sha256(directory / name), "bytes": (directory / name).stat().st_size} for name in names}}
    (directory / "release.json").write_text(json.dumps(result, indent=2) + "\n")
    (directory / "SHA256SUMS").write_text("".join(f"{sha256(directory / name)}  {name}\n" for name in sorted([*names, "release.json"])))
    return result


def publish(api, directory, version, commit):
    parse_version(version)
    if not COMMIT_PATTERN.fullmatch(commit):
        raise ValueError("Release commit must be a full SHA")
    tag = f"v{version}"
    resolved = api.tag_commit(tag)
    if resolved is not None and resolved != commit:
        raise ValueError(f"Tag {tag} already belongs to another commit; it will not be moved")
    existing_releases = api.pages("releases")
    if any(not release["draft"] and tag_version(release["tag_name"]) is not None
           and tag_version(release["tag_name"]) > parse_version(version) for release in existing_releases):
        raise ValueError("A newer release was published during this build; rerun to recheck version and ancestry")
    releases = [release for release in existing_releases if release["tag_name"] == tag]
    if len(releases) > 1:
        raise ValueError("Multiple releases found for the tag")
    release = releases[0] if releases else None
    if release and not release["draft"]:
        if resolved != commit:
            raise ValueError("Published release tag does not resolve to the requested commit")
        validate_published(release, tag, commit)
        print(f"{tag} already published for {commit}; existing assets preserved")
        return
    bundle(directory, version, commit)
    if release and (release.get("target_commitish") != commit or marker(tag, commit) not in (release.get("body") or "")):
        raise ValueError("Existing draft belongs to another commit or author; refusing to replace it")
    if main_workflows_differ(api, commit):
        raise ValueError("Main's release workflow changed during this build; the newer main push will publish the next release")
    # Both builds and the Actions artifact uploads have succeeded before this
    # command runs. Reserve this exact version now; never retarget a git ref.
    if resolved is None:
        api.request("POST", "git/refs", {"ref": f"refs/tags/{tag}", "sha": commit})
    paths = sorted(path for path in directory.iterdir() if path.is_file())
    integrity = asset_integrity(paths)
    if release is None:
        body = (f"CryptoDesk desktop builds from commit `{commit}`.\n\n"
                "- Ubuntu x64: extract the tar.gz and run `./cryptodesk`; GTK3, WebKitGTK 4.1 and libsoup3 runtime libraries are required. "
                "Install the Ubuntu dependencies using the apt command in [Linux runtime requirements](https://github.com/neodix42/charting#linux-runtime-requirements).\n"
                "- Windows x64: extract the complete ZIP and run `cryptodesk.exe`. The English WebView2 runtime is included.\n"
                "- Verify downloads with `SHA256SUMS`; `release.json` records the source commit and archive hashes.\n\n"
                f"{marker(tag, commit)}\n{integrity_marker(integrity)}")
        release = api.request("POST", "releases", {"tag_name": tag, "target_commitish": commit,
                              "name": f"CryptoDesk {tag}", "body": body, "draft": True,
                              "prerelease": False, "generate_release_notes": True})
    expected = {path.name for path in paths}
    assets = {asset["name"]: asset for asset in api.pages(f"releases/{release['id']}/assets")}
    if set(assets) - expected:
        raise ValueError("Existing draft has unexpected assets; refusing to remove them")
    for path in paths:
        digest = f"sha256:{sha256(path)}"
        existing = assets.get(path.name)
        if existing:
            if existing.get("state") == "uploaded" and existing.get("size") == path.stat().st_size and existing.get("digest") == digest:
                continue
            # Only an owned unpublished draft may have failed/stale uploads
            # replaced. Published release contents are always left untouched.
            api.request("DELETE", f"releases/assets/{existing['id']}")
        upload_url = release["upload_url"].split("{", 1)[0] + "?" + urllib.parse.urlencode({"name": path.name})
        uploaded = api.request("POST", upload_url, upload=path)
        if uploaded.get("state") != "uploaded" or uploaded.get("size") != path.stat().st_size or uploaded.get("digest") != digest:
            raise ValueError(f"GitHub did not confirm the uploaded asset checksum: {path.name}")
    completed = api.pages(f"releases/{release['id']}/assets")
    verified = {asset["name"]: asset for asset in completed}
    if set(verified) != expected or any(verified[path.name].get("digest") != f"sha256:{sha256(path)}" or verified[path.name].get("state") != "uploaded" for path in paths):
        raise ValueError("Draft assets failed final verification; release remains unpublished")
    if api.tag_commit(tag) != commit:
        raise ValueError("Release tag changed during upload; release remains unpublished")
    body = INTEGRITY_PATTERN.sub("", release.get("body") or "").rstrip() + "\n" + integrity_marker(integrity)
    published = api.request("PATCH", f"releases/{release['id']}", {"draft": False, "make_latest": "true", "body": body})
    if published.get("draft") is not False:
        raise ValueError("GitHub did not confirm publication")
    print(f"Published CryptoDesk {tag} for {commit}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    subcommands = parser.add_subparsers(dest="command", required=True)
    planner = subcommands.add_parser("plan", help="Read-only version selection")
    planner.add_argument("--root", type=Path, default=Path(__file__).resolve().parent.parent)
    planner.add_argument("--github-output", type=Path)
    for name in ("stage", "bundle", "publish"):
        child = subcommands.add_parser(name)
        child.add_argument("--version", required=True)
        child.add_argument("--directory", required=True, type=Path)
        if name == "stage":
            child.add_argument("--root", type=Path, default=Path(__file__).resolve().parent.parent)
            child.add_argument("--target", choices=TARGETS, required=True)
    arguments = parser.parse_args()
    if arguments.command == "stage":
        stage(arguments.root, arguments.target, arguments.version, arguments.directory)
        return
    commit = os.environ.get("GITHUB_SHA", "")
    if arguments.command == "bundle":
        bundle(arguments.directory, arguments.version, commit)
        return
    api = GitHub(os.environ.get("GITHUB_REPOSITORY", ""), os.environ.get("GH_TOKEN") or os.environ.get("GITHUB_TOKEN"))
    if arguments.command == "publish":
        publish(api, arguments.directory, arguments.version, commit)
        return
    result = plan(arguments.root, api, commit)
    print(json.dumps(result, indent=2))
    if arguments.github_output:
        with arguments.github_output.open("a") as stream:
            for key, value in result.items():
                stream.write(f"{key}={str(value).lower() if isinstance(value, bool) else value}\n")


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, KeyError, subprocess.CalledProcessError, tarfile.TarError, zipfile.BadZipFile) as error:
        raise SystemExit(f"Release failed: {error}")

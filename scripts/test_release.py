"""Exercise release ordering, integrity and resumability without remote writes."""
import copy
import io
import json
from pathlib import Path
import struct
import subprocess
import tarfile
import tempfile
import unittest
from unittest.mock import patch
import urllib.error
import zipfile

import release

COMMIT = "a" * 40
OTHER = "b" * 40


def published(tag="v0.1.0", commit=COMMIT, draft=False):
    names = [*release.archive_names(tag[1:]), "release.json", "SHA256SUMS"]
    integrity = {name: {"bytes": 20, "sha256": "1" * 64} for name in names}
    return {"id": 7, "tag_name": tag, "target_commitish": commit, "draft": draft,
            "body": release.marker(tag, commit) + "\n" + release.integrity_marker(integrity),
            "upload_url": "https://uploads.github.com/repos/example/charting/releases/7/assets{?name,label}",
            "assets": [{"id": index, "name": name, "state": "uploaded", "size": 20, "digest": "sha256:" + "1" * 64}
                       for index, name in enumerate(names, 10)]}


def portable_inputs(directory, version="0.1.0", commit=COMMIT, modified=False):
    """Small structurally real archives, without WebView2 or external toolchains."""
    for target, suffix in release.TARGETS.items():
        prefix = f"cryptodesk-{version}-{target}/"
        manifest = {"app": "CryptoDesk", "version": version, "target": target.replace("-", "/"),
                    "gitRevision": commit, "workingTreeModified": modified}
        versioned = directory / f"cryptodesk-{version}-{target}{suffix}"
        if target == "linux-amd64":
            binary = bytearray(64)
            binary[:6] = b"\x7fELF\x02\x01"
            struct.pack_into("<H", binary, 18, 62)
            with tarfile.open(versioned, "w:gz") as archive:
                for name, content, mode in [("cryptodesk", bytes(binary), 0o755), ("version.json", json.dumps(manifest).encode(), 0o644)]:
                    entry = tarfile.TarInfo(prefix + name)
                    entry.size, entry.mode = len(content), mode
                    archive.addfile(entry, io.BytesIO(content))
        else:
            binary = bytearray(160)
            binary[:2] = b"MZ"
            struct.pack_into("<I", binary, 60, 64)
            binary[64:68] = b"PE\0\0"
            struct.pack_into("<H", binary, 68, 0x8664)
            struct.pack_into("<H", binary, 88, 0x20B)
            struct.pack_into("<H", binary, 156, 2)
            with zipfile.ZipFile(versioned, "w") as archive:
                for name in ("portable.marker", "WebView2/msedgewebview2.exe", "WebView2/Locales/en-US.pak"):
                    archive.writestr(prefix + name, "portable runtime fixture")
                archive.writestr(prefix + "cryptodesk.exe", binary)
                archive.writestr(prefix + "version.json", json.dumps(manifest))
        (directory / f"cryptodesk-{target}{suffix}").write_bytes(versioned.read_bytes())


class FakeGitHub:
    def __init__(self, releases=None, tags=None):
        self.releases = copy.deepcopy(releases or [])
        self.tags = dict(tags or {})
        self.writes = []
        self.next_id = 100
        self.corrupt_upload = False
        self.fail_upload = False
        self.main_commit = COMMIT
        self.workflows = {}

    def pages(self, path):
        if path == "releases":
            return copy.deepcopy(self.releases)
        if path == "releases/7/assets":
            return copy.deepcopy(self.releases[0]["assets"])
        raise AssertionError(path)

    def tag_commit(self, tag):
        return self.tags.get(tag)

    def request(self, method, path, payload=None, upload=None):
        if method == "GET" and path == "git/ref/heads/main":
            return {"object": {"sha": self.main_commit}}
        self.writes.append((method, path, payload, str(upload) if upload else None))
        if path == "git/refs":
            self.tags[payload["ref"].removeprefix("refs/tags/")] = payload["sha"]
            return {"ref": payload["ref"]}
        if method == "POST" and path == "releases":
            value = dict(payload, id=7, assets=[], upload_url=published()["upload_url"])
            self.releases.append(value)
            return copy.deepcopy(value)
        if method == "DELETE":
            asset_id = int(path.rsplit("/", 1)[1])
            self.releases[0]["assets"] = [asset for asset in self.releases[0]["assets"] if asset["id"] != asset_id]
            return None
        if upload:
            if self.fail_upload:
                self.fail_upload = False
                raise OSError("interrupted upload")
            value = {"id": self.next_id, "name": Path(upload).name, "state": "uploaded", "size": Path(upload).stat().st_size,
                     "digest": "sha256:" + ("0" * 64 if self.corrupt_upload else release.sha256(upload))}
            self.next_id += 1
            self.releases[0]["assets"].append(value)
            return value
        if method == "PATCH":
            self.releases[0].update(payload)
            return copy.deepcopy(self.releases[0])
        raise AssertionError((method, path, payload, upload))

    def workflow_files(self, commit):
        return self.workflows.get(commit, {})


class VersionSelectionTests(unittest.TestCase):
    def test_first_release_uses_baseline_and_subsequent_releases_increment_patch(self):
        self.assertEqual(release.choose_plan("0.1.0", COMMIT, {}, [])["tag"], "v0.1.0")
        tags = {"v0.1.9": OTHER, "v0.1.10": OTHER, "v2.0.1-beta": OTHER, "other": OTHER}
        self.assertEqual(release.choose_plan("0.1.0", COMMIT, tags, [])["version"], "0.1.11")

    def test_increased_baseline_advances_minor_or_major_without_bot_commits(self):
        self.assertEqual(release.choose_plan("1.0.0", COMMIT, {"v0.9.9": OTHER}, [])["version"], "1.0.0")
        with self.assertRaises(ValueError):
            release.choose_plan("0.1.0", COMMIT, {"v0.1.65535": OTHER}, [])

    def test_reserved_tag_is_reused_for_same_commit_but_not_another_commit(self):
        tags = {"v0.1.0": COMMIT}
        result = release.choose_plan("0.1.0", COMMIT, tags, [])
        self.assertEqual(result["tag"], "v0.1.0")
        self.assertFalse(result["published"])
        self.assertEqual(release.choose_plan("0.1.0", OTHER, tags, [])["tag"], "v0.1.1")

    def test_interrupted_draft_without_tag_is_resumed_only_with_ownership_marker(self):
        draft = published(draft=True)
        self.assertEqual(release.choose_plan("0.1.0", COMMIT, {}, [draft])["tag"], "v0.1.0")
        draft["body"] = "Manually created draft"
        self.assertEqual(release.choose_plan("0.1.0", COMMIT, {}, [draft])["tag"], "v0.1.1")

    def test_completed_release_is_idempotent_and_incomplete_publication_is_rejected(self):
        value = published()
        self.assertTrue(release.choose_plan("0.1.0", COMMIT, {"v0.1.0": COMMIT}, [value])["published"])
        value["assets"].pop()
        with self.assertRaisesRegex(ValueError, "incomplete"):
            release.choose_plan("0.1.0", COMMIT, {"v0.1.0": COMMIT}, [value])

    def test_published_assets_without_commit_marker_are_not_overwritten(self):
        value = published()
        value["body"] = "Someone else's release"
        with self.assertRaisesRegex(ValueError, "refusing to replace"):
            release.choose_plan("0.1.0", COMMIT, {"v0.1.0": COMMIT}, [value])

    def test_replaced_published_asset_digest_is_detected_on_rerun(self):
        value = published()
        value["assets"][0]["digest"] = "sha256:" + "9" * 64
        with self.assertRaisesRegex(ValueError, "checksums differ"):
            release.choose_plan("0.1.0", COMMIT, {"v0.1.0": COMMIT}, [value])

    def test_prerelease_cannot_be_mistaken_for_latest_stable_downloads(self):
        value = published()
        value["prerelease"] = True
        with self.assertRaisesRegex(ValueError, "prerelease"):
            release.choose_plan("0.1.0", COMMIT, {"v0.1.0": COMMIT}, [value])


class GitPlanningTests(unittest.TestCase):
    def fixture(self, root):
        subprocess.run(["git", "init", "-q", str(root)], check=True)
        def run(*arguments):
            return release.git(root, *arguments)
        run("config", "user.email", "fixture@example.invalid")
        run("config", "user.name", "Fixture")
        (root / "VERSION").write_text("0.1.0\n")
        run("add", "VERSION")
        run("commit", "-qm", "first")
        first = run("rev-parse", "HEAD")
        run("commit", "--allow-empty", "-qm", "second")
        second = run("rev-parse", "HEAD")
        return run, first, second

    def test_readonly_plan_resolves_annotated_tags_and_skips_superseded_push(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            run, first, second = self.fixture(root)
            run("tag", "-a", "v0.1.0", "-m", "release", second)
            api = FakeGitHub([published(commit=second)])
            run("checkout", "-q", first)
            result = release.plan(root, api, first)
            self.assertTrue(result["superseded"])
            self.assertTrue(result["published"])
            self.assertEqual(api.writes, [])
            self.assertEqual(run("tag", "--list"), "v0.1.0")

    def test_descendant_new_push_gets_next_version_and_commit_must_match_checkout(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            run, first, second = self.fixture(root)
            run("tag", "v0.1.0", first)
            api = FakeGitHub([published(commit=first)])
            self.assertEqual(release.plan(root, api, second)["version"], "0.1.1")
            with self.assertRaisesRegex(ValueError, "checked-out"):
                release.plan(root, api, first)

    def test_old_workflow_push_is_skipped_when_main_has_new_workflow_content(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            run, first, second = self.fixture(root)
            workflow = root / ".github/workflows/release.yml"
            workflow.parent.mkdir(parents=True)
            workflow.write_text("name: New release workflow\n")
            run("add", ".github")
            run("commit", "-qm", "new workflow")
            actual_main = run("rev-parse", "HEAD")
            # Checkout can set its origin/main tracking ref back to event SHA.
            run("update-ref", "refs/remotes/origin/main", second)
            run("checkout", "-q", second)
            api = FakeGitHub()
            api.main_commit = actual_main
            api.workflows = {actual_main: {".github/workflows/release.yml": "new"}, second: {}}
            result = release.plan(root, api, second)
            self.assertTrue(result["skip"])
            self.assertTrue(result["superseded"])
            self.assertEqual(api.writes, [])


class BundleTests(unittest.TestCase):
    def test_bundle_verifies_source_and_architecture_and_creates_download_checksums(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            portable_inputs(root, "1.2.3")
            result = release.bundle(root, "1.2.3", COMMIT)
            self.assertEqual(result["commit"], COMMIT)
            self.assertEqual(result["tag"], "v1.2.3")
            for line in (root / "SHA256SUMS").read_text().splitlines():
                digest, name = line.split("  ", 1)
                self.assertEqual(release.sha256(root / name), digest)
            self.assertEqual(len((root / "SHA256SUMS").read_text().splitlines()), 5)
            self.assertEqual(release.bundle(root, "1.2.3", COMMIT), result)

    def test_wrong_source_commit_version_and_modified_checkout_are_rejected(self):
        for expected_commit, modified in [(OTHER, False), (COMMIT, True)]:
            with self.subTest(commit=expected_commit, modified=modified), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                portable_inputs(root, modified=modified)
                with self.assertRaises(ValueError):
                    release.bundle(root, "0.1.0", expected_commit)

    def test_missing_or_mismatched_alias_and_extra_files_are_rejected(self):
        for mutation in ("missing", "different", "extra"):
            with self.subTest(mutation=mutation), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                portable_inputs(root)
                alias = root / "cryptodesk-windows-amd64.zip"
                if mutation == "missing":
                    alias.unlink()
                elif mutation == "different":
                    alias.write_bytes(b"wrong archive")
                else:
                    (root / "private-workspace.sqlite").write_bytes(b"private")
                with self.assertRaises(ValueError):
                    release.bundle(root, "0.1.0", COMMIT)

    def test_archive_traversal_and_wrong_linux_architecture_fail_before_publication(self):
        for variant in ("traversal", "arm64"):
            with self.subTest(variant=variant), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                portable_inputs(root)
                archive = root / "cryptodesk-0.1.0-linux-amd64.tar.gz"
                with tarfile.open(archive, "r:gz") as source:
                    entries = [(entry, source.extractfile(entry).read()) for entry in source.getmembers()]
                with tarfile.open(archive, "w:gz") as target:
                    for entry, content in entries:
                        if entry.name.endswith("cryptodesk"):
                            if variant == "traversal":
                                entry.name = "../outside"
                            else:
                                data = bytearray(content)
                                struct.pack_into("<H", data, 18, 183)
                                content = bytes(data)
                        target.addfile(entry, io.BytesIO(content))
                (root / "cryptodesk-linux-amd64.tar.gz").write_bytes(archive.read_bytes())
                with self.assertRaises(ValueError):
                    release.bundle(root, "0.1.0", COMMIT)

    def test_stage_retains_versioned_archive_and_creates_exact_alias(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            portable_inputs(root, "2.1.9")
            output = root / "staged"
            release.stage(root, "windows-amd64", "2.1.9", output)
            self.assertEqual(sorted(path.name for path in output.iterdir()), ["cryptodesk-2.1.9-windows-amd64.zip", "cryptodesk-windows-amd64.zip"])
            self.assertEqual((output / "cryptodesk-windows-amd64.zip").read_bytes(), (root / "cryptodesk-2.1.9-windows-amd64.zip").read_bytes())


class PublicationTests(unittest.TestCase):
    def test_fresh_release_uploads_both_platforms_then_publishes_once(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            portable_inputs(root)
            api = FakeGitHub()
            release.publish(api, root, "0.1.0", COMMIT)
            self.assertEqual(api.tags, {"v0.1.0": COMMIT})
            self.assertFalse(api.releases[0]["draft"])
            self.assertEqual(len(api.releases[0]["assets"]), 6)
            self.assertEqual(api.writes[-1][:2], ("PATCH", "releases/7"))
            writes = copy.deepcopy(api.writes)
            release.publish(api, root, "0.1.0", COMMIT)
            self.assertEqual(api.writes, writes, "Reruns must not replace published assets")

    def test_tag_collision_and_unowned_draft_are_rejected_without_writes(self):
        for api in (FakeGitHub(tags={"v0.1.0": OTHER}), FakeGitHub([published(commit=OTHER, draft=True)])):
            with tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                portable_inputs(root)
                with self.assertRaises(ValueError):
                    release.publish(api, root, "0.1.0", COMMIT)
                self.assertEqual(api.writes, [])

    def test_bad_upload_digest_keeps_release_unpublished(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            portable_inputs(root)
            api = FakeGitHub()
            api.corrupt_upload = True
            with self.assertRaisesRegex(ValueError, "checksum"):
                release.publish(api, root, "0.1.0", COMMIT)
            self.assertTrue(api.releases[0]["draft"])
            self.assertFalse(any(method == "PATCH" for method, *_ in api.writes))

    def test_failed_upload_rerun_reuses_reserved_tag_and_draft(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            portable_inputs(root)
            api = FakeGitHub()
            api.fail_upload = True
            with self.assertRaises(OSError):
                release.publish(api, root, "0.1.0", COMMIT)
            self.assertTrue(api.releases[0]["draft"])
            release.publish(api, root, "0.1.0", COMMIT)
            self.assertFalse(api.releases[0]["draft"])
            self.assertEqual(sum(path == "git/refs" for _, path, *_ in api.writes), 1)
            self.assertEqual(sum(path == "releases" for _, path, *_ in api.writes), 1)

    def test_rebuilt_partial_draft_refreshes_integrity_before_publication(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            portable_inputs(root)
            api = FakeGitHub()
            api.corrupt_upload = True
            with self.assertRaises(ValueError):
                release.publish(api, root, "0.1.0", COMMIT)
            old_body = api.releases[0]["body"]
            # Real rerun rebuilds change ZIP timestamps/manifests. Add a harmless
            # payload to model different bytes from the same source revision.
            versioned = root / "cryptodesk-0.1.0-windows-amd64.zip"
            with zipfile.ZipFile(versioned, "a") as archive:
                archive.writestr("cryptodesk-0.1.0-windows-amd64/rebuilt.txt", "new build timestamp")
            (root / "cryptodesk-windows-amd64.zip").write_bytes(versioned.read_bytes())
            api.corrupt_upload = False
            release.publish(api, root, "0.1.0", COMMIT)
            value = api.releases[0]
            self.assertNotEqual(value["body"], old_body)
            self.assertFalse(value["draft"])
            release.validate_published(value, "v0.1.0", COMMIT)
            self.assertEqual(len(release.INTEGRITY_PATTERN.findall(value["body"])), 1)

    def test_existing_matching_draft_assets_are_not_uploaded_again(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            portable_inputs(root)
            api = FakeGitHub()
            api.corrupt_upload = True
            with self.assertRaises(ValueError):
                release.publish(api, root, "0.1.0", COMMIT)
            # Turn the already uploaded file into a verified completed upload.
            existing = api.releases[0]["assets"][0]
            existing["digest"] = "sha256:" + release.sha256(root / existing["name"])
            before = len(api.writes)
            api.corrupt_upload = False
            release.publish(api, root, "0.1.0", COMMIT)
            self.assertEqual(sum(upload is not None for _, _, _, upload in api.writes[before:]), 5)
            self.assertFalse(any(method == "DELETE" for method, *_ in api.writes))

    def test_newer_published_release_prevents_older_build_from_becoming_latest(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            portable_inputs(root)
            api = FakeGitHub([published("v0.1.1", OTHER)])
            with self.assertRaisesRegex(ValueError, "newer release"):
                release.publish(api, root, "0.1.0", COMMIT)
            self.assertEqual(api.writes, [])

    def test_main_workflow_change_mid_build_is_rejected_before_remote_writes(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            portable_inputs(root)
            api = FakeGitHub()
            api.main_commit = OTHER
            api.workflows = {COMMIT: {"release.yml": "old"}, OTHER: {"release.yml": "new"}}
            with self.assertRaisesRegex(ValueError, "workflow changed"):
                release.publish(api, root, "0.1.0", COMMIT)
            self.assertEqual(api.writes, [])


class GitHubClientTests(unittest.TestCase):
    def test_pagination_reads_every_page_without_mutations(self):
        api = release.GitHub("example/charting", "token")
        with patch.object(api, "request", side_effect=[[1] * 100, [2]]) as request:
            self.assertEqual(len(api.pages("releases")), 101)
            self.assertEqual(request.call_args_list[1].args, ("GET", "releases?per_page=100&page=2"))

    def test_unknown_upload_host_is_rejected_before_sending_credentials(self):
        api = release.GitHub("example/charting", "token")
        with self.assertRaisesRegex(ValueError, "host"):
            api.request("POST", "https://untrusted.invalid/upload")

    def test_tag_resolution_handles_annotated_and_missing_refs(self):
        api = release.GitHub("example/charting", "token")
        with patch.object(api, "request", side_effect=[{"object": {"type": "tag", "sha": OTHER}}, {"object": {"type": "commit", "sha": COMMIT}}]):
            self.assertEqual(api.tag_commit("v0.1.0"), COMMIT)
        missing = urllib.error.HTTPError("url", 404, "missing", {}, None)
        with patch.object(api, "request", side_effect=missing):
            self.assertIsNone(api.tag_commit("v0.1.0"))
        missing.close()


if __name__ == "__main__":
    unittest.main()

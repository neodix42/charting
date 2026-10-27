"""Updater policy, real staged validation and failure recovery without live market requests."""
import contextlib
import fcntl
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import update_screener as updater
import screener_bundle as bundle

ROOT = Path(__file__).resolve().parent.parent
CURRENT = bundle.pinned_dukascopy_version()
NEXT = ".".join(map(str, (*updater.stable_version(CURRENT)[:2], updater.stable_version(CURRENT)[2] + 1)))


def change_version(root, version, catalog=True):
    package = updater.read_json(root / updater.SOURCE_FILES[0])
    package["dependencies"]["dukascopy-node"] = version
    (root / updater.SOURCE_FILES[0]).write_text(json.dumps(package, indent=2) + "\n")
    lock = updater.read_json(root / updater.SOURCE_FILES[1])
    lock["packages"][""]["dependencies"]["dukascopy-node"] = version
    lock["packages"]["node_modules/dukascopy-node"]["version"] = version
    (root / updater.SOURCE_FILES[1]).write_text(json.dumps(lock, indent=2) + "\n")
    if catalog:
        data = updater.read_json(root / updater.SOURCE_FILES[2])
        data["version"] = version
        (root / updater.SOURCE_FILES[2]).write_text(json.dumps(data, indent=2) + "\n")


def fixture(root):
    updater.stage_sources(ROOT, root)
    (root / "screener-runtime/node_modules").mkdir()
    (root / "screener-runtime/node_modules/user-file").write_text("previous installed dependency edits")
    for name in (".tools/screener-runtime-linux-amd64/worker.cjs", "cryptodesk", "release/existing.tar.gz", "uncommitted.txt"):
        path = root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text("preserve " + name)
    with (root / "screener-runtime/worker.cjs").open("a") as stream:
        stream.write("\n// uncommitted source customization\n")
    return root


def ready_candidate(root, stage, version, runner):
    updater.stage_sources(root, stage)
    change_version(stage, version)
    modules = stage / "screener-runtime/node_modules"
    (modules / "dukascopy-node").mkdir(parents=True)
    (modules / "dukascopy-node/package.json").write_text(json.dumps({"version": version}))
    (modules / "new-file").write_text("validated candidate dependencies")


class UpdaterTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = fixture(Path(self.temporary.name) / "checkout with spaces")
        self.environment = patch.dict(os.environ, {"CRYPTODESK_UPDATE_SCREENER": "1"})
        self.environment.start()
        self.addCleanup(self.environment.stop)
        os.environ.pop("CRYPTODESK_SCREENER_LOCK_FD", None)
        self.output = io.StringIO()
        self.enterContext(contextlib.redirect_stdout(self.output))
        self.enterContext(contextlib.redirect_stderr(self.output))

    def registry(self, value):
        def command(args, **kwargs):
            self.assertEqual(args[:4], ["npm", "view", "dukascopy-node@latest", "version"])
            self.assertIn("--fetch-retries=0", args)
            self.assertIn("--fetch-timeout=15000", args)
            return json.dumps(value)
        return command

    def preserved(self):
        return {name: updater.fingerprint(self.root / name) for name in (
            *updater.PUBLISH_FILES, "screener-runtime/worker.cjs", ".tools/screener-runtime-linux-amd64",
            "cryptodesk", "release", "uncommitted.txt")}

    def test_newer_version_publishes_matching_pins_and_keeps_other_sources_and_artifacts(self):
        before = self.preserved()
        self.assertEqual(updater.update(self.root, self.registry(NEXT), ready_candidate), "updated")
        self.assertEqual(updater.source_pin(self.root), NEXT)
        self.assertEqual(bundle.pinned_dukascopy_version(self.root), NEXT)
        self.assertEqual(updater.read_json(self.root / "screener-runtime/node_modules/dukascopy-node/package.json")["version"], NEXT)
        after = self.preserved()
        for name in before.keys() - set(updater.PUBLISH_FILES):
            self.assertEqual(after[name], before[name], name)
        self.assertFalse(list((self.root / ".tools").glob(".screener-update-*")))

    def test_same_version_and_registry_downgrade_never_install_or_change_sources(self):
        for latest in (CURRENT, "0.0.1"):
            with self.subTest(latest=latest):
                before = self.preserved()
                with patch.object(updater, "validate_candidate") as validator:
                    self.assertEqual(updater.update(self.root, self.registry(latest), validator), "current")
                    validator.assert_not_called()
                self.assertEqual(self.preserved(), before)
        self.assertGreater(updater.stable_version("1.10.0"), updater.stable_version("1.9.99"))

    def test_invalid_or_prerelease_registry_metadata_fails_without_fallback(self):
        for latest in ("latest", "1.52.0-beta.1", "01.52.0", "1.52.0+local", [NEXT], {"error": "bad metadata"}, None):
            with self.subTest(latest=latest):
                before = self.preserved()
                with self.assertRaises(ValueError):
                    updater.update(self.root, self.registry(latest), ready_candidate)
                self.assertEqual(self.preserved(), before)
        with self.assertRaises(ValueError):
            updater.update(self.root, lambda *args, **kwargs: "not JSON", ready_candidate)

    def test_unavailable_registry_warns_and_keeps_only_consistent_existing_pins(self):
        before = self.preserved()
        for error in (OSError("offline"), subprocess.TimeoutExpired("npm", 25), subprocess.CalledProcessError(1, "npm")):
            with patch.object(updater, "run", side_effect=error) as command:
                self.assertEqual(updater.update(self.root, command, ready_candidate), "offline")
        self.assertIn("WARNING: npm registry unavailable", self.output.getvalue())
        self.assertEqual(self.preserved(), before)
        change_version(self.root, NEXT, catalog=False)
        with patch.object(updater, "run") as command, self.assertRaisesRegex(ValueError, "catalog"):
            updater.update(self.root, command, ready_candidate)
        command.assert_not_called()

    def test_skip_checks_pin_parity_without_registry_access(self):
        with patch.dict(os.environ, {"CRYPTODESK_UPDATE_SCREENER": "0"}), patch.object(updater, "run") as command:
            self.assertEqual(updater.update(self.root, command, ready_candidate), "skipped")
            command.assert_not_called()
        with patch.dict(os.environ, {"CRYPTODESK_UPDATE_SCREENER": "false"}), self.assertRaisesRegex(ValueError, "must be 0"):
            updater.update(self.root)

    def test_candidate_failure_preserves_pins_local_edits_dependencies_and_previous_outputs(self):
        before = self.preserved()
        def rejected(root, stage, version, runner):
            ready_candidate(root, stage, version, runner)
            raise ValueError("offline candle correctness regression")
        with self.assertRaisesRegex(ValueError, "candle correctness"):
            updater.update(self.root, self.registry(NEXT), rejected)
        self.assertEqual(self.preserved(), before)
        self.assertFalse(list((self.root / ".tools").glob(".screener-update-*")))

    def test_intervening_source_and_dependency_edits_abort_publication(self):
        for name in ("screener-runtime/worker.cjs", "screener-runtime/node_modules/user-file"):
            with self.subTest(name=name):
                def edited(root, stage, version, runner):
                    ready_candidate(root, stage, version, runner)
                    (root / name).write_text("new user edits during validation")
                with self.assertRaisesRegex(RuntimeError, "changed during validation"):
                    updater.update(self.root, self.registry(NEXT), edited)
                self.assertEqual(updater.source_pin(self.root), CURRENT)
                self.assertEqual((self.root / name).read_text(), "new user edits during validation")

    def test_failed_replacement_and_interrupt_after_original_moves_roll_back_all_files(self):
        original_replace = os.replace
        for interrupt in (False, True):
            with self.subTest(interrupt=interrupt):
                before = self.preserved()
                triggered = False
                def replace(source, target):
                    nonlocal triggered
                    if not triggered and interrupt and Path(source) == self.root / updater.SOURCE_FILES[0]:
                        triggered = True
                        original_replace(source, target)
                        raise KeyboardInterrupt()
                    if not triggered and not interrupt and "/candidate/" in str(source) and Path(source).name == "package-lock.json":
                        triggered = True
                        raise OSError("disk full while publishing")
                    return original_replace(source, target)
                with patch.object(updater.os, "replace", side_effect=replace), self.assertRaises((KeyboardInterrupt, OSError)):
                    updater.update(self.root, self.registry(NEXT), ready_candidate)
                self.assertTrue(triggered)
                self.assertEqual(self.preserved(), before)
                self.assertFalse(list((self.root / ".tools").glob(".screener-update-*")))

    def test_failed_rollback_retains_original_backups_and_blocks_next_update(self):
        original_replace = os.replace
        original = (self.root / updater.SOURCE_FILES[0]).read_bytes()
        def replace(source, target):
            if "/candidate/" in str(source) and Path(source).name == "package-lock.json":
                raise OSError("publication failed")
            if "/previous/" in str(source):
                raise OSError("rollback failed")
            return original_replace(source, target)
        with patch.object(updater.os, "replace", side_effect=replace), self.assertRaisesRegex(RuntimeError, "retain recovery backups"):
            updater.update(self.root, self.registry(NEXT), ready_candidate)
        journal, = (self.root / ".tools").glob(".screener-update-*/PUBLISHING.json")
        self.assertEqual((journal.parent / "previous" / updater.SOURCE_FILES[0]).read_bytes(), original)
        with self.assertRaisesRegex(RuntimeError, "interrupted downloader publication"):
            updater.update(self.root, self.registry(NEXT), ready_candidate)

    def test_lock_rejects_concurrent_update_and_inherited_lock_stays_held(self):
        lock_path = self.root / ".tools/screener-update.lock"
        with lock_path.open("a") as lock:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            with self.assertRaisesRegex(RuntimeError, "Another CryptoDesk"):
                updater.update(self.root, self.registry(CURRENT), ready_candidate)
            with patch.dict(os.environ, {"CRYPTODESK_SCREENER_LOCK_FD": str(lock.fileno())}):
                self.assertEqual(updater.update(self.root, self.registry(CURRENT), ready_candidate), "current")
            with lock_path.open("a") as other, self.assertRaises(BlockingIOError):
                fcntl.flock(other, fcntl.LOCK_EX | fcntl.LOCK_NB)

    def test_linux_and_windows_wrappers_reject_held_build_lock_before_work(self):
        for name in ("build.sh", "win-build.sh"):
            shutil.copy2(ROOT / name, self.root / name)
        (self.root / "scripts").mkdir()
        shutil.copy2(ROOT / "scripts/app_version.py", self.root / "scripts/app_version.py")
        shutil.copy2(ROOT / "VERSION", self.root / "VERSION")
        with (self.root / ".tools/screener-update.lock").open("a") as lock:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            for name in ("build.sh", "win-build.sh"):
                result = subprocess.run(["bash", str(self.root / name)], text=True, capture_output=True, timeout=10)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("Another CryptoDesk build or downloader update", result.stderr)

    def candidate_runner(self, alteration=None, fail_tests=False):
        """Substitute package acquisition only; real staged JS validates compatibility."""
        commands = []
        def command(args, **kwargs):
            commands.append(args)
            runtime = Path(kwargs["cwd"])
            if "install" in args:
                self.assertIn("--ignore-scripts", args)
                self.assertIn("--engine-strict", args)
                self.assertIn("--save-exact", args)
                self.assertIn("--fetch-retries=0", args)
                self.assertEqual(kwargs["env"]["PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD"], "1")
                shutil.copytree(ROOT / "screener-runtime/node_modules", runtime / "node_modules", symlinks=True)
                change_version(runtime.parent, NEXT, catalog=False)
                installed = runtime / "node_modules/dukascopy-node/package.json"
                package = updater.read_json(installed)
                package["version"] = NEXT
                installed.write_text(json.dumps(package))
                if alteration:
                    alteration(runtime)
                return "Offline registry/package fixture"
            if "--test" in args and fail_tests:
                raise subprocess.CalledProcessError(1, args, output="candidate candle regression")
            return updater.run(args, **kwargs)
        return command, commands

    @unittest.skipUnless(shutil.which("node") and (ROOT / "screener-runtime/node_modules/dukascopy-node").is_dir(), "requires existing pinned runtime dependencies")
    def test_candidate_runs_real_public_apis_catalog_and_all_worker_tests_before_publish(self):
        command, commands = self.candidate_runner()
        def registry_and_commands(args, **kwargs):
            return self.registry(NEXT)(args, **kwargs) if args[:2] == ["npm", "view"] else command(args, **kwargs)
        self.assertEqual(updater.update(self.root, registry_and_commands), "updated")
        self.assertTrue(any("--test" in args and "worker.test.cjs" in args and "provider-session.test.cjs" in args for args in commands))
        self.assertEqual(updater.source_pin(self.root), NEXT)

    @unittest.skipUnless(shutil.which("node") and (ROOT / "screener-runtime/node_modules/dukascopy-node").is_dir(), "requires existing pinned runtime dependencies")
    def test_staged_incompatible_browser_catalog_and_worker_failures_keep_originals(self):
        def browser(runtime):
            path = runtime / "node_modules/playwright-core/package.json"
            package = updater.read_json(path)
            package["version"] = "99.0.0"
            path.write_text(json.dumps(package))
        def category(runtime):
            generator = runtime / "generate-catalog.cjs"
            generator.write_text(generator.read_text().replace("const result =", "groups[0].name = 'Renamed group';\nconst result ="))
        def removed(runtime):
            generator = runtime / "generate-catalog.cjs"
            generator.write_text(generator.read_text().replace("const result =", "groups[0].instruments.pop();\nconst result ="))
        def timeframe(runtime):
            generator = runtime / "generate-catalog.cjs"
            generator.write_text(generator.read_text().replace("Object.values(api.Timeframe)", "Object.values(api.Timeframe).slice(1)"))
        for alteration, failure, message in ((browser, False, "Playwright/Chromium"), (category, False, "categories"),
                                            (removed, False, "instrument IDs"), (timeframe, False, "timeframes"),
                                            (None, True, "")):
            with self.subTest(message=message):
                command, commands = self.candidate_runner(alteration, failure)
                def registry_and_commands(args, **kwargs):
                    return self.registry(NEXT)(args, **kwargs) if args[:2] == ["npm", "view"] else command(args, **kwargs)
                before = self.preserved()
                with self.assertRaisesRegex((ValueError, subprocess.CalledProcessError), message):
                    updater.update(self.root, registry_and_commands)
                self.assertEqual(self.preserved(), before)
                if alteration:
                    self.assertFalse(any("--test" in args for args in commands))


if __name__ == "__main__":
    unittest.main()

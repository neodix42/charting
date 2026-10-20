"""Offline failure evidence checks; real owned subprocesses never start a GUI."""
import contextlib
import importlib.util
import io
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import Mock, patch

from smoke_signals import NativeProcess

spec = importlib.util.spec_from_file_location("smoke_portable_under_test", Path(__file__).with_name("smoke-portable.py"))
smoke = importlib.util.module_from_spec(spec)
spec.loader.exec_module(smoke)


class PortableSmokeEvidenceTests(unittest.TestCase):
    def run_main(self, root, verify):
        evidence = root / "evidence"
        evidence.mkdir()
        (evidence / "portable.json").write_text('{"passed":true,"stale":"must disappear"}')
        with patch.object(smoke, "verify_archive", side_effect=verify):
            smoke.main([str(root / "unused.tar.gz"), "--evidence", str(evidence)])

    def test_real_owned_startup_exit_surfaces_exit_code_safe_log_tail_and_failed_report(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            children = []

            def launch(_arguments, environment, log):
                child = NativeProcess([sys.executable, "-c",
                                       "print('Cannot initialize display backend', flush=True); "
                                       "print('Cookie: explicit-secret-cookie', flush=True); raise SystemExit(7)"], environment, log)
                children.append(child)
                return child

            def verify(_archive, evidence, report):
                # Initial failure state is written before validation/launch.
                self.assertFalse(json.loads((evidence / "portable.json").read_text())["passed"])
                with patch.object(smoke, "NativeProcess", side_effect=launch):
                    smoke.verify_launch([], {}, evidence / "fresh.log", root / "package", root, None, report, "fresh")

            with self.assertRaises(SystemExit) as error:
                self.run_main(root, verify)
            message = str(error.exception)
            self.assertIn("exited during startup (exit code 7)", message)
            self.assertIn("Process exit code: 7", message)
            self.assertIn("Cannot initialize display backend", message)
            self.assertNotIn("explicit-secret-cookie", message)
            report = json.loads((root / "evidence/portable.json").read_text())
            self.assertFalse(report["passed"])
            self.assertNotIn("stale", report)
            self.assertIn("exited during startup", report["error"])
            self.assertEqual(report["failure"]["exitCode"], 7)
            self.assertEqual(report["failure"]["phase"], "fresh")
            self.assertEqual(children[0].process.poll(), 7)

    def test_webkit_timeout_records_still_running_process_before_owned_cleanup(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            children = []
            original_wait = smoke.wait_for_webkit

            def launch(_arguments, environment, log):
                child = NativeProcess([sys.executable, "-c", "import time; print('Waiting for render process', flush=True); time.sleep(60)"],
                                      environment, log)
                children.append(child)
                return child

            def verify(_archive, evidence, report):
                with patch.object(smoke, "NativeProcess", side_effect=launch), \
                        patch.object(smoke, "wait_for_webkit", side_effect=lambda child: original_wait(child, timeout=.15)):
                    smoke.verify_launch([], {}, evidence / "fresh.log", root / "package", root, None, report, "fresh")

            with self.assertRaises(SystemExit) as error:
                self.run_main(root, verify)
            self.assertIn("Bundled WebKit render process did not start", str(error.exception))
            report = json.loads((root / "evidence/portable.json").read_text())
            self.assertFalse(report["passed"])
            self.assertIsNone(report["failure"]["exitCode"])
            self.assertIn(children[0].process.pid, [item["pid"] for item in report["failure"]["liveProcesses"]])
            self.assertIsNotNone(children[0].process.poll())
            self.assertEqual(children[0]._live_session_processes(), [])

    def test_wait_failure_keeps_original_reason_and_snapshot_precedes_cleanup(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            child = Mock()
            cleaned = []
            child.__enter__ = Mock(return_value=child)
            child.__exit__ = Mock(side_effect=lambda *_: cleaned.append(True))
            child.process.poll.return_value = 0
            child.process.returncode = 0
            child._live_session_processes.side_effect = lambda: [] if cleaned else [
                {"pid": 12345, "name": "WebKitWebProcess", "state": "S", "parent": 12344, "group": 12344,
                 "cmdline": "must-not-record-argv-secrets"}]
            child.wait_success.side_effect = RuntimeError("GUI/browser processes did not exit naturally: explicit test helper")
            package = root / "package"
            mappings = [{"executable": str(package / "runtime/usr/lib/WebKitWebProcess"),
                         "libraries": [str(package / "runtime/usr/lib/libwebkit2gtk-4.1.so.0")]}]
            report = {"passed": False, "launches": []}
            with patch.object(smoke, "NativeProcess", return_value=child), \
                    patch.object(smoke, "wait_for_webkit", return_value=mappings), \
                    patch.object(smoke, "process_mappings", return_value=mappings), \
                    patch.object(smoke, "read_workspace", return_value=(3, {"tabs": [{"charts": [{}]}]})), \
                    patch.object(smoke, "workspace_lock_available", return_value=False), \
                    patch.object(smoke.time, "sleep"):
                with self.assertRaisesRegex(RuntimeError, "GUI/browser processes did not exit naturally: explicit test helper"):
                    smoke.verify_launch([], {}, root / "missing.log", package, root, None, report, "restore")
            self.assertTrue(cleaned)
            self.assertEqual(report["launches"], [])
            self.assertEqual(report["failure"]["phase"], "restore")
            self.assertEqual(report["failure"]["liveProcesses"][0]["name"], "WebKitWebProcess")
            self.assertNotIn("cmdline", report["failure"]["liveProcesses"][0])
            self.assertEqual(report["failure"]["mappings"], [{"executable": "WebKitWebProcess", "bundledExecutable": True,
                              "libraries": [{"name": "libwebkit2gtk-4.1.so.0", "bundled": True}]}])

    def test_log_tail_is_byte_bounded_handles_invalid_utf8_and_redacts_sensitive_lines(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            path = root / "fresh.log"
            path.write_bytes(b"old discarded header\n" + b"x" * (smoke.LOG_TAIL_BYTES * 3) +
                             b"\nbackend error\n\xff\xff\nAuthorization: Bearer secret-test-value\nACCESS_TOKEN=other-secret\n")
            tail = smoke.log_tail(path)
            self.assertLessEqual(len(tail.encode("utf-8")), 16 * 1024)
            self.assertNotIn("old discarded header", tail)
            self.assertIn("backend error", tail)
            self.assertNotIn("secret-test-value", tail)
            self.assertNotIn("other-secret", tail)
            self.assertIn("[sensitive log line omitted]", tail)
            self.assertEqual(smoke.log_tail(root / "missing.log"), "")

    def test_validation_failure_overwrites_stale_pass_and_success_is_only_marked_after_completion(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            with self.assertRaises(SystemExit) as error:
                self.run_main(root, Mock(side_effect=ValueError("Archive checksum mismatch")))
            self.assertIn("Archive checksum mismatch", str(error.exception))
            failed = json.loads((root / "evidence/portable.json").read_text())
            self.assertFalse(failed["passed"])
            self.assertEqual(failed["error"], "Archive checksum mismatch")
            self.assertIn("finishedAtUTC", failed)
            succeeded = root / "successful"
            succeeded.mkdir()

            def verify(_archive, evidence, report):
                self.assertFalse(json.loads((evidence / "portable.json").read_text())["passed"])
                report["launches"].append({"phase": "fresh"})

            with contextlib.redirect_stdout(io.StringIO()):
                self.run_main(succeeded, verify)
            passed = json.loads((succeeded / "evidence/portable.json").read_text())
            self.assertTrue(passed["passed"])
            self.assertEqual(passed["launches"], [{"phase": "fresh"}])
            self.assertNotIn("error", passed)


if __name__ == "__main__":
    unittest.main()

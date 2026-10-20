"""Check terminal signal delivery and lock verification without a desktop session."""
from contextlib import closing
import json
import os
from pathlib import Path
import signal
import sqlite3
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

from smoke_signals import NativeProcess, load_report, read_workspace, wait_failure, wait_ready, workspace_lock_available


class SignalHarnessTests(unittest.TestCase):
    def wait_file(self, child, path):
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            if path.exists():
                return
            if child.process.poll() is not None:
                self.fail(f"Signal fixture exited before readiness: {child.process.returncode}")
            time.sleep(.005)
        self.fail("Signal fixture did not become ready")

    def test_actual_terminal_ctrl_c_reaches_its_foreground_process(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            ready, output = root / "ready", root / "terminal.log"
            code = ("import os,signal,sys;from pathlib import Path;"
                    "signal.signal(signal.SIGINT,lambda value,_:(print('caught',value,flush=True),sys.exit(0)));"
                    "Path(sys.argv[1]).write_text(str(os.getpid()));signal.pause()")
            with NativeProcess([sys.executable, "-c", code, str(ready)], dict(os.environ), output) as child:
                self.wait_file(child, ready)
                self.assertEqual(os.getsid(child.process.pid), child.process.pid)
                self.assertEqual(os.tcgetpgrp(child.master), child.process.pid)
                child.interrupt_terminal()
                self.assertEqual(child.wait_success(timeout=5), 0)
            self.assertIn("caught 2", output.read_text())

    def test_direct_sigterm_reaches_only_the_owned_child(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            ready, output = root / "ready", root / "term.log"
            code = ("import signal,sys;from pathlib import Path;"
                    "signal.signal(signal.SIGTERM,lambda value,_:(print('caught',value,flush=True),sys.exit(0)));"
                    "Path(sys.argv[1]).write_text('ready');signal.pause()")
            with NativeProcess([sys.executable, "-c", code, str(ready)], dict(os.environ), output) as child:
                self.wait_file(child, ready)
                child.send_signal(signal.SIGTERM)
                self.assertEqual(child.wait_success(timeout=5), 0)
            self.assertIn("caught 15", output.read_text())

    def test_nonzero_exit_is_not_reported_as_graceful_success(self):
        with tempfile.TemporaryDirectory() as temporary:
            with NativeProcess([sys.executable, "-c", "raise SystemExit(7)"], dict(os.environ), Path(temporary) / "failure.log") as child:
                with self.assertRaisesRegex(RuntimeError, "exited with 7"):
                    child.wait_success(timeout=5)

    def test_worker_group_cleanup_stays_inside_the_owned_terminal_session(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            ready = root / "ready"
            code = ("import os,signal,subprocess,sys;from pathlib import Path;"
                    "worker=subprocess.Popen([sys.executable,'-c','import time;time.sleep(30)'],preexec_fn=lambda:os.setpgid(0,0));"
                    "signal.signal(signal.SIGTERM,lambda *_:(worker.terminate(),worker.wait(),sys.exit(0)));"
                    "Path(sys.argv[1]).write_text(str(worker.pid));signal.pause()")
            with NativeProcess([sys.executable, "-c", code, str(ready)], dict(os.environ), root / "groups.log") as child:
                self.wait_file(child, ready)
                worker_pid = int(ready.read_text())
                groups = child._session_groups()
                self.assertIn(child.process.pid, groups)
                self.assertIn(worker_pid, groups)
                self.assertNotIn(os.getpgrp(), groups, "Harness cleanup must not target the invoking shell/session")
                child.send_signal(signal.SIGTERM)
                child.wait_success(timeout=5)

    def test_success_does_not_hide_a_living_worker_with_forced_cleanup(self):
        with tempfile.TemporaryDirectory() as temporary:
            with NativeProcess([sys.executable, "-c", "pass"], dict(os.environ), Path(temporary) / "leak.log") as child:
                leaked = [{"pid": child.process.pid + 1, "group": child.process.pid + 1, "state": "S", "name": "WebKit fixture"}]
                with patch.object(child, "_live_session_processes", return_value=leaked), patch.object(child, "_kill_owned_group") as forced:
                    with self.assertRaisesRegex(RuntimeError, "did not exit naturally"):
                        child.wait_success(timeout=5, natural_exit_timeout=.01)
                    self.assertFalse(child.natural_exit_verified)
                    self.assertEqual(child.remaining_live_processes, leaked)
                    forced.assert_not_called()

    def test_partial_or_missing_report_cannot_satisfy_readiness(self):
        with tempfile.TemporaryDirectory() as temporary:
            report = Path(temporary) / "ready.json"
            self.assertIsNone(load_report(report))
            report.write_text('{"passed":')
            self.assertIsNone(load_report(report))
            value = {"passed": True, "signalReady": True}
            report.write_text(json.dumps(value))
            with NativeProcess([sys.executable, "-c", "import time;time.sleep(30)"], dict(os.environ), Path(temporary) / "wait.log") as child:
                self.assertEqual(wait_ready(child, report, timeout=1), value)

    def test_preparation_failure_is_reported_instead_of_sending_a_signal(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            report = root / "ready.json"
            report.write_text(json.dumps({"passed": False, "error": "pending edit assertion failed"}))
            with NativeProcess([sys.executable, "-c", "import time;time.sleep(30)"], dict(os.environ), root / "wait.log") as child:
                with self.assertRaisesRegex(RuntimeError, "pending edit assertion failed"):
                    wait_ready(child, report, timeout=1)

    def test_failure_stage_requires_explicit_paused_close_confirmation(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            report = root / "failure.json"
            ready = {"passed": True, "signalReady": True, "syntheticSaveFailure": True}
            report.write_text(json.dumps(ready))
            with NativeProcess([sys.executable, "-c", "import time;time.sleep(30)"], dict(os.environ), root / "wait.log") as child:
                with self.assertRaisesRegex(RuntimeError, "paused-close UI"):
                    wait_failure(child, report, timeout=.02)
                failed = dict(ready, signalSaveFailureObserved=True, injectedFailureCount=1)
                report.write_text(json.dumps(failed))
                self.assertEqual(wait_failure(child, report, timeout=1), failed)

    def test_workspace_lock_probe_detects_held_lock_and_kernel_release(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            lock, ready = root / "workspace.sqlite.lock", root / "ready"
            lock.write_bytes(b"")
            code = ("import fcntl,signal,sys;from pathlib import Path;"
                    "lock=open(sys.argv[1],'r+b');fcntl.flock(lock,fcntl.LOCK_EX);"
                    "signal.signal(signal.SIGTERM,lambda *_:sys.exit(0));"
                    "Path(sys.argv[2]).write_text('ready');signal.pause()")
            with NativeProcess([sys.executable, "-c", code, str(lock), str(ready)], dict(os.environ), root / "lock.log") as child:
                self.wait_file(child, ready)
                self.assertFalse(workspace_lock_available(root))
                child.send_signal(signal.SIGTERM)
                child.wait_success(timeout=5)
            self.assertTrue(workspace_lock_available(root))

    def test_sqlite_probe_reads_revision_and_workspace_without_writing(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            database_path = root / "workspace.sqlite"
            workspace = {"id": "fixture-workspace", "tabs": [{"charts": [{"timeframe": "4h"}]}]}
            with closing(sqlite3.connect(database_path)) as database, database:
                database.execute("CREATE TABLE workspace_state(id INTEGER PRIMARY KEY, revision INTEGER, json_document TEXT)")
                database.execute("INSERT INTO workspace_state VALUES(1,7,?)", [json.dumps(workspace)])
            before = database_path.read_bytes()
            self.assertEqual(read_workspace(root), (7, workspace))
            self.assertEqual(database_path.read_bytes(), before)


if __name__ == "__main__":
    unittest.main()

#!/usr/bin/env python3
"""Send actual terminal/OS signals to a real desktop app and verify durable edits.

Requires a production desktop_smoke-tagged CryptoDesk binary and a working Linux
desktop session. Each scenario gets its own temporary workspace and process group.
Normal deterministic tests import the helpers but never launch the desktop app.
"""
import argparse
from contextlib import closing
import datetime
import fcntl
import json
import os
from pathlib import Path
import pty
import signal
import sqlite3
import subprocess
import tempfile
import termios
import time


def controlling_terminal():
    os.setsid()
    fcntl.ioctl(0, termios.TIOCSCTTY, 0)
    os.tcsetpgrp(0, os.getpid())


class NativeProcess:
    """Own one foreground terminal process group; cleanup never targets others."""
    def __init__(self, arguments, environment, log_path):
        self.natural_exit_verified = False
        self.remaining_live_processes = None
        self.log = Path(log_path).open("wb")
        self.master, slave = pty.openpty()
        try:
            self.process = subprocess.Popen(arguments, stdin=slave, stdout=self.log, stderr=self.log,
                                            env=environment, preexec_fn=controlling_terminal)
        except BaseException:
            os.close(self.master)
            self.log.close()
            raise
        finally:
            os.close(slave)

    def interrupt_terminal(self):
        if self.process.poll() is not None:
            raise RuntimeError("Desktop process exited before terminal Ctrl+C")
        if os.tcgetpgrp(self.master) != self.process.pid:
            raise RuntimeError("The desktop app does not own its isolated terminal foreground group")
        os.write(self.master, b"\x03")

    def send_signal(self, value):
        if self.process.poll() is not None:
            raise RuntimeError("Desktop process exited before the requested OS signal")
        os.kill(self.process.pid, value)

    def wait_success(self, timeout=25, natural_exit_timeout=3):
        try:
            result = self.process.wait(timeout=timeout)
        except subprocess.TimeoutExpired as error:
            raise RuntimeError("Desktop process did not finish the graceful signal shutdown") from error
        if result != 0:
            raise RuntimeError(f"Desktop process exited with {result}, expected a successful graceful shutdown (0)")
        deadline = time.monotonic() + natural_exit_timeout
        while True:
            self.remaining_live_processes = self._live_session_processes()
            if not self.remaining_live_processes:
                self.natural_exit_verified = True
                break
            if time.monotonic() >= deadline:
                raise RuntimeError("GUI/browser processes did not exit naturally after the desktop closed: "
                                   + json.dumps(self.remaining_live_processes))
            time.sleep(.025)
        return result

    def close(self):
        if self.natural_exit_verified:
            os.close(self.master)
            self.log.close()
            return
        if self.process.poll() is None:
            self._kill_owned_group(signal.SIGTERM)
            try:
                self.process.wait(timeout=2)
            except subprocess.TimeoutExpired:
                self._kill_owned_group(signal.SIGKILL)
                self.process.wait(timeout=5)
        # A WebKit helper may remain after its parent exits. This group was
        # created exclusively for this child; a signal supervisor may put its
        # GUI worker into another group in this same isolated terminal session.
        self._kill_owned_group(signal.SIGKILL)
        for group in self._session_groups():
            try:
                os.killpg(group, signal.SIGKILL)
            except ProcessLookupError:
                pass
        os.close(self.master)
        self.log.close()

    def _kill_owned_group(self, value):
        try:
            os.killpg(self.process.pid, value)
        except ProcessLookupError:
            pass

    def _session_groups(self):
        groups = set()
        for entry in Path("/proc").iterdir():
            if not entry.name.isdigit():
                continue
            try:
                pid = int(entry.name)
                if os.getsid(pid) == self.process.pid:
                    groups.add(os.getpgid(pid))
            except (ProcessLookupError, PermissionError):
                continue
        return groups

    def _live_session_processes(self):
        processes = []
        for entry in Path("/proc").iterdir():
            if not entry.name.isdigit():
                continue
            try:
                text = (entry / "stat").read_text()
                end = text.rfind(")")
                fields = text[end + 2:].split()
                # Zombies have exited and hold no SQLite/browser resources.
                # They belong to their own reaper once the GUI parent exits.
                if int(fields[3]) == self.process.pid and fields[0] != "Z":
                    processes.append({"pid": int(entry.name), "name": text[text.find("(") + 1:end],
                                      "state": fields[0], "parent": int(fields[1]), "group": int(fields[2])})
            except (FileNotFoundError, ProcessLookupError, PermissionError):
                continue
        return sorted(processes, key=lambda item: item["pid"])

    def __enter__(self):
        return self

    def __exit__(self, *_):
        self.close()


def load_report(path):
    try:
        return json.loads(Path(path).read_text())
    except (FileNotFoundError, json.JSONDecodeError):
        return None


def wait_ready(child, path, timeout=30):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        report = load_report(path)
        if report:
            if not report.get("passed"):
                raise RuntimeError(f"Desktop preparation failed: {report.get('error', report)}")
            if report.get("signalReady"):
                return report
        if child.process.poll() is not None:
            raise RuntimeError(f"Desktop exited before creating the pending edit: {child.process.returncode}")
        time.sleep(.01)
    raise RuntimeError("Timed out waiting for the desktop's unsaved signal-test edit")


def wait_failure(child, path, timeout=10):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        report = load_report(path)
        if report:
            if report.get("error") or not report.get("passed"):
                raise RuntimeError(f"Native synthetic failure check failed: {report.get('error', report)}")
            if report.get("signalSaveFailureObserved"):
                return report
        if child.process.poll() is not None:
            raise RuntimeError("Desktop exited while its final save was unavailable")
        time.sleep(.01)
    raise RuntimeError("Timed out waiting for paused-close UI after the synthetic save failure")


def read_workspace(directory):
    path = Path(directory) / "workspace.sqlite"
    with closing(sqlite3.connect(path.resolve().as_uri() + "?mode=ro", uri=True)) as database:
        revision, document = database.execute("SELECT revision,json_document FROM workspace_state WHERE id=1").fetchone()
        integrity = database.execute("PRAGMA quick_check").fetchone()[0]
    if integrity != "ok":
        raise RuntimeError(f"Workspace SQLite integrity failure: {integrity}")
    return revision, json.loads(document)


def workspace_lock_available(directory):
    path = Path(directory) / "workspace.sqlite.lock"
    with path.open("r+b") as lock:
        try:
            fcntl.flock(lock.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            return False
        fcntl.flock(lock.fileno(), fcntl.LOCK_UN)
        return True


def run_case(binary, evidence, name, mode):
    with tempfile.TemporaryDirectory(prefix=f"cryptodesk-signal-{name}-") as temporary:
        data = Path(temporary)
        edit_report = evidence / f"{name}-edit.json"
        restore_report = evidence / f"{name}-restore.json"
        early_release = data / "early-release"
        retry_marker = data / "retry-save"
        # Stale reports from a previous invocation cannot satisfy readiness.
        for path in (edit_report, restore_report):
            path.unlink(missing_ok=True)
        environment = dict(os.environ, CRYPTODESK_SMOKE_PHASE={"early": "signal-early", "failure": "signal-failure"}.get(mode, "signal-edit"),
                           CRYPTODESK_SMOKE_REPORT=str(edit_report), CRYPTODESK_SMOKE_EARLY_RELEASE=str(early_release),
                           CRYPTODESK_SMOKE_SIGNAL_RETRY=str(retry_marker))
        arguments = [str(binary), "--data-dir", str(data)]
        with NativeProcess(arguments, environment, evidence / f"{name}-edit.log") as child:
            ready = wait_ready(child, edit_report)
            before_revision, before = read_workspace(data)
            if before["tabs"][0]["charts"][0]["timeframe"] != ready["originalTimeframe"]:
                raise RuntimeError("The intended pending edit was already committed before the OS signal")
            if workspace_lock_available(data):
                raise RuntimeError("The running desktop app does not hold its workspace lock")
            started = time.monotonic()
            if mode == "terminal":
                child.interrupt_terminal()
            elif mode == "term":
                child.send_signal(signal.SIGTERM)
            elif mode == "repeated":
                for _ in range(3):
                    child.send_signal(signal.SIGINT)
            elif mode == "early":
                if not ready.get("earlyBeforeUI"):
                    raise RuntimeError("Early signal readiness did not precede UI startup")
                child.interrupt_terminal()
                # The explicit gate keeps GTK/WebKit unstarted while the
                # foreground supervisor receives and forwards the terminal byte.
                time.sleep(.05)
                early_release.touch()
            elif mode == "failure":
                if not ready.get("syntheticSaveFailure"):
                    raise RuntimeError("Native test did not explicitly enable synthetic save failure injection")
                child.interrupt_terminal()
                failed = wait_failure(child, edit_report)
                if failed.get("injectedFailureCount") != 1:
                    raise RuntimeError("Native test did not inject exactly one failing SaveWorkspace call")
                for _ in range(3):
                    child.send_signal(signal.SIGINT)
                time.sleep(.15)
                if child.process.poll() is not None:
                    raise RuntimeError("Repeated close signals forced exit while the final save was unavailable")
                blocked_revision, blocked = read_workspace(data)
                if blocked_revision != before_revision or blocked["tabs"][0]["charts"][0]["timeframe"] != ready["originalTimeframe"]:
                    raise RuntimeError("Synthetic unavailable storage did not preserve the last committed workspace")
                if workspace_lock_available(data):
                    raise RuntimeError("Paused close released the workspace lock before retry")
                retry_marker.touch()
            else:
                raise ValueError(f"Unknown signal scenario: {mode}")
            child.wait_success()
            elapsed = time.monotonic() - started
            remaining_after_signal = child.remaining_live_processes
        after_revision, workspace = read_workspace(data)
        finished = load_report(edit_report)
        if not finished or finished.get("error") or not finished.get("passed"):
            raise RuntimeError(f"Native signal scenario failed: {finished}")
        if mode == "failure" and not finished.get("realBackendRetryRequested"):
            raise RuntimeError("Native save retry did not restore the actual Wails/SQLite backend")
        chart = workspace["tabs"][0]["charts"][0]
        if chart["timeframe"] != ready["expectedTimeframe"] or after_revision < before_revision or (mode != "early" and after_revision == before_revision):
            raise RuntimeError("The final frontend edit was not durably committed during signal shutdown")
        if workspace["id"] != ready["workspaceId"] or chart["id"] != ready["chartId"]:
            raise RuntimeError("Signal shutdown unexpectedly replaced the workspace or chart")
        if not workspace_lock_available(data):
            raise RuntimeError("Signal shutdown did not release the SQLite workspace lock")
        environment.update(CRYPTODESK_SMOKE_PHASE="signal-restore", CRYPTODESK_SMOKE_REPORT=str(restore_report),
                           CRYPTODESK_SMOKE_EXPECT_WORKSPACE=ready["workspaceId"], CRYPTODESK_SMOKE_EXPECT_CHART=ready["chartId"],
                           CRYPTODESK_SMOKE_EXPECT_TIMEFRAME=ready["expectedTimeframe"])
        with NativeProcess(arguments, environment, evidence / f"{name}-restore.log") as restart:
            restart.wait_success(timeout=40)
            remaining_after_restart = restart.remaining_live_processes
        restored = load_report(restore_report)
        if not restored or not restored.get("passed"):
            raise RuntimeError(f"Restart did not restore the saved signal edit: {restored}")
        if not workspace_lock_available(data):
            raise RuntimeError("Restart did not release its workspace lock")
        return {"name": name, "passed": True, "signal": mode, "exitCode": 0, "shutdownSeconds": elapsed,
                "signalDelivery": "terminal Ctrl+C byte" if mode in ("terminal", "early", "failure") else "direct process signal",
                "signalCount": 4 if mode == "failure" else 3 if mode == "repeated" else 1, "earlyBeforeUI": mode == "early",
                "syntheticSaveFailure": mode == "failure", "realBackendRetryVerified": mode == "failure",
                "closeStayedOpenDuringSaveFailure": mode == "failure",
                "originalTimeframe": ready["originalTimeframe"], "savedTimeframe": ready["expectedTimeframe"],
                "revisionBefore": before_revision, "revisionAfter": after_revision, "workspaceLockReleased": True,
                "remainingLiveProcessesAfterSignal": remaining_after_signal,
                "remainingLiveProcessesAfterRestart": remaining_after_restart, "naturalProcessExitVerified": True,
                "restartPassed": True, "nativeChecks": len(finished["checks"]) + len(restored["checks"])}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, type=Path)
    parser.add_argument("--evidence", type=Path)
    arguments = parser.parse_args()
    if not os.environ.get("DISPLAY") and not os.environ.get("WAYLAND_DISPLAY"):
        raise SystemExit("Signal smoke blocked: a DISPLAY or WAYLAND_DISPLAY desktop session is required")
    binary = arguments.binary.resolve(strict=True)
    evidence = (arguments.evidence or Path(__file__).resolve().parent.parent / "docs/verification" /
                ("signals-" + datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ"))).resolve()
    evidence.mkdir(parents=True, exist_ok=True)
    report = {"startedAtUTC": datetime.datetime.now(datetime.timezone.utc).isoformat(), "binary": str(binary),
              "productionPublicData": True, "isolatedTemporaryProfiles": True, "passed": False, "scenarios": []}
    try:
        for name, mode in (("terminal-ctrl-c", "terminal"), ("sigterm", "term"), ("repeated-sigint", "repeated"), ("early-ctrl-c", "early"), ("save-failure-retry", "failure")):
            result = run_case(binary, evidence, name, mode)
            report["scenarios"].append(result)
            outcome = "early workspace preserved" if mode == "early" else "synthetic save failure stayed open; real backend retry saved edit" if mode == "failure" else "pending edit saved"
            print(f"{name}: PASS (exit 0, {outcome}, workspace lock released, native restart passed)", flush=True)
        report["passed"] = True
    except (OSError, RuntimeError, ValueError, sqlite3.Error) as error:
        report["error"] = str(error)
        raise SystemExit(f"Signal smoke failed: {error}. Evidence: {evidence}") from error
    finally:
        report["finishedAtUTC"] = datetime.datetime.now(datetime.timezone.utc).isoformat()
        (evidence / "signals.json").write_text(json.dumps(report, indent=2) + "\n")
    print(f"Signal evidence: {evidence}")


if __name__ == "__main__":
    main()

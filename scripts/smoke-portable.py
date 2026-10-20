#!/usr/bin/env python3
"""Verify the actual Linux portable archive with host GTK/WebKit masked.

Requires a graphical session and unprivileged bubblewrap. All profiles, cache
and extraction paths are temporary; only owned terminal-session processes can
be cleaned up on failure. Never opens a browser or changes a user workspace.
"""
import argparse
import datetime
import json
import os
from pathlib import Path
import re
import tarfile
import tempfile
import time

from release import archive_manifest, sha256
from smoke_signals import NativeProcess, read_workspace, workspace_lock_available

LOG_TAIL_BYTES = 16 * 1024
# Application diagnostics are useful, but authentication/cookie dumps are not.
SENSITIVE_LOG_LINE = re.compile(r"(?:authorization|proxy-authorization|set-cookie|cookie)\s*[:=]|"
                                r"\b(?:[\w]*token|[\w]*secret|[\w]*password|api[_-]?key)\s*[:=]", re.IGNORECASE)


def log_tail(path):
    """Read bounded bytes, not the entire process log, and omit sensitive lines."""
    try:
        with Path(path).open("rb") as stream:
            stream.seek(0, os.SEEK_END)
            stream.seek(max(0, stream.tell() - LOG_TAIL_BYTES))
            text = stream.read(LOG_TAIL_BYTES).decode("utf-8", errors="replace")
    except OSError:
        return ""
    text = "\n".join("[sensitive log line omitted]" if SENSITIVE_LOG_LINE.search(line) else line
                     for line in text.splitlines())
    return text.encode("utf-8")[-LOG_TAIL_BYTES:].decode("utf-8", errors="ignore")


def process_mappings(child):
    mappings = []
    for process in child._live_session_processes():
        try:
            pid = str(process["pid"])
            executable = os.readlink("/proc/" + pid + "/exe")
            libraries = sorted({line.split(maxsplit=5)[5] for line in Path("/proc/" + pid + "/maps").read_text().splitlines()
                                if any(name in line for name in ("libgtk-3.so", "libwebkit2gtk-4.1.so", "libjavascriptcoregtk-4.1.so"))})
            if libraries:
                mappings.append({"executable": executable, "libraries": libraries})
        except (FileNotFoundError, PermissionError, ProcessLookupError):
            continue
    return mappings


def wait_for_webkit(child, timeout=20):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        code = child.process.poll()
        if code is not None:
            raise RuntimeError(f"Portable desktop exited during startup (exit code {code}); inspect its log")
        mappings = process_mappings(child)
        if any("WebKitWebProcess" in entry["executable"] for entry in mappings):
            return mappings
        time.sleep(.05)
    raise RuntimeError("Bundled WebKit render process did not start")


def failure_state(child, log_path, package, mappings=None):
    """Capture before owned-process cleanup; never include argv or environment."""
    result = {"exitCode": child.process.poll(), "logTail": log_tail(log_path)}
    try:
        result["liveProcesses"] = [{key: item[key] for key in ("pid", "name", "state", "parent", "group") if key in item}
                                   for item in child._live_session_processes()[:64]]
    except OSError as error:
        result["processInspectionError"] = type(error).__name__
    try:
        current = process_mappings(child)
    except OSError as error:
        current = []
        result["mappingInspectionError"] = type(error).__name__
    runtime = str(package / "runtime") + "/"
    result["mappings"] = [{"executable": Path(item["executable"]).name,
                           "bundledExecutable": item["executable"].startswith(runtime),
                           "libraries": [{"name": Path(library).name, "bundled": library.startswith(runtime)}
                                         for library in item["libraries"][:128]]}
                          for item in (current or mappings or [])[:64]]
    return result


def verify_launch(arguments, environment, log_path, package, data, previous, report, phase):
    with NativeProcess(arguments, environment, log_path) as child:
        mappings = []
        try:
            mappings = wait_for_webkit(child)
            if not all(str(package / "runtime") in library for entry in mappings for library in entry["libraries"]):
                raise RuntimeError("A host GTK/WebKit library leaked into the portable runtime")
            if not all(str(package / "runtime") in entry["executable"] for entry in mappings):
                raise RuntimeError("A host WebKit helper was used by the portable runtime")
            revision, workspace = read_workspace(data)
            if workspace_lock_available(data) or len(workspace["tabs"]) != 1 or len(workspace["tabs"][0]["charts"]) != 1:
                raise RuntimeError("Portable default layout/profile lock is incorrect")
            if previous is not None and workspace != previous:
                raise RuntimeError("Portable restart changed the committed workspace")
            time.sleep(1)
            started = time.monotonic()
            child.interrupt_terminal()
            child.wait_success()
            revision, workspace = read_workspace(data)
            if not workspace_lock_available(data):
                raise RuntimeError("Portable shutdown did not release its profile lock")
            report["launches"].append({"phase": phase, "exitCode": child.process.returncode,
                "signal": "real controlling-terminal Ctrl+C", "shutdownSeconds": time.monotonic() - started,
                "workspaceRevision": revision, "sqliteIntegrity": "ok", "workspaceLockReleased": True,
                "naturalProcessExitVerified": child.natural_exit_verified, "remainingLiveProcesses": child.remaining_live_processes,
                "mappings": mappings})
            return workspace
        except Exception:
            # __exit__ may need to kill surviving owned helpers. Record their
            # actual failing state first, preserving the original exception.
            try:
                report["failure"] = {"phase": phase, **failure_state(child, log_path, package, mappings)}
            except Exception as inspection_error:
                report["failure"] = {"phase": phase, "inspectionError": type(inspection_error).__name__,
                                     "logTail": log_tail(log_path)}
            raise


def verify_archive(path, evidence, report):
    with tarfile.open(path, "r:gz") as archive:
        prefix = archive.getnames()[0].split("/")[0]
        with archive.extractfile(prefix + "/version.json") as stream:
            manifest = json.load(stream)
    archive_manifest(path, "linux-amd64-portable", manifest["version"])
    report.update({"sha256": sha256(path), "manifest": {
        name: manifest[name] for name in ("version", "target", "packageVariant")}})
    with tempfile.TemporaryDirectory(prefix="cryptodesk portable check ") as temporary:
        temporary = Path(temporary)
        with tarfile.open(path, "r:gz") as archive:
            archive.extractall(temporary, filter="data")
        package = temporary / prefix
        data = temporary / "isolated profile"
        data.mkdir()
        run = data / "run"
        run.mkdir(mode=0o700)
        empty = temporary / "empty-library"
        empty.write_bytes(b"")
        multiarch = Path("/usr/lib/x86_64-linux-gnu")
        masks = []
        masked_paths = []
        for name in ("libgtk-3.so.0", "libgdk-3.so.0", "libwebkit2gtk-4.1.so.0", "libjavascriptcoregtk-4.1.so.0"):
            library = (multiarch / name).resolve()
            if library.is_file():
                masks.extend(["--ro-bind", str(empty), str(library)])
                masked_paths.append(str(library))
        masked_dirs = [multiarch / "webkit2gtk-4.1", multiarch / "gio/modules", multiarch / "gdk-pixbuf-2.0",
                       multiarch / "gstreamer-1.0", Path("/usr/share/glib-2.0/schemas"), Path("/usr/libexec/glycin-loaders")]
        for directory in masked_dirs:
            if directory.is_dir():
                masks.extend(["--tmpfs", str(directory)])
                masked_paths.append(str(directory))
        report["maskedHostPaths"] = masked_paths
        environment = dict(os.environ, XDG_CACHE_HOME=str(data / "cache"), XDG_CONFIG_HOME=str(data / "config"), XDG_RUNTIME_DIR=str(run))
        wayland = environment.get("WAYLAND_DISPLAY", "")
        if wayland and not wayland.startswith("/"):
            environment["WAYLAND_DISPLAY"] = str(Path(os.environ["XDG_RUNTIME_DIR"]) / wayland)
        previous = None
        for phase in ("fresh", "restore"):
            arguments = ["bwrap", "--ro-bind", "/", "/", "--dev-bind", "/dev", "/dev", "--proc", "/proc", *masks,
                         "--bind", str(data), str(data), str(package / "cryptodesk"), "--data-dir", str(data)]
            # Do not let the test's outer bwrap process die from the terminal's
            # SIGINT before returning the actual app's successful exit status.
            arguments = ["bash", "-c", 'trap "" INT; exec "$@"', "portable-runtime-test", *arguments]
            previous = verify_launch(arguments, environment, evidence / f"{phase}.log", package, data, previous, report, phase)


def write_report(evidence, report):
    pending = evidence / "portable.json.tmp"
    pending.write_text(json.dumps(report, indent=2) + "\n")
    pending.replace(evidence / "portable.json")


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("archive", type=Path)
    parser.add_argument("--evidence", required=True, type=Path)
    args = parser.parse_args(argv)
    evidence = args.evidence.resolve()
    evidence.mkdir(parents=True, exist_ok=True)
    path = args.archive.resolve()
    report = {"passed": False, "archive": path.name, "launches": []}
    # A failed/interrupted rerun must never leave a previous passing report.
    write_report(evidence, report)
    try:
        verify_archive(path, evidence, report)
        report["passed"] = True
    except Exception as error:
        report["error"] = str(error)
        state = report.get("failure", {})
        code = state.get("exitCode")
        detail = f"\nProcess exit code: {code if code is not None else 'still running or unavailable'}" if state else ""
        if state.get("logTail"):
            detail += "\nProcess log tail (bounded, sensitive lines omitted):\n" + state["logTail"]
        raise SystemExit(f"Portable runtime smoke failed: {error}{detail}\nEvidence: {evidence}") from error
    finally:
        report["finishedAtUTC"] = datetime.datetime.now(datetime.timezone.utc).isoformat()
        write_report(evidence, report)
    print("Portable runtime: PASS (host GTK/WebKit/modules masked; real GUI/helpers, Ctrl+C, save/restart and natural process exit)")


if __name__ == "__main__":
    main()

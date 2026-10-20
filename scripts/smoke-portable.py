#!/usr/bin/env python3
"""Verify the actual Linux portable archive with host GTK/WebKit masked.

Requires a graphical session and unprivileged bubblewrap. All profiles, cache
and extraction paths are temporary; only owned terminal-session processes can
be cleaned up on failure. Never opens a browser or changes a user workspace.
"""
import argparse
import json
import os
from pathlib import Path
import tarfile
import tempfile
import time

from release import archive_manifest, sha256
from smoke_signals import NativeProcess, read_workspace, workspace_lock_available


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("archive", type=Path)
    parser.add_argument("--evidence", required=True, type=Path)
    args = parser.parse_args()
    evidence = args.evidence.resolve()
    evidence.mkdir(parents=True, exist_ok=True)
    path = args.archive.resolve()
    with tarfile.open(path, "r:gz") as archive:
        prefix = archive.getnames()[0].split("/")[0]
        manifest = json.load(archive.extractfile(prefix + "/version.json"))
    archive_manifest(path, "linux-amd64-portable", manifest["version"])
    report = {"passed": False, "archive": path.name, "sha256": sha256(path), "manifest": {
              name: manifest[name] for name in ("version", "target", "packageVariant")}, "launches": []}
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
            with NativeProcess(arguments, environment, evidence / f"{phase}.log") as child:
                deadline = time.monotonic() + 20
                mappings = []
                while time.monotonic() < deadline:
                    if child.process.poll() is not None:
                        raise RuntimeError("Portable desktop exited during startup; inspect its log")
                    mappings = []
                    for process in child._live_session_processes():
                        try:
                            pid = str(process["pid"])
                            executable = os.readlink("/proc/" + pid + "/exe")
                            libraries = sorted({line.split(maxsplit=5)[5] for line in Path("/proc/" + pid + "/maps").read_text().splitlines()
                                                if any(name in line for name in ("libgtk-3.so", "libwebkit2gtk-4.1.so", "libjavascriptcoregtk-4.1.so"))})
                            if libraries:
                                mappings.append({"executable": executable, "libraries": libraries})
                        except FileNotFoundError:
                            continue
                    if any("WebKitWebProcess" in entry["executable"] for entry in mappings):
                        break
                    time.sleep(.05)
                else:
                    raise RuntimeError("Bundled WebKit render process did not start")
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
                revision, previous = read_workspace(data)
                if not workspace_lock_available(data):
                    raise RuntimeError("Portable shutdown did not release its profile lock")
                report["launches"].append({"phase": phase, "exitCode": child.process.returncode,
                    "signal": "real controlling-terminal Ctrl+C", "shutdownSeconds": time.monotonic() - started,
                    "workspaceRevision": revision, "sqliteIntegrity": "ok", "workspaceLockReleased": True,
                    "naturalProcessExitVerified": child.natural_exit_verified, "remainingLiveProcesses": child.remaining_live_processes,
                    "mappings": mappings})
    report["passed"] = True
    (evidence / "portable.json").write_text(json.dumps(report, indent=2) + "\n")
    print("Portable runtime: PASS (host GTK/WebKit/modules masked; real GUI/helpers, Ctrl+C, save/restart and natural process exit)")


if __name__ == "__main__":
    main()

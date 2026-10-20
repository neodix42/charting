#!/usr/bin/env python3
"""Prepare Bubblewrap's namespace permission only on disposable Ubuntu 24 CI.

An already-working unprivileged probe makes no policy change. A restricted
GitHub-hosted Noble runner may add the pinned upstream capability-stripping
Bubblewrap policy, never replace existing policy or change system settings.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess

CI_POLICY = Path(__file__).resolve().parent / "apparmor/bwrap-userns-restrict"
UPSTREAM_COMMIT = "72229df83059480f4e9fb1488624201bdbd61755"
POLICY_SHA256 = "a964037f6cf0df1099f14226b037eaedde6237c86e715188e93eb460b30be859"
POLICY_CONFLICT_PATHS = tuple(Path("/etc/apparmor.d") / name for name in (
    "bwrap", "bwrap-userns-restrict", "local/bwrap-userns-restrict", "local/unpriv_bwrap"))
LOADED_PROFILES = "/sys/kernel/security/apparmor/profiles"
PROBE = ["/usr/bin/bwrap", "--ro-bind", "/", "/", "--", "/usr/bin/true"]


def read_text(path):
    try:
        return Path(path).read_text().strip()
    except OSError:
        return None


def execute(arguments, timeout=20):
    return subprocess.run(arguments, capture_output=True, text=True, timeout=timeout, check=False)


def bootstrap(evidence, environment=None):
    environment = os.environ if environment is None else environment
    evidence = Path(evidence).resolve()
    evidence.mkdir(parents=True, exist_ok=True)
    report = {"passed": False, "profileLoaded": False, "probes": [],
              "upstreamCommit": UPSTREAM_COMMIT, "policySHA256": POLICY_SHA256,
              "system": {}}
    (evidence / "namespace.json").write_text(json.dumps(report, indent=2) + "\n")

    def command(arguments):
        record = {"command": arguments}
        report["probes"].append(record)
        try:
            result = execute(arguments)
        except (OSError, subprocess.SubprocessError) as error:
            record["error"] = str(error)
            raise RuntimeError(f"Namespace setup command failed: {arguments[0]}: {error}") from error
        record.update({"exitCode": result.returncode, "stdout": result.stdout[-12000:],
                       "stderr": result.stderr[-12000:]})
        return result

    try:
        report["system"] = {
            "uid": os.geteuid(), "distribution": platform.freedesktop_os_release(),
            "apparmorEnabled": read_text("/sys/module/apparmor/parameters/enabled"),
            "restrictUnprivilegedUserns": read_text("/proc/sys/kernel/apparmor_restrict_unprivileged_userns"),
            "processProfile": read_text("/proc/self/attr/current"),
            "githubActions": environment.get("GITHUB_ACTIONS"),
            "runnerOS": environment.get("RUNNER_OS"),
            "runnerEnvironment": environment.get("RUNNER_ENVIRONMENT"),
        }
        if os.geteuid() == 0:
            raise RuntimeError("Run the namespace probe as the unprivileged runner user")
        selected_bwrap = shutil.which("bwrap")
        if not selected_bwrap or Path(selected_bwrap).resolve() != Path("/usr/bin/bwrap").resolve():
            raise RuntimeError("The portable smoke test requires the system /usr/bin/bwrap")
        initial = command(PROBE)
        if initial.returncode == 0:
            report["passed"] = True
            report["result"] = "Unprivileged Bubblewrap already works; no policy changed"
            return report
        denial = initial.stderr + initial.stdout
        if not (re.search(r"(?:uid map|namespace)", denial, re.IGNORECASE)
                and re.search(r"(?:permission denied|operation not permitted)", denial, re.IGNORECASE)):
            raise RuntimeError("Bubblewrap failed for a reason other than denied namespace setup; refusing policy changes")
        system = report["system"]
        distribution = system["distribution"]
        if not (system["githubActions"] == "true" and system["runnerOS"] == "Linux"
                and system["runnerEnvironment"] == "github-hosted"
                and distribution.get("ID") == "ubuntu" and distribution.get("VERSION_ID") == "24.04"
                and system["apparmorEnabled"] == "Y" and system["restrictUnprivilegedUserns"] == "1"):
            raise RuntimeError("Policy bootstrap is restricted to GitHub-hosted Ubuntu 24.04 with AppArmor user namespace restrictions")
        conflicts = [str(path) for path in POLICY_CONFLICT_PATHS if path.exists() or path.is_symlink()]
        if conflicts:
            raise RuntimeError("Existing Bubblewrap policy files must not be overridden: " + ", ".join(conflicts))
        parser = shutil.which("apparmor_parser")
        if not parser or not shutil.which("sudo"):
            raise RuntimeError("AppArmor parser and noninteractive sudo are required for the CI-only policy bootstrap")
        if hashlib.sha256(CI_POLICY.read_bytes()).hexdigest() != POLICY_SHA256:
            raise RuntimeError("Pinned upstream Bubblewrap policy SHA-256 mismatch")
        loaded = command(["sudo", "-n", "cat", LOADED_PROFILES])
        if loaded.returncode:
            raise RuntimeError("Cannot inspect loaded AppArmor profiles; refusing policy changes")
        conflicts = [line for line in loaded.stdout.splitlines()
                     if re.match(r"(?:bwrap|unpriv_bwrap|/usr/bin/bwrap)(?:\s|//|$)", line)]
        if conflicts:
            raise RuntimeError("Existing loaded Bubblewrap profiles must not be replaced: " + ", ".join(conflicts))
        options = ["--skip-cache", "--Include", "/etc/apparmor.d", "--warn=rule-not-enforced", str(CI_POLICY)]
        compiled = command([parser, "--skip-kernel-load", *options])
        if compiled.returncode:
            raise RuntimeError("Pinned ABI 4.0 Bubblewrap policy failed unprivileged compilation")
        added = command(["sudo", "-n", parser, "--add", *options])
        if added.returncode:
            raise RuntimeError("Could not add the CI Bubblewrap policy; no existing policy was replaced")
        report["profileLoaded"] = True
        repeated = command(PROBE)
        if repeated.returncode:
            raise RuntimeError("Unprivileged Bubblewrap still fails after the CI policy was added")
        report["passed"] = True
        report["result"] = "Pinned upstream Bubblewrap policy added; unprivileged namespace probe passed"
        return report
    except (OSError, ValueError, RuntimeError) as error:
        report["error"] = str(error)
        raise RuntimeError(str(error)) from error
    finally:
        (evidence / "namespace.json").write_text(json.dumps(report, indent=2) + "\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--evidence", required=True, type=Path)
    arguments = parser.parse_args()
    try:
        report = bootstrap(arguments.evidence)
    except (OSError, ValueError, RuntimeError) as error:
        parser.exit(1, f"Portable namespace setup failed: {error}\n")
    print(report["result"])


if __name__ == "__main__":
    main()

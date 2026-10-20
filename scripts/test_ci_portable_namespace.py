"""Offline checks for the narrowly scoped GitHub-runner AppArmor bootstrap.

Every subprocess, host setting and command lookup is replaced. These checks
never load a policy, run sudo, start namespaces or contact the network.
"""
from contextlib import ExitStack
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import ci_portable_namespace as namespace


ROOT = Path(__file__).resolve().parent.parent
UPSTREAM_COMMIT = "72229df83059480f4e9fb1488624201bdbd61755"
POLICY_SHA256 = "a964037f6cf0df1099f14226b037eaedde6237c86e715188e93eb460b30be859"
HOSTED_ENVIRONMENT = {
    "GITHUB_ACTIONS": "true",
    "RUNNER_OS": "Linux",
    "RUNNER_ENVIRONMENT": "github-hosted",
    "ImageOS": "ubuntu24",
}
DENIED = "bwrap: setting up uid map: Permission denied\n"


def completed(command, code=0, stdout="", stderr=""):
    return subprocess.CompletedProcess(command, code, stdout, stderr)


class NamespaceBootstrapTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.evidence = Path(self.temporary.name) / "evidence"
        self.files = {
            "/sys/module/apparmor/parameters/enabled": "Y",
            "/proc/sys/kernel/apparmor_restrict_unprivileged_userns": "1",
            "/proc/self/attr/current": "unconfined",
        }
        self.calls = []
        self.probe_results = [(1, "", DENIED), (0, "", "")]
        self.profiles = "other-program (enforce)\n"
        self.compile_result = (0, "", "")
        self.add_result = (0, "", "")
        self.missing = set()
        self.uid = 1001
        self.distribution = {"ID": "ubuntu", "VERSION_ID": "24.04", "PRETTY_NAME": "Ubuntu 24.04.3 LTS"}
        stack = self.enterContext(ExitStack())
        conflicts = tuple(Path(self.temporary.name) / "conflicts" / str(index)
                          for index, _path in enumerate(namespace.POLICY_CONFLICT_PATHS))
        stack.enter_context(patch.object(namespace, "POLICY_CONFLICT_PATHS", conflicts))
        stack.enter_context(patch.object(namespace, "read_text", side_effect=self.read_text))
        stack.enter_context(patch.object(namespace, "execute", side_effect=self.execute))
        stack.enter_context(patch.object(namespace.shutil, "which", side_effect=self.which))
        stack.enter_context(patch.object(namespace.os, "geteuid", side_effect=lambda: self.uid))
        stack.enter_context(patch.object(namespace.platform, "freedesktop_os_release", side_effect=lambda: self.distribution))

    def read_text(self, path):
        return self.files.get(str(path))

    def which(self, name):
        return None if name in self.missing else "/usr/bin/" + name

    def execute(self, command, timeout=20):
        command = [str(part) for part in command]
        self.calls.append(command)
        if Path(command[0]).name == "bwrap":
            return completed(command, *self.probe_results.pop(0))
        if command[:3] == ["sudo", "-n", "cat"] or command[:3] == ["/usr/bin/sudo", "-n", "cat"]:
            return completed(command, stdout=self.profiles)
        if "--skip-kernel-load" in command:
            return completed(command, *self.compile_result)
        if "--add" in command:
            return completed(command, *self.add_result)
        self.fail("Unexpected subprocess: " + repr(command))

    def bootstrap(self, environment=None):
        return namespace.bootstrap(self.evidence, environment=HOSTED_ENVIRONMENT if environment is None else environment)

    def report(self):
        return json.loads((self.evidence / "namespace.json").read_text())

    def sudo_calls(self):
        return [command for command in self.calls if Path(command[0]).name == "sudo"]

    def add_calls(self):
        return [command for command in self.calls if "--add" in command]

    def assert_failed(self):
        report = self.report()
        self.assertFalse(report["passed"])
        self.assertTrue(report["error"])
        return report

    def test_existing_unprivileged_namespaces_never_invoke_sudo_or_change_policy(self):
        self.distribution = {"ID": "debian", "VERSION_ID": "13"}
        self.probe_results = [(0, "already works\n", "")]
        report = self.bootstrap(environment={})
        self.assertTrue(report["passed"])
        self.assertFalse(report["profileLoaded"])
        self.assertEqual(len(self.calls), 1)
        self.assertEqual(Path(self.calls[0][0]).name, "bwrap")
        self.assertEqual(self.sudo_calls(), [])
        self.assertEqual(self.report(), report)

    def test_successful_fallback_only_reads_profiles_adds_exact_policy_and_reprobes(self):
        report = self.bootstrap()
        self.assertTrue(report["passed"])
        self.assertTrue(report["profileLoaded"])
        self.assertEqual(self.report(), report)
        probes = [probe for probe in report["probes"] if probe["command"] == namespace.PROBE]
        self.assertEqual([probe["exitCode"] for probe in probes], [1, 0])
        sudo = self.sudo_calls()
        self.assertEqual(len(sudo), 2)
        self.assertEqual(sudo[0][1:], ["-n", "cat", "/sys/kernel/security/apparmor/profiles"])
        self.assertEqual(sudo[1][1:], ["-n", "/usr/bin/apparmor_parser", "--add", "--skip-cache", "--Include",
                                     "/etc/apparmor.d", "--warn=rule-not-enforced", str(namespace.CI_POLICY)])
        self.assertEqual(self.calls[0], self.calls[-1])
        self.assertNotIn("--replace", [argument for call in self.calls for argument in call])
        self.assertNotIn("--remove", [argument for call in self.calls for argument in call])
        self.assertFalse(any("sysctl" in part or "systemctl" in part or "aa-unconfined" in part
                             for call in self.calls for part in call))
        compilation = [call for call in self.calls if "--skip-kernel-load" in call]
        self.assertEqual(len(compilation), 1)
        self.assertNotEqual(Path(compilation[0][0]).name, "sudo")
        self.assertIn("--skip-cache", compilation[0])
        self.assertIn("--warn=rule-not-enforced", compilation[0])

    def test_host_gate_refuses_nonhosted_runners_and_other_distributions_without_sudo(self):
        variants = [
            ({}, {"ID": "ubuntu", "VERSION_ID": "24.04"}),
            ({**HOSTED_ENVIRONMENT, "GITHUB_ACTIONS": "false"}, {"ID": "ubuntu", "VERSION_ID": "24.04"}),
            ({**HOSTED_ENVIRONMENT, "RUNNER_OS": "Windows"}, {"ID": "ubuntu", "VERSION_ID": "24.04"}),
            ({**HOSTED_ENVIRONMENT, "RUNNER_ENVIRONMENT": "self-hosted"}, {"ID": "ubuntu", "VERSION_ID": "24.04"}),
            (HOSTED_ENVIRONMENT, {"ID": "debian", "VERSION_ID": "24.04"}),
            (HOSTED_ENVIRONMENT, {"ID": "ubuntu", "VERSION_ID": "26.04"}),
            (HOSTED_ENVIRONMENT, {"ID": "ubuntu"}),
        ]
        for environment, distribution in variants:
            with self.subTest(environment=environment, distribution=distribution):
                self.distribution = distribution
                self.probe_results = [(1, "", DENIED)]
                self.calls.clear()
                with self.assertRaisesRegex(RuntimeError, "restricted to GitHub-hosted Ubuntu 24.04"):
                    self.bootstrap(environment)
                self.assertEqual(self.sudo_calls(), [])
                self.assertFalse(self.assert_failed()["profileLoaded"])

    def test_missing_or_disabled_namespace_restriction_fails_closed_without_sudo(self):
        for path, value in [
            ("/sys/module/apparmor/parameters/enabled", "N"),
            ("/sys/module/apparmor/parameters/enabled", None),
            ("/proc/sys/kernel/apparmor_restrict_unprivileged_userns", "0"),
            ("/proc/sys/kernel/apparmor_restrict_unprivileged_userns", None),
        ]:
            with self.subTest(path=path, value=value):
                original = self.files[path]
                self.files[path] = value
                self.probe_results = [(1, "", DENIED)]
                self.calls.clear()
                with self.assertRaisesRegex(RuntimeError, "restricted to GitHub-hosted Ubuntu 24.04"):
                    self.bootstrap()
                self.assertEqual(self.sudo_calls(), [])
                self.assert_failed()
                self.files[path] = original

    def test_root_user_is_rejected_before_running_the_probe(self):
        self.uid = 0
        with self.assertRaisesRegex(RuntimeError, "unprivileged runner user"):
            self.bootstrap()
        self.assertEqual(self.calls, [])
        self.assert_failed()

    def test_unrelated_bubblewrap_errors_do_not_authorize_policy_changes(self):
        for stderr in ["bwrap: Unknown option --ro-bind\n", "bwrap: Can't bind /missing: No such file or directory\n",
                       "bwrap: Permission denied opening a runtime file\n", "bwrap: namespace option not recognized\n"]:
            with self.subTest(stderr=stderr):
                self.probe_results = [(1, "", stderr)]
                self.calls.clear()
                with self.assertRaisesRegex(RuntimeError, "reason other than denied namespace setup"):
                    self.bootstrap()
                self.assertEqual(self.sudo_calls(), [])
                self.assertEqual(self.assert_failed()["probes"][0]["stderr"], stderr)

    def test_missing_commands_or_non_system_bubblewrap_stop_without_sudo(self):
        for missing in ("bwrap", "apparmor_parser", "sudo"):
            with self.subTest(missing=missing):
                self.missing = {missing}
                self.probe_results = [(1, "", DENIED)]
                self.calls.clear()
                with self.assertRaises(RuntimeError):
                    self.bootstrap()
                self.assertEqual(self.sudo_calls(), [])
                self.assert_failed()
        self.missing.clear()
        self.calls.clear()
        with patch.object(namespace.shutil, "which", return_value="/tmp/custom-bwrap"):
            with self.assertRaisesRegex(RuntimeError, "requires the system /usr/bin/bwrap"):
                self.bootstrap()
        self.assertEqual(self.calls, [])

    def test_existing_policy_files_including_empty_files_stop_before_sudo(self):
        for path in namespace.POLICY_CONFLICT_PATHS:
            with self.subTest(path=path):
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text("")
                self.probe_results = [(1, "", DENIED)]
                self.calls.clear()
                with self.assertRaisesRegex(RuntimeError, "policy files must not be overridden"):
                    self.bootstrap()
                self.assertEqual(self.sudo_calls(), [])
                self.assert_failed()
                path.unlink()

    def test_loaded_bubblewrap_profiles_and_stacked_children_are_never_replaced(self):
        for name in ("bwrap", "unpriv_bwrap", "/usr/bin/bwrap", "bwrap//child", "unpriv_bwrap//child"):
            with self.subTest(name=name):
                self.profiles = "other-program (enforce)\n" + name + " (complain)\n"
                self.probe_results = [(1, "", DENIED)]
                self.calls.clear()
                with self.assertRaisesRegex(RuntimeError, "loaded Bubblewrap profiles must not be replaced"):
                    self.bootstrap()
                self.assertEqual(len(self.sudo_calls()), 1)
                self.assertEqual(self.add_calls(), [])
                self.assertFalse(self.assert_failed()["profileLoaded"])

    def test_existing_directories_and_broken_policy_symlinks_are_conflicts(self):
        path = namespace.POLICY_CONFLICT_PATHS[0]
        path.parent.mkdir(parents=True, exist_ok=True)
        for kind in ("directory", "broken-symlink"):
            with self.subTest(kind=kind):
                if kind == "directory":
                    path.mkdir()
                else:
                    path.symlink_to(path.parent / "missing-policy")
                self.probe_results = [(1, "", DENIED)]
                self.calls.clear()
                with self.assertRaisesRegex(RuntimeError, "policy files must not be overridden"):
                    self.bootstrap()
                self.assertEqual(self.sudo_calls(), [])
                self.assert_failed()
                if kind == "directory":
                    path.rmdir()
                else:
                    path.unlink()

    def test_similar_loaded_profile_names_do_not_block_an_unrelated_exact_policy(self):
        self.profiles = "bwrapper (enforce)\nunpriv_bwrapper (enforce)\n/usr/bin/bwrapper (enforce)\n"
        self.assertTrue(self.bootstrap()["passed"])

    def test_cannot_inspect_loaded_profiles_records_failure_without_add(self):
        original = self.execute

        def execute(command, timeout=20):
            if "cat" in command:
                self.calls.append(command)
                return completed(command, 1, "", "sudo: a password is required\n")
            return original(command, timeout)

        with patch.object(namespace, "execute", side_effect=execute):
            with self.assertRaisesRegex(RuntimeError, "Cannot inspect loaded AppArmor profiles"):
                self.bootstrap()
        self.assertEqual(self.add_calls(), [])
        self.assertIn("a password is required", self.assert_failed()["probes"][-1]["stderr"])

    def test_failed_unprivileged_policy_compilation_stops_before_add(self):
        self.compile_result = (1, "", "unsupported policy ABI 4.0\n")
        with self.assertRaisesRegex(RuntimeError, "failed unprivileged compilation"):
            self.bootstrap()
        self.assertEqual(self.add_calls(), [])
        report = self.assert_failed()
        self.assertFalse(report["profileLoaded"])
        self.assertIn("unsupported policy ABI 4.0", report["probes"][-1]["stderr"])

    def test_failed_policy_add_is_recorded_without_claiming_loaded_or_running_second_probe(self):
        self.add_result = (1, "", "apparmor_parser: permission denied loading policy\n")
        with self.assertRaisesRegex(RuntimeError, "Could not add the CI Bubblewrap policy"):
            self.bootstrap()
        report = self.assert_failed()
        self.assertFalse(report["profileLoaded"])
        self.assertEqual(len([call for call in self.calls if call == namespace.PROBE]), 1)
        self.assertIn("permission denied loading policy", report["probes"][-1]["stderr"])

    def test_failed_second_probe_honestly_records_policy_loaded_and_test_failed(self):
        self.probe_results = [(1, "", DENIED), (1, "", "bwrap: namespace still denied\n")]
        with self.assertRaisesRegex(RuntimeError, "still fails after the CI policy was added"):
            self.bootstrap()
        report = self.assert_failed()
        self.assertTrue(report["profileLoaded"])
        self.assertEqual(report["probes"][-1]["exitCode"], 1)

    def test_policy_integrity_mismatch_fails_before_any_sudo_command(self):
        policy = Path(self.temporary.name) / "modified-policy"
        policy.write_bytes(b"profile bwrap flags=(unconfined) {}\n")
        with patch.object(namespace, "CI_POLICY", policy):
            with self.assertRaisesRegex(RuntimeError, "SHA-256 mismatch"):
                self.bootstrap()
        self.assertEqual(self.sudo_calls(), [])
        self.assert_failed()

    def test_failed_run_replaces_stale_success_evidence_and_bounds_command_output(self):
        self.evidence.mkdir()
        (self.evidence / "namespace.json").write_text('{"passed":true,"stale":"previous run"}')
        self.probe_results = [(1, "x" * 24000, "unrelated failure" + "y" * 24000)]
        with self.assertRaises(RuntimeError):
            self.bootstrap()
        report = self.assert_failed()
        self.assertNotIn("stale", report)
        self.assertEqual(len(report["probes"][0]["stdout"]), 12000)
        self.assertEqual(len(report["probes"][0]["stderr"]), 12000)

    def test_failed_initial_evidence_is_written_before_any_subprocess(self):
        self.evidence.mkdir()
        (self.evidence / "namespace.json").write_text('{"passed":true,"stale":"previous run"}')
        original = self.execute

        def execute(command, timeout=20):
            initial = self.report()
            self.assertFalse(initial["passed"])
            self.assertNotIn("stale", initial)
            return original(command, timeout)

        with patch.object(namespace, "execute", side_effect=execute):
            self.assertTrue(self.bootstrap()["passed"])

    def test_subprocess_timeout_and_missing_host_metadata_are_recorded_without_mutation(self):
        with patch.object(namespace, "execute", side_effect=subprocess.TimeoutExpired(namespace.PROBE, 20)):
            with self.assertRaisesRegex(RuntimeError, "Namespace setup command failed"):
                self.bootstrap()
        self.assertIn("timed out", self.assert_failed()["probes"][0]["error"])
        self.assertEqual(self.sudo_calls(), [])
        with patch.object(namespace.platform, "freedesktop_os_release", side_effect=OSError("OS identity unreadable")):
            with self.assertRaisesRegex(RuntimeError, "OS identity unreadable"):
                self.bootstrap()
        self.assertEqual(self.assert_failed()["probes"], [])


class PinnedPolicyTests(unittest.TestCase):
    def test_vendored_policy_pins_upstream_bytes_abi_and_child_capability_denial(self):
        policy = ROOT / "scripts/apparmor/bwrap-userns-restrict"
        self.assertEqual(namespace.CI_POLICY, policy)
        self.assertEqual(namespace.UPSTREAM_COMMIT, UPSTREAM_COMMIT)
        self.assertEqual(namespace.POLICY_SHA256, POLICY_SHA256)
        self.assertEqual(hashlib.sha256(policy.read_bytes()).hexdigest(), POLICY_SHA256)
        rules = "\n".join(line.split("#", 1)[0] for line in policy.read_text().splitlines())
        self.assertIn("abi <abi/4.0>,", rules)
        self.assertIn("profile bwrap /usr/bin/bwrap", rules)
        self.assertIn("profile unpriv_bwrap", rules)
        self.assertIn("allow px /** -> bwrap//&unpriv_bwrap,", rules)
        self.assertIn("allow pix /** -> &unpriv_bwrap,", rules)
        self.assertIn("audit deny capability,", rules)
        self.assertNotIn("unconfined", rules)
        self.assertNotIn("complain", rules)
        self.assertNotIn("allow all", rules)
        license_text = (policy.parent / "LICENSE.apparmor").read_text()
        self.assertIn("GNU GENERAL PUBLIC LICENSE", license_text)
        self.assertIn("Version 2, June 1991", license_text)

    def test_execute_uses_bounded_nonchecking_captured_subprocess_without_shell(self):
        result = completed(["example"], 7, "diagnostic", "error")
        with patch.object(namespace.subprocess, "run", return_value=result) as run:
            self.assertIs(namespace.execute(["example", "arg"], timeout=3), result)
        run.assert_called_once_with(["example", "arg"], capture_output=True, text=True, timeout=3, check=False)


if __name__ == "__main__":
    unittest.main()

"""Guard the independent test/release workflows without a YAML dependency."""
from pathlib import Path
import re
import unittest


ROOT = Path(__file__).resolve().parent.parent
RELEASE = (ROOT / ".github/workflows/release.yml").read_text()
TESTS = (ROOT / ".github/workflows/tests.yml").read_text()
PINNED_ACTIONS = {
    "actions/checkout@d23441a48e516b6c34aea4fa41551a30e30af803",
    "actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16",
    "actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02",
    "actions/download-artifact@634f93cb2916e3fdff6788551b99b062d0335ce0",
}


def section(document, key, indent=0):
    """Read one mapping block using the workflows' explicit indentation."""
    lines = document.splitlines()
    start = next(index for index, line in enumerate(lines) if line == " " * indent + key + ":")
    selected = []
    for line in lines[start + 1:]:
        if line.strip() and not line.lstrip().startswith("#") and len(line) - len(line.lstrip()) <= indent:
            break
        selected.append(line)
    return "\n".join(selected)


def step(document, name):
    marker = "      - name: " + name
    lines = document.splitlines()
    start = lines.index(marker)
    selected = []
    for line in lines[start + 1:]:
        if line.strip() and not line.lstrip().startswith("#") and len(line) - len(line.lstrip()) <= 6:
            break
        selected.append(line)
    return "\n".join(selected)


class WorkflowPolicyTests(unittest.TestCase):
    def test_release_is_build_and_publish_only_with_integrity_guards(self):
        for removed in ("scripts/test.sh", "go test", "go vet", "test:e2e", "playwright install",
                        "smoke-enrichment-runtime.py", "diagnostics-", "workflow_run", "tests.yml"):
            with self.subTest(removed=removed):
                self.assertNotIn(removed, RELEASE)
        self.assertEqual(re.findall(r"^  ([a-z][a-z_-]*):$", section(RELEASE, "jobs"), re.M),
                         ["plan", "build", "publish"])
        build = section(RELEASE, "build", 2)
        publish = section(RELEASE, "publish", 2)
        self.assertIn("needs: plan", build)
        self.assertIn("needs: [plan, build]", publish)
        for guard in ("run: ./build.sh", "run: ./win-build.sh", "release.archive_manifest(path, target, version)",
                      "scripts/release.py stage", "CRYPTODESK_UPDATE_SCREENER: '0'", "VITE_EXPLICIT_TEST_BRIDGE: '0'"):
            self.assertIn(guard, build)
        self.assertIn("scripts/release.py publish", publish)
        self.assertIn("pattern: release-*", publish)
        self.assertIn("group: cryptodesk-main-release", section(RELEASE, "concurrency"))
        self.assertIn("queue: max", section(RELEASE, "concurrency"))
        self.assertIn("cancel-in-progress: false", section(RELEASE, "concurrency"))
        self.assertIn("branches: [main]", section(RELEASE, "on"))

    def test_checks_run_independently_on_main_pull_requests_and_manual_dispatch(self):
        triggers = section(TESTS, "on")
        for trigger in ("  push:", "    branches: [main]", "  pull_request:", "  workflow_dispatch:"):
            self.assertIn(trigger, triggers)
        concurrency = section(TESTS, "concurrency")
        self.assertIn("group: cryptodesk-tests-${{ github.event.pull_request.number || github.ref }}", concurrency)
        self.assertIn("cancel-in-progress: true", concurrency)
        for forbidden in ("workflow_run", "pull_request_target", "needs:", "secrets.", "GH_TOKEN", "contents: write",
                          "scripts/release.py", "run: ./build.sh", "run: ./win-build.sh", "continue-on-error:"):
            with self.subTest(forbidden=forbidden):
                self.assertNotIn(forbidden, TESTS)
        self.assertEqual(section(TESTS, "permissions").strip(), "contents: read")
        self.assertNotRegex(TESTS, r"(?m)^ +permissions:")
        self.assertIn("CRYPTODESK_UPDATE_SCREENER: '0'", TESTS)

    def test_every_previously_released_test_suite_has_an_independent_check(self):
        deterministic = step(TESTS, "Verify domain, storage, frontend and packaging")
        for command in ("./scripts/test.sh", "go test -race -tags webkit2_41 ./...",
                        "go test -race -tags 'production webkit2_41' .", "go vet -tags webkit2_41 ./..."):
            self.assertIn(command, deterministic)
        browser = step(TESTS, "Verify browser interactions")
        for command in ("require_node", "npx --no-install playwright install --with-deps chromium", "npm run test:e2e"):
            self.assertIn(command, browser)
        runtime = step(TESTS, "Assemble the pinned production Screener runtime")
        self.assertIn("require_node", runtime)
        self.assertIn('python3 scripts/screener_bundle.py --target linux-amd64 --destination "$RUNNER_TEMP/screener-runtime"', runtime)
        smoke = step(TESTS, "Verify packaged indicators, downloads and provider pacing offline")
        self.assertIn('scripts/smoke-enrichment-runtime.py "$RUNNER_TEMP/screener-runtime"', smoke)
        self.assertIn('--report "$RUNNER_TEMP/test-checks/enrichment-runtime.json"', smoke)
        for name, path in (("Preserve browser test diagnostics on success or failure", "frontend/test-results/"),
                           ("Preserve packaged runtime diagnostics on success or failure", "${{ runner.temp }}/test-checks/")):
            diagnostics = step(TESTS, name)
            self.assertIn("if: always()", diagnostics)
            self.assertIn("actions/upload-artifact@", diagnostics)
            self.assertIn("path: " + path, diagnostics)
            self.assertIn("if-no-files-found: ignore", diagnostics)
            self.assertNotIn("name: release-", diagnostics)

    def test_python_updater_integration_runs_after_pinned_runtime_install(self):
        driver = (ROOT / "scripts/test.sh").read_text()
        self.assertLess(driver.index("require_node"), driver.index("python3 -m unittest"))
        self.assertLess(driver.index("npm ci --ignore-scripts"), driver.index("python3 -m unittest"))
        for suite in ("npm test", "npm run catalog -- --check", "go test -race ./internal/... ./cmd/...",
                      "go test -tags bindings .", "go vet ./internal/... ./cmd/...", "npm run typecheck",
                      "npm run lint", "npm run build"):
            self.assertIn(suite, driver)

    def test_action_versions_and_checkout_credentials_stay_pinned(self):
        for name, workflow in (("release", RELEASE), ("tests", TESTS)):
            with self.subTest(workflow=name):
                actions = re.findall(r"(?m)^\s*(?:-\s*)?uses:\s+(\S+)", workflow)
                self.assertTrue(actions)
                self.assertTrue(set(actions).issubset(PINNED_ACTIONS), actions)
                checkouts = sum(action.startswith("actions/checkout@") for action in actions)
                self.assertEqual(workflow.count("persist-credentials: false"), checkouts)
                self.assertIn("go-version: '1.26.8'", workflow)


if __name__ == "__main__":
    unittest.main()

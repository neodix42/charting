# Start here — implementation mandate

## Deliver the application

Build CryptoDesk inside the repository containing this directory. The bundle itself contains
instructions and fixtures only. Do not finish by rewriting the requirements or generating
only another scaffold. Produce runnable source, tests, build tooling, and user documentation.

The chosen desktop stack is Go + Wails v2 with a React/TypeScript webview frontend,
Lightweight Charts v5, and SQLite. Go owns market data and persistence; the webview owns UI
and rendering. This is not a pure-Go chart canvas and not an Electron application.

FIRST LAUNCH: exactly one tab and one chart, in a 1×1 grid. The user may add up to
20 tabs total and up to 9 charts per tab. New tabs start with one chart. These are
maximum capacities, not the initial workspace. Do not pre-create hidden tabs/charts.
Later launches restore the actual saved workspace. The 180-chart example is test-only.

The maximum supported workload is 20 saved tabs × 9 chart configurations. In the default mode,
only the active tab is rendered and subscribed; inactive tabs keep their state and cache.
No arbitrary subscription-style paywall or paid account is part of this application.

## First actions

1. Read the repository's existing AGENTS.md files, inspect its tree, and run `git status --short`.
   Preserve existing and uncommitted work. Never use `git reset --hard`, `git clean -fd`,
   force-push, or delete unrelated files as a shortcut.
2. Read SPEC.md, ARCHITECTURE.md, IMPLEMENTATION_PLAN.md, and TEST_PLAN.md before coding.
   Read the remaining specialist documents before implementing their respective components.
3. Run `bash codex-cryptodesk/tools/preflight.sh`; inspect OS/toolchain versions. Missing GUI
   dependencies must not prevent writing and testing the independent domain/data packages.
4. Check current official references in SOURCES.md. Select mutually compatible versions,
   pin the Wails CLI/module and frontend dependencies, and record actual versions.
5. Use AGENTS.template.md as a concise root instruction template only if no root AGENTS.md
   exists. Otherwise merge a nonconflicting pointer to this bundle; do not replace existing
   project instructions. Never edit the user's global agent settings.
6. Begin stage 0, then implement the stages in sequence. Keep STATUS.md and DECISIONS.md
   accurate. Do not initialize a second Git repository inside this one.

If the repository is empty apart from this bundle, scaffold at the project root.
If it already contains unrelated code, place the app under `apps/cryptodesk/` and document
that decision instead of repurposing or replacing the unrelated application. If it already
contains a compatible app, extend it and preserve its architecture where reasonable.

## Read and use

- Product: SPEC.md, UI_UX.md.
- Design: ARCHITECTURE.md, CONTRACTS.md.
- Data: EXCHANGES.md, CANDLE_CORRECTNESS.md, fixtures/README.md.
- State: PERSISTENCE.md, schema/workspace.schema.json, examples/.
- Delivery: BUILD_LINUX.md, SECURITY_LICENSES.md, TEST_PLAN.md, DELIVERY_CHECKLIST.md.

## Authority and changes

The user's request and existing repository policies remain authoritative. Within this bundle,
SPEC.md defines the product, TEST_PLAN.md its observable acceptance requirements, and the
schema its initial portable workspace shape. Specialist documents explain implementation.
The examples illustrate the schema; they do not constrain which real instruments are supported.

Resolve ordinary technical choices yourself using these defaults. Document changes justified
by actual library APIs. Do not silently remove an exchange, reduce capacity, replace Go/Wails
with another desktop stack, or replace the persistence requirements with browser localStorage.
If an external dependency makes a requirement impossible in this environment, name the exact
failure and preserve the requirement as unverified or blocked rather than pretending it passed.

## Execution and reporting rules

Work in functioning vertical slices. Tests, diagnostics, and lifecycle cleanup belong in each
slice, not only at the end. Continue through all required stages when tools permit.
Do not ask for approval after every phase, but honor tool permission boundaries and protect
existing user data. Do not install system packages, change OS repositories, or run as root
without explicit approval.

A restricted environment is not permission to fake the GUI, market data, or verification.
Use deterministic tests with explicit fixture mode and separate live smoke tests. An external
403/429/451 or unavailable GUI must appear in the final report as such.

Before final delivery, complete DELIVERY_CHECKLIST.md and update STATUS.md with:
actual commands run, passing/failing/skipped tests, build outputs, known limitations,
and the first concrete task needed to resume unfinished work.

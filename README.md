# CryptoDesk — Codex implementation bundle

**Prepared: 2026-10-19 · Bundle version: 1.1.0**

This is a build specification and test-fixture bundle, not an implemented desktop app.
The deliverable Codex must build is a read-only Ubuntu cryptocurrency charting terminal:
Go + Wails v2 + React/TypeScript + TradingView Lightweight Charts + SQLite.

On first launch, create **exactly one tab containing exactly one chart**. The user can
add and remove content, up to **20 tabs total and 9 independent charts per tab**, using
Binance, Bybit, or Coinbase public spot data, with automatic workspace restoration.
These numbers are upper limits, NOT the default layout or pre-created hidden objects.

## Start

Extract this archive in your Git project's root. It adds only `codex-cryptodesk/`;
it does not include a root README, root AGENTS.md, go.mod, or package.json to overwrite.
If this bundle directory already exists, extract to a temporary directory and compare it.

Open Codex with the repository as its working directory and paste the contents of
[CODEX_PROMPT.txt](CODEX_PROMPT.txt). The entry point is [START_HERE.md](START_HERE.md).
No bootstrap installation is needed to read the instructions.

Optional, read-only local checks:

```bash
python3 codex-cryptodesk/tools/verify_bundle.py
bash codex-cryptodesk/tools/preflight.sh
```

The first command checks the shipped bundle, not the future application's correctness.
The second reports the development environment; it does not install or change anything.

## Contents

| File | Purpose |
|---|---|
| START_HERE.md | Implementation mandate, reading order, repository precautions |
| AGENTS.template.md | Small root-agent instruction template; merge, never overwrite |
| SPEC.md / UI_UX.md | Required behavior, defaults, and interactions |
| ARCHITECTURE.md / CONTRACTS.md | Component boundaries, lifecycle, bridge contracts |
| EXCHANGES.md / CANDLE_CORRECTNESS.md | Public APIs, normalization, reconnect and overlap rules |
| PERSISTENCE.md | Autosave, SQLite, versioning, import/export, crash recovery |
| BUILD_LINUX.md | Ubuntu dependencies, build verification, packaging |
| IMPLEMENTATION_PLAN.md | Ordered stages with working-code exit gates |
| TEST_PLAN.md / DELIVERY_CHECKLIST.md | Concrete acceptance tests and release evidence |
| SECURITY_LICENSES.md / SOURCES.md | Boundaries, attribution, primary references |
| STATUS.md / DECISIONS.md | Agent handoff and implementation decision records |
| schema/ | Versioned workspace exchange-format schema |
| examples/ | One-tab/one-chart starter; a separate capacity-only 20-tab / 180-chart test |
| fixtures/ | Synthetic candle and lifecycle regression cases |
| tools/ | Read-only preflight and bundle verifier |

## Scope boundaries

Required: real charting, public exchange adapters, layout editing, automatic persistence,
network recovery, repeatable tests, and an Ubuntu build.
Not required: trading, API-key management, derivatives, Pine Script, drawing tools,
indicators other than volume, account login, cloud sync, or TradingView account integration.
The optional all-feeds-live mode must not delay the core deliverable.

The supplied exchange symbols and numerical fixture prices are test inputs, not live
market observations or claims that a particular product is currently available.
Codex must discover instruments dynamically and validate actual endpoint behavior.

External API notes are dated reference observations. They are not a promise of future
availability. Sources are catalogued in SOURCES.md; Codex must recheck versions and schemas
before implementation and record any differences in DECISIONS.md.

---

The runnable application has been implemented at this repository root. See [APP_README.md](APP_README.md) for build/run/use and recovery instructions, [STATUS.md](STATUS.md) for actual test evidence, and [docs/verification](docs/verification) for reports. The original bundle documents above are retained.

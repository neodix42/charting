# Implementation status

**Bundle version 1.1.0, prepared 2026-10-19.**

This package contains instructions, schemas, examples and synthetic test fixtures only.
The application has NOT been implemented, built, or live-tested by preparing this bundle.

## User clarification (normative)

First launch: exactly 1 tab and 1 chart. User adds up to 20 tabs total and up to 9 charts
per tab. New tabs have one chart. Larger grid presets do not auto-create charts. Subsequent
launches restore the actual saved workspace. The 20×9 example is capacity-test-only.

## Stages

| Stage | Status | Evidence / blocker |
|---|---|---|
| 0 Repository/toolchains/contracts | Not started | Codex must inspect repository and environment |
| 1 Single live Binance chart | Not started | |
| 2 Dynamic workspace and persistence | Not started | |
| 3 Market lifecycle and history | Not started | |
| 4 Bybit and Coinbase | Not started | |
| 5 Restore and recovery | Not started | |
| 6 Tests and Ubuntu delivery | Not started | |

## Actual commands and outcomes

Codex: replace this placeholder with commands actually executed, return codes and results.
Do not list proposed commands as passed tests.

## Current blockers

None observed for app implementation: the user's repository/environment has not been inspected.
No live exchange connectivity or Ubuntu GUI result is asserted.

## Resume entry point

Read START_HERE.md, inspect existing repository instructions and status, run the preflight,
then execute stage 0. Maintain this file with the next concrete task after each work session.

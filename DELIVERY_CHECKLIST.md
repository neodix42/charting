# Release checklist — observed 2026-10-19

Evidence: docs/verification/REPORT.md; usage: APP_README.md. The bundle was unpacked at root. Checked items refer to implemented code and observed automated/live evidence, with manual limits stated separately.

## Product

- [x] First launch exactly1tab/1chart/1×1, verified Go/browser/actual native.
- [x] Add Tab creates one-tab/one-chart; Add Chart creates one actual chart.
- [x] Larger grids produce placeholders only; exact1-chart3×3 tested.
- [x]20tabs/9charts cap enforced frontend/Go/imports.
- [x] Small saved3+1workspace restores exactly; capacity fixture test/import only.
- [x] Tabs/grid proportions/reorder/maximize/independent controls/shortcuts implemented; reducer/browser checks pass.
- [x] All three public spot adapters/all six intervals implemented and deterministic/live tested.
- [x] Coinbase public5mrestriction addressed via trade-derived intervals; aliases rejected/labelled; overlapping volume stays provisional.
- [x] History/volume/precision/crosshair/scale/viewport code and regression tests, including bounded moving windows.
- [x] Per-chart up/down arrows, horizontal lines and two-click trendlines; UTC/price anchors persist through SQLite restart, JSON round trips and independent tab duplication. Drawing controls and zoom/resize/line-style/symbol-scope behavior verified in browser; actual Wails drawing placement/restart verified on Ubuntu (2026-10-22).
- [x] Single-click selection, per-chart Delete button and Delete/Del keys; draggable line endpoints, finite horizontal segments and same-ID edits/deletions persist across restart. Native Wails/WebKit verifies selection, preview isolation, endpoint release, button/keyboard deletion and exact SQLite restoration (2026-10-22).
- [x] Double-click drawing selection, preview movement, click placement and cancellation; preserve IDs/scopes and restore moved anchors through native SQLite restart. All 85 browser tests pass, including 17 drawing cases with physical 2× backing stores (2026-10-22).
- [ ] Full native manual header/splitter dragging and historical/manual-price viewport matrix (code/tests exist; manual pass not asserted).

## Reliability

- [x] Autosave/debounce/maxdeferral/ordered acknowledgments and controlled native-close flush tested.
- [x] Import/export code, validation/backups/reset/recovery/corrupt/future/migration/busy/crash/lock tests.
- [x] Active-tab-only rendering/leases/sharing/cleanup: browser and native180config/200switch checks.
- [x] Reconnect/gap/ratelimit/overflow/stalegeneration/cancellation/terminalretry regression tests.
- [x] Resource bounds and actual switching measurements retained with sampling limitations.
- [ ] Real desktop suspend/resume/forced disconnect and combined200switch/high-rate scenario remain unrun.
- [x] No trading/account keys/private endpoints/telemetry/paid gates.

## Build and report

- [x] Go race/vet; frontend23unit/8browser/typecheck/lint/build pass.
- [x] Actual native production Wails/WebKit smoke passes, separate from Chromium fixture tests.
- [x] All-provider real discovery/history/stream/minute transition results retained, including corrected initial failure.
- [x] Exact pinned modules/frontend lockfile and reproducible dev/test/build/userlocal installer scripts.
- [x] Source, Ubuntu26.04native release binary/archive, original SVG icon and .desktop launcher.
- [x] APP_README includes prerequisites/run/use/storage/backup/recovery/limitations.
- [x] Retained third-party LICENSE/NOTICE and visible TradingView creator attribution.
- [x] Actual environment/versions/metrics/manifest/checksum/report files.
- [x] STATUS/DECISIONS updated with current evidence and next manual verification task.
- [ ] Full manual dialogs/copy-paste/creator links/DPI/other Ubuntu releases/X11/dailyrollover not claimed.

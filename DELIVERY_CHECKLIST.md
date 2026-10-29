# Release checklist — observed 2026-10-19

Evidence: docs/verification/REPORT.md; usage: APP_README.md. The bundle was unpacked at root. Checked items refer to implemented code and observed automated/live evidence, with manual limits stated separately.

## Configurable Screener workers — 2026-10-29

- [x] Settings → Screener applies and persists Worker threads, default 5, valid range 1–20, alongside the download folder and outside workspace exports.
- [x] Downloader, Enrichment and manual Refresh obey the configured pool; legacy queue/view values cannot replace it and resume uses the current preference.
- [x] Production worker tests cover real concurrent transport calls, caps, stable order, cancellation/checkpoints, shared pacing and 403/429 cooldowns; settings/browser persistence and error handling pass.
- [ ] Native desktop interaction, live provider downloads and Windows execution for this change remain unverified; see STATUS.md for automated checks and build status.

## Screener periods and Live Alerts — 2026-10-28

- [x] Historical All periods/d1/h1 scope persists with drafts and named conditions; companion timeframe evaluation remains aligned to closed bars.
- [x] Explicitly enabled local Live Alerts refresh selected histories and indicators sequentially, evaluate the latest closed bars and prepare verified-recipient Telegram notifications with asset/condition/time/Entry/SL/TP.
- [x] Restart-safe selections/delivery claims, cancellation, partial failures, storage recovery, Telegram serialization and hidden-panel polling have offline Go/race/browser coverage.
- [x] Full deterministic/native-tagged checks, packaged runtime smoke and all new browser checks pass; drawing readiness regression fixed with unchanged geometry tolerance (see STATUS.md).
- [ ] Native desktop Live Alerts interaction, real overnight scheduling, Windows execution and actual Dukascopy-to-Telegram alert delivery are not claimed by offline tests.

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
- [x] Screener downloader runs 1–20 parallel symbols and resumes saved checkpoints after native close/restart, restoring selection/timeframe/category state; committed separately as 059f7a1 (2026-10-25).
- [x] Screener Enrichment uses pinned trading-signals for 40 indicators, with CSV header/period discovery, parameters, progress, per-file errors, exact original backups and restart discovery. Native Ubuntu verifies real downloaded SMA/Tenkan values and preserved data (2026-10-25).
- [x] Screener Enrichment adds timestamp-based UTC Weekday (Monday=1 through Sunday=7) and Hour (0–23), with Refresh recipe replay and Analysis fields (2026-10-25).
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

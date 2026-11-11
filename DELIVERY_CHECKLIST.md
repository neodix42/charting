# Release checklist — observed 2026-10-19

Evidence: docs/verification/REPORT.md; usage: APP_README.md. The bundle was unpacked at root. Checked items refer to implemented code and observed automated/live evidence, with manual limits stated separately.

## Stable order-metrics geometry — 2026-11-11

- [x] Hosted 400/401 failure is traced to an async metrics-card expansion moving Review order; two controlled late-value/error cases fail before the fix and pass after it.
- [x] Stable responsive card size, keyboard-accessible diagnostic scrolling and existing preview/confirmation rules pass all 49 related browser cases, typecheck/lint and 285 unit tests.
- [x] Linux production build, archive inventory/architecture/runtime provenance and root/release checksums pass with final verification included.
- [ ] Post-push hosted checks are tracked by Actions for this revision. Native desktop/funded/Windows GUI remain unrun.

## Hosted Ubuntu dependency setup — 2026-11-11

- [x] Cancelled test-run logs identify the Azure Ubuntu mirror DNS failure and warning-only stale-index reuse before any test; the Windows build succeeds.
- [x] Both workflows share a runner-only HTTPS archive selector with required-suite probes and fixed Ubuntu/kernel.org/Princeton choices; preserve signing/other repositories, fail index updates visibly and bound dependency setup.
- [x] All 160 Python tests, workflow contract checks and YAML parsing pass. Read-only fallback probes and Ubuntu-signed metadata verification pass without privileged local commands or host configuration changes; the complete 401-case local browser suite also passes.
- [ ] Replacement hosted Tests/Linux release checks remain tracked by Actions for this revision; see STATUS.md.

## Trade-history exit prices and pip/tick P/L — 2026-11-11

- [x] Exit price and Profit/Loss, pips occupy the requested adjacent columns; open/incomplete closing histories stay unavailable.
- [x] Native Dukascopy pips, fixed-entry Hyperliquid ticks, Long/Short partial exits, reversal caps, missing versus zero, legacy backfill and final persistence have provider/root race and pinned SDK regressions.
- [x] 285 frontend unit tests, typecheck/lint, provider/root race/vet and all 35 selected browser cases pass. Linux build, actual no-login bundled SDK probe, archive inventory/architecture/runtime provenance and root/release checksums pass; final verification is included.
- [ ] Post-push hosted verification is tracked by Actions for this revision; see STATUS.md.
- [ ] Native desktop, funded account execution and Windows GUI remain unrun; all tests use explicit fixtures and isolated profiles.

## Trade-history P/L and coherent JForex source builds — 2026-11-11

- [x] Refresh notices reserve space; healthy verification text and the requested long caption are removed. Browser checks prove identical table-header Y before/during/after refresh.
- [x] Legacy account-currency SL/TP risk recovers on refresh; remaining open size and confirmed zero/missing values are tested. Profit/Loss shows current/final values with native SDK/fill attribution and persisted closed results.
- [x] Executable-relative bridge selection, explicit overrides, symlink launchers, standalone installed fallback and fail-closed damaged bundles have Go regressions. Source/root/build/bin/release/development outputs select their matching bridge without changing an installation or SDK pin.
- [x] 284 frontend unit tests, 154 Python tests, typecheck/lint, provider/root race/vet and offline pinned SDK checks pass. Both related browser groups pass (38 trading/money/history cases and 40 header/JForex/chart/history cases), including runner-font layout stress.
- [x] Full native Go race/production/vet checks, Linux build, all three executable-relative real-bundle probes and archive inventory/architecture/runtime provenance pass with unchanged SDK/API pins. Final documentation is included in the archive and root/release checksums are verified.
- [x] Post-push Tests run43013005387 and both platform builds/publication pass for 269f7f8 (v0.1.66), including 400 hosted browser cases and packaged-runtime checks. Native desktop/funded/Windows GUI checks remain separate.
- [ ] Native desktop, funded Demo/Testnet/Live execution and Windows GUI remain unrun; no real account operation or notification is used for verification.

## Hosted browser fixes and open-trade chart levels — 2026-11-11

- [x] Both browser failures from Tests run42983193786 reproduce locally and pass with production fixes; geometry, profitability checks and original timeouts remain intact.
- [x] Existing/new open platform trades display native entry/SL/TP lines with order IDs on matching Dukascopy/Hyperliquid-perpetual charts, including line charts and restart. Account/environment filtering, closure cleanup, active-grid deduplication and stale-response protection are covered.
- [x] All 283 frontend unit tests, typecheck/lint, 14 focused browser checks and root Go race/vet checks pass. Linux build, packaged enrichment/download/pacing smoke and actual offline Telegram chart rendering pass.
- [x] Complete 396-case browser suite, all native Go race/production/vet checks, archive inventory/provenance and final root/release checksums pass; final STATUS.md is included.
- [ ] Hosted Tests have not been rerun for the local changes; see STATUS.md.
- [ ] Native desktop, funded trading/close execution and Windows GUI remain unrun; verification uses isolated profiles and synthetic clients without messages or account operations.

## Platform trade history — 2026-11-11

- [x] Trade history follows Predictions, lists only locally claimed Dukascopy/Hyperliquid submissions, and shows the requested prices, sizes, execution dates, account-currency SL/TP estimates, submission balance and supported position margin.
- [x] Existing provider journals survive restart; native ID/client label and account/environment verification exclude outside trades. Legacy unknown balances/conversions remain unavailable; actual zero and explicit currency labels are retained.
- [x] Confirmed Close uses the exact JForex position or a verified sole Hyperliquid exposure with reduce-only IOC; partial/uncertain results, one-use durable claims, protective-order ownership and exchange-confirmed close times have offline production tests.
- [x] Provider/market/shared-trading/root Go race suites, vet, pinned JForex offline SDK tests, 279 frontend unit tests, typecheck/lint and 49 distinct related browser cases pass.
- [x] Linux executable/archive rebuilt; archive inventory/JForex provenance and root/release checksums pass, with final STATUS.md included.
- [ ] Native desktop interaction, funded Demo/Testnet/Live order/close execution, Windows GUI and hosted CI remain unrun. No real account was used for verification. Install the matching JForex bridge and restart before the manual check.

## Explicit chart trading — 2026-11-09

- [x] Hyperliquid private API-agent setup/help/read-only test and source-scoped chart Buy/Sell review/confirm tickets.
- [x] One-use expiry/account/precision fences, persistent intent/nonce claims, no automatic signed-write retries and explicit partial/uncertain handling covered by provider race tests.
- [x] Exact official signing vectors and installed JForex sizing/thread/protective-order tests pass; secrets remain outside exports and read responses.
- [x] Local deterministic, native/bindings, frontend and related browser checks pass. Linux build is verified before the authorized commit/push; hosted Tests are monitored afterward.
- [ ] Native desktop, funded/Testnet/Demo execution and Windows trading interaction remain unrun; no real orders were placed during implementation.

## Hyperliquid downloaded history — 2026-11-09

- [x] Source selectors retain Dukascopy defaults, independent Hyperliquid choices, all-selected initialization and deliberately empty selection; source-filtered enrichment applies before pagination.
- [x] D1 actual availability, source/market/native identity, daily-derived Monday W1, decimal volume, durable extension/recipe replay, closed-bar provenance and interrupted replay recovery are covered by production regressions.
- [x] D1/W1 chart history works with optional storage disabled; source isolation, sparse-page merges and shared Live Alerts history/required indicators are covered by race tests.
- [x] Frontend build/lint/230 unit tests and 44 related browser tests pass. Runtime 97 tests, independent Go race packages, complete final Screener race rerun, root bindings/native race and vet pass. Linux production build succeeds.
- [x] Isolated real BTC/HYPE daily/weekly download, SMA enrichment, backward extension, full BTC daily snapshot and alert preparation/cache reuse pass without Telegram sends.
- [ ] Native desktop interaction, Windows execution and full live-universe timing remain unrun; see STATUS.md for exact commands and the next manual check.

## Retained JForex dependency cache — 2026-11-06

- [x] Tests and release builds restore/verify/save the same manifest-pinned SDK cache before subsequent work; valid client/dependencies are reused without vendor requests.
- [x] Failed downloads preserve previous files. Classpaths exclude older/unlisted cached JARs; cache-transfer inventory/checksum tests and real offline bridge/bootstrap checks pass.
- [x] Hosted seed run 42235757876 verified all 51 artifacts and saved the shared cache. The temporary unpublished transfer was removed. Resumed release/Tests jobs are monitored for the submitted revision.

## Resilient Live Alerts and editable asset sets — 2026-11-06

- [x] Per-asset history/warm-up/timeouts and Telegram failures retain details and allow remaining assets and later alerts to run. Completed scans reach 100% with skipped counts; actual cancellation and persistence failures remain distinct.
- [x] Assets dialog stages per-source selections until Save; defaults/migration retain all eligible assets, native market identities and required companion histories. Existing enabled rows, counters, claims and restart behavior are covered by isolated regressions.
- [x] Worker stages and provider waits are forwarded to row progress outside the manager lock. Scan errors no longer create a title-level error box.
- [x] Full Go race suite, production-tagged app race tests, vet, generated bindings, typecheck/lint, 228 frontend unit tests, 31 related browser cases and Linux build/archive/checksums pass. Artifacts are rebuilt from the submitted commit; hosted jobs are monitored after submission.
- [ ] Native desktop interaction, Windows execution and real provider/Telegram delivery timing were not exercised. Actual-profile investigation was read-only.

## Sequential alert checks and row progress — 2026-11-06

- [x] Check history has per-alert Progress with phase, current asset, percentage and manual/scheduled mode; the aggregate running-progress block is removed.
- [x] Scheduled/manual checks dispatch one complete alert at a time in table order across sources, including all-Dukascopy batches. Queued and skipped rows retain their previous actual-attempt history and claims.
- [x] Check now queues and acknowledges without Manager metadata reads; fast Stop and duplicate-request handling remain responsive under a held Manager lock.
- [x] Focused Go race regressions, typecheck/lint, 226 frontend unit tests and 30 related browser cases pass, with a three-case rerun validating omitted zero-percent JSON fields.
- [x] Final full Screener race suite passes (174.1 seconds), along with independent/bindings Go checks, vet, Wails model generation and Linux build/archive/checksums; clean artifacts are rebuilt from the submitted commit and hosted jobs are monitored.
- [ ] Native desktop interaction, Windows execution and live venue-to-Telegram timing remain separate checks.

## Per-alert assets window — 2026-11-05

- [x] Alerts table has an Assets column for enabled and disabled rows; the standalone Monitored assets section is removed.
- [x] Dukascopy preserves analyzed targets/profitability/scope failures; explicit public catalog discovery preserves active Spot/Perpetual identities, warnings and retry without downloading candles or saving configuration.
- [x] Typecheck/lint, 224 unit tests, 25 related browser cases, production Linux build/archive/checksums and whitespace checks pass. Hosted jobs are monitored after submission.
- [ ] Native desktop and Windows interaction remain separate checks; catalog tests use explicit browser fixtures, with no real provider request or Telegram delivery.

## Named alerts and responsive Stop — 2026-11-05

- [x] Named Add/Edit/Remove entries reuse a condition independently across source/period; row and header checkboxes preserve master delivery state.
- [x] Migration preserves active/paused rows, original identities/claims/counters, and readable alert/rule attribution; isolated actual-profile verification leaves original files unchanged.
- [x] Stop cancels/saves without waiting for Manager.mu or full Info; Stopping... covers pending operations and worker cleanup with failure/stale-response recovery.
- [x] Full Screener race suite, other Go/cmd race tests, bindings/vet, 221 frontend tests, 21 combined browser cases and final Stop regressions pass.
- [x] Production Linux build/archive/checksums and documentation pass; hosted jobs are monitored for the submitted commit.
- [ ] Native desktop timing and Windows execution remain separate checks; hosted build verification is reported independently.

## Live Alerts sources, partial delivery counts and graceful shutdown — 2026-11-05

- [x] Data source per condition defaults to existing Dukascopy; public overrides cover all tradable spot/perpetual markets with inherited periods and separate history/claim identities.
- [x] On-demand required closed-bar fetch, pinned-runtime indicator/companion warm-up, durable cache reuse/journal recovery, publication retries and refusal cancellation pass production tests.
- [x] Existing enabled configs/claims survive; partial confirmed sends count once, with conservative idempotent repair of provable old records and no automatic resend.
- [x] Red failed-status popup, plain Recent errors and graceful-close message preserve focus/save/timeout recovery in browser checks; parallel shutdown preserves worker/storage ordering in race tests.
- [x] Full independent Go race/vet, bindings race/vet, 219 frontend unit tests and real two-market Hyperliquid preparation/evaluation/cache-reuse smoke pass without Telegram or user-profile writes.
- [x] Production Linux executable/archive and checksums pass; APP_README/STATUS/DECISIONS document behavior and limits.
- [ ] Native desktop timing/full-venue alert delivery and Windows execution remain unrun; see STATUS.md.

## Hyperliquid chart source — 2026-11-05

- [x] Public dynamic spot/native-perpetual/HIP-3 discovery, exact market identities and partial-catalog warnings/retry.
- [x] Native trade history/live candles and Monday weekly derivation; cumulative volume, delayed seeds, gaps and retention limits covered.
- [x] Separate live Bid/Ask, nullable empty sides, timestamp ordering, shared subscriptions and disconnect cleanup.
- [x] Workspace/drawing/import/cache persistence, active-feed isolation and cancellation pass production Go/race/browser tests; existing provider browser cases remain passing.
- [x] Real discovery, all seven interval histories and candle/quote delivery verified for BTC and xyz:NVDA; HYPE spot also passes every history interval and live weekly price/quote delivery; counts are recorded in STATUS.md.
- [x] Production Linux executable/archive builds and checksums pass; original specification bundle and user data are preserved.
- [ ] Native desktop Hyperliquid interaction, Windows build/execution and exhaustive live checks of every discovered market remain unrun.

## Live Alerts error details — 2026-11-04

- [x] Status/settings, scope, failed checks and failed/uncertain deliveries use keyboard-accessible expandable error icons with full stored details.
- [x] Duplicate publication failures collapse once; distinct contexts, partial Telegram counts, persisted history and delivery claims remain correct in Go/race/browser tests.
- [x] Source-based loading explanation documented, with no claim that profile latency was measured or optimized.
- [x] Frontend typecheck/lint/214 tests, all Live Go race regressions, bindings/vet, nine browser tests, Linux build/checksums and diff checks pass.
- [ ] Native desktop/Windows interaction and actual Telegram/provider verification remain unrun; restart the rebuilt executable for the manual disclosure check.

## Automatic chart history retries and Low/High — 2026-11-04

- [x] JForex seed failures retry indefinitely with capped backoff, continuous live quotes, checkpoint reuse, stale-result fencing and retry cancellation; actual SDK-interface offline tests pass.
- [x] Chart and Enrichment Low/High use complete trailing wick windows; validation, corrections, persistence, refresh recipes and browser controls pass.
- [x] New chart SMA defaults to red/200; saved parameters/colors persist unchanged.
- [x] Scoped Go race/vet, bindings tests, frontend typecheck/lint/213 unit tests, 25 browser tests, 97 runtime tests, Linux build/checksums and packaged offline smoke pass; see STATUS.md.
- [ ] Install the rebuilt bridge explicitly and verify authenticated timeout recovery/native desktop controls. Windows execution remains unrun; no installation or user-data changes were performed.

## Installed JForex paths and workspace preservation — 2026-10-29

- [x] Linux selects the user's installed bridge independently of checkout/executable paths; Windows portability and explicit test overrides remain supported.
- [x] System Java takes priority over an IDE's JAVA_HOME; discovery never rewrites installed runtimes or changes global settings.
- [x] Runtime discovery, native race and bridge packaging checks pass; an isolated copy of the real saved workspace loads all 4 tabs and 11 charts without resetting the original.
- [x] Mixed-market restart/export/import regression includes Dukascopy/forex drawings alongside crypto spot and perpetual feeds.
- [x] Linux/Windows archives at implementation commit 7e025b0 pass clean-source provenance, complete inventory and checksum validation; root Linux executable rebuilt.
- [x] Hosted Desktop release run 40929447201 publishes v0.1.26 successfully.
- [ ] Hosted Tests run 40929447051 is pending; native GUI execution remains unverified.

## Responsive JForex charts and Settings — 2026-10-29, `main` branch

- [x] Running-bridge thread evidence identifies synchronous historical HTTP loading blocking the shared command queue.
- [x] Asynchronous bounded history, independent controls/live subscriptions, 45-second data deadlines, cancellation and stale callback protection pass real SDK-interface regressions.
- [x] Login deadline/cancellation recovers a blocked control process; Settings close saves credentials then cancels pending checks without stale response waits.
- [x] Wider responsive Settings tabs and corrected Dukascopy labels pass 18 JForex, 27 Predictions/Telegram and 4 appearance browser cases, plus Go/native/Java/Python/frontend checks.
- [x] Final Linux/Windows archives pass complete inventories, clean source provenance and checksums; hosted release run 40923934923 publishes v0.1.25.
- [x] Final hosted Tests run 40923934860 passes, including 254 browser cases; see STATUS.md.
- [ ] Updated-bridge Live historical/tick delivery and native desktop/Windows GUI execution remain unverified; Live login/catalog success is user-reported.

## Saved forecast removal — 2026-10-29, `predictions` branch

- [x] Saved forecasts offer right-click/keyboard Remove, persistent successor selection, last-chart clearing and newest-100 inventory backfill.
- [x] Removal preserves source CSVs/models/unrelated snapshots, validates private paths and handles selection/write/unlink failures, restart and stale UI requests.
- [x] Independent/facade/native race tests, native vet, frontend checks, 198 unit tests and 18 Predictions browser cases pass; final menu focus regression also passes.
- [x] Linux/Windows archives, complete inventories, source provenance and checksums pass at clean implementation commit 3d4bdf1.
- [x] Hosted Tests run 40875128658 passes all 249 browser cases, frontend/Python/Go/native checks, Java 17 bootstrap and the packaged runtime smoke; see STATUS.md.
- [ ] Native desktop interaction and Windows execution remain unverified.

## Enrichment periods and Short simulations — 2026-10-29

- [x] Catalog-based Enrichment Period scopes the entire batch before consolidation and leaves other periods unchanged; counts span all pages and recipes remain reusable by Refresh.
- [x] Analysis Long/Short simulation retains legacy Long defaults and saved selections; Short enforces TP below Entry below SL, with correct risk targets, exits and chart prices.
- [x] Production race/facade/native-tagged checks, oracle/worker replay tests, 198 frontend unit tests and 63 distinct focused browser cases pass.
- [x] Both release archive targets, complete inventories and checksums pass, including the combined forecast-removal implementation.
- [x] Combined hosted Tests run 40875128658 passes the full suite, including all 249 browser cases; see STATUS.md.
- [ ] Native desktop interaction and Windows GUI execution remain unverified.

## JForex forex chart bridge — 2026-10-29, `predictions` branch

- [x] Optional Dukascopy source and Forex identity, account-based fiat pair discovery, native Bid history/live bridge and all seven UTC chart intervals.
- [x] Settings → Dukascopy supports Demo/Live credentials, Test connection, transient Live PIN challenges, cancellation and first-configuration recovery without mixing account caches.
- [x] Private settings, shared process/subscription lifecycle, quote-health preservation and owner-exit cleanup have offline protocol/race/facade coverage; the actual SDK initializes without credentials.
- [x] Linux/Windows packaging and user installation include and validate our bridge plus exact SDK dependency manifest. Java 17+ prerequisites and first-use SDK installation are documented.
- [x] Local Linux/Windows archives, checksums/inventories and actual isolated Linux installation pass. Hosted Tests run 40866094626 passes all 238 browser cases, Java 17 bootstrap and the full deterministic/runtime suite; see STATUS.md.
- [ ] Authenticated JForex login/live ticks, native desktop interaction and Windows GUI execution remain unverified; no JForex credentials were supplied.

## Local Predictions — 2026-10-29, `predictions` branch

- [x] Independent Predictions page/settings default to Toto-2.0-313m, with downloaded asset/period, 512/1024 closed-bar context and future horizon default 20.
- [x] Real model preparation and inference pass on isolated synthetic inputs; source CSV is unchanged and timestamped forecasts restore identically after restart.
- [x] Private pinned runtime/model downloads, cancellation, closed-data validation, independent settings and immutable snapshot persistence have automated coverage.
- [x] All 191 frontend unit tests, 12 Predictions browser cases and 33 related regressions pass, including failure paths and close handling; native build/checksums pass.
- [x] Hosted branch Tests run 40833324464 passes, including all 225 browser cases and the packaged offline runtime smoke; see STATUS.md.
- [ ] Native desktop interaction, Windows execution and financial forecast quality/calibration remain unverified.

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
- [x] Read-only market integration; no order execution, wallet, telemetry or paid gates. Optional JForex credentials remain outside portable workspaces.

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

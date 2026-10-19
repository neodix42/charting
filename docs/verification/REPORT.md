# CryptoDesk verification report — 2026-10-19

The application is implemented at the repository root. The specification bundle was already unpacked here, rather than under a `codex-cryptodesk/` subdirectory. The initial working tree was clean; existing specification, schema, fixtures, examples and license were retained. No system packages/settings or global agent configuration were changed.

## Delivered implementation

Go/Wails v2 desktop shell, React/TypeScript/Lightweight Charts v5 frontend, SQLite essential workspace state, real public Binance/Bybit/Coinbase spot adapters, all six required intervals, dynamic tabs/grids/splitters/reordering, active-tab-only rendering, shared provider sockets, bounded history and caches, instrument discovery, candle/volume/OHLCV/scale/timezone controls, historical/live viewports, autosave, controlled shutdown, strict JSON import/export, backups/recovery and single-profile locking. Source and portable workspace shape remain independent from runtime feed state.

First launch and new tabs contain exactly one chart. A missing store is distinguished from corrupt/future saved data. Larger grids contain placeholders, never pre-created chart configurations. Limits are 20 actual tabs and nine actual charts per tab, validated in Go and frontend/import code. Real saved counts/order/settings are restored.

## Observed commands

| Command | Result/evidence |
|---|---|
| `bash tools/preflight.sh` | Ubuntu 26.04.1, x86_64, Wayland; Go1.26.8/Node24.19.0/npm11.17.0; development GTK/WebKit headers initially missing |
| `scripts/bootstrap-native-sdk.sh` | Downloaded/extracted86 Ubuntu packages locally (no installation/root); versions in native-sdk-package-versions.txt |
| `go mod download all`, `go mod tidy` | Pinned dependencies resolved; go.mod/go.sum retained |
| `scripts/test.sh` | PASS: all independent Go race tests/vet, npm ci, typecheck, ESLint,23unit tests, production frontend build; deterministic-final.log |
| `source scripts/native-env.sh; go test -race -tags webkit2_41 ./...` | PASS across root/domain/exchange/market/storage/cmd; go-all-final.log |
| `source scripts/native-env.sh; go vet -tags webkit2_41 ./...` | PASS |
| `cd frontend && npm run test:e2e` | PASS8/8, no skips/flakes; browser-final/results.json and screenshots. Explicit fixture bridge, separately labelled |
| `scripts/smoke-desktop.sh` | PASS in actual Wails/WebKit production window, no development server/fixture bridge; desktop-20261019T164147Z/{fresh,restore,capacity}.json and logs |
| `go run ./cmd/smoke -duration 65s` and Binance-only rerun | All provider discovery, six REST intervals each, real stream updates and one observed minute transition/provider. Original failed Binance attempt retained separately |
| `go run ./cmd/smoke -duration 8s` on final exchange code | PASS all three discovery/history/stream; live-final-all-providers.jsonl. Short-run transition counter is not rollover evidence |
| `.tools/wails build -tags webkit2_41 -trimpath -m -nosyncgomod` | PASS, actual native production binary |
| `scripts/build-linux.sh` | Release build/archive/checksum/manifest command; final packaging output retained in build-final.log |
| `git diff --check`, `bash -n scripts/*.sh` | PASS |

The original instruction-bundle verifier passes all34checksums, example schemas, malformed-workspace cases and Decimal fixtures when run on an isolated archive of original HEAD. Running it in the implemented checkout reports `Changed file: DECISIONS.md`, because STATUS/DECISIONS/README are intentionally updated as required. The original HASHES.json was preserved; that checksum-only failure is distinct from application tests.

## Acceptance mapping

| Group | Status and practical scope |
|---|---|
| A01–A14 startup/capacity/workspace | Automated Go/frontend/browser/native coverage:1/1, placeholder-only expansion, caps, single-chart new tabs, small saved3+1 restoration, deep-copy identity, grid-shrink rejection, rename/reorder/maximize. Native header/splitter dragging has production code and reducer tests; full manual interaction remains below |
| B01–B12 market correctness | Deterministic production parser/aggregation/history tests for all providers/intervals, cumulative replacement, decimal trades/late order, UTC boundaries, overlap/provisional volume, aliases, reconnect/backfill, bounded overflow, stale generations, pagination/windowing and listener snapshot revisions. Real live matrix below |
| C storage/recovery | Exact JSON+SQLite roundtrip of all three examples; malformed/future/duplicate/unknown-field imports preserve state; revision ordering/concurrency; busy failures; backups/reset/recovery; transactional migration rollback/pre-migration backup; process locking; acknowledged commit survives a killed isolated child |
| D lifecycle/resources | Browser and actual native180-configuration/200-switch checks; nine mounted charts/leases, bounded queues, shared feeds; StrictMode; explicit boundary coalescing; stale-request/cancellation/retry races. Combined200switches with a separately injected high-rate stream was not run; high-rate overflow and switching are tested separately |
| E actual desktop | Automated production Wails smoke PASS; comprehensive manual native matrix remains UNRUN (see docs/DESKTOP_SMOKE.md) |
| F live providers | PASS this environment for discovery/history1m,5m,15m,1h,4h,1d and live1m transition on each provider. Actual daily rollover and forced desktop disconnect/suspend not observed |

The final native run includes all final historical-cache, persistent saved-range-error and tiny-volume formatting fixes. The normal release excludes only the explicitly tagged automation/metrics methods.

## Live outcomes and resolved failures

| Provider/product | Discovery | Six bounded REST histories |65-second live observation |
|---|---:|---|---|
| Binance BTCUSDT |3689spot products | PASS |35live/provisional updates, one minute transition |
| Bybit BTCUSDT |527spot products | PASS |56updates, one minute transition |
| Coinbase BTC-USD |925spot products | PASS |251trade-derived updates, one minute transition |

Binance initially returned17.8MiB metadata exceeding the original8MiB response bound. The adapter now requests SPOT metadata without bulky permissionSets and retains a32MiB hard bound. Live observation also exposed case-distinct JSON names such as `e/E` and `v/V`; explicit wire fields and a complete synthetic provider-envelope regression fix Go encoding/json case matching. The initial failure is kept in live-initial-and-bybit-coinbase.jsonl; successful Binance65s evidence is live-binance-final.jsonl. Final shorter all-provider smoke is live-final-all-providers.jsonl. These are observed endpoint results on this network, not availability guarantees.

## Measurements and limits

Native capacity run:20tabs/180configurations, nine canvases,200 tab switches in 29.865 seconds; median 144 ms / p95 176 ms / max 281 ms. Final diagnostics:9leases,6active unique feeds,18cached feeds,8997 cached candles,3provider sockets,0queued/buffered frontend events. Completed historical-window requests are coalesced/cached and invalidated on eviction; retries restart terminated feeds immediately. Raw metrics/samples are retained in capacity.json.

Browser explicit-fixture run:200switches, median214ms/p95307ms; GC JS heap12.49→15.77MB. This modest retained growth is measured, not proof of leak freedom. Native Go heap and process-tree CPU/RSS samples are also raw observations. The final metrics enumerate children across all OS threads (Wails and two WebKit helpers observed). Process-tree RSS rose from 527.7 MB to checkpoint values 895.8/948.9/968.0/965.4 MB, then 678.7 MB after grace; it sums shared pages and is not proportional-set memory. Post-Go-GC heap checkpoints were 12.32/12.09/12.12/12.06 MB, then 10.50 MB after grace. Process-tree CPU delta was 47.82 CPU seconds over 32.39 wall seconds on 32 logical CPUs. Earlier reports sampled only leader-thread children and undercounted some process trees; use the final report for measurements. GPU/browser GC and long-duration behavior require additional profiling. No absolute CPU/FPS/memory promises are asserted.

## Unverified behavior and next steps

No essential adapter/persistence component is a stub, and no system dependency currently blocks the observed Ubuntu26.04build. Remaining verification:

- Full native file-dialog/overwrite cancellation, creator-link launch, copy/paste, keyboard focus and header/splitter dragging matrix.
- High-DPI, other monitors, X11 and other Ubuntu release/runtime combinations.
- Real OS suspend/resume, forced network outage repair, quiet-market behavior over long periods and actual daily rollover. Deterministic reconnection/overflow/boundary tests do not establish those live observations.
- Long-duration resource profiling and a combined200-switch injected high-rate desktop scenario.

First concrete follow-up: run the normal release binary with an isolated `--data-dir`, follow docs/DESKTOP_SMOKE.md's remaining manual checks, and record native dialogs, resize/maximize, historical/manual-price viewport restore, suspend/reconnect and high-DPI outcomes. Existing user workspace data is never required for tests.

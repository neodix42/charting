# Acceptance and regression test contract

Tests must exercise production logic, not a parallel test-only implementation. Isolate test
data directories. Normal CI is deterministic/offline; public exchange smoke tests and actual
desktop tests are separate with explicit prerequisites. The bundle verifier only checks this
instruction package and fixtures; it does NOT prove the future application passes these tests.

## A. First launch, counts and user controls — mandatory

| ID | Setup/action | Required result |
|---|---|---|
| A01 | Start with a new isolated data directory | Exactly 1 tab, 1 chart, 1×1 grid; no hidden pre-created charts/tabs |
| A02 | Choose 3×3 with the initial one chart | Still 1 actual chart/canvas/lease; eight Add Chart placeholders |
| A03 | Add Chart once | Exactly one chart added, independent ID and controls |
| A04 | Grow selected tab to 9 charts | All 9 work; Add Chart/duplicate chart disabled at cap |
| A05 | Invoke direct backend operation/import adding a tenth chart | Validation error; original state unchanged |
| A06 | Add Tab once from a 9-chart tab | One NEW tab with ONE chart/1×1 grid, not a nine-chart clone |
| A07 | Grow workspace to 20 tabs | All accessible via scrolling/navigation; Add/Duplicate Tab disabled |
| A08 | Try a 21st tab via UI, backend or import | Rejected without deletion or partial state change |
| A09 | Remove charts/tabs until only one of each remains | Further removal disabled; no invalid empty state |
| A10 | Save 2 tabs with 3 and 1 charts; restart | Exactly those counts, order, active tab, selections and proportions restored |
| A11 | Restart with existing workspace | No default or capacity fixture is loaded over it |
| A12 | Duplicate a tab, then edit the duplicate | All IDs unique; original chart settings unaffected |
| A13 | Shrink grid below chart count | Rejected with message, no chart lost or silently hidden |
| A14 | Rename/reorder/maximize/restore | Content and identity preserved; temporary maximize leaves saved grid intact |

A01/A02/A06/A10 are explicit safeguards for the user's clarification. A full 20×9 workspace
is a capacity test, NEVER the initial setup. Implement startup tests against an absent DB,
an existing one-chart DB, and an existing smaller-than-capacity DB.

## B. Data correctness

| ID | Case | Required result |
|---|---|---|
| B01 | Each provider's history, all six intervals | UTC seconds, ascending unique starts, correct precision and base-volume mapping |
| B02 | Binance/Bybit cumulative update repeated/stale | Idempotent replacement, not summed volume or a stale downgrade |
| B03 | Trade duplicates/out-of-order/late corrections | Exact decimal OHLCV matches fixtures; event-time ordering |
| B04 | Exact interval boundary and UTC daily boundary | New bucket only at correct boundary; display timezone changes no data |
| B05 | Coinbase live interval selection | 1m is not 5m; all requested intervals correctly derived/native |
| B06 | REST/stream overlap without trade watermark | Provisional volume, never naive REST-plus-buffer double counting |
| B07 | Gap/reconnect/buffer overflow | Visible reconciling state, backfill and dedup; no fabricated completeness |
| B08 | Two candle keys in one coalesced UI flush | Both delivered in ascending order |
| B09 | Missing/invalid data/quiet market | Honest status; heartbeat health distinct from trade freshness |
| B10 | Pair/timeframe changed during REST call | Old-generation response cannot update new chart |
| B11 | Older-history pagination with page overlap | Progress, no duplicates, no viewport shift or infinite request loop |
| B12 | Coinbase aliased public -USDC products | Explicit handling/label or rejection; no silent market substitution |

Use `fixtures/trades.json`, `fixtures/native-candles.json`, `fixtures/history-pages.json`,
and `fixtures/lifecycle-cases.json`. Add actual provider envelope parser fixtures from the
current public documentation with synthetic values and clear source provenance. Never treat
the shipped normalized fixtures as evidence a provider's wire parser has been tested.

## C. Storage and recovery

Roundtrip all three workspace examples against schema and database. Validate UUID uniqueness,
activeTabId membership, grid capacity, track fractions, enum/range/string limits and file size.
Check failed imports (0/21 tabs, 0/10 charts, duplicate IDs, unsafe URL fields, future version)
leave the existing workspace byte-for-byte logically unchanged.

Simulate reversed autosave response order, a stale save request, continuous splitter dragging,
DB busy/write failure, and normal exit immediately after a viewport change. Saved indicator
must describe the acknowledged revision only. Test confirmed reset and valid-backup recovery.
A controlled kill after an acknowledged commit restores that commit on restart; no claim
is made for edits still in frontend memory. Use temporary directories and never kill an
unrelated user process. Test two-instance protection and safe migration rollback.

## D. Rendering, lifecycle and performance

Load the maximum example only through test/import. Diagnostics must show at most 9 independent
chart instances for the active tab, not 180. In a one-chart 3×3 grid, show one instance and
one desired chart lease. Identical visible feed keys share one upstream stream; different
charts keep independent viewports even when sharing data.

Switch between tabs at least 200 times with mocked high-rate data. After the configured grace
and work cancellation, no inactive chart retains callbacks, request handles or leases. Check
React StrictMode double mount/unmount. Test closing an in-flight chart and switching back
during pending unsubscribe. Race-test shared Go state.

Measure baseline, active 9-chart, and full-workspace resident memory/CPU and tab-switch latency
on a documented machine. Look for monotonic memory growth after repeated cycles and garbage
collection. Use generous regression thresholds established from actual measurements; do not
invent absolute CPU/FPS promises. Confirm bounded history/caches under long scrolling.

## E. Desktop manual smoke

Run a production Wails binary on a real Ubuntu desktop with no Vite/Wails dev server. Verify
first-run 1/1, adding tabs/charts, mixed providers, all intervals, history, resize/maximize,
keyboard focus, dialogs, theme, time formatting, attribution, network disconnect/reconnect,
normal close/restart, and a small saved workspace. Repeat restart with the capacity example.
Record X11/Wayland, DPI, OS and WebKit version. Browser E2E is useful but not a substitute for
this desktop smoke. Mark this section unrun if the environment has no display.

## F. Optional live smoke matrix

For Binance, Bybit and Coinbase, discover an actually available spot product, request a bounded
history page, attach a live stream, and record native/derived behavior. Observe at least one
1m candle transition when permitted. For long intervals test parser/aggregation transitions
with deterministic time; do not claim a live daily rollover merely because a fixture passed.
Use conservative request rates and stop on restriction. No credentials required.

## Evidence format

For every acceptance group, list status (pass/fail/blocked/unrun), exact command or manual
steps, observed outcome, artifact/log path, environment and remaining issue. Keep release
verification evidence under `docs/verification/` or an equivalent documented path. A skipped
live test is not a passed integration. A built bundle is not a built app.

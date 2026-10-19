# Implementation stages and exit gates

Implement working code in sequence. Use short internal plans where useful, but this file
is not an invitation to deliver only more planning. Do not require user approval between
ordinary code/test steps. Preserve permission boundaries for privileged/system changes.
Record actual evidence in STATUS.md after each stage. Do not mark a blocked check passed.

## Stage 0 — repository and contracts

Inspect existing instructions/files/git status, choose root versus apps/cryptodesk, run
preflight, verify sources, and pin compatible toolchain versions. Scaffold Wails v2 plus
React/TypeScript safely. Add domain types, workspace validation, and a first-run constructor.
Read the official lightweight-charts agent guidance if useful; installation is optional and
must not change global agent configuration. [S20, S21]

Exit: the constructor creates exactly ONE tab with ONE chart/1×1 grid; tests reject 0/21 tabs
and 0/10 charts. The GUI shell runs where prerequisites exist. Source builds/typechecks with
actual commands documented. No root file was blindly overwritten.

## Stage 1 — one real chart

Implement the Go Binance spot adapter: instrument metadata, historical 1h candles, native
live updates, health/error states and clean cancellation. Wire one independent chart through
typed Wails bindings. Add candlesticks, volume, precision formatting, crosshair readout and
basic zoom/pan. Keep domain and adapter tests GUI-independent.

Exit: a first-run one-chart desktop slice works against Binance when the endpoint is
available. Parser/reducer tests use synthetic fixtures. An access failure is reported; it
must not be replaced by pretend live prices. Listener/snapshot handover has an ordering test.

## Stage 2 — dynamic tabs, grid and essential persistence

Implement tab/chart actions, cap checks, grid presets, splitters and chart reordering. Add
SQLite storage and versioned workspace schema. Save selections/layout while editing and
restore on restart. Implement active-tab-only rendering/subscriptions and lease sharing.

Exit: start 1/1, add incrementally, stop at 20 tabs/9 charts, and restore a SMALL saved
workspace with its exact counts. Selecting 3×3 with one chart still leaves one actual chart.
New tab contains one chart. No removal of the only tab/chart. Full capacity is test-only.

## Stage 3 — robust market service and history

Implement all six intervals for Binance, history pagination, UTC normalization, bounded
caching, lifecycle states, reconnect/backoff/gap repair, source-aware candle replacement,
request generation guards and bounded frontend coalescing. Add diagnostics and simulated
network/lifecycle tests using injectable clock/transports.

Exit: no duplicate volume from native snapshots; no lost boundary bar; no old-symbol data
applied after a switch; no leaked sockets/leases after repeated tab changes. A selected
historical range stays stable while older/newer bars arrive.

## Stage 4 — Bybit and Coinbase

Implement Bybit Spot completely; explicitly set category=spot. Implement Coinbase public
instrument discovery/history and correct live intervals with the documented five-minute
native channel restriction. Implement trade dedup/aggregation, provisional overlap handling,
REST reconciliation and product-alias visibility. Do not leave TODO adapters in production.

Exit: all providers × required intervals have deterministic adapter tests. Run optional real
smoke tests where reachable; record endpoint, product, timeframe and observed outcome.
Coinbase 1m/1h/4h must not secretly show relabelled five-minute bars or double-counted volume.
One provider's failure must not break the others.

## Stage 5 — restoration, shutdown and recovery

Finish viewport/follow-live settings, timezones, window sizing/maximized state, autosave
sequence/revision acknowledgments, normal close handshake, single-instance behavior,
import/export, backups, invalid/future version handling and migration tests.

Exit: exact roundtrip tests for starter, intermediate and maximum workspaces; last committed
state survives abrupt process termination in an isolated test data directory. Corrupt state
and failed imports preserve original data. Save failure is visible and normal exit handles it.

## Stage 6 — integration, performance and Ubuntu delivery

Add browser-level interaction tests plus actual Wails desktop smoke tests. Run the entire
TEST_PLAN.md. Measure a full 180-configuration workspace while rendering only the active tab.
Fix repeated-switch resource growth. Implement scripts, launcher/icon, user documentation,
notices, release artifact checksums and version manifest. Test a production build without
any development server. Record OS/runtime and unverified target platforms.

Exit: DELIVERY_CHECKLIST.md has truthful statuses and commands. Provide working source and
release artifact(s), not just screenshots. Build failure, live-network restrictions and lack
of a GUI environment are reported separately from feature completion. Preserve clear resume
instructions if any requirement remains blocked.

## Scope control

No trading, API keys, indicators beyond volume, drawing tools, Pine, custom backend services,
cloud account, mobile version, automatic updater, or all-hidden-feeds-live mode is required.
Never postpone the core app while implementing optional features.

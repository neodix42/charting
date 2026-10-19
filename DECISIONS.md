# Decision log

| ID | Decision | Reason/status |
|---|---|---|
| D001 | Go + Wails v2 + React/TypeScript + Lightweight Charts v5 + SQLite | Chosen project stack; pin compatible versions during implementation |
| D002 | First launch 1 tab, 1 chart, 1×1 grid | Explicit user clarification; capacities are not defaults |
| D003 | Hard maxima: 20 tabs and 9 charts per tab | User-requested supported limits; enforce UI/backend/import |
| D004 | Add Tab creates one chart; grid expansion only creates placeholders | Avoid unintended pre-filled chart configurations |
| D005 | Active-tab-only rendering and live leases | Default resource policy; full 180-chart fixture is test-only |
| D006 | Public spot data only, no account keys | Focus on requested read-only charting |
| D007 | Binance BTCUSDT 1h starter with visible error when unavailable | Explicit default, never silent exchange fallback |
| D008 | Ordered autosave + SQLite + controlled close handshake | Preserve actual workspace and last acknowledged commit |
| D009 | Coinbase partial-bucket OHLCV may be provisional until reconciled | Avoid inventing a REST/trade watermark or double-counting volume |
| D010 | Import replaces after validation/confirmation/backup | No ambiguous merge semantics in v1 |
| D011 | App path chosen after inspecting existing repository | Protect unrelated files and uncommitted work |

Codex must add dated entries for exact dependency versions, SQLite driver choice, Ubuntu
runtime testing, provider schema differences, supported public chart APIs, and justified
architecture adjustments. Do not use this file to silently relax the user's explicit limits
or remove required exchanges. The default settings above are design choices, not measured
performance results or assertions of current exchange availability.

| D012 | 2026-10-19: bundle is unpacked at project root, application built at root | No unrelated app or uncommitted work existed; preserve instruction files and add APP_README.md for running the app |
| D013 | Go module/CLI Wails v2.17.0; Go 1.26.8; React 19.3.1; Lightweight Charts 5.2.1; Vite 8.3.4; Vitest 5.0.5 | Versions verified against official docs/release registry and installed public APIs; exact lockfiles retained |
| D014 | modernc.org/sqlite v1.60.1; gorilla/websocket v1.5.3; shopspring/decimal v1.4.0 | GUI-independent SQLite and exact decimal aggregation; WAL synchronous FULL, serialized commits and transaction backups |
| D015 | Local extracted Ubuntu SDK under ignored .tools/native | Runtime GTK/WebKit was installed but development headers absent. Unprivileged download/extraction enables build without changing OS packages/settings |
| D016 | Lightweight Charts public getVisibleRange/setVisibleRange/setAutoScale | Pinned v5.2.1 supports manual price range restoration without private APIs; documented since v5.0.7 |
| D017 | Multiplex provider sockets and Coinbase product trades, chart leases per feed | One provider socket can serve several independent visible charts; partial Coinbase buckets stay provisional and REST never adds buffered trade volume |
| D018 | Current official source review | Wails v2 options/events/install/build and current LWC price-scale docs reviewed; provider wire schemas reviewed against SOURCES.md. Coinbase channel URL now redirects to websocket-endpoints; concrete parser fixtures use synthetic documentation-shaped envelopes |
| D019 | 2026-10-19: actual native build and automated smoke on Ubuntu26.04.1/Wayland | Project-local SDK links installed GTK3.24.52/WebKitGTK2.52.6; no system installation performed. Wails doctor package checks alone do not recognize the local SDK; production build is the evidence |
| D020 | Provider limits/current observed wire details | Binance SPOT discovery suppresses permissionSets and caps32MiB; explicit case-distinct wire fields avoid encoding/json e/E,v/V collisions. Coinbase cursor pagination, aliases and trade-derived all-interval feeds verified against current public docs/live calls |
| D021 | Bounded moving historical windows and paginated repair |10k/frontendseries and10k/feed cache; older loads evict newer rendering bars while preserving viewport; Returnlive reloads newest. Reconnect repair paginates up to10k with explicit older-outage omission reason |
| D022 | Release verification boundary | Actual native automated20/180+200switch test and all-provider65sminute observations passed; complete manual dialogs/DPI/suspend/daily/other-release coverage remains unverified, documented in docs/verification/REPORT.md |
| D023 | Project-local Node 24.19.0/npm bootstrap for build scripts | Regular terminal PATH can lack npm even when IDE builds succeed. Prefer a usable toolchain; otherwise download the official Linux bundle, verify pinned SHA-256, and export its bin directory only for the build process. No IDE paths or OS/shell changes |
| D024 | Separate Wails bindings entry point | Wails executes the app during binding generation. The bindings build registers the App type without opening user storage, allowing builds while the real workspace is locked by a running app |
| D025 | 2026-10-19: optional per-chart indicator configuration in workspace schema 1 | Additive optional field preserves existing documents and exact legacy fixture round trips without a database migration. Strict backend/import validation checks indicator type, globally unique IDs, exact integer parameters and colors; at most 12 indicators per chart |
| D026 | Monday 00:00 UTC weekly candles | Binance uses native `1w`, Bybit native `W`. Coinbase has no weekly REST granularity, so bounded daily pages are aggregated with exact decimal OHLCV; missing coverage remains provisional. Live trades use the same Monday bucket |
| D027 | Local SMA/EMA/Ichimoku/MACD series using installed Lightweight Charts public APIs | Price overlays and a custom cloud primitive share existing candles; each MACD uses an independent linear pane. Parameter edits reuse charts and subscriptions. EMA uses a full SMA seed; long periods load bounded older history with explicit insufficient-history feedback |
| D028 | 2026-10-19: centered controls apply one atomic edit to the active tab's actual charts | Periods reset time views to live while retaining independent chart settings. One indicator dialog creates independent IDs/parameters on every chart, preserving existing indicators and hidden tabs. If any chart is full, the entire indicator addition is refused; no partial application or new workspace schema is needed |

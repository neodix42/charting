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

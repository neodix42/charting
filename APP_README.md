# CryptoDesk

CryptoDesk is an Ubuntu desktop spot-market chart workspace built with Go, Wails v2, React/TypeScript, Lightweight Charts v5, and SQLite. It fetches public Binance, Bybit and Coinbase data directly. No API keys, account or development server are required to run the release.

**First launch has exactly one tab and one chart in a 1×1 grid.** Add tabs and charts as needed, up to 20 tabs and 9 charts per tab. Selecting a larger grid adds empty controls, not chart configurations. New tabs start with one chart. Later launches restore your actual saved workspace.

The original specification bundle is retained at the repository root. [STATUS.md](STATUS.md) and [docs/verification](docs/verification) describe observed verification and remaining limits.

## Run the built release

Extract `release/cryptodesk-0.1.0-linux-amd64.tar.gz`, then run `./cryptodesk`. It requires compatible GTK3, WebKitGTK 4.1, libsoup3 and Linux desktop runtime libraries. The archive does not bundle a browser or claim cross-distribution portability. Tested build environment: Ubuntu 26.04.1 x86_64, Wayland, GTK 3.24.52, WebKitGTK 2.52.6.

Optional user-local installation:

```bash
./install-user.sh --dry-run
./install-user.sh
```

This writes the binary under `$XDG_DATA_HOME/cryptodesk-app` (normally `~/.local/share/cryptodesk-app`), a launcher and an SVG icon. It does not start the application or replace workspace data. Source-checkout equivalent: `scripts/install-user.sh --dry-run` after building.

## Build and develop

Pinned versions: Go module/CLI Wails 2.17.0; React 19.3.1; Lightweight Charts 5.2.1; SQLite Go driver modernc.org/sqlite 1.60.1. This checkout was built with Go 1.26.8, Node 24.19.0 and npm 11.17.0. Frontend and Go lockfiles are included.

Install Go, Node LTS, a C compiler and pkg-config. Linux native builds also require GTK3 and WebKitGTK 4.1 development packages; the standard Ubuntu package names are `libgtk-3-dev` and `libwebkit2gtk-4.1-dev`. Installing OS packages is a separate user/admin action.

On an Ubuntu machine which already has the corresponding runtime libraries, `scripts/bootstrap-native-sdk.sh` is an optional unprivileged alternative: it downloads exact currently resolved Ubuntu development-package archives and extracts them under ignored `.tools/native`; it does not install packages or change the OS. `scripts/native-env.sh` scopes those headers/linker paths to the invoking process. This SDK path was used in the test environment. Repositories/network access are needed for bootstrap; package versions are recorded in `.tools/native/package-versions.txt`.

```bash
scripts/build-linux.sh       # pinned local CLI, production frontend + desktop + archive
scripts/dev.sh               # desktop development
scripts/test.sh              # deterministic Go race/vet + frontend checks/build
cd frontend && npm run test:e2e
```

`scripts/build-linux.sh` emits `build/bin/cryptodesk`, a release archive, checksums, licenses and a version manifest. Build scripts never use sudo or a system package installation. The CLI is installed into project-local `.tools/` if absent. Normal builds contain local assets and no fixture bridge.

Optional live and native verification:

```bash
go run ./cmd/smoke -duration 65s
scripts/smoke-desktop.sh
```

Live smoke makes public requests and reports exact outcomes. Native smoke uses an isolated temporary workspace and an explicitly tagged automation binary, real public feeds and the actual Wails/WebKit webview. Browser E2E uses a separately labelled explicit test bridge; it is not a live-exchange or desktop test.

## Use

Use Add Tab or Ctrl+T to create a single-chart tab; Add Chart or Ctrl+Shift+N adds one chart. Ctrl+Tab / Ctrl+Shift+Tab changes tab; Ctrl+W closes with confirmation. Shortcuts stay inactive in editors/selectors. Tabs scroll, and the All tabs menu reaches all 20. Rename, duplicate or drag tabs; drag chart headers to reorder. Row/column splitters resize tracks. Maximize chart temporarily and restore it without changing the saved grid.

Each chart has its own exchange, searchable native instrument, interval, volume, candlestick/line style and linear/log scale. Drag/pan/zoom the chart; scrolling left loads older history. Return to live follows current bars. Manual price scaling and time viewports are saved. Settings provides theme, IANA/system timezone, JSON import/export/reset and diagnostics. Candle boundaries remain UTC regardless of display timezone.

Only the active tab mounts charts and requests leases. Identical feeds share data and provider sockets; hidden tab leases release after a cancellable two-second grace. Cached data and chart settings remain. A larger grid never creates hidden subscriptions.

Provider failures remain visible in their chart. Retry or reselect without losing configuration. Prices are never synthesized as a fallback. Coinbase live intervals derive from deduplicated trades, rather than relabelling its five-minute candle channel. An initial/reconnected partial bucket has provisional volume: the last REST snapshot or explicitly observed-only trades, never REST plus all buffered trade volume. Public Coinbase aliased USDC products are labelled/rejected; choose the actual USD product explicitly.

## Save, export and recover

Workspace data lives at `$XDG_DATA_HOME/cryptodesk/workspace.sqlite`, normally `~/.local/share/cryptodesk/workspace.sqlite`, with user-only permissions. A local lock prevents another process from editing the same store. For an isolated profile use `./cryptodesk --data-dir /absolute/test-directory`.

Autosave uses 500 ms idle debounce with at most two seconds of deferral, ordered edit sequences and acknowledged SQLite revisions. Tab switches flush; native close captures viewports and awaits the final commit. A save failure keeps the app open with retry/discard options. A forced kill can lose pending frontend edits; the last acknowledged commit survives.

Import/export uses native JSON file dialogs. Imports are limited to 5 MiB, strictly validated, previewed, confirmed and transactionally replace the workspace with a consistent backup. Exports contain configuration only. At most five recovery snapshots are kept in the workspace database. Reset is explicit and confirmed. Invalid/future saved documents are preserved and open a recovery screen, with backup/import/reset choices. A physically unreadable database fails startup with an error and preserves the file.

For a portable backup, export JSON. To copy the database manually, close the app first and retain any `-wal`/`-shm` companion files; do not copy only a live main database file. Move a corrupt database and its companion files aside before an explicit fresh profile; never delete them as an automatic recovery shortcut.

## Boundaries and attribution

Caches, history pages, queues and rendered instances are bounded. Active series retain at most 10,000 bars in a moving window: older-history loads evict newer bars while preserving the selected historical range, and Return to live reloads the latest window. Full manual desktop, high-DPI and target-release coverage is recorded separately in STATUS.md; automated browser tests do not establish those checks. Exact window position on Wayland is best effort; normal size and maximized state are saved.

Charts by [TradingView](https://www.tradingview.com/), using Apache-2.0 Lightweight Charts. Its creator attribution remains visible. [Third-party notices](docs/THIRD_PARTY_NOTICES.md) and copied licenses ship with the archive. There is no trade execution, wallet, credential storage, hosted TradingView client, paid feature gate, telemetry or automatic updater.

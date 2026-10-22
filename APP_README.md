# CryptoDesk

CryptoDesk is an Ubuntu and Windows desktop chart workspace for spot and perpetual markets built with Go, Wails v2, React/TypeScript, Lightweight Charts v5, and SQLite. It fetches public Binance, Bybit and Coinbase data directly. No API keys, account or development server are required to run the release.

Open the symbol dropdown to browse both **Spot** and **Perpetual** instruments on Binance and Bybit. Each row shows its market type on the right; identical names such as BTCUSDT remain separate choices. The chart keeps the selected market on save/restart and uses its own historical/live feed and candle cache. Binance USD-M/COIN-M and Bybit linear/inverse perpetuals are supported, including XAGUSDT silver contracts. Dated futures and options are excluded. Coinbase currently supplies spot instruments. Search by symbol, base/quote asset or market label. If a provider category is restricted, available symbols remain usable alongside an explicit warning and Retry discovery.

**First launch has exactly one tab and one chart in a 1×1 grid.** Add tabs and charts as needed, up to 20 tabs and 9 charts per tab. Selecting a larger grid adds empty controls, not chart configurations. New tabs start with one chart. Later launches restore your actual saved workspace.

The original specification bundle is retained at the repository root. [STATUS.md](STATUS.md) and [docs/verification](docs/verification) describe observed verification and remaining limits.

## Run the built release

Run the root `./cryptodesk` produced by `./build.sh`, or extract `cryptodesk-X.Y.Z-linux-amd64.tar.gz` and run `./cryptodesk` inside its directory. This small archive requires compatible GTK3, WebKitGTK 4.1, libsoup3 and Linux desktop runtime libraries. Tested local build environment: Ubuntu 26.04.1 x86_64, Wayland, GTK 3.24.52, WebKitGTK 2.52.6.

For a machine without GTK/WebKit, choose **`cryptodesk-X.Y.Z-linux-amd64-portable.tar.gz`**, extract the whole directory and run its **`./cryptodesk` launcher**. It includes GTK/WebKit, their helper processes and runtime dependencies, licenses and matching source archives. Keep its `runtime` directory beside the launcher. No installation or FUSE is needed. The host still supplies compatible glibc, graphics drivers, desktop session and fonts; CI builds use Ubuntu 24.04, while local packages require the glibc version of their build host or newer. Workspace storage remains in the normal user profile unless `--data-dir` is supplied. [Portable Linux details](docs/LINUX_PORTABLE.md).

Optional user-local installation for **either Linux archive**:

```bash
./install-user.sh --dry-run
./install-user.sh
```

This writes the app under `$XDG_DATA_HOME/cryptodesk-app` (normally `~/.local/share/cryptodesk-app`), registers its application launcher and installs the same diamond logo shown in the app's top-left corner. The launcher uses an absolute PNG icon path, with SVG and eight PNG sizes also registered in the user icon theme. The native window embeds a smaller copy that GTK accepts on X11; the matching `cryptodesk` desktop identity provides Wayland taskbar association. Windows builds carry the same logo in their executable resources.

If your configured desktop folder exists, installation also creates an executable `cryptodesk.desktop` shortcut there, preserving any differing existing shortcut. It sets that file's trusted metadata when supported; your desktop may still require **Allow Launching**. Per-user icon/application metadata is refreshed when the desktop tools are available. No system settings or workspace data are changed. The bundled-runtime installer copies the complete runtime, including licenses and sources; the original extraction folder can then be removed.

Run the installer again after rebuilding or downloading an updated archive, then close and reopen CryptoDesk. If an existing pinned taskbar entry retains an old icon, unpin it and pin the newly installed **CryptoDesk** application entry. Installation does not start the app. Source-checkout equivalent: `scripts/install-user.sh --dry-run` after building.

## Downloads and release versions

The main [README](https://github.com/neodix42/charting#download) links to the [latest release](https://github.com/neodix42/charting/releases/latest). Pushing commits to `main` runs the release workflow: check and build both Linux variants and Windows, assign `v0.1.0` initially and increment subsequent patch tags, then publish all three versioned packages together. Unversioned duplicate archives are no longer published. `SHA256SUMS` verifies the archives and `release.json` records their version, source commit and hashes. Historical completed releases are preserved. The workflow assumes `main` is the default branch. Superseded queued commits or older workflow contents are skipped in favor of the newer push; a workflow change during a build stops publication before remote writes.

The header displays the installed build's `vX.Y.Z` release tag beside the CryptoDesk name. It is embedded at build time and works offline; it does not change when a newer release is published. Packaging verifies that the UI version matches the selected release, and diagnostics reports the same compiled version.

Local builds use the baseline in `VERSION` (currently `0.1.0`). Set `CRYPTODESK_VERSION=X.Y.Z` to override it for either build script; the header, output filename, package manifest and Windows product version follow this value. Release CI passes its selected version without making source-version bump commits. Hosted execution and Windows GUI checks are recorded separately in [STATUS.md](STATUS.md).

Ubuntu release CI checks unprivileged Bubblewrap namespace setup before building. Only a restricted GitHub-hosted Ubuntu 24.04 runner can load the pinned upstream Bubblewrap AppArmor profile when the initial probe fails; existing policy and global namespace settings are preserved. Portable startup failures print their exit code/log tail and retain `namespace.json`, `portable.json` and application logs in the **diagnostics-portable-linux-amd64** workflow artifact. Diagnostics are kept separate from published application archives. Local build scripts do not load AppArmor policy.

## Build and develop

Pinned versions: Go module/CLI Wails 2.17.0; React 19.3.1; Lightweight Charts 5.2.1; SQLite Go driver modernc.org/sqlite 1.60.1. This checkout was built with Go 1.26.8, Node 24.19.0 and npm 11.17.0. Frontend and Go lockfiles are included.

Install Go, Python 3.12+, a C compiler and pkg-config. Build/dev/test scripts check that both Node and npm run. If either is missing or incompatible, they download the official Node 24.19.0 bundle (including npm), verify its pinned SHA-256, and reuse it under `.tools/` on later runs. The selected `bin` directory is exported to Wails and its child processes. No shell-profile edits, global npm installation or IDE runtime paths are needed. Initial download requires access to `nodejs.org`; alternatively provide Node 24 LTS with npm on your terminal's PATH.

Linux native builds also require GTK3 and WebKitGTK 4.1 development packages; the standard Ubuntu package names are `libgtk-3-dev` and `libwebkit2gtk-4.1-dev`. Installing OS packages is a separate user/admin action.

On an Ubuntu machine which already has the corresponding runtime libraries, `scripts/bootstrap-native-sdk.sh` is an optional unprivileged alternative: it downloads exact currently resolved Ubuntu development-package archives and extracts them under ignored `.tools/native`; it does not install packages or change the OS. `scripts/native-env.sh` scopes those headers/linker paths to the invoking process. This SDK path was used in the test environment. Repositories/network access are needed for bootstrap; package versions are recorded in `.tools/native/package-versions.txt`.

```bash
./build.sh                   # ready-to-run app + portable archive + checksums in repo root
./portable-build.sh          # larger Linux archive with GTK/WebKit runtime, licenses and sources
./win-build.sh               # cross-build Windows x64 ZIP with English WebView2 locale packs
scripts/build-linux.sh       # pinned local CLI, production frontend + desktop + archive
scripts/dev.sh               # desktop development
scripts/test.sh              # deterministic Go race/vet + frontend checks/build
cd frontend && npm run test:e2e
```

`./build.sh` works from any current directory and publishes `cryptodesk`, `cryptodesk-X.Y.Z-linux-amd64.tar.gz`, and `cryptodesk-SHA256SUMS` at the repository root. Launch `./cryptodesk`, or copy the archive to a compatible Linux desktop, extract it and launch the included executable. Verify root outputs with `sha256sum -c cryptodesk-SHA256SUMS`.

Binding generation runs without opening a workspace database, so building while CryptoDesk is open does not conflict with its workspace lock.

`./portable-build.sh` first builds the normal Linux executable, then creates `cryptodesk-X.Y.Z-linux-amd64-portable.tar.gz` and `cryptodesk-Portable-SHA256SUMS` at the root. Use `--skip-build` only immediately after a matching `./build.sh`. This Ubuntu-only bundler copies installed runtime files and downloads their exact source versions through authenticated Ubuntu source indexes into a project-local cache. It does not modify APT configuration or install packages. Build on the oldest Ubuntu release you intend to support; the package records its build OS/glibc, host-library requirements and complete runtime file hashes.

For Windows, run `./win-build.sh` on Ubuntu with Go, Python 3.12+ and `7z` (Ubuntu package `7zip`). It produces `cryptodesk-X.Y.Z-windows-amd64.zip` and `cryptodesk-Windows-SHA256SUMS` in the root. The ZIP includes Microsoft Fixed Version WebView2 with English (`en-US` and `en-GB`) locale packs; other locale PAKs are omitted. The complete source runtime is verified against its pinned version, SHA-256 and inventory before reducing only the staged package. Shared resources and all licenses are retained. Build sources are copied to a temporary directory, preserving existing generated bindings, uncommitted files and Linux outputs. The script uses project-local Node/npm/Wails and does not install system packages. Fresh machines download the pinned Windows-only Go WebView2 module before preparing the English-language overlay; a prefilled Go module cache is not required, and normal Go checksum/source verification remains enabled. Initial runtime download is about 300 MB; the complete verified runtime remains cached under `.tools/webview2`. Use `./win-build.sh --webview2-cab /path/to/pinned.cab` for a local copy of the exact pinned runtime.

On Windows, extract the **whole ZIP** to a local writable folder and launch `cryptodesk.exe`. Keep `WebView2` and `portable.marker` beside it; the workspace and browser profile live in its `data` directory. This needs no separate WebView2 installation. Windows 10 startup applies Microsoft's required read/execute grants to the bundled runtime directory; Windows 11 needs no grants. Fixed runtime updates require a new reviewed runtime pin and rebuild. The executable is unsigned, and cross-compilation on Ubuntu does not replace Windows runtime testing. Full details: [Windows portable build and usage](docs/WINDOWS_PORTABLE.md).

The underlying `scripts/build-linux.sh` also retains `build/bin/cryptodesk` and the distribution under `release/`, including checksums, licenses and a version manifest. Build scripts never use sudo or a system package installation. The CLI is installed into project-local `.tools/` if absent. Normal builds contain local assets and no fixture bridge.

Optional live and native verification:

```bash
go run ./cmd/smoke -duration 65s
scripts/smoke-desktop.sh
```

Live smoke makes public requests and reports exact outcomes. Native smoke uses an isolated temporary workspace and an explicitly tagged automation binary, real public feeds and the actual Wails/WebKit webview. Browser E2E uses a separately labelled explicit test bridge; it is not a live-exchange or desktop test.

## Use

Use the **＋** immediately after the tabs, or Ctrl+T, to create a single-chart tab; Add Chart or Ctrl+Shift+N adds one chart. The tabs and **＋** share one horizontally scrolling strip, so scroll along the row to reach later tabs. Ctrl+Tab / Ctrl+Shift+Tab changes tab; Ctrl+W closes the active tab with confirmation. **Double-click a tab to rename it. Middle-click any tab** to close that tab and all its charts without a confirmation dialog; an inactive target stays inactive. The final tab cannot be closed. Each chart has a larger **×** button that removes the chart immediately without confirmation; the final chart in a tab stays protected. Shortcuts stay inactive in editors/selectors. Duplicate a tab from the header or drag tabs to reorder; drag chart headers to reorder charts. Row/column splitters resize tracks. Maximize chart temporarily and restore it without changing the saved grid.

Each chart has its own exchange, searchable native instrument, horizontal period buttons (`1m`, `5m`, `15m`, `1h`, `4h`, `1d`, `1w`), volume, candlestick/line style and linear/log scale. The buttons stay in the chart header row; small chart headers scroll horizontally. Drag/pan/zoom the chart; scrolling left loads older history. Return to live follows current bars. Manual price scaling and time viewports are saved. Settings provides theme, IANA/system timezone, optional local candle storage, JSON import/export/reset and diagnostics. Candle boundaries remain UTC regardless of display timezone.

**Settings → Chart right gap** sets the space between the latest candle and the chart’s right edge for every tab. Choose **Bars** (default **10**) or **Pixels**, enter a whole number, and press **Apply**. All current charts update immediately, including maximized siblings; inactive tabs use the same setting when opened. Changing the gap returns all charts to live while retaining their zoom and price settings. The preference survives restart and JSON export/import. Accepted values are 0–500 bars or 0–5,000 CSS pixels; a gap that exceeds the available chart width is reduced to keep the latest candle visible. Ichimoku’s projected cloud does not move the gap’s anchor away from the latest price candle.

Centered in the top header alongside the CryptoDesk logo, shared period buttons and an **Indicators** dropdown apply to every existing chart in the current tab, including charts hidden by maximization. Grid, Add chart and Duplicate controls sit to their right, immediately before the save indicator. Its width stays fixed while changes are pending, saving, saved or awaiting retry, keeping neighboring controls in place. Narrow windows use compact action icons with tooltips. Tab names remain in the tab row. A period click updates all those charts immediately and returns them to live view. The shared period is highlighted only when every chart agrees; individual chart controls can still override it. For an indicator, configure its parameters once and press **OK** to add an independent instance to every chart in the tab. Existing indicators are retained, other tabs stay unchanged, and all changes are saved. If any chart already has 12 indicators, the shared dropdown is disabled until an indicator is removed from that chart.

Weekly candles start Monday at 00:00 UTC. Binance and Bybit provide native weekly candles. Coinbase weekly history is derived from actual daily candles, with live updates from its public trades; incomplete weekly coverage remains provisional.

### Drawing tools

Every chart has a **Draw** toolbar: **Arrow up**, **Arrow down**, **Horizontal line** and **Trendline**. Choose a tool, then click the price chart to place it. A trendline uses two clicks, one for each end, with a preview between them. Press **Esc** or choose **Cursor** to cancel an unfinished drawing and resume panning. **Undo drawing** removes the last drawing on the current instrument; **Clear drawings** removes all drawings on that instrument.

**Single-click a drawing to select it.** Select an arrow by its tip or shaft, a horizontal line anywhere along it, or a trendline along its segment. Click **Delete** in that chart's toolbar or press **Delete / Del** to remove the selected item. Keyboard deletion is suspended while typing or using a dialog. Clicking empty chart space or pressing **Esc** clears the selection.

Selected lines show square handles. **Drag either endpoint and release** to change its position or length. A full-width horizontal line becomes a finite horizontal segment when an endpoint is dragged; it stays horizontal as you adjust either end. **Esc** or **Cursor** cancels an unfinished adjustment. Changes are saved after release.

To reposition a whole item, **double-click the drawing**, move the pointer, then **click to place it**. Trendlines and finite horizontal segments keep their shape and the point where you grabbed them. **Esc**, **Cursor** or a chart header control cancels the move and keeps the previous position. Clicking **Save** during a move or endpoint adjustment saves the previous completed position and keeps the preview active. Completed edits retain the drawing's identity, and both edits and deletions restore after restart.

Completed drawings autosave to SQLite and restore after restart, tab switches and JSON export/import. Positions use UTC times and prices, so zooming, resizing and changing timeframes preserve their anchors. Drawings belong to their chart, exchange, market and instrument: switching symbols hides unrelated drawings and switching back restores them. Add Chart starts without drawings; Duplicate Tab copies them independently. Each chart supports up to 100 saved drawings across its instruments.

### Indicators

Each chart has an **Indicators** dropdown. Select SMA, EMA, Ichimoku Cloud or MACD, adjust its parameters, then press **OK** to add it. SMA and EMA also offer **Line width** from **1–4 px**, default **2 px**, in both individual and shared dialogs. Cancel leaves the chart unchanged. Click an applied indicator's label to edit it, or its × to remove it. Multiple instances can use different periods, colors and line widths; settings are independent per chart and survive restart and JSON export/import. There are at most 12 indicators per chart to bound rendering work. Existing SMA/EMA instances without a width retain their two-pixel appearance.

| Indicator | Default parameters | Display |
|---|---|---|
| SMA | Close-price period 20, line width 2 px | Price overlay |
| EMA | Close-price period 20, line width 2 px | Price overlay |
| Ichimoku Cloud | Conversion 9, base 26, leading span B 52, displacement 26 | Conversion/base/leading/lagging lines and green/red cloud between the leading spans |
| MACD | Fast 12, slow 26, signal 9 | Separate pane with MACD/signal lines, histogram and zero baseline |

Drag the separator above a MACD pane to resize it. Each MACD instance saves its own height relative to the main price pane, restoring the same proportions after tab switches, parameter edits, restart and JSON export/import. The proportions adapt when the chart or window is resized.

Periods count chart candles, from 1 to 2,000; Ichimoku displacement is 0–500 bars. MACD fast must be smaller than slow. Color controls the indicator's primary line. Long periods request older candles automatically, with a visible warm-up/history status when available data is insufficient. Calculations use the loaded candle window, including the current provisional candle, and update after candle corrections. EMA seeds from a full SMA window before applying `2 / (period + 1)`; MACD's signal uses the same EMA convention. Ichimoku displaces leading spans forward and the close backward by the configured number of bars, extrapolating future timestamps only for the projected cloud. The cloud contains derived values, never invented future prices. Earlier history can change an EMA's seed, so results can differ slightly from chart services with a different seed/history window.

Existing workspaces without an `indicators` field continue to load unchanged. Indicator calculations are local; no additional account, data feed or subscription is required. Indicator changes reuse the existing chart and feed.

Only the active tab mounts charts and requests leases. Identical feeds share data and provider sockets; hidden tab leases release after a cancellable two-second grace. Cached data and chart settings remain. A larger grid never creates hidden subscriptions.

Live status and public-data details stay in the chart’s bottom-left footer, including when the cursor is over blank space or a future cloud span. Connection progress and feed failures retain their visible messages and retry controls.

Provider failures remain visible in their chart. Retry or reselect without losing configuration. Prices are never synthesized as a fallback. Coinbase live intervals derive from deduplicated trades, rather than relabelling its five-minute candle channel. An initial/reconnected partial bucket has provisional volume: the last REST snapshot or explicitly observed-only trades, never REST plus all buffered trade volume. Public Coinbase aliased USDC products are labelled/rejected; choose the actual USD product explicitly.

## Local candle storage

**Store candle data locally** in Settings is **off by default**. Check it, edit the **Storage folder** path or use **Choose folder…**, then click **Apply**. Checkbox and folder edits take effect together only when Apply succeeds. Clearing the path and applying selects the default folder. Failed configuration changes remain visible and preserve the previously applied setting; Apply can retry the same configuration after a storage error.

The default folder is `<profile>/candles`: normally `~/.local/share/cryptodesk/candles` on Linux, or `$XDG_DATA_HOME/cryptodesk/candles` when that variable is set. `--data-dir` selects the profile and therefore its default candle folder. The Windows portable profile uses `data/candles` beside the executable. The optional database is `candles.sqlite3` inside the selected folder. The enabled flag and chosen path are saved separately in `<profile>/candle-storage.json`; workspace JSON import, export and Reset Workspace do not change them or include cache contents.

Settings shows the applied folder and its total size. **Refresh** measures it again; while storage is enabled and its settings are visible, size updates approximately every five seconds. The total includes SQLite sidecars and other regular files in the selected folder; symlinks are not followed. Folder edits do not replace the applied path or its size until Apply succeeds.

Enabling storage saves genuine downloaded candles currently held in memory, then stores accepted live updates and history pages by exchange, instrument and period. On restart, saved candles can appear immediately with a **cached** status while real public data reconnects. Complete, contiguous pages of closed historical candles can be reused from disk. Current/open candles, missing coverage and gaps still require provider data; unavailable feeds remain visibly unavailable rather than being presented as live cached prices.

Disabling storage stops disk caching and leaves existing files intact. Changing folders opens the selected cache for subsequent storage without moving or deleting the previous folder; select the old folder again to reuse its data. Disk storage can grow with downloaded history and has no automatic eviction or deletion. Memory caches and active chart windows remain bounded.

## Save, export and recover

Workspace data lives at `$XDG_DATA_HOME/cryptodesk/workspace.sqlite`, normally `~/.local/share/cryptodesk/workspace.sqlite`, with user-only permissions. A local lock prevents another process from editing the same store. For an isolated profile use `./cryptodesk --data-dir /absolute/test-directory`.

Autosave uses 500 ms idle debounce with at most two seconds of deferral, ordered edit sequences and acknowledged SQLite revisions. Tab switches flush; native close captures viewports and awaits the final commit. When launched from a terminal, **Ctrl+C** follows that same save-and-close flow; **SIGTERM** also requests a graceful close. Interrupts received during startup are queued until the workspace can acknowledge its final save. A save failure keeps the app open with retry/discard options; repeated interrupts do not discard unsaved changes. A forced kill can lose pending frontend edits; the last acknowledged commit survives.

Import/export uses native JSON file dialogs. Imports are limited to 5 MiB, strictly validated, previewed, confirmed and transactionally replace the workspace with a consistent backup. Exports contain configuration only. At most five recovery snapshots are kept in the workspace database. Reset is explicit and confirmed. Invalid/future saved documents are preserved and open a recovery screen, with backup/import/reset choices. A physically unreadable database fails startup with an error and preserves the file.

For a portable backup, export JSON. To copy the database manually, close the app first and retain any `-wal`/`-shm` companion files; do not copy only a live main database file. Move a corrupt database and its companion files aside before an explicit fresh profile; never delete them as an automatic recovery shortcut.

## Boundaries and attribution

Memory caches, history request sizes, queues and rendered instances are bounded. The optional disk candle cache is persistent and can grow; it has no automatic deletion. Active series retain at most 10,000 bars in a moving window: older-history loads evict newer bars while preserving the selected historical range, and Return to live reloads the latest window. Full manual desktop, high-DPI and target-release coverage is recorded separately in STATUS.md; automated browser tests do not establish those checks. Exact window position on Wayland is best effort; normal size and maximized state are saved.

Charts by [TradingView](https://www.tradingview.com/), using Apache-2.0 Lightweight Charts. Its creator attribution remains visible. [Third-party notices](docs/THIRD_PARTY_NOTICES.md) and copied licenses ship with the archive. There is no trade execution, wallet, credential storage, hosted TradingView client, paid feature gate, telemetry or automatic updater.

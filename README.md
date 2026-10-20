# CryptoDesk

<img src="frontend/src/assets/cryptodesk.svg" width="64" height="64" alt="CryptoDesk diamond logo">

A desktop workspace for public cryptocurrency spot charts on Ubuntu and Windows. CryptoDesk uses Go, Wails, React, Lightweight Charts and SQLite. Market data comes directly from Binance, Bybit and Coinbase; no account or API keys are needed.

**First launch opens exactly one tab with one chart.** Add up to **20 tabs** and **9 charts per tab**. These are capacities, not a preloaded layout. Subsequent launches restore your actual saved workspace.

## Download

| Platform | Latest portable package | Run |
|---|---|---|
| Ubuntu x64 | [Download Linux archive](https://github.com/neodix42/charting/releases/latest/download/cryptodesk-linux-amd64.tar.gz) | Extract, then run `./cryptodesk` |
| Windows x64 | [Download Windows ZIP](https://github.com/neodix42/charting/releases/latest/download/cryptodesk-windows-amd64.zip) | Extract the whole ZIP, then run `cryptodesk.exe` |

[All releases and version history](https://github.com/neodix42/charting/releases) · [Latest SHA-256 checksums](https://github.com/neodix42/charting/releases/latest/download/SHA256SUMS)

The download links become available after the first successful release workflow on `main`. Each release includes versioned archives, the stable filenames linked above, and checksums.

The Linux archive requires GTK3, WebKitGTK 4.1 and libsoup3 runtime libraries. CI builds target Ubuntu 24.04 x64; local native verification is recorded for Ubuntu 26.04.1/Wayland in [STATUS.md](STATUS.md). The archive includes a user-local desktop/icon installer and does not bundle a Linux browser runtime. See [Linux usage and build instructions](APP_README.md).

The larger Windows ZIP includes its pinned Fixed Version WebView2 runtime with English locale packs, so a separate browser installation is unnecessary. Extract into a writable local folder, keeping `WebView2` and `portable.marker` beside the executable. Workspace and browser data remain in the portable `data` folder. The executable is unsigned; native Windows GUI verification remains pending. See [Windows portable details](docs/WINDOWS_PORTABLE.md).

## Features

- Independent exchange, instrument and period per chart: **1m, 5m, 15m, 1h, 4h, 1d and 1w**. Search current spot instruments, pan/zoom, load older history, and return to live data. Connection failures and provisional candles remain visible.
- **SMA, EMA, Ichimoku Cloud and MACD**, with parameter dialogs, multiple instances, editable colors and saved settings. MACD uses separate resizable panes and remembers their proportions.
- Shared period buttons and an Indicators menu in the top header apply to every chart in the current tab. Individual chart controls remain independent.
- Configurable grids with empty slots, draggable tabs/charts, resizable tracks, and chart maximization. Rename or duplicate a tab; **middle-click a tab** to close it and all its charts without confirmation. Each chart’s enlarged **×** button also closes it without confirmation. The final tab and final chart in each tab stay protected.
- Theme and display timezone settings, volume, candlestick/line rendering, linear/log price scales, and saved viewports. Set the global right-side chart gap in **bars or pixels**, defaulting to **10 bars**.
- Automatic SQLite workspace saving and restoration, JSON import/export, recovery snapshots and a final-save handshake on window close or terminal **Ctrl+C**. Hidden tabs retain configuration without keeping all their charts rendered or subscribed.

This is a read-only charting application. It does not place trades or connect to exchange accounts. Weekly candles start Monday at 00:00 UTC. Coinbase weekly history is aggregated from actual daily candles; incomplete coverage is marked provisional.

Useful shortcuts: **Ctrl+T** adds a tab, **Ctrl+Shift+N** adds a chart, **Ctrl+Tab / Ctrl+Shift+Tab** switches tabs, and **Ctrl+W** confirms closing the active tab.

## Build from source

Requires Go, Python 3.12+, and a C compiler/pkg-config for Linux. Linux builds need the GTK3 and WebKitGTK 4.1 development packages; Windows packaging on Ubuntu needs `7z`. Scripts use pinned project-local Wails and bootstrap a verified local Node/npm bundle if a usable toolchain is absent. They do not install OS packages or change your shell configuration.

```bash
./build.sh                 # Linux executable, portable archive and checksums in the root
./win-build.sh             # Windows x64 portable ZIP, including English WebView2
```

`VERSION` provides the source baseline. Release builds pass `CRYPTODESK_VERSION=X.Y.Z` so both packages and their manifests carry the automatic release version. Full prerequisites, installation, storage and recovery instructions are in [APP_README.md](APP_README.md).

## Automatic releases

The [release workflow](.github/workflows/release.yml) runs when commits are pushed to `main`. It runs checks, builds both x64 packages, and publishes a GitHub release only after both builds succeed. The first release uses `v0.1.0`; later releases increment the highest release tag's patch version (`v0.1.1`, `v0.1.2`, …). Builds do not create source-version bump commits.

The workflow assumes `main` is the repository's default branch. Release jobs are serialized, and reruns recognize an already published commit. Superseded commits and queued commits whose workflow files have since changed on `main` are skipped; the newer push includes those changes. If workflow files change during a build, publication stops before creating a tag or release. GitHub-hosted execution and the first published download require committing and pushing this workflow; local verification results and remaining platform checks are documented in [STATUS.md](STATUS.md).

## Development and verification

```bash
scripts/test.sh                         # Python, Go race/vet, frontend checks and build
cd frontend && npm run test:e2e          # Browser tests with an explicit fixture bridge
# From the repository root:
scripts/smoke-desktop.sh                # Actual Wails/WebKit app, isolated test workspace
go run ./cmd/smoke -duration 65s          # Separate public-exchange live checks
```

Normal builds contain no fixture bridge. Browser fixture tests, native desktop checks and live exchange checks are separate; see [verification evidence](docs/verification), [desktop smoke instructions](docs/DESKTOP_SMOKE.md) and [STATUS.md](STATUS.md) for what has actually passed.

The original implementation specifications and fixtures remain in this repository: [START_HERE.md](START_HERE.md), [SPEC.md](SPEC.md), [IMPLEMENTATION_PLAN.md](IMPLEMENTATION_PLAN.md) and [TEST_PLAN.md](TEST_PLAN.md). Design changes are recorded in [DECISIONS.md](DECISIONS.md).

## License and attribution

CryptoDesk is licensed under [Apache 2.0](LICENSE). Charts use [TradingView Lightweight Charts](https://www.tradingview.com/lightweight-charts/). Dependency notices and redistributed licenses are included in [docs/THIRD_PARTY_NOTICES.md](docs/THIRD_PARTY_NOTICES.md) and the portable packages. Windows packages also include Microsoft's WebView2 redistribution terms.

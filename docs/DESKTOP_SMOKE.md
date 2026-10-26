# Native desktop verification

`scripts/smoke-screener-enrichment.sh` adds an isolated production Wails/WebKit check for Enrichment. It downloads real BTCUSD/ETHUSD daily data, drives SMA and Tenkan parameters through the native UI, verifies numeric row alignment, preserved fields and previous indicators, checks exact original backups, then restarts to discover unchanged enriched CSVs. Evidence defaults to `docs/verification/screener-enrichment/desktop/`. Windows execution remains a separate native check.

Run `scripts/smoke-desktop.sh` in a logged-in Ubuntu graphical session after building prerequisites. It compiles the real local production bundle with an additional `desktop_smoke` build tag. The tag contributes UI-driving/reporting methods absent from the normal release. No Vite server, injected fixture prices, separate Go daemon or live user workspace is used.

The script creates an isolated temporary SQLite profile and runs three actual Wails/WebKit application launches:

1. Fresh: asserts exactly one tab/one chart/1×1, a 3×3 grid with eight placeholders, one-at-a-time chart additions, a new single-chart tab and committed 3+1 configuration.
2. Restore: restarts that exact profile, verifies saved settings/order/active layout and saves a final viewport/selection edit through native close without an explicit prior flush.
3. Capacity: explicitly validates/imports the supplied maximum fixture in test-tagged Go code, checks 20/180 configuration with only nine rendered charts, then switches real tabs 200 times and records leases, queues, sockets, timing, Go heap, process-tree RSS and CPU. It checks disabled cap controls and post-grace resource cleanup.

Reports/logs are written to `docs/verification/desktop-<UTC>/`. This is automated native smoke, separate from Chromium E2E, provider live observations and the complete manual matrix below.

Remaining manual checks (record outcomes, never infer a pass from automation):

- Native import/export dialogs, overwrite cancellation and creator link opening in the user's browser.
- Keyboard focus, copy/paste, dragging chart headers and splitters at normal and high DPI.
- Actual maximize/unmaximize, multi-monitor clamping and close/restart at different window sizes.
- Panning to an old date, manual price scale restoration and Return to live in WebKit.
- Real network loss/recovery, OS suspend/resume and quiet instruments.
- Mixed providers on each interval in the native UI, alongside already separate live adapter tests.
- Additional Ubuntu releases and the complete X11 manual matrix. Earlier full desktop runs used Ubuntu 26.04.1 and Wayland; the theme smoke below additionally exercises X11/XWayland.

## Terminal signal verification

Run `scripts/smoke-signals.sh --evidence /absolute/evidence/directory` in the same graphical session. Its tagged binary runs the production frontend and public feeds with an isolated workspace. The harness creates its own controlling terminal and foreground process group, sends a real Ctrl+C byte or direct OS signals, verifies exit status, SQLite state/lock release and natural process-tree exit, and restarts the actual desktop UI to verify restoration. Signal-specific automation methods and storage-failure injection remain confined to the explicit smoke build; normal releases expose none of those test hooks.

The deterministic harness tests use temporary SQLite data and synthetic process reports without launching a desktop app. The actual native harness is separate from those tests and from Chromium browser E2E. Failure cleanup targets only the terminal session created by the harness.

## Screener refresh and visual analysis

Run `scripts/smoke-screener-analysis-refresh.sh` with an available Linux display and the private Screener runtime. It uses an isolated profile, genuine BTCUSD/ETHUSD daily downloads, SMA enrichment, Refresh data and indicators through the present, two visual AND rules, named-condition save/load, every-match counts, popup navigation and normal-close/restart verification. The named snapshot is restored independently of a deliberately changed draft, and loading waits for Apply. Evidence defaults to `docs/verification/screener-analysis-refresh/desktop`. It never opens the user's profile. Windows GUI and hosted CI require their own execution; cross-building and archive validation do not establish native Windows behavior.

## Themes and opened dropdowns

Run `scripts/smoke-themes.sh` in a graphical Linux session with X11/XWayland, `xwd`, `xwininfo`, `ffmpeg` and the XTest library available. The script selects X11 only for its isolated test processes and creates a temporary profile; it does not change the desktop theme or use the ordinary application profile. All automation hooks are confined to the `desktop_smoke` build.

The test uses trusted native mouse events to open and select application dropdowns in Dark, Light, Matrix, Cyberpunk and Red Sands. It checks popup bounds, colors, selected options, keyboard navigation, all 42 grouped enrichment choices, chart/lease cleanup and Red Sands persistence after restarting the app. Seven screenshots of the actual Wails/WebKit window, JSON reports and logs are retained in ignored `.tools/verification/themes/desktop/`. Browser tests additionally inspect painted progress-bar pixels for all 25 theme transitions on each Screener screen and both bullish/bearish chart candles. Native Windows GUI verification remains separate.

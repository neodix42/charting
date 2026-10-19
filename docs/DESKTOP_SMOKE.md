# Native desktop verification

Run `scripts/smoke-desktop.sh` in a logged-in Ubuntu graphical session after building prerequisites. It compiles the real local production bundle with an additional `desktop_smoke` build tag. The tag contributes UI-driving/reporting methods absent from the normal release. No Vite server, injected fixture prices, separate Go daemon or live user workspace is used.

The script creates an isolated temporary SQLite profile and runs three actual Wails/WebKit processes:

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
- Additional Ubuntu releases and X11. Current recorded build/smoke runs use Ubuntu 26.04.1 and Wayland.

# Release checklist — Codex completes this with evidence

Do not check boxes based on intentions. Reference actual source, test output or artifacts.

## Product

- [ ] First launch is exactly 1 tab / 1 chart / 1×1 grid.
- [ ] Add Tab creates one tab with one chart; Add Chart adds one chart.
- [ ] Selecting a larger grid leaves empty placeholders, not auto-created charts.
- [ ] Maximum 20 tabs and 9 charts per tab enforced in UI, backend and imports.
- [ ] Small saved workspaces restore exact counts; capacity fixture is never the default.
- [ ] Tabs, grid resizing, independent selections and keyboard navigation work.
- [ ] All three public spot adapters and all six intervals implemented without dummy fallback.
- [ ] Coinbase five-minute native restriction and ambiguous-volume handover handled.
- [ ] History, realtime candles, volume, precision, crosshair and viewport behavior tested.

## Reliability

- [ ] Autosave during use, ordered acknowledgments and normal-close flush tested.
- [ ] Workspace import/export, backups, corrupt/future schema handling and migrations tested.
- [ ] Active-tab-only rendering, subscription sharing and cleanup verified with diagnostics.
- [ ] Reconnect, rate limits, suspend/resume and stale-generation guards tested.
- [ ] Bounded resources and repeated tab-switch performance measured.
- [ ] No trading keys, account APIs, hidden telemetry or unauthorized source endpoints.

## Build and report

- [ ] Go tests / race tests where supported; frontend tests / lint / typecheck / build.
- [ ] Real Wails desktop smoke, or explicit unrun blocker without false success claim.
- [ ] Separate live exchange smoke results with restrictions disclosed.
- [ ] Pinned dependencies and reproducible build/dev/test scripts.
- [ ] Source, Ubuntu release binary/archive, icon and `.desktop` launcher.
- [ ] README includes prerequisites, build/run/use, storage path, backup/recovery and limitations.
- [ ] Third-party LICENSE/NOTICE and visible TradingView attribution retained.
- [ ] Actual versions, checksums and verification report included.
- [ ] STATUS.md and DECISIONS.md reflect actual final state and resume steps.

Final report sections: Delivered / Commands and tests / Artifacts / Unverified behavior /
Known issues / Exact next steps for unfinished work. Do not claim completion when essential
providers or persistence remain stubs.

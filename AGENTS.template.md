# CryptoDesk project guidance

This is a template. Merge it with existing repository guidance; do not overwrite that guidance.

- Build the app specified in `codex-cryptodesk/START_HERE.md` and `SPEC.md`.
- Use `IMPLEMENTATION_PLAN.md` for execution and `TEST_PLAN.md` for acceptance.
- Keep `STATUS.md` and `DECISIONS.md` in that directory current with observed evidence.
- The required stack is Go + Wails v2 + React/TypeScript + Lightweight Charts v5 + SQLite.
- Keep Go exchange/domain/storage packages independent of the webview.
- First launch: exactly 1 tab with 1 chart. Add up to 20 tabs total / 9 charts per tab.
- New tabs have 1 chart; larger grids do not auto-create charts. Restore saved counts on restart.
- Render/subscribe only the active tab by default. Never load the capacity fixture by default.
- No trading, credentials, hidden fixture fallback, or undocumented TradingView endpoints.
- Check current official API documentation and installed TypeScript definitions; do not invent APIs.
- Preserve existing work. Do not reset Git history, remove unrelated files, or modify OS settings.
- Avoid privileged installation; report missing system prerequisites and continue unblocked work.
- Run relevant Go/frontend tests and builds after each implementation slice.
- Respect exchange limits, report stale/provisional data, and never claim unrun tests passed.
- Local dependencies may be installed with the user's ordinary project permissions;
  system package installation still needs permission.
- Use a single root application unless existing unrelated content requires `apps/cryptodesk/`.
- Deliver working code and test evidence, not just an implementation plan.

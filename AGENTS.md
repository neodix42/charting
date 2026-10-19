# CryptoDesk project guidance

Build the root application according to START_HERE.md, SPEC.md, IMPLEMENTATION_PLAN.md and TEST_PLAN.md. The specification bundle was unpacked directly at the repository root; preserve it.

- Go + Wails v2 + React/TypeScript + Lightweight Charts v5 + SQLite.
- First launch and new tabs have exactly one chart. Maximum 20 tabs, 9 charts per tab; larger grids add placeholders only. Restore actual saved state.
- Keep domain, exchange, market and storage packages independent of Wails.
- Render and subscribe only the active tab. Real public spot data in normal operation; never fall back to fixtures silently.
- Preserve existing work and user data. No privileged/system installs or global configuration changes.
- Pin dependencies; verify installed public APIs. Test production logic, cleanup, ordering, persistence and candle correctness.
- Keep STATUS.md and DECISIONS.md truthful, including blocked desktop and live checks.

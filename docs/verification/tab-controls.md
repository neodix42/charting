# Current-tab chart controls — 2026-10-19

Centered below the tab row, seven period buttons and an Indicators dropdown apply to every existing chart in the active tab, including siblings hidden by maximization. Individual chart controls remain available. A shared period is highlighted only when every chart has that period. A period click resets time views to live and preserves instrument, price scale, indicators and other chart preferences.

One indicator dialog applies the chosen parameters to every chart in one workspace edit. Each chart receives its own UUID and parameter copy, retaining its existing indicators. Cancel changes nothing. An inactive tab is never edited. If any chart has 12 indicators, the shared dropdown is disabled; the reducer also rejects the entire action before any chart changes. Switching tabs unmounts the dialog, and stale tab targets are rejected.

No new persistent settings or database/schema changes are required: the resulting per-chart configurations use the existing persistence and export/import paths. First launch remains one tab and one chart; larger layouts retain empty cells.

## Verification

- `env PATH=/usr/bin:/bin scripts/test.sh`: PASS Go race/vet/bindings, seven toolchain regressions, frontend typecheck/lint, 46 unit tests and production frontend build. [Log](tab-controls-deterministic.log).
- Reducer regressions cover all nine charts with mixed periods, independent preferences and hidden tab isolation; globally unique indicator IDs and parameter copies; atomic capacity failure; stale targets; and empty layout cells.
- `cd frontend && npm run test:e2e`: PASS 18/18, including four new current-tab scenarios and the existing 200-switch capacity check. [Log](tab-controls-browser.log), [full results](tab-controls-browser/results.json), [workspace screenshot](tab-controls-browser/current-tab-controls.png). Browser checks confirm centered placement, all-chart live history ranges, one dialog for each indicator, cancel/custom parameters, persistence, independent editing, hidden maximized siblings, inactive tab isolation, capacity enforcement and unchanged market attachment counts when adding indicators.
- The global-dialog screenshot scenario also passes separately: [modal](tab-controls-browser/current-tab-indicator-parameters.png), [results](tab-controls-browser/modal-results.json). Both workspace and dialog screenshots were visually inspected.
- `scripts/smoke-desktop.sh`: PASS actual Wails/WebKit/Go/SQLite; [fresh](desktop-20261019T173409Z/fresh.json) 25 assertions, [restore](desktop-20261019T173409Z/restore.json) 11, [capacity](desktop-20261019T173409Z/capacity.json) five; [log](tab-controls-desktop.log). Global `1w` reaches all three active charts while maximized; one EMA dialog adds custom period 34 with distinct IDs, retains original indicators and leaves the hidden tab unchanged. Exact settings restore from SQLite. The 200-switch run retains nine leases/instances and clean buffers; observed median 145 ms, p95 165 ms.
- `env PATH=/usr/bin:/bin ./build.sh`: PASS production desktop build and portable packaging; [log](tab-controls-build.log). Root checksums pass; archive and root executables match, executable `--help` works, and all six generated binding files retain their original contents and permissions.

## Limits

Full manual high-DPI, suspend/reconnect and other-distribution checks remain outside this change's automated verification. Browser scenarios use the explicit test bridge; native smoke uses actual Wails/WebKit/Go/SQLite with isolated temporary profiles and real public feeds.

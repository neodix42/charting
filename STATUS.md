# Implementation status — 2026-10-19

CryptoDesk is implemented at this repository root. The bundle was unpacked directly here. Existing files were retained; original working tree was clean. Runnable source, tests, build tooling, native launcher/icon, release packaging and user documentation are included.

**First launch: exactly one tab / one chart /1×1. New tabs: one chart. Maxima:20tabs/9charts per tab. Larger grids only add placeholders. Subsequent launches restore the actual saved workspace.**

| Stage | Status/evidence |
|---|---|
|0Repository/contracts | Implemented; pinned Go/Wails/React/LWC/SQLite; strict schema/default/capacity tests pass |
|1Single Binance chart | Implemented and real live tested; actual Wails/WebKit one-chart smoke passes |
|2Dynamic workspace/persistence | Implemented; caps/placeholders/deepcopy/grids/SQLite/small3+1restart pass |
|3Market lifecycle/history | Implemented; allintervals, bounded moving history windows, shared leases, revisions, stale guards, reconnect/overflow/cancellation/retry race tests pass |
|4Bybit/Coinbase | Implemented; all3×6REST/parser tests and real discovery/history/streams pass; observed1m transition/provider |
|5Restore/recovery/shutdown | Implemented; exact example roundtrips, ordered autosave, native final-close flush, transactional backups/import/recovery/migrations, busy failures, process lock and killed-child durability pass |
|6Integration/delivery | Production Ubuntu26.04Wails build and automated native/browser smoke pass; release scripts/artifacts delivered. Full manual desktop/other-platform matrix remains unverified |

## Actual commands/results

- `bash tools/preflight.sh`: Ubuntu26.04.1x86_64, Wayland; Go1.26.8, Node24.19.0, npm11.17.0. Native headers initially absent.
- `scripts/bootstrap-native-sdk.sh`: unprivileged package download/extraction into ignored .tools/native, no OS/package installation/settings changes. GTK3.24.52/WebKitGTK2.52.6 resolved against installed runtime.
- `scripts/test.sh`: PASS Go race/vet, frontend typecheck/ESLint/23unit tests/production build; docs/verification/deterministic-final.log.
- `source scripts/native-env.sh; go test -race -tags webkit2_41 ./...` and `go vet -tags webkit2_41 ./...`: PASS.
- `cd frontend && npm run test:e2e`: PASS8/8; docs/verification/browser-final/results.json.
- `scripts/smoke-desktop.sh`: PASS actual native production Wails/WebKit, isolated first launch/restored3+1/capacity20×9/200switches; docs/verification/desktop-20261019T164147Z.
- Separate live65s/provider observations: all3discovery/sixinterval REST/live updates/minute transitions passed after fixing observed Binance metadata/wire issues. Final8sall-provider smoke also passed. Logs in docs/verification/live-*.jsonl retain the initial failure and successful reruns.
- Actual native `.tools/wails build -tags webkit2_41 -trimpath` production build passes; `scripts/build-linux.sh` emits release binary/archive, notices, version manifest and SHA256SUMS.
- Original-bundle verifier passes from isolated original HEAD archive. Working-tree checksum verifier flags intentional DECISIONS/STATUS/README edits; original HASHES.json retained. Production Go schema/fixture tests pass.

## Artifacts and limits

Build output: build/bin/cryptodesk. Distribution: release/cryptodesk-0.1.0-linux-amd64.tar.gz; checksums and version manifest alongside. Usage/storage/recovery/build details: APP_README.md. Full evidence and acceptance mapping: docs/verification/REPORT.md.

Actual native capacity test mounts9charts, with9leases/9frontendconsumers/0buffered events after200switches; three provider sockets. Latest measured median switch 144 ms / p95 176 ms. Metrics are observed baselines, not resource promises; process-tree RSS has shared/reparented-WebKit sampling limits.

No current build or provider blocker on this host. Full native manual dialogs/DPI/window/viewport/copy-paste matrix, real suspend/disconnect, daily rollover, X11/other Ubuntu releases and long-duration memory validation remain UNRUN. The final native run includes the final historical-cache, persistent-error and volume-format fixes. Full manual verification remains separate from that automated smoke.

## Exact resume task

Run the normal release with an isolated --data-dir and complete the remaining manual matrix in docs/DESKTOP_SMOKE.md; record native dialogs, historical/manual-price restore, resize/maximize, suspend/reconnect and high-DPI results. No implementation stage remains a scaffold or fixture-only adapter. Do not use live user data for verification.

## Root build entry point — 2026-10-19

Added executable `build.sh`. It delegates to the pinned production build, preserves existing Wails binding contents/permissions, and publishes `cryptodesk`, `cryptodesk-0.1.0-linux-amd64.tar.gz` and `cryptodesk-SHA256SUMS` in the repository root. `APP_README.md` documents direct launch and compatible GTK/WebKit runtime requirements.

Verified the script from `/tmp`, shell syntax/help/invalid arguments, root checksums, extracted archive contents, executable `--help` and linked-library resolution. Existing uncommitted binding permission changes were preserved. The build passed; this build-script change did not rerun the full GUI/manual application matrix.

## Terminal Node/npm fix — 2026-10-19

The regular terminal had `/usr/bin/node` but no npm; previous builds inherited npm from the IDE environment. Build/dev/test/smoke scripts now check both tools and, when needed, install the official pinned Node 24.19.0 bundle with npm under ignored `.tools/`, verifying the archive against a SHA-256 pinned from nodejs.org. The selected toolchain PATH reaches Wails and the version manifest. No system package or shell configuration changes are required. Cached tools work without another download.

The sanitized-PATH build also exposed binding generation opening the live workspace. A separate `bindings` entry point now reflects the facade without opening storage or starting services; production startup is unchanged.

Verified: `env PATH=/usr/bin:/bin ./build.sh` succeeds through production packaging and root checksum verification. `env PATH=/usr/bin:/bin scripts/test.sh` passes seven toolchain regressions, Go race/vet checks, the binding-generation regression (fresh and locked isolated profiles, all 15 exported API methods), frontend typecheck/lint/23 unit tests and production build. Cached Node/npm selection also passed with unreachable HTTP/HTTPS proxies. Existing binding contents and permissions were preserved. Full manual GUI behavior was not retested for this build-tooling fix.

## Period buttons and indicators — 2026-10-19

Each chart now has seven horizontal period buttons, including `1w`. Weekly buckets begin Monday UTC. Binance/Bybit use native weeks; Coinbase derives real weekly OHLCV from paginated daily candles and public trades. All seven intervals passed live history checks on all three providers; weekly streams delivered updates on each. The short live run did not observe an actual weekly rollover; deterministic rollover/alignment tests cover that boundary.

Per-chart SMA, EMA, Ichimoku Cloud and MACD dropdowns open parameter dialogs. OK applies, Cancel preserves the chart; applied labels support editing/removal. Overlays include shaded cloud spans, and MACD uses a separate pane. Settings persist in SQLite and JSON import/export, with legacy workspaces still accepted. Long periods load older candles automatically; unavailable history is reported. First-launch and workspace capacity behavior is unchanged.

Verification: `env PATH=/usr/bin:/bin scripts/test.sh` passes Go race/vet, bindings, seven toolchain regressions, frontend typecheck/lint, 41 unit tests and production build (`docs/verification/indicators-deterministic.log`). Actual Wails/WebKit native smoke passes fresh/restore/capacity phases with 19/8/5 assertions, including weekly controls, all four parameter dialogs, exact SQLite restart persistence, MACD pane and 200 tab switches (`docs/verification/desktop-20261019T172326Z`). All 14 browser E2E tests pass, including cancel/edit/remove, independent chart settings, long-period warm-up, insufficient history and stale response rejection. See `docs/verification/periods-indicators.md` for final browser/build evidence and remaining verification limits.

## Shared controls for the current tab — 2026-10-19

Centered between the tab row and chart area, shared period buttons immediately update all current-tab charts and return their time views to live. The shared Indicators dropdown opens one parameter dialog; OK adds an independent instance to every chart, preserving their existing indicators. Maximized siblings are included; other tabs remain unchanged. Per-chart controls remain available. Shared period highlighting reflects agreement across the actual charts. Capacity is enforced atomically, and resulting configurations use the existing autosave/restart/export/import paths without a schema change.

Verified: `env PATH=/usr/bin:/bin scripts/test.sh` passes Go race/vet/bindings, seven toolchain tests, frontend typecheck/lint, 46 unit tests and production assets. All 18 browser E2E tests pass, including the four shared-control scenarios and 200-switch capacity check. Native Wails/WebKit/Go/SQLite smoke passes 25 fresh, 11 restore and five capacity assertions; global weekly selection and EMA period 34 apply to three charts while maximized, persist with distinct IDs, and restore without editing the hidden tab. Evidence: `docs/verification/desktop-20261019T173409Z` and `docs/verification/tab-controls.md`. Test profiles were isolated from user data; the remaining full manual/platform limits above still apply.

## Portable Windows ZIP — 2026-10-19

Added executable `win-build.sh`. It cross-builds the actual production Windows x64 GUI binary in a temporary copy of the current sources, then creates `cryptodesk-0.1.0-windows-amd64.zip` and `cryptodesk-Windows-SHA256SUMS` in the root. The complete pinned Microsoft WebView2 Fixed Version 155.0.4283.62 x64 runtime is included, with original notices, the Microsoft license, runtime provenance and file checksums. No runtime installation or administrator access is needed on Windows. Portable marker selects workspace/browser data beside the executable; default capacity and saved-workspace semantics are retained.

OS-specific storage uses Windows LockFileEx or Unix Flock. Windows startup handles fixed-runtime paths, data overrides, timezone data, native errors and Microsoft's Windows 10 folder permissions. Linux behavior and existing Linux release outputs remain preserved. A sanitized-PATH Ubuntu cross-build passed; the 303.8 MiB ZIP contains a verified x64 PE32+ Windows GUI binary, and all 351 file checksums passed. Twenty-five Python tests, Go race/vet/bindings, 46 frontend units and production assets pass. All internal Windows package tests and startup tests also pass under an isolated Wine profile. Actual Windows 10/11 GUI/WebView2/dialog/DPI/network behavior remains **UNVERIFIED**; Wine is not native Windows verification. Usage: `docs/WINDOWS_PORTABLE.md`; evidence: `docs/verification/windows-build.md`.

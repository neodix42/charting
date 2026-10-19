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

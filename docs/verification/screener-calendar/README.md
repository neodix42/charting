# Weekday and Hour enrichment verification

Enrichment → Time adds `weekday` (Monday=1 through Sunday=7) and `hour` (0–23) from the original row's Unix-millisecond timestamp in UTC. Both need only timestamp and have no parameters or warm-up. Existing calendar columns are replaced; Refresh retains/reconstructs their recipes and recalculates appended closed bars. Analysis groups both under Time.

- `tests.log`: complete deterministic Python packaging, Node download/enrichment/Refresh, Go race/bindings, frontend typecheck/lint/unit and production build.
- `go-tests.log`, `production-tests.log`, `go-vet.log`: additional tagged whole-app/native compilation and race/vet checks.
- `browser-tests.log`, `browser-results.json`: focused Enrichment, Refresh and Analysis browser tests, including timestamp-only calendar selection and submitted Analysis operands.
- `native-calendar.log`, `desktop/*.json`: actual production Wails/WebKit buttons on real BTCUSD/ETHUSD daily downloads, SMA/Tenkan/calendar values, eight exact backups and identical columns/files after restart. Profiles are isolated; the user workspace is never opened. These D1 timestamps are UTC midnight, so native Hour values are zero. Node/offline tests cover nonzero hours.
- `private-runtime.json`, `bundled-runtime.json`: actual private Node/worker/decoder under process-only `TZ=Pacific/Honolulu`; local Sunday14 differs from UTC Monday00, but output remains weekday1/hour0. Tests also cover Sunday23→Monday00, no warm-up, repeat idempotence, calendar Refresh replay and exclusion of open candles using a local transport. No network or system timezone change.
- `*-build.log`, `*-checksums.log`, `release-packages.json`: all three rebuilt release variants, checksums, runtime inventories and exact worker/catalog source hashes.

The existing release workflow runs the expanded tests and packaged offline smoke and inventories the updated catalog/workers in Ubuntu and Windows archives. No dependency, global installation or workflow change is needed. Native Windows GUI and GitHub-hosted CI remain unrun.

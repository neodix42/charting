# Screener Refresh and visual analysis verification

The source change replaces list-only Refresh with provider data merging and indicator recipe replay, and adds historical visual AND rules with occurrence charts. Native tests use isolated profiles and real public data; no user workspace was opened.

- `tests.log`: deterministic Python, Node, Go race/bindings, TypeScript/lint/unit and production assets.
- `go-tests.log`, `production-tests.log`, `go-vet.log`: additional tagged native whole-app checks.
- `browser-tests.log`, `browser-results.json`: final full browser run. `initial-browser-*` and `initial-markets-failure/` retain the first run's market-test injection race; `focused-markets.log` verifies the narrowly scoped test fix.
- `native-refresh-analysis.log`, `desktop/*.json`: actual production Wails/WebKit refresh of genuine BTCUSD/ETHUSD daily history, applied SMA values, every-bar AND matches, popup navigation and exact close/restart restoration.
- `native-resume.log`, `desktop-resume/*.json`: actual current-build close during two public minute downloads and automatic restart completion, unchanged committed prefixes and identical queue IDs, restored controls/categories, no duplicate CSVs.
- `analysis-ui.png`, `analysis-match-chart.png`: layout review in the explicitly marked browser test harness, populated with the real CSVs/results from `desktop/analysis-refresh.json`. Screenshots do not establish native execution; the preceding reports do.
- `private-runtime.json`, `bundled-runtime.json`: pinned private Node, real CSV parser and indicator calculators, plus Refresh's real downloader/decoder with a local replacement transport. No network; exact append, saved SMA replay, open-candle exclusion and preservation checks.
- `*-build.log`, `*-checksums.log`, `release-packages.json`: Ubuntu standard, Ubuntu portable and Windows cross-builds, checksums, complete archive inventories and exact source worker/catalog hashes.
- `workflow-lint.json`: local actionlint 1.7.12 reports the unchanged `concurrency.queue` baseline key; removing only that key in a temporary copy passes all other checks. The workflow retains its existing queue behavior.

Native Windows GUI and GitHub-hosted CI are unrun. Normal download jobs resume automatically. Refresh/enrichment preserve completed files but require an explicit rerun for unfinished work. Analysis drafts persist; scan results and indexes are transient.

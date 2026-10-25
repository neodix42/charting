# Screener enrichment verification — 2026-10-25

The downloader resume/preferences task was committed first as `059f7a1`.
This evidence covers the subsequent trading-signals enrichment feature.

| Check | Observed result | Evidence |
| --- | --- | --- |
| `scripts/test.sh` | PASS: 155 Python packaging, 28 Node, 161 frontend unit tests; Go race/bindings, typecheck/lint/build | `deterministic-tests.log` |
| Real library default/minimum parameters | PASS for all 40 indicators; exact SMA, MACD/Ichimoku warm-up/row alignment, ticks, quoted fields, repeated columns, errors and streamed 50,000-row file | Node section of deterministic log |
| `go test -race -tags webkit2_41 ./...` | PASS | `go-tests.log` |
| Production race / tagged vet | PASS | `production-tests.log`, `go-vet.log` |
| Full browser E2E | PASS 117/117, including eight Enrichment and ten previous Screener tests | `browser-tests.log`, `browser-results.json` |
| Native production Wails/WebKit | PASS 8 enrichment / 3 restart assertions | `native-enrichment.log`, `desktop/enrichment.json`, `desktop/enrichment-restore.json` |
| Exact original backups / restart bytes | PASS: four original backups and unchanged enriched files across restart | `desktop/backups.json`, `desktop/restart-comparison.json` |
| Packaged private Node/worker offline | PASS: exact SMA 3, blank warm-up cells and unchanged original fields | `bundled-runtime.json` |
| Linux, portable Linux, Windows builds | PASS, complete runtime inventories and checksums; workers/catalog match source | `release-packages.json`, `*-build.log`, `*-checksums.log` |
| Workflow lint | Only the existing `concurrency.queue` key is rejected by old actionlint 1.7.12 in both baseline/current; temporary copy without that key passes | `workflow-lint.json` |

The native run uses actual Dukascopy BTCUSD and ETHUSD daily data for
2024-01-01 through exclusive 2024-01-10, drives the native Enrich button for
SMA 3 and Tenkan 9, checks numeric values against the original candle windows,
and reopens the same isolated profile. It does not open a user workspace.

`enrichment-ui.png` is a browser test-harness screenshot using file metadata from
the real native run. Its explicit fixture badge distinguishes it from native
and provider evidence. Production never falls back to those fixtures.

Native Windows GUI/Node execution and GitHub-hosted workflow execution remain
unrun. Enrichment itself does not automatically resume interrupted work;
completed CSV columns persist and are rediscovered. Downloader continuation and
saved symbols/timeframe/category behavior remain covered by the earlier commit
and the full regression suite.

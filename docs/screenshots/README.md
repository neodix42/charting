# README screenshots

These six light-theme screenshots were captured on 2026-10-25 from the current CryptoDesk React UI at 1600 × 1100, using its explicit browser-test bridge and an isolated documentation profile. They are product documentation assets, kept separately from disposable verification output. No application behavior or styles were changed for the captures.

| Screenshot | Content |
|---|---|
| [chart-workspace.png](chart-workspace.png) | Two BTCUSDT spot charts, 1h and 4h, with SMA, EMA, volume and MACD. |
| [screener-downloader.png](screener-downloader.png) | The complete Dukascopy catalog, grouped selection and illustrative per-symbol progress. |
| [screener-enrichment.png](screener-enrichment.png) | MACD parameters, completed enrichment and actual CSV headers. |
| [screener-analysis.png](screener-analysis.png) | A two-row AND rule and saved condition sets. |
| [screener-results.png](screener-results.png) | Historical occurrence counts and first/last match dates. |
| [screener-match-chart.png](screener-match-chart.png) | A BTCUSD match with real surrounding bars and calculated indicator series. |

Chart-workspace candles were fetched from public Binance through the production exchange adapter, then replayed for a stable capture. They are recorded market data, not a live price display. The chart health text identifies this documentation replay.

Screener inputs are genuine Dukascopy BTCUSD and ETHUSD daily histories from 2024-01-01 through 2026-10-24, with 1,028 bars per file. The production Screener manager and bundled indicator runtime calculated SMA(200), Ichimoku(9,26,52), MACD(12,26,9), ATR(14), RSI(14), Weekday and Hour. An earlier SMA(2) column remains present.

The pictured rule requires Close above SMA(200) and a MACD histogram cross above zero within three closed bars. The production analysis engine returned 59 BTCUSD and 41 ETHUSD occurrences, 100 in total. Result metadata and popup series were supplied from those real outputs; the match chart shows the first BTCUSD occurrence, 2024-10-14. Saved sets were populated from full condition requests.

Downloader job states and percentages are **illustrative**, labelled “Example” in their messages. They demonstrate queue/progress presentation and do not establish download speed or a completed four-symbol batch. The catalog itself is the actual 1,499-instrument, 39-category catalog. `/demo/cryptodesk/screener` is a documentation display path.

The **EXPLICIT TEST FIXTURE MODE** banner identifies the capture environment. These screenshots document layout and usage; they do not establish native Ubuntu/Windows execution or live subscription behavior. See [STATUS.md](../../STATUS.md) for separate verification results. Capture completed with all six images and no browser page errors. Generated inputs, isolated profiles and logs remain in ignored `.tools/readme-demo/`.

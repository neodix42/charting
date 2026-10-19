# Synthetic regression fixtures

All prices, volumes, IDs and events here are invented test inputs. No live API tests were run
in preparing them. These are NORMALIZED domain payloads, not complete Binance/Bybit/Coinbase
wire captures. Add separate sourced provider-envelope parser tests during implementation.

`trades.json`: duplicate and out-of-order trades, then a late correction. Expected 1m OHLCV
and aggregated 5m values use exact decimal arithmetic. Trade IDs are strings.

`native-candles.json`: cumulative snapshots include a duplicate, stale revision, close and
new bucket. Expected volume is 5, not accumulated snapshots. `sourceRevision` is test metadata;
real adapters must derive ordering from actual documented provider fields.

`history-pages.json`: newest-first pages overlap by one candle. Expected output is ascending
and unique. Implement pagination-progress tests in addition to this merge test.

`lifecycle-cases.json`: startup/maxima, grid placeholders, restore-small, overlap uncertainty,
stale responses, lease races and save ordering. Translate into executable production tests;
a list of cases is not itself an application test suite.

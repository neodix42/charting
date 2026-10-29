# CryptoDesk screener runtime

This private runtime ships beside CryptoDesk on Ubuntu and Windows. It runs the
exact `dukascopy-node` dependency from `package-lock.json`, using its public
Node API. It never invokes `npx`, installs packages at application startup, changes
global Node configuration, or substitutes generated market data.

See [Updating the Screener downloader](../README.md#updating-the-screener-downloader)
for the automatic version check in `build.sh`, compatibility validation before
adopting a newer release, and rebuilding the matching application and private
runtime. Set `CRYPTODESK_UPDATE_SCREENER=0` to build the existing exact pin without
an upstream version check; release CI uses this setting for both targets.

The release runtime includes:

```
screener-runtime/
  node                  # node.exe on Windows
  worker.cjs
  package.json
  package-lock.json
  node_modules/
  browsers/             # pinned Playwright Chromium for WAF recovery
```

The portable launcher sets `CRYPTODESK_SCREENER_RUNTIME` to this directory. The Go
manager also finds a sibling runtime beside the executable. Source checkouts
prefer `.tools/screener-runtime-<os>-<arch>` produced by the packaging scripts;
otherwise development uses local `screener-runtime/node_modules` and an existing
Node.js 20 or newer on `PATH`. Dependencies can be prepared locally with
`npm ci --prefix screener-runtime --ignore-scripts`. Releases bundle Node and
Chromium rather than requiring a global installation. If WAF recovery cannot
launch a browser or the public endpoint is unavailable, the individual job fails
with the actual error.

`generate-catalog.cjs` reads the pinned installed package README's 39 categories
and preserves their ordering and labels. It verifies every listed ID against the
public `instrumentMetaData` export and requires exact equality: 1,499 unique
instruments. The result is embedded in Go, so catalog browsing needs no network.
The README's earliest date is descriptive; the download API applies its own
timeframe-specific availability limits. All 10 exported timeframes are exposed:
`tick`, `s1`, `m1`, `m5`, `m15`, `m30`, `h1`, `h4`, `d1`, and `mn1`.

Dates are UTC; `from` is inclusive and `to` is exclusive. CSV output preserves
the library's default bid candles, volume units (millions), and flat filtering.
Ticks contain both bid and ask prices and their volumes. Timestamps are Unix
milliseconds. Candle CSV columns are `timestamp,open,high,low,close,volume`; tick
columns are `timestamp,askPrice,bidPrice,askVolume,bidVolume`.

The worker reproduces the library's `getHistoricalRates` validation, date
normalization, URL generation, fetching, processing and CSV formatting through
public exports verified in the installed declarations and implementation. It
fetches one source file at a time, keeping the price-data working set bounded
for long tick ranges. Downloader and Refresh share a Go-owned request queue
that permits one active data request, including browser verification, with
request starts at least two seconds apart. HTTP 429 adds Retry-After/backoff;
HTTP 403 stops requests and records an access-denial cooldown that survives
restart. This pacing is an application default, not a published provider quota.
Source files contain whole UTC aggregation buckets: hourly
tick sources, daily minute sources, monthly hourly sources, and yearly daily
sources. Download progress counts actual completed source requests. CSV headers
are written once, rows stay chronological, and an all-empty range fails visibly.

The adapter skips structurally valid empty JSON sources before aggregation,
avoiding dukascopy-node 1.51.1's empty daily/hourly aggregation crash. Timestamp
and all source columns must describe a consistent empty dataset; malformed
responses still reach the upstream decoder and fail. Empty source checkpoints
remain resumable, and incremental downloads/refresh preserve saved histories.

Go downloads and refreshes multiple files in parallel, using **Settings →
Screener → Worker threads**: **5** by default, configurable from **1–20**.
Each refresh worker completes its download and indicator recalculation before
taking another file. Job rows stay in selection order. Resumed queues use the
saved setting; older profiles without it default to 5. Local Enrichment uses the
same configured file-worker limit without making provider requests. Source
requests remain sequential inside each worker, with shared two-second request
start pacing, initial session setup and provider cooldowns across workers.

Go owns cancellation of every child process tree, machine-local preferences,
private job directories and final publication. Downloader and Refresh reuse the
library's supported session cache at `<profile>/screener-provider/waf-token.json`.
That cache persists across jobs and restarts. Cancellation stops dispatch,
drains active children and removes their owned temporary directories; completed
files remain, and failed jobs retain valid checkpoints for explicit retry.

Downloads maintain one CSV per symbol and timeframe, fetching only missing
ranges and retaining saved bars. Existing overlapping or separate histories are
consolidated safely, with exact original files retained in hidden backups and
applied indicators recalculated over the combined history. See the
[Screener guide](../APP_README.md#screener) for download, refresh, publication,
backup and restart behavior.
The destination defaults to `<profile>/screener`; settings are atomically stored
in `<profile>/screener.json` and do not enter workspace imports or exports.

Validation commands:

```
npm test --prefix screener-runtime
npm run catalog --prefix screener-runtime -- --check
go test -race ./internal/screener
```

The deterministic worker tests inject only transport responses. Production
decoder, aggregation and CSV formatting come from the pinned installed package.
The Go tests run real child processes through the production protocol and cover
validation, bounded parallel dispatch, stable result ordering, errors, cancellation,
shutdown, preservation of existing output, and restoration of legacy preferences
and queued work using the saved global worker limit. Transport tests check
sequential source requests within each worker, shared pacing and access-denial
handling. Local enrichment tests cover its configurable file-worker pool.
The worker syncs each complete source batch and atomically commits checkpoint.json
with its CSV byte offset, row count, header, last timestamp, frozen UTC range
and next source index. Normal app close leaves queued jobs and checkpoints for
automatic restart; explicit Cancel remains cancelled. Restart truncates only
uncommitted tails and skips completed source files. A completed checkpoint can
publish without another network request. Invalid/truncated checkpoints fail
without silently discarding existing data. Go records publication intent before
final publication to support recovery after interruption.
The parent-owned stdin pipe stays open while Go owns the worker; EOF terminates
an orphan worker after an abrupt app exit, including on Windows.

UI choices are stored separately in screener-view.json and the durable queue in
screener-downloads.json. They stay outside chart workspace exports. The native
scripts/smoke-screener-resume.sh verifies actual UI close/restart, automatic
continuation, preserved CSV prefixes, job identity and restored category state.
Optional live checks are recorded separately from offline checks.

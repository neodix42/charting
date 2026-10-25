# CryptoDesk screener runtime

This private runtime ships beside CryptoDesk on Ubuntu and Windows. It runs the
exact `dukascopy-node` 1.51.0 dependency from `package-lock.json`, using its public
Node API. It never invokes `npx`, installs packages at application startup, changes
global Node configuration, or substitutes generated market data.

The complete release layout is:

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
fetches at most four source files at a time, keeping the price-data working set
bounded for long tick ranges. The manager assigns smaller source batches when
several symbols run together: `min(4, floor(20 / effectiveSymbolWorkers))`.
This limits the entire batch to at most 20 concurrent source requests. Direct
worker calls without a manager allowance retain the four-file default.
Source files contain whole UTC aggregation buckets: hourly
tick sources, daily minute sources, monthly hourly sources, and yearly daily
sources. Download progress counts actual completed source requests. CSV headers
are written once, rows stay chronological, and an all-empty range fails visibly.

Go downloads symbols through a bounded worker pool with 1–20 workers, defaulting
to 20. Fewer selected symbols use fewer workers; queued jobs start as slots
become available. Job rows stay in selection order regardless of completion
order, and a failed symbol does not stop the others. Concurrency is a per-batch
request setting, not part of imported workspace state.

Go owns cancellation of every child process tree, machine-local preferences,
private job directories and final publication. Each child has a separate WAF
token cache inside its exclusively-created temporary directory. Cancellation
stops dispatch, drains active children and removes those owned directories,
including token-cache scratch files, before returning. A successful
download is published atomically using a same-directory hard link. Existing
files are preserved; repeated requests receive numbered filenames. A filesystem
without hard-link support returns an explicit publication error. Failed or
cancelled jobs remove only their private temporary directories. Completed files remain.
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
validation, pool limits, slot refill, stable result ordering, independent errors,
cancellation, shutdown, preferences and preservation of existing output. Twenty
gated helper processes prove simultaneous execution while a 21st job stays queued.
Transport tests check the worker's actual maximum simultaneous source requests.
Optional live checks are recorded separately from these
offline checks.

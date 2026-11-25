# Architecture and ownership

## Components

```text
Wails desktop shell
  Go facade (typed bound methods + runtime events)
    Workspace service → SQLite workspace store
    Market service → subscription manager → exchange adapters
                   → historical request coordinator → candle cache
    Comparison service → existing market history/catalog APIs
  React / TypeScript
    workspace state → tab toolbar / grid / chart controls
    data bridge → bounded frontend series buffers → Lightweight Charts instances
```

Use Wails v2 deliberately; do not mix v3 examples with v2 APIs. Frontend assets ship with the
app; normal operation must not require a Node development server, CDN, or separate Go daemon.
The Linux runtime requirements are described in BUILD_LINUX.md. [S01, S02]

Suggested layout when starting a new root app:

```text
main.go, app.go, wails.json, go.mod, go.sum
internal/domain/              # types, workspace validation, timeframes
internal/exchange/binance/
internal/exchange/bybit/
internal/exchange/coinbase/
internal/market/              # subscriptions, reconciliation, caches
internal/workspace/           # operations and persistence orchestration
internal/storage/             # SQLite migrations and repositories
frontend/src/bridge/
frontend/src/workspace/
frontend/src/charts/
frontend/src/components/
frontend/src/test/
scripts/, build/, docs/, tests/
codex-cryptodesk/             # this retained specification bundle
```

No exchange or domain package should import the GUI framework. Accept contexts, a clock,
HTTP transport, and WebSocket dialer through testable interfaces. Prefer a small design over
microservices, an embedded browser server, or a generic plugin system.

## Workspace ownership

The frontend holds the interactive current workspace draft. A central reducer/store owns
its mutations and increments an edit sequence. Go validates and commits complete workspace
snapshots through an ordered save coordinator. Snapshot order and acknowledgment rules are
in PERSISTENCE.md. Runtime caches, live connections, listeners, and request IDs never appear
in persisted workspace JSON. A layout change is not a reason to reconnect an unchanged feed.

Keep stable UUIDs for tabs/charts. A chart's identity is independent of a grid position, feed
key, or rendered component. Render keys must be chart IDs, not list indices or random values.
Construct the initial 1-tab/1-chart workspace only when no saved workspace exists.

## Data ownership

Go owns normalized candles and authoritative per-feed state. Use this feed key:
`exchange | market=spot | nativeInstrument | interval | candleTimezone=UTC`.
Coinbase may share one raw trade feed across several requested intervals for the same product.
A stream subscription and a chart subscription are distinct concepts.

The subscription manager tracks leases. Identical visible requests share upstream data.
Releasing one chart does not disconnect another owner. Last release schedules a cancellable
2-second unsubscribe grace; a new lease cancels it. Connection readers/writers have bounded
queues, cancellation, and one owner for writes. Batch subscribe changes and respect each
exchange's documented limits. Default hidden tabs release their leases; caches may remain.

Before acknowledging an attach, establish frontend listener readiness and avoid the gap
between returning a snapshot and delivering subsequent updates. Use monotonically increasing
per-feed revisions and a listener-first/snapshot-watermark protocol, described in CONTRACTS.md.
Do not send bulk history via high-frequency broadcast events. Use bounded requested snapshots.

## Rendering lifecycle

Dukascopy's native and calculated instrument integration is described in
[Synthetic Instruments](docs/SYNTHETIC_INSTRUMENTS.md). Canonical Screener CSVs and
SHA-bound provenance feed enrichment, analysis, alerts and the Wails-independent
`internal/synthetic` chart adapter. Calculated charts do not create broker sessions;
their active-tab subscription polls atomically published closed history. Both deferred
production startup and the synchronous initializer install this adapter; its history
reader waits for Screener readiness only when historical data is requested. The provider
fetcher, pacing, durable queue and publication infrastructure remain shared. Screener
receives a Wails-independent history-provider interface before restored downloads
resume. A configured JForex manager exports bounded underlying tick hours and native
daily BID candles through the Go-owned private worker pipe; credentials never enter
Node. The source/account identity is frozen per batch, cached separately, and checked
again before publication. Public HTTP denial affects the public route only; shared
rate limiting, cancellation and atomic storage apply to both routes.

Fast synthetic inputs are genuine H1 BID observations exported in at most
28-day batches and read inside the SDK one UTC day at a time. An empty synchronous
day receives an independent, fully completed date-range read. Because both paths
can reuse a falsely empty SDK monthly cache, unresolved days require a direct,
nonempty monthly BID source response with strict bounds, finite loaded availability,
source/account fences and actual historical calendar proof. Selected BID candles
apply SDK splits followed by account feed commission, matching the normal history
path. A null monthly-file result permits one same-file retry after five seconds
inside the original 120-second budget, with source fences before and after; empty,
corrupt and changed-source results never authorize that retry. An unresolved null
archive remains an error, never empty coverage. Only a verified complete
past-month file can establish a genuine no-quote member day. Unknown/MAX metadata
is not proof of absent history; one instrument-scoped RAM reload may recover a failed
bootstrap within the absolute work deadline. Partial/error/cancelled reads cannot
publish coverage. Hourly policies `verified-full-hour-v2` and `provider-native-h1-v2`
use new cache identities; checksum-valid positive legacy days remain read-through,
while consecutive missing/legacy-zero days are revalidated in at most 28-day batches.
Every compatible cached day stops the batch: valid neighbors retain their exact
bytes without becoming new provider dependencies. Original files are not deleted.
Shared Node cache ownership is published atomically
with PID/nonce identity. Only proven-dead, unchanged generations can be retired;
sibling reaper locks use the same bounded recovery protocol and retain evidence.

Only the active tab creates charts: first launch 1; at maximum 9. Inactive tabs keep state,
not mounted canvases. Empty grid cells create neither chart instances nor upstream leases.
Each independent instrument cell gets its own chart instance; an optional volume pane is
part of that chart, not an additional independent chart. Use current public v5 APIs. [S04]

Create the chart once per mounted cell. Update options and series imperatively rather than
recreating it whenever React state changes. Capture viewport before disposal. Disconnect
ResizeObserver, crosshair/range callbacks, DOM handlers, timers, and the exact Wails event
subscription cleanup callback on unmount. Test React development StrictMode remount behavior.

Live bars use incremental updates. History prepend and older-bar corrections may use a
controlled data replacement with viewport restoration; avoid full setData on every tick.
Do not call fitContent on every update. Queue updates by candle timestamp, not only `latest`,
so a batched flush crossing a candle boundary retains both bars. [S04, S05]

## Comparison ownership

The top-level Comparison screen is separate from saved chart tabs and contains
Daily change. `internal/comparison` owns validated weeks/base-symbol selections,
shared UTC baseline calculations and a small bounded cache of complete closed
pages. `app_comparison.go` is its lazy Wails facade; it delegates catalogs and
history to the existing market service, so provider pacing, retained candle
storage and cancellation remain shared. Comparison creates no stream leases,
broker sessions or trading services. Its private `comparison-view.json` is saved
atomically with version validation; missing files alone receive defaults.

Catalog selection prefers an exact native Hyperliquid perpetual, then exact-base
spot, then an exact-base builder perpetual, all quoted in actual USD, USDC, USDT
or USDH. USDC is preferred among equivalent spot pairs. Binance USDT spot is
eligible only when complete Hyperliquid discovery proves no eligible USD-quoted
market; provider failure cannot authorize fallback. Quotes are disclosed and are
not converted or treated as aliases. TON has one explicitly verified same-asset
rename to GRAM, supported by [TON](https://ton.org/) and the
[Binance 1:1 rebranding notice](https://www.binance.com/en/support/announcement/detail/fe307fba935b44698fde4db01e84a7eb).
The canonical saved TON selection discloses the actual GRAM feed; it does not stitch
historical markets or accept wrapped/unrelated tokens. All lines use the same fully closed UTC
baseline day and `(close / baselineClose - 1) * 100`; unavailable baselines cannot
be rebased individually. Missing daily closes remain explicit gaps. The visible
screen owns cancellable request IDs and its chart, observers and listeners;
hidden Comparison charts are disposed and late replies cannot replace a newer
selection. Selected-asset labels use the returned series metadata before catalog
metadata, so a refreshed market or unavailable asset cannot retain an old source
label. Empty selections persist and request no daily history.

## Stale-response protection

Every chart attach/selection change increments its runtime generation. Requests and results
carry chartId, generation, feedKey, and requestId. Dropping a response for an old generation
must not change a new chart selection or reopen an already closed tab. Cancellation saves
resources but generation validation is still mandatory. Feed revisions order events within
one feed; revisions from different feeds cannot be compared.

## Bounds and diagnostics

Initial engineering defaults, to be measured rather than advertised as performance claims:
- 500 initial history bars; ordinarily 10,000 loaded bars per active series with windowed historical loads. Intraday calendar-range overlays can raise this to their bounded warm-up, at most 20,161 one-minute bars for a complete previous week plus the current week and a boundary bar. Only active charts load this optional history; provider limits remain visible.
- 250,000 normalized candles total in the backend LRU cache, with correct ownership eviction.
- 100 ms maximum ordinary UI update coalescing; preserve distinct bar changes and error states.
- At most 2 concurrent history requests per provider initially, plus provider-specific pacing.
- A separate optional disk candle cache capped at 256 MiB; never evict workspace settings.
- Bounded reconnect/trade buffers. Overflow marks affected data provisional and starts repair;
  never silently drop trades while marking a derived candle complete.

Expose a diagnostics panel/log with active chart instances, leases, unique feeds, sockets,
queue depth, cache size, REST counts, reconnects, and last persistence revision. Use these to
prove the maximum workspace does not mount 180 charts or open 180 sockets.

No background all-feeds-live mode is required for completion. Add it only after the required
application passes. Do not introduce a price/trading recommendation engine or account APIs.

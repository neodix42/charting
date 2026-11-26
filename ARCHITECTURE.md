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

JForex platform-order reconciliation normally requires the SDK original amount to
match the durable requested quantity. The pinned SDK's PlatformOrderImpl copy
constructor omits originalAmount. A private knownFilledQuantity RPC field carries
only a previously acknowledged actual fill or successfully verified filled execution
for that exact journal/native order. Recovery of zero or fill-sized original amounts
requires exact ID, label, instrument and side plus complete matching fill history;
Go independently validates the durable witness and returned quantities. Unknown or
changed exposure remains blocked. Close repeats immutable identity/fill/remaining
checks on the SDK strategy thread before its existing one-use operation. The true
SDK original amount stays in snapshots; stored requested size is never replaced.
Trade-history UI uses last successful checkedAt and refresh failures to distinguish
saved status/protection from current broker verification, preserving data on errors.

Live top-of-book observations travel through `domain.Health.Quote`, separately from
candles and `LastTradeAt`. Binance bookTicker, Bybit orderbook.1 and Coinbase ticker
subscriptions are shared by native instrument through the existing category hubs;
Binance derivatives use a separate shared `/public/ws` hub from candle `/market/ws`.
JForex forwards the immutable accepted ITick pair, retaining epoch milliseconds on
heartbeats and closed-market health. Validate prices, identity, ordering and observed
timestamps; missing sides stay nullable and invalid/disconnected books lose trust.
Queued quotes are fenced across transport generations. No candle price supplies a
missing executable side, and quote-only events never modify candles.

ChartCell owns the top-left hours/quote stack without recreating its chart or feed.
The quote legend computes midpoint spread in basis points, preserves price precision,
ages observations locally and clears previous-feed data on identity changes. Calculated
instruments expose no executable book. Hyperliquid's stateless bottom-right overlay
uses matching public catalog maxLeverage for asset maximum/minimum initial margin;
it adds no account polling and makes no claim about selected leverage or size-tier
margin. Ticket estimates and previews carry optional bidPrice/askPrice from the same
book/tick as their existing side-specific entryPrice. Review freezes the complete
snapshot; sizing, risk, freshness, confirmation and order submission remain unchanged.

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
day receives an independent, fully completed date-range read. Both paths can reuse
a falsely empty SDK monthly cache. Eligible fully closed UTC years therefore use
the canonical paired ASK/BID yearly archive first, aligned to January 1 and only
when loaded first-H1 availability is at or before that year start. Current and
partial listing years retain monthly recovery. Validate the complete year, every pair,
strict hourly ordering and observed BID data before taking immutable BID snapshots.
Apply SDK splits then account feed commission without changing any timestamp.

Only unavailable null/empty yearly results may try a monthly BID archive. Each
attempt has independent retired callback/failure state; a positive failed or
corrupt archive aborts without fallback. An otherwise unfailed null monthly result
permits one same-file retry after five seconds within the original 120-second
budget. Context, client, account, active/default provider and first-H1 metadata
fence archive reads, adjustments, ordinary day reads and cached-month reuse.
Retain at most the three requested months that fit the bounded export. Actual
historical calendar proof remains required; Sunday or an archive failure never
proves absence. Unknown/MAX metadata gets one scoped RAM bootstrap, and unresolved,
partial or cancelled reads cannot publish coverage. Hourly policies
`verified-full-hour-v2` and `provider-native-h1-v2`
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
Daily change. `internal/comparison` owns validated weeks/source selections,
shared UTC baseline calculations and a small bounded cache of complete closed
pages. `app_comparison.go` is its lazy Wails facade; it delegates catalogs and
history to the existing market service, so provider pacing, retained candle
storage and cancellation remain shared. Comparison creates no stream leases,
broker sessions or trading services. Its private `comparison-view.json` is saved
atomically with version validation; missing files alone receive defaults.

`Catalog.markets` retains each exact eligible provider/market/native instrument
from independent public Hyperliquid, Binance, Bybit and Coinbase discovery.
Explicit selections use canonical escaped source IDs, preserve native case and
Unicode, and resolve only against discovered markets. The same base asset may
appear on several venues. `Catalog.assets` retains the old bare-symbol automatic
preference for saved views; incomplete Hyperliquid discovery disables that
automatic inference while leaving healthy explicit choices usable. Real provider
warnings remain visible; normal source-selection notices are omitted.

The private version 2 view retains exact selections, accepts valid legacy version
1 bare choices without rewriting on read and preserves unrecognized saved bytes.
Chart keys, colours, hover, visibility and selection use source IDs rather than
base symbols. Source-aware labels distinguish multiple curves for the same asset;
missing markets retain readable native labels without exposing storage keys or
inventing availability.

Markets quote actual USD, USDC, USDT or USDH without conversion. Verified
TON/GRAM identities retain actual native feeds: Hyperliquid/Binance's known
successors use the existing July 2 guard, Bybit's exact GRAMUSDT Spot/Perpetual
uses its documented June 22 transition, and Coinbase retains TON-USD. All lines
use one fully closed UTC baseline and `(close / baselineClose - 1) * 100`.
Missing baselines return their requested UTC date and first actual close in the
returned window; they never cause a source change, rebase or application-level
history stitching. Subsequent missing closes remain gaps.

Coinbase native history sends `end=to-1` to its inclusive API. A caller's bounded
range up to 1,000 candles is paged in at most 350-candle windows using existing
HTTP pacing/cancellation and normalization, including empty windows. This avoids
both a displaced 26-week baseline and truncation of longer Comparison periods.

The visible screen owns cancellable request IDs and its chart, observers and
listeners; hidden charts are disposed and late replies cannot replace a newer
selection. Returned source metadata precedes catalog metadata, and empty
selections persist without requesting history.

The Comparison facade uses `market.HistorySnapshot`, owned by its caller and the
market service rather than a chart feed. A retained feed can keep its cancelled
context after its last chart lease closes; that context must not cancel an
independent Comparison read. Snapshot requests retain shared caches, provider
epochs, semaphores and pending-request bounds without acquiring a stream lease.
Ordinary `market.History` retains chart-owned pagination cancellation. Leaving
Comparison, replacing a request or closing the service still cancels its work.

Comparison line hover reuses the crosshair subscription and public time/price
coordinates to hit-test real dots and consecutive daily segments. The chart
library's native hit test also includes transparent gaps and dots-only segments,
so it cannot establish a visible line hit. Only genuine pointer events activate
hover; model-only resize/render events clear it, and a guard suppresses recursive
events emitted by line-style updates. Highlighting changes width without replacing
data, reconnecting feeds or resetting the viewport.

Comparison legend buttons control both the line and separate dots series through
the public `visible` option. Session-only hidden symbols belong to the parent
screen so refreshes, period changes and hidden-chart disposal retain the choices
without changing saved selections or triggering history requests. Visibility
updates reuse the chart, exclude hidden geometry from hover, clear active focus
and move the single zero-percent reference to the first visible series. An
all-hidden chart keeps its legend and data available for restoration.

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

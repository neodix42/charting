# Architecture and ownership

## Components

```text
Wails desktop shell
  Go facade (typed bound methods + runtime events)
    Workspace service → SQLite workspace store
    Market service → subscription manager → exchange adapters
                   → historical request coordinator → candle cache
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

## Stale-response protection

Every chart attach/selection change increments its runtime generation. Requests and results
carry chartId, generation, feedKey, and requestId. Dropping a response for an old generation
must not change a new chart selection or reopen an already closed tab. Cancellation saves
resources but generation validation is still mandatory. Feed revisions order events within
one feed; revisions from different feeds cannot be compared.

## Bounds and diagnostics

Initial engineering defaults, to be measured rather than advertised as performance claims:
- 500 initial history bars; 10,000 loaded bars per active series with windowed historical loads.
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

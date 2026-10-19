# Domain and frontend/Go bridge contracts

The names below are proposed application contracts, not claims that Wails supplies these
functions. Implement equivalents using generated Wails v2 bindings and runtime events. [S03]

## Core values

```text
Exchange = binance | bybit | coinbase
Market = spot
Interval = 1m | 5m | 15m | 1h | 4h | 1d
FeedKey = (exchange, market, nativeInstrument, interval, UTC)
Instrument = nativeId, baseAsset, quoteAsset, priceIncrement, sizeIncrement,
             availability, supportedIntervals, providerMetadata
Candle = startUnixSec, open, high, low, close, baseVolume,
         closed, quality, sourceRevision
Quality = authoritative | live | provisional | reconciling
FeedHealth = connection state + lastMessageAt + lastTradeAt + error reason
```

Preserve exchange decimal strings/fixed-point or decimal values during parsing and trade
aggregation; convert to finite JavaScript numbers for rendering at a checked boundary.
Do not round all assets to two decimals. Trade IDs are strings to avoid JS integer loss.
UTC bucket starts are integer seconds; exchange timestamps may have another precision and
must be normalized in the adapter. Use half-open logical ranges [start, end).

## Suggested bound methods

```text
LoadWorkspace() -> workspace + persistedRevision + recovery/status
SaveWorkspace(expectedRevision, clientSessionId, editSequence, workspace)
  -> committedRevision + acceptedEditSequence
ListInstruments(exchange) -> instruments + fetchedAt + cacheStatus
GetHistory(requestId, feedKey, fromSec, toSec, maxBars)
  -> requestId + feedKey + candles + completeness + moreHistory
AttachChart(chartId, generation, feedKey, requestedRange)
  -> leaseId + feedRevision + bounded snapshot + health
DetachChart(leaseId) -> acknowledgment             # idempotent
GetFeedSnapshot(leaseId, afterRevision) -> snapshot + revision + health
ExportWorkspace() / ImportWorkspace()             # use native file dialogs
GetDiagnostics() -> bounded diagnostics
```

Imports use schema validation plus semantic checks. API payload names must be documented
and tested. Do not allow an arbitrary imported URL to become a backend request target.
Public exchange endpoints belong to trusted adapter configuration, not workspace JSON.

## Events and ordering

Use a single central frontend bridge listener for `market:update` and a typed event such as:

```text
leaseId, chartId, generation, feedKey, revision,
kind=(bars|health|resyncRequired), bars?, health?
```

Register the listener before AttachChart. Buffer events for the requested chart/generation
while waiting for the attach result. Apply its snapshot at revision R, then only buffered
updates with revision > R in order. Gaps force resnapshot rather than guessing. Buffer
bounds and attach failure cleanup are required. Shared feeds can fan out to multiple leases;
duplicate deliveries to one lease must be harmless.

Do not have every chart call a global `EventsOff` that removes other charts' handlers.
Use the individual cancellation function or a centrally reference-counted listener. Wails
runtime events are transport, not durable state or guaranteed replay. [S03]

History is merged into the backend/feed state with revisions; independent HTTP response
arrival order must not cause older snapshots to overwrite fresher current bars. For a symbol
change, clear the old data only for that chart, bump generation, and ignore late old results.

## Error contract

Return typed user-safe errors: offline, timeout, rateLimited with retryAt, restricted,
unknownInstrument, unsupportedInterval, invalidWorkspace, saveFailed, conflict, internal.
Do not treat HTTP 200 alone as success; exchange payload-level codes also matter.
Keep detailed diagnostics in redacted logs. An exchange failure does not invalidate saved
layout or require restarting all other adapters.

# Candle correctness and handover rules

These rules are project design requirements. Provider field mappings are in EXCHANGES.md.
The supplied fixtures are synthetic regression cases, not recorded exchange responses.

## Normalized invariant

One series contains at most one candle per integer UTC bucket start, sorted ascending.
For interval duration D seconds: bucket = floor(tradeUnixSec / D) * D, covering [bucket,bucket+D).
Daily buckets start 00:00 UTC. Time display formatting does not alter bucket identity.
Reject NaN/infinity, nonpositive prices for these spot charts, negative volume, and invalid
OHLC relations (`low <= min(open, close) <= max(open, close) <= high`).

Use exact decimal/fixed-point values for aggregation and provider parsing when feasible,
especially volume sums. Chart API numeric conversion must check range and finiteness.
Duplicate timestamps, milliseconds passed as seconds, and newest-first history must be caught
before rendering. The chart library expects a correctly ordered dataset. [S04, S05]

## Native cumulative candle streams

An update describes the current whole candle, not an additional trade. Two updates with
volumes 3 then 5 mean volume 5, not 8. Replace the candle by key after validating source
ordering. A repeated update is idempotent. A close flag is sticky against an older update;
a documented later correction may replace a closed candle. Do not use `max(volume)` as a
universal reconciliation rule: an authoritative correction can reduce an earlier bad value.

Exchange-specific event timestamps/sequence numbers determine comparable source order.
A monotonically increasing local revision determines frontend application order. Neither is
an invented global exchange sequence; do not compare unlike streams' counters.

## Trade-derived candles

Deduplicate with provider + native product + trade ID, with bounded retention covering the
replay/correction horizon. Bucket by event time. Open/close follow first/last trade in source
chronological order, not network arrival order. Keep enough provenance for late trades to
repair open/close/high/low/volume. Use a documented deterministic tie-break for equal times.
Do not add buy and sell records twice when a provider message represents both sides of one
trade. Ignore initial replay snapshots already accounted for by the deduplication state.

New buckets should exist only when provider data/trades justify them. A minute with no trade
is not automatically an OHLC candle with invented volume. Preserve provider conventions and
explain gaps. A synthetic carry-forward display, if added later, must be labelled synthetic.

## Initial attach and reconnect

Start the live stream and buffer bounded events before requesting history. Fetch overlapping
REST candles, normalize them, merge by bucket, then apply events with source-aware ordering.
Keep the current bar provisional when the REST snapshot cutover is ambiguous. Never assume
HTTP response time tells you which trades the response included.

For native streams, cumulative updates replace open-bar state; older buffered events must
not overwrite a fresher REST snapshot. Where no shared watermark exists, use current feed
snapshots and budgeted reconciliation rather than claiming perfect atomicity.

For Coinbase trade aggregation, follow the provisional-volume rule in EXCHANGES.md. Fully
observed future buckets can be computed directly. Incomplete initial/reconnected buckets
stay provisional until repaired. Surface this instead of quietly double-counting volume.

Reconnects and suspend/resume are explicit discontinuities. Buffer incoming live events,
fetch missing plus overlap intervals, merge, and announce a new consistent revision. If a
buffer overflows, mark affected ranges incomplete and schedule repair. Never silently lose
trades while claiming completeness. Do not discard old cached history when only the latest
few bars need reconciliation.

## Frontend updates and historical pagination

Batch by feed and bucket key. A 100 ms flush crossing a boundary must include the last update
for the old bucket AND the first update for the new bucket. Repeated open-bar updates can be
coalesced, but distinct bucket updates cannot be dropped just because only one is latest.
Use incremental update for the last/new bar. An older correction must use a supported historic
update API in the pinned version or a controlled series replacement preserving viewport.

When loading older history, protect the user's visible window from shifting because indices
changed. Save time anchors and any right-edge offset/spacing needed; restore after merge.
Historical mode never auto-scrolls on new live data. Return to live explicitly enables follow.
Cached candle eviction must not invalidate the viewport silently; fetch the selected range.

## Specific regression cases

- Reversed historical page plus an overlapping duplicate timestamp.
- Native current bar volume 3, then 5, then repeated 5, then stale 3; final volume remains 5.
- Two bars in one frontend batch, neither lost.
- Duplicate trade ID; out-of-order trade; late trade changes a previous candle's close/low.
- Exact-boundary trade enters the next bucket; 1h/4h/1d mapping respects UTC.
- Mid-bucket REST/WS overlap cannot claim volume `REST + all buffered trades`.
- Reconnect with two missing buckets; recovery has no duplicate starts or invented bars.
- Symbol changed while a history response was in flight; old response is ignored.
- Sequence gap and trade-buffer overflow produce visible provisional/resync states.

Implement the fixtures as tests of production normalization/aggregation functions, not a
second algorithm that is used only by tests.

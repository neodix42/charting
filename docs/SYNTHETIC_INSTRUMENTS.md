# Dukascopy Synthetic Instruments

The Dukascopy catalog overlays one **Synthetic Instruments** category. Its canonical
identities are `btcusd`, `btcxau`, `xauspx`, `ethbtc`, and `xauxag`; existing BTCUSD and
XAUXAG identities and files are reused. Chart identifiers use the same uppercase
symbols. The S&P leg is Dukascopy's **USA500IDXUSD index**, never SPY or another ETF.

New selections in this category offer D1 and W1. Existing native BTCUSD/XAUXAG
intraday downloads and saved workspaces remain readable. Other Dukascopy categories
retain their existing intervals.

## Prices and accuracy

BTCUSD uses native Dukascopy BID history. BTCXAU is BTCUSD/XAUUSD, XAUSPX is
XAUUSD/USA500IDXUSD, ETHBTC is ETHUSD/BTCUSD, and XAUXAG falls back to XAUUSD/XAGUSD
where native XAUXAG history is unavailable. The pinned provider's native XAUXAG tick
history starts at 2024-09-17 09:39:43.256 UTC. A range spanning that instant can have
calculated and native observations in the same daily candle. Provenance records the
native/calculated segments and the pricing convention; the transition is not hidden.
Provider authentication, transport, rate-limit, or decode failures fail the job and
preserve published data. They do not authorize synthetic fallback.

Calculated history merges chronologically sorted BID ticks at **identical millisecond
timestamps**. All updates on both legs at that timestamp are consumed before a ratio
is observed; the final duplicate update on each leg wins. Open is the first valid
ratio, high/low are the observed maximum/minimum, and close is the last ratio. Invalid,
unordered, nonpositive or nonfinite source prices fail rather than fabricate prices.
No independent source OHLC extrema are divided. D1 boundaries are UTC midnight; W1
uses the existing D1 aggregation with Monday 00:00 UTC boundaries.

**Calculated OHLC is approximate.** Exact timestamp intersection can omit valid
asynchronous observations and their extrema, even though its inputs are ticks. The
resolution is recorded as `simultaneous millisecond BID ticks`, with zero allowed
quote age and `exact-timestamp-no-carry-v1` session policy. This conservative policy
carries no quote across a market closure, holiday, weekend, missing quote or session
boundary. Missing observations remain gaps. A common historical session calendar is
not inferred from today's trading hours. The calculation module permits a nonzero
quote age only with an explicit verified common-session token policy; production does
not enable that mode.

Synthetic ratio volume is unavailable. Its CSV has no `volume` column; chart transport
uses `volumeUnavailable` and hides volume. A mixed native/calculated dataset also
omits volume throughout. Volume-dependent indicators are rejected, while price-based
indicators retain the existing recipes, warm-up and refresh behavior.

## Download, reuse and recovery

The normal Downloader queue owns synthetic jobs, cancellation, resume, concurrency
and progress. W1 queues a D1 prerequisite. With a configured JForex connection,
the existing authenticated SDK exports the required underlying ticks; BTCXAU requests
BTCUSD and XAUUSD, never a nonexistent native BTCXAU instrument. Native BTCUSD and
the available native XAUXAG segment use the SDK's BID daily history. Without a
configured connection the existing public Dukascopy downloader remains available.
SDK failures do not silently switch price sources.

If a successfully validated SDK daily response contains no BTCUSD candles, the
Downloader builds that native instrument's daily candles from complete UTC-hour
BTCUSD BID ticks. Cached quote hours are reused first. Open/high/low/close come
from actual observations of BTCUSD itself; no ratio, copied price or traded volume
is introduced. The tick-derived origin is recorded, complete days are checkpointed,
and empty tick days remain gaps. Authentication, transport, invalid-data and other
errors never authorize this empty-history fallback.

The Go owner schedules both paths through the existing shared FIFO limiter and
concurrency controls. A public HTTP access-denial cooldown does not block a separate
authenticated SDK request; shared rate-limit deadlines still apply. Credentials stay
in the existing Go/Java connection, never in the Node worker or its messages. The
batch freezes its history source and account identity; a changed account stops
publication, including when all required ticks were already cached.

Complete UTC-hour BID chunks are privately cached in `synthetic-quotes-v1` beside
the provider session cache. SDK chunks live in a separate account/source namespace;
public CSVs cannot establish SDK coverage. Chunks and manifests are checksummed and
atomically published under per-hour cross-process locks. Live incomplete hours are
not published. Decoding is bounded to one hour, one million rows and 64 MiB; daily
output is streamed. A cancelled SDK export closes its file before releasing its
reservation. Completed hours survive retry; a timed-out hour has one bounded retry.
Native daily downloads likewise have a separate bounded SDK loading deadline and
one retry for a typed loading timeout; ordinary chart deadlines remain unchanged.
SDK history shares the bridge's bounded history workers and account candle cache.
Slow bulk loads can delay a concurrent native chart backfill; its existing timeout
and Retry remain visible. Live quote, control and trading callbacks use separate
paths. Large first-time tick histories still require substantial download time.

Ratio requests are clipped to the later exact tick-availability instant of both
legs; dates before any possible common observation require no source fetch. The
original requested range remains the queue/checkpoint identity.

Compatible published underlying tick CSVs are reused when their saved verified
coverage ledger and checksum prove the requested hours. A filename or a few rows do
not establish complete coverage. Disjoint coverage is skipped before hashing or
streaming irrelevant files. Unverified data cannot suppress a provider request.
Incremental extension/refresh reuses completed hourly chunks and existing candle
coverage. It does not redownload full history on each alert check. Daily checkpoints
retain committed output and the frozen closed-bar cutoff for resume. Generated CSVs
use the existing history transaction/publication and enrichment-replay mechanisms;
published originals survive cancellation or failure.

SHA-bound metadata stores native, synchronized BID ratio, or mixed provenance,
observation resolution, pricing and unavailable-volume policy. Calculated data without
valid provenance cannot be published or accepted by the synthetic chart reader.
The `historySource` field distinguishes public downloader, JForex SDK and mixed
source histories. Extending an existing public dataset can produce explicitly mixed
provenance; existing published rows and enrichment recipes are preserved. Switching
an unfinished public download to SDK first preserves its old partial and checkpoint
in a private archive rather than reinterpreting those rows as SDK observations.
An older native XAUXAG partial spanning the pre-native period cannot be resumed as
a calculated dataset: its files stay intact and the error asks for **Download again
instead of Resume**, creating a fresh job with the same selected dates.
SDK-downloaded native price histories also omit volume: SDK quote-volume units are
not silently combined with public export volume. This does not change native chart
volume supplied directly by JForex.

## Analysis, alerts, charts and execution

Enrichment, Historical Analysis, Screener and Live Alerts use the canonical files,
including enriched price columns. Alerts refresh closed history through the normal
incremental pipeline before evaluating new signals; failures or incomplete refreshes
cannot supply a fresh signal. Scheduled and manual synthetic checks both require the
expected latest closed bar; a successfully verified gap does not authorize a stale
alert. Existing scheduling, persisted alert IDs, chart snapshots
and Telegram delivery remain in use. Synthetic simulations, outcomes and notifications
identify ratio returns as analytical; they do not model executable positions in the
underlying instruments.

Calculated workspace charts read downloaded D1/W1 history. Download the instrument
in Screener first. Only an active chart polls published files (every 30 seconds);
opening a chart does not initiate a multi-year tick download. The chart identifies
approximate downloaded closed candles and does not claim a live broker connection.
Drawings, existing price indicators, navigation and markers use the normal chart
infrastructure. Native instruments retain their SDK history/stream when available;
retained downloaded fallback is clearly identified. Mixed historical bars retain
per-bar synthetic/approximate flags, including the first partial native week.

BTCXAU, XAUSPX and ETHBTC have no direct order placement, broker margin or executable
quantity calculation. Guards exist in both the desktop facade and JForex request
validation. Native BTCUSD/XAUXAG trading remains available only when the account's
native SDK catalog actually offers the instrument.

## Runtime delivery and verification

`synthetic-ratio.cjs`, `synthetic-quotes.cjs`, `synthetic-worker.cjs` and `sdk-worker.cjs` are required
development/production runtime modules, copied by the common Linux/Windows bundler
and included in the runtime integrity manifest. Offline runtime probes use the pinned
public provider API and an explicit test transport, including the owner-pipe SDK
export with public HTTP access denied. They do not use account credentials or modify
user histories. The separate opt-in `sdkhistoryintegration` test copies connection
settings into a private temporary profile, downloads real underlying ticks and checks
saved BTCXAU/BTCUSD history without orders or changes to the original profile. See
STATUS.md for the checks actually completed and outstanding desktop/live checks.

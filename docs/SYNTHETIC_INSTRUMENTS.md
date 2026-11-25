# Dukascopy Synthetic Instruments

The Dukascopy catalog overlays one **Synthetic Instruments** category. Its canonical
identities are `btcusd`, `btcxau`, `xauspx`, `ethbtc`, and `xauxag`; existing BTCUSD and
XAUXAG identities and files are reused. Chart identifiers use the same uppercase
symbols. The S&P leg is Dukascopy's **USA500IDXUSD index**, never SPY or another ETF.

| Symbol | Exact display name | Price series |
|---|---|---|
| BTCUSD | Bitcoin / US Dollar | Native BTCUSD, with same-asset fallback on validated empty D1 |
| BTCXAU | Bitcoin / Gold | BTCUSD / XAUUSD |
| XAUSPX | Gold / S&P 500 Index | XAUUSD / USA500IDXUSD |
| ETHBTC | Ethereum / Bitcoin | ETHUSD / BTCUSD |
| XAUXAG | Gold / Silver | Native XAUXAG where available, XAUUSD / XAGUSD elsewhere |

New selections in this category offer D1 and W1. Existing native BTCUSD/XAUXAG
intraday downloads and saved workspaces remain readable. Other Dukascopy categories
retain their existing intervals.

## Prices and accuracy

BTCUSD uses native Dukascopy BID history. BTCXAU is BTCUSD/XAUUSD, XAUSPX is
XAUUSD/USA500IDXUSD, ETHBTC is ETHUSD/BTCUSD, and XAUXAG falls back to XAUUSD/XAGUSD
where native XAUXAG history is unavailable. The pinned provider's native XAUXAG tick
history starts at 2024-09-17 09:39:43.256 UTC. A tick-mode range spanning that instant can have
calculated and native observations in the same daily candle. Provenance records the
native/calculated segments and the pricing convention; the transition is not hidden.
Provider authentication, transport, rate-limit, or decode failures fail the job and
preserve published data. They do not authorize synthetic fallback.

The Downloader offers two observation modes. **Fast — hourly observations**
(`h1-close`) is the default for new UI downloads; **Tick observations — slow**
(`ticks`) preserves the earlier method. The source is an input-resolution choice,
not an output timeframe: calculated series still expose only D1/W1. A configured
JForex connection is required for Fast. An omitted legacy API request or saved job
mode means `ticks`; the saved UI view defaults to Fast and sends it explicitly.

Fast reads genuine, closed H1 BID candles. Each leg's hour must be open for its
**entire interval** according to the loaded, actual historical SDK READ calendar.
A pinned-SDK readiness helper distinguishes an unloaded schedule from a valid
calendar and supports explicit, loaded custom-only closure schedules. Empty or
unknown calendar state never proves 24/7 trading. A custom-only loaded READ
schedule explicitly defines absolute closures against an open baseline; dates
outside its listed closures are open. This baseline is accepted only from the
loaded, valid pinned-SDK READ object, never inferred from an empty public list. A
missing, unready, malformed or insufficiently covered calendar fails the request.
Today's regular trading hours are not substituted for historical evidence.

Only matching verified hour timestamps produce an observation: numerator close
divided by denominator close. The first and last sampled ratios become daily
open and close; their extrema become daily high and low. **All four OHLC values
are sampled approximations, including open and close.** They are not executable
prices or exact session-boundary values. Source OHLC extrema are never divided
independently. No quote is forward-filled across an absent hour, closure, holiday,
weekend or session boundary. H1 provenance records
`verified full-session H1 BID close observations`, maximum quote age 3,600,000 ms,
and session policy `verified-full-hour-close-v1`.

Tick mode merges chronologically sorted BID ticks at **identical millisecond
timestamps**. Both legs consume all updates at that instant; the final duplicate
update wins. The first/last valid ratio supplies open/close, and observed extrema
supply high/low. Its OHLC is also approximate: exact intersection omits asynchronous
observations and their extrema. Tick provenance records
`simultaneous millisecond BID ticks`, zero allowed quote age and
`exact-timestamp-no-carry-v1`; no quote is carried across missing observations.
Invalid, unordered, nonpositive or nonfinite source prices fail explicitly.

Both methods use UTC-midnight D1 boundaries and existing Monday 00:00 UTC W1
aggregation. Native/calculated and tick/hourly transitions are recorded rather
than hidden; retained historical precision is not upgraded by changing a selector.

Synthetic ratio volume is unavailable. Its CSV has no `volume` column; chart transport
uses `volumeUnavailable` and hides volume. A mixed native/calculated dataset also
omits volume throughout. Volume-dependent indicators are rejected, while price-based
indicators retain the existing recipes, warm-up and refresh behavior.

## Download, reuse and recovery

The normal Downloader queue owns synthetic jobs, cancellation, resume, concurrency
and progress. W1 queues a D1 prerequisite. With a configured JForex connection,
Fast exports the required underlying H1 candles and tick mode exports underlying
ticks; BTCXAU requests BTCUSD and XAUUSD, never a nonexistent native BTCXAU or an
unrelated ETHUSD source. Native BTCUSD and the available native XAUXAG segment use
the SDK's BID daily history first. Configured ordinary Dukascopy D1 downloads also
use authenticated SDK history. Without a configured connection, tick mode can use
the existing public downloader. SDK errors never silently switch price sources.

Fast XAUXAG uses native daily candles from the provider's native **daily**
availability, independently of the later first-tick timestamp within that date.
Each native day is preferred when returned; only a successfully decoded empty
native day permits the H1 ratio fallback. Transport and data errors remain errors.

If a successfully validated SDK daily response contains no BTCUSD candles, Fast
aggregates **BTCUSD's own H1 OHLC** into daily bars, preserving the native hourly
open, high, low and close. This same-asset path does not divide ratios or start a
hidden tick fallback. Tick mode instead aggregates complete UTC-hour BTCUSD BID
ticks and reuses cached chunks. Provenance distinguishes the fallback source;
complete days are checkpointed and empty source days remain gaps. Authentication,
transport, invalid-data and other failures never authorize empty-history fallback.
For native hourly fallback, an empty day requires historical READ closure intervals
covering the entire day or a verified complete past-month BID source file proving
there were no usable observations that day. A partly open, open or unverified empty
source response cannot establish absence. Ratio sampling separately requires
complete open hours. Cold calendar initialization may load one bounded source day,
but no observation is accepted until the actual historical READ proof succeeds.

Each job freezes its observation mode. Existing jobs without a mode remain ticks
through restart and Resume, independently of the new saved UI preference. To
switch unfinished work, cancel it and start a new Fast download. Cancellation
retains published histories; existing covered ranges and indicator recipes are
reused rather than replaced. Extending an older tick dataset in Fast mode can
produce mixed observation precision, which remains explicit in provenance.

The Go owner schedules both paths through the existing shared FIFO limiter and
concurrency controls. A public HTTP access-denial cooldown does not block a separate
authenticated SDK request; shared rate-limit deadlines still apply. Credentials stay
in the existing Go/Java connection, never in the Node worker or its messages. The
batch freezes its history source and account identity; a changed account stops
publication, including when all required observations were already cached.

Fast H1 exports batch at most **28 UTC days / 672 source bars** per request, through
the shared SDK request pacer. Within an export, SDK `getBars` reads are limited
to one UTC source day using `NO_FILTER`: the pinned SDK's multi-day path can
silently return empty spans or carry a previous close across unloaded days.
Ambiguous zero-volume flat shapes are excluded; genuine zero-volume non-flat
observations remain eligible. Actual historical calendar proof precedes accepting
and caching ratio observations. Unexpected empty days first use the SDK's independent
date-range loader, requiring finite loaded first-H1 metadata, exact full-range
completion and unchanged connection/account. That loader can still reuse a falsely
empty monthly SDK cache: a failed historical download can be stored as a successful
empty month. If no observed rows are recovered, the exporter requests the actual
monthly BID file directly, with strict UTC-month bounds and ordering. Its selected
BID candles receive SDK split adjustments followed by the account's feed commission,
matching the price convention of normal SDK history. A null monthly-file result
permits one retry of that same file after five seconds, within the original
120-second work deadline and with unchanged source/account checks. Empty, corrupt
and changed-source responses do not receive this retry. An unresolved null archive
remains a visible protected error and never certifies empty coverage. Null, empty,
malformed or entirely ambiguous source files remain failures. A validated nonempty
complete past-month file can establish a genuine no-quote day within that month;
current-month absence is not inferred this way. The absolute 120-second export
deadline and source fences cover every path. Unknown or MAX availability cannot
establish absent history; one instrument-scoped RAM reset/bootstrap is allowed
within that deadline. Partial, failed or cancelled reads cannot certify coverage.
Genuine absent observations remain gaps, never carried candles.

Validated complete days are checksummed and cached under `synthetic-hourly-v1`,
separately by account/source and symbol. The cache directory/file format remains
version 1, while source policies and their cache identities are now
`verified-full-hour-v2` for ratio inputs and `provider-native-h1-v2` for native
hourly fallback. Exact-identity, checksum-valid **nonempty** legacy days are reused
without rewriting their manifests or CSVs. Legacy empty days are refetched in
consecutive missing/legacy-empty batches of at most 28 days. A batch stops before
every compatible cached day, including a positive legacy day, so reloading a valid
neighbor cannot become a new provider dependency. It publishes only the requested
missing/legacy-empty days. Failed or cancelled recovery preserves old cache files; completed
new manifests are published atomically. Published user histories are not blindly
rebuilt or discarded by this cache upgrade. Daily caches and output checkpoints
survive Resume; incomplete days do not certify coverage. The API's wider row limit
does not change the worker's 28-day batch bound.

Shared hourly ownership uses an atomically published PID/nonce document, written
and synced privately before rename. Peers wait through initialization rather than
parsing a partially written JSON file. A dead-owner claim is rechecked against
the same directory and owner generation before retirement; the old files are
retained. Sibling reaper locks use the same protocol, so a crashed recovery owner
can itself be recovered within the shared deadline. Live, unknown or malformed
ownership is never stolen because it is old, and a stale release cannot remove a
successor's lock. Cancellation preserves committed quote and candle caches.

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

Tick ratio requests are clipped to the later exact tick-availability instant of both
legs; dates before any possible common observation require no source fetch. The
original requested range remains the queue/checkpoint identity.

Compatible published underlying tick CSVs are reused when their saved verified
coverage ledger and checksum prove the requested hours. A filename or a few rows do
not establish complete coverage. Disjoint coverage is skipped before hashing or
streaming irrelevant files. Unverified data cannot suppress a provider request.
Incremental extension/refresh reuses completed source chunks/days and existing candle
coverage. It does not redownload full history on each alert check. Daily checkpoints
retain committed output and the frozen closed-bar cutoff for resume. Generated CSVs
use the existing history transaction/publication and enrichment-replay mechanisms;
published originals survive cancellation or failure.

SHA-bound metadata stores native, synchronized BID ratio, or mixed provenance,
observation resolution, pricing and unavailable-volume policy. Calculated data without
valid provenance cannot be published or accepted by the synthetic chart reader.
The `historySource` field distinguishes public downloader, JForex SDK and mixed
source histories. `syntheticSource` retains the requested `h1-close`/`ticks` mode even
when native daily candles suffice, so a future empty-native fallback keeps that
preference. Actual observation resolution remains independently disclosed. Extending an existing public dataset can produce explicitly mixed
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
opening a chart does not initiate a multi-year history download. The chart identifies
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

`synthetic-ratio.cjs`, `synthetic-quotes.cjs`, `synthetic-hourly.cjs`,
`synthetic-worker.cjs` and `sdk-worker.cjs` are required
development/production runtime modules, copied by the common Linux/Windows bundler
and included in the runtime integrity manifest. Offline runtime probes use the pinned
public provider API and an explicit test transport, including the owner-pipe SDK
export with public HTTP access denied. They do not use account credentials or modify
user histories. The separate opt-in `sdkhistoryintegration` test copies connection
settings into a private temporary profile, reads real underlying history and checks
saved calculated/native history without orders or changes to the original profile. See
STATUS.md for the checks actually completed and outstanding desktop/live checks.

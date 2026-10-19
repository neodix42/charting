# CryptoDesk bridge v1

The local Wails v2 binding is generated from exported methods on `main.App`. The frontend's typed `bridge/api.ts` is an adapter over these generated browser globals; no HTTP server is used in the desktop release.

`LoadWorkspace()` returns `{workspace,revision,recovery}`. A nonempty recovery reason prevents mounting the workspace or saving defaults. `SaveWorkspace(expectedRevision,sessionId,editSequence,workspace)` validates the schema and semantics and returns `{revision,acceptedEditSequence}` after a FULL SQLite commit. Native window state is captured at commit.

`ListInstruments(exchange)` returns the public spot catalog. `AttachChart(chartId,generation,feedKey,fromSec,toSec)` returns `{leaseId,revision,bars,health}` immediately from the cache, and starts shared provider streams/history asynchronously. `DetachChart(leaseId)` is idempotent. `GetFeedSnapshot(leaseId,afterRevision)` provides a complete bounded snapshot; afterRevision is informational and snapshots can always repair a gap. `GetHistory(requestId,feedKey,fromSec,toSec,maxBars)` returns `{requestId,feedKey,bars,moreHistory}` with ascending UTC seconds. Limits are 1,000/request (Coinbase 350), 10,000/feed cache and 250,000 total cached candles.

Listen to `market:update` before attaching. Each update contains `leaseId,chartId,generation,feedKey,revision,kind,bars,health`. Apply the attach snapshot at revision R, then only newer buffered events. Revision gaps require a new snapshot. Events are revision-serialized; stale chart generations cannot apply to another instrument. Queue bounds force reconciliation.

`feedKey` is `{exchange,market:"spot",instrument,timeframe}`; `bars` contain `{time,open,high,low,close,volume,closed,quality,sourceRevision}`. `health` is `{state,reason,lastMessageAt,lastTradeAt}`, timestamps in seconds. Imported JSON never controls request URLs. Errors are safe readable strings prefixed with their classification where applicable.

Timeframes include `1w` (UTC Monday boundaries). Chart configuration optionally carries `indicators:[{id,type,parameters,color?}]`; the backend validates it with the workspace and persists it without opening additional feeds. Frontend calculations/rendering use the existing chart's candles. Weekly Coinbase history aggregates genuine daily candles rather than requesting an unsupported weekly granularity.

Import/export use native file dialogs; import validation and confirmation precede a transaction with a bounded backup. Import/reset/recovery return a LoadWorkspace-shaped result. `GetBackups` lists safe recovery choices. `OpenTradingView` opens only the fixed HTTPS creator URL.

A `prepare-close` event includes a one-use token. Frontend captures viewports, awaits autosave and calls `ConfirmClose(token,false)`; a failed save keeps the app open and supports retry or explicit discard. A ten-second timeout is failure, never success. `window:state` reports sampled changes in normal size/maximized state. Diagnostics describe actual adapter sockets, shared leases, queues, cache counts, revisions and frontend chart instances.

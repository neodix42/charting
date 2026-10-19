# Public exchange adapters

Reference observations checked 2026-10-19; revalidate against SOURCES.md before coding.
These are implementation notes, not live connectivity results. Use production public spot
data only. Never require keys for the standard workflow. Use documented endpoint families
and show regional/network restrictions honestly. Do not mix venue products or private APIs.

## Binance Spot

REST base: `https://api.binance.com`.
- Instruments: `GET /api/v3/exchangeInfo`; inspect trading status and price/size filters.
- History: `GET /api/v3/klines`; use native symbol, interval, startTime/endTime in milliseconds,
  and a limit respecting the current documented maximum (1,000 at preparation).
  Klines are arrays; map open time, OHLC, base volume and close time by documented positions.
  Reference: [S07, S08].

WebSocket: `wss://stream.binance.com:9443/ws` (documented port 443 also exists).
Subscribe to lowercase `<symbol>@kline_<interval>` using the documented SUBSCRIBE envelope.
Support raw/combined wrappers. Use candle `k.t` for bucket time, `k.v` for cumulative base
volume, `k.x` for closure. All six required intervals have native streams. The notes currently
specify 24-hour connection expiry, ping/pong, at most 1,024 streams per connection, and a
5-per-second incoming control-frame/message limit that includes ping/pong. Design below
those limits, not at their edge. [S09]

Implementation: multiplex by provider, replace cumulative bar values rather than adding
volume, rotate/reconnect without a resubscription storm, and repair gaps through REST.

## Bybit Spot

REST base: `https://api.bybit.com`.
- Instruments: `GET /v5/market/instruments-info?category=spot`; follow actual spot response
  rules rather than assuming derivatives pagination. Preserve tick sizes/status. [S10]
- History: `GET /v5/market/kline?category=spot&symbol=...&interval=...`.
  ALWAYS send `category=spot`; omission can select another market. Responses are reverse
  ordered by startTime; normalize to ascending. Current limit maximum is 1,000. For spot,
  volume represents base units and turnover quote units. [S11]

WebSocket: `wss://stream.bybit.com/v5/public/spot`.
Subscribe to `kline.<interval>.<symbol>`. Candle `start` is milliseconds and `confirm` signals
closure. Fields are cumulative candle state; do not add repeated volumes. Check subscription
acknowledgments as well as payloads. Public channels do not require authentication. Implement
the documented heartbeat and argument/connection limits, not Binance's policy. [S12, S13]

## Coinbase Advanced Trade public spot

REST base: `https://api.coinbase.com`.
- Products: `GET /api/v3/brokerage/market/products`; use the current pagination/filter fields
  and select spot products. Native IDs are typically hyphenated, e.g. BTC-USD. [S14, S15]
- Candles: `GET /api/v3/brokerage/market/products/{product_id}/candles` with Unix-second
  `start`, `end`, a named `granularity`, and maximum 350 buckets per request as currently
  documented. All required historical intervals are listed. [S16]

Public WebSocket: `wss://advanced-trade-ws.coinbase.com`.
No JWT is required for this public host. Subscribe to heartbeats as well as relevant market
channels. The `candles` channel supplies FIVE-MINUTE buckets, not arbitrary requested
intervals; `market_trades` carries trades suitable for aggregation. Current docs also warn
that most `-USDC` product IDs return matching `-USD` data on public channels. Do not silently
present such aliases as an independent USDC market: verify mapping and label or reject it.
Consult linked channel schemas/AsyncAPI for current envelopes and sequence scope. [S17]

Implementation policy for Coinbase:
1. Use native 5m candle snapshots where practical. Use shared public trade subscriptions
   to produce other live intervals; REST provides historical and reconciliation candles.
2. Derive bucket OHLC from trade timestamps, not receipt time. Deduplicate trade IDs, support
   batching/out-of-order events, and use documented per-channel sequence-gap rules.
3. Subscribe/buffer first, then fetch history. An OHLCV REST response does not establish an
   exact last-included trade ID. Therefore NEVER seed its volume and add all buffered trades.
4. A partially observed current bucket is provisional. Keep last verified REST volume or a
   separately labelled observed-only volume; do not claim a complete total. Fresh prices can
   update the visible provisional close/high/low. Periodic budgeted REST refresh replaces the
   uncertain snapshot. Only a fully observed uninterrupted bucket can be aggregated directly
   as complete; closed buckets are checked against REST after settlement delay.
5. After interruption, repair the affected buckets through REST. Do not require observing an
   entire historical day of trades to reconstruct a daily candle. A complete trade backfill
   optimization is allowed only if the chosen documented API actually supports it.

These five steps are this project's correctness policy, not a guarantee from Coinbase that
its REST snapshots and streaming trades share a transaction watermark.

## Required interval mapping

| UI | Seconds | Binance | Bybit | Coinbase REST |
|---|---:|---|---|---|
| 1m | 60 | 1m | 1 | ONE_MINUTE |
| 5m | 300 | 5m | 5 | FIVE_MINUTE |
| 15m | 900 | 15m | 15 | FIFTEEN_MINUTE |
| 1h | 3600 | 1h | 60 | ONE_HOUR |
| 4h | 14400 | 4h | 240 | FOUR_HOUR |
| 1d | 86400 | 1d | D | ONE_DAY |

Mappings derive from [S08, S11, S12, S16]. All buckets use UTC boundaries. A chart's display
timezone must not change upstream UTC buckets or reinterpret daily candles.

## Shared adapter obligations

Sort/deduplicate historical responses, enforce progress on pagination, use bounded requests,
cache instrument metadata, and validate finite OHLCV values. Native candles may include an
unfinished current bucket. Avoid marking it closed solely because it came from REST.
Inspect exchange error bodies; a successful HTTP code can still carry an API failure.
Handle 429/Retry-After and rate headers per provider; back off on 403/451 and expose the
restriction instead of looping. Retries must be bounded and cancellable.

No silent provider failover. Binance BTCUSDT, Bybit BTCUSDT, and Coinbase BTC-USD are separate
markets. Removing an instrument from discovery must not delete its saved chart configuration.
Provide a clear reselect/retry path while retaining the original instrument in the workspace.

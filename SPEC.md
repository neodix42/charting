# Product specification

## Objective and capacity

CryptoDesk is a personal, read-only Ubuntu desktop terminal for cryptocurrency spot charts.
The user wants many charts without depending on TradingView's hosted application or account.

**FIRST LAUNCH: exactly 1 tab containing exactly 1 chart in a 1×1 grid.**

**Upper limits: 20 tabs total and 9 charts per tab.** The valid workspace has 1–20 tabs;
each tab has 1–9 actual charts. Users add charts/tabs incrementally. Do not pre-create
20 tabs, 9 charts, or hidden chart configurations. Do not auto-load the capacity example.
A new tab also starts with exactly one chart. Later launches restore the actual saved
workspace, even when it contains fewer than the maxima. Enforce the limits in both UI
and backend/import validation. At capacity, disable creation/duplication with a clear
explanation rather than deleting an existing chart/tab or silently exceeding the caps.

A workspace can therefore contain 180 chart configurations. This is not a requirement to
render all 180 canvases or maintain all 180 feeds simultaneously. Only the selected tab
is rendered in the default efficient mode.

## Required functionality

### Workspace and tabs

Create, rename, duplicate, reorder, select, and close tabs. Use stable identifiers, not array
positions, for persistence and chart ownership. Tab duplication deep-copies configuration and
assigns new IDs. Reordering preserves chart state. Closing the last remaining tab is disabled
with a clear explanation; removing its last chart is also disabled. An explicit, confirmed
Reset Workspace command returns to one tab and one chart. Prompt before closing a populated tab, or provide
a reliable undo action; default to confirmation. Do not prompt on ordinary application exit
when autosave succeeded.

The tab bar must remain usable with all 20 tabs: horizontal scrolling, visible selected tab,
keyboard navigation, and a menu listing all tab names. Persist tab order and selection.

### Chart grid

Support grid presets named as **rows × columns**: 1×1, 1×2, 2×1, 2×2, 2×3, 3×2, and 3×3.
A tab may contain fewer charts than grid cells; show an Add Chart control in an empty cell.
Selecting a larger grid MUST NOT create more charts: empty cells are placeholders only.
Add Chart creates exactly one chart; Add Tab creates exactly one tab with one chart.
Persist chart order and proportional row/column track sizes. Support splitter dragging,
chart reordering, and temporarily maximizing a chart with a restore action.

Never lose charts when reducing grid capacity. Reject the change with a clear message until
the user moves/removes excess charts; do not silently discard, park, or hide them. Adding a
chart may expand to the smallest suitable preset while retaining existing configurations.
At nine charts, disable Add Chart.

### Per-chart selection and rendering

Each chart has an independent exchange, market type, instrument ID, and timeframe.
Different exchanges and instruments can appear on the same tab. V1 market type is `spot`.

Support Binance Spot, Bybit Spot, and Coinbase public spot-market data. Discover searchable
instrument lists dynamically. The default starter selection is Binance BTCUSDT at 1h when
available. If unavailable, display an error and selector; do not secretly switch providers.

Required intervals: 1m, 5m, 15m, 1h, 4h, 1d. Native and correctly derived intervals are both
acceptable. Clearly distinguish an unavailable instrument, a temporarily unreachable feed,
and a permanently unsupported interval. Do not label a five-minute feed as another interval.

Provide candlesticks, optional volume, crosshair OHLCV readout, instrument-specific price
formatting, linear/logarithmic price scale, zoom, pan, and Return to live. A line chart view
is a useful low-cost addition, but optional. Indicators, drawing tools, Pine Script, and
trade execution are out of scope.

Paginate history when the user scrolls left. Preserve the visible time range while prepending
bars. Respect listing dates and available history. Do not imply exchange history is infinite.
Use UTC candle boundaries; display timezone is a separate saved setting.

### Market-data truthfulness

Normal application operation must use real adapters. Provide explicit per-chart statuses for
loading, live, provisional/reconciling, paused, stale, disconnected, rate-limited, restricted,
unavailable, and failed. Compact UI may group states but must retain their distinct reasons.
Show last-data freshness and Retry where useful. Do not display a new fabricated candle just
because the wall clock crossed a boundary.

No API key, exchange account, paid TradingView account, or local server installation is
required for the intended public-data workflow. Availability remains subject to each
provider's public endpoints, policies, and the user's network. Restricted access is an error,
not permission to scrape private APIs or attempt a bypass.

### Persistence

Automatically save the full workspace during use and flush on normal exit. Restore tab
names/order, selected tab, chart selections/order, grid proportions, theme, scale options,
volume settings, time viewport/follow-live mode, and normal window size/maximized state.

On restart, show the saved workspace before waiting for the internet. Load the active tab's
cache/data first. Restore a historical viewport by loading that date range; do not jump it
to the present. Persist a manual price range where the pinned public library API supports it;
choose a compatible current v5 release rather than a private-library hack. Record any
remaining platform-specific limitation explicitly.

Autosave defaults: 500 ms idle debounce, at most 2 seconds of deferral under continuous
interaction, plus explicit flush on tab switch/shutdown. Save errors must be visible.
A sudden kill can lose uncommitted edits; it must preserve the last acknowledged commit.

Provide validated JSON workspace export/import. Back up the existing workspace before an
import replacement. Never include exchange credentials or market-data caches in an export.

## Defaults selected for implementation

| Setting | Default |
|---|---|
| App name / desktop ID | CryptoDesk / `cryptodesk` |
| Interface language | English |
| Theme | Dark, with optional light theme |
| Display timezone | UTC; allow system/IANA timezone selection |
| Initial workspace | Exactly 1 tab / 1 chart / 1×1 grid; Binance BTCUSDT 1h or visible unavailable state |
| New tab | Exactly 1 chart / 1×1 grid; never clone all charts unless Duplicate Tab was selected |
| Capacity | Maximum 20 tabs total; maximum 9 actual charts per tab |
| Visible-history target | 500 bars initially; adapt pagination to each provider |
| Default normal window | 1600×1000, clamped to the available monitor |
| Minimum UI window | 1100×720; grid must remain usable with overflow/min-size handling |
| Hidden tabs | No rendered charts; unsubscribe after short grace period |
| Subscription grace | 2 seconds, reference-counted and cancellable |
| Chart update batching | Up to 100 ms coalescing; retain each distinct candle update |
| Import size limit | 5 MiB |
| Resource limits | Explicit bounded caches/queues; see ARCHITECTURE.md |

These numbers are engineering defaults, not claims about measured performance or exchange
rate limits. Adjust them only with an explanation and test evidence.

## Optional after the required app works

All-feeds-live mode for hidden tabs, chart-group symbol/time synchronization, favorites,
additional intervals, indicators, drawings, extra exchanges, derivatives, and custom keyboard
shortcuts. Keep these out of required-stage critical paths. Retain independent chart controls.

## Definition of completion

The application builds for Ubuntu, uses all three real adapters, restores the full 180-chart
fixture, limits rendering to the active tab, survives reconnect and restart tests, and has
repeatable automated tests plus a documented desktop smoke test. A browser mock or fixture-only
build is not completion. All unavailable environment checks must be distinguished from passes.

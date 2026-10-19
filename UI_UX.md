# UI and interaction contract

## First launch is small, never pre-filled

A missing workspace creates Tab 1 with ONE Binance BTCUSDT 1h chart and a 1×1 grid.
If market data cannot load, keep that one chart and show the reason and instrument selector.
Do not seed a watchlist of tabs, open nine charts, or create invisible placeholder charts.
On subsequent starts restore the saved tab/chart counts; only a genuinely missing store is
first launch. A corrupt/unreadable store is a recovery case, not permission to overwrite it.

Visible toolbar: tab bar, Add Tab, layout selector, Add Chart, settings, and save status.
Show counters as `Tabs 1/20` and `Charts 1/9`, progressing as the user adds content.

## Add, remove, duplicate

- Add Tab / Ctrl+T: create a uniquely identified tab named `Tab N`, containing exactly one
  default chart in a 1×1 grid; select it. Choose N to avoid an existing generated name.
- Add Chart / Ctrl+Shift+N: create exactly one independent chart with a new ID. Copy the
  focused chart's exchange/instrument/timeframe/style for convenience, but start in live
  viewport mode. When there is no focus, use the documented starter selection.
- Selecting a larger grid changes arrangement ONLY. Unoccupied cells contain Add Chart
  placeholders, not subscriptions, chart models, or canvases. Clicking one adds one chart.
- Duplicate Tab is a separate explicit action: deep-copy its chart settings with new IDs;
  it must respect the 20-tab cap. Never treat Add Tab as Duplicate Tab.
- Close Tab / Ctrl+W: confirm, then close the selected tab and select an adjacent tab.
  Disable closing the only remaining tab. Remove Chart similarly keeps at least one chart.
- Disable Add/Duplicate at the corresponding maximum; tooltip explains `Maximum 20 tabs`
  or `Maximum 9 charts per tab`. Backend rejects the same operation even if invoked directly.
- Reset Workspace is an explicit confirmation-protected settings action, not normal startup.

Keyboard shortcuts must not fire while typing in a search box or editing a tab name.
Provide Ctrl+Tab / Ctrl+Shift+Tab for tab switching and accessible button labels.

## Grids

Presets are rows × columns: 1×1, 1×2, 2×1, 2×2, 2×3, 3×2, 3×3.
Charts occupy cells in row-major chart-array order; unused trailing cells show placeholders.
On overflow after Add Chart, expand: 1×1 → 1×2 → 2×2 → 2×3 → 3×3, choosing the first
preset with sufficient capacity. Respect a manually selected orientation while it fits.
Equal track sizes are the default after changing dimensions. Resizing within the same
preset preserves proportions. Minimum cell sizes and overflow must protect controls.
Shrinking a grid below the current actual chart count is rejected without losing content.

Drag chart headers to reorder. Do not use draggable areas inside the chart canvas because
that conflicts with panning. Splitters resize adjacent tracks; normalized positive sizes
must sum to 1 for rows and columns. Temporarily maximizing one chart leaves the stored grid
intact. Restore returns to the prior grid. Maximization within a tab is transient in v1.

## Chart toolbar and states

Each chart independently exposes exchange, searchable native instrument, timeframe, volume
on/off, linear/log price scale, Return to live, maximize/restore, and remove.
Display native instrument plus exchange and `Spot`; never relabel USD as USDT/USDC.
OHLCV readout follows the crosshair; otherwise show the latest bar. Volume is base-asset
volume when available, with an honest unit label. Price formatting follows instrument metadata.

Loading/errors appear within the affected cell; one exchange's failure must not block other
charts or persistence. Disconnection preserves existing candles marked stale. Healthy quiet
markets are not automatically disconnected just because no new trade arrived; use heartbeat
health and data freshness separately. Derived incomplete candles show provisional state.
Keep normal network events out of disruptive modal dialogs. Show details/retry on demand.

## Restore behavior

Render the workspace shell before network data. Restore only the actual saved charts.
One saved tab with three charts must return as one tab with three charts, not as a 3×3 full
workspace. Load the active tab first. Restoring a scrolled-back chart should fetch/cache the
saved historical window and preserve its time range even while fresh data arrives elsewhere.

Dark theme is the default; UI language is English. Use a clean desktop terminal layout with
adequate spacing, clearly visible focused chart, keyboard focus rings, and compact controls
that remain usable in a 3×3 grid. Implement visible third-party attribution per the included
license notes, without suggesting TradingView endorses the app.

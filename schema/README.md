# Workspace JSON schema

`workspace.schema.json` is JSON Schema Draft 2020-12. It defines portable version 1,
not Wails-generated types or a database layout. Production validation must apply the schema
and the semantic rules below in the Go backend, not rely only on frontend controls.

Normative cardinality: tabs minItems=1/maxItems=20; charts minItems=1/maxItems=9. Defaults
are constructed in application code as one tab and one chart, not by JSON Schema `default`.

Additional semantic validation:
- UUIDs are valid and globally unique across workspace, tabs, charts and indicators.
- activeTabId refers to an existing tab.
- Actual charts fit the selected grid; only documented grid presets are accepted.
- Row/column fraction array lengths match rows/columns; each sum is 1 within 1e-6.
- Historical mode requires non-null fromSec < toSec; live mode may keep null anchors.
- Manual price range, when set, has min < max; autoScale=false requires a manual range.
- Timezone is UTC or a valid installed IANA zone; strings are bounded and nonblank.
- All numbers are finite; no NaN/Infinity, executable content or arbitrary endpoint fields.
- Imports have a 5 MiB maximum and are rejected before unbounded allocation/parsing.

Persisted order is array order. Unused grid cells are inferred; never serialize placeholders
as charts. Runtime revisions, feed health, caches, subscribers and request generations are
not part of the exported document. Unsupported enum/version values must not be silently
reinterpreted. The schema permits `line` as an optional chart view; its implementation is
not required for candlestick-based v1 completion.

`tools/verify_bundle.py` checks shipped documents, checksums and important example invariants
without installing anything. When Python jsonschema is already available, it also runs full
Draft 2020-12 validation. It does not test the future Go implementation or live exchanges.

The v1 shape now accepts `1w` and an optional per-chart `indicators` array. Its omission preserves legacy saved workspaces and exports. Each indicator has a UUID, type, exact integer parameter object, and optional six-digit hex color. Maximum 12 per chart; periods 1–2,000, displacement 0–500. MACD `fast < slow` is an additional semantic check. Unknown or irrelevant parameter names, null fields and malformed values are rejected. Only configuration is saved; computed indicator series remain runtime data. These additive fields need no SQLite table migration because the store already validates and commits complete workspace JSON snapshots.

MACD indicators may also save `paneStretchFactor`, a finite number from 0.001–1,000
giving that indicator pane's height relative to the main chart pane. This keeps resized
panes proportional when a chart changes size. Only MACD accepts this optional property;
other indicator types, explicit null and malformed values are rejected. Legacy MACD
configurations may omit it to use the default layout, and roundtrip without adding a
new field. The saved value survives SQLite restart and portable JSON export/import.

The optional workspace-level `chartRightGap` object saves the shared chart setting as
`{"unit":"bars","value":10}` or `{"unit":"pixels","value":120}`. Both properties
are required when the object is present. Values must be whole numbers from 0–500 bars
or 0–5,000 CSS pixels; null, extra properties and unsupported units are rejected.
New workspaces explicitly use ten bars. Legacy documents may omit the setting, in
which case charts use ten bars while the exported JSON retains the original omitted
field. Each chart's existing `view.rightOffset` remains a viewport value, separate from
this global preference. No schema-version or SQLite table migration is required.

Chart `market` accepts `spot` and `perpetual` for Binance and Bybit; Coinbase
accepts `spot` only. It remains required, so existing spot workspaces retain their
exact identities and new charts still start with spot. Exchange, market, native
instrument and timeframe together identify a feed, including its memory and disk
candle caches. A spot and perpetual instrument may have the same native symbol.
This extends the existing string enum without changing the workspace or SQLite
schema version.

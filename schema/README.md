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

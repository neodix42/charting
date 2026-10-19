# Workspace examples — do not confuse startup with capacity

`starter-one-tab-one-chart.json`: first-run shape, exactly ONE tab with ONE chart and a
1×1 grid. Production creates fresh UUIDs; it must not reuse fixture IDs for all new workspaces.

`intermediate-two-tabs.json`: restoration regression case, TWO tabs containing THREE and
ONE charts. A 2×2 grid with three charts has one empty placeholder, not an invisible fourth
chart. Includes non-equal splitter widths and a historical time viewport.

`capacity-20-tabs-180-charts.json`: explicit TEST/IMPORT-ONLY maximum, TWENTY tabs with NINE
charts each. Never load it on first run, recovery, or normal restart. It is not a user default.
Repeated markets are deliberate for subscription-sharing tests; they are not a recommendation.

These examples contain configurations, not real market prices or credentials. Symbols and
historical timestamps are test inputs; discover real product availability at runtime. File
size is comfortably below the configured import cap. All files share workspace schema v1.

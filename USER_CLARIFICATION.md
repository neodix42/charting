# User clarification — capacity is not startup layout

The user clarified on 2026-10-19:

The application must start with ONE tab and ONE chart. Users add content as needed, with
maximum TWENTY tabs total and maximum NINE charts per tab. This is not a pre-filled setup.

Implementation consequences:
- The first-run workspace constructor creates 1 tab / 1 chart / 1×1 grid.
- Add Tab creates exactly one tab containing one chart.
- Add Chart creates exactly one chart.
- A larger grid creates empty cells, not chart models, canvases or subscriptions.
- Existing saved workspaces restore their actual counts and content.
- The supplied 20-tab / 180-chart file is an explicitly imported capacity-test fixture only.

This clarification is incorporated into SPEC.md, UI_UX.md, the Codex prompt, the agent
template, schema limits, workspace examples, implementation exit gates and acceptance tests.

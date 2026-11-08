# Security, privacy and attribution

This is a new public-data chart application, not a modified TradingView client. Do not extract
TradingView Desktop code, load its private APIs, impersonate paid sessions or rely on an
existing TradingView login. Use the published Lightweight Charts package and documented
exchange market data. Public market data is the default. Explicit chart trades use a dedicated Hyperliquid API wallet or the configured JForex account, after a fresh provider preview and one-use confirmation. No withdrawal/transfer or seed-phrase controls are implemented. Hyperliquid main-wallet keys are rejected; API keys and JForex passwords are machine-local, owner-only files and are excluded from workspace exports and bridge read responses.

Treat exchange messages, product names and imported workspaces as untrusted data. Apply body
and queue limits, strict parsing and schema checks. Render labels as text, not raw HTML.
Use TLS with certificate checks. Do not execute strings received from exchanges/imports,
launch shell commands with user-supplied chart names, or accept arbitrary feed URLs from JSON.

Keep the privileged Wails bridge limited to required methods. External links open through
a safe native/browser mechanism with scheme validation; do not navigate the privileged
webview to arbitrary remote pages. Production assets are local and dependencies are pinned.
Use a suitable content security policy consistent with the selected Wails build. No third-
party CDN scripts, remote module loading, auto-update execution, or telemetry by default.
Do not disable Linux sandboxing or run the app as root as a troubleshooting shortcut.

Store user settings under XDG paths, redact sensitive filesystem details in shared reports,
and keep logs bounded. Never commit cache databases, user workspace exports, `.env` secrets
or generated private data. Tests use synthetic prices and isolated temporary directories.

Lightweight Charts is Apache-2.0 licensed; its repository/docs also specify attribution.
Retain the LICENSE and NOTICE from the pinned package, include them with distributions, and
show an appropriate TradingView creator attribution/link in an accessible app location.
Keep the library attribution logo/link unless the upstream guidance permits a compliant
alternative. Do not invent endorsement or name the app TradingView. [S04, S22]

Collect licenses for all dependencies and fonts/icons included in the actual app. Use an
original project icon and properly licensed assets. Do not assume public exchange data can
be redistributed commercially without checking current provider terms; this is a local application and no data redistribution service is requested.

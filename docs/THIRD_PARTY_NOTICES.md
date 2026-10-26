# Third-party notices

CryptoDesk uses TradingView Lightweight Charts™ 5.2.1, licensed under Apache-2.0. Copyright (с) 2025 TradingView, Inc. <https://www.tradingview.com/>. The chart's attribution logo remains enabled, and the application footer/settings expose a creator link. CryptoDesk is independent and does not imply endorsement.

Exact installed licenses and notices for Go modules and frontend runtime packages are retained under `docs/licenses/` and included in the release archive. `docs/licenses/index.json` maps package versions to retained files; `scripts/collect-licenses.py` reproduces the installed-package collection. The npm Lightweight Charts package omits its repository NOTICE, so the exact v5.2.1 NOTICE is retained from <https://raw.githubusercontent.com/tradingview/lightweight-charts/v5.2.1/NOTICE>.

fancy-canvas 2.1.0 declares MIT in its npm metadata but omits LICENSE from the published package. The upstream license (Copyright 2019 TradingView, Inc.) is retained from <https://github.com/tradingview/fancy-canvas/blob/master/LICENSE>. No fonts, images or icon packs are downloaded by the application. The CryptoDesk SVG icon is original project artwork under the repository license.

The Linux release uses OS-provided GTK3, WebKitGTK 4.1 and libsoup3 shared libraries; see the Ubuntu runtime installation command in README.md or APP_README.md. Windows includes its pinned Microsoft WebView2 runtime and license.

All release variants also include the Screener's private Node.js 24.19.0, MIT-licensed dukascopy-node 1.51.0 and its locked dependencies, Playwright Core 1.64.1 and Chromium 156.0.8078.4. Their licenses, notices and browser credits are retained inside `screener-runtime`; `SCREENER-PROVENANCE.json` records the verified source archives and complete packaged inventory. `scripts/screener_bundle.py` assembles and verifies these files without installing global packages.

Screener enrichment uses MIT-licensed trading-signals 8.3.0 and csv-parse 7.0.3, pinned with npm integrity values and included in every runtime inventory. csv-parse includes its LICENSE under `node_modules/csv-parse`. The trading-signals npm tarball omits its MIT license; `screener-runtime/licenses/trading-signals-LICENSE` retains the exact file from published gitHead [a6893630d0565483bb6b12f7a55f2dee0cfd378e](https://github.com/bennycode/trading-signals/blob/a6893630d0565483bb6b12f7a55f2dee0cfd378e/packages/trading-signals/LICENSE). Packaging verifies that file's reviewed SHA-256.

Provider data is fetched directly for local display or user-requested CSV downloads; workspace exports contain configuration only. There are no trading, account-key, telemetry or hosted data redistribution components.

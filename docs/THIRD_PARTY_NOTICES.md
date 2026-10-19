# Third-party notices

CryptoDesk uses TradingView Lightweight Charts™ 5.2.1, licensed under Apache-2.0. Copyright (с) 2025 TradingView, Inc. <https://www.tradingview.com/>. The chart's attribution logo remains enabled, and the application footer/settings expose a creator link. CryptoDesk is independent and does not imply endorsement.

Exact installed licenses and notices for Go modules and frontend runtime packages are retained under `docs/licenses/` and included in the release archive. `docs/licenses/index.json` maps package versions to retained files; `scripts/collect-licenses.py` reproduces the installed-package collection. The npm Lightweight Charts package omits its repository NOTICE, so the exact v5.2.1 NOTICE is retained from <https://raw.githubusercontent.com/tradingview/lightweight-charts/v5.2.1/NOTICE>.

fancy-canvas 2.1.0 declares MIT in its npm metadata but omits LICENSE from the published package. The upstream license (Copyright 2019 TradingView, Inc.) is retained from <https://github.com/tradingview/fancy-canvas/blob/master/LICENSE>. No fonts, images or icon packs are downloaded by the application. The CryptoDesk SVG icon is original project artwork under the repository license.

The release uses OS-provided GTK3/WebKitGTK shared libraries; it does not redistribute a browser runtime. Provider data is fetched directly for local display; workspace exports contain configuration only. There are no trading, account-key, telemetry or hosted data redistribution components.

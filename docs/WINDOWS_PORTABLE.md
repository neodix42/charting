# CryptoDesk portable Windows x64

Extract the **complete ZIP** to a writable local folder on Windows 10/11 x64, then double-click `cryptodesk.exe`. Keep `portable.marker` and the entire `WebView2` directory beside the executable. No app installation, administrator rights or separately installed WebView2 runtime are needed. Internet access is still needed for public exchange data.

The first launch creates exactly one tab and one chart. The portable workspace database and browser profile are created under `data` beside the executable, independently of the current working directory. Later launches restore that workspace. Move the complete directory while the app is closed to move the workspace. For a separate workspace, use `cryptodesk.exe --data-dir "D:\CryptoDesk profile"`; JSON export/import is also available in Settings. Keep the data directory when replacing the application with a newer build.

Use a local writable folder. Microsoft does not support the Fixed Version runtime on network shares/UNC paths. On Windows 10, startup grants the two AppContainer read/execute permissions required by Microsoft to the bundled `WebView2` folder only. Windows 11 does not require this step. A startup failure displays an error instead of silently selecting a different browser runtime. The app does not install a global runtime, change the registry, or disable the browser sandbox.

The ZIP includes a pinned Microsoft **Fixed Version** WebView2 runtime, not an installer/bootstrapper. It does not update itself. Rebuild and redistribute the package with a newly reviewed runtime pin for browser security updates. It retains four English locale packs: `en-US.pak`, `en-GB.pak` and their two `copilot_overlay_strings_` counterparts. Other locale PAKs are omitted; shared PAKs, libraries, ICU data and all licensing notices are retained. WebView2's browser UI uses English through its supported Language API; explicit chart number/date/timezone formatting continues to work.

The runtime version, official source URL and full-source SHA-256 are recorded in `webview2-runtime.json`. `version.json` and `WebView2/WEBVIEW2-PROVENANCE.json` separately record the English package inventory, removed files and temporary build overlay used to select English in the pinned Go loader. Neither the downloaded CAB, complete extracted cache nor dependency source files are modified. `SHA256SUMS` lists every packaged file. Microsoft recommends shipping all Fixed Version files and does not document locale pruning as a supported deployment profile; Windows GUI behavior of this reduced package remains unverified.

## Build on Ubuntu

Run `./win-build.sh` from the checkout, or invoke its absolute path from another directory. Requires Go, Python 3.12+ and `7z` (Ubuntu package `7zip`). Node/npm and the pinned Wails CLI are cached locally using the existing toolchain bootstrap. The script does not install system packages or require MinGW or a Windows installation. Initial dependency/runtime downloads require network access.

The runtime is downloaded from Microsoft's version-specific URL and checked against the checked-in SHA-256. Later builds reuse the verified `.tools/webview2` cache. To supply the exact pinned CAB locally, run `./win-build.sh --webview2-cab /path/to/Microsoft.WebView2.FixedVersionRuntime.VERSION.x64.cab`; a different version or checksum is rejected.

Builds use a temporary copy of the current working sources, including uncommitted edits. Generated Wails files and Linux outputs in the checkout are preserved. Successful builds publish `cryptodesk-0.1.0-windows-amd64.zip` and `cryptodesk-Windows-SHA256SUMS` at the repository root. Verify with `sha256sum -c cryptodesk-Windows-SHA256SUMS` before copying the ZIP.

Cross-compilation and archive checks on Ubuntu do not establish Windows runtime compatibility. Verify first launch, saved-workspace restoration, dialogs, market connectivity and the bundled runtime on an actual Windows machine or VM before distributing widely. Windows ARM64/x86 builds are not included. The executable is unsigned.

Sources: [Wails fixed runtime configuration](https://v2.wails.io/docs/guides/windows/), [Microsoft Fixed Version deployment and Windows 10 permissions](https://learn.microsoft.com/en-us/microsoft-edge/webview2/concepts/distribution#the-fixed-version-runtime-distribution-mode).

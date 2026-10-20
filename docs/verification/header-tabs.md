# Header and tab-strip update — 2026-10-20

Removed the header's Rename and Close tab buttons, the All tabs dropdown and the Tabs x/y label. Renaming remains available by double-click; middle-click closes a tab immediately, and Ctrl+W retains its confirmation. The final tab and final chart stay protected.

The save indicator keeps a fixed 128px width in saved, pending, saving and error states. Grid, Add chart, Duplicate and Settings stay in place when its label changes. Tabs and the trailing plus now share a content-sized horizontal scroll container, with the plus immediately after the last tab. Selecting the last tab reveals the plus. Long labels ellipsize within their 260px bound while retaining their full accessible names.

Verification:

- `env PATH=/usr/bin:/bin scripts/test.sh`: PASS — 143 Python tooling/packaging tests, Go race/vet/bindings, frontend typecheck/ESLint, 74 frontend unit tests and production assets. [Log](header-tabs-deterministic.log).
- Full browser E2E: **53/53 PASS**. New geometry checks exercise both 1100×720 and 1600×1000 windows, saved/pending/saving/error/retry states, one/three/twenty tabs, long names, horizontal overflow, rename/reorder/removal and Ctrl+Tab navigation. Existing capacity/resource/save/restart tests pass. [Log](header-tabs-browser.log), [1100px layout](header-tabs-browser/header-save-stability-1100.png), [1600px layout](header-tabs-browser/header-save-stability-1600.png).
- Actual Wails/WebKit desktop: **51 fresh, 15 restore and 9 capacity assertions PASS**. The save indicator remains exactly 128px throughout all tested states; the neighboring Duplicate button stays at x=1305.421875. The maximum workspace exposes the trailing plus after the twentieth tab and completes 200 actual tab switches with nine mounted charts/leases. Native double-click rename, Ctrl+W confirmation/cancel, real candle persistence, indicator panes and acknowledged close/restart also pass. [Evidence](header-tabs-desktop/).
- The first native-driver run dispatched a synthetic keydown from Window instead of the focused tab, so the driver failed to open the Ctrl+W confirmation. Its report is retained in [header-tabs-desktop-initial](header-tabs-desktop-initial/). Dispatching from the focused tab matches real keyboard events; the corrected run passes. Native report errors now include their message as well as WebKit's stack.

All three root distribution archives are rebuilt through the existing Linux, bundled-runtime Linux and Windows scripts. Native Windows GUI and a full manual DPI/session matrix remain unverified. No release publication or repository commit was requested.

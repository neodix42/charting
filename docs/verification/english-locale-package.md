# English-only Windows locale package — 2026-10-19

The Windows build keeps `en-US.pak`, `en-GB.pak`, `copilot_overlay_strings_en-US.pak` and `copilot_overlay_strings_en-GB.pak` in `WebView2/Locales`. Other locale PAKs are omitted only from the temporary staged package, after the complete Microsoft runtime has passed its original CAB and extracted-inventory validation. Shared root PAKs, libraries, ICU data and all licenses remain intact. The complete verified cache is retained.

`WEBVIEW2-PROVENANCE.json` separates original source provenance from the reduced package's file inventory and removal list. `version.json` records the same profile and the pinned build-local Go overlay that selects English using `webviewloader.WithLanguage("en-US")`. No dependency cache source is edited and no browser development flag is used. The original runtime pin continues to describe the full Microsoft source download.

Microsoft recommends including all decompressed Fixed Version files and does not document locale pruning as a supported deployment profile. The smaller package is the requested custom distribution, and native Windows GUI/startup/non-English OS verification remains unrun. Chart formatting still uses its existing explicit locales and timezone settings.

Sources: [Microsoft distribution guidance](https://learn.microsoft.com/en-us/microsoft-edge/webview2/concepts/distribution#the-fixed-version-runtime-distribution-mode), [WebView2 Language API](https://learn.microsoft.com/en-us/microsoft-edge/webview2/reference/win32/icorewebview2environmentoptions#get_language).

## Observed verification

- `env PATH=/usr/bin:/bin ./win-build.sh`: PASS actual production cross-build and reduced runtime packaging. [Log](windows-english-build.log).
- ZIP size: **318,527,501 → 284,440,716 bytes** (303.8 → 271.3 MiB). Removed 164 non-English locale PAKs, totaling 133,088,128 unpacked bytes. Four English packs remain. [Package report](english-locale-package.json).
- Independently checked ZIP integrity, all **187 file checksums**, the exact four-pack allowlist, all **89 unchanged non-locale runtime files**, the distinct packaged inventory hash and original source provenance. The full cached runtime still has all 168 PAKs and validates against the original full-source inventory pin. Linux release outputs and all six generated binding files retain their contents and permissions.
- `scripts/test.sh`: PASS **34 Python tests**, Go race/vet/bindings, frontend typecheck/lint, **46 frontend unit tests** and production assets. [Log](windows-english-tests.log). New regressions cover English pack selection, missing-English failure before deletion, untouched source cache/shared resources/licenses, package provenance, pinned language injection, temporary module isolation, quoted Go flags and compilation-only flag scoping.
- The patched WebView2 package and complete application cross-compile for Windows. Go 1.26 disallows overlays directly in its module cache, so the build overlays a temporary copy selected through an alternate module file. Wails' version probes disable modules, so that module file/overlay are passed only through the temporary compilation wrapper. The initial unscoped flag attempt failed safely before publishing ([log](windows-english-build-initial.log)); the complete final build passes.

Native Windows GUI and non-English OS execution remain unverified. The reduced deployment is not claimed to be Microsoft-certified.

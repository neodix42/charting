# Portable Windows build verification — 2026-10-19

`win-build.sh` cross-builds the actual production frontend and Windows x64 GUI executable from Ubuntu, then bundles the full Microsoft Fixed Version WebView2 runtime, portable marker, usage instructions, licenses, version manifest and file checksums in one ZIP. Builds use a temporary copy of current working sources, preserving generated bindings and existing Linux outputs. No system packages or global runtime are installed.

## Implemented Windows support

- Nonblocking exclusive `LockFileEx` workspace locks, retained lockfiles and kernel release on process exit. Linux retains `Flock` behavior.
- Escaped Windows SQLite URIs, including spaces, percent/hash characters and Unicode filenames. Standalone default data paths use Windows AppData; portable profiles use the directory beside the executable.
- Executable-relative `WebView2` and `data` directories, explicit `--data-dir` override, missing-runtime errors, UNC rejection, Windows 10 AppContainer folder grants, native startup error dialogs and embedded IANA timezone data.
- Official runtime **155.0.4283.62 x64**: CAB 310,492,238 bytes; SHA-256 `cf54aa717a0ebd05b40e87620724583cb6eb0a5b8b64764cc11e42822f68bf6c`. Full runtime inventory SHA-256 `65550893b7c5e0d3c04ccc912e690398a31d94efc8d569382d5a8148a134fe11`; 257 inventoried files, 705,204,732 bytes, plus provenance. Microsoft license and original runtime notices are retained. Pin: `scripts/webview2-runtime.json`.

## Completed checks

- `env PATH=/usr/bin:/bin ./win-build.sh`: PASS real production Windows x64 cross-build and complete runtime packaging. [Build log](windows-build.log). The 303.8 MiB ZIP has 352 entries, including 351 independently verified file checksums. The executable is AMD64/PE32+/Windows GUI; the full runtime, license and marker are present and no user workspace is included. [Package report](windows-package.json). Existing Linux executable/archive/checksums and all six generated binding files retain exact contents and permissions.
- `env PATH=/usr/bin:/bin scripts/test.sh`: PASS 25 Python toolchain/runtime/packaging tests, Go race/vet and bindings checks, frontend typecheck/lint, 46 frontend unit tests and production asset build. [Log](windows-deterministic.log).
- Native Linux build-tag regression: `go test -race -tags webkit2_41 ./...` and `go vet -tags webkit2_41 ./...`: PASS. [Log](windows-linux-regression.log).
- Windows AMD64/CGO-disabled test binaries compile for every internal package, the bindings entry point and Windows startup helpers. Domain, exchange, market, storage and startup Windows test executables also PASS under **Wine**, using an isolated temporary prefix and each package's normal test working directory. This includes real Windows API locking, cross-process contention/crash recovery, SQLite persistence, profile paths and embedded timezone lookup. [Log](windows-wine.log). An initial test-driver invocation used the wrong working directory for fixture discovery and was corrected; [initial log](windows-wine-initial-cwd.log).
- Actual official runtime download/extraction, complete inventory and EXE/DLL version/architecture checks pass. A second run using the exact local CAB passed without downloading again. Offline tests cover corrupt/mismatched archives, cache verification, archive path/symlink rejection, runtime tree integrity, architecture/version, license preservation and destination protection.
- A second complete `win-build.sh --webview2-cab ...` invocation from `/tmp` with a sanitized PATH also PASSed using the cached runtime. An inherited `VITE_EXPLICIT_TEST_BRIDGE=1` was deliberately overridden for production, and the final executable contains neither test workspace hooks nor fixture storage keys. [Cached build log](windows-build-cached.log). Final package checksums pass, and the nine Linux output/binding files remain unchanged.
- The actual packaged executable's `--help` command PASSes under an isolated Wine prefix without starting a GUI or opening a workspace. [Log](windows-executable-help.log). Shell syntax/help/invalid arguments and the final 25 Python regressions also pass. [Python log](windows-python-final.log).

## Verification limits

Wine tests establish additional compatibility checks, not actual Windows GUI certification. No native Windows 10/11 GUI, WebView2 sandbox/ACL behavior, desktop dialogs, DPI or market connectivity was exercised on a Windows machine. The script's ZIP/PE/checksum validation does not claim those checks. The executable is unsigned. Fixed Version WebView2 does not auto-update; update the pin and rebuild to ship browser patches. Package usage and requirements: [Windows portable instructions](../WINDOWS_PORTABLE.md).

## Recovery of accidentally deleted files

The new Windows source files were restored from the preceding implementation's tool history. Python helpers/tests match the surviving original bytecode, constants, line metadata and source sizes; the runtime manifest and Windows instructions were recovered byte-for-byte from the surviving ZIP. Restored Windows startup helpers/tests produce the same executable SHA-256 as before deletion. The nine surviving tracked edits were preserved unchanged. Original raw verification logs were deleted and cannot be recovered byte-for-byte; replacement logs explicitly identify rerun evidence or historical notes. The original Windows ZIP is retained unchanged.

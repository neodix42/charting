# Linux JavaScriptCore GC signal verification

Verified on Ubuntu 26.04, amd64, Wayland, Go 1.26.8, Wails 2.17.0, and the installed WebKitGTK 4.1 runtime on 2026-10-20. All runs used disposable workspace directories; the normal user workspace was not opened.

- `fresh.json`: the production React bundle ran in a real Wails/WebKit window and passed 31 desktop assertions, including real public feeds, indicator rendering, MACD resizing, workspace persistence, shared controls, and gap settings. The app shut down normally. `fresh.log` is empty: neither the original handler notice nor a replacement JSC option error was printed.
- `gc-probe.json`: a standalone Go/cgo probe using the production signal helper selected unused public realtime signal **34**. It allocated 1,600,000 JavaScript objects in 64 evaluation/`JSGarbageCollect` rounds while Go performed 64 collections. JavaScriptCore installed its handler for signal 34, and Go's SIGUSR1, SIGURG and SIGPROF handlers remained unchanged. No `JSC_SIGNAL_FOR_GC` environment variable was introduced; `gc-probe.log` is empty.
- `startup_webkit_linux_test.go` starts isolated subprocesses to verify automatic selection and retention of explicit user environment settings, including an explicitly empty value. Run with `source scripts/native-env.sh && go test -tags 'production webkit2_41' -run '^TestWebKitSignalSelection$' -v .`. Production tags are necessary because Wails otherwise links a desktop stub without JavaScriptCore.

The default uses the optional exported `JSConfigureSignalForGC(int)` SPI, available since WebKitGTK 2.32. The weak reference permits normal startup on runtimes where it is unavailable. The helper checks `sigaction` before selecting a public realtime signal and does not reset existing handlers. Explicit `JSC_SIGNAL_FOR_GC` settings take precedence. If JSC already initialized or no unused public realtime signal exists, its default initialization remains intact.

The advertised environment-variable workaround was also tested and rejected: although it removed the original warning, the installed JSC option parser emitted `ERROR: invalid option: JSC_SIGNAL_FOR_GC=34`. The SPI avoids that conflict, with no stderr filtering and no persistent environment changes.

Upstream references:

- [WebKit GC signal configuration rationale and SPI contract](https://bugs.webkit.org/show_bug.cgi?id=220641)
- [SPI implementation](https://github.com/WebKit/WebKit/blob/main/Source/JavaScriptCore/API/JSBase.cpp)
- [GC signal handler ownership and notice](https://github.com/WebKit/WebKit/blob/main/Source/WTF/wtf/posix/ThreadingPOSIX.cpp)
- [JSC environment option parsing](https://github.com/WebKit/WebKit/blob/main/Source/JavaScriptCore/runtime/Options.cpp)

//go:build linux && cgo && !bindings

package main

/*
#include <signal.h>
#include <stdbool.h>

// Exported by JavaScriptCore since WebKitGTK 2.32, but its declaration lives
// in JSBasePrivate.h rather than the installed public headers. Keep it weak:
// older/different runtimes retain their normal initialization behavior.
extern bool JSConfigureSignalForGC(int signal) __attribute__((weak));

// SIGRTMIN is a libc expression, not a portable numeric constant. Starting
// there excludes NPTL's private signals, and sigaction keeps other libraries'
// installed handlers intact. Go leaves libc's first public realtime signal
// available on glibc Linux; higher signals may already belong to Go.
static int cryptodesk_configure_gc_signal(void) {
    if (!JSConfigureSignalForGC)
        return 0;
    struct sigaction current;
    for (int candidate = SIGRTMIN; candidate <= SIGRTMAX; candidate++) {
        if (sigaction(candidate, NULL, &current) == 0 && current.sa_handler == SIG_DFL)
            return JSConfigureSignalForGC(candidate) ? candidate : 0;
    }
    return 0;
}
*/
import "C"

import "os"

// configureWebKitSignal gives JavaScriptCore an unused GC suspension signal
// before Wails initializes WebKit. Its SIGUSR1 default otherwise replaces Go's
// handler and prints a notice on every launch. An explicit environment setting
// takes precedence, and an occupied signal is never reset or replaced here.
func configureWebKitSignal() int {
	if _, configured := os.LookupEnv("JSC_SIGNAL_FOR_GC"); configured {
		return 0
	}
	// Calling the SPI also avoids newer JSC versions treating their documented
	// JSC_SIGNAL_FOR_GC environment variable as an invalid VM option. Returning
	// zero leaves default initialization intact if the SPI is unavailable,
	// JSC already initialized, or no public realtime signal is free.
	return int(C.cryptodesk_configure_gc_signal())
}

package exchange

import (
	"context"
	"errors"
	"io"
	"net"
)

// RetryableReadError applies only to public data reads. A caller must never use
// it to resend a transaction or a notification with an uncertain outcome.
func RetryableReadError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	switch e := err.(type) {
	case *Error:
		return e.Kind == "offline" || e.Kind == "timeout" || e.Kind == "rateLimited"
	case *net.DNSError, *net.OpError:
		// NXDOMAIN from a local resolver can be a temporary upstream failure.
		return true
	case net.Error:
		if e.Timeout() {
			return true
		}
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			if RetryableReadError(child) {
				return true
			}
		}
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return RetryableReadError(wrapped.Unwrap())
	}
	return false
}

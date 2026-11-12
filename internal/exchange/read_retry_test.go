package exchange

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"testing"
)

func TestPublicReadRetriesRecognizeResolverFailureAndExcludePermanentOrCancelledReads(t *testing.T) {
	dns := &url.Error{Op: "Post", URL: "https://api.hyperliquid.xyz/info", Err: &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Name: "api.hyperliquid.xyz", Err: "no such host", IsNotFound: true}}}
	for _, err := range []error{dns, fmt.Errorf("catalog: %w", dns), errors.Join(errors.New("other market issue"), dns), context.DeadlineExceeded, &Error{Kind: "rateLimited", RetryAt: 123}, &Error{Kind: "offline"}} {
		if !RetryableReadError(err) {
			t.Fatal("recoverable public read was not retryable", err)
		}
	}
	for _, err := range []error{nil, context.Canceled, &Error{Kind: "unknownInstrument"}, &Error{Kind: "restricted"}, &Error{Kind: "internal"}, errors.New("unsupported indicator")} {
		if RetryableReadError(err) {
			t.Fatal("permanent/cancelled read was retried", err)
		}
	}
}

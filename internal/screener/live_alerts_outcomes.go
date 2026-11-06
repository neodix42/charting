package screener

import (
	"context"
	"errors"
)

// These issues describe a scan which exhausted its selected universe. They are
// retained for row details and publication retries, rather than aborting peers.
type liveAlertScanIssues struct{ error }
type liveAlertDeliveryIssue struct{ error }
type liveAlertPersistenceError struct{ error }

func (e *liveAlertScanIssues) Unwrap() error       { return e.error }
func (e *liveAlertDeliveryIssue) Unwrap() error    { return e.error }
func (e *liveAlertPersistenceError) Unwrap() error { return e.error }

func finishedLiveAlertScan(values ...error) error {
	err := joinLiveAlertErrors(values...)
	if err == nil {
		return nil
	}
	return &liveAlertScanIssues{err}
}
func completedLiveAlertScan(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := err.(*liveAlertScanIssues); ok {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !completedLiveAlertScan(child) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return completedLiveAlertScan(wrapped.Unwrap())
	}
	return false
}
func recoverableLiveAlertDelivery(ctx context.Context, err error) bool {
	var issue *liveAlertDeliveryIssue
	return ctx.Err() == nil && errors.As(err, &issue)
}
func fatalLiveAlertPersistence(err error) bool {
	var failure *liveAlertPersistenceError
	return errors.As(err, &failure)
}

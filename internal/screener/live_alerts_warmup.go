package screener

// Only explicit insufficient-history leaves are benign. An errors.Join which
// also contains a timeout, invalid CSV or other real failure remains an error.
func publicAlertHistoryPending(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := err.(*publicAlertInsufficientHistory); ok {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !publicAlertHistoryPending(child) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return publicAlertHistoryPending(wrapped.Unwrap())
	}
	return false
}

func publicAlertRequiredHistoryFailure(symbol string, periods []string, preparation, indexing map[string]error) error {
	var warming error
	for _, period := range periods {
		key := symbol + "/" + period
		for _, failure := range []error{preparation[key], indexing[key]} {
			if failure == nil {
				continue
			}
			if !publicAlertHistoryPending(failure) {
				return failure
			}
			if warming == nil {
				warming = failure
			}
		}
	}
	return warming
}

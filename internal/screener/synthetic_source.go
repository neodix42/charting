package screener

import "strings"

// Omitted protocol/job fields belong to existing tick downloads. The Downloader
// view supplies its new fast default explicitly, so old queues keep their
// original observations and checkpoints through upgrade and Resume.
func validSyntheticSource(value string) bool {
	return value == "" || value == "ticks" || value == "h1-close"
}

func effectiveSyntheticSource(value string) string {
	if value == "h1-close" {
		return value
	}
	return "ticks"
}

func syntheticSourceFromProvenance(value *DataProvenance) string {
	if value != nil && value.SyntheticSource != "" {
		return effectiveSyntheticSource(value.SyntheticSource)
	}
	if value != nil && (strings.Contains(value.SessionPolicy, "full-hour") || strings.Contains(value.ObservationResolution, "H1")) {
		return "h1-close"
	}
	return "ticks"
}

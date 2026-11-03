package screener

import (
	"errors"
	"os"
	"path/filepath"
)

// Only the daily family enters this read-only plan. Weekly publication can
// replace/consolidate weekly inputs, but must never retire its daily sources.
func prepareWeeklyDailyPlan(job Job, directory, work string) (string, map[string]os.FileInfo, error) {
	daily := job
	daily.Timeframe = "d1"
	dailyWork := filepath.Join(work, "daily")
	if err := os.MkdirAll(dailyWork, 0700); err != nil {
		return "", nil, err
	}
	plan, originals, err := prepareDownloadPlan(daily, directory, dailyWork)
	if err != nil {
		return "", nil, err
	}
	if len(plan.Inputs) == 0 {
		return "", nil, errors.New("weekly conversion requires retained d1 history; download daily data first")
	}
	filename := filepath.Join(work, "daily-plan.json")
	if err := writeState(filename, plan); err != nil {
		return "", nil, err
	}
	return filename, originals, nil
}

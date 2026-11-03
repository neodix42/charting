package screener

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func weeklyQueueJobs() []Job {
	jobs := []Job{}
	for _, period := range []string{"d1", "w1"} {
		for _, symbol := range []string{"btcusd", "ethusd"} {
			jobs = append(jobs, Job{ID: strings.Repeat(string(rune('a'+len(jobs))), 32), Symbol: symbol, Timeframe: period, From: "2024-01-01", To: "2024-01-15", AsOf: 1705276800000, Status: "queued"})
		}
	}
	return jobs
}

func TestWeeklyQueueRestoresOnlyMatchingDailyThenWeeklyBatchShape(t *testing.T) {
	for _, periods := range [][]string{{"d1"}, {"d1", "w1"}} {
		jobs := weeklyQueueJobs()
		if len(periods) == 1 {
			jobs = jobs[:2]
		}
		request, err := downloadBatchRequest(jobs, 5)
		if err != nil || request.Timeframe != periods[len(periods)-1] || request.From != "2024-01-01" || request.To != "2024-01-15" || !reflect.DeepEqual(request.Symbols, []string{"btcusd", "ethusd"}) || request.Concurrency != 5 {
			t.Fatalf("valid batch shape changed: %+v %v", request, err)
		}
	}
}

func TestMalformedWeeklyQueueIsRejectedAndSavedStateIsPreserved(t *testing.T) {
	cases := []struct {
		name   string
		mutate func([]Job) []Job
	}{
		{"missing-daily", func(jobs []Job) []Job { return jobs[2:] }},
		{"missing-weekly", func(jobs []Job) []Job { return jobs[:3] }},
		{"mismatched-symbol", func(jobs []Job) []Job { jobs[2].Symbol = "ltcusd"; return jobs }},
		{"mismatched-from", func(jobs []Job) []Job { jobs[2].From = "2024-01-02"; return jobs }},
		{"mismatched-to", func(jobs []Job) []Job { jobs[2].To = "2024-01-22"; return jobs }},
		{"mismatched-cutoff", func(jobs []Job) []Job { jobs[2].AsOf++; return jobs }},
		{"interleaved-phases", func(jobs []Job) []Job { jobs[1], jobs[2] = jobs[2], jobs[1]; return jobs }},
		{"unsupported-prerequisite", func(jobs []Job) []Job { jobs[0].Timeframe = "h1"; return jobs }},
		{"duplicate-symbol", func(jobs []Job) []Job { jobs[1].Symbol, jobs[3].Symbol = "btcusd", "btcusd"; return jobs }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			profile, directory := t.TempDir(), t.TempDir()
			jobs := tc.mutate(weeklyQueueJobs())
			queuePath := filepath.Join(profile, "screener-downloads.json")
			if err := writeState(queuePath, savedDownloads{Version: 1, Directory: directory, Concurrency: 5, Jobs: jobs}); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(queuePath)
			if err != nil {
				t.Fatal(err)
			}
			m := reopenTestManager(t, profile)
			if info := m.Info(); info.Error == "" || info.Active || len(info.Jobs) != 0 {
				t.Fatalf("malformed paired queue was accepted: %+v", info)
			}
			m.Close()
			after, err := os.ReadFile(queuePath)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("rejected saved queue was overwritten: %v", err)
			}
		})
	}
}

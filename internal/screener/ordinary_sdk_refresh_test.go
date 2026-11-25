package screener

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOrdinarySDKDailyRefreshUsesClosedHistoryDespiteSavedPublicDenial(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	disallowSDKTestHTTP(t)
	const name = "0700hkhkd-d1-2024-01-01-2024-01-03.csv"
	const original = "timestamp,open,high,low,close,volume,sma_2\n1704067200000,10,12,9,11,3,\n1704153600000,20,22,19,21,4,16\n"
	input := writeDataFile(t, m, name, original)
	info, err := os.Stat(input)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(original))
	if err = writeFileMetadata(m.directory, name, fileMetadata{Verified: true, Bytes: info.Size(), Modified: info.ModTime().UnixNano(), Checksum: hex.EncodeToString(digest[:]), CheckedThrough: 1704240000000, Coverage: []dataRange{{From: 1704067200000, To: 1704240000000}}}); err != nil {
		t.Fatal(err)
	}
	provider := newFakeSDKHistory()
	provider.export = sdkExportFixture
	ctx, cleanup, err := withProviderSessionWithHistory(context.Background(), m.profile, provider)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	state := sessionFromContext(ctx)
	state.limiter.interval = 0
	state.limiter.state.DeniedTill = time.Now().Add(time.Hour).UnixMilli()
	denied := state.limiter.state.DeniedTill
	job := RefreshJob{FileID: dataFileID(m.directory, name), Name: name, Status: "running"}
	m.refreshJobs = []RefreshJob{job}
	if err = m.refreshFileRequired(ctx, 0, m.directory, job, nil, 1704326400000+sdkHistoryHour, []string{"weekday", "sma_2"}); err != nil {
		t.Fatal("ordinary stock SDK refresh failed", err)
	}
	if state.limiter.state.DeniedTill != denied {
		t.Fatal("SDK refresh cleared the public HTTP denial")
	}
	completed := m.refreshJobs[0]
	if completed.Status != "completed" || completed.Rows != 3 || completed.AddedRows != 1 {
		t.Fatal(completed)
	}
	data, err := os.ReadFile(filepath.Join(m.directory, completed.Name))
	if err != nil || !strings.HasPrefix(string(data), "timestamp,open,high,low,close,sma_2,weekday\n") || !strings.Contains(string(data), "1704240000000,10,12,9,11,11,3\n") {
		t.Fatal("closed SDK bars or alert price fields were lost", string(data), err)
	}
	metadata, err := readFileMetadata(m.directory, completed.Name)
	if err != nil || !metadata.Verified || metadata.Provenance == nil || metadata.Provenance.Kind != "native" || metadata.Provenance.HistorySource != "jforex-sdk" || metadata.Provenance.Volume != "unavailable" {
		t.Fatal("ordinary SDK source lineage was lost", metadata, err)
	}
	backup, err := os.ReadFile(completed.BackupPath)
	if err != nil || string(backup) != original {
		t.Fatal("public original was not preserved", string(backup), err)
	}
	provider.mu.Lock()
	calls := append([]sdkHistoryCall(nil), provider.calls...)
	provider.mu.Unlock()
	if len(calls) != 1 || calls[0].symbol != "0700hkhkd" || calls[0].timeframe != "d1" || calls[0].from != 1704067200000 || calls[0].to != 1704326400000 {
		t.Fatal("ordinary refresh used the wrong source or execution cutoff", calls)
	}
}

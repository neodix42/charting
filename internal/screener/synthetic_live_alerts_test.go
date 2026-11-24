package screener

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSyntheticLiveChecksRequireTheExpectedClosedPeriodEvenAfterSuccessfulPartialRefresh(t *testing.T) {
	for _, period := range []string{"d1", "w1"} {
		for _, scheduled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s-scheduled-%t", period, scheduled), func(t *testing.T) {
				m := analysisTestManager(t)
				first := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
				step := 24 * time.Hour
				if period == "w1" {
					step = 7 * 24 * time.Hour
				}
				asOf := first.Add(3 * step)
				expected := first.Add(2 * step).UnixMilli()
				name := fmt.Sprintf("btcxau-%s-2024-01-01-%s.csv", period, asOf.Format("2006-01-02"))
				old := fmt.Sprintf("timestamp,open,high,low,close\n%d,10,12,9,11\n%d,11,14,10,13\n", first.UnixMilli(), first.Add(step).UnixMilli())
				writeProvenanceCSV(t, m, name, old, testRatioProvenance("btcxau"))
				request := positiveAnalysisRequest()
				request.Period = period
				saved, err := saveLiveCondition(t, m, "Synthetic latest-close rule", request)
				if err != nil {
					t.Fatal(err)
				}
				var sent []LiveAlert
				l, err := NewLiveAlerts(m, func(_ context.Context, alert LiveAlert) error { sent = append(sent, alert); return nil })
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(l.Close)
				l.now = func() time.Time { return asOf }
				fresh := false
				refreshes := 0
				l.refresh = func(ctx context.Context, _ string, groups []historyGroup, _ int64) (map[string]bool, error) {
					refreshes++
					// Explicit successful provider-result fixture: older overlap is
					// available while the latest exact timestamp join is still absent.
					text := old
					if fresh {
						text += fmt.Sprintf("%d,14,16,13,15\n", expected)
					}
					writeProvenanceCSV(t, m, name, text, testRatioProvenance("btcxau"))
					out := map[string]bool{}
					for _, group := range groups {
						match := downloadFilename.FindStringSubmatch(group.name)
						if match != nil {
							out[match[1]+"/"+match[2]] = true
						}
					}
					return out, ctx.Err()
				}
				config := LiveAlertsConfig{Enabled: true, ConditionIDs: []string{saved.ID}}
				if _, err := l.Configure(config); err != nil {
					t.Fatal(err)
				}
				err = l.checkCycle(context.Background(), config, asOf.UnixMilli(), scheduled)
				if !errors.Is(err, errLiveAlertsPendingBar) || len(sent) != 0 || len(l.state.Watermarks) != 0 || refreshes != 1 {
					t.Fatal("successful partial refresh replayed stale synthetic observations", err, sent, l.state.Watermarks, refreshes)
				}
				fresh = true
				if err = l.checkCycle(context.Background(), config, asOf.UnixMilli(), scheduled); err != nil || len(sent) != 1 || sent[0].Time != expected || !sent[0].Synthetic {
					t.Fatal("fresh exact observations did not resume alert evaluation", err, sent)
				}
				if err = l.checkCycle(context.Background(), config, asOf.UnixMilli(), scheduled); err != nil || len(sent) != 1 {
					t.Fatal("fresh synthetic close was sent twice", err, sent)
				}
			})
		}
	}
}

func TestMalformedMixedFamilyMetadataCannotDowngradeToUnqualifiedNativeAnalysis(t *testing.T) {
	m := analysisTestManager(t)
	name := "xauxag-d1-2024-01-01-2024-01-04.csv"
	provenance := testRatioProvenance("xauxag")
	provenance.Kind = "mixed-native-ratio"
	provenance.Segments = []ProvenanceSegment{{From: 1704067200000, To: 1704153600000, Kind: "synchronized-bid-ratio"}, {From: 1704153600000, To: 1704326400000, Kind: "native"}}
	writeProvenanceCSV(t, m, name, ratioCSV, provenance)
	metadataPath := filepath.Join(metadataDirectory(m.directory), dataFileID(m.directory, name)+".json")
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	value["provenance"].(map[string]any)["kind"] = "invalid-origin"
	changed, _ := json.Marshal(value)
	if err := os.WriteFile(metadataPath, changed, 0600); err != nil {
		t.Fatal(err)
	}
	file := describeFile(m.directory, name)
	if !strings.Contains(file.Error, "metadata is invalid") {
		t.Fatal("invalid mixed metadata looked like native legacy data", file)
	}
	if _, err := m.StartAnalysis(positiveAnalysisRequest()); err != nil {
		t.Fatal(err)
	}
	info := waitAnalysis(t, m)
	if info.Failed != 1 || info.TotalMatches != 0 {
		t.Fatal("invalid origin reached analysis or alert evidence", info)
	}
	// An absent metadata file remains compatible with a genuine legacy native
	// family CSV; absence is distinct from a malformed existing provenance.
	if err := os.Remove(metadataPath); err != nil {
		t.Fatal(err)
	}
	file = describeFile(m.directory, name)
	if file.Error != "" || file.Provenance != nil || file.Synthetic {
		t.Fatal("legacy native XAUXAG required invented provenance", file)
	}
}

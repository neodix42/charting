package screener

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"cryptodesk/internal/domain"
)

const ratioCSV = "timestamp,open,high,low,close\n1704067200000,10,12,9,11\n1704153600000,11,14,10,13\n1704240000000,13,15,12,14\n"

func testRatioProvenance(symbol string) *DataProvenance {
	definition, _ := SyntheticDefinitionFor(symbol)
	return &DataProvenance{Version: 1, Kind: "synchronized-bid-ratio", Numerator: definition.Numerator, Denominator: definition.Denominator, PriceType: "bid", MaxQuoteAgeMs: 0, SessionPolicy: "strict common timestamp; no carry", Volume: "unavailable", Approximate: true, ObservationResolution: "common millisecond BID observations"}
}

func writeProvenanceCSV(t *testing.T, m *Manager, name, text string, provenance *DataProvenance) string {
	t.Helper()
	filename := writeDataFile(t, m, name, text)
	info, err := os.Stat(filename)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(text))
	if err := writeFileMetadata(m.directory, name, fileMetadata{Verified: true, Bytes: info.Size(), Modified: info.ModTime().UnixNano(), Checksum: hex.EncodeToString(digest[:]), Provenance: provenance}); err != nil {
		t.Fatal(err)
	}
	return filename
}

func TestSyntheticCatalogCanonicalIDsNativeCompatibilityAndCalculatedPeriods(t *testing.T) {
	catalog := loadCatalog()
	counts := map[string]int{}
	var family []Instrument
	for _, group := range catalog.Groups {
		for _, item := range group.Instruments {
			counts[item.ID]++
		}
		if group.Name == SyntheticCategory {
			family = group.Instruments
		}
	}
	if len(family) != 5 {
		t.Fatal("synthetic catalog must expose exactly five family choices", family)
	}
	for index, definition := range SyntheticDefinitions() {
		item := family[index]
		if counts[definition.ID] != 1 || item.ID != definition.ID || item.Synthetic != definition.Calculated || item.Category != SyntheticCategory || !reflect.DeepEqual(item.SupportedTimeframes, []string{"d1", "w1"}) {
			t.Fatal("duplicate identity or incorrect family metadata", definition, item, counts)
		}
		for _, period := range []string{"h1", "d1", "w1"} {
			request := validRequest()
			request.Symbols, request.Timeframe = []string{definition.ID}, period
			err := validateRequest(request, time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC))
			if (err != nil) != (definition.Calculated && period == "h1") {
				t.Fatal("calculated restriction or native legacy compatibility changed", definition.ID, period, err)
			}
		}
	}
	family[0].SupportedTimeframes[0] = "changed"
	if loadCatalog().Groups[len(catalog.Groups)-1].Instruments[0].SupportedTimeframes[0] != "d1" {
		t.Fatal("mutable catalog timeframes escaped")
	}
	if definition, _ := SyntheticDefinitionFor("XAUXAG"); definition.EarliestDate != "2003-05-05" || definition.NativeHistoryFrom != "2024-09-17" {
		t.Fatal("native/fallback history boundaries were conflated", definition)
	}
}

func TestSyntheticHistoryRequiresMatchingProvenanceAndPreservesUnavailableVolume(t *testing.T) {
	m := analysisTestManager(t)
	key := domain.FeedKey{Exchange: "dukascopy", Market: "cfd", Instrument: "BTCXAU", Timeframe: "1d"}
	if _, err := m.SyntheticHistory(context.Background(), key, 0, 0, 0); !errors.Is(err, ErrSyntheticHistoryUnavailable) {
		t.Fatal("missing calculated history did not give download guidance", err)
	}
	name := "btcxau-d1-2024-01-01-2024-01-04.csv"
	filename := writeDataFile(t, m, name, ratioCSV)
	if _, err := m.SyntheticHistory(context.Background(), key, 0, 0, 0); err == nil || errors.Is(err, ErrSyntheticHistoryUnavailable) {
		t.Fatal("unproven CSV was accepted or mistaken for absent history", err)
	}
	writeProvenanceCSV(t, m, name, ratioCSV, testRatioProvenance("btcxau"))
	bars, err := m.SyntheticHistory(context.Background(), key, 1704067200, 1704240000, 1)
	if err != nil || len(bars) != 1 || bars[0].Time != 1704153600 || bars[0].Close != 13 || !bars[0].Synthetic || !bars[0].Approximate || !bars[0].VolumeUnavailable || !bars[0].Closed {
		t.Fatal("calculated bars lost identity, bounds, or unavailable-volume semantics", bars, err)
	}
	bytes, _ := os.ReadFile(filename)
	changed := strings.Replace(string(bytes), "10,12,9,11", "10,12,9,10", 1)
	info, _ := os.Stat(filename)
	if err = os.WriteFile(filename, []byte(changed), 0640); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(filename, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SyntheticHistory(context.Background(), key, 0, 0, 0); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatal("equal-size and timestamp edit bypassed provenance binding", err)
	}
}

func TestSyntheticHistoryMixedXAUXAGProvenanceAndNativeVolumeStayDistinct(t *testing.T) {
	m := analysisTestManager(t)
	provenance := testRatioProvenance("xauxag")
	provenance.Kind = "mixed-native-ratio"
	provenance.Segments = []ProvenanceSegment{{From: 1704067200000, To: 1704153600000, Kind: "synchronized-bid-ratio"}, {From: 1704153600000, To: 1704326400000, Kind: "native"}}
	writeProvenanceCSV(t, m, "xauxag-d1-2024-01-01-2024-01-04.csv", ratioCSV, provenance)
	key := domain.FeedKey{Exchange: "dukascopy", Market: "metal", Instrument: "XAUXAG", Timeframe: "1d"}
	bars, err := m.SyntheticHistory(context.Background(), key, 0, 0, 0)
	if err != nil || len(bars) != 3 || !bars[0].Synthetic || bars[1].Synthetic || bars[1].Approximate || !bars[1].VolumeUnavailable {
		t.Fatal("mixed dataset lost its analytical identity", bars, err)
	}
	writeDataFile(t, m, "btcusd-d1-2024-01-01-2024-01-02.csv", "timestamp,open,high,low,close,volume\n1704067200000,10,12,9,11,42\n")
	key.Market, key.Instrument = "cfd", "BTCUSD"
	bars, err = m.SyntheticHistory(context.Background(), key, 0, 0, 0)
	if err != nil || len(bars) != 1 || bars[0].Synthetic || bars[0].Approximate || bars[0].VolumeUnavailable || bars[0].Volume != 42 {
		t.Fatal("native provider data was relabelled as calculated", bars, err)
	}
}

func TestNodeSyntheticProvenanceContractAndMixedBarOriginBoundaries(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("project-local Node is required for the cross-language provenance contract")
	}
	worker, err := filepath.Abs("../../screener-runtime/synthetic-worker.cjs")
	if err != nil {
		t.Fatal(err)
	}
	script := `const {provenance,mergeProvenance}=require(process.argv[1]);const first=Date.parse('2024-09-16T00:00:00Z'),boundary=Date.parse('2024-09-17T09:39:43.256Z'),to=Date.parse('2024-09-30T00:00:00Z');const ratio=provenance('xauxag',true),native=provenance('xauxag',false);process.stdout.write(JSON.stringify([mergeProvenance('xauxag',[{value:ratio,from:first,to}],false),mergeProvenance('xauxag',[{value:ratio,from:first,to:boundary},{value:native,from:boundary,to}],false),mergeProvenance('xauxag',[{value:native,from:first,to}],false)]));`
	output, err := exec.Command(node, "-e", script, worker).Output()
	if err != nil {
		t.Fatal("read real Node provenance", err)
	}
	var values []*DataProvenance
	if err := json.Unmarshal(output, &values); err != nil || len(values) != 3 {
		t.Fatal(string(output), err)
	}
	for index, value := range values {
		if err := validateSyntheticDataset("xauxag", "d1", value); err != nil {
			t.Fatal("Node provenance failed Go validation", index, value, err)
		}
	}
	if values[0].Kind != "synchronized-bid-ratio" || len(values[0].Segments) != 0 || values[1].Kind != "mixed-native-ratio" || len(values[1].Segments) != 2 || values[2].Kind != "native" || len(values[2].Segments) != 0 {
		t.Fatal("pure and mixed provenance shapes diverged", string(output))
	}
	m := analysisTestManager(t)
	start := time.Date(2024, time.September, 16, 0, 0, 0, 0, time.UTC).UnixMilli()
	for _, test := range []struct {
		period, interval string
		offsets          []int64
		want             []bool
	}{
		{"d1", "1d", []int64{0, 86400000, 2 * 86400000}, []bool{true, true, false}},
		{"w1", "1w", []int64{0, 7 * 86400000}, []bool{true, false}},
	} {
		var text strings.Builder
		text.WriteString("timestamp,open,high,low,close\n")
		for _, offset := range test.offsets {
			fmt.Fprintf(&text, "%d,10,12,9,11\n", start+offset)
		}
		writeProvenanceCSV(t, m, "xauxag-"+test.period+"-2024-09-16-2024-09-30.csv", text.String(), values[1])
		bars, err := m.SyntheticHistory(context.Background(), domain.FeedKey{Exchange: "dukascopy", Market: "metal", Instrument: "XAUXAG", Timeframe: test.interval}, 0, 0, 0)
		if err != nil || len(bars) != len(test.want) {
			t.Fatal(test.period, bars, err)
		}
		for index, bar := range bars {
			if bar.Synthetic != test.want[index] || bar.Approximate != test.want[index] || !bar.VolumeUnavailable {
				t.Fatal("mixed source origin leaked into native-only period", test.period, index, bar)
			}
		}
	}
}

func TestSyntheticAnalysisAndSavedEvidenceRemainAnalyticalWithoutVolume(t *testing.T) {
	m := analysisTestManager(t)
	name := "btcxau-d1-2024-01-01-2024-01-04.csv"
	writeProvenanceCSV(t, m, name, ratioCSV, testRatioProvenance("btcxau"))
	request := positiveAnalysisRequest()
	request.Period, request.Categories = "d1", []string{SyntheticCategory}
	request.TradeSimulation = &AnalysisTradeSimulation{StopLoss: "low", TakeProfitMultiple: 1, Duration: 1}
	if _, err := m.StartAnalysis(request); err != nil {
		t.Fatal(err)
	}
	info := waitAnalysis(t, m)
	if info.Failed != 0 || len(info.Results) != 1 || info.Results[0].Matches != 3 || !info.Results[0].Synthetic || info.Results[0].SimulationBasis != SyntheticSimulationBasis || info.Results[0].Provenance == nil {
		t.Fatal("analysis lost synthetic result provenance", info)
	}
	chart, err := m.AnalysisChart(info.Results[0].ID, 0, 0, 0)
	if err != nil || !chart.Synthetic || chart.SimulationBasis != SyntheticSimulationBasis || chart.Provenance == nil {
		t.Fatal("analysis chart looks executable", chart, err)
	}
	for _, field := range chart.Fields {
		if field == "volume" {
			t.Fatal("calculated chart invented volume")
		}
	}
	saved, err := m.SaveAnalysisCondition("Ratio close rule", request)
	if err != nil || saved.Analysis == nil || len(saved.Analysis.Assets) != 1 || !saved.Analysis.Assets[0].Synthetic || saved.Analysis.Assets[0].SimulationBasis != SyntheticSimulationBasis {
		t.Fatal("saved alert evidence lost analytical qualification", saved, err)
	}
	series := m.analysis.series[info.Results[0].ID]
	row, err := series.row(2)
	if err != nil {
		t.Fatal(err)
	}
	alert, err := liveAlertFromMatch(saved, series, 2, row)
	if err != nil || !alert.Synthetic || alert.SimulationBasis != SyntheticSimulationBasis || alert.Provenance == nil {
		t.Fatal("closed-bar live alert lost analytical provenance", alert, err)
	}
	info.Results[0].Provenance.Kind = "changed"
	copy, err := m.Analysis(0, 10)
	if err != nil || copy.Results[0].Provenance.Kind != "synchronized-bid-ratio" {
		t.Fatal("analysis response exposed mutable provenance", copy, err)
	}
}

func TestSyntheticVolumeIndicatorsFailBeforeWorkerOrSourceMutation(t *testing.T) {
	m := analysisTestManager(t)
	name := "ethbtc-d1-2024-01-01-2024-01-04.csv"
	filename := writeProvenanceCSV(t, m, name, ratioCSV, testRatioProvenance("ethbtc"))
	for _, indicator := range []string{"obv", "vwap", "vwma", "mfi", "cmf", "ad", "pvt", "force"} {
		err := m.enrichFile(context.Background(), 0, m.directory, EnrichmentJob{Name: name}, EnrichmentRequest{Indicator: indicator})
		if err == nil || !strings.Contains(err.Error(), "volume-based indicators") {
			t.Fatal("unsupported calculated volume reached the worker", indicator, err)
		}
	}
	data, _ := os.ReadFile(filename)
	if string(data) != ratioCSV {
		t.Fatal("rejected volume enrichment changed source CSV")
	}
}

func TestSyntheticAnalysisRejectsTamperedOrMisattributedSourceBeforeAlerts(t *testing.T) {
	for _, mode := range []string{"same size and time edit", "wrong ratio legs", "wrong period"} {
		t.Run(mode, func(t *testing.T) {
			m := analysisTestManager(t)
			name := "btcxau-d1-2024-01-01-2024-01-04.csv"
			provenance := testRatioProvenance("btcxau")
			if mode == "wrong ratio legs" {
				provenance.Numerator = "ethusd"
			}
			if mode == "wrong period" {
				name = "btcxau-h1-2024-01-01-2024-01-04.csv"
			}
			filename := writeProvenanceCSV(t, m, name, ratioCSV, provenance)
			if mode == "same size and time edit" {
				info, _ := os.Stat(filename)
				if err := os.WriteFile(filename, []byte(strings.Replace(ratioCSV, "10,12,9,11", "10,12,9,10", 1)), 0640); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(filename, info.ModTime(), info.ModTime()); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := m.StartAnalysis(positiveAnalysisRequest()); err != nil {
				t.Fatal(err)
			}
			info := waitAnalysis(t, m)
			if info.Failed != 1 || info.TotalMatches != 0 || len(info.Results) != 1 || info.Results[0].Status != "failed" {
				t.Fatal("unverified ratio entered analysis/alert indexes", mode, info)
			}
		})
	}
}

func TestSyntheticEnrichmentCannotBlessEditedPricesWithNewProvenanceChecksum(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	name := "btcxau-d1-2024-01-01-2024-01-04.csv"
	filename := writeProvenanceCSV(t, m, name, ratioCSV, testRatioProvenance("btcxau"))
	metadataPath := filepath.Join(metadataDirectory(m.directory), dataFileID(m.directory, name)+".json")
	beforeMetadata, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(ratioCSV, "10,12,9,11", "10,12,9,10", 1)
	if err := os.WriteFile(filename, []byte(changed), 0640); err != nil {
		t.Fatal(err)
	}
	if _, err := m.StartEnrichment(EnrichmentRequest{Indicator: "sma", Parameters: map[string]float64{"period": 2}}); err != nil {
		t.Fatal(err)
	}
	info := waitEnrichment(t, m, []string{dataFileID(m.directory, name)})
	if info.Failed != 1 || len(info.Jobs) != 1 || !strings.Contains(info.Jobs[0].Message, "source checksum") {
		t.Fatal("enrichment reverified changed synthetic prices", info)
	}
	after, _ := os.ReadFile(filename)
	afterMetadata, _ := os.ReadFile(metadataPath)
	if string(after) != changed || string(afterMetadata) != string(beforeMetadata) {
		t.Fatal("failed enrichment changed source bytes or provenance metadata")
	}
}

func TestSyntheticDownloadPlanRetainsHashForReverificationAfterTimestampOnlyChange(t *testing.T) {
	m := analysisTestManager(t)
	name := "btcxau-d1-2024-01-01-2024-01-04.csv"
	filename := writeProvenanceCSV(t, m, name, ratioCSV, testRatioProvenance("btcxau"))
	info, err := os.Stat(filename)
	if err != nil {
		t.Fatal(err)
	}
	stamp := info.ModTime().Add(-time.Hour)
	if err := os.Chtimes(filename, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	plan, _, err := prepareDownloadPlan(Job{Symbol: "btcxau", Timeframe: "d1", From: "2024-01-01", To: "2024-01-04", AsOf: time.Now().UnixMilli()}, m.directory, t.TempDir())
	if err != nil || len(plan.Inputs) != 1 || plan.Inputs[0].Metadata.Provenance == nil || plan.Inputs[0].Metadata.Checksum == "" || plan.Inputs[0].Metadata.Verified || len(plan.Inputs[0].Metadata.Coverage) != 0 {
		t.Fatal("source timestamp change discarded provenance hash or trusted stale coverage", plan, err)
	}
}

func TestSyntheticWeeklyQueueRetainsDailyPrerequisiteAndRestoresWithoutNewIdentity(t *testing.T) {
	m, gates := gatedManager(t)
	request := validRequest()
	request.Symbols, request.Timeframe, request.To = []string{"btcxau"}, "w1", "2024-01-15"
	if _, err := m.Start(request); err != nil {
		t.Fatal(err)
	}
	started := waitGate(t, gates, "btcxau")
	if started.HistoryDirectory != m.directory || !filepath.IsAbs(started.HistoryDirectory) {
		t.Fatal("tick cache reuse lacks the configured history directory", started)
	}
	m.Close()
	paused := m.Info()
	if len(paused.Jobs) != 2 || paused.Jobs[0].Timeframe != "d1" || paused.Jobs[1].Timeframe != "w1" || paused.Jobs[0].Symbol != "btcxau" || paused.Jobs[1].Symbol != "btcxau" {
		t.Fatal("weekly synthetic batch lost one canonical daily identity", paused)
	}
	restored := newManager(m.profile, func() (workerRuntime, error) { return workerRuntime{}, errors.New("explicit offline runtime") })
	t.Cleanup(restored.Close)
	got := restored.Info()
	if got.Active || len(got.Jobs) != 2 || got.Jobs[0].ID != paused.Jobs[0].ID || got.Jobs[1].ID != paused.Jobs[1].ID || got.Error != "" {
		t.Fatal("synthetic queue restore lost identity or rewrote the request", got)
	}
}

package screener

import (
	"context"
	"crypto/sha256"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type publicAlertFakeAdapter struct {
	mu           sync.Mutex
	catalogCalls int
	historyKeys  []domain.FeedKey
	ranges       [][2]int64
	items        []domain.Instrument
	price        float64
	short        bool
	listedAt     int64
	pending      bool
	provisional  bool
	fail         error
}

func (f *publicAlertFakeAdapter) Instruments(context.Context) ([]domain.Instrument, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.catalogCalls++
	return f.items, nil
}
func (f *publicAlertFakeAdapter) Stream(context.Context, domain.FeedKey, func([]domain.Candle, domain.Health)) error {
	return errors.New("alerts must not subscribe streams")
}
func (f *publicAlertFakeAdapter) History(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.historyKeys = append(f.historyKeys, key)
	f.ranges = append(f.ranges, [2]int64{from, to})
	if f.fail != nil {
		return nil, f.fail
	}
	d := exchange.TimeframeSeconds(key.Timeframe)
	if f.short && f.listedAt == 0 {
		f.listedAt = to - d
	}
	if to-from > int64(limit)*d {
		return nil, errors.New("unbounded history range")
	}
	rows := []domain.Candle{}
	for ts := from; ts < to; ts += d {
		if f.short && ts < f.listedAt {
			continue
		}
		if f.pending && ts+d == to {
			continue
		}
		price := f.price
		if key.Market == "perpetual" {
			price += 1000
		}
		quality := "authoritative"
		if f.provisional {
			quality = "provisional"
		}
		rows = append(rows, domain.Candle{Time: ts, Open: price, High: price + 2, Low: price - 2, Close: price + 1, Volume: 3, Closed: true, Quality: quality})
	}
	return rows, nil
}

func publicAlertTestCondition() SavedAnalysisCondition {
	return SavedAnalysisCondition{ID: strings.Repeat("a", 32), Name: "Public cross-period rule", Request: AnalysisRequest{Period: "m1", Conditions: []AnalysisCondition{
		{Operator: "above", Left: AnalysisOperand{Kind: "field", Field: "close"}, Right: AnalysisOperand{Kind: "field", Field: "sma_3"}},
		{Operator: "above", Left: AnalysisOperand{Kind: "field", Field: "weekday", Timeframe: "d1"}, Right: AnalysisOperand{Kind: "number", Value: 0}},
	}}}
}

func newPublicAlertTestLive(t *testing.T, m *Manager, source string, fake *publicAlertFakeAdapter) *LiveAlerts {
	t.Helper()
	l, err := NewLiveAlerts(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	l.publicData = &publicAlertData{adapters: map[string]exchange.Adapter{source: fake}}
	return l
}

func TestPublicAlertPlanningFollowsRuleFieldsCompanionsAndLookbacks(t *testing.T) {
	c := publicAlertTestCondition()
	c.Request.Conditions[0].Left.Offset = 4
	c.Request.Conditions[0].Left.Window = 5
	c.Request.Conditions[1].Operator = "remainedFor"
	c.Request.Conditions[1].Bars = 7
	c.Request.TradeSimulation = &AnalysisTradeSimulation{StopLoss: "low_10", StopLossBarsAgo: 12, TakeProfit: "high_20"}
	plans, err := publicAlertPlans([]SavedAnalysisCondition{c}, map[string][]string{c.ID: {"m1"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 2 || plans["m1"].Extra != 15 || plans["d1"].Extra != 1 || !reflect.DeepEqual(plans["m1"].Fields, []string{"close", "high_20", "low_10", "sma_3"}) || !reflect.DeepEqual(plans["d1"].Fields, []string{"weekday"}) {
		t.Fatalf("incorrect dependency plan: %+v", plans)
	}
	c.Request.Conditions[1].Left.Timeframe = "tick"
	if _, err := publicAlertPlans([]SavedAnalysisCondition{c}, map[string][]string{c.ID: {"m1"}}); err == nil {
		t.Fatal("unsupported companion period accepted")
	}
}

func TestPublicAlertLoaderAllMarketsPersistentReuseAndSourceSeparation(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	foreign := writeDataFile(t, m, "btcusd-m1-2024-01-01-2024-01-02.csv", "timestamp,close\n1704067200000,99999\n")
	original, _ := os.ReadFile(foreign)
	fake := &publicAlertFakeAdapter{price: 100, items: []domain.Instrument{
		{NativeID: "BTCUSDT", Market: "spot", Availability: "TRADING"},
		{NativeID: "BTCUSDT", Market: "perpetual", Availability: "Trading"},
		{NativeID: "OLDUSDT", Market: "spot", Availability: "disabled"},
	}}
	l := newPublicAlertTestLive(t, m, "binance", fake)
	c := publicAlertTestCondition()
	periods := map[string][]string{c.ID: {"m1"}}
	asOf := time.Date(2026, 11, 5, 12, 0, 5, 0, time.UTC).UnixMilli()
	batch, err := l.preparePublicAlertData(context.Background(), "binance", []SavedAnalysisCondition{c}, periods, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Assets) != 2 || len(batch.Files) != 4 || len(batch.Failures) != 0 {
		t.Fatalf("all markets/companions not prepared: %+v", batch)
	}
	for _, asset := range batch.Assets {
		if asset.Symbol != publicAlertSymbol(asset.Market, asset.NativeID) || strings.ContainsAny(asset.Symbol, "/@:") {
			t.Fatal(asset)
		}
		name := batch.Files[asset.Symbol+"/m1"]
		data, err := os.ReadFile(filepath.Join(batch.Directory, name))
		if err != nil {
			t.Fatal(err)
		}
		rows, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
		if err != nil {
			t.Fatal(err)
		}
		positions := map[string]int{}
		for i, f := range rows[0] {
			positions[f] = i
		}
		want := "101"
		if asset.Market == "perpetual" {
			want = "1101"
		}
		last := rows[len(rows)-1]
		if last[positions["close"]] != want || last[positions["sma_3"]] != want {
			t.Fatalf("foreign source/market data or incorrect actual SMA: %v", last)
		}
	}
	fake.mu.Lock()
	historyCalls := len(fake.historyKeys)
	fake.mu.Unlock()
	if _, err = l.preparePublicAlertData(context.Background(), "binance", []SavedAnalysisCondition{c}, periods, asOf); err != nil {
		t.Fatal(err)
	}
	if fake.catalogCalls != 1 || len(fake.historyKeys) != historyCalls {
		t.Fatalf("same closed cutoff refetched cached data: catalogs=%d histories=%d -> %d", fake.catalogCalls, historyCalls, len(fake.historyKeys))
	}
	// A fresh owner has no memory cache, but successfully checked raw histories
	// remain reusable after restart without another historical request.
	restarted := newPublicAlertTestLive(t, m, "binance", fake)
	if _, err = restarted.preparePublicAlertData(context.Background(), "binance", []SavedAnalysisCondition{c}, periods, asOf); err != nil {
		t.Fatal(err)
	}
	if len(fake.historyKeys) != historyCalls {
		t.Fatal("restart discarded persisted provider history")
	}
	// Next close only requests an overlap and the new bar, preserving warm-up.
	if _, err = l.preparePublicAlertData(context.Background(), "binance", []SavedAnalysisCondition{c}, periods, asOf+60000); err != nil {
		t.Fatal(err)
	}
	for _, r := range fake.ranges[historyCalls:] {
		if r[1]-r[0] > 120 {
			t.Fatalf("incremental close refetched warm-up: %v", r)
		}
	}
	other := &publicAlertFakeAdapter{price: 500, items: fake.items}
	l.publicData.adapters["bybit"] = other
	second, err := l.preparePublicAlertData(context.Background(), "bybit", []SavedAnalysisCondition{c}, periods, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if second.Directory == batch.Directory || len(other.historyKeys) == 0 {
		t.Fatal("public venues shared a history cache")
	}
	for _, key := range other.historyKeys {
		if key.Exchange != "bybit" {
			t.Fatal(key)
		}
	}
	after, _ := os.ReadFile(foreign)
	if string(after) != string(original) {
		t.Fatal("Dukascopy history changed during public check")
	}
}

func TestPublicAlertLoaderShortUndefinedAndProvisionalNeverBecomeTargets(t *testing.T) {
	for _, mode := range []string{"short", "provisional", "undefined", "transport"} {
		t.Run(mode, func(t *testing.T) {
			m := sourceEnrichmentManager(t, false)
			fake := &publicAlertFakeAdapter{price: 100, items: []domain.Instrument{{NativeID: "BTC", Market: "perpetual", Availability: "available"}}}
			switch mode {
			case "short":
				fake.short = true
			case "provisional":
				fake.provisional = true
			case "transport":
				fake.fail = &exchange.Error{Kind: "restricted", Message: "restricted synthetic transport"}
			}
			l := newPublicAlertTestLive(t, m, "hyperliquid", fake)
			c := publicAlertTestCondition()
			c.Request.Conditions = c.Request.Conditions[:1]
			if mode == "undefined" {
				c.Request.Conditions[0].Right.Field = "unknown_17"
			}
			batch, err := l.preparePublicAlertData(context.Background(), "hyperliquid", []SavedAnalysisCondition{c}, map[string][]string{c.ID: {"m1"}}, time.Date(2026, 11, 5, 12, 0, 5, 0, time.UTC).UnixMilli())
			if mode == "undefined" {
				if err == nil {
					t.Fatal("unrecognized field silently accepted")
				}
				if len(fake.historyKeys) != 0 {
					t.Fatal("invalid field contacted public provider")
				}
				return
			}
			if mode == "transport" {
				if err == nil || exchange.ErrorKind(err) != "restricted" {
					t.Fatalf("terminal provider refusal was hidden: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if len(batch.Files) != 0 || len(batch.Failures) != 1 {
				t.Fatalf("invalid data became an alert target: %+v", batch)
			}
			if mode != "transport" {
				raw, _ := filepath.Glob(filepath.Join(m.profile, "screener-public-history", "hyperliquid", "raw", "*.csv"))
				if len(raw) != 1 {
					t.Fatal("available real bars were not retained for future warm-up")
				}
			}
		})
	}
}

func TestPublicAlertIndicatorWarmupUsesInstalledConstructors(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	plans := map[string]publicAlertPlan{
		"m1": {Fields: []string{"close", "sma_200"}, Extra: 4},
		"m5": {Fields: []string{"macd_histogram_12_26_9"}, Extra: 2},
		"h1": {Fields: []string{"tema_20"}, Extra: 1},
		"h4": {Fields: []string{"adx_14"}, Extra: 1},
		"d1": {Fields: []string{"weekday", "hour"}, Extra: 1},
	}
	w, got, err := startPublicAlertWorker(context.Background(), m.runtime, plans)
	if err != nil {
		t.Fatal(err)
	}
	defer w.close()
	for period, want := range map[string]int{"m1": 204, "m5": 36, "h1": 59, "h4": 28, "d1": 2} {
		if got[period].Bars != want {
			t.Errorf("%s warmup=%d want%d", period, got[period].Bars, want)
		}
	}
}

func TestPublicAlertCacheCorruptionAndCancellationPreserveSavedBytes(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	fake := &publicAlertFakeAdapter{price: 100, items: []domain.Instrument{{NativeID: "BTCUSDT", Market: "spot", Availability: "TRADING"}}}
	l := newPublicAlertTestLive(t, m, "binance", fake)
	c := publicAlertTestCondition()
	c.Request.Conditions = c.Request.Conditions[:1]
	periods := map[string][]string{c.ID: {"m1"}}
	asOf := time.Date(2026, 11, 5, 12, 0, 5, 0, time.UTC).UnixMilli()
	batch, err := l.preparePublicAlertData(context.Background(), "binance", []SavedAnalysisCondition{c}, periods, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Files) != 1 {
		t.Fatalf("initial cache failed: %+v", batch.Failures)
	}
	path := filepath.Join(m.profile, "screener-public-history", "binance", "raw", publicAlertSymbol("spot", "BTCUSDT")+"-m1.csv")
	original, _ := os.ReadFile(path)
	changed := append(append([]byte{}, original...), []byte("changed\n")...)
	if err = os.WriteFile(path, changed, 0600); err != nil {
		t.Fatal(err)
	}
	batch, err = l.preparePublicAlertData(context.Background(), "binance", []SavedAnalysisCondition{c}, periods, asOf)
	if err != nil || len(batch.Files) != 0 || len(batch.Failures) != 1 {
		t.Fatalf("corrupt public cache reused: %+v %v", batch, err)
	}
	current, _ := os.ReadFile(path)
	if string(current) != string(changed) {
		t.Fatal("corrupt source was overwritten")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = l.preparePublicAlertData(ctx, "binance", []SavedAnalysisCondition{c}, periods, asOf)
	if err == nil {
		t.Fatal("cancelled public preparation succeeded")
	}
	if len(fake.historyKeys) != 1 {
		t.Fatal(fmt.Sprintf("cancel/corruption made %d unwanted provider calls", len(fake.historyKeys)-1))
	}
}

func TestPublicAlertLatePublicationRetriesSameCutoff(t *testing.T) {
	for _, mode := range []string{"missing", "provisional"} {
		t.Run(mode, func(t *testing.T) {
			m := sourceEnrichmentManager(t, false)
			fake := &publicAlertFakeAdapter{price: 100, items: []domain.Instrument{{NativeID: "BTC", Market: "perpetual", Availability: "available"}}}
			if mode == "missing" {
				fake.pending = true
			} else {
				fake.provisional = true
			}
			l := newPublicAlertTestLive(t, m, "hyperliquid", fake)
			c := publicAlertTestCondition()
			c.Request.Conditions = c.Request.Conditions[:1]
			periods := map[string][]string{c.ID: {"m1"}}
			cutoff := time.Date(2026, 11, 5, 12, 0, 5, 0, time.UTC).UnixMilli()
			first, err := l.preparePublicAlertData(context.Background(), "hyperliquid", []SavedAnalysisCondition{c}, periods, cutoff)
			if err != nil || len(first.Files) != 0 || len(first.Failures) != 1 {
				t.Fatalf("late initial publication: %+v %v", first, err)
			}
			for _, failure := range first.Failures {
				if !errors.Is(failure, errLiveAlertsPendingBar) {
					t.Fatalf("late publication not retryable: %v", failure)
				}
			}
			count := len(fake.historyKeys)
			fake.pending, fake.provisional = false, false
			next, err := l.preparePublicAlertData(context.Background(), "hyperliquid", []SavedAnalysisCondition{c}, periods, cutoff)
			if err != nil || len(next.Files) != 1 || len(next.Failures) != 0 || len(fake.historyKeys) <= count {
				t.Fatalf("same-cutoff retry never repaired data: %+v %v", next, err)
			}
		})
	}
}

func TestPublicAlertCompanionsCoverEarlierBaseCloses(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	fake := &publicAlertFakeAdapter{price: 100, items: []domain.Instrument{{NativeID: "BTCUSDT", Market: "spot", Availability: "TRADING"}}}
	l := newPublicAlertTestLive(t, m, "binance", fake)
	c := SavedAnalysisCondition{ID: strings.Repeat("a", 32), Name: "Daily temporal with hourly SMA", Request: AnalysisRequest{Period: "d1", Conditions: []AnalysisCondition{{Operator: "remainedFor", Comparison: "above", Bars: 3, Left: AnalysisOperand{Kind: "field", Field: "sma_3", Timeframe: "h1"}, Right: AnalysisOperand{Kind: "number", Value: 0}}}}}
	cutoff := time.Date(2026, 11, 5, 12, 0, 5, 0, time.UTC).UnixMilli()
	batch, err := l.preparePublicAlertData(context.Background(), "binance", []SavedAnalysisCondition{c}, map[string][]string{c.ID: {"d1"}}, cutoff)
	if err != nil || len(batch.Files) != 2 || len(batch.Failures) != 0 {
		t.Fatalf("prepare temporal companions: %+v %v", batch, err)
	}
	work := t.TempDir()
	a := &analysisState{directory: batch.Directory, work: work, series: map[string]*analysisSeries{}, cache: newAnalysisRowCache(), request: c.Request, asOf: cutoff}
	var base *analysisSeries
	for _, name := range batch.Files {
		id := dataFileID(batch.Directory, name)
		s, err := indexAnalysisCSV(context.Background(), batch.Directory, name, filepath.Join(work, id+".rows"), func(float64) {})
		if err != nil {
			t.Fatal(err)
		}
		s.cache = a.cache
		a.series[id] = s
		if s.metadata.Timeframe == "d1" {
			base = s
		}
	}
	closed, err := base.closedCount(cutoff)
	if err != nil {
		t.Fatal(err)
	}
	matched, err := evaluateLiveAlertLatest(context.Background(), a, base, closed)
	if err != nil || !matched {
		t.Fatalf("hourly companion unavailable at previous daily closes: matched=%v %v", matched, err)
	}
	for i, key := range fake.historyKeys {
		if key.Timeframe == "1h" && fake.ranges[i][1]-fake.ranges[i][0] < 4*86400 {
			t.Fatalf("hourly companion only fetched its own SMA lookback: %v", fake.ranges[i])
		}
	}
}

func TestPublicAlertRawJournalRecoversBeforeAndAfterCSVPublication(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(fmt.Sprint(after), func(t *testing.T) {
			directory := t.TempDir()
			rawPath, metadataPath := filepath.Join(directory, "raw.csv"), filepath.Join(directory, "raw.json")
			old, next := []byte("old raw bytes"), []byte("new raw bytes")
			hash := func(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
			identity := publicAlertRawState{Source: "binance", Market: "spot", NativeID: "BTCUSDT", Period: "m1"}
			previous := identity
			previous.Checksum = hash(old)
			previous.CheckedThrough = 60
			state := identity
			state.Checksum = hash(next)
			state.CheckedThrough = 120
			encoded, _ := json.Marshal(previous)
			if err := publicAlertAtomicWrite(metadataPath, encoded); err != nil {
				t.Fatal(err)
			}
			data := old
			if after {
				data = next
			}
			if err := publicAlertAtomicWrite(rawPath, data); err != nil {
				t.Fatal(err)
			}
			journal, _ := json.Marshal(publicAlertRawJournal{State: state, PreviousChecksum: hash(old)})
			if err := publicAlertAtomicWrite(metadataPath+".pending", journal); err != nil {
				t.Fatal(err)
			}
			if err := publicAlertRecoverRaw(rawPath, metadataPath, identity); err != nil {
				t.Fatal(err)
			}
			saved, _ := os.ReadFile(metadataPath)
			var got publicAlertRawState
			_ = json.Unmarshal(saved, &got)
			want := previous
			if after {
				want = state
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("interrupted raw/state pair not recovered: %+v want %+v", got, want)
			}
			if _, err := os.Stat(metadataPath + ".pending"); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("recovered journal retained")
			}
		})
	}
}

func TestPublicAlertWarmupSupportsEveryReviewedIndicator(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	plans := map[string]publicAlertPlan{}
	for _, indicator := range m.Indicators().Indicators {
		parts := []string{indicator.ID}
		for _, parameter := range indicator.Parameters {
			parts = append(parts, strconv.FormatFloat(parameter.Default, 'g', -1, 64))
		}
		fields := []string{strings.Join(parts, "_")}
		switch indicator.ID {
		case "macd":
			fields = []string{"macd_12_26", "macd_signal_12_26_9", "macd_histogram_12_26_9"}
		case "bb":
			fields = []string{"bb_lower_20_2", "bb_middle_20_2", "bb_upper_20_2"}
		case "donchian":
			fields = []string{"donchian_lower_20", "donchian_upper_20"}
		case "stochastic":
			fields = []string{"stochastic_k_14_3_3", "stochastic_d_14_3_3"}
		case "ichimoku":
			fields = []string{"tenkan_9", "kijun_26", "senkou_a_9_26", "senkou_b_52"}
		}
		plans[indicator.ID] = publicAlertPlan{Fields: fields, Extra: 1}
	}
	worker, prepared, err := startPublicAlertWorker(context.Background(), m.runtime, plans)
	if err != nil {
		t.Fatal(err)
	}
	defer worker.close()
	if len(prepared) != len(plans) {
		t.Fatal("some reviewed indicator plans omitted")
	}
	// Cloud recipes share columns with individual Tenkan/Kijun recipes. The
	// one-pass worker must publish each field exactly once, preserving all values.
	work := t.TempDir()
	input, output := filepath.Join(work, "input.csv"), filepath.Join(work, "output.csv")
	bars := []domain.Candle{}
	for i := 0; i < prepared["ichimoku"].Bars; i++ {
		bars = append(bars, domain.Candle{Time: int64(1704067200 + i*60), Open: 100, High: 102, Low: 98, Close: 101, Volume: 3})
	}
	data, _ := publicAlertRawCSV(bars, false)
	if err = os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = worker.enrich("ichimoku", input, output); err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(encoded))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, field := range rows[0] {
		if seen[field] {
			t.Fatalf("duplicate recovered indicator column: %s", field)
		}
		seen[field] = true
	}
}

func TestPublicAlertTerminalProviderFailureStopsFanout(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	for _, kind := range []string{"restricted", "rateLimited", "auth"} {
		t.Run(kind, func(t *testing.T) {
			fake := &publicAlertFakeAdapter{price: 100, fail: &exchange.Error{Kind: kind, Message: "synthetic public refusal"}}
			for i := 0; i < 200; i++ {
				fake.items = append(fake.items, domain.Instrument{NativeID: fmt.Sprintf("COIN%dUSDT", i), Market: "spot", Availability: "TRADING"})
			}
			l := newPublicAlertTestLive(t, m, "binance", fake)
			c := publicAlertTestCondition()
			c.Request.Conditions = c.Request.Conditions[:1]
			batch, err := l.preparePublicAlertData(context.Background(), "binance", []SavedAnalysisCondition{c}, map[string][]string{c.ID: {"m1"}}, time.Date(2026, 11, 5, 12, 0, 5, 0, time.UTC).UnixMilli())
			if err == nil || exchange.ErrorKind(err) != kind || len(fake.historyKeys) != 1 || len(batch.Files) != 0 {
				t.Fatalf("terminal refusal fanned out: calls=%d err=%v", len(fake.historyKeys), err)
			}
		})
	}
}

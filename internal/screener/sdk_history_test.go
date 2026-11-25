package screener

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
)

type sdkHistoryCall struct {
	key, symbol, timeframe, output string
	from, to                       int64
}
type fakeSDKHistory struct {
	mu         sync.Mutex
	key        string
	configured bool
	calls      []sdkHistoryCall
	export     func(context.Context, sdkHistoryCall) (int64, error)
}

func (p *fakeSDKHistory) HistoricalSource() (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.key, p.configured
}
func (p *fakeSDKHistory) write(ctx context.Context, key, symbol string, from, to int64, output, timeframe string) (int64, error) {
	call := sdkHistoryCall{key: key, symbol: symbol, from: from, to: to, output: output, timeframe: timeframe}
	p.mu.Lock()
	if !p.configured || key != p.key {
		p.mu.Unlock()
		return 0, errors.New("SDK account changed")
	}
	p.calls = append(p.calls, call)
	fn := p.export
	p.mu.Unlock()
	if fn != nil {
		return fn(ctx, call)
	}
	return 1, os.WriteFile(output, []byte(fmt.Sprintf("timestamp,bidPrice\n%d,10\n", from)), 0600)
}
func (p *fakeSDKHistory) ExportTicks(ctx context.Context, key, symbol string, from, to int64, output string) (int64, error) {
	return p.write(ctx, key, symbol, from, to, output, "tick")
}
func (p *fakeSDKHistory) ExportCandles(ctx context.Context, key, symbol string, from, to int64, output string) (int64, error) {
	return p.write(ctx, key, symbol, from, to, output, "d1")
}
func (p *fakeSDKHistory) ExportHourlyCandles(ctx context.Context, key, symbol string, from, to int64, output string, full bool) (int64, error) {
	period := "h1-native"
	if full {
		period = "h1"
	}
	return p.write(ctx, key, symbol, from, to, output, period)
}
func newFakeSDKHistory() *fakeSDKHistory {
	return &fakeSDKHistory{key: strings.Repeat("a", 64), configured: true}
}
func sdkHistorySession(t *testing.T, provider *fakeSDKHistory) (context.Context, *providerSession) {
	t.Helper()
	ctx, cleanup, err := withProviderSessionWithHistory(context.Background(), t.TempDir(), provider)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	state := sessionFromContext(ctx)
	state.limiter.interval = 0
	state.limiter.persist = func(providerRateState) error { return nil }
	return ctx, state
}
func sdkTickRequest() providerEvent {
	return providerEvent{RequestID: 1, Operation: "history", Symbol: "btcusd", Timeframe: "tick", From: 1704067200000, To: 1704070800000}
}

func TestSDKHistorySessionFreezesOpaqueIdentityAndConstructorInstallsProvider(t *testing.T) {
	provider := newFakeSDKHistory()
	_, state := sdkHistorySession(t, provider)
	data, err := json.Marshal(state)
	if err != nil || !strings.Contains(string(data), `"sdk":{"sourceKey":"`+provider.key+`"}`) || strings.Contains(string(data), "configured") || strings.Contains(string(data), "export") {
		t.Fatal("SDK session exposed more than its opaque source identity", string(data), err)
	}
	m := newManagerWithDukascopyHistory(t.TempDir(), func() (workerRuntime, error) { return workerRuntime{}, errors.New("explicit offline runtime") }, provider)
	t.Cleanup(m.Close)
	if m.dukascopyHistory != provider {
		t.Fatal("constructor did not install SDK provider before queue restoration")
	}
	provider.mu.Lock()
	provider.key = strings.Repeat("b", 64)
	provider.mu.Unlock()
	if state.SDK.SourceKey != strings.Repeat("a", 64) {
		t.Fatal("batch source identity followed a later account change")
	}
	provider.key = "invalid"
	if _, cleanup, err := withProviderSessionWithHistory(context.Background(), t.TempDir(), provider); err == nil {
		cleanup()
		t.Fatal("configured SDK accepted an invalid source identity")
	}
}

func TestSDKHistoryValidatesSourcePeriodsAndClosedBoundsBeforeExport(t *testing.T) {
	provider := newFakeSDKHistory()
	ctx, _ := sdkHistorySession(t, provider)
	r := newProviderReplies(ctx, &bytes.Buffer{}, nil)
	defer r.close()
	valid := sdkTickRequest()
	for _, symbol := range []string{"btcusd", "xauusd", "xagusd", "ethusd", "usa500idxusd", "xauxag"} {
		request := valid
		request.Symbol = symbol
		if err := r.validateSDKHistoryRequest(request); err != nil {
			t.Fatal(symbol, err)
		}
	}
	daily := valid
	daily.Timeframe = "d1"
	daily.To = daily.From + 366*sdkHistoryDay
	if err := r.validateSDKHistoryRequest(daily); err != nil {
		t.Fatal(err)
	}
	for _, symbol := range []string{"0700hkhkd", "eurusd", "xauusd", "ethusd"} {
		request := daily
		request.Symbol = symbol
		if err := r.validateSDKHistoryRequest(request); err != nil {
			t.Fatal("actual native catalog daily source rejected", symbol, err)
		}
	}
	for _, period := range []string{"h1", "h1-native"} {
		request := valid
		request.Timeframe, request.To = period, request.From+1000*sdkHistoryHour
		if err := r.validateSDKHistoryRequest(request); err != nil {
			t.Fatal("bounded hourly contract rejected", period, err)
		}
	}
	for name, change := range map[string]func(*providerEvent){
		"unknown symbol":     func(e *providerEvent) { e.Symbol = "eurusd" },
		"uppercase symbol":   func(e *providerEvent) { e.Symbol = "BTCUSD" },
		"unsupported period": func(e *providerEvent) { e.Timeframe = "h4" },
		"misaligned hour":    func(e *providerEvent) { e.From++; e.To++ },
		"multiple hours":     func(e *providerEvent) { e.To += sdkHistoryHour },
		"open hour": func(e *providerEvent) {
			e.From = time.Now().UnixMilli() / sdkHistoryHour * sdkHistoryHour
			e.To = e.From + sdkHistoryHour
		},
		"negative timestamp":   func(e *providerEvent) { e.From = -sdkHistoryHour; e.To = 0 },
		"reversed range":       func(e *providerEvent) { e.To = e.From },
		"calculated daily leg": func(e *providerEvent) { e.Timeframe = "d1"; e.Symbol = "btcxau"; e.To = e.From + sdkHistoryDay },
		"native hourly other":  func(e *providerEvent) { e.Timeframe = "h1-native"; e.Symbol = "xauusd" },
		"oversized hourly":     func(e *providerEvent) { e.Timeframe = "h1"; e.To = e.From + 1001*sdkHistoryHour },
		"misaligned daily":     func(e *providerEvent) { e.Timeframe = "d1"; e.To = e.From + sdkHistoryDay - 1 },
		"oversized daily":      func(e *providerEvent) { e.Timeframe = "d1"; e.To = e.From + 367*sdkHistoryDay },
		"open daily": func(e *providerEvent) {
			e.Timeframe = "d1"
			e.From = time.Now().UnixMilli() / sdkHistoryDay * sdkHistoryDay
			e.To = e.From + sdkHistoryDay
		},
	} {
		t.Run(name, func(t *testing.T) {
			request := valid
			change(&request)
			if err := r.handle(request); err == nil {
				t.Fatal("invalid request reached SDK", request)
			}
		})
	}
	if len(provider.calls) != 0 {
		t.Fatal("invalid requests invoked SDK", provider.calls)
	}
}

func TestSDKHourlyOwnerRepliesEnforceDistinctVerifiedAndNativePolicies(t *testing.T) {
	provider := newFakeSDKHistory()
	provider.export = sdkExportFixture
	ctx, _ := sdkHistorySession(t, provider)
	for _, period := range []string{"h1", "h1-native"} {
		var output bytes.Buffer
		r := newProviderReplies(ctx, &output, nil)
		request := sdkTickRequest()
		request.Timeframe = period
		if err := r.handle(request); err != nil {
			t.Fatal(err)
		}
		r.wg.Wait()
		var reply struct {
			Error, SessionPolicy, Path string
			Rows                       int64
		}
		if err := json.Unmarshal(output.Bytes(), &reply); err != nil {
			t.Fatal(err)
		}
		wanted := "verified-full-hour-v2"
		if period == "h1-native" {
			wanted = "provider-native-h1-v2"
		}
		if reply.Error != "" || reply.SessionPolicy != wanted || reply.Rows != 1 {
			t.Fatal("hourly owner contract lost", output.String())
		}
		r.close()
	}
	if len(provider.calls) != 2 || provider.calls[0].timeframe != "h1" || provider.calls[1].timeframe != "h1-native" {
		t.Fatal(provider.calls)
	}
}
func TestSDKHistoryOwnerReplyBypassesOnlyHTTPDenialAndCleansExports(t *testing.T) {
	provider := newFakeSDKHistory()
	ctx, state := sdkHistorySession(t, provider)
	denied := time.Now().Add(time.Hour).UnixMilli()
	state.limiter.state.DeniedTill = denied
	var output bytes.Buffer
	r := newProviderReplies(ctx, &output, nil)
	if err := r.handle(sdkTickRequest()); err != nil {
		t.Fatal(err)
	}
	r.wg.Wait()
	var reply struct {
		Type      string `json:"type"`
		RequestID uint64 `json:"requestId"`
		Path      string `json:"path"`
		Rows      int64  `json:"rows"`
		Error     string `json:"error"`
	}
	if err := json.Unmarshal(output.Bytes(), &reply); err != nil || reply.Type != "provider-reply" || reply.RequestID != 1 || reply.Error != "" || reply.Rows != 1 || filepath.Dir(reply.Path) != state.Directory {
		t.Fatal("invalid SDK reply", output.String(), err)
	}
	if _, err := os.Stat(reply.Path); err != nil {
		t.Fatal(err)
	}
	if state.limiter.state.DeniedTill != denied {
		t.Fatal("SDK bypass cleared the HTTP denial")
	}
	if err := state.limiter.permit(context.Background(), nil); err == nil {
		t.Fatal("SDK permit opened the blocked HTTP transport")
	}
	r.close()
	if _, err := os.Stat(reply.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("reply cleanup retained its exported file", err)
	}
}

func TestSDKHistoryConsumedExportsDoNotAccumulateCleanupState(t *testing.T) {
	provider := newFakeSDKHistory()
	ctx, _ := sdkHistorySession(t, provider)
	var output bytes.Buffer
	r := newProviderReplies(ctx, &output, nil)
	defer r.close()
	for index := range 32 {
		request := sdkTickRequest()
		request.RequestID = uint64(index + 1)
		if err := r.handle(request); err != nil {
			t.Fatal(err)
		}
		r.wg.Wait()
		var reply struct {
			Path  string `json:"path"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(output.Bytes(), &reply); err != nil || reply.Error != "" {
			t.Fatal(output.String(), err)
		}
		if err := os.Remove(reply.Path); err != nil {
			t.Fatal(err)
		}
		output.Reset()
		if len(r.temporary) > 1 {
			t.Fatal("consumed SDK exports accumulated cleanup state", len(r.temporary))
		}
	}
}

func TestSDKHistoryRejectsChangedSourceUnsafeFileAndUnboundedExport(t *testing.T) {
	for _, mode := range []string{"changed account", "symlink", "new inode", "oversized file", "too many rows", "negative rows", "empty file", "provider failure"} {
		t.Run(mode, func(t *testing.T) {
			provider := newFakeSDKHistory()
			ctx, state := sdkHistorySession(t, provider)
			outside := filepath.Join(t.TempDir(), "untouched.csv")
			if err := os.WriteFile(outside, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			provider.export = func(_ context.Context, call sdkHistoryCall) (int64, error) {
				if err := os.WriteFile(call.output, []byte("timestamp,bidPrice\n1704067200000,10\n"), 0600); err != nil {
					return 0, err
				}
				switch mode {
				case "changed account":
					provider.mu.Lock()
					provider.key = strings.Repeat("b", 64)
					provider.mu.Unlock()
				case "symlink":
					if err := os.Remove(call.output); err != nil {
						return 0, err
					}
					return 1, os.Symlink(outside, call.output)
				case "new inode":
					temporary := call.output + ".next"
					if err := os.WriteFile(temporary, []byte("replacement"), 0600); err != nil {
						return 0, err
					}
					return 1, os.Rename(temporary, call.output)
				case "oversized file":
					return 1, os.Truncate(call.output, sdkHistoryMaxBytes+1)
				case "too many rows":
					return sdkHistoryMaxRows + 1, nil
				case "negative rows":
					return -1, nil
				case "empty file":
					return 0, os.Truncate(call.output, 0)
				case "provider failure":
					return 0, errors.New("SDK history failed")
				}
				return 1, nil
			}
			r := newProviderReplies(ctx, &bytes.Buffer{}, nil)
			defer r.close()
			filename, _, err := r.exportSDKHistory(sdkTickRequest())
			if err == nil || filename != "" {
				t.Fatal("invalid export was published", filename, err)
			}
			entries, err := os.ReadDir(state.Directory)
			if err != nil || len(entries) != 0 {
				t.Fatal("failed SDK export retained temporary files", entries, err)
			}
			if data, err := os.ReadFile(outside); err != nil || string(data) != "original" {
				t.Fatal("SDK cleanup changed an unowned file", string(data), err)
			}
		})
	}
}

func TestSDKHistoryCloseWaitsForLateExportAndRejectsItsReply(t *testing.T) {
	provider := newFakeSDKHistory()
	ctx, state := sdkHistorySession(t, provider)
	started, release := make(chan struct{}), make(chan struct{})
	provider.export = func(_ context.Context, call sdkHistoryCall) (int64, error) {
		close(started)
		<-release
		return 1, os.WriteFile(call.output, []byte("timestamp,bidPrice\n1704067200000,10\n"), 0600)
	}
	var output bytes.Buffer
	r := newProviderReplies(ctx, &output, nil)
	if err := r.handle(sdkTickRequest()); err != nil {
		t.Fatal(err)
	}
	<-started
	done := make(chan struct{})
	go func() { r.close(); close(done) }()
	<-r.ctx.Done()
	select {
	case <-done:
		t.Fatal("coordinator returned while SDK export was in flight")
	default:
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled export did not close")
	}
	if strings.Contains(output.String(), `"path"`) || !strings.Contains(output.String(), "context canceled") {
		t.Fatal("late export published a success reply", output.String())
	}
	if files, err := os.ReadDir(state.Directory); err != nil || len(files) != 0 {
		t.Fatal("cancelled export survived close", files, err)
	}
}

func TestSDKHistoryShares429AndLiveAlertsDoNotStopOnHTTP403(t *testing.T) {
	provider := newFakeSDKHistory()
	ctx, state := sdkHistorySession(t, provider)
	state.limiter.state.DeniedTill = time.Now().Add(time.Hour).UnixMilli()
	if liveAlertProviderStopped(ctx, errors.New("Dukascopy HTTP 403 shared cooldown")) {
		t.Fatal("HTTP-only refusal stopped SDK-capable peers")
	}
	retryAt := time.Now().Add(11 * time.Minute).Unix()
	provider.export = func(context.Context, sdkHistoryCall) (int64, error) {
		return 0, &exchange.Error{Kind: "rateLimited", RetryAt: retryAt, Message: "SDK provider rate limit"}
	}
	r := newProviderReplies(ctx, &bytes.Buffer{}, nil)
	defer r.close()
	if _, _, err := r.exportSDKHistory(sdkTickRequest()); err == nil {
		t.Fatal("SDK 429 was ignored")
	}
	if state.limiter.state.LimitedTill < retryAt*1000 {
		t.Fatal("SDK RetryAt did not pause shared limiter", state.limiter.state)
	}
	if !liveAlertProviderStopped(ctx, errors.New("other SDK failure")) {
		t.Fatal("global429 did not stop alert batch")
	}
	if _, _, err := r.exportSDKHistory(sdkTickRequest()); err == nil || !strings.Contains(err.Error(), "Retry-After") || len(provider.calls) != 1 {
		t.Fatal("SDK bypassed shared429", err, provider.calls)
	}
}

func sdkExportFixture(_ context.Context, call sdkHistoryCall) (int64, error) {
	if call.timeframe == "d1" || call.timeframe == "h1" || call.timeframe == "h1-native" {
		data := "timestamp,open,high,low,close\n"
		rows := int64(0)
		step := sdkHistoryDay
		if call.timeframe != "d1" {
			step = sdkHistoryHour
		}
		for at := call.from; at < call.to; at += step {
			data += fmt.Sprintf("%d,10,12,9,11\n", at)
			rows++
		}
		return rows, os.WriteFile(call.output, []byte(data), 0600)
	}
	data := "timestamp,askPrice,bidPrice,askVolume,bidVolume\n"
	if call.from%sdkHistoryDay != 0 {
		return 0, os.WriteFile(call.output, []byte(data), 0600)
	}
	prices := []int{10, 20, 30}
	if call.symbol == "xauusd" {
		prices = []int{2, 4, 5}
	}
	for index, price := range prices {
		data += fmt.Sprintf("%d,%d,%d,0,0\n", call.from+int64(index)*100, price+1, price)
	}
	return 3, os.WriteFile(call.output, []byte(data), 0600)
}

func disallowSDKTestHTTP(t *testing.T) {
	t.Helper()
	preload := filepath.Join(t.TempDir(), "no-http.cjs")
	script := `global.fetch=async()=>{throw Error('SDK history attempted an HTTP request');};`
	if err := os.WriteFile(preload, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NODE_OPTIONS", "--require="+preload)
}

func TestSDKHistoryRealNodeRatioPublishesAndReusesAccountScopedQuotesWhileHTTPDenied(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	disallowSDKTestHTTP(t)
	provider := newFakeSDKHistory()
	provider.export = sdkExportFixture
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ctx, cleanup, err := withProviderSessionWithHistory(ctx, m.profile, provider)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	state := sessionFromContext(ctx)
	state.limiter.interval = 0
	state.limiter.state.DeniedTill = time.Now().Add(time.Hour).UnixMilli()
	if err = state.limiter.saveLocked(); err != nil {
		t.Fatal(err)
	}
	denied := state.limiter.state.DeniedTill
	job := Job{ID: strings.Repeat("a", 32), Symbol: "btcxau", Timeframe: "d1", From: "2024-01-01", To: "2024-01-02", AsOf: 1704153600000, Status: "running"}
	m.batchDirectory = m.directory
	m.jobs = []Job{job}
	m.publication = map[string]string{}
	output, _, err := m.download(ctx, 0, job, m.directory, 1)
	if err != nil {
		t.Fatal("real SDK owner-pipe ratio failed", err, m.Info())
	}
	data, err := os.ReadFile(output)
	if err != nil || string(data) != "timestamp,open,high,low,close\n1704067200000,5,6,5,6\n" {
		t.Fatal("SDK sources did not form the published ratio", string(data), err)
	}
	page, err := m.Files(0, 10)
	if err != nil || len(page.Files) != 1 || page.Files[0].Provenance == nil || page.Files[0].Provenance.HistorySource != "jforex-sdk" || !page.Files[0].Synthetic {
		t.Fatal("published SDK dataset lost provenance", page, err)
	}
	key := domain.FeedKey{Exchange: "dukascopy", Market: "cfd", Instrument: "BTCXAU", Timeframe: "1d"}
	if bars, err := m.SyntheticHistory(ctx, key, 0, 0, 0); err != nil || len(bars) != 1 || bars[0].Close != 6 || !bars[0].VolumeUnavailable {
		t.Fatal("published SDK ratio was not chart-readable", bars, err)
	}
	provider.mu.Lock()
	calls := append([]sdkHistoryCall(nil), provider.calls...)
	provider.mu.Unlock()
	if len(calls) != 48 {
		t.Fatal("ratio did not request exactly both closed-day source legs", len(calls))
	}
	for _, call := range calls {
		if call.symbol != "btcusd" && call.symbol != "xauusd" || call.key != provider.key || call.timeframe != "tick" {
			t.Fatal("SDK ratio requested the wrong source", call)
		}
	}
	// Lose only this test's published CSV. Rebuilding it must use the verified
	// account-scoped hourly quote cache, without another SDK or HTTP request.
	if err = os.Remove(output); err != nil {
		t.Fatal(err)
	}
	job.ID = strings.Repeat("b", 32)
	m.jobs = []Job{job}
	m.publication = map[string]string{}
	if _, _, err = m.download(ctx, 0, job, m.directory, 1); err != nil {
		t.Fatal("cached SDK rebuild failed", err)
	}
	provider.mu.Lock()
	count := len(provider.calls)
	provider.mu.Unlock()
	if count != 48 || state.limiter.state.DeniedTill != denied {
		t.Fatal("SDK cache reuse refetched sources or cleared HTTP denial", count, state.limiter.state)
	}
}

func TestSDKHistoryRestoredNativeQueueUsesProviderBeforeAutomaticResume(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	disallowSDKTestHTTP(t)
	job := Job{ID: strings.Repeat("c", 32), Symbol: "btcusd", Timeframe: "d1", From: "2024-01-01", To: "2024-01-02", AsOf: 1704153600000, Status: "queued"}
	m.mu.Lock()
	m.batchDirectory = m.directory
	m.jobs = []Job{job}
	m.publication = map[string]string{}
	saved := m.saveDownloadsLocked()
	m.mu.Unlock()
	if !saved {
		t.Fatal("could not seed durable native queue", m.stateError)
	}
	runtime := m.runtime
	profile := m.profile
	m.Close()
	provider := newFakeSDKHistory()
	provider.export = sdkExportFixture
	restored := newManagerWithDukascopyHistory(profile, func() (workerRuntime, error) { return runtime, nil }, provider)
	t.Cleanup(restored.Close)
	info := waitFinished(t, restored)
	if len(info.Jobs) != 1 || info.Jobs[0].Status != "completed" {
		t.Fatal("automatic resume missed the installed SDK provider", info)
	}
	data, err := os.ReadFile(info.Jobs[0].OutputPath)
	if err != nil || strings.Contains(string(data), "volume") {
		t.Fatal("native SDK BID candles invented traded volume", string(data), err)
	}
	page, err := restored.Files(0, 10)
	if err != nil || len(page.Files) != 1 || page.Files[0].Synthetic || page.Files[0].Provenance == nil || page.Files[0].Provenance.Volume != "unavailable" || page.Files[0].Provenance.HistorySource != "jforex-sdk" {
		t.Fatal("native SDK provenance or identity changed", page, err)
	}
	provider.mu.Lock()
	calls := append([]sdkHistoryCall(nil), provider.calls...)
	provider.mu.Unlock()
	if len(calls) != 1 || calls[0].symbol != "btcusd" || calls[0].timeframe != "d1" {
		t.Fatal("native resume used a ratio or HTTP path", calls)
	}
	restored.runtime.node = "must-not-start-volume-worker"
	err = restored.enrichFile(context.Background(), 0, restored.directory, EnrichmentJob{Name: filepath.Base(info.Jobs[0].OutputPath)}, EnrichmentRequest{Indicator: "mfi"})
	if err == nil || !strings.Contains(err.Error(), "volume-based indicators") {
		t.Fatal("native SDK no-volume data reached the volume worker", err)
	}
	if after, err := os.ReadFile(info.Jobs[0].OutputPath); err != nil || !bytes.Equal(after, data) {
		t.Fatal("rejected native SDK volume enrichment changed the source", err)
	}
}

func TestSDKHistoryAccountChangeAfterWorkerCompletionPreservesPublications(t *testing.T) {
	for _, retained := range []bool{false, true} {
		t.Run(fmt.Sprintf("retained=%t", retained), func(t *testing.T) {
			m := sourceEnrichmentManager(t, false)
			provider := newFakeSDKHistory()
			provider.export = sdkExportFixture
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			ctx, cleanup, err := withProviderSessionWithHistory(ctx, m.profile, provider)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			state := sessionFromContext(ctx)
			state.limiter.interval = 0
			original := "timestamp,open,high,low,close\n1704067200000,5,6,5,6\n"
			name := "btcxau-d1-2024-01-01-2024-01-02.csv"
			if retained {
				provenance := testRatioProvenance("btcxau")
				provenance.HistorySource = "jforex-sdk"
				writeProvenanceCSV(t, m, name, original, provenance)
				metadata, e := readFileMetadata(m.directory, name)
				if e != nil {
					t.Fatal(e)
				}
				metadata.FirstTimestamp, metadata.LastTimestamp = 1704067200000, 1704067200000
				metadata.Coverage = []dataRange{{From: 1704067200000, To: 1704153600000}}
				metadata.CheckedThrough = 1704153600000
				if e = writeFileMetadata(m.directory, name, metadata); e != nil {
					t.Fatal(e)
				}
			}
			var before fileMetadata
			if retained {
				before, err = readFileMetadata(m.directory, name)
				if err != nil {
					t.Fatal(err)
				}
			}
			ready, release := filepath.Join(t.TempDir(), "ready"), filepath.Join(t.TempDir(), "release")
			readyJSON, _ := json.Marshal(ready)
			releaseJSON, _ := json.Marshal(release)
			preload := filepath.Join(t.TempDir(), "before-publication.cjs")
			script := fmt.Sprintf(`const fs=require('node:fs');global.fetch=async()=>{throw Error('unexpected HTTP');};
const write=process.stdout.write.bind(process.stdout),wait=new Int32Array(new SharedArrayBuffer(4));
process.stdout.write=function(chunk,...args){let event;try{event=JSON.parse(String(chunk));}catch{}
if(event?.type==='done'){fs.writeFileSync(%s,'ready');const deadline=Date.now()+10000;
while(!fs.existsSync(%s)){if(Date.now()>deadline)throw Error('publication test gate timed out');Atomics.wait(wait,0,0,5);}}
return write(chunk,...args);};`, readyJSON, releaseJSON)
			if err = os.WriteFile(preload, []byte(script), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("NODE_OPTIONS", "--require="+preload)
			job := Job{ID: strings.Repeat("d", 32), Symbol: "btcxau", Timeframe: "d1", From: "2024-01-01", To: "2024-01-02", AsOf: 1704153600000, Status: "running"}
			m.batchDirectory = m.directory
			m.jobs = []Job{job}
			m.publication = map[string]string{}
			done := make(chan error, 1)
			go func() { _, _, err := m.download(ctx, 0, job, m.directory, 1); done <- err }()
			deadline := time.After(5 * time.Second)
			ticker := time.NewTicker(5 * time.Millisecond)
			defer ticker.Stop()
		waiting:
			for {
				select {
				case err := <-done:
					t.Fatal("worker exited before publication barrier", err)
				case <-deadline:
					t.Fatal("worker never reached its completed CSV barrier")
				case <-ticker.C:
					if _, err := os.Stat(ready); err == nil {
						break waiting
					}
				}
			}
			provider.mu.Lock()
			provider.key = strings.Repeat("b", 64)
			calls := len(provider.calls)
			provider.mu.Unlock()
			if retained && calls != 0 || !retained && calls != 48 {
				t.Fatal("test did not exercise the expected cached/export path", retained, calls)
			}
			if err = os.WriteFile(release, []byte("release"), 0600); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), "account source changed") {
					t.Fatal("changed SDK account published the completed CSV", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("worker did not close after publication barrier")
			}
			names, err := csvNames(m.directory)
			if err != nil {
				t.Fatal(err)
			}
			if !retained && len(names) != 0 {
				t.Fatal("old SDK account published a new CSV", names)
			}
			if retained {
				data, e := os.ReadFile(filepath.Join(m.directory, name))
				after, metadataErr := readFileMetadata(m.directory, name)
				beforeJSON, _ := json.Marshal(before)
				afterJSON, _ := json.Marshal(after)
				if e != nil || metadataErr != nil || string(data) != original || !bytes.Equal(beforeJSON, afterJSON) || len(names) != 1 {
					t.Fatal("changed account modified retained CSV or metadata", string(data), names, e, metadataErr)
				}
			}
		})
	}
}

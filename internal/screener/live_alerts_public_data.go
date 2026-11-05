package screener

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type publicAlertAsset struct {
	Symbol   string `json:"symbol"`
	NativeID string `json:"nativeId"`
	Market   string `json:"market"`
	Name     string `json:"name"`
}

type publicAlertBatch struct {
	Directory string
	Assets    []publicAlertAsset
	Files     map[string]string
	Failures  map[string]error
}

type publicAlertCatalog struct {
	items []domain.Instrument
	at    time.Time
}

// Owned by LiveAlerts, so discovery and HTTP request pacing survive individual
// checks. Construction, settings and Info never contact public exchanges.
type publicAlertData struct {
	mu       sync.Mutex
	adapters map[string]exchange.Adapter
	catalogs map[string]publicAlertCatalog
}

var publicAlertIntervals = map[string]string{"m1": "1m", "m5": "5m", "m15": "15m", "h1": "1h", "h4": "4h", "d1": "1d", "w1": "1w"}

type publicAlertPlan struct {
	Fields []string `json:"fields"`
	Extra  int      `json:"extra"`
	Bars   int      `json:"bars"`
}

// Planning follows the saved rules to their actual operand periods. Companion
// periods remain data dependencies and never become independent alert targets.
func publicAlertPlans(conditions []SavedAnalysisCondition, periods map[string][]string) (map[string]publicAlertPlan, error) {
	fields := map[string]map[string]bool{}
	plans := map[string]publicAlertPlan{}
	add := func(period, field string, extra int) error {
		if publicAlertIntervals[period] == "" {
			return fmt.Errorf("public exchange alerts do not support saved period %q", period)
		}
		field = strings.ToLower(strings.TrimSpace(field))
		if field == "price" {
			field = "close"
		}
		if field == "" {
			return nil
		}
		if fields[period] == nil {
			fields[period] = map[string]bool{}
		}
		fields[period][field] = true
		plan := plans[period]
		plan.Extra = max(plan.Extra, extra)
		plans[period] = plan
		return nil
	}
	for _, saved := range conditions {
		warm := 1
		for _, condition := range saved.Request.Conditions {
			if condition.Operator == "occurredWithin" || condition.Operator == "remainedFor" {
				warm = max(warm, condition.Bars)
			}
		}
		for _, base := range periods[saved.ID] {
			if err := add(base, "close", warm); err != nil {
				return nil, err
			}
			for _, condition := range saved.Request.Conditions {
				op := condition.Operator
				if op == "occurredWithin" || op == "remainedFor" {
					op = condition.Comparison
				}
				if (op == "highest" || op == "lowest") && condition.Right.Kind == "number" {
					if err := add(base, "close", warm+condition.Bars); err != nil {
						return nil, err
					}
				}
				for operandIndex, operand := range conditionOperands(condition) {
					if operand.Kind != "field" {
						continue
					}
					period := operand.Timeframe
					if period == "" || period == "same" {
						period = base
					}
					extra := warm
					if period != base && publicAlertIntervals[period] != "" {
						baseDuration := exchange.TimeframeSeconds(publicAlertIntervals[base])
						duration := exchange.TimeframeSeconds(publicAlertIntervals[period])
						// Evaluation visits preceding BASE closes. An intraday manual
						// check may also lag its latest base close by almost one base
						// interval. Express that horizon in companion candle units.
						extra = int((baseDuration*int64(warm+1) + duration - 1) / duration)
					}
					op := condition.Operator
					if op == "occurredWithin" || op == "remainedFor" {
						op = condition.Comparison
					}
					window := max(1, operand.Window)
					if (op == "highest" || op == "lowest") && operandIndex == 1 {
						window = condition.Bars + 1
					}
					if err := add(period, operand.Field, extra+operand.Offset+window-1); err != nil {
						return nil, err
					}
				}
			}
			if trade := saved.Request.TradeSimulation; trade != nil {
				if err := add(base, trade.StopLoss, trade.StopLossBarsAgo+1); err != nil {
					return nil, err
				}
				if trade.TakeProfitMultiple == 0 {
					if err := add(base, trade.TakeProfit, 1); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	for period, set := range fields {
		plan := plans[period]
		for field := range set {
			plan.Fields = append(plan.Fields, field)
		}
		sort.Strings(plan.Fields)
		plans[period] = plan
	}
	return plans, nil
}

func (l *LiveAlerts) publicAlertCatalog(ctx context.Context, source string) (exchange.Adapter, []domain.Instrument, error) {
	if source != "binance" && source != "bybit" && source != "hyperliquid" {
		return nil, nil, errors.New("unsupported public alert source")
	}
	l.mu.Lock()
	if l.publicData == nil {
		l.publicData = &publicAlertData{}
	}
	data := l.publicData
	l.mu.Unlock()
	data.mu.Lock()
	if data.adapters == nil {
		data.adapters = map[string]exchange.Adapter{}
	}
	if data.catalogs == nil {
		data.catalogs = map[string]publicAlertCatalog{}
	}
	a := data.adapters[source]
	if a == nil {
		var err error
		a, err = exchange.New(source)
		if err != nil {
			data.mu.Unlock()
			return nil, nil, err
		}
		data.adapters[source] = a
	}
	cached := data.catalogs[source]
	data.mu.Unlock()
	if len(cached.items) > 0 && time.Since(cached.at) < 30*time.Minute {
		return a, cached.items, nil
	}
	items, err := a.Instruments(ctx)
	if err != nil {
		return a, nil, err
	}
	partial := false
	for _, item := range items {
		partial = partial || item.ProviderMetadata["catalogWarning"] != ""
	}
	if !partial {
		data.mu.Lock()
		data.catalogs[source] = publicAlertCatalog{items: items, at: time.Now()}
		data.mu.Unlock()
	}
	return a, items, nil
}

func publicAlertSymbol(market, native string) string {
	digest := sha256.Sum256([]byte(native))
	return market + hex.EncodeToString(digest[:])
}

func (l *LiveAlerts) preparePublicAlertData(ctx context.Context, source string, conditions []SavedAnalysisCondition, periods map[string][]string, asOf int64) (publicAlertBatch, error) {
	batch := publicAlertBatch{Files: map[string]string{}, Failures: map[string]error{}}
	plans, err := publicAlertPlans(conditions, periods)
	if err != nil {
		return batch, err
	}
	if len(plans) == 0 {
		return batch, errors.New("selected public conditions have no supported periods")
	}
	l.manager.mu.Lock()
	profile, runtime := l.manager.profile, l.manager.runtime
	runtimeError := l.manager.enrichmentRuntimeError()
	l.manager.mu.Unlock()
	if runtimeError != "" {
		return batch, errors.New(runtimeError)
	}
	if asOf <= 0 {
		return batch, errors.New("public alert cutoff must be a UTC millisecond timestamp")
	}
	root := filepath.Join(profile, "screener-public-history", source)
	batch.Directory = filepath.Join(root, "data")
	worker, preparedPlans, err := startPublicAlertWorker(ctx, runtime, plans)
	if err != nil {
		return batch, err
	}
	defer worker.close()
	plans = preparedPlans
	a, items, err := l.publicAlertCatalog(ctx, source)
	if err != nil {
		return batch, err
	}
	for _, dir := range []string{root, filepath.Join(root, "raw"), batch.Directory} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			return batch, err
		}
		if err = checkHistoryDirectory(dir, false); err != nil {
			return batch, err
		}
	}
	seen := map[string]bool{}
	for _, item := range items {
		status := strings.ToLower(item.Availability)
		if (item.Market != "spot" && item.Market != "perpetual") || (status != "available" && status != "trading" && status != "online") {
			continue
		}
		symbol := publicAlertSymbol(item.Market, item.NativeID)
		if seen[symbol] {
			continue
		}
		seen[symbol] = true
		name := item.ProviderMetadata["displaySymbol"]
		if name == "" {
			name = item.NativeID
		}
		batch.Assets = append(batch.Assets, publicAlertAsset{Symbol: symbol, NativeID: item.NativeID, Market: item.Market, Name: name})
		if len(batch.Assets) > 20000 {
			return batch, errors.New("public asset discovery exceeds the supported 20,000-market bound")
		}
		if warning := item.ProviderMetadata["catalogWarning"]; warning != "" {
			batch.Failures["catalog"] = errors.New(warning)
		}
	}
	if len(batch.Assets) == 0 {
		return batch, errors.New("public source returned no active spot or perpetual markets")
	}
	sort.Slice(batch.Assets, func(i, j int) bool { return batch.Assets[i].Symbol < batch.Assets[j].Symbol })
	manifest, _ := json.Marshal(struct {
		Source string             `json:"source"`
		Assets []publicAlertAsset `json:"assets"`
	}{source, batch.Assets})
	if err = publicAlertAtomicWrite(filepath.Join(root, "assets.json"), append(manifest, '\n')); err != nil {
		return batch, err
	}
	orderedPeriods := make([]string, 0, len(plans))
	for period := range plans {
		orderedPeriods = append(orderedPeriods, period)
	}
	sort.Slice(orderedPeriods, func(i, j int) bool {
		return exchange.TimeframeSeconds(publicAlertIntervals[orderedPeriods[i]]) > exchange.TimeframeSeconds(publicAlertIntervals[orderedPeriods[j]])
	})
	for i, asset := range batch.Assets {
		for _, period := range orderedPeriods {
			if err = ctx.Err(); err != nil {
				return batch, err
			}
			l.mu.Lock()
			l.status, l.currentAsset = "Fetching public market history", source+" "+asset.Name+" "+asset.Market+" "+period
			l.progress = 70 * float64(i) / float64(len(batch.Assets))
			l.mu.Unlock()
			pair := asset.Symbol + "/" + period
			name, e := preparePublicAlertSeries(ctx, a, worker, root, batch.Directory, source, asset, period, plans[period], asOf)
			if e != nil {
				batch.Failures[pair] = fmt.Errorf("%s %s %s: %w", asset.NativeID, asset.Market, period, e)
				if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
					return batch, e
				}
				switch exchange.ErrorKind(e) {
				case "restricted", "rateLimited", "auth", "unauthorized", "authentication":
					return batch, e
				}
				continue
			}
			batch.Files[pair] = name
		}
	}
	return batch, nil
}

type publicAlertRawState struct {
	Source         string `json:"source"`
	Market         string `json:"market"`
	NativeID       string `json:"nativeId"`
	Period         string `json:"period"`
	Checksum       string `json:"checksum"`
	CheckedThrough int64  `json:"checkedThrough"`
}

func preparePublicAlertSeries(ctx context.Context, adapter exchange.Adapter, worker *publicAlertWorker, root, directory, source string, asset publicAlertAsset, period string, plan publicAlertPlan, asOf int64) (string, error) {
	interval := publicAlertIntervals[period]
	d := exchange.TimeframeSeconds(interval)
	end := exchange.BucketStart(asOf/1000, d)
	from := end - int64(plan.Bars)*d
	if from < 0 {
		from = 0
	}
	key := domain.FeedKey{Exchange: source, Market: asset.Market, Instrument: asset.NativeID, Timeframe: interval}
	if err := key.Validate(); err != nil {
		return "", err
	}
	stem := asset.Symbol + "-" + period
	rawPath := filepath.Join(root, "raw", stem+".csv")
	metadataPath := filepath.Join(root, "raw", stem+".json")
	state := publicAlertRawState{Source: source, Market: asset.Market, NativeID: asset.NativeID, Period: period}
	bars := map[int64]domain.Candle{}
	if err := publicAlertRecoverRaw(rawPath, metadataPath, state); err != nil {
		return "", err
	}
	raw, err := publicAlertReadBounded(rawPath, 32<<20)
	if err == nil {
		if encoded, e := publicAlertReadBounded(metadataPath, 1<<20); e == nil {
			var previous publicAlertRawState
			if json.Unmarshal(encoded, &previous) != nil || previous.Source != source || previous.Market != asset.Market || previous.NativeID != asset.NativeID || previous.Period != period {
				return "", errors.New("saved public history identity is invalid; original preserved")
			}
			digest := sha256.Sum256(raw)
			if previous.Checksum != hex.EncodeToString(digest[:]) {
				return "", errors.New("saved public history changed outside its provider cache; original preserved")
			}
			state = previous
		} else if !errors.Is(e, os.ErrNotExist) {
			return "", e
		}
		rows, e := publicAlertReadRaw(raw, d)
		if e != nil {
			return "", fmt.Errorf("read saved public history: %w", e)
		}
		for _, c := range rows {
			bars[c.Time] = c
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	first := end
	for ts := range bars {
		first = min(first, ts)
	}
	type span struct{ from, to int64 }
	ranges := []span{}
	if len(bars) == 0 {
		ranges = append(ranges, span{from, end})
	} else {
		if first > from {
			ranges = append(ranges, span{from, min(first, end)})
		}
		repairFrom := max(from, state.CheckedThrough-d)
		for _, c := range bars {
			if c.Time >= from && c.Time < end && c.Quality != "authoritative" {
				repairFrom = min(repairFrom, c.Time)
			}
		}
		latest, hasLatest := bars[end-d]
		if state.CheckedThrough < end || !hasLatest || latest.Quality != "authoritative" || repairFrom < max(from, state.CheckedThrough-d) {
			ranges = append(ranges, span{repairFrom, end})
		}
	}
	for _, r := range ranges {
		for cursor := r.from; cursor < r.to; {
			pageEnd := min(r.to, cursor+1000*d)
			rows, e := adapter.History(ctx, key, cursor, pageEnd, 1000)
			if e != nil {
				return "", e
			}
			for _, c := range rows {
				if err := c.Validate(); err != nil || exchange.BucketStart(c.Time, d) != c.Time {
					return "", errors.New("public provider returned invalid candle data")
				}
				if c.Time >= cursor && c.Time < pageEnd && c.Closed && c.Time+d <= end {
					bars[c.Time] = c
				}
			}
			cursor = pageEnd
		}
	}
	// A successful empty/provisional response does not prove publication of
	// the closing candle. Keep the checkpoint behind its first unverified bar
	// so a retry at this same scheduled cutoff actually contacts the provider.
	provenThrough := end
	if latest, ok := bars[end-d]; !ok || latest.Quality != "authoritative" {
		provenThrough = end - d
	}
	for _, c := range bars {
		if c.Time >= from && c.Time < end && c.Quality != "authoritative" {
			provenThrough = min(provenThrough, c.Time)
		}
	}
	state.CheckedThrough = max(state.CheckedThrough, provenThrough)
	ordered := make([]domain.Candle, 0, len(bars))
	for _, c := range bars {
		ordered = append(ordered, c)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Time < ordered[j].Time })
	encoded, err := publicAlertRawCSV(ordered, true)
	if err != nil {
		return "", err
	}
	if len(encoded) > 32<<20 {
		return "", errors.New("public history exceeds the 32 MiB per-series bound; previous history preserved")
	}
	digest := sha256.Sum256(encoded)
	state.Checksum = hex.EncodeToString(digest[:])
	previousChecksum := ""
	if raw != nil {
		previous := sha256.Sum256(raw)
		previousChecksum = hex.EncodeToString(previous[:])
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if err = publicAlertPublishRaw(rawPath, metadataPath, encoded, state, previousChecksum); err != nil {
		return "", err
	}
	eligible := []domain.Candle{}
	for _, c := range ordered {
		if c.Time < end {
			eligible = append(eligible, c)
		}
	}
	if len(eligible) == 0 || eligible[len(eligible)-1].Time != end-d {
		return "", errLiveAlertsPendingBar
	}
	if eligible[len(eligible)-1].Quality != "authoritative" {
		return "", fmt.Errorf("%w; latest public candle is provisional", errLiveAlertsPendingBar)
	}
	if len(eligible) < plan.Bars {
		return "", fmt.Errorf("indicator/rule warm-up needs %d closed bars; provider has %d in the requested history window (saved for reuse)", plan.Bars, len(eligible))
	}
	for _, c := range eligible[len(eligible)-plan.Bars:] {
		if c.Quality != "authoritative" {
			return "", errors.New("required public candles are provisional; awaiting authoritative history")
		}
	}
	work, err := os.MkdirTemp(root, ".prepare-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	input, err := publicAlertRawCSV(eligible, false)
	if err != nil {
		return "", err
	}
	inputPath, outputPath := filepath.Join(work, "input.csv"), filepath.Join(work, "enriched.csv")
	if err = writeSyncedExclusive(inputPath, input); err != nil {
		return "", err
	}
	if err = worker.enrich(period, inputPath, outputPath); err != nil {
		return "", err
	}
	fromDate := time.Unix(eligible[0].Time, 0).UTC().Format("2006-01-02")
	toDate := time.Unix(end, 0).UTC().AddDate(0, 0, 1).Format("2006-01-02")
	name := stem + "-" + fromDate + "-" + toDate + ".csv"
	output, err := publicAlertReadBounded(outputPath, 128<<20)
	if err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if err = publicAlertAtomicWrite(filepath.Join(directory, name), output); err != nil {
		return "", err
	}
	// Only this application's previous derived snapshot is replaced. Raw venue
	// history remains durable, and files belonging to other assets are untouched.
	entries, _ := os.ReadDir(directory)
	for _, entry := range entries {
		if entry.Name() != name && strings.HasPrefix(entry.Name(), stem+"-") && downloadFilename.MatchString(entry.Name()) && entry.Type().IsRegular() {
			_ = os.Remove(filepath.Join(directory, entry.Name()))
		}
	}
	return name, nil
}

func publicAlertReadRaw(data []byte, d int64) ([]domain.Candle, error) {
	r := csv.NewReader(bytes.NewReader(data))
	header, err := r.Read()
	if err != nil || strings.Join(header, ",") != "timestamp,open,high,low,close,volume,quality" {
		return nil, errors.New("invalid public history CSV header")
	}
	rows := []domain.Candle{}
	for {
		row, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		ts, err := strconv.ParseInt(row[0], 10, 64)
		if err != nil || ts%1000 != 0 {
			return nil, errors.New("invalid cached public timestamp")
		}
		c, err := exchange.ParseCandle(ts/1000, row[1], row[2], row[3], row[4], row[5], true, row[6], 0)
		if err != nil {
			return nil, err
		}
		bar, err := c.Candle()
		if err != nil || exchange.BucketStart(bar.Time, d) != bar.Time {
			return nil, errors.New("invalid cached public candle")
		}
		if len(rows) > 0 && rows[len(rows)-1].Time >= bar.Time {
			return nil, errors.New("cached public candles are not strictly ordered")
		}
		rows = append(rows, bar)
	}
	return rows, nil
}

func publicAlertRawCSV(bars []domain.Candle, quality bool) ([]byte, error) {
	var buffer bytes.Buffer
	w := csv.NewWriter(&buffer)
	header := []string{"timestamp", "open", "high", "low", "close", "volume"}
	if quality {
		header = append(header, "quality")
	}
	_ = w.Write(header)
	for _, c := range bars {
		row := []string{strconv.FormatInt(c.Time*1000, 10)}
		for _, v := range []float64{c.Open, c.High, c.Low, c.Close, c.Volume} {
			row = append(row, strconv.FormatFloat(v, 'g', -1, 64))
		}
		if quality {
			row = append(row, c.Quality)
		}
		if err := w.Write(row); err != nil {
			return nil, err
		}
	}
	w.Flush()
	return buffer.Bytes(), w.Error()
}

type publicAlertRawJournal struct {
	State            publicAlertRawState `json:"state"`
	PreviousChecksum string              `json:"previousChecksum"`
}

func publicAlertPublishRaw(rawPath, metadataPath string, data []byte, state publicAlertRawState, previousChecksum string) error {
	current, err := publicAlertReadBounded(rawPath, 32<<20)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	currentChecksum := ""
	if current != nil {
		digest := sha256.Sum256(current)
		currentChecksum = hex.EncodeToString(digest[:])
	}
	if currentChecksum != previousChecksum {
		return errors.New("public history changed during preparation; original preserved")
	}
	journal := publicAlertRawJournal{State: state, PreviousChecksum: previousChecksum}
	encoded, _ := json.Marshal(journal)
	if err = publicAlertAtomicWrite(metadataPath+".pending", append(encoded, '\n')); err != nil {
		return err
	}
	if err = publicAlertAtomicWrite(rawPath, data); err != nil {
		return err
	}
	metadata, _ := json.Marshal(state)
	if err = publicAlertAtomicWrite(metadataPath, append(metadata, '\n')); err != nil {
		return err
	}
	return os.Remove(metadataPath + ".pending")
}

// A tiny write-ahead journal makes the raw CSV and its identity/checkpoint
// recoverable together. A crash between renames never poisons all future checks.
func publicAlertRecoverRaw(rawPath, metadataPath string, identity publicAlertRawState) error {
	encoded, err := publicAlertReadBounded(metadataPath+".pending", 1<<20)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var journal publicAlertRawJournal
	if json.Unmarshal(encoded, &journal) != nil || journal.State.Source != identity.Source || journal.State.Market != identity.Market || journal.State.NativeID != identity.NativeID || journal.State.Period != identity.Period || len(journal.State.Checksum) != 64 {
		return errors.New("invalid public cache publication journal; original preserved")
	}
	current, err := publicAlertReadBounded(rawPath, 32<<20)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	checksum := ""
	if current != nil {
		digest := sha256.Sum256(current)
		checksum = hex.EncodeToString(digest[:])
	}
	if checksum == journal.State.Checksum {
		metadata, _ := json.Marshal(journal.State)
		if err = publicAlertAtomicWrite(metadataPath, append(metadata, '\n')); err != nil {
			return err
		}
	} else if checksum != journal.PreviousChecksum {
		return errors.New("public cache changed during interrupted publication; original preserved")
	}
	return os.Remove(metadataPath + ".pending")
}

func publicAlertReadBounded(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("saved public history must be a regular file within its bounded size; original preserved")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	current, err := f.Stat()
	if err != nil || !os.SameFile(info, current) {
		return nil, errors.New("public history changed before reading")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(data)) > limit {
		return nil, errors.New("public history exceeded its bounded size while reading")
	}
	return data, err
}

func publicAlertAtomicWrite(path string, data []byte) error {
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return errors.New("public history destination must be a regular file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".public-write-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(data)
	err = errors.Join(writeErr, f.Sync(), f.Close())
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	if parent, e := os.Open(filepath.Dir(path)); e == nil {
		_ = parent.Sync()
		_ = parent.Close()
	}
	return nil
}

type publicAlertWorker struct {
	cmd    *exec.Cmd
	in     io.WriteCloser
	encode *json.Encoder
	decode *json.Decoder
	stderr tailBuffer
}

func startPublicAlertWorker(ctx context.Context, runtime workerRuntime, plans map[string]publicAlertPlan) (*publicAlertWorker, map[string]publicAlertPlan, error) {
	w := &publicAlertWorker{}
	w.cmd = exec.CommandContext(ctx, runtime.node, "-e", publicAlertWorkerScript, filepath.Dir(runtime.worker))
	configureProcess(w.cmd)
	in, err := w.cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	w.in, w.encode = in, json.NewEncoder(in)
	out, err := w.cmd.StdoutPipe()
	if err != nil {
		in.Close()
		return nil, nil, err
	}
	w.decode = json.NewDecoder(bufio.NewReaderSize(out, 65536))
	w.cmd.Stderr = &w.stderr
	if err = w.cmd.Start(); err != nil {
		in.Close()
		return nil, nil, err
	}
	if err = w.encode.Encode(map[string]any{"plans": plans}); err != nil {
		w.close()
		return nil, nil, err
	}
	var result struct {
		Plans map[string]publicAlertPlan `json:"plans"`
		Error string                     `json:"error"`
	}
	if err = w.decode.Decode(&result); err != nil {
		w.close()
		return nil, nil, fmt.Errorf("prepare public indicator runtime: %w %s", err, cleanMessage(w.stderr.String()))
	}
	if result.Error != "" {
		w.close()
		return nil, nil, errors.New(result.Error)
	}
	for period, plan := range result.Plans {
		if plan.Bars < 2 || plan.Bars > 50000 || plan.Bars < plan.Extra+1 {
			w.close()
			return nil, nil, fmt.Errorf("required %s warm-up exceeds the supported 50,000-bar bound", period)
		}
	}
	return w, result.Plans, nil
}

func (w *publicAlertWorker) enrich(period, input, output string) error {
	if err := w.encode.Encode(map[string]string{"period": period, "input": input, "output": output}); err != nil {
		return err
	}
	var result struct {
		Error string `json:"error"`
	}
	if err := w.decode.Decode(&result); err != nil {
		return fmt.Errorf("public indicator worker: %w", err)
	}
	if result.Error != "" {
		return errors.New(result.Error)
	}
	return nil
}

func (w *publicAlertWorker) close() { _ = w.in.Close(); _ = w.cmd.Wait() }

// Reuse the pinned production recipe recovery and calculators. Warm-up counts
// come from the installed constructors' getRequiredInputs(), including the
// extra signal smoothing required by MACD histogram/signal fields. Each batch
// owns one cancellable child, irrespective of the number of discovered assets.
const publicAlertWorkerScript = `
'use strict';
const path=require('node:path'),fs=require('node:fs/promises'),readline=require('node:readline');
const root=process.argv[1],{calculator,line,writeAll}=require(path.join(root,'enrich-worker.cjs'));
const {recipesFor,records}=require(path.join(root,'refresh-worker.cjs'));
const base=['timestamp','open','high','low','close','volume'];
const emit=x=>process.stdout.write(JSON.stringify(x)+'\n');
async function required(recipe,fields){
 const api=await import(path.join(root,'node_modules/trading-signals/dist/index.js')),p=recipe.parameters,id=recipe.indicator;
 if(['weekday','hour'].includes(id))return 1;
 if(['low','high','tenkan','kijun'].includes(id))return p.period;
 if(id==='ichimoku')return Math.max(...fields.map(f=>f.startsWith('tenkan_')?p.conversion:f.startsWith('kijun_')?p.base:f.startsWith('senkou_a_')?Math.max(p.conversion,p.base):f.startsWith('senkou_b_')?p.spanB:1));
 if(id==='macd')return p.slow+(fields.some(f=>/^macd_(signal|histogram)_/.test(f))?p.signal-1:0);
 const simple={sma:'SMA',ema:'EMA',wma:'WMA',wsma:'WSMA',rma:'RMA',dema:'DEMA',tema:'TEMA',trima:'TRIMA',zlema:'ZLEMA',hma:'HMA',kama:'KAMA',rsi:'RSI',roc:'ROC',mom:'MOM',cci:'CCI',adx:'ADX',adxr:'ADXR',dx:'DX',atr:'ATR',obv:'OBV',vwma:'VWMA',mfi:'MFI',cmf:'CMF',force:'ForceIndex',stochrsi:'StochasticRSI'};
 let indicator;
 if(simple[id])indicator=new api[simple[id]](p.period);
 else if(id==='alma')indicator=new api.ALMA(p.period,p.offset,p.sigma);
 else if(id==='bb'||id==='bbw')indicator=new api.BollingerBands(p.period,p.deviation);
 else if(id==='donchian')indicator=new api.DonchianChannels(p.period);
 else if(id==='stochastic')indicator=new api.StochasticOscillator({kPeriod:p.k,kSlowingPeriod:p.slowing,dPeriod:p.d});
 else if(id==='ao')indicator=new api.AO(p.fast,p.slow);
 else if(id==='ac')indicator=new api.AC(p.fast,p.slow,p.signal);
 else {const ctor={tr:'TR',vwap:'VWAP',ad:'AD',pvt:'PVT'}[id];if(ctor)indicator=new api[ctor]();}
 if(!indicator?.getRequiredInputs)throw Error('Cannot determine complete indicator warm-up for '+id);
 return indicator.getRequiredInputs();
}
(async()=>{
 let plans,recipes;
 for await(const text of readline.createInterface({input:process.stdin,crlfDelay:Infinity})){
  try{
   const request=JSON.parse(text);
   if(!plans){
    plans=request.plans;recipes={};
    for(const [period,plan]of Object.entries(plans)){
     recipes[period]=await recipesFor(base,[],plan.fields);
     let warm=1;for(const recipe of recipes[period])warm=Math.max(warm,await required(recipe,plan.fields));
     plan.bars=warm+plan.extra;
    }
    emit({plans});continue;
   }
   const plan=plans[request.period],calculators=[];
   for(const recipe of recipes[request.period])calculators.push(await calculator(recipe,base));
   const columns=[...new Set([...base,...calculators.flatMap(c=>c.columns)])],positions=plan.fields.map(f=>columns.indexOf(f));
   if(positions.some(p=>p<0))throw Error('Required public condition field is unavailable.');
   const handle=await fs.open(request.output,'wx',0o600);let count=0,tail=[],buffer='';
   try{
    await writeAll(handle,line(columns));
    for await(const row of records(request.input)){
     if(!count++){continue;}
     const values=[...row];
     for(const calculate of calculators){const derived=calculate.next(row,count);calculate.columns.forEach((column,i)=>{values[columns.indexOf(column)]=derived[i];});}
     tail.push(values);if(tail.length>plan.extra+1)tail.shift();
     buffer+=line(values);if(buffer.length>=131072){await writeAll(handle,buffer);buffer='';}
    }
    if(buffer)await writeAll(handle,buffer);await handle.sync();
   }finally{await handle.close();}
   if(count-1<plan.bars||tail.some(row=>positions.some(p=>row[p]===''||!Number.isFinite(Number(row[p])))))throw Error('Required indicator/rule history is still warming up or undefined; public bars were saved for reuse.');
   emit({ok:true});
  }catch(error){emit({error:String(error?.message||error).slice(0,4000)});if(!plans)break;}
 }
})().catch(error=>{emit({error:String(error?.message||error)});process.exitCode=1;});
`

package screener

import (
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

func normalizeDataSource(source string) string {
	if source == "" {
		return "dukascopy"
	}
	return source
}
func fileDataSource(name string) string {
	if strings.HasPrefix(strings.ToLower(name), "hyperliquid-") || strings.HasPrefix(strings.ToLower(name), "hyperliquid_") {
		return "hyperliquid"
	}
	return "dukascopy"
}
func validHyperliquidDownloadID(id string) bool {
	return strings.HasPrefix(id, "hyperliquid-") && publicLiveAlias.MatchString(strings.TrimPrefix(id, "hyperliquid-"))
}
func validHyperliquidJob(job Job) bool {
	return validHyperliquidDownloadID(job.Symbol) && job.Symbol == "hyperliquid-"+publicAlertSymbol(job.Market, job.NativeID) && (domain.FeedKey{Exchange: "hyperliquid", Market: job.Market, Instrument: job.NativeID, Timeframe: "1d"}).Validate() == nil
}
func validateHyperliquidRequest(r Request, now time.Time) error {
	if r.Timeframe != "h1" && r.Timeframe != "d1" && r.Timeframe != "w1" {
		return errors.New("Hyperliquid downloader supports H1, D1 and W1 derived from D1")
	}
	if len(r.Symbols) == 0 || len(r.Symbols) > 20000 {
		return errors.New("select at least one Hyperliquid instrument")
	}
	seen := map[string]bool{}
	for _, id := range r.Symbols {
		if !validHyperliquidDownloadID(id) || seen[id] {
			return errors.New("invalid or duplicate Hyperliquid instrument")
		}
		seen[id] = true
	}
	from, e := time.Parse("2006-01-02", r.From)
	to, e2 := time.Parse("2006-01-02", r.To)
	if e != nil || e2 != nil || from.Format("2006-01-02") != r.From || to.Format("2006-01-02") != r.To || from.Year() < 1900 || !from.Before(to) || to.After(now.UTC().Truncate(24*time.Hour).Add(24*time.Hour)) {
		return errors.New("choose valid UTC dates; To is exclusive, after From and no later than tomorrow")
	}
	return nil
}

// Catalog discovery uses the same paced production adapter as charts, including
// native spot IDs and builder DEX prefixes. No list is hardcoded.
func (m *Manager) CatalogForSource(ctx context.Context, source string) (Catalog, error) {
	switch normalizeDataSource(source) {
	case "dukascopy":
		return loadCatalog(), nil
	case "hyperliquid":
	default:
		return Catalog{}, errors.New("unsupported Screener data source")
	}
	m.hyperliquidMu.Lock()
	defer m.hyperliquidMu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	if len(m.hyperliquidCatalog.Groups) > 0 && time.Since(m.hyperliquidCatalogAt) < 30*time.Minute {
		return cloneHyperliquidCatalog(m.hyperliquidCatalog), nil
	}
	if m.hyperliquidAdapter == nil {
		a, err := exchange.New("hyperliquid")
		if err != nil {
			return Catalog{}, err
		}
		m.hyperliquidAdapter = a
	}
	items, err := m.hyperliquidAdapter.Instruments(ctx)
	if err != nil {
		return Catalog{}, err
	}
	catalog := Catalog{Version: "hyperliquid-public", Timeframes: []string{"h1", "d1", "w1"}, Groups: []Group{}}
	groups := map[string][]Instrument{}
	for _, item := range items {
		if warning := item.ProviderMetadata["catalogWarning"]; warning != "" {
			return Catalog{}, fmt.Errorf("incomplete Hyperliquid catalog: %s; retry discovery", warning)
		}
		status := strings.ToLower(item.Availability)
		if status != "available" && status != "trading" && status != "online" {
			continue
		}
		key := domain.FeedKey{Exchange: "hyperliquid", Market: item.Market, Instrument: item.NativeID, Timeframe: "1d"}
		if key.Validate() != nil {
			continue
		}
		label := item.ProviderMetadata["displaySymbol"]
		if label == "" {
			label = item.NativeID
		}
		if label != item.NativeID {
			label += " · " + item.NativeID
		}
		name := "Perpetual"
		if item.Market == "spot" {
			name = "Spot"
		}
		groups[name] = append(groups[name], Instrument{ID: "hyperliquid-" + publicAlertSymbol(item.Market, item.NativeID), NativeID: item.NativeID, Market: item.Market, Name: label})
	}
	for _, group := range []string{"Spot", "Perpetual"} {
		values := groups[group]
		sort.Slice(values, func(i, j int) bool { return values[i].Name < values[j].Name })
		if len(values) > 0 {
			catalog.Groups = append(catalog.Groups, Group{Name: group, Instruments: values})
		}
	}
	if len(catalog.Groups) == 0 {
		return Catalog{}, errors.New("Hyperliquid returned no active downloadable markets")
	}
	m.hyperliquidCatalog, m.hyperliquidCatalogAt = catalog, time.Now()
	return cloneHyperliquidCatalog(catalog), nil
}
func cloneHyperliquidCatalog(c Catalog) Catalog {
	data, _ := json.Marshal(c)
	var out Catalog
	_ = json.Unmarshal(data, &out)
	return out
}
func hyperliquidAssetPath(directory, id string) string {
	return filepath.Join(directory, ".cryptodesk-hyperliquid-assets", id+".json")
}
func writeHyperliquidAsset(directory string, asset Instrument) error {
	if !validHyperliquidJob(Job{Symbol: asset.ID, Market: asset.Market, NativeID: asset.NativeID}) {
		return errors.New("invalid Hyperliquid identity")
	}
	folder := filepath.Dir(hyperliquidAssetPath(directory, asset.ID))
	if err := os.MkdirAll(folder, 0700); err != nil {
		return err
	}
	if err := checkHistoryDirectory(folder, false); err != nil {
		return err
	}
	if err := writeState(hyperliquidAssetPath(directory, asset.ID), asset); err != nil {
		return err
	}
	_, err := hyperliquidFileSymbol(directory, asset.ID)
	return err
}
func readHyperliquidAsset(directory, id string) (Instrument, error) {
	var asset Instrument
	if !validHyperliquidDownloadID(id) {
		return asset, errors.New("invalid Hyperliquid alias")
	}
	err := readState(hyperliquidAssetPath(directory, id), 4096, &asset)
	if err == nil && (!validHyperliquidJob(Job{Symbol: asset.ID, Market: asset.Market, NativeID: asset.NativeID}) || asset.ID != id) {
		err = errors.New("invalid Hyperliquid asset metadata")
	}
	return asset, err
}

func (m *Manager) runHyperliquidDownloads(ctx context.Context, directory string, workers int) {
	// Preserve the daily-before-weekly durable queue shape and bounded workers.
	for _, weekly := range []bool{false, true} {
		next := 0
		var wg sync.WaitGroup
		for range min(workers, publicAlertConcurrency) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					m.mu.Lock()
					if ctx.Err() != nil || next >= len(m.jobs) {
						m.mu.Unlock()
						return
					}
					i := next
					next++
					job := m.jobs[i]
					if job.Status != "queued" || (job.Timeframe == "w1") != weekly {
						m.mu.Unlock()
						continue
					}
					if weekly {
						ready := false
						for _, d := range m.jobs {
							if d.Symbol == job.Symbol && d.Timeframe == "d1" && d.Status == "completed" {
								ready = true
							}
						}
						if !ready {
							m.jobs[i].Status, m.jobs[i].Message = "failed", "Daily prerequisite did not complete"
							m.saveDownloadsLocked()
							m.mu.Unlock()
							continue
						}
					}
					m.jobs[i].Status, m.jobs[i].Message = "running", "Preparing Hyperliquid "+strings.ToUpper(job.Timeframe)+" history"
					m.mu.Unlock()
					_, _, err := m.downloadHyperliquid(ctx, i, job, directory)
					m.mu.Lock()
					if m.jobs[i].Status != "completed" {
						if ctx.Err() != nil {
							if m.jobs[i].Status != "cancelled" {
								m.jobs[i].Status, m.jobs[i].Message = "queued", "Paused; resumes on restart"
							}
						} else if err != nil {
							m.jobs[i].Status, m.jobs[i].Message = "failed", cleanMessage(err.Error())
						}
					}
					saved := m.saveDownloadsLocked()
					terminal := m.jobs[i].Status == "completed" || m.jobs[i].Status == "cancelled"
					m.mu.Unlock()
					if saved && terminal {
						m.cleanupJobAt(directory, job)
					}
				}
			}()
		}
		wg.Wait()
	}
}

func (m *Manager) downloadHyperliquid(ctx context.Context, index int, job Job, directory string) (string, string, error) {
	if !validHyperliquidJob(job) {
		return "", "", errors.New("invalid Hyperliquid download identity")
	}
	work, err := m.prepareJob(job)
	if err != nil {
		return "", "", err
	}
	plan, originals, err := prepareDownloadPlan(job, directory, work)
	if err != nil {
		return "", "", err
	}
	from, _ := time.Parse("2006-01-02", job.From)
	to, _ := time.Parse("2006-01-02", job.To)
	asset := publicAlertAsset{Market: job.Market, NativeID: job.NativeID, Symbol: strings.TrimPrefix(job.Symbol, "hyperliquid-")}
	var incoming []domain.Candle
	coverage := []dataRange{}
	if job.Timeframe == "h1" || job.Timeframe == "d1" {
		m.hyperliquidMu.Lock()
		a := m.hyperliquidAdapter
		if a == nil {
			a, err = exchange.New("hyperliquid")
			m.hyperliquidAdapter = a
		}
		m.hyperliquidMu.Unlock()
		if err != nil {
			return "", "", err
		}
		key := domain.FeedKey{Exchange: "hyperliquid", Market: job.Market, Instrument: job.NativeID, Timeframe: publicAlertIntervals[job.Timeframe]}
		step := exchange.TimeframeSeconds(key.Timeframe)
		// candleSnapshot itself returns the newest retained 5,000 bars in this
		// range. Calendar-day clipping would lose sparse older candles.
		end := min(to.Unix(), job.AsOf/1000+1)
		requested := dataRange{From: max(int64(0), from.UnixMilli()), To: end * 1000}
		covered, e := hyperliquidCoveredRanges(plan.Inputs, job.AsOf, step)
		if e != nil {
			return "", "", e
		}
		missing := hyperliquidMissingRanges(requested, covered)
		for _, span := range missing {
			rows, e := a.History(ctx, key, span.From/1000, (span.To+999)/1000, 5000)
			if e != nil {
				return "", "", e
			}
			for _, bar := range rows {
				if bar.Time*1000 >= span.From && bar.Time*1000 < span.To {
					incoming = append(incoming, bar)
				}
			}
		}
		closedEnd := exchange.BucketStart(job.AsOf/1000-2, step) * 1000
		coverage = append(covered, dataRange{From: requested.From, To: min(requested.To, closedEnd)})
		coverage = hyperliquidUnionRanges(coverage)

		m.mu.Lock()
		m.jobs[index].Progress = 80
		m.jobs[index].Message = "Downloaded available Hyperliquid " + strings.ToUpper(job.Timeframe) + " candles"
		m.mu.Unlock()

		if len(incoming) > 0 {
			if err = mergeHyperliquidHistory(ctx, m.profile, asset, job.Timeframe, incoming); err != nil {
				return "", "", err
			}
		}
	} else {
		// Read the actual retained daily CSV, including old bars beyond provider
		// retention, rather than issuing a second network download for weeks.
		names, e := csvNames(directory)
		if e != nil {
			return "", "", e
		}
		var daily []domain.Candle
		for _, name := range names {
			match := parseDownloadFilename(directory, name)
			if match != nil && match[1] == job.Symbol && match[2] == "d1" {
				rows, e := readHyperliquidVerifiedCSV(directory, name, 86400, job.AsOf)
				if e != nil {
					return "", "", e
				}
				daily = append(daily, rows...)
			}
		}
		incoming, err = hyperliquidWeeklyBars(daily, job.AsOf/1000)
		if err != nil {
			return "", "", err
		}
		if err = mergeHyperliquidHistory(ctx, m.profile, asset, "w1", incoming); err != nil {
			return "", "", err
		}
	}
	// Previous published rows always survive an empty provider range.
	all := map[int64]domain.Candle{}
	recipes := []EnrichmentRequest{}
	for _, source := range plan.Inputs {
		rows, e := readHyperliquidVerifiedCSV(directory, source.Name, exchange.TimeframeSeconds(publicAlertIntervals[job.Timeframe]), job.AsOf)
		if e != nil {
			return "", "", e
		}
		for _, c := range rows {
			if _, ok := all[c.Time]; !ok {
				all[c.Time] = c
			}
		}
		for _, r := range source.Metadata.Recipes {
			recipes = addRecipe(recipes, r)
		}
	}
	for _, c := range incoming {
		if err := c.Validate(); err != nil {
			return "", "", err
		}
		all[c.Time] = c
	}
	if len(all) == 0 {
		return "", "", fmt.Errorf("no Hyperliquid candles are available in the requested dates; history begins at listing and only the latest 5,000 %s candles are retained", strings.ToUpper(job.Timeframe))
	}
	ordered := make([]domain.Candle, 0, len(all))
	for _, c := range all {
		ordered = append(ordered, c)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Time < ordered[j].Time })
	// Import all actual retained history, including an older visible download.
	// Current buckets remain provisional and are excluded by the raw importer.
	step := exchange.TimeframeSeconds(publicAlertIntervals[job.Timeframe])
	for i := range ordered {
		ordered[i].Closed = ordered[i].Closed && ordered[i].Time+step <= job.AsOf/1000-2
		if !ordered[i].Closed {
			ordered[i].Quality = "provisional"
		}
	}
	if job.Timeframe == "w1" {
		// Derived weeks prove only buckets containing seven verified daily
		// candles. Retain that proof alongside the visible weekly CSV.
		for _, bar := range ordered {
			if bar.Closed && bar.Quality == "authoritative" {
				coverage = append(coverage, dataRange{From: bar.Time * 1000, To: (bar.Time + step) * 1000})
			}
		}
		coverage = hyperliquidUnionRanges(coverage)
	}
	// Successful empty responses do not certify an older partial snapshot.
	// Keep those exact buckets uncovered until fresh authoritative bars arrive.
	for _, bar := range ordered {
		if !bar.Closed || bar.Quality != "authoritative" {
			filtered := []dataRange{}
			bucket := dataRange{From: bar.Time * 1000, To: (bar.Time + step) * 1000}
			for _, span := range coverage {
				filtered = append(filtered, hyperliquidMissingRanges(span, []dataRange{bucket})...)
			}
			coverage = hyperliquidUnionRanges(filtered)
		}
	}
	if err = mergeHyperliquidHistory(ctx, m.profile, asset, job.Timeframe, ordered); err != nil {
		return "", "", err
	}
	data, err := publicAlertRawCSV(ordered, false)
	if err != nil {
		return "", "", err
	}
	partial := filepath.Join(work, "data.part")
	if err = publicAlertAtomicWrite(partial, data); err != nil {
		return "", "", err
	}
	// Recover applied recipes from headers too, retaining legacy enriched data.
	if len(plan.Inputs) > 0 {
		data, recipes, err = m.replayHyperliquidIndicators(ctx, work, partial, job.Timeframe, plan.Inputs, recipes)
		if err != nil {
			return "", "", err
		}
	}
	digest := sha256.Sum256(data)
	result := workerEvent{Coverage: coverage, Rows: int64(len(ordered)), FirstTimestamp: ordered[0].Time * 1000, LastTimestamp: ordered[len(ordered)-1].Time * 1000, Checksum: hex.EncodeToString(digest[:]), Recipes: recipes, Message: fmt.Sprintf("Saved %d Hyperliquid %s candles; available history starts %s", len(ordered), strings.ToUpper(job.Timeframe), time.Unix(ordered[0].Time, 0).UTC().Format("2006-01-02"))}
	if len(plan.Inputs) > 0 {
		return m.publishExtendedDownload(ctx, index, job, directory, work, partial, plan, originals, result)
	}
	metadata, err := downloadedMetadata(partial, result)
	if err != nil {
		return "", "", err
	}
	// Actual dates describe availability rather than claiming pre-listing bars.
	first := time.Unix(ordered[0].Time, 0).UTC().Format("2006-01-02")
	name, err := canonicalHistoryName(job.Symbol, job.Timeframe, first, job.To, nil, metadata, directory)
	if err != nil {
		return "", "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err = ctx.Err(); err != nil {
		return "", "", err
	}
	m.publication[job.ID] = name
	if !m.saveDownloadsLocked() {
		return "", "", errors.New(m.stateError)
	}
	if _, err = publishHistory(directory, name, partial, nil, nil, metadata); err != nil {
		return "", "", err
	}
	output := filepath.Join(directory, name)
	m.jobs[index].Status, m.jobs[index].Progress, m.jobs[index].OutputPath, m.jobs[index].Message = "completed", 100, output, result.Message
	m.saveDownloadsLocked()
	return output, result.Message, nil
}

// CSVs may be enriched; only named base columns enter candle history.
func readHyperliquidCSV(path string, d int64) ([]domain.Candle, error) {
	data, err := publicAlertReadBounded(path, 32<<20)
	if err != nil {
		return nil, err
	}
	reader := csv.NewReader(bytes.NewReader(data))
	header, err := reader.Read()
	if err != nil {
		return nil, err
	}
	positions := map[string]int{}
	for i, h := range header {
		positions[strings.ToLower(strings.TrimSpace(h))] = i
	}
	for _, field := range []string{"timestamp", "open", "high", "low", "close", "volume"} {
		if _, ok := positions[field]; !ok {
			return nil, errors.New("Hyperliquid CSV is missing OHLCV columns")
		}
	}
	rows := []domain.Candle{}
	for {
		row, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		ts, e := strconv.ParseInt(row[positions["timestamp"]], 10, 64)
		if e != nil || ts%1000 != 0 {
			return nil, errors.New("invalid Hyperliquid CSV timestamp")
		}
		c, e := exchange.ParseCandle(ts/1000, row[positions["open"]], row[positions["high"]], row[positions["low"]], row[positions["close"]], row[positions["volume"]], true, "authoritative", 0)
		if e != nil {
			return nil, e
		}
		bar, e := c.Candle()
		if e != nil || exchange.BucketStart(bar.Time, d) != bar.Time {
			return nil, errors.New("invalid Hyperliquid CSV candle boundary")
		}
		if len(rows) > 0 && rows[len(rows)-1].Time >= bar.Time {
			return nil, errors.New("Hyperliquid CSV candles are not strictly ordered")
		}
		rows = append(rows, bar)
	}
	return exchange.Normalize(rows, d)
}

// A previous open snapshot cannot become final just because time elapsed.
// Only checksum-bound provider coverage proves closed visible daily candles.
func readHyperliquidVerifiedCSV(directory, name string, step, asOf int64) ([]domain.Candle, error) {
	rows, err := readHyperliquidCSV(filepath.Join(directory, name), step)
	if err != nil {
		return nil, err
	}
	metadata, err := readFileMetadata(directory, name)
	if err != nil {
		return nil, err
	}
	data, err := publicAlertReadBounded(filepath.Join(directory, name), 32<<20)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(data)
	valid := metadata.Checksum == hex.EncodeToString(digest[:])
	for i := range rows {
		proven := false
		if valid {
			for _, span := range metadata.Coverage {
				if span.From <= rows[i].Time*1000 && span.To >= (rows[i].Time+step)*1000 {
					proven = true
					break
				}
			}
		}
		rows[i].Closed = proven && rows[i].Time+step <= asOf/1000-2
		if !rows[i].Closed {
			rows[i].Quality = "provisional"
		}
	}
	return rows, nil
}

func hyperliquidWeeklyBars(daily []domain.Candle, asOf int64) ([]domain.Candle, error) {
	ordered, err := exchange.Normalize(daily, 86400)
	if err != nil {
		return nil, err
	}
	for i := range ordered {
		ordered[i].Closed = ordered[i].Closed && ordered[i].Time+86400 <= asOf-2
		if !ordered[i].Closed {
			ordered[i].Quality = "provisional"
		}
	}
	out := hyperliquidWeeklyFromDaily(ordered)
	for i := range out {
		out[i].Closed = out[i].Time+604800 <= asOf-2
		if !out[i].Closed {
			out[i].Quality = "provisional"
		}
	}
	return out, nil
}

func hyperliquidUnionRanges(ranges []dataRange) []dataRange {
	valid := []dataRange{}
	for _, r := range ranges {
		if r.From < r.To {
			valid = append(valid, r)
		}
	}
	sort.Slice(valid, func(i, j int) bool { return valid[i].From < valid[j].From })
	out := []dataRange{}
	for _, r := range valid {
		if len(out) > 0 && r.From <= out[len(out)-1].To {
			out[len(out)-1].To = max(out[len(out)-1].To, r.To)
		} else {
			out = append(out, r)
		}
	}
	return out
}
func hyperliquidMissingRanges(requested dataRange, covered []dataRange) []dataRange {
	out := []dataRange{}
	cursor := requested.From
	for _, r := range hyperliquidUnionRanges(covered) {
		if r.To <= cursor {
			continue
		}
		if r.From >= requested.To {
			break
		}
		if r.From > cursor {
			out = append(out, dataRange{From: cursor, To: min(r.From, requested.To)})
		}
		cursor = max(cursor, r.To)
	}
	if cursor < requested.To {
		out = append(out, dataRange{From: cursor, To: requested.To})
	}
	return out
}
func hyperliquidCoveredRanges(sources []downloadSource, asOf, step int64) ([]dataRange, error) {
	covered := []dataRange{}
	closed := exchange.BucketStart(asOf/1000-2, step) * 1000
	for _, source := range sources {
		data, err := publicAlertReadBounded(source.Input, 32<<20)
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(data)
		if source.Metadata.Checksum == hex.EncodeToString(digest[:]) && len(source.Metadata.Coverage) > 0 {
			for _, r := range source.Metadata.Coverage {
				covered = append(covered, dataRange{From: r.From, To: min(r.To, closed)})
			}

		}
	}
	return hyperliquidUnionRanges(covered), nil
}

func (m *Manager) replayHyperliquidIndicators(ctx context.Context, work, input, period string, sources []downloadSource, recipes []EnrichmentRequest) ([]byte, []EnrichmentRequest, error) {
	if recipes == nil {
		recipes = []EnrichmentRequest{}
	}
	headers := []string{}
	for _, source := range sources {
		f := describeFile(filepath.Dir(source.Input), source.Name)
		if f.Error != "" {
			return nil, nil, errors.New(f.Error)
		}
		headers = append(headers, f.Fields...)
	}
	baseOnly := len(recipes) == 0
	base := map[string]bool{"timestamp": true, "open": true, "high": true, "low": true, "close": true, "volume": true}
	for _, field := range headers {
		if !base[strings.ToLower(strings.TrimSpace(field))] {
			baseOnly = false
			break
		}
	}
	if baseOnly {
		data, err := publicAlertReadBounded(input, 32<<20)
		return data, recipes, err
	}
	if m.enrichmentRuntimeError() != "" {
		return nil, nil, errors.New("updating an existing history requires the packaged indicator runtime to preserve its columns")
	}
	// A paused process can leave an unfinished replay behind. Each attempt
	// owns fresh scratch, so Resume never collides with that older file.
	replayWork, err := os.MkdirTemp(work, ".replay-*")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(replayWork)
	output := filepath.Join(replayWork, "replayed.csv")
	request, _ := json.Marshal(map[string]any{"root": filepath.Dir(m.runtime.worker), "input": input, "output": output, "headers": headers, "recipes": recipes, "period": period})
	cmd := exec.CommandContext(ctx, m.runtime.node, "-e", hyperliquidReplayScript)
	cmd.Stdin = bytes.NewReader(request)
	configureProcess(cmd)
	var stderr tailBuffer
	cmd.Stderr = &stderr
	result, err := cmd.Output()
	if err != nil {
		return nil, nil, fmt.Errorf("recalculate Hyperliquid indicators: %w %s", err, cleanMessage(stderr.String()))
	}
	if err = json.Unmarshal(result, &recipes); err != nil {
		return nil, nil, err
	}
	data, err := publicAlertReadBounded(output, 32<<20)
	if err != nil {
		return nil, nil, err
	}
	if err = os.Rename(output, input); err != nil {
		return nil, nil, err
	}
	return data, recipes, nil
}

const hyperliquidReplayScript = `
'use strict';
(async()=>{const fs=require('node:fs/promises'),path=require('node:path');let text='';for await(const chunk of process.stdin)text+=chunk;const r=JSON.parse(text),{recipesFor,records}=require(path.join(r.root,'refresh-worker.cjs')),{calculator,line,writeAll}=require(path.join(r.root,'enrich-worker.cjs')),base=['timestamp','open','high','low','close','volume'];const recipes=await recipesFor([...new Set([...base,...r.headers])],r.recipes,[],r.period),calculators=[];for(const recipe of recipes)calculators.push(await calculator(recipe,base,r.period));const columns=[...new Set([...base,...calculators.flatMap(c=>c.columns)])],handle=await fs.open(r.output,'wx',0o600);try{await writeAll(handle,line(columns));let count=0;for await(const row of records(r.input)){if(!count++)continue;const values=[...row];for(const c of calculators){const v=c.next(row,count);c.columns.forEach((name,i)=>values[columns.indexOf(name)]=v[i]);}await writeAll(handle,line(values));}await handle.sync();}finally{await handle.close();}process.stdout.write(JSON.stringify(recipes));})().catch(e=>{process.stderr.write(String(e));process.exitCode=1;});
`

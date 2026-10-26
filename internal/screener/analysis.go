package screener

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Operands always address closed bars. Offset zero means the latest fully
// closed candle at the evaluated bar's closing time, never the current open bar.
type AnalysisOperand struct {
	Kind      string  `json:"kind"`
	Field     string  `json:"field"`
	Value     float64 `json:"value"`
	Timeframe string  `json:"timeframe"`
	Offset    int     `json:"offset"`
	Aggregate string  `json:"aggregate"`
	Window    int     `json:"window"`
}
type AnalysisCondition struct {
	ID         string          `json:"id"`
	Operator   string          `json:"operator"`
	Comparison string          `json:"comparison"`
	Left       AnalysisOperand `json:"left"`
	Right      AnalysisOperand `json:"right"`
	Upper      AnalysisOperand `json:"upper"`
	Distance   AnalysisOperand `json:"distance"`
	Multiplier float64         `json:"multiplier"`
	Bars       int             `json:"bars"`
}
type AnalysisRequest struct {
	Conditions []AnalysisCondition `json:"conditions"`
	// A nil selection preserves older saved conditions: apply to every asset.
	// An explicit empty selection means none and must never silently mean all.
	Categories []string `json:"categories"`
}
type AnalysisCatalog struct {
	Directory  string     `json:"directory"`
	Fields     []string   `json:"fields"`
	Timeframes []string   `json:"timeframes"`
	Files      []DataFile `json:"files"`
	Categories []string   `json:"categories"`
}
type AnalysisFilter struct {
	Categories   []string `json:"categories"`
	IncludeEmpty bool     `json:"includeEmpty"`
}
type AnalysisResult struct {
	ID           string `json:"id"`
	FileID       string `json:"fileId"`
	Name         string `json:"name"`
	Symbol       string `json:"symbol"`
	Description  string `json:"description"`
	Category     string `json:"category"`
	Timeframe    string `json:"timeframe"`
	Status       string `json:"status"`
	Error        string `json:"error,omitempty"`
	Rows         int64  `json:"rows"`
	Matches      int64  `json:"matches"`
	AssetMatches int64  `json:"assetMatches"`
	AssetFiles   int    `json:"assetFiles"`
	FirstTime    int64  `json:"firstTime,omitempty"`
	LastTime     int64  `json:"lastTime,omitempty"`
}
type AnalysisInfo struct {
	Active       bool             `json:"active"`
	Phase        string           `json:"phase"`
	Total        int              `json:"total"`
	Completed    int              `json:"completed"`
	Failed       int              `json:"failed"`
	CurrentFile  string           `json:"currentFile"`
	Progress     float64          `json:"progress"`
	Rows         int64            `json:"rows"`
	TotalMatches int64            `json:"totalMatches"`
	MatchedFiles int              `json:"matchedFiles"`
	Results      []AnalysisResult `json:"results"`
	Offset       int              `json:"offset"`
	Limit        int              `json:"limit"`
	TotalResults int              `json:"totalResults"`
	Error        string           `json:"error,omitempty"`
}
type AnalysisBar struct {
	Index  int64              `json:"index"`
	Time   int64              `json:"time"`
	Values map[string]float64 `json:"values"`
}
type AnalysisChart struct {
	ResultID     string        `json:"resultId"`
	Name         string        `json:"name"`
	Symbol       string        `json:"symbol"`
	Timeframe    string        `json:"timeframe"`
	Occurrence   int           `json:"occurrence"`
	TotalMatches int64         `json:"totalMatches"`
	MatchTime    int64         `json:"matchTime"`
	MatchIndex   int64         `json:"matchIndex"`
	StartIndex   int64         `json:"startIndex"`
	Fields       []string      `json:"fields"`
	Bars         []AnalysisBar `json:"bars"`
}
type analysisState struct {
	active                                     bool
	phase, directory, work, current, errorText string
	progress                                   float64
	rows                                       int64
	cancel                                     context.CancelFunc
	done                                       chan struct{}
	request                                    AnalysisRequest
	results                                    []AnalysisResult
	series                                     map[string]*analysisSeries
	matchFiles                                 map[string]string
	// Duplicate files still count as indexed inputs and remain available as
	// companion sources, but publish only one canonical set of occurrences.
	duplicateOf map[string]string
	cache       *analysisRowCache
	asOf        int64
}

func defaultAnalysisRequest() AnalysisRequest {
	return AnalysisRequest{Conditions: []AnalysisCondition{{ID: "condition-1", Operator: "above", Comparison: "above", Left: AnalysisOperand{Kind: "field", Field: "close", Timeframe: "same", Aggregate: "value", Window: 1}, Right: AnalysisOperand{Kind: "number"}, Upper: AnalysisOperand{Kind: "number", Value: 100}, Distance: AnalysisOperand{Kind: "field", Field: "atr_14", Timeframe: "same", Aggregate: "value", Window: 1}, Multiplier: 0.2, Bars: 3}}}
}

func validAnalysisTimeframe(tf string) bool {
	if tf == "" || tf == "same" {
		return true
	}
	for _, value := range loadCatalog().Timeframes {
		if value == tf {
			return true
		}
	}
	return false
}
func conditionOperands(c AnalysisCondition) []*AnalysisOperand {
	op := c.Operator
	if op == "occurredWithin" || op == "remainedFor" {
		op = c.Comparison
	}
	values := []*AnalysisOperand{&c.Left, &c.Right}
	if op == "between" {
		values = append(values, &c.Upper)
	}
	if op == "withinDistance" {
		values = append(values, &c.Distance)
	}
	return values
}
func baseAnalysisOperator(op string) bool {
	switch op {
	case "above", "below", "crossesAbove", "crossesBelow", "between", "withinDistance", "highest", "lowest":
		return true
	}
	return false
}
func validateAnalysisRequest(request AnalysisRequest, draft bool) error {
	if err := validateAnalysisCategories(request.Categories); err != nil {
		return err
	}
	if len(request.Conditions) > 32 || (!draft && len(request.Conditions) == 0) {
		return errors.New("analysis requires between 1 and 32 condition rows")
	}
	seen := map[string]bool{}
	for i, c := range request.Conditions {
		if c.ID == "" || len(c.ID) > 128 || seen[c.ID] {
			return fmt.Errorf("condition %d requires a unique ID", i+1)
		}
		seen[c.ID] = true
		if !baseAnalysisOperator(c.Operator) && c.Operator != "occurredWithin" && c.Operator != "remainedFor" {
			return fmt.Errorf("unsupported condition operator %q", c.Operator)
		}
		if (c.Operator == "occurredWithin" || c.Operator == "remainedFor") && !baseAnalysisOperator(c.Comparison) {
			return errors.New("choose a comparison for the bar window condition")
		}
		if c.Bars < 0 || c.Bars > 10000 || ((c.Operator == "occurredWithin" || c.Operator == "remainedFor" || c.Operator == "highest" || c.Operator == "lowest" || c.Comparison == "highest" || c.Comparison == "lowest") && c.Bars < 1) {
			return errors.New("condition bar window must be between 1 and 10000")
		}
		if math.IsNaN(c.Multiplier) || math.IsInf(c.Multiplier, 0) || c.Multiplier < 0 {
			return errors.New("distance multiplier must be a finite nonnegative number")
		}
		for _, operand := range conditionOperands(c) {
			if operand.Kind != "field" && operand.Kind != "number" {
				return errors.New("choose a field or a number for each side")
			}
			if operand.Kind == "number" {
				if math.IsNaN(operand.Value) || math.IsInf(operand.Value, 0) {
					return errors.New("analysis constants must be finite")
				}
				continue
			}
			if (!draft && strings.TrimSpace(operand.Field) == "") || len(operand.Field) > 1024 {
				return errors.New("choose a data field for each field operand")
			}
			if !validAnalysisTimeframe(operand.Timeframe) {
				return fmt.Errorf("unsupported operand timeframe %q", operand.Timeframe)
			}
			if operand.Offset < 0 || operand.Offset > 10000 {
				return errors.New("earlier bar offset must be between 0 and 10000")
			}
			if operand.Aggregate != "" && operand.Aggregate != "value" && operand.Aggregate != "highest" && operand.Aggregate != "lowest" {
				return errors.New("unsupported operand aggregation")
			}
			if operand.Window < 0 || operand.Window > 10000 || ((operand.Aggregate == "highest" || operand.Aggregate == "lowest") && operand.Window < 1) {
				return errors.New("operand window must be between 1 and 10000")
			}
		}
	}
	return nil
}
func copyAnalysisRequest(request AnalysisRequest) AnalysisRequest {
	request.Conditions = append([]AnalysisCondition{}, request.Conditions...)
	if request.Categories != nil {
		request.Categories = append([]string{}, request.Categories...)
	}
	return request
}

func validateAnalysisCategories(categories []string) error {
	known := map[string]bool{otherAssetCategory: true}
	for _, group := range loadCatalog().Groups {
		known[group.Name] = true
	}
	seen := map[string]bool{}
	for _, category := range categories {
		if !known[category] {
			return fmt.Errorf("unsupported asset category %q", category)
		}
		if seen[category] {
			return fmt.Errorf("duplicate asset category %q", category)
		}
		seen[category] = true
	}
	return nil
}

func includesAnalysisCategory(categories []string, category string) bool {
	if categories == nil {
		return true
	}
	for _, selected := range categories {
		if selected == category {
			return true
		}
	}
	return false
}
func (m *Manager) AnalysisView() (AnalysisRequest, error) {
	var saved struct {
		Version int             `json:"version"`
		Request AnalysisRequest `json:"request"`
	}
	err := readState(filepath.Join(m.profile, "screener-analysis.json"), 1024*1024, &saved)
	if errors.Is(err, os.ErrNotExist) {
		return defaultAnalysisRequest(), nil
	}
	if err != nil {
		return AnalysisRequest{}, fmt.Errorf("restore analysis conditions: %w", err)
	}
	if saved.Version != 1 {
		return AnalysisRequest{}, errors.New("unsupported saved analysis version; original state preserved")
	}
	if err = validateAnalysisRequest(saved.Request, true); err != nil {
		return AnalysisRequest{}, err
	}
	return saved.Request, nil
}
func (m *Manager) SaveAnalysisView(request AnalysisRequest) (AnalysisRequest, error) {
	if err := validateAnalysisRequest(request, true); err != nil {
		return AnalysisRequest{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return AnalysisRequest{}, errors.New("screener is closed")
	}
	// Preserve corrupt/future versions until explicitly repaired by the user.
	if _, err := m.AnalysisView(); err != nil {
		return AnalysisRequest{}, err
	}
	request = copyAnalysisRequest(request)
	err := writeState(filepath.Join(m.profile, "screener-analysis.json"), struct {
		Version int             `json:"version"`
		Request AnalysisRequest `json:"request"`
	}{1, request})
	return request, err
}
func (m *Manager) AnalysisCatalog() (AnalysisCatalog, error) {
	m.mu.Lock()
	directory := m.directory
	m.mu.Unlock()
	catalog := AnalysisCatalog{Directory: directory, Fields: []string{}, Timeframes: []string{}, Files: []DataFile{}, Categories: []string{}}
	for _, group := range loadCatalog().Groups {
		catalog.Categories = append(catalog.Categories, group.Name)
	}
	names, err := csvNames(directory)
	if err != nil {
		return catalog, err
	}
	fields, tfs := map[string]bool{}, map[string]bool{}
	hasOther := false
	for _, name := range names {
		file := describeFile(directory, name)
		catalog.Files = append(catalog.Files, file)
		hasOther = hasOther || file.Category == otherAssetCategory
		for _, field := range file.Fields {
			normalized := strings.ToLower(strings.TrimSpace(field))
			if normalized != "timestamp" {
				fields[normalized] = true
			}
		}
		if file.Timeframe != "" {
			tfs[file.Timeframe] = true
		}
	}
	if hasOther {
		catalog.Categories = append(catalog.Categories, otherAssetCategory)
	}
	// "price" resolves to close, then bidPrice, then askPrice, in that order.
	if fields["close"] || fields["bidprice"] || fields["askprice"] {
		fields["price"] = true
	}
	for field := range fields {
		catalog.Fields = append(catalog.Fields, field)
	}
	sort.Strings(catalog.Fields)
	for _, tf := range loadCatalog().Timeframes {
		if tfs[tf] {
			catalog.Timeframes = append(catalog.Timeframes, tf)
		}
	}
	return catalog, nil
}
func (m *Manager) analysisActiveLocked() bool { return m.analysis != nil && m.analysis.active }
func (m *Manager) cleanupAnalysisLocked() {
	if m.analysis == nil || m.analysis.active {
		return
	}
	if m.analysis.work != "" {
		_ = os.RemoveAll(m.analysis.work)
	}
	m.analysis = nil
}
func (m *Manager) analysisInfoLocked(offset, limit int) AnalysisInfo {
	return m.analysisFilteredInfoLocked(offset, limit, AnalysisFilter{IncludeEmpty: true})
}
func (m *Manager) analysisFilteredInfoLocked(offset, limit int, filter AnalysisFilter) AnalysisInfo {
	info := AnalysisInfo{Phase: "idle", Results: []AnalysisResult{}, Offset: offset, Limit: limit}
	a := m.analysis
	if a == nil {
		return info
	}
	info.Active, info.Phase, info.CurrentFile, info.Progress, info.Rows, info.Error = a.active, a.phase, a.current, a.progress, a.rows, a.errorText
	info.Total = len(a.results)
	assetMatches, assetFiles := map[string]int64{}, map[string]int{}
	filtered := []AnalysisResult{}
	for _, r := range a.results {
		if r.Status == "completed" || r.Status == "failed" || r.Status == "cancelled" {
			info.Completed++
		}
		if r.Status == "failed" {
			info.Failed++
		}
		if a.duplicateOf[r.ID] != "" {
			continue
		}
		info.TotalMatches += r.Matches
		if r.Matches > 0 {
			info.MatchedFiles++
			assetFiles[r.Symbol]++
			assetMatches[r.Symbol] += r.Matches
		}
		if includesAnalysisCategory(filter.Categories, r.Category) && (filter.IncludeEmpty || r.Matches > 0 || r.Status == "failed" || r.Status == "cancelled") {
			filtered = append(filtered, r)
		}
	}
	info.TotalResults = len(filtered)
	start := min(offset, len(filtered))
	info.Results = append(info.Results, filtered[start:start+min(limit, len(filtered)-start)]...)
	for i := range info.Results {
		symbol := info.Results[i].Symbol
		info.Results[i].AssetMatches = assetMatches[symbol]
		info.Results[i].AssetFiles = assetFiles[symbol]
	}
	return info
}
func (m *Manager) AnalysisFiltered(offset, limit int, filter AnalysisFilter) (AnalysisInfo, error) {
	if offset < 0 || limit < 1 || limit > 200 {
		return AnalysisInfo{}, errors.New("analysis page requires a nonnegative offset and limit between 1 and 200")
	}
	if err := validateAnalysisCategories(filter.Categories); err != nil {
		return AnalysisInfo{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.analysisFilteredInfoLocked(offset, limit, filter), nil
}
func (m *Manager) Analysis(offset, limit int) (AnalysisInfo, error) {
	if offset < 0 || limit < 1 || limit > 200 {
		return AnalysisInfo{}, errors.New("analysis page requires a nonnegative offset and limit between 1 and 200")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.analysisInfoLocked(offset, limit), nil
}
func (m *Manager) StartAnalysis(request AnalysisRequest) (AnalysisInfo, error) {
	if err := validateAnalysisRequest(request, false); err != nil {
		return AnalysisInfo{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return AnalysisInfo{}, errors.New("screener is closed")
	}
	if m.analysisActiveLocked() {
		return m.analysisInfoLocked(0, 100), errors.New("an analysis is already running")
	}
	if m.enrichmentActive || m.refreshActive {
		return m.analysisInfoLocked(0, 100), errors.New("finish or cancel data refresh and enrichment before applying analysis")
	}
	names, err := csvNames(m.directory)
	if err != nil {
		return AnalysisInfo{}, err
	}
	if len(names) == 0 {
		return AnalysisInfo{}, errors.New("there are no downloaded CSV files to analyze")
	}
	if request.Categories != nil && len(request.Categories) == 0 {
		return AnalysisInfo{}, errors.New("select at least one asset category")
	}
	files := []DataFile{}
	for _, name := range names {
		file := describeFile(m.directory, name)
		if includesAnalysisCategory(request.Categories, file.Category) {
			files = append(files, file)
		}
	}
	if len(files) == 0 {
		return AnalysisInfo{}, errors.New("there are no downloaded CSV files in the selected asset categories")
	}
	if err = os.MkdirAll(m.profile, 0700); err != nil {
		return AnalysisInfo{}, err
	}
	work, err := os.MkdirTemp(m.profile, ".screener-analysis-*")
	if err != nil {
		return AnalysisInfo{}, err
	}
	m.cleanupAnalysisLocked()
	ctx, cancel := context.WithCancel(context.Background())
	a := &analysisState{active: true, phase: "indexing", directory: m.directory, work: work, cancel: cancel, done: make(chan struct{}), request: copyAnalysisRequest(request), series: map[string]*analysisSeries{}, matchFiles: map[string]string{}, results: []AnalysisResult{}, cache: newAnalysisRowCache(), asOf: time.Now().UTC().UnixMilli()}
	for _, file := range files {
		id := file.ID
		a.results = append(a.results, AnalysisResult{ID: id, FileID: id, Name: file.Name, Symbol: file.Symbol, Description: file.Description, Category: file.Category, Timeframe: file.Timeframe, Status: "queued"})
	}
	m.analysis = a
	m.wg.Add(1)
	go m.runAnalysis(ctx, a)
	return m.analysisInfoLocked(0, 100), nil
}
func (m *Manager) CancelAnalysis() AnalysisInfo {
	m.mu.Lock()
	a := m.analysis
	if a != nil && a.cancel != nil {
		a.cancel()
	}
	var done chan struct{}
	if a != nil {
		done = a.done
	}
	m.mu.Unlock()
	if done != nil {
		<-done
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.analysisInfoLocked(0, 100)
}
func (m *Manager) runAnalysis(ctx context.Context, a *analysisState) {
	defer m.wg.Done()
	defer func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		a.active = false
		a.cancel = nil
		a.current = ""
		a.progress = 100
		if ctx.Err() != nil {
			a.phase = "cancelled"
			for i := range a.results {
				if a.results[i].Status == "queued" || a.results[i].Status == "running" || a.results[i].Status == "indexing" {
					a.results[i].Status = "cancelled"
					a.results[i].Matches = 0
					a.results[i].FirstTime = 0
					a.results[i].LastTime = 0
				}
			}
		} else {
			a.phase = "completed"
		}
		close(a.done)
	}()
	// Only the background worker mutates/reads series until scanning finishes;
	// chart requests are rejected while active. Pollers access result summaries.
	for i := range a.results {
		if ctx.Err() != nil {
			return
		}
		m.mu.Lock()
		r := a.results[i]
		a.current = r.Name
		a.results[i].Status = "indexing"
		m.mu.Unlock()
		s, err := indexAnalysisCSV(ctx, a.directory, r.Name, filepath.Join(a.work, r.ID+".rows"), func(progress float64) {
			m.mu.Lock()
			a.progress = (float64(i) + progress/100) / float64(len(a.results)) * 50
			m.mu.Unlock()
		})
		m.mu.Lock()
		if err != nil {
			if ctx.Err() != nil {
				a.results[i].Status = "cancelled"
			} else {
				a.results[i].Status = "failed"
				a.results[i].Error = cleanMessage(err.Error())
			}
		} else {
			s.cache = a.cache
			a.series[r.ID] = s
			a.results[i].Status = "queued"
		}
		m.mu.Unlock()
	}
	m.mu.Lock()
	a.markDuplicateResults()
	a.phase = "scanning"
	m.mu.Unlock()
	for i := range a.results {
		if ctx.Err() != nil {
			return
		}
		m.mu.Lock()
		r := a.results[i]
		if r.Status == "failed" || a.duplicateOf[r.ID] != "" {
			m.mu.Unlock()
			continue
		}
		a.current = r.Name
		a.results[i].Status = "running"
		m.mu.Unlock()
		err := m.scanAnalysisFile(ctx, a, i)
		m.mu.Lock()
		if err != nil {
			if ctx.Err() != nil {
				a.results[i].Status = "cancelled"
			} else {
				a.results[i].Status = "failed"
				a.results[i].Error = cleanMessage(err.Error())
			}
			a.results[i].Matches = 0
			a.results[i].FirstTime = 0
			a.results[i].LastTime = 0
		} else {
			a.results[i].Status = "completed"
		}
		m.mu.Unlock()
	}
}

// Called after indexing while holding the manager lock. Requested dates in a
// filename can precede an asset's actual available history, so two downloads
// may contain identical bars and indicators. Keep their files untouched and
// deterministically publish the lexically first name once. All indexed series
// remain available: an identical companion can have a later verified horizon.
func (a *analysisState) markDuplicateResults() {
	type identity struct {
		symbol, timeframe string
		contentHash       [32]byte
	}
	indices := make([]int, 0, len(a.results))
	for i, r := range a.results {
		if a.series[r.ID] != nil {
			indices = append(indices, i)
		}
	}
	sort.Slice(indices, func(i, j int) bool { return a.results[indices[i]].Name < a.results[indices[j]].Name })
	a.duplicateOf = map[string]string{}
	canonical := map[identity]string{}
	for _, i := range indices {
		r := &a.results[i]
		s := a.series[r.ID]
		key := identity{r.Symbol, r.Timeframe, s.contentHash}
		if id := canonical[key]; id != "" {
			a.duplicateOf[r.ID] = id
			r.Status = "completed"
		} else {
			canonical[key] = r.ID
		}
	}
}

func (m *Manager) scanAnalysisFile(ctx context.Context, a *analysisState, index int) error {
	r := a.results[index]
	s := a.series[r.ID]
	compiled, err := compileAnalysisConditions(a, s)
	if err != nil {
		return err
	}
	path := filepath.Join(a.work, r.ID+".matches")
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer out.Close()
	buffered := bufio.NewWriterSize(out, 64*1024)
	var buf [16]byte
	var matches, first, last int64
	now := a.asOf
	closedCount, err := s.closedCount(now)
	if err != nil {
		return err
	}
	var checked, reported int64
	defer func() { m.mu.Lock(); a.rows += checked - reported; a.results[index].Rows = checked; m.mu.Unlock() }()
	for rowIndex := int64(0); rowIndex < closedCount; rowIndex++ {
		if rowIndex%256 == 0 {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			m.mu.Lock()
			a.progress = 50 + (float64(index)+float64(rowIndex)/float64(max(closedCount, 1)))/float64(len(a.results))*50
			a.rows += checked - reported
			reported = checked
			a.results[index].Rows = checked
			a.results[index].Matches = matches
			a.results[index].FirstTime = first
			a.results[index].LastTime = last
			m.mu.Unlock()
		}
		row, err := s.row(rowIndex)
		if err != nil {
			return err
		}
		closed := analysisClosedAt(row.timestamp, s.metadata.Timeframe)
		if closed > now {
			continue
		}
		match := true
		for _, condition := range compiled {
			ok, e := condition.evaluate(rowIndex, closed)
			if e != nil {
				return e
			}
			if !ok {
				match = false
			}
		}
		checked++
		if match {
			binary.LittleEndian.PutUint64(buf[:8], uint64(rowIndex))
			binary.LittleEndian.PutUint64(buf[8:], uint64(row.timestamp))
			if _, err = buffered.Write(buf[:]); err != nil {
				return fmt.Errorf("store analysis matches: %w", err)
			}
			matches++
			if matches == 1 {
				first = row.timestamp
			}
			last = row.timestamp
		}
	}
	if err = buffered.Flush(); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	for _, dependency := range compiled {
		for _, series := range dependency.dependencies() {
			if err = analysisUnchanged(series); err != nil {
				return err
			}
		}
	}
	if err = analysisUnchanged(s); err != nil {
		return err
	}
	m.mu.Lock()
	a.matchFiles[r.ID] = path
	a.results[index].Matches = matches
	a.results[index].FirstTime = first
	a.results[index].LastTime = last
	m.mu.Unlock()
	return nil
}
func (m *Manager) AnalysisChart(resultID string, occurrence, before, after int) (AnalysisChart, error) {
	if occurrence < 0 || before < 0 || after < 0 || before > 500 || after > 500 {
		return AnalysisChart{}, errors.New("chart occurrence must be nonnegative and each context window at most 500 bars")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.analysis
	if m.closed || a == nil {
		return AnalysisChart{}, errors.New("apply analysis before opening a match")
	}
	if a.active {
		return AnalysisChart{}, errors.New("wait for analysis to finish before opening its chart")
	}
	var result AnalysisResult
	found := false
	for _, r := range a.results {
		if r.ID == resultID {
			result = r
			found = true
			break
		}
	}
	if !found || result.Status != "completed" || int64(occurrence) >= result.Matches {
		return AnalysisChart{}, errors.New("analysis match does not exist")
	}
	s := a.series[resultID]
	if s == nil {
		return AnalysisChart{}, errors.New("analysis chart index is unavailable")
	}
	// Cross-timeframe dependencies are also part of the result snapshot.
	compiled, err := compileAnalysisConditions(a, s)
	if err != nil {
		return AnalysisChart{}, err
	}
	for _, c := range compiled {
		for _, dependency := range c.dependencies() {
			if err = analysisUnchanged(dependency); err != nil {
				return AnalysisChart{}, err
			}
		}
	}
	if err = analysisUnchanged(s); err != nil {
		return AnalysisChart{}, err
	}
	f, err := os.Open(a.matchFiles[resultID])
	if err != nil {
		return AnalysisChart{}, err
	}
	defer f.Close()
	var b [16]byte
	if _, err = f.ReadAt(b[:], int64(occurrence)*16); err != nil {
		return AnalysisChart{}, err
	}
	matchIndex := int64(binary.LittleEndian.Uint64(b[:8]))
	matchTime := int64(binary.LittleEndian.Uint64(b[8:]))
	start := max(int64(0), matchIndex-int64(before))
	end := min(s.count, matchIndex+int64(after)+1)
	chart := AnalysisChart{ResultID: resultID, Name: result.Name, Symbol: result.Symbol, Timeframe: result.Timeframe, Occurrence: occurrence, TotalMatches: result.Matches, MatchTime: matchTime, MatchIndex: matchIndex, StartIndex: start, Fields: append([]string{}, s.headers...), Bars: []AnalysisBar{}}
	qualified := map[string]*analysisOperandReader{}
	for _, condition := range compiled {
		for _, reader := range []*analysisOperandReader{condition.left, condition.right, condition.upper, condition.distance} {
			if reader == nil || reader.operand.Kind != "field" {
				continue
			}
			operand := reader.operand
			if operand.Timeframe == "" || operand.Timeframe == "same" || operand.Timeframe == s.metadata.Timeframe {
				continue
			}
			key := operand.Timeframe + ":" + strings.ToLower(operand.Field)
			if operand.Offset > 0 {
				key += fmt.Sprintf("[offset %d]", operand.Offset)
			}
			if operand.Aggregate == "highest" || operand.Aggregate == "lowest" {
				key += fmt.Sprintf("[%s %d]", operand.Aggregate, operand.Window)
			}
			qualified[key] = reader
		}
	}
	qualifiedNames := []string{}
	for key := range qualified {
		qualifiedNames = append(qualifiedNames, key)
	}
	sort.Strings(qualifiedNames)
	chart.Fields = append(chart.Fields, qualifiedNames...)
	for i := start; i < end; i++ {
		row, e := s.row(i)
		if e != nil {
			return AnalysisChart{}, e
		}
		at := analysisClosedAt(row.timestamp, s.metadata.Timeframe)
		if at > a.asOf {
			break
		}
		values := map[string]float64{}
		for k, v := range row.values {
			if !math.IsNaN(v) && !math.IsInf(v, 0) {
				values[s.headers[k]] = v
			}
		}
		for _, name := range qualifiedNames {
			value, ok, e := qualified[name].resolve(i, at, 0, 0)
			if e != nil {
				return AnalysisChart{}, e
			}
			if ok {
				values[name] = value
			}
		}
		chart.Bars = append(chart.Bars, AnalysisBar{Index: i, Time: row.timestamp, Values: values})
	}
	return chart, nil
}

package screener

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
)

// Live Alerts deliberately operate on existing enriched histories. They do not
// create a second source, bootstrap a short indicator history, or bypass the
// downloader's shared request pacing and provider cooldown.
type LiveAlertConditionPeriod struct {
	ConditionID string `json:"conditionId"`
	Period      string `json:"period"`
}

type LiveAlertsConfig struct {
	Enabled      bool     `json:"enabled"`
	ConditionIDs []string `json:"conditionIds"`
	// Deprecated v1/v2 fields are accepted only to restore saved selections.
	ConditionPeriods []LiveAlertConditionPeriod `json:"conditionPeriods,omitempty"`
	Symbols          []string                   `json:"symbols,omitempty"`
	Periods          []string                   `json:"periods,omitempty"`
	// Effective periods are derived from the saved rules, never from public input.
	scope    []LiveAlertConditionPeriod
	calendar []SavedAnalysisCondition
}

type LiveAlertTarget struct {
	Symbol        string                       `json:"symbol"`
	Name          string                       `json:"name"`
	Category      string                       `json:"category"`
	Period        string                       `json:"period"`
	Profitability *SavedConditionProfitability `json:"profitability,omitempty"`
}

type LiveAlertConditionScope struct {
	ConditionID string            `json:"conditionId"`
	Period      string            `json:"period"`
	Targets     []LiveAlertTarget `json:"targets"`
	Error       string            `json:"error"`
}

type LiveAlertAsset struct {
	Symbol   string   `json:"symbol"`
	Name     string   `json:"name"`
	Category string   `json:"category"`
	Periods  []string `json:"periods"`
}

// Time is the signal candle's opening time in UTC milliseconds. EntryPrice is
// its close, an indicative entry available at signal time. A future next-open
// execution price cannot be known by a closed-bar alert.
type LiveAlert struct {
	ConditionID   string                       `json:"conditionId"`
	ConditionName string                       `json:"conditionName"`
	Symbol        string                       `json:"symbol"`
	AssetName     string                       `json:"assetName"`
	Timeframe     string                       `json:"timeframe"`
	Time          int64                        `json:"time"`
	EntryPrice    float64                      `json:"entryPrice"`
	StopLoss      *float64                     `json:"stopLoss,omitempty"`
	TakeProfit    *float64                     `json:"takeProfit,omitempty"`
	Profitability *SavedConditionProfitability `json:"profitability,omitempty"`
}

type LiveAlertOutcome struct {
	ConditionName string                       `json:"conditionName"`
	Symbol        string                       `json:"symbol"`
	AssetName     string                       `json:"assetName"`
	Timeframe     string                       `json:"timeframe"`
	Time          int64                        `json:"time"`
	Status        string                       `json:"status"`
	Message       string                       `json:"message"`
	Profitability *SavedConditionProfitability `json:"profitability,omitempty"`
}

type LiveAlertsInfo struct {
	SavedConditionsRevision uint64                    `json:"savedConditionsRevision"`
	Config                  LiveAlertsConfig          `json:"config"`
	ConditionScopes         []LiveAlertConditionScope `json:"conditionScopes"`
	Running                 bool                      `json:"running"`
	LastRun                 int64                     `json:"lastRun"`
	NextRun                 int64                     `json:"nextRun"`
	Sent                    int64                     `json:"sent"`
	Error                   string                    `json:"error"`
	Status                  string                    `json:"status"`
	Progress                float64                   `json:"progress"`
	CurrentAsset            string                    `json:"currentAsset"`
	Assets                  []LiveAlertAsset          `json:"assets"`
	Recent                  []LiveAlertOutcome        `json:"recent"`
	Checks                  []LiveAlertCheck          `json:"checks"`
}

type liveAlertsState struct {
	Version    int                            `json:"version"`
	Config     LiveAlertsConfig               `json:"config"`
	Watermarks map[string]int64               `json:"watermarks"`
	Sent       int64                          `json:"sent"`
	Recent     []LiveAlertOutcome             `json:"recent"`
	Checks     map[string]liveAlertCheckState `json:"checks,omitempty"`
}

const liveAlertsStateBytes = 64 * 1024 * 1024
const liveAlertRetryDelay = 30 * time.Second
const liveAlertPublicationWindow = 5 * time.Minute

var errLiveAlertsPendingBar = errors.New("the newly closed bar is not yet available from the provider")

var errLiveAlertsBusy = errors.New("downloads, refresh, enrichment or Analysis are active; Live Alerts will check after they finish")

// LiveAlerts owns only local configuration, scheduling and delivery claims.
// The caller starts it with the application context and closes it before the
// Manager. Construction and configuration alone never contact a provider.
type LiveAlerts struct {
	mu                              sync.Mutex
	wg                              sync.WaitGroup
	manager                         *Manager
	path                            string
	state                           liveAlertsState
	blocked, started, closed        bool
	running, requested              bool
	watermarksDirty                 bool
	lastRun, nextRun, nextBoundary  int64
	retryConfig                     *LiveAlertsConfig
	retryAsOf                       int64
	retryManual                     bool
	configRevision                  uint64
	scheduleScope                   []LiveAlertConditionPeriod
	scheduleCalendar                string
	now                             func() time.Time
	status, errorText, currentAsset string
	progress                        float64
	wake                            chan struct{}
	cancel, cycleCancel             context.CancelFunc
	send                            func(context.Context, LiveAlert) error
	// Tests replace the transport only. Indexing, rule evaluation, claims and
	// scheduling still use production code and real on-disk CSVs.
	refresh func(context.Context, string, []historyGroup, int64) (map[string]bool, error)
}

func NewLiveAlerts(manager *Manager, send func(context.Context, LiveAlert) error) (*LiveAlerts, error) {
	l := &LiveAlerts{manager: manager, path: filepath.Join(manager.profile, "screener-live-alerts.json"), send: send, wake: make(chan struct{}, 1), status: "Disabled", now: time.Now, state: liveAlertsState{Version: 4, Config: LiveAlertsConfig{ConditionIDs: []string{}}, Watermarks: map[string]int64{}, Recent: []LiveAlertOutcome{}}}
	st, err := os.Lstat(l.path)
	if errors.Is(err, os.ErrNotExist) {
		return l, nil
	}
	if err == nil && (!st.Mode().IsRegular() || st.Size() > liveAlertsStateBytes) {
		err = errors.New("Live Alerts state must be a regular file no larger than 64 MiB")
	}
	if err == nil {
		err = readState(l.path, liveAlertsStateBytes, &l.state)
	}
	if err == nil {
		err = validateLiveAlertsState(l.state)
	}
	if err != nil {
		l.blocked, l.errorText, l.status = true, "Restore Live Alerts: "+cleanMessage(err.Error())+"; original state preserved", "Saved state could not be loaded"
		return l, errors.New(l.errorText)
	}
	legacyEnabled := l.state.Version < 3 && l.state.Config.Enabled
	l.state.Config = canonicalLiveAlertsConfig(l.state.Config)
	l.state.Version = 4
	if legacyEnabled {
		l.state.Config.Enabled = false
		l.errorText = "Live Alerts now inherits each saved condition's analyzed assets and periods. Review the inherited scope and start alerts again. Previous delivery history is preserved."
		l.status = "Review inherited saved condition scope"
	}
	// A claim committed before interruption may have reached Telegram. Keeping
	// its watermark and showing uncertainty avoids duplicate notifications.
	for i := range l.state.Recent {
		if l.state.Recent[i].Status == "claimed" {
			l.state.Recent[i].Status, l.state.Recent[i].Message = "uncertain", "Delivery was interrupted; check Telegram. This bar will not be sent again automatically."
		}
	}
	for id, record := range l.state.Checks {
		if record.Status == "running" {
			record.Status, record.Error = "cancelled", "Check was interrupted before completion; the recorded scan count may be incomplete."
			l.state.Checks[id] = record
		}
	}
	if l.state.Config.Enabled {
		l.status = "Waiting for application startup"
	}
	if l.state.Watermarks == nil {
		l.state.Watermarks = map[string]int64{}
	}
	return l, nil
}

func copyLiveAlertsConfig(config LiveAlertsConfig) LiveAlertsConfig {
	config.ConditionIDs = append([]string{}, config.ConditionIDs...)
	config.calendar = append([]SavedAnalysisCondition(nil), config.calendar...)
	config.ConditionPeriods = append([]LiveAlertConditionPeriod(nil), config.ConditionPeriods...)
	config.Periods = append([]string(nil), config.Periods...)
	config.Symbols = append([]string(nil), config.Symbols...)
	if config.scope != nil {
		config.scope = append([]LiveAlertConditionPeriod{}, config.scope...)
	}
	return config
}

// Migration retains condition identities; obsolete choices never determine scope.
func canonicalLiveAlertsConfig(config LiveAlertsConfig) LiveAlertsConfig {
	config = copyLiveAlertsConfig(config)
	if len(config.ConditionIDs) == 0 {
		for _, rule := range config.ConditionPeriods {
			config.ConditionIDs = append(config.ConditionIDs, rule.ConditionID)
		}
	}
	config.ConditionPeriods, config.Symbols, config.Periods = nil, nil, nil
	return config
}

func liveAlertsPeriods(config LiveAlertsConfig) []string {
	periods := []string{}
	for _, rule := range config.scope {
		if !containsLiveValue(periods, rule.Period) {
			periods = append(periods, rule.Period)
		}
	}
	return periods
}

func liveAlertsScopeAllows(config LiveAlertsConfig, id, period string) bool {
	if config.scope == nil {
		return true
	}
	for _, rule := range config.scope {
		if rule.ConditionID == id && rule.Period == period {
			return true
		}
	}
	return false
}

func validateLiveAlertsConfig(config LiveAlertsConfig) error {
	if len(config.ConditionIDs) > savedConditionsLimit || len(config.Periods) > 2 || len(config.ConditionPeriods) > savedConditionsLimit || len(config.Symbols) > len(catalogAssets) {
		return errors.New("too many selected Live Alerts conditions, symbols or periods")
	}
	if len(config.ConditionPeriods) > 0 && (len(config.ConditionIDs) > 0 || len(config.Periods) > 0) {
		return errors.New("mixed legacy Live Alerts selections")
	}
	seen := map[string]bool{}
	for _, period := range config.Periods {
		if (period != "d1" && period != "h1") || seen[period] {
			return errors.New("invalid legacy Live Alerts periods")
		}
		seen[period] = true
	}
	for _, rule := range config.ConditionPeriods {
		if rule.Period != "d1" && rule.Period != "h1" && rule.Period != "" {
			return errors.New("invalid legacy Live Alerts condition period")
		}
	}
	seen = map[string]bool{}
	for _, symbol := range config.Symbols {
		if _, ok := catalogAssets[symbol]; !ok || seen[symbol] {
			return errors.New("invalid legacy Live Alerts assets")
		}
		seen[symbol] = true
	}
	config = canonicalLiveAlertsConfig(config)
	if config.Enabled && len(config.ConditionIDs) == 0 {
		return errors.New("select at least one saved condition before enabling Live Alerts")
	}
	seen = map[string]bool{}
	for _, id := range config.ConditionIDs {
		if len(id) != 32 || strings.IndexFunc(id, func(r rune) bool { return !strings.ContainsRune("0123456789abcdef", r) }) >= 0 || seen[id] {
			return errors.New("invalid or duplicate saved Live Alerts condition ID")
		}
		seen[id] = true
	}
	return nil
}

func validateLiveAlertsState(state liveAlertsState) error {
	if (state.Version != 1 && state.Version != 2 && state.Version != 3 && state.Version != 4) || state.Sent < 0 || len(state.Recent) > 50 || len(state.Watermarks) > savedConditionsLimit*len(catalogAssets)*2 {
		return errors.New("invalid Live Alerts state version or count")
	}
	validationConfig := state.Config
	if state.Version == 1 && state.Config.Enabled && (len(state.Config.ConditionIDs) == 0 || len(state.Config.Symbols) == 0 || len(state.Config.Periods) == 0) {
		return errors.New("invalid enabled legacy Live Alerts selection")
	}
	if state.Version == 2 && (len(state.Config.ConditionIDs) > 0 || len(state.Config.Periods) > 0) {
		return errors.New("version 2 Live Alerts require one period per saved condition")
	}
	if state.Version == 2 && state.Config.Enabled && (len(state.Config.ConditionPeriods) == 0 || len(state.Config.Symbols) == 0) {
		return errors.New("invalid enabled legacy Live Alerts selection")
	}
	if state.Version >= 3 && (len(state.Config.ConditionPeriods) > 0 || len(state.Config.Symbols) > 0 || len(state.Config.Periods) > 0) {
		return errors.New("version 3 Live Alerts inherit assets and periods from saved conditions")
	}
	if err := validateLiveAlertsConfig(validationConfig); err != nil {
		return err
	}
	for key, timestamp := range state.Watermarks {
		parts := strings.Split(key, "/")
		if len(parts) != 3 || timestamp < 0 || timestamp > 253_402_300_799_999 {
			return errors.New("invalid Live Alerts processed-bar state")
		}
		if err := validateLiveAlertsConfig(LiveAlertsConfig{ConditionIDs: []string{parts[0]}, Symbols: []string{parts[1]}, Periods: []string{parts[2]}}); err != nil {
			return err
		}
	}
	if err := validateLiveCheckStates(state.Checks, state.Sent); err != nil {
		return err
	}
	for _, outcome := range state.Recent {
		if outcome.Time < 0 || len(outcome.Message) > 5000 || (outcome.Status != "sent" && outcome.Status != "failed" && outcome.Status != "claimed" && outcome.Status != "uncertain") {
			return errors.New("invalid Live Alerts delivery history")
		}
		if outcome.Profitability != nil && (!validLiveProfitability(*outcome.Profitability) || outcome.Profitability.Symbol != outcome.Symbol || outcome.Profitability.Timeframe != outcome.Timeframe) {
			return errors.New("invalid Live Alerts recorded profitability")
		}
	}
	return nil
}

func (l *LiveAlerts) assets() ([]LiveAlertAsset, error) {
	l.manager.mu.Lock()
	directory := l.manager.directory
	l.manager.mu.Unlock()
	names, err := csvNames(directory)
	if err != nil {
		return []LiveAlertAsset{}, err
	}
	bySymbol := map[string]*LiveAlertAsset{}
	for _, name := range names {
		match := downloadFilename.FindStringSubmatch(strings.ToLower(name))
		if match == nil || (match[2] != "d1" && match[2] != "h1") {
			continue
		}
		if _, known := catalogAssets[match[1]]; !known {
			continue
		}
		asset := bySymbol[match[1]]
		if asset == nil {
			metadata := describeAsset(match[1], match[1])
			asset = &LiveAlertAsset{Symbol: match[1], Name: metadata.description, Category: metadata.category, Periods: []string{}}
			bySymbol[match[1]] = asset
		}
		if !containsLiveValue(asset.Periods, match[2]) {
			asset.Periods = append(asset.Periods, match[2])
		}
	}
	assets := make([]LiveAlertAsset, 0, len(bySymbol))
	for _, asset := range bySymbol {
		sort.Strings(asset.Periods)
		assets = append(assets, *asset)
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].Symbol < assets[j].Symbol })
	return assets, nil
}

func containsLiveValue(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (l *LiveAlerts) Info() LiveAlertsInfo {
	assets, assetErr := l.assets()
	l.manager.mu.Lock()
	saved, savedErr := l.manager.readSavedConditions()
	revision := l.manager.savedConditionsRevision
	if savedErr == nil {
		savedErr = l.pruneDeletedConditions(saved)
	}
	l.manager.mu.Unlock()
	l.mu.Lock()
	info := LiveAlertsInfo{SavedConditionsRevision: revision, Config: copyLiveAlertsConfig(l.state.Config), Running: l.running, LastRun: l.lastRun, NextRun: l.nextRun, Sent: l.state.Sent, Error: l.errorText, Status: l.status, Progress: l.progress, CurrentAsset: l.currentAsset, Recent: append([]LiveAlertOutcome{}, l.state.Recent...), Assets: assets, ConditionScopes: []LiveAlertConditionScope{}, Checks: []LiveAlertCheck{}}
	for i := range info.Recent {
		info.Recent[i].Profitability = copyLiveProfitability(info.Recent[i].Profitability)
	}
	states := copyLiveCheckStates(l.state.Checks)
	var retry *LiveAlertsConfig
	if l.retryConfig != nil {
		value := copyLiveAlertsConfig(*l.retryConfig)
		retry = &value
	}
	retryAt, retryAsOf, retryManual, now := l.nextRun, l.retryAsOf, l.retryManual, l.now()
	l.mu.Unlock()
	if savedErr == nil {
		for _, condition := range saved {
			info.ConditionScopes = append(info.ConditionScopes, liveConditionScope(condition, assets))
		}
		info.Checks = liveAlertCheckRows(saved, info.ConditionScopes, info.Config, states, now, retry, retryAt, retryAsOf, retryManual)
	} else if info.Error == "" {
		info.Error = cleanMessage(savedErr.Error())
	}
	if assetErr != nil && info.Error == "" {
		info.Error = cleanMessage(assetErr.Error())
	}
	return info
}

func (l *LiveAlerts) selectedConditions(config LiveAlertsConfig) ([]SavedAnalysisCondition, error) {
	l.manager.mu.Lock()
	defer l.manager.mu.Unlock()
	saved, err := l.manager.readSavedConditions()
	if err != nil {
		return nil, err
	}
	if err := l.pruneDeletedConditions(saved); err != nil {
		return nil, err
	}
	config = canonicalLiveAlertsConfig(config)
	conditions := make([]SavedAnalysisCondition, 0, len(config.ConditionIDs))
	for _, id := range config.ConditionIDs {
		found := false
		for _, condition := range saved {
			if id == condition.ID {
				conditions, found = append(conditions, condition), true
				break
			}
		}
		if !found {
			continue
		}
	}

	return conditions, nil
}

// Saved conditions have a bounded count, so their deleted identities cannot
// accumulate an unbounded history of processed bars. Retain all still-saved
// conditions, including inactive selections, to prevent re-selection repeats.
func (l *LiveAlerts) pruneDeletedConditions(saved []SavedAnalysisCondition) error {
	known := make(map[string]bool, len(saved))
	for _, condition := range saved {
		known[condition.ID] = true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	hasDeleted := false
	for _, id := range l.state.Config.ConditionIDs {
		if !known[id] {
			hasDeleted = true
		}
	}
	for id := range l.state.Checks {
		if !known[id] {
			hasDeleted = true
		}
	}
	for key := range l.state.Watermarks {
		id, _, _ := strings.Cut(key, "/")
		if !known[id] {
			hasDeleted = true
		}
	}
	if !hasDeleted {
		return nil
	}
	if l.closed || l.blocked {
		return errors.New("Live Alerts are closed or their saved state is invalid; original processed-bar state preserved")
	}
	previousConfig := copyLiveAlertsConfig(l.state.Config)
	previousWatermarks, previousChecks := l.state.Watermarks, l.state.Checks
	ids := []string{}
	for _, id := range l.state.Config.ConditionIDs {
		if known[id] {
			ids = append(ids, id)
		}
	}
	l.state.Config.ConditionIDs = ids
	if len(ids) == 0 {
		l.state.Config.Enabled = false
	}
	l.state.Watermarks = map[string]int64{}
	for key, timestamp := range previousWatermarks {
		id, _, _ := strings.Cut(key, "/")
		if known[id] {
			l.state.Watermarks[key] = timestamp
		}
	}
	l.state.Checks = map[string]liveAlertCheckState{}
	for id, record := range previousChecks {
		if known[id] {
			l.state.Checks[id] = record
		}
	}
	if err := writeState(l.path, l.state); err != nil {
		l.state.Config, l.state.Watermarks, l.state.Checks = previousConfig, previousWatermarks, previousChecks
		return fmt.Errorf("remove deleted Live Alerts condition history: %w", err)
	}
	l.configRevision++
	if !l.state.Config.Enabled {
		l.requested, l.nextRun, l.nextBoundary, l.retryConfig = false, 0, 0, nil
		l.status = "Disabled"
		if l.cycleCancel != nil {
			l.cycleCancel()
		}
	}
	l.signal()
	return nil
}

func (l *LiveAlerts) resolveConfig(config LiveAlertsConfig) (LiveAlertsConfig, error) {
	conditions, err := l.selectedConditions(config)
	if err != nil {
		return config, err
	}
	return resolvedLiveAlertsConfig(config, conditions), nil
}

func (l *LiveAlerts) validateSelection(config LiveAlertsConfig) error {
	conditions, err := l.selectedConditions(config)
	if err != nil {
		return err
	}
	assets, err := l.assets()
	if err != nil {
		return err
	}
	for _, saved := range conditions {
		scope := liveConditionScope(saved, assets)
		if scope.Error != "" {
			return errors.New(scope.Error)
		}
	}
	return nil
}

func (l *LiveAlerts) Configure(config LiveAlertsConfig) (LiveAlertsInfo, error) {
	if err := validateLiveAlertsConfig(config); err != nil {
		return l.Info(), err
	}
	config = canonicalLiveAlertsConfig(config)
	if config.Enabled {
		if err := l.validateSelection(config); err != nil {
			return l.Info(), err
		}
	}
	resolved, resolveErr := l.resolveConfig(config)
	if config.Enabled && resolveErr != nil {
		return l.Info(), resolveErr
	}
	if resolveErr == nil {
		config.ConditionIDs = append([]string{}, resolved.ConditionIDs...)
		if len(config.ConditionIDs) == 0 {
			config.Enabled = false
		}
	}
	l.mu.Lock()
	if l.closed || l.blocked || (l.running && config.Enabled) {
		l.mu.Unlock()
		return l.Info(), errors.New("Live Alerts are closed, their saved state is invalid, or a check is running; stop the check before changing the selection")
	}
	previous := l.state.Config
	l.state.Config = config
	if err := writeState(l.path, l.state); err != nil {
		l.state.Config = previous
		l.mu.Unlock()
		return l.Info(), fmt.Errorf("save Live Alerts configuration: %w", err)
	}
	l.watermarksDirty = false
	l.configRevision++
	l.errorText = ""
	if config.Enabled {
		l.scheduleScope = append([]LiveAlertConditionPeriod{}, resolved.scope...)
		l.scheduleCalendar = liveAlertsCalendarIdentity(resolved)
		l.requested, l.status = false, "Waiting for next closed bar"
		l.nextRun = nextLiveAlertsConfigCheck(l.now(), resolved)
		l.nextBoundary = l.nextRun
		l.retryConfig = nil
	} else {
		l.requested, l.nextRun, l.status = false, 0, "Disabled"
		l.retryConfig = nil
		if l.cycleCancel != nil {
			l.cycleCancel()
		}
	}
	l.mu.Unlock()
	l.signal()
	return l.Info(), nil
}

func (l *LiveAlerts) signal() {
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

func (l *LiveAlerts) Start(parent context.Context) {
	l.mu.Lock()
	config, revision := copyLiveAlertsConfig(l.state.Config), l.configRevision
	l.mu.Unlock()
	resolved, err := l.resolveConfig(config)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.started || l.closed || l.blocked {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	l.cancel, l.started = cancel, true
	if l.state.Config.Enabled {
		if revision == l.configRevision && err == nil {
			l.scheduleScope = append([]LiveAlertConditionPeriod{}, resolved.scope...)
			l.scheduleCalendar = liveAlertsCalendarIdentity(resolved)
			l.nextRun = nextLiveAlertsConfigCheck(l.now(), resolved)
			l.nextBoundary = l.nextRun
			l.status = "Waiting for next closed bar"
		} else if err != nil {
			l.errorText, l.status = cleanMessage(err.Error()), "Saved condition scope unavailable"
		}
	}
	l.wg.Add(1)
	go l.loop(ctx)
}

func (l *LiveAlerts) CheckNow() (LiveAlertsInfo, error) {
	l.mu.Lock()
	if l.closed || l.blocked || !l.state.Config.Enabled || l.running || l.requested {
		l.mu.Unlock()
		return l.Info(), errors.New("enable Live Alerts and wait for the current check before checking again")
	}
	l.requested, l.status = true, "Check queued"
	l.mu.Unlock()
	l.signal()
	return l.Info(), nil
}

func nextLiveAlertCheck(now time.Time, periods []string) time.Time {
	interval := 24 * time.Hour
	if containsLiveValue(periods, "h1") {
		interval = time.Hour
	}
	now = now.UTC()
	return now.Truncate(interval).Add(interval)
}

func liveAlertsCalendarIdentity(config LiveAlertsConfig) string {
	hash := sha256.New()
	for _, condition := range config.calendar {
		_, _ = hash.Write([]byte(condition.ID))
		encoded, _ := json.Marshal(condition.Request.Conditions)
		_, _ = hash.Write(encoded)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func liveAlertsCalendarCondition(config LiveAlertsConfig, id string) (SavedAnalysisCondition, bool) {
	for _, condition := range config.calendar {
		if condition.ID == id {
			return condition, true
		}
	}
	return SavedAnalysisCondition{}, false
}

func nextLiveAlertsConfigCheck(now time.Time, config LiveAlertsConfig) int64 {
	var next time.Time
	for _, rule := range config.scope {
		condition, found := liveAlertsCalendarCondition(config, rule.ConditionID)
		if !found {
			continue
		}
		candidate, ok := nextLiveAlertConditionClose(now, condition, rule.Period)
		if ok && (next.IsZero() || candidate.Before(next)) {
			next = candidate
		}
	}
	if next.IsZero() {
		return 0
	}
	return next.UnixMilli()
}

// Select periods whose UTC close falls between the scheduled wakeup and now.
// This includes midnight after a long previous check or a suspended process.
func dueLiveAlertsConfig(config LiveAlertsConfig, scheduled, now time.Time) LiveAlertsConfig {
	config = copyLiveAlertsConfig(config)
	config.ConditionIDs = []string{}
	selected := []LiveAlertConditionPeriod{}
	for _, rule := range config.scope {
		condition, found := liveAlertsCalendarCondition(config, rule.ConditionID)
		if !found {
			continue
		}
		due, eligible := nextLiveAlertConditionClose(scheduled.Add(-time.Millisecond), condition, rule.Period)
		if eligible && !due.After(now) && liveAlertConditionDueAt(condition, rule.Period, now) {
			selected = append(selected, rule)
			if !containsLiveValue(config.ConditionIDs, rule.ConditionID) {
				config.ConditionIDs = append(config.ConditionIDs, rule.ConditionID)
			}
		}
	}
	config.scope = selected
	return config
}

func (l *LiveAlerts) loop(ctx context.Context) {
	defer l.wg.Done()
	for {
		if ctx.Err() != nil {
			return
		}
		l.mu.Lock()
		current, revision := copyLiveAlertsConfig(l.state.Config), l.configRevision
		l.mu.Unlock()
		resolved, resolveErr := l.resolveConfig(current)
		now := l.now()
		l.mu.Lock()
		if revision != l.configRevision {
			l.mu.Unlock()
			continue
		}
		if resolveErr != nil && current.Enabled {
			l.errorText = cleanMessage(resolveErr.Error())
		}
		if current.Enabled && resolveErr == nil && (!reflect.DeepEqual(l.scheduleScope, resolved.scope) || l.scheduleCalendar != liveAlertsCalendarIdentity(resolved)) {
			// A saved rule's own period can change while waiting, including D1
			// to H1. Its new scope begins at its next natural UTC bar close.
			l.scheduleScope = append([]LiveAlertConditionPeriod{}, resolved.scope...)
			l.scheduleCalendar = liveAlertsCalendarIdentity(resolved)
			l.nextBoundary = nextLiveAlertsConfigCheck(now, resolved)
			l.nextRun, l.retryConfig = l.nextBoundary, nil
			l.status = "Waiting for next closed bar"
		}
		enabled := l.state.Config.Enabled
		run := enabled && (l.requested || (l.nextRun > 0 && now.UnixMilli() >= l.nextRun))
		config := copyLiveAlertsConfig(resolved)
		if run {
			manual := l.requested
			asOf := now.UnixMilli()
			if l.retryConfig != nil && !manual {
				config = copyLiveAlertsConfig(*l.retryConfig)
				asOf, manual = l.retryAsOf, l.retryManual
				if l.nextBoundary <= now.UnixMilli() {
					due := dueLiveAlertsConfig(resolved, time.UnixMilli(l.nextBoundary), now)
					for _, rule := range due.scope {
						if !liveAlertsScopeAllows(config, rule.ConditionID, rule.Period) {
							config.scope = append(config.scope, rule)
						}
						if !containsLiveValue(config.ConditionIDs, rule.ConditionID) {
							config.ConditionIDs = append(config.ConditionIDs, rule.ConditionID)
						}
					}
					asOf = now.UnixMilli()
				}
			} else if !manual {
				config = dueLiveAlertsConfig(config, time.UnixMilli(l.nextBoundary), now)
			}
			if !manual && len(config.ConditionIDs) == 0 {
				// After suspension, the latest closed bar may already be outside
				// the condition's calendar. Wait for its next eligible close
				// without recording an invented check or an empty-selection error.
				l.nextBoundary = nextLiveAlertsConfigCheck(now, resolved)
				l.nextRun, l.retryConfig = l.nextBoundary, nil
				l.status = "Waiting for next eligible bar close"
				l.mu.Unlock()
				continue
			}
			l.running, l.requested, l.lastRun = true, false, now.UnixMilli()
			l.progress, l.errorText, l.status = 0, "", "Refreshing selected data and indicators"
			// Retain the next original boundary even if this cycle takes a long time.
			if l.nextBoundary <= now.UnixMilli() {
				l.nextBoundary = nextLiveAlertsConfigCheck(now, resolved)
			}
			l.nextRun = l.nextBoundary
			l.retryConfig = nil
			cycle, cancel := context.WithCancel(ctx)
			l.cycleCancel = cancel
			l.mu.Unlock()
			err := l.checkCycle(cycle, config, asOf, !manual)
			cancel()
			l.mu.Lock()
			l.running, l.cycleCancel, l.currentAsset = false, nil, ""
			if !l.state.Config.Enabled {
				l.status, l.nextRun = "Disabled", 0
			} else if err != nil {
				l.errorText, l.status = cleanMessage(err.Error()), "Check stopped"
				if errors.Is(err, errLiveAlertsBusy) || (errors.Is(err, errLiveAlertsPendingBar) && l.now().Sub(time.UnixMilli(asOf)) < liveAlertPublicationWindow) {
					retry := copyLiveAlertsConfig(config)
					l.retryConfig, l.retryAsOf, l.retryManual = &retry, asOf, manual
					retryAt := l.now().Add(liveAlertRetryDelay).UnixMilli()
					if l.nextRun <= 0 {
						l.nextRun = retryAt
					} else {
						l.nextRun = min(l.nextRun, retryAt)
					}
					l.status = "Waiting to retry closed-bar check"
				}
			} else {
				l.progress, l.status = 100, "Waiting for next closed bar"
			}
		}
		wait := time.Hour
		if l.state.Config.Enabled && l.nextRun > 0 {
			wait = max(time.Millisecond, time.UnixMilli(l.nextRun).Sub(l.now()))
		}
		l.mu.Unlock()
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-l.wake:
			timer.Stop()
		case <-l.manager.savedConditionsChanged:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (l *LiveAlerts) Close() {
	l.mu.Lock()
	l.closed = true
	if l.cancel != nil {
		l.cancel()
	}
	if l.cycleCancel != nil {
		l.cycleCancel()
	}
	l.mu.Unlock()
	l.wg.Wait()
}

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

// Existing alerts retain their analyzed Dukascopy histories. Explicit public
// source overrides fetch and retain their own venue-separated history on demand.
type LiveAlertConditionSource struct {
	ConditionID string `json:"conditionId"`
	Source      string `json:"source"`
}

type LiveAlertConditionPeriod struct {
	ConditionID string `json:"conditionId"`
	Period      string `json:"period"`
}

type LiveAlertsConfig struct {
	Alerts           []LiveAlertDefinition `json:"alerts"`
	derived          bool
	Enabled          bool                       `json:"enabled"`
	ConditionIDs     []string                   `json:"conditionIds"`
	ConditionSources []LiveAlertConditionSource `json:"conditionSources,omitempty"`
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
	AvailableTargets []LiveAlertTarget `json:"availableTargets,omitempty"`
	AlertID          string            `json:"alertId,omitempty"`
	SavedConditionID string            `json:"savedConditionId,omitempty"`
	ConditionID      string            `json:"conditionId"`
	Period           string            `json:"period"`
	Targets          []LiveAlertTarget `json:"targets"`
	Error            string            `json:"error"`
	DataSource       string            `json:"dataSource,omitempty"`
	AllAssets        bool              `json:"allAssets,omitempty"`
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
	AlertID            string                       `json:"alertId,omitempty"`
	AlertName          string                       `json:"alertName,omitempty"`
	SavedConditionID   string                       `json:"savedConditionId,omitempty"`
	SavedConditionName string                       `json:"savedConditionName,omitempty"`
	DataSource         string                       `json:"dataSource,omitempty"`
	Market             string                       `json:"market,omitempty"`
	ConditionID        string                       `json:"conditionId"`
	ConditionName      string                       `json:"conditionName"`
	Symbol             string                       `json:"symbol"`
	AssetName          string                       `json:"assetName"`
	Timeframe          string                       `json:"timeframe"`
	Time               int64                        `json:"time"`
	EntryPrice         float64                      `json:"entryPrice"`
	StopLoss           *float64                     `json:"stopLoss,omitempty"`
	TakeProfit         *float64                     `json:"takeProfit,omitempty"`
	Profitability      *SavedConditionProfitability `json:"profitability,omitempty"`
	// A delivery owns its small immutable chart snapshot. Media is never part
	// of persisted alert history or the periodically polled application state.
	Chart      *AnalysisChart `json:"-"`
	ChartError string         `json:"-"`
}

type LiveAlertOutcome struct {
	AlertID             string                       `json:"alertId,omitempty"`
	AlertName           string                       `json:"alertName,omitempty"`
	SavedConditionID    string                       `json:"savedConditionId,omitempty"`
	SavedConditionName  string                       `json:"savedConditionName,omitempty"`
	ConditionID         string                       `json:"conditionId,omitempty"`
	DataSource          string                       `json:"dataSource,omitempty"`
	Market              string                       `json:"market,omitempty"`
	ConfirmedRecipients int                          `json:"confirmedRecipients,omitempty"`
	ConditionName       string                       `json:"conditionName"`
	Symbol              string                       `json:"symbol"`
	AssetName           string                       `json:"assetName"`
	Timeframe           string                       `json:"timeframe"`
	Time                int64                        `json:"time"`
	Status              string                       `json:"status"`
	Message             string                       `json:"message"`
	Profitability       *SavedConditionProfitability `json:"profitability,omitempty"`
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

var errLiveAlertsBusy = errors.New("refresh, enrichment, Analysis or another Live Alerts check is active; Live Alerts will check after it finishes")

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
	requestedIDs                    []string
	requestedAt                     time.Time
	requestRevision                 uint64
	watermarksDirty                 bool
	lastRun, nextRun, nextBoundary  int64
	retryConfig                     *LiveAlertsConfig
	retryAsOf                       int64
	retryManual                     bool
	retryTargeted                   bool
	configRevision                  uint64
	configureRevision               uint64
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
	refresh         func(context.Context, string, []historyGroup, int64) (map[string]bool, error)
	publicData      *publicAlertData
	checkExecuting  bool
	resolutionPhase string
	checkProgress   map[string]liveAlertProgress
}

func NewLiveAlerts(manager *Manager, send func(context.Context, LiveAlert) error) (*LiveAlerts, error) {
	l := &LiveAlerts{manager: manager, path: filepath.Join(manager.profile, "screener-live-alerts.json"), send: send, wake: make(chan struct{}, 1), status: "Disabled", now: time.Now, state: liveAlertsState{Version: 7, Config: LiveAlertsConfig{ConditionIDs: []string{}}, Watermarks: map[string]int64{}, Recent: []LiveAlertOutcome{}}}
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
	l.state.Version = 7
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
	if err := l.recoverLegacyPartialDeliveries(); err != nil {
		l.blocked, l.errorText = true, "Restore confirmed Live Alerts deliveries: "+cleanMessage(err.Error())+"; original state preserved"
		return l, errors.New(l.errorText)
	}
	// Current entries already carry their source and identity. Only legacy
	// selection/source migration needs the saved-condition catalog here.
	if l.state.Config.Alerts == nil || needsLiveAlertSourceDefaults(l.state.Config) {
		l.manager.mu.Lock()
		saved, savedErr := l.manager.readSavedConditions()
		l.manager.mu.Unlock()
		if savedErr == nil {
			l.state.Config = migrateLiveAlertEntries(l.state.Config, saved, l.state.Watermarks, l.state.Checks)
		}
	}
	return l, nil
}

func copyLiveAlertsConfig(config LiveAlertsConfig) LiveAlertsConfig {
	if config.Alerts != nil {
		config.Alerts = append([]LiveAlertDefinition{}, config.Alerts...)
		for i := range config.Alerts {
			config.Alerts[i] = copyLiveAlertDefinition(config.Alerts[i])
		}
	}
	config.ConditionIDs = append([]string{}, config.ConditionIDs...)
	config.ConditionSources = append([]LiveAlertConditionSource(nil), config.ConditionSources...)
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
	if config.Alerts != nil && !config.derived {
		config = projectLiveAlertEntries(config)
	}
	if config.Alerts == nil && len(config.ConditionIDs) == 0 {
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
	if config.Alerts != nil {
		if err := validateLiveAlertEntries(config.Alerts); err != nil {
			return err
		}
		config = canonicalLiveAlertsConfig(config)
	}
	if err := validateLiveAlertSources(config.ConditionSources); err != nil {
		return err
	}
	if len(config.ConditionIDs) > savedConditionsLimit || len(config.Periods) > 3 || len(config.ConditionPeriods) > savedConditionsLimit || len(config.Symbols) > len(catalogAssets) {
		return errors.New("too many selected Live Alerts conditions, symbols or periods")
	}
	if len(config.ConditionPeriods) > 0 && (len(config.ConditionIDs) > 0 || len(config.Periods) > 0) {
		return errors.New("mixed legacy Live Alerts selections")
	}
	seen := map[string]bool{}
	for _, period := range config.Periods {
		if !supportedLiveAlertPeriod(period) || seen[period] {
			return errors.New("invalid legacy Live Alerts periods")
		}
		seen[period] = true
	}
	for _, rule := range config.ConditionPeriods {
		if !supportedLiveAlertPeriod(rule.Period) && rule.Period != "" {
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
	if config.Alerts == nil && config.Enabled && len(config.ConditionIDs) == 0 {
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
	if (state.Version < 1 || state.Version > 7) || state.Sent < 0 || len(state.Recent) > 50 || len(state.Watermarks) > savedConditionsLimit*max(len(catalogAssets), 20000)*3 {
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
		if strings.HasPrefix(parts[1], "public:") {
			if !validPublicLiveAlertClaim(parts[1]) {
				return errors.New("invalid public Live Alerts identity")
			}
			if err := validateLiveAlertsConfig(LiveAlertsConfig{ConditionIDs: []string{parts[0]}}); err != nil {
				return err
			}
			if !supportedLiveAlertPeriod(parts[2]) {
				return errors.New("invalid public Live Alerts period")
			}
			continue
		}
		if err := validateLiveAlertsConfig(LiveAlertsConfig{ConditionIDs: []string{parts[0]}, Symbols: []string{parts[1]}, Periods: []string{parts[2]}}); err != nil {
			return err
		}
	}
	if err := validateLiveCheckStates(state.Checks, state.Sent); err != nil {
		return err
	}
	for _, outcome := range state.Recent {
		if outcome.Time < 0 || outcome.ConfirmedRecipients < 0 || outcome.ConfirmedRecipients > 20 || len(outcome.Message) > 5000 || (outcome.Status != "sent" && outcome.Status != "failed" && outcome.Status != "claimed" && outcome.Status != "uncertain") {
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
		if match == nil || !supportedLiveAlertPeriod(match[2]) {
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

// ProgressInfo reads only LiveAlerts-owned memory. It must remain available while
// Info's catalog/evidence refresh or a worker holds Manager.mu. Callers retain
// separately loaded rule/scope metadata when merging this lightweight snapshot.
func (l *LiveAlerts) ProgressInfo() LiveAlertsInfo {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stopInfoLocked()
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
	info := LiveAlertsInfo{SavedConditionsRevision: revision, Config: copyLiveAlertsConfig(l.state.Config), Running: l.running, LastRun: l.lastRun, NextRun: l.nextRun, Sent: l.state.Sent, Error: liveAlertErrorText(l.errorText), Status: l.status, Progress: l.progress, CurrentAsset: l.currentAsset, Recent: append([]LiveAlertOutcome{}, l.state.Recent...), Assets: assets, ConditionScopes: []LiveAlertConditionScope{}, Checks: []LiveAlertCheck{}}
	for i := range info.Recent {
		info.Recent[i].Message = liveAlertErrorText(info.Recent[i].Message)
		info.Recent[i].Profitability = copyLiveProfitability(info.Recent[i].Profitability)
	}
	states := copyLiveCheckStates(l.state.Checks)
	progressRows := copyLiveAlertProgress(l.checkProgress)
	watermarks := make(map[string]int64, len(l.state.Watermarks))
	for key, value := range l.state.Watermarks {
		watermarks[key] = value
	}
	var retry *LiveAlertsConfig
	if l.retryConfig != nil {
		value := copyLiveAlertsConfig(*l.retryConfig)
		retry = &value
	}
	retryAt, retryAsOf, retryManual, now := l.nextRun, l.retryAsOf, l.retryManual, l.now()
	l.mu.Unlock()
	if savedErr == nil {
		info.Config = migrateLiveAlertEntries(info.Config, saved, watermarks, states)
		presented := materializeLiveAlertConditions(saved, info.Config, true)
		for _, condition := range presented {
			scope := namedLiveAlertScope(liveConditionScopeForConfig(condition, assets, info.Config), condition)
			info.ConditionScopes = append(info.ConditionScopes, scope)
		}
		info.Checks = liveAlertCheckRows(presented, info.ConditionScopes, info.Config, states, now, retry, retryAt, retryAsOf, retryManual)
		applyLiveAlertProgress(info.Checks, progressRows)
	} else if info.Error == "" {
		info.Error = cleanMessage(savedErr.Error())
	}
	needsDownloadedAssets := len(info.Config.ConditionIDs) == 0
	for _, id := range info.Config.ConditionIDs {
		needsDownloadedAssets = needsDownloadedAssets || liveAlertSource(info.Config, id) == "dukascopy"
	}
	if assetErr != nil && needsDownloadedAssets && info.Error == "" {
		info.Error = cleanMessage(assetErr.Error())
	}
	return info
}

func (l *LiveAlerts) selectedConditions(config LiveAlertsConfig) ([]SavedAnalysisCondition, error) {
	return l.selectedConditionsWithProgress(context.Background(), config, false)
}

func (l *LiveAlerts) selectedConditionsForCheck(ctx context.Context, config LiveAlertsConfig) ([]SavedAnalysisCondition, error) {
	return l.selectedConditionsWithProgress(ctx, config, true)
}

func (l *LiveAlerts) selectedConditionsWithProgress(ctx context.Context, config LiveAlertsConfig, report bool) ([]SavedAnalysisCondition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	reportPhase := func(phase string) {
		if !report {
			return
		}
		if id, _ := ctx.Value(liveAlertProgressKey{}).(string); id == "" {
			l.mu.Lock()
			l.resolutionPhase = phase
			// Idle scope refresh also reads the saved rules. Track that work so a
			// new Check now can describe it, but leave future retry rows waiting.
			preparing := l.requested || l.running || l.checkExecuting || (l.state.Config.Enabled && l.nextRun > 0 && l.now().UnixMilli() >= l.nextRun)
			if preparing {
				l.prepareCheckProgressLocked(config.ConditionIDs, phase)
			}
			l.mu.Unlock()
			return
		}
		l.reportCheckPreparation(ctx, config.ConditionIDs, phase)
	}
	if report {
		defer func() {
			if id, _ := ctx.Value(liveAlertProgressKey{}).(string); id == "" {
				l.mu.Lock()
				l.resolutionPhase = ""
				l.mu.Unlock()
			}
		}()
	}
	if !l.manager.mu.TryLock() {
		reportPhase("Waiting for Screener work to load saved conditions")
		if !report {
			l.manager.mu.Lock()
		} else {
			// Background restoration/check cancellation must not wait for an
			// unrelated manager operation before the scheduler can shut down.
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-ticker.C:
					if l.manager.mu.TryLock() {
						goto locked
					}
				}
			}
		}
	}
locked:
	defer l.manager.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	reportPhase("Loading and validating saved conditions")
	saved, err := l.manager.readSavedConditions()
	if err != nil {
		return nil, err
	}
	if err := l.pruneDeletedConditions(saved); err != nil {
		return nil, err
	}
	config = canonicalLiveAlertsConfig(config)
	conditions := materializeLiveAlertConditions(saved, config, false)

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
	if handled, err := l.pruneNamedEntriesLocked(saved); handled {
		return err
	}
	hasDeleted := false
	for _, id := range l.state.Config.ConditionIDs {
		if !known[id] {
			hasDeleted = true
		}
	}
	for _, choice := range l.state.Config.ConditionSources {
		if !known[choice.ConditionID] {
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
	l.state.Config.ConditionSources = nil
	for _, choice := range previousConfig.ConditionSources {
		if known[choice.ConditionID] {
			l.state.Config.ConditionSources = append(l.state.Config.ConditionSources, choice)
		}
	}
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
		l.clearRequestedCheckLocked()
		l.retryTargeted = false
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
	if config.Alerts != nil {
		found := map[string]bool{}
		for _, condition := range conditions {
			found[condition.ID] = true
		}
		for _, entry := range config.Alerts {
			if entry.Enabled && !found[entry.ID] {
				return fmt.Errorf("%s: saved condition was not found", entry.Name)
			}
		}
	}
	var assets []LiveAlertAsset
	for _, saved := range conditions {
		if liveAlertSource(config, saved.ID) == "dukascopy" {
			assets, err = l.assets()
			if err != nil {
				return err
			}
			break
		}
	}
	for _, saved := range conditions {
		scope := liveConditionScopeForConfig(saved, assets, config)
		if scope.Error != "" {
			return errors.New(scope.Error)
		}
	}
	return nil
}

func (l *LiveAlerts) Configure(config LiveAlertsConfig) (LiveAlertsInfo, error) {
	l.mu.Lock()
	submittedRevision := l.configureRevision
	previousSources := copyLiveAlertsConfig(l.state.Config)
	l.mu.Unlock()
	if config.Alerts != nil {
		config.derived = false
	}
	if err := validateLiveAlertsConfig(config); err != nil {
		return l.Info(), err
	}
	config = canonicalLiveAlertsConfig(config)
	config = preserveLiveAlertSourceOverrides(config, previousSources)
	if needsLiveAlertSourceDefaults(config) {
		var err error
		config, err = l.resolveSourceDefaults(config, previousSources)
		if err != nil {
			return l.Info(), err
		}
	}
	if !config.Enabled {
		return l.stopAlerts(config)
	}
	runtimeUnchanged := false
	if config.Alerts != nil {
		l.mu.Lock()
		runtimeUnchanged = sameActiveLiveAlertEntries(config, l.state.Config)
		if !runtimeUnchanged {
			if l.cycleCancel != nil {
				l.cycleCancel()
			}
			l.cancelCheckProgressLocked("Cancelled after configuration changed")
		}
		l.mu.Unlock()
	}
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
		if config.Alerts == nil && len(config.ConditionIDs) == 0 {
			config.Enabled = false
		}
	}
	l.mu.Lock()
	if submittedRevision != l.configureRevision {
		l.mu.Unlock()
		return l.Info(), errors.New("Live Alerts changed while this configuration was being prepared; retry the edit")
	}
	if l.closed || l.blocked || (l.running && config.Enabled && config.Alerts == nil) {
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
	l.configureRevision++
	if runtimeUnchanged {
		// Paused-row metadata cannot interrupt an unchanged active check or
		// move its already scheduled boundary/publication retry.
		l.mu.Unlock()
		l.signal()
		return l.Info(), nil
	}
	l.configRevision++
	l.clearRequestedCheckLocked()
	l.retryTargeted = false
	l.errorText = ""
	if config.Enabled {
		l.scheduleScope = append([]LiveAlertConditionPeriod{}, resolved.scope...)
		l.scheduleCalendar = liveAlertsCalendarIdentity(resolved)
		l.requested, l.status = false, "Waiting for next closed bar"
		l.nextRun = nextLiveAlertsConfigCheck(l.now(), resolved)
		l.nextBoundary = l.nextRun
		l.retryConfig = nil
		if len(config.ConditionIDs) == 0 {
			l.status = "All alerts are disabled"
		}
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
	defer l.mu.Unlock()
	if l.started || l.closed || l.blocked || parent.Err() != nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	l.cancel, l.started = cancel, true
	if l.state.Config.Enabled {
		l.status = "Restoring saved condition schedule"
	}
	l.wg.Add(1)
	go l.loop(ctx)
}

func (l *LiveAlerts) CheckNow() (LiveAlertsInfo, error) {
	return l.requestCheckNow("")
}

// CheckAlertNow queues one named alert without changing the saved selection or
// its automatic schedule. The ID belongs to the alert row, not its reusable rule.
func (l *LiveAlerts) CheckAlertNow(id string) (LiveAlertsInfo, error) {
	if !validLiveAlertID(id) {
		return l.ProgressInfo(), errors.New("choose a valid alert to check")
	}
	return l.requestCheckNow(id)
}

func (l *LiveAlerts) requestCheckNow(id string) (LiveAlertsInfo, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.blocked || !l.state.Config.Enabled || l.running || l.requested || l.checkExecuting || len(l.state.Config.ConditionIDs) == 0 {
		return l.stopInfoLocked(), errors.New("enable Live Alerts and wait for the current check before checking again")
	}
	ids := append([]string{}, l.state.Config.ConditionIDs...)
	if id != "" {
		if entry, exists := liveAlertEntry(l.state.Config, id); l.state.Config.Alerts != nil && (!exists || !entry.Enabled) || !containsLiveValue(ids, id) {
			return l.stopInfoLocked(), errors.New("choose an enabled alert before checking it")
		}
		// A retry owns its queue even while no worker is running. A row action
		// must not replace it or mark another row's queued attempt as skipped.
		if l.retryConfig != nil {
			return l.stopInfoLocked(), errors.New("wait for the pending alert check before checking again")
		}
		for _, progress := range l.checkProgress {
			if progress.state == "queued" || progress.state == "running" {
				return l.stopInfoLocked(), errors.New("wait for the pending alert check before checking again")
			}
		}
		ids = []string{id}
	}
	l.requestedIDs = nil
	if id != "" {
		l.requestedIDs = ids
	}
	l.requestRevision++
	l.requested, l.status = true, "Waiting for scheduler"
	l.requestedAt = l.now()
	l.queueCheckProgressLocked(ids, true)
	phase := l.resolutionPhase
	if phase == "" {
		phase = "Waiting for scheduler"
	}
	l.prepareCheckProgressLocked(ids, phase)
	l.signal()
	return l.stopInfoLocked(), nil
}

// Weekly bars use Monday 00:00 UTC rather than Unix-epoch week alignment.
func supportedLiveAlertPeriod(period string) bool {
	return period == "h1" || period == "d1" || period == "w1"
}

func liveAlertPeriodStart(now time.Time, period string) time.Time {
	utc := now.UTC()
	if period == "w1" {
		day := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
		return day.AddDate(0, 0, -(int(day.Weekday())+6)%7)
	}
	if period == "h1" {
		return utc.Truncate(time.Hour)
	}
	return utc.Truncate(24 * time.Hour)
}

func nextLiveAlertCheck(now time.Time, periods []string) time.Time {
	period, interval := "d1", 24*time.Hour
	if containsLiveValue(periods, "h1") {
		period, interval = "h1", time.Hour
	} else if !containsLiveValue(periods, "d1") && containsLiveValue(periods, "w1") {
		period, interval = "w1", 7*24*time.Hour
	}
	return liveAlertPeriodStart(now, period).Add(interval)
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
		current, revision, requestRevision := copyLiveAlertsConfig(l.state.Config), l.configRevision, l.requestRevision
		preparing := current.Enabled && (l.requested || (l.nextRun > 0 && l.now().UnixMilli() >= l.nextRun))
		var triggeredAt time.Time
		if preparing {
			triggeredAt = l.now()
			if l.requested && !l.requestedAt.IsZero() {
				triggeredAt = l.requestedAt
			}
			ids := current.ConditionIDs
			if l.requested && l.requestedIDs != nil {
				ids = l.requestedIDs
			} else if l.retryConfig != nil && l.retryTargeted {
				ids = l.retryConfig.ConditionIDs
			}
			l.queueCheckProgressLocked(ids, l.requested || l.retryConfig != nil && l.retryManual)
			l.prepareCheckProgressLocked(ids, "Preparing saved conditions")
		}
		l.mu.Unlock()
		var resolved LiveAlertsConfig
		resolved = current
		var resolveErr error
		// A disabled scheduler has no scope to prepare. Enabling or a saved-rule
		// notification wakes this loop; current settings are resolved then.
		if current.Enabled {
			var conditions []SavedAnalysisCondition
			conditions, resolveErr = l.selectedConditionsForCheck(ctx, current)
			if resolveErr == nil {
				resolved = resolvedLiveAlertsConfig(current, conditions)
			}
		}
		now := l.now()
		l.mu.Lock()
		if revision != l.configRevision || requestRevision != l.requestRevision {
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
			if triggeredAt.IsZero() {
				triggeredAt = now
			}
			manual := l.requested
			targeted := manual && l.requestedIDs != nil
			if targeted {
				config = subsetLiveAlertsConfig(config, l.requestedIDs)
			}
			asOf := now.UnixMilli()
			if l.retryConfig != nil && !manual {
				config = copyLiveAlertsConfig(*l.retryConfig)
				asOf, manual = l.retryAsOf, l.retryManual
				targeted = l.retryTargeted
				if !targeted && l.nextBoundary <= now.UnixMilli() {
					due := dueLiveAlertsConfig(resolved, time.UnixMilli(l.nextBoundary), now)
					for _, rule := range due.scope {
						if !liveAlertsScopeAllows(config, rule.ConditionID, rule.Period) {
							config.scope = append(config.scope, rule)
						}
						if !containsLiveValue(config.ConditionIDs, rule.ConditionID) {
							config.ConditionIDs = append(config.ConditionIDs, rule.ConditionID)
						}
					}
					// Retry subsets contain only failed named definitions. Bring
					// newly due peers back from the current resolved definition list.
					scope := config.scope
					config = subsetLiveAlertsConfig(resolved, config.ConditionIDs)
					config.scope = scope
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
			l.running, l.lastRun = true, now.UnixMilli()
			l.clearRequestedCheckLocked()
			l.progress, l.errorText, l.status = 0, "", "Refreshing selected data and indicators"
			// Retain the next original boundary even if this cycle takes a long time.
			if !targeted && l.nextBoundary <= now.UnixMilli() {
				l.nextBoundary = nextLiveAlertsConfigCheck(now, resolved)
			}
			l.nextRun = l.nextBoundary
			l.retryConfig = nil
			l.retryTargeted = false
			cycle, cancel := context.WithCancel(ctx)
			cycle = context.WithValue(cycle, liveAlertTriggerKey{}, triggeredAt)
			executionRevision := l.configRevision
			executionRequestRevision := l.requestRevision
			l.cycleCancel = cancel
			l.queueCheckProgressLocked(config.ConditionIDs, manual)
			l.mu.Unlock()
			err := l.checkCycle(cycle, config, asOf, !manual)
			cancel()
			l.mu.Lock()
			l.running, l.cycleCancel, l.currentAsset = false, nil, ""
			if executionRevision != l.configRevision || executionRequestRevision != l.requestRevision {
				// A newer edit/Stop owns its scheduler and presentation. The
				// cancelled older cycle cannot resurrect its pending retry.
				if !l.state.Config.Enabled {
					l.status, l.nextRun = "Disabled", 0
				} else if len(l.state.Config.ConditionIDs) == 0 {
					l.status, l.nextRun = "All alerts are disabled", 0
				}
			} else if !l.state.Config.Enabled {
				l.status, l.nextRun = "Disabled", 0
			} else if err != nil {
				l.errorText, l.status = checkHistoryMessage(err), "Check stopped"
				if completedLiveAlertScan(err) {
					l.progress, l.status = 100, "Check completed with errors; waiting for next closed bar"
				}
				if retry, eligible := retryLiveAlertConfig(config, err, asOf, l.now(), manual); eligible {
					l.retryConfig, l.retryAsOf, l.retryManual = &retry, asOf, manual
					l.retryTargeted = targeted
					retryAt := l.now().Add(liveAlertRetryDelay).UnixMilli()
					if targeted || l.nextRun <= 0 {
						l.nextRun = retryAt
					} else {
						l.nextRun = min(l.nextRun, retryAt)
					}
					l.status = "Waiting to retry closed-bar check"
					l.markReadRetryProgressLocked(retry, err)
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

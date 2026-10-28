package screener

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Live Alerts deliberately operate on existing enriched histories. They do not
// create a second source, bootstrap a short indicator history, or bypass the
// downloader's shared request pacing and provider cooldown.
type LiveAlertsConfig struct {
	Enabled      bool     `json:"enabled"`
	ConditionIDs []string `json:"conditionIds"`
	Periods      []string `json:"periods"`
	Symbols      []string `json:"symbols"`
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
	ConditionID   string   `json:"conditionId"`
	ConditionName string   `json:"conditionName"`
	Symbol        string   `json:"symbol"`
	AssetName     string   `json:"assetName"`
	Timeframe     string   `json:"timeframe"`
	Time          int64    `json:"time"`
	EntryPrice    float64  `json:"entryPrice"`
	StopLoss      *float64 `json:"stopLoss,omitempty"`
	TakeProfit    *float64 `json:"takeProfit,omitempty"`
}

type LiveAlertOutcome struct {
	ConditionName string `json:"conditionName"`
	Symbol        string `json:"symbol"`
	AssetName     string `json:"assetName"`
	Timeframe     string `json:"timeframe"`
	Time          int64  `json:"time"`
	Status        string `json:"status"`
	Message       string `json:"message"`
}

type LiveAlertsInfo struct {
	Config       LiveAlertsConfig   `json:"config"`
	Running      bool               `json:"running"`
	LastRun      int64              `json:"lastRun"`
	NextRun      int64              `json:"nextRun"`
	Sent         int64              `json:"sent"`
	Error        string             `json:"error"`
	Status       string             `json:"status"`
	Progress     float64            `json:"progress"`
	CurrentAsset string             `json:"currentAsset"`
	Assets       []LiveAlertAsset   `json:"assets"`
	Recent       []LiveAlertOutcome `json:"recent"`
}

type liveAlertsState struct {
	Version    int                `json:"version"`
	Config     LiveAlertsConfig   `json:"config"`
	Watermarks map[string]int64   `json:"watermarks"`
	Sent       int64              `json:"sent"`
	Recent     []LiveAlertOutcome `json:"recent"`
}

const liveAlertsStateBytes = 64 * 1024 * 1024
const liveAlertProviderDelay = 30 * time.Second

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
	lastRun, nextRun                int64
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
	l := &LiveAlerts{manager: manager, path: filepath.Join(manager.profile, "screener-live-alerts.json"), send: send, wake: make(chan struct{}, 1), status: "Disabled", state: liveAlertsState{Version: 1, Config: LiveAlertsConfig{Periods: []string{"d1"}, Symbols: []string{}, ConditionIDs: []string{}}, Watermarks: map[string]int64{}, Recent: []LiveAlertOutcome{}}}
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
	// A claim committed before interruption may have reached Telegram. Keeping
	// its watermark and showing uncertainty avoids duplicate notifications.
	for i := range l.state.Recent {
		if l.state.Recent[i].Status == "claimed" {
			l.state.Recent[i].Status, l.state.Recent[i].Message = "uncertain", "Delivery was interrupted; check Telegram. This bar will not be sent again automatically."
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
	config.Periods = append([]string{}, config.Periods...)
	config.Symbols = append([]string{}, config.Symbols...)
	return config
}

func validateLiveAlertsConfig(config LiveAlertsConfig) error {
	if len(config.ConditionIDs) > savedConditionsLimit || len(config.Symbols) > len(catalogAssets) || len(config.Periods) > 2 {
		return errors.New("too many selected Live Alerts conditions, symbols or periods")
	}
	if config.Enabled && (len(config.ConditionIDs) == 0 || len(config.Symbols) == 0 || len(config.Periods) == 0) {
		return errors.New("select at least one saved condition, period and downloaded asset before enabling Live Alerts")
	}
	for kind, values := range map[string][]string{"condition": config.ConditionIDs, "symbol": config.Symbols, "period": config.Periods} {
		seen := map[string]bool{}
		for _, value := range values {
			if value == "" || seen[value] {
				return fmt.Errorf("invalid or duplicate Live Alerts %s", kind)
			}
			seen[value] = true
			switch kind {
			case "period":
				if value != "d1" && value != "h1" {
					return errors.New("Live Alerts periods must be d1 or h1")
				}
			case "symbol":
				if _, ok := catalogAssets[value]; !ok {
					return errors.New("Live Alerts requires a supported downloaded asset")
				}
			case "condition":
				if len(value) != 32 || strings.IndexFunc(value, func(r rune) bool { return !strings.ContainsRune("0123456789abcdef", r) }) >= 0 {
					return errors.New("invalid saved Live Alerts condition ID")
				}
			}
		}
	}
	return nil
}

func validateLiveAlertsState(state liveAlertsState) error {
	if state.Version != 1 || state.Sent < 0 || len(state.Recent) > 50 || len(state.Watermarks) > savedConditionsLimit*len(catalogAssets)*2 {
		return errors.New("invalid Live Alerts state version or count")
	}
	if err := validateLiveAlertsConfig(state.Config); err != nil {
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
	for _, outcome := range state.Recent {
		if outcome.Time < 0 || len(outcome.Message) > 5000 || (outcome.Status != "sent" && outcome.Status != "failed" && outcome.Status != "claimed" && outcome.Status != "uncertain") {
			return errors.New("invalid Live Alerts delivery history")
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
	l.mu.Lock()
	info := LiveAlertsInfo{Config: copyLiveAlertsConfig(l.state.Config), Running: l.running, LastRun: l.lastRun, NextRun: l.nextRun, Sent: l.state.Sent, Error: l.errorText, Status: l.status, Progress: l.progress, CurrentAsset: l.currentAsset, Recent: append([]LiveAlertOutcome{}, l.state.Recent...), Assets: []LiveAlertAsset{}}
	l.mu.Unlock()
	assets, err := l.assets()
	info.Assets = assets
	if err != nil && info.Error == "" {
		info.Error = cleanMessage(err.Error())
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
			return nil, errors.New("a selected saved condition no longer exists; update the Live Alerts selection")
		}
	}
	if err := l.pruneDeletedConditionWatermarks(saved); err != nil {
		return nil, err
	}
	return conditions, nil
}

// Saved conditions have a bounded count, so their deleted identities cannot
// accumulate an unbounded history of processed bars. Retain all still-saved
// conditions, including inactive selections, to prevent re-selection repeats.
func (l *LiveAlerts) pruneDeletedConditionWatermarks(saved []SavedAnalysisCondition) error {
	known := make(map[string]bool, len(saved))
	for _, condition := range saved {
		known[condition.ID] = true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	hasDeleted := false
	for key := range l.state.Watermarks {
		id, _, _ := strings.Cut(key, "/")
		if !known[id] {
			hasDeleted = true
			break
		}
	}
	if !hasDeleted {
		return nil
	}
	if l.closed || l.blocked {
		return errors.New("Live Alerts are closed or their saved state is invalid; original processed-bar state preserved")
	}
	retained := make(map[string]int64, len(l.state.Watermarks))
	for key, timestamp := range l.state.Watermarks {
		id, _, _ := strings.Cut(key, "/")
		if known[id] {
			retained[key] = timestamp
		}
	}
	previous := l.state.Watermarks
	l.state.Watermarks = retained
	if err := writeState(l.path, l.state); err != nil {
		l.state.Watermarks = previous
		return fmt.Errorf("remove deleted Live Alerts condition history: %w", err)
	}
	return nil
}

func (l *LiveAlerts) validateSelection(config LiveAlertsConfig) error {
	if _, err := l.selectedConditions(config); err != nil {
		return err
	}
	assets, err := l.assets()
	if err != nil {
		return err
	}
	for _, symbol := range config.Symbols {
		found := false
		for _, asset := range assets {
			if asset.Symbol != symbol {
				continue
			}
			for _, period := range config.Periods {
				found = found || containsLiveValue(asset.Periods, period)
			}
		}
		if !found {
			return fmt.Errorf("download and enrich %s in a selected period before selecting it for Live Alerts", strings.ToUpper(symbol))
		}
	}
	return nil
}

func (l *LiveAlerts) Configure(config LiveAlertsConfig) (LiveAlertsInfo, error) {
	config = copyLiveAlertsConfig(config)
	if err := validateLiveAlertsConfig(config); err != nil {
		return l.Info(), err
	}
	if config.Enabled {
		if err := l.validateSelection(config); err != nil {
			return l.Info(), err
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
	l.errorText = ""
	if config.Enabled {
		l.requested, l.status = true, "Check queued"
	} else {
		l.requested, l.nextRun, l.status = false, 0, "Disabled"
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
	if l.started || l.closed || l.blocked {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	l.cancel, l.started, l.requested = cancel, true, l.state.Config.Enabled
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
	next := now.Truncate(interval).Add(liveAlertProviderDelay)
	if !next.After(now) {
		next = next.Add(interval)
	}
	return next
}

func (l *LiveAlerts) loop(ctx context.Context) {
	defer l.wg.Done()
	for {
		if ctx.Err() != nil {
			return
		}
		now := time.Now()
		l.mu.Lock()
		enabled := l.state.Config.Enabled
		run := enabled && (l.requested || (l.nextRun > 0 && now.UnixMilli() >= l.nextRun))
		config := copyLiveAlertsConfig(l.state.Config)
		if run {
			l.running, l.requested, l.lastRun = true, false, now.UnixMilli()
			l.progress, l.errorText, l.status = 0, "", "Refreshing selected data and indicators"
			l.nextRun = nextLiveAlertCheck(now, config.Periods).UnixMilli()
			cycle, cancel := context.WithCancel(ctx)
			l.cycleCancel = cancel
			l.mu.Unlock()
			err := l.check(cycle, config, now.UnixMilli())
			cancel()
			l.mu.Lock()
			l.running, l.cycleCancel, l.currentAsset = false, nil, ""
			if !l.state.Config.Enabled {
				l.status, l.nextRun = "Disabled", 0
			} else if err != nil {
				l.errorText, l.status = cleanMessage(err.Error()), "Check stopped"
				if errors.Is(err, errLiveAlertsBusy) {
					l.nextRun = time.Now().Add(liveAlertProviderDelay).UnixMilli()
				}
			} else {
				l.progress, l.status = 100, "Waiting for next closed bar"
			}
		}
		wait := time.Hour
		if l.state.Config.Enabled && l.nextRun > 0 {
			wait = max(time.Millisecond, time.Until(time.UnixMilli(l.nextRun)))
		}
		l.mu.Unlock()
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-l.wake:
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

// Package comparison compares genuine closed daily prices using a shared UTC
// baseline. It owns no provider connections, subscriptions or trading actions.
package comparison

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"

	"cryptodesk/internal/domain"
	"cryptodesk/internal/market"
)

const day = int64(86400)
const MaxWeeks, MaxAssets = 104, 20

type View struct {
	Weeks  int      `json:"weeks"`
	Assets []string `json:"assets"`
}
type Asset struct {
	ID            string          `json:"id,omitempty"`
	Symbol        string          `json:"symbol"`
	DisplaySymbol string          `json:"displaySymbol,omitempty"`
	Name          string          `json:"name"`
	Exchange      string          `json:"exchange"`
	Market        string          `json:"market"`
	NativeID      string          `json:"nativeId"`
	QuoteAsset    string          `json:"quoteAsset"`
	FeedKey       *domain.FeedKey `json:"feedKey,omitempty"`
}
type Catalog struct {
	Assets    []Asset  `json:"assets"`
	Markets   []Asset  `json:"markets"`
	Warnings  []string `json:"warnings"`
	AutoError string   `json:"autoError,omitempty"`
}
type Point struct {
	Time  int64    `json:"time"`
	Close *float64 `json:"close"`
	Pct   *float64 `json:"pct"`
}
type Series struct {
	Asset  Asset   `json:"asset"`
	Points []Point `json:"points"`
	Error  string  `json:"error"`
}
type Result struct {
	RequestID      string   `json:"requestId"`
	Weeks          int      `json:"weeks"`
	BaselineTime   int64    `json:"baselineTime"`
	LastClosedTime int64    `json:"lastClosedTime"`
	AsOf           int64    `json:"asOf"`
	Series         []Series `json:"series"`
	Warnings       []string `json:"warnings"`
}

// Source is implemented by the application's existing shared market service.
type Source interface {
	Instruments(context.Context, string) ([]domain.Instrument, error)
	History(context.Context, string, domain.FeedKey, int64, int64, int) (market.History, error)
}
type Options struct {
	Profile string
	Source  Source
	Now     func() time.Time
}
type pageKey struct {
	Feed     domain.FeedKey
	From, To int64
}
type page struct {
	Bars []domain.Candle
	At   time.Time
}
type request struct{ cancel context.CancelFunc }
type Manager struct {
	mu          sync.Mutex
	opts        Options
	ctx         context.Context
	cancel      context.CancelFunc
	closed      bool
	wg          sync.WaitGroup
	requests    map[string]*request
	catalogGate chan struct{}
	catalog     Catalog
	catalogAt   time.Time
	pages       map[pageKey]page
}

func New(profile string, source Source) *Manager {
	return NewWithOptions(Options{Profile: profile, Source: source})
}
func NewWithOptions(opts Options) *Manager {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{opts: opts, ctx: ctx, cancel: cancel, requests: map[string]*request{}, catalogGate: make(chan struct{}, 1), pages: map[pageKey]page{}}
}
func DefaultView() View {
	return View{Weeks: 4, Assets: []string{"BTC", "ETH", "TON", "HYPE", "ZEN", "APT", "NEAR"}}
}

var symbolPattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9._-]{0,31}$`)

func normalizeView(value View) (View, error) {
	if value.Weeks < 1 || value.Weeks > MaxWeeks || len(value.Assets) > MaxAssets {
		return View{}, fmt.Errorf("comparison requires 1–%d weeks and at most %d assets", MaxWeeks, MaxAssets)
	}
	result := View{Weeks: value.Weeks, Assets: make([]string, 0, len(value.Assets))}
	seen := map[string]bool{}
	for _, symbol := range value.Assets {
		symbol = strings.TrimSpace(symbol)
		if strings.Contains(symbol, "|") {
			if !validMarketID(symbol) {
				return View{}, errors.New("comparison source selection is invalid")
			}
		} else {
			symbol = strings.ToUpper(symbol)
			if !symbolPattern.MatchString(symbol) {
				return View{}, errors.New("comparison assets must be base symbols or explicit market selections")
			}
		}
		if seen[symbol] {
			return View{}, errors.New("comparison selections must be unique")
		}
		seen[symbol] = true
		result.Assets = append(result.Assets, symbol)
	}
	return result, nil
}
func (m *Manager) operation(parent context.Context) (context.Context, context.CancelFunc, error) {
	if parent == nil {
		parent = context.Background()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, nil, errors.New("comparison is closed")
	}
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	stop := context.AfterFunc(m.ctx, cancel)
	m.wg.Add(1)
	return ctx, func() { stop(); cancel(); m.wg.Done() }, nil
}

// TON was renamed to GRAM at 1:1 in July 2026. Preserve saved TON selections,
// but disclose the actual ticker and keep requests on the provider's GRAM feed.
// Verified identity: https://ton.org/ and Binance's June 12, 2026 announcement:
// https://www.binance.com/en/support/announcement/detail/fe307fba935b44698fde4db01e84a7eb
// This deliberately excludes wrapped tokens and unverified similarly named
// spot or builder markets; it does not stitch the retired TON price history.
func renamedToncoin(item domain.Instrument, provider string, now time.Time) bool {
	if strings.ToUpper(item.BaseAsset) != "GRAM" {
		return false
	}
	// Bybit explicitly renamed TON to GRAM 1:1 on June 22, 2026. The
	// provider owns any earlier observations returned under that native symbol.
	// https://announcements.bybitglobal.com/en/article/bybit-to-support-toncoin-ton-rebrand-and-ticker-change-to-gram-gram--blt617f4bf48950ab77/
	if provider == "bybit" {
		return !now.Before(time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC)) && (item.Market == "spot" || item.Market == "perpetual") && item.NativeID == "GRAMUSDT" && item.QuoteAsset == "USDT"
	}
	if now.Before(time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)) {
		return false
	}
	return provider == "hyperliquid" && item.Market == "perpetual" && item.NativeID == "GRAM" ||
		provider == "binance" && item.Market == "spot" && item.NativeID == "GRAMUSDT" && item.QuoteAsset == "USDT"
}
func supportsDaily(item domain.Instrument) bool {
	for _, period := range item.SupportedIntervals {
		if period == "1d" {
			return true
		}
	}
	return false
}
func available(value string) bool {
	switch strings.ToLower(value) {
	case "available", "trading", "online", "active":
		return true
	}
	return false
}
func assetOrder(asset Asset) string {
	rank := "9"
	switch asset.QuoteAsset {
	case "USDC":
		rank = "0"
	case "USDT":
		rank = "1"
	case "USDH":
		rank = "2"
	case "USD":
		rank = "3"
	}
	return rank + asset.QuoteAsset + "/" + asset.NativeID
}
func usdQuote(quote string) bool {
	switch quote {
	case "USD", "USDC", "USDT", "USDH":
		return true
	}
	return false
}
func assetName(symbol, name string) string {
	if name != "" {
		return name
	}
	if name := map[string]string{"BTC": "Bitcoin", "ETH": "Ethereum", "TON": "Toncoin", "HYPE": "Hyperliquid", "ZEN": "Horizen", "APT": "Aptos", "NEAR": "NEAR"}[symbol]; name != "" {
		return name
	}
	return symbol
}

func (m *Manager) DailyChanges(parent context.Context, id string, value View) (Result, error) {
	view, err := normalizeView(value)
	if err != nil {
		return Result{}, err
	}
	if len(id) == 0 || len(id) > 96 {
		return Result{}, errors.New("comparison request identity is required and must be at most 96 bytes")
	}
	ctx, done, err := m.operation(parent)
	if err != nil {
		return Result{}, err
	}
	defer done()
	ctx, cancel := context.WithCancel(ctx)
	current := &request{cancel: cancel}
	m.mu.Lock()
	for _, old := range m.requests {
		old.cancel()
	}
	m.requests = map[string]*request{id: current}
	m.mu.Unlock()
	defer func() {
		cancel()
		m.mu.Lock()
		if m.requests[id] == current {
			delete(m.requests, id)
		}
		m.mu.Unlock()
	}()
	now := m.opts.Now().UTC().Unix()
	last := now - now%day - day
	from := last - int64(view.Weeks*7)*day
	result := Result{RequestID: id, Weeks: view.Weeks, BaselineTime: from, LastClosedTime: last, AsOf: now, Series: make([]Series, len(view.Assets)), Warnings: []string{}}
	if len(view.Assets) == 0 {
		return result, ctx.Err()
	}
	catalog, err := m.loadCatalog(ctx)
	if err != nil {
		return Result{}, err
	}
	result.Warnings = catalog.Warnings
	assets := map[string]Asset{}
	for _, asset := range catalog.Assets {
		assets[asset.ID] = asset
	}
	for _, asset := range catalog.Markets {
		assets[asset.ID] = asset
	}
	var workers sync.WaitGroup
	slots := make(chan struct{}, 4)
	selectedFeeds := map[domain.FeedKey]bool{}
	for i, selection := range view.Assets {
		asset, found := assets[selection]
		if !found {
			message := "No matching market is available in the verified provider catalog; choose another source"
			if !strings.Contains(selection, "|") && catalog.AutoError != "" {
				message = catalog.AutoError
			}
			result.Series[i] = Series{Asset: unavailableSelection(selection), Points: []Point{}, Error: message}
			continue
		}
		if selectedFeeds[*asset.FeedKey] {
			result.Series[i] = Series{Asset: asset, Points: []Point{}, Error: "This source and market is already selected; remove the duplicate"}
			continue
		}
		selectedFeeds[*asset.FeedKey] = true
		workers.Add(1)
		go func(index int, asset Asset) {
			defer workers.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-slots }()
			result.Series[index] = m.series(ctx, fmt.Sprintf("%s/%d", id, index), asset, from, last)
		}(i, asset)
	}
	workers.Wait()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	return result, nil
}
func (m *Manager) series(ctx context.Context, id string, asset Asset, from, last int64) Series {
	result := Series{Asset: asset, Points: []Point{}}
	key := pageKey{Feed: *asset.FeedKey, From: from, To: last + day}
	count := int((last-from)/day + 1)
	m.mu.Lock()
	cached, found := m.pages[key]
	m.mu.Unlock()
	var bars []domain.Candle
	reused := found && m.opts.Now().Sub(cached.At) < time.Minute
	if reused {
		bars = append([]domain.Candle(nil), cached.Bars...)
	} else {
		history, err := m.opts.Source.History(ctx, id, key.Feed, from, last+day, count)
		if err != nil {
			result.Error = err.Error()
			return result
		}
		bars = history.Bars
	}
	if err := ctx.Err(); err != nil {
		result.Error = err.Error()
		return result
	}
	byTime := map[int64]domain.Candle{}
	for _, bar := range bars {
		if bar.Time < from || bar.Time > last {
			continue
		}
		if bar.Time%day != 0 || bar.Validate() != nil {
			result.Error = "Provider returned an invalid daily candle"
			return result
		}
		if _, exists := byTime[bar.Time]; exists {
			result.Error = "Provider returned duplicate daily candles"
			return result
		}
		if !bar.Closed || bar.Quality != "authoritative" {
			continue
		}
		byTime[bar.Time] = bar
	}
	baseline, ok := byTime[from]
	if !ok {
		date := time.Unix(from, 0).UTC().Format("2006-01-02")
		var earliest int64
		for at := range byTime {
			if earliest == 0 || at < earliest {
				earliest = at
			}
		}
		result.Error = "The common UTC baseline candle on " + date + " is unavailable; this asset was not rebased"
		if earliest > 0 {
			result.Error += "; first available daily close in this requested range: " + time.Unix(earliest, 0).UTC().Format("2006-01-02") + " UTC"
		} else {
			result.Error += "; no completed daily closes were returned in this requested range"
		}
		result.Error += ". Choose another source or a shorter period"
		return result
	}
	complete := true
	for ts := from; ts <= last; ts += day {
		point := Point{Time: ts}
		if bar, ok := byTime[ts]; ok {
			price, pct := bar.Close, (bar.Close/baseline.Close-1)*100
			if math.IsNaN(pct) || math.IsInf(pct, 0) {
				result.Points = []Point{}
				result.Error = "Daily percentage change exceeds its numeric bounds"
				return result
			}
			point.Close, point.Pct = &price, &pct
		} else {
			complete = false
		}
		result.Points = append(result.Points, point)
	}
	if complete && !reused {
		m.mu.Lock()
		if ctx.Err() == nil && !m.closed {
			if len(m.pages) >= 40 {
				var oldest pageKey
				var oldestAt time.Time
				for k, v := range m.pages {
					if oldestAt.IsZero() || v.At.Before(oldestAt) {
						oldest, oldestAt = k, v.At
					}
				}
				delete(m.pages, oldest)
			}
			m.pages[key] = page{Bars: append([]domain.Candle(nil), bars...), At: m.opts.Now()}
		}
		m.mu.Unlock()
	}
	return result
}
func (m *Manager) Cancel(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if current := m.requests[id]; current != nil {
		current.cancel()
	}
}
func (m *Manager) Close() {
	m.mu.Lock()
	if !m.closed {
		m.closed = true
		m.cancel()
		for _, r := range m.requests {
			r.cancel()
		}
	}
	m.mu.Unlock()
	m.wg.Wait()
}

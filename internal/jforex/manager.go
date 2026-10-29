package jforex

import (
	"context"
	"crypto/sha256"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
)

var _ exchange.Adapter = (*Manager)(nil)

type Manager struct {
	mu              sync.Mutex
	ctx             context.Context
	cancel          context.CancelFunc
	path            string
	cacheDirectory  string
	config          configuration
	loadError       error
	runtime         bridgeRuntime
	factory         commandFactory
	prepare         func(context.Context) error
	process         *bridgeProcess
	authenticated   bool
	online          bool
	instrumentCount int
	lastError       string
	closed          bool
	operations      int
	gate            chan struct{}
	idle            *time.Timer
	holdUntil       time.Time
	pinSession      bool
	wg              sync.WaitGroup
	processes       sync.WaitGroup
	next            atomic.Uint64
	testCancel      context.CancelFunc
	testGeneration  uint64
}

// New reads only machine-local configuration and discovers files. Login is
// lazy, and never occurs when CryptoDesk starts or the Settings page opens.
func New(profile string) *Manager {
	runtime := discoverRuntime()
	runtime.privateDirectory, _ = filepath.Abs(filepath.Join(profile, "jforex-private"))
	runtime.sdkDirectory, _ = filepath.Abs(filepath.Join(profile, "jforex-runtime", "3.6.51"))
	manager := newManager(profile, runtime, runtime.command)
	manager.prepare = runtime.prepare
	return manager
}

func newManager(profile string, runtime bridgeRuntime, factory commandFactory) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	path := filepath.Join(profile, "dukascopy-jforex.json")
	config, err := readConfiguration(path)
	cache, _ := filepath.Abs(filepath.Join(profile, "jforex-cache"))
	return &Manager{ctx: ctx, cancel: cancel, path: path, cacheDirectory: cache, config: config, loadError: err, runtime: runtime, factory: factory, gate: make(chan struct{}, 1)}
}

func (m *Manager) infoLocked() Info {
	connected := m.authenticated && m.online && m.process != nil
	if connected {
		select {
		case <-m.process.done:
			connected = false
		default:
		}
	}
	info := Info{Username: m.config.Username, Environment: m.config.Environment, PasswordConfigured: m.config.Password != "", RuntimeAvailable: m.runtime.err == nil, RuntimeDirectory: m.runtime.directory, JavaPath: m.runtime.java, Connected: connected, InstrumentCount: m.instrumentCount, Error: m.lastError}
	if m.loadError != nil {
		info.Error = m.loadError.Error()
	} else if m.runtime.err != nil {
		info.Error = m.runtime.err.Error()
	}
	return info
}

func (m *Manager) Info() Info { m.mu.Lock(); defer m.mu.Unlock(); return m.infoLocked() }

func (m *Manager) Configure(request Settings) (Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return m.infoLocked(), errors.New("The Dukascopy service is closed")
	}
	if m.loadError != nil {
		return m.infoLocked(), m.loadError
	}
	config, err := normalizeSettings(request, m.config)
	if err != nil {
		return m.infoLocked(), err
	}
	if config == m.config {
		return m.infoLocked(), nil
	}
	// A chart can fail its initial unconfigured lookup while Settings is being
	// saved. No bridge can exist for incomplete credentials, so that harmless
	// in-flight check must not prevent supplying the first valid configuration.
	initialConfiguration := m.process == nil && (m.config.Username == "" || m.config.Password == "")
	if m.operations > 0 && !initialConfiguration {
		return m.infoLocked(), errors.New("Close the active Dukascopy charts and finish connection requests before changing these credentials")
	}
	if err = saveConfiguration(m.path, config); err != nil {
		return m.infoLocked(), err
	}
	if m.idle != nil {
		m.idle.Stop()
		m.idle = nil
	}
	if m.process != nil {
		m.process.finish(errors.New("Dukascopy credentials changed"))
		m.process = nil
	}
	m.config = config
	m.authenticated = false
	m.online = false
	m.instrumentCount = 0
	m.lastError = ""
	m.holdUntil = time.Time{}
	m.pinSession = false
	return m.infoLocked(), nil
}

func (m *Manager) begin(ctx context.Context, login bool, pin string) (*bridgeProcess, func(), error) {
	return m.beginWithForce(ctx, login, pin, false)
}

func (m *Manager) beginWithForce(ctx context.Context, login bool, pin string, force bool) (*bridgeProcess, func(), error) {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(m.ctx, cancel)
	defer stop()
	defer cancel()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, nil, errors.New("The Dukascopy service is closed")
	}
	if m.loadError != nil {
		err := m.loadError
		m.mu.Unlock()
		return nil, nil, err
	}
	if m.idle != nil {
		m.idle.Stop()
		m.idle = nil
	}
	m.operations++
	m.wg.Add(1)
	m.mu.Unlock()
	release := func() { m.mu.Lock(); m.operations--; m.scheduleIdleLocked(); m.mu.Unlock(); m.wg.Done() }
	p, err := m.ensure(ctx, login, pin, force)
	if err != nil {
		m.recordError(err)
		release()
		return nil, nil, err
	}
	return p, release, nil
}

func (m *Manager) scheduleIdleLocked() {
	if m.closed || m.operations != 0 || m.process == nil || m.pinSession {
		return
	}
	delay := 2 * time.Second
	if remaining := time.Until(m.holdUntil); remaining > delay {
		delay = remaining
	}
	p := m.process
	m.idle = time.AfterFunc(delay, func() {
		m.mu.Lock()
		if m.closed || m.operations != 0 || m.process != p {
			m.mu.Unlock()
			return
		}
		m.process = nil
		m.authenticated = false
		m.online = false
		m.pinSession = false
		m.idle = nil
		m.mu.Unlock()
		p.stop()
	})
}

func (m *Manager) ensure(ctx context.Context, login bool, pin string, force bool) (*bridgeProcess, error) {
	select {
	case m.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-m.ctx.Done():
		return nil, errors.New("The Dukascopy service is closed")
	}
	defer func() { <-m.gate }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, errors.New("The Dukascopy service is closed")
	}
	config := m.config
	if config.Username == "" || config.Password == "" {
		m.mu.Unlock()
		return nil, &exchange.Error{Kind: "restricted", Message: "Configure a Dukascopy JForex username and password in Settings → Ducas Copy before opening a forex chart"}
	}
	p := m.process
	if p != nil {
		select {
		case <-p.done:
			p = nil
			m.process = nil
			m.authenticated = false
		default:
		}
	}
	authenticated := m.authenticated
	m.mu.Unlock()
	if p == nil {
		if m.prepare != nil {
			if err := m.prepare(ctx); err != nil {
				return nil, err
			}
		}
		if err := os.MkdirAll(m.cachePath(config), 0700); err != nil {
			return nil, errors.New("Could not create the private JForex cache folder")
		}
		var err error
		p, err = startBridge(m.ctx, m.factory, config.Username, config.Password)
		if err != nil {
			return nil, err
		}
		m.processes.Add(1)
		go func() { <-p.exited; m.processes.Done() }()
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			p.stop()
			return nil, errors.New("The Dukascopy service is closed")
		}
		m.process = p
		m.authenticated = false
		m.online = false
		m.pinSession = false
		m.mu.Unlock()
	}
	if !login || authenticated && !force {
		return p, nil
	}
	m.mu.Lock()
	m.online = false
	m.mu.Unlock()
	request := map[string]any{"op": "connect", "username": config.Username, "password": config.Password, "environment": config.Environment, "cacheDirectory": m.cachePath(config)}
	if pin != "" {
		request["pin"] = pin
		p.addSecret(pin)
		defer p.removeSecret(pin)
	}
	var result struct {
		Connected       bool `json:"connected"`
		InstrumentCount int  `json:"instrumentCount"`
	}
	if err := p.call(ctx, request, &result); err != nil {
		// A failed login may retain the SDK's CAPTCHA challenge for an explicit
		// retry with PIN, but never loops or retries credentials automatically.
		if ctx.Err() != nil && !authenticated {
			p.finish(errors.New("The JForex login was canceled before completion"))
		}
		m.mu.Lock()
		m.holdUntil = time.Now().Add(2 * time.Minute)
		m.mu.Unlock()
		return nil, err
	}
	if !result.Connected || result.InstrumentCount < 0 || result.InstrumentCount > 10000 {
		return nil, errors.New("The JForex bridge did not confirm its connection")
	}
	m.mu.Lock()
	m.authenticated = true
	m.online = true
	m.instrumentCount = result.InstrumentCount
	m.lastError = ""
	m.holdUntil = time.Time{}
	if pin != "" {
		m.pinSession = true
	}
	m.mu.Unlock()
	return p, nil
}

func (m *Manager) recordError(err error) {
	if err == nil {
		return
	}
	m.mu.Lock()
	m.lastError = err.Error()
	m.mu.Unlock()
}

func (m *Manager) cachePath(config configuration) string {
	hash := sha256.Sum256([]byte(config.Username))
	return filepath.Join(m.cacheDirectory, config.Environment, hex.EncodeToString(hash[:16]))
}

func (m *Manager) TestConnection(ctx context.Context) (Info, error) {
	return m.TestConnectionWithPIN(ctx, "")
}

func (m *Manager) TestConnectionWithPIN(ctx context.Context, pin string) (Info, error) {
	if len(pin) > 128 || strings.ContainsFunc(pin, unicode.IsControl) {
		return m.Info(), errors.New("Enter a valid transient Dukascopy security code")
	}
	ctx, finish, err := m.beginConnectionTest(ctx)
	if err != nil {
		return m.Info(), err
	}
	defer finish()
	_, release, err := m.beginWithForce(ctx, true, pin, true)
	if err != nil {
		return m.Info(), err
	}
	defer release()
	return m.Info(), nil
}

func (m *Manager) Challenge(ctx context.Context) (LoginChallenge, error) {
	ctx, finish, err := m.beginConnectionTest(ctx)
	if err != nil {
		return LoginChallenge{}, err
	}
	defer finish()
	p, release, err := m.begin(ctx, false, "")
	if err != nil {
		return LoginChallenge{}, err
	}
	defer release()
	var result LoginChallenge
	m.mu.Lock()
	cacheDirectory := m.cachePath(m.config)
	m.mu.Unlock()
	if err = p.call(ctx, map[string]any{"op": "captcha", "cacheDirectory": cacheDirectory}, &result); err != nil {
		if ctx.Err() != nil {
			m.mu.Lock()
			if !m.authenticated && m.operations <= 1 {
				p.finish(errors.New("The JForex security image request was canceled"))
			}
			m.mu.Unlock()
		}
		m.recordError(err)
		return LoginChallenge{}, err
	}
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(result.ImageDataURL, prefix) || len(result.ImageDataURL) > 2*1024*1024 {
		return LoginChallenge{}, errors.New("The JForex bridge returned an invalid security image")
	}
	image, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(result.ImageDataURL, prefix))
	if err != nil || len(image) < 8 || string(image[:8]) != "\x89PNG\r\n\x1a\n" {
		return LoginChallenge{}, errors.New("The JForex bridge returned an invalid security image")
	}
	m.mu.Lock()
	m.holdUntil = time.Now().Add(2 * time.Minute)
	m.authenticated = false
	m.online = false
	m.pinSession = false
	m.lastError = ""
	m.mu.Unlock()
	return result, nil
}

func (m *Manager) beginConnectionTest(ctx context.Context) (context.Context, func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, nil, errors.New("The Dukascopy service is closed")
	}
	if m.testCancel != nil {
		return nil, nil, errors.New("A Dukascopy connection request is already running")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	m.testGeneration++
	generation := m.testGeneration
	m.testCancel = cancel
	return ctx, func() {
		cancel()
		m.mu.Lock()
		if m.testGeneration == generation {
			m.testCancel = nil
		}
		m.mu.Unlock()
	}, nil
}

// CancelConnectionTest does not interrupt existing chart subscriptions.
func (m *Manager) CancelConnectionTest() Info {
	m.mu.Lock()
	cancel := m.testCancel
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return m.Info()
}

func (m *Manager) Instruments(ctx context.Context) ([]domain.Instrument, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	p, release, err := m.begin(ctx, true, "")
	if err != nil {
		return nil, err
	}
	defer release()
	rpc, cancelRPC := context.WithTimeout(ctx, 60*time.Second)
	defer cancelRPC()
	var instruments []domain.Instrument
	if err = p.call(rpc, map[string]any{"op": "instruments"}, &instruments); err != nil {
		m.recordError(err)
		return nil, err
	}
	if len(instruments) > 10000 {
		return nil, errors.New("The JForex instrument catalog exceeds its limit")
	}
	seen := map[string]bool{}
	for _, instrument := range instruments {
		key := domain.FeedKey{Exchange: "dukascopy", Market: "forex", Instrument: instrument.NativeID, Timeframe: "1m"}
		if key.Validate() != nil || instrument.Market != "forex" || seen[instrument.NativeID] || instrument.BaseAsset == "" || instrument.QuoteAsset == "" {
			return nil, errors.New("The JForex bridge returned an invalid forex instrument")
		}
		seen[instrument.NativeID] = true
	}
	sort.Slice(instruments, func(i, j int) bool { return instruments[i].NativeID < instruments[j].NativeID })
	m.mu.Lock()
	m.instrumentCount = len(instruments)
	m.lastError = ""
	m.mu.Unlock()
	return instruments, nil
}

func validateKey(key domain.FeedKey) error {
	if err := key.Validate(); err != nil {
		return &exchange.Error{Kind: "unsupportedInterval", Message: err.Error()}
	}
	if key.Exchange != "dukascopy" || key.Market != "forex" {
		return &exchange.Error{Kind: "unknownInstrument", Message: "The JForex adapter supplies Dukascopy forex quotes only"}
	}
	return nil
}

func normalizeBars(key domain.FeedKey, bars []domain.Candle) ([]domain.Candle, error) {
	if len(bars) > 10000 {
		return nil, errors.New("The JForex candle response exceeds its limit")
	}
	for _, bar := range bars {
		if bar.SourceRevision < 0 || len(bar.Quality) > 64 || math.IsNaN(bar.Volume) || math.IsInf(bar.Volume, 0) {
			return nil, errors.New("The JForex bridge returned invalid candle metadata")
		}
	}
	return exchange.Normalize(bars, exchange.TimeframeSeconds(key.Timeframe))
}

func (m *Manager) History(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	if from < 0 || to <= from || to > 32503680000 || limit < 1 || limit > 1000 {
		return nil, errors.New("Invalid bounded JForex history range")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	p, release, err := m.begin(ctx, true, "")
	if err != nil {
		return nil, err
	}
	defer release()
	rpc, cancelRPC := context.WithTimeout(ctx, 60*time.Second)
	defer cancelRPC()
	var bars []domain.Candle
	if err = p.call(rpc, map[string]any{"op": "history", "feedKey": key, "from": from, "to": to, "limit": limit}, &bars); err != nil {
		m.recordError(err)
		return nil, err
	}
	bars, err = normalizeBars(key, bars)
	if err != nil {
		return nil, err
	}
	result := make([]domain.Candle, 0, min(limit, len(bars)))
	for _, bar := range bars {
		if bar.Time >= from && bar.Time < to {
			result = append(result, bar)
		}
	}
	if len(result) > limit {
		result = result[len(result)-limit:]
	}
	return result, nil
}

func (m *Manager) Stream(ctx context.Context, key domain.FeedKey, emit func([]domain.Candle, domain.Health)) error {
	if err := validateKey(key); err != nil {
		return err
	}
	emit(nil, domain.Health{State: "connecting", Reason: "Connecting to authenticated Dukascopy forex Bid quotes"})
	setup, cancel := context.WithTimeout(ctx, 5*time.Minute)
	p, release, err := m.begin(setup, true, "")
	if err != nil {
		cancel()
		return err
	}
	defer release()
	id := "feed-" + domain.ID()
	events := p.subscribe(id)
	defer p.unsubscribe(id)
	var subscribed struct {
		Subscribed bool `json:"subscribed"`
	}
	rpc, cancelRPC := context.WithTimeout(setup, 60*time.Second)
	err = p.call(rpc, map[string]any{"op": "subscribe", "subscriptionId": id, "feedKey": key}, &subscribed)
	cancelRPC()
	cancel()
	if err != nil {
		m.recordError(err)
		return err
	}
	if !subscribed.Subscribed {
		return errors.New("The JForex bridge did not confirm its subscription")
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-m.ctx.Done():
			return errors.New("The Dukascopy service is closed")
		case <-p.done:
			err := p.failure()
			m.recordError(err)
			return err
		case message := <-events:
			if message.Event != "bars" && message.Event != "health" {
				return errors.New("The JForex bridge returned an unknown subscription event")
			}
			bars, err := normalizeBars(key, message.Bars)
			if err != nil {
				return err
			}
			health := message.Health
			if health.Reason != "" {
				health.Reason = p.redact(health.Reason)
			}
			if len(health.State) > 64 || health.LastMessageAt < 0 || health.LastTradeAt < 0 {
				return errors.New("The JForex bridge returned invalid feed status")
			}
			// SDK bars and connection health are separate records. A history seed
			// or closed-bar update must preserve the preceding connection/quote
			// state rather than manufacture a live status with zero timestamps.
			if message.Event == "health" && health.State == "" {
				return errors.New("The JForex bridge returned an empty feed status")
			}
			m.mu.Lock()
			if m.process == p {
				switch health.State {
				case "live", "marketClosed":
					m.online = true
					m.lastError = ""
				case "offline", "restricted", "disconnected", "error":
					m.online = false
				}
			}
			m.mu.Unlock()
			emit(bars, health)
		}
	}
}

func (m *Manager) Diagnostics() exchange.Diagnostics {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := exchange.Diagnostics{}
	if m.process != nil {
		select {
		case <-m.process.done:
		default:
			if m.authenticated && m.online {
				result.Sockets = 1
			}
		}
	}
	return result
}

func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	m.cancel()
	if m.idle != nil {
		m.idle.Stop()
		m.idle = nil
	}
	if m.process != nil {
		m.process.finish(errors.New("The Dukascopy service is closed"))
	}
	m.mu.Unlock()
	m.wg.Wait()
	m.processes.Wait()
}

package exchange

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type wireEvent struct {
	data          []byte
	state, reason string
	err           error
}
type socketHub struct {
	a             *publicAdapter
	mu            sync.Mutex
	subs          map[string]map[uint64]chan wireEvent
	next          uint64
	running       bool
	wake          chan struct{}
	sequence      int64
	hasSequence   bool
	sockets       int
	reconnects    int64
	attemptCancel context.CancelFunc
	active        map[string]bool
}

func newSocketHub(a *publicAdapter) *socketHub {
	return &socketHub{a: a, subs: map[string]map[uint64]chan wireEvent{}, wake: make(chan struct{}, 1), active: map[string]bool{}}
}
func (h *socketHub) subscribe(topic string) (<-chan wireEvent, func()) {
	h.mu.Lock()
	h.next++
	id := h.next
	ch := make(chan wireEvent, 512)
	if h.subs[topic] == nil {
		h.subs[topic] = map[uint64]chan wireEvent{}
	}
	h.subs[topic][id] = ch
	if h.active[topic] {
		ch <- wireEvent{state: "subscribed", reason: "Attached to shared public subscription"}
	}
	if !h.running {
		h.running = true
		go h.run()
	}
	h.mu.Unlock()
	h.signal()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs[topic], id)
		if len(h.subs[topic]) == 0 {
			delete(h.subs, topic)
		}
		if len(h.subs) == 0 && h.attemptCancel != nil {
			h.attemptCancel()
		}
		h.mu.Unlock()
		h.signal()
	}
}
func (h *socketHub) signal() {
	select {
	case h.wake <- struct{}{}:
	default:
	}
}
func (h *socketHub) topics() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, len(h.subs))
	for t := range h.subs {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
func (h *socketHub) publish(topic string, e wireEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for key, channels := range h.subs {
		if topic != "" && key != topic {
			continue
		}
		for _, ch := range channels {
			select {
			case ch <- e:
			default:
				// Flush bounded backlog and make loss explicit. Consumers repair via REST.
			drain:
				for {
					select {
					case <-ch:
					default:
						break drain
					}
				}
				ch <- wireEvent{state: "reconciling", reason: "Stream buffer overflow; repairing affected candles"}
			}
		}
	}
}
func (h *socketHub) run() {
	permanent := false
	defer func() {
		h.mu.Lock()
		h.running = false
		h.sockets = 0
		h.active = map[string]bool{}
		if !permanent && len(h.subs) > 0 {
			h.running = true
			go h.run()
		}
		h.mu.Unlock()
	}()
	delay := time.Second
	for len(h.topics()) > 0 {
		h.publish("", wireEvent{state: "connecting", reason: "Connecting public spot stream"})
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		h.mu.Lock()
		h.attemptCancel = cancel
		if len(h.subs) == 0 {
			cancel()
		}
		h.mu.Unlock()
		dialer := *websocket.DefaultDialer
		dialer.HandshakeTimeout = 12 * time.Second
		conn, resp, err := dialer.DialContext(ctx, h.a.ws, http.Header{"User-Agent": {"CryptoDesk/1.0"}})
		cancel()
		if err != nil {
			if resp != nil {
				resp.Body.Close()
				if resp.StatusCode == 403 || resp.StatusCode == 451 {
					permanent = true
					h.publish("", wireEvent{err: &Error{Kind: "restricted", Message: fmt.Sprintf("%s WebSocket restricted (HTTP %d)", h.a.provider, resp.StatusCode)}})
					return
				}
				if resp.StatusCode == 429 {
					permanent = true
					h.publish("", wireEvent{err: &Error{Kind: "rateLimited", Message: h.a.provider + " WebSocket rate limited", RetryAt: time.Now().Add(time.Minute).Unix()}})
					return
				}
			}
			h.publish("", wireEvent{state: "offline", reason: "Public WebSocket unavailable; retrying"})
			if !h.pause(delay) {
				return
			}
			delay = min(delay*2, 60*time.Second)
			continue
		}
		h.mu.Lock()
		h.sockets = 1
		h.reconnects++
		h.mu.Unlock()
		conn.SetReadLimit(2 << 20)
		_ = conn.SetReadDeadline(time.Now().Add(75 * time.Second))
		conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(75 * time.Second)) })
		active := map[string]bool{}
		if err = h.syncSubscriptions(conn, active); err != nil {
			conn.Close()
			if !h.pause(delay) {
				return
			}
			continue
		}
		delay = time.Second
		h.hasSequence = false
		h.publish("", wireEvent{state: "subscribed", reason: "Public stream subscribed"})
		err = h.connected(conn, active)
		conn.Close()
		h.mu.Lock()
		h.sockets = 0
		h.active = map[string]bool{}
		h.mu.Unlock()
		if len(h.topics()) == 0 {
			return
		}
		h.publish("", wireEvent{state: "disconnected", reason: "Stream interrupted; buffering and repairing overlap"})
		if !h.pause(delay) {
			return
		}
		delay = min(delay*2, 60*time.Second)
	}
}
func (h *socketHub) pause(d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			return len(h.topics()) > 0
		case <-h.wake:
			if len(h.topics()) == 0 {
				return false
			}
		}
	}
}
func (h *socketHub) connected(conn *websocket.Conn, active map[string]bool) error {
	type readResult struct {
		data []byte
		err  error
	}
	messages := make(chan readResult, 128)
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			_, data, err := conn.ReadMessage()
			if err == nil {
				_ = conn.SetReadDeadline(time.Now().Add(75 * time.Second))
			}
			select {
			case messages <- readResult{data, err}:
			case <-done:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	changes := time.NewTicker(time.Second)
	defer changes.Stop()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	rotation := time.NewTimer(23*time.Hour + 50*time.Minute)
	defer rotation.Stop()
	for {
		select {
		case <-h.wake:
			if len(h.topics()) == 0 {
				return nil
			}
		case <-changes.C:
			if err := h.syncSubscriptions(conn, active); err != nil {
				return err
			}
			if len(h.topics()) == 0 {
				return nil
			}
		case <-rotation.C:
			return fmt.Errorf("scheduled public WebSocket rotation")
		case <-ping.C:
			if h.a.provider == "bybit" {
				if err := conn.WriteJSON(map[string]any{"op": "ping"}); err != nil {
					return err
				}
			}
		case result := <-messages:
			if result.err != nil {
				return result.err
			}
			h.route(result.data)
		}
	}
}
func (h *socketHub) syncSubscriptions(conn *websocket.Conn, active map[string]bool) error {
	wanted := map[string]bool{}
	for _, topic := range h.topics() {
		wanted[topic] = true
	}
	var add, remove []string
	for topic := range wanted {
		if !active[topic] {
			add = append(add, topic)
		}
	}
	for topic := range active {
		if !wanted[topic] {
			remove = append(remove, topic)
		}
	}
	sort.Strings(add)
	sort.Strings(remove)
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	write := func(topics []string, subscribe bool) error {
		if len(topics) == 0 {
			return nil
		}
		switch h.a.provider {
		case "binance":
			method := "UNSUBSCRIBE"
			if subscribe {
				method = "SUBSCRIBE"
			}
			return conn.WriteJSON(map[string]any{"method": method, "params": topics, "id": time.Now().UnixMilli()})
		case "bybit":
			op := "unsubscribe"
			if subscribe {
				op = "subscribe"
			}
			return conn.WriteJSON(map[string]any{"op": op, "args": topics})
		case "coinbase":
			kind := "unsubscribe"
			if subscribe {
				kind = "subscribe"
			}
			if err := conn.WriteJSON(map[string]any{"type": kind, "product_ids": topics, "channel": "market_trades"}); err != nil {
				return err
			}
			return conn.WriteJSON(map[string]any{"type": kind, "product_ids": topics, "channel": "heartbeats"})
		}
		return nil
	}
	if err := write(remove, false); err != nil {
		return err
	}
	if err := write(add, true); err != nil {
		return err
	}
	for _, t := range remove {
		delete(active, t)
	}
	h.mu.Lock()
	for _, t := range remove {
		delete(h.active, t)
	}
	for _, t := range add {
		h.active[t] = true
	}
	h.mu.Unlock()
	for _, t := range add {
		active[t] = true
		h.publish(t, wireEvent{state: "subscribed", reason: "Public stream subscribed"})
	}
	return nil
}
func (h *socketHub) route(data []byte) {
	var e struct {
		Stream    string          `json:"stream"`
		Data      json.RawMessage `json:"data"`
		Type      string          `json:"type"`
		Event     string          `json:"e"`
		EventTime int64           `json:"E"`
		Symbol    string          `json:"s"`
		Topic     string          `json:"topic"`
		Sequence  *int64          `json:"sequence_num"`
		Channel   string          `json:"channel"`
		Success   *bool           `json:"success"`
		RetMsg    string          `json:"ret_msg"`
		Code      *int            `json:"code"`
		Msg       string          `json:"msg"`
		Message   string          `json:"message"`
		K         struct {
			Interval string `json:"i"`
		} `json:"k"`
		Events []struct {
			Trades []struct {
				ProductID string `json:"product_id"`
			}
		} `json:"events"`
	}
	if json.Unmarshal(data, &e) != nil {
		h.publish("", wireEvent{state: "reconciling", reason: "Malformed WebSocket payload; repairing overlap"})
		return
	}
	if e.Stream != "" && len(e.Data) > 0 {
		h.route(e.Data)
		return
	}
	if e.Event == "serverShutdown" {
		h.publish("", wireEvent{state: "reconciling", reason: "Exchange announced stream shutdown"})
		return
	}
	if e.Success != nil && !*e.Success {
		h.publish("", wireEvent{err: &Error{Kind: "unknownInstrument", Message: h.a.provider + " rejected subscription: " + e.RetMsg}})
		return
	}
	if e.Type == "error" || e.Code != nil {
		h.publish("", wireEvent{err: &Error{Kind: "unknownInstrument", Message: h.a.provider + " rejected subscription: " + e.Msg + e.Message}})
		return
	}
	if h.a.provider == "coinbase" && e.Sequence != nil {
		if h.hasSequence && *e.Sequence != h.sequence+1 {
			h.publish("", wireEvent{state: "reconciling", reason: "Coinbase connection sequence gap; repairing affected candles"})
		}
		if !h.hasSequence || *e.Sequence > h.sequence {
			h.sequence = *e.Sequence
		}
		h.hasSequence = true
	}
	switch h.a.provider {
	case "binance":
		if e.Event == "kline" {
			h.publish(strings.ToLower(e.Symbol)+"@kline_"+e.K.Interval, wireEvent{data: data})
		}
	case "bybit":
		if strings.HasPrefix(e.Topic, "kline.") {
			h.publish(e.Topic, wireEvent{data: data})
		} else {
			h.publish("", wireEvent{state: "heartbeat"})
		}
	case "coinbase":
		if e.Channel == "market_trades" {
			products := map[string]bool{}
			for _, event := range e.Events {
				for _, trade := range event.Trades {
					products[trade.ProductID] = true
				}
			}
			for product := range products {
				h.publish(product, wireEvent{data: data})
			}
		} else {
			h.publish("", wireEvent{state: "heartbeat"})
		}
	}
}

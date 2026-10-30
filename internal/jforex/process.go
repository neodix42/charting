package jforex

import (
	"bufio"
	"context"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type bridgeError struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

type bridgeMessage struct {
	ID             string          `json:"id"`
	OK             bool            `json:"ok"`
	Result         json.RawMessage `json:"result"`
	Error          *bridgeError    `json:"error"`
	Event          string          `json:"event"`
	SubscriptionID string          `json:"subscriptionId"`
	Bars           []domain.Candle `json:"bars"`
	Health         domain.Health   `json:"health"`
	CoverageFrom   int64           `json:"coverageFrom"`
	CoverageTo     int64           `json:"coverageTo"`
}

type bridgeProcess struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	cancel   context.CancelFunc
	writeMu  sync.Mutex
	mu       sync.Mutex
	pending  map[string]chan bridgeMessage
	subs     map[string]chan bridgeMessage
	err      error
	once     sync.Once
	done     chan struct{}
	exited   chan struct{}
	next     atomic.Uint64
	username string
	password string
	secrets  []string
}

func startBridge(ctx context.Context, factory commandFactory, username, password string) (*bridgeProcess, error) {
	ctx, cancel := context.WithCancel(ctx)
	cmd, err := factory(ctx)
	if err != nil {
		cancel()
		return nil, err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, errors.New("Could not create the private JForex input pipe")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		cancel()
		return nil, errors.New("Could not create the private JForex output pipe")
	}
	// SDK diagnostics can contain account details. Only our structured,
	// explicitly redacted protocol errors cross the application boundary.
	cmd.Stderr = io.Discard
	p := &bridgeProcess{cmd: cmd, stdin: stdin, cancel: cancel, pending: map[string]chan bridgeMessage{}, subs: map[string]chan bridgeMessage{}, done: make(chan struct{}), exited: make(chan struct{}), username: username, password: password}
	if err = cmd.Start(); err != nil {
		stdin.Close()
		cancel()
		return nil, errors.New("Could not start the JForex bridge; Java 17 or newer and the complete jforex-runtime folder are required")
	}
	go p.read(stdout)
	return p, nil
}

func (p *bridgeProcess) read(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 16*1024), 8*1024*1024)
	for scanner.Scan() {
		var message bridgeMessage
		if json.Unmarshal(scanner.Bytes(), &message) != nil || (message.ID == "" && message.Event == "") {
			p.finish(errors.New("The JForex bridge returned an invalid protocol record"))
			break
		}
		p.mu.Lock()
		if message.ID != "" {
			if target := p.pending[message.ID]; target != nil {
				delete(p.pending, message.ID)
				target <- message
			}
		} else if target := p.subs[message.SubscriptionID]; target != nil {
			select {
			case target <- message:
			default:
				p.mu.Unlock()
				p.finish(errors.New("The JForex update queue overflowed; retry the chart to reconcile missing bars"))
				goto stopped
			}
		}
		p.mu.Unlock()
	}
	if scanner.Err() != nil {
		p.finish(errors.New("The JForex bridge exceeded its protocol limit or stopped unexpectedly"))
	}
	p.finish(errors.New("The JForex connection ended; retry the chart or test the connection in Settings"))
stopped:
	_ = p.cmd.Wait()
	close(p.exited)
}

func (p *bridgeProcess) finish(err error) {
	p.once.Do(func() {
		p.mu.Lock()
		p.err = err
		p.mu.Unlock()
		close(p.done)
		_ = p.stdin.Close()
		p.cancel()
	})
}

func (p *bridgeProcess) stop() {
	p.finish(errors.New("The JForex connection was closed"))
	<-p.exited
}

func (p *bridgeProcess) failure() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

func (p *bridgeProcess) call(ctx context.Context, request map[string]any, destination any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	id := strconv.FormatUint(p.next.Add(1), 10)
	request["id"] = id
	payload, err := json.Marshal(request)
	if err != nil || len(payload) > 32*1024 {
		return errors.New("The JForex request exceeds its protocol limit")
	}
	response := make(chan bridgeMessage, 1)
	p.mu.Lock()
	if p.err != nil {
		err = p.err
		p.mu.Unlock()
		return err
	}
	p.pending[id] = response
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.pending, id); p.mu.Unlock() }()
	written := make(chan error, 1)
	go func() {
		p.writeMu.Lock()
		defer p.writeMu.Unlock()
		_, err := p.stdin.Write(append(payload, '\n'))
		written <- err
	}()
	select {
	case <-ctx.Done():
		// A cancellation racing a completed tiny pipe write must not disconnect
		// unrelated charts. Only an actually blocked writer poisons the session.
		go func() {
			timer := time.NewTimer(2 * time.Second)
			defer timer.Stop()
			select {
			case <-written:
			case <-p.done:
			case <-timer.C:
				p.finish(errors.New("The JForex bridge did not accept a request before its deadline"))
			}
		}()
		return ctx.Err()
	case <-p.done:
		return p.failure()
	case err := <-written:
		if err != nil {
			p.finish(errors.New("Could not write to the JForex bridge"))
			return p.failure()
		}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		return p.failure()
	case message := <-response:
		if !message.OK {
			if message.Error == nil {
				return errors.New("The JForex bridge rejected a request")
			}
			return &exchange.Error{Kind: errorKind(message.Error.Kind), Message: p.redact(message.Error.Message)}
		}
		if destination != nil && json.Unmarshal(message.Result, destination) != nil {
			return errors.New("The JForex bridge returned an invalid response")
		}
		return nil
	}
}

func errorKind(kind string) string {
	switch kind {
	case "offline", "restricted", "unknownInstrument", "unsupportedInterval", "rateLimited", "timeout", "internal":
		return kind
	case "authentication":
		return "restricted"
	default:
		return "offline"
	}
}

func (p *bridgeProcess) redact(value string) string {
	p.mu.Lock()
	secrets := append([]string{p.password, p.username}, p.secrets...)
	p.mu.Unlock()
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[redacted]")
		}
	}
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	if len(value) > 1500 {
		value = value[:1500]
	}
	if strings.TrimSpace(value) == "" {
		return "The JForex request failed; check the connection in Settings"
	}
	return value
}

func (p *bridgeProcess) addSecret(secret string) {
	if secret == "" {
		return
	}
	p.mu.Lock()
	p.secrets = append(p.secrets, secret)
	p.mu.Unlock()
}

func (p *bridgeProcess) removeSecret(secret string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for index, value := range p.secrets {
		if value == secret {
			p.secrets = append(p.secrets[:index], p.secrets[index+1:]...)
			return
		}
	}
}

func (p *bridgeProcess) subscribe(id string) chan bridgeMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	channel := make(chan bridgeMessage, 64)
	p.subs[id] = channel
	return channel
}

func (p *bridgeProcess) unsubscribe(id string) {
	p.mu.Lock()
	delete(p.subs, id)
	p.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = p.call(ctx, map[string]any{"op": "unsubscribe", "subscriptionId": id}, nil)
}

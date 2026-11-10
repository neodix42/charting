// Package hyperliquid owns authenticated trading separately from public chart
// adapters and Wails. Credential reads never expose the signing private key.
package hyperliquid

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Settings struct {
	AccountAddress  string `json:"accountAddress"`
	PrivateKey      string `json:"privateKey"`
	Network         string `json:"network"`
	ClearPrivateKey bool   `json:"clearPrivateKey"`
}
type Info struct {
	AccountAddress       string `json:"accountAddress"`
	AgentAddress         string `json:"agentAddress"`
	Network              string `json:"network"`
	PrivateKeyConfigured bool   `json:"privateKeyConfigured"`
	Connected            bool   `json:"connected"`
	Error                string `json:"error,omitempty"`
}
type Store struct {
	mu               sync.Mutex
	gate             chan struct{}
	profile          string
	settings         Settings
	lastError        string
	connected        bool
	client           *http.Client
	endpoint         string
	now              func() time.Time
	previews         map[string]prepared
	transferPreviews map[string]preparedTransfer
	fundingRevision  uint64
	balanceReadGate  chan struct{}
}

func NewStore(profile string) *Store {
	s := &Store{profile: profile, gate: make(chan struct{}, 1), client: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, now: time.Now, previews: map[string]prepared{}, settings: Settings{Network: "mainnet"}}
	raw, err := readBounded(filepath.Join(profile, "hyperliquid.json"), 16384)
	if errors.Is(err, os.ErrNotExist) {
		return s
	}
	if err != nil || len(raw) > 16384 {
		s.lastError = "Cannot read Hyperliquid settings; original preserved"
		return s
	}
	var saved Settings
	if json.Unmarshal(raw, &saved) != nil {
		s.lastError = "Invalid Hyperliquid settings; original preserved"
		return s
	}
	normalized, err := normalize(saved)
	if err != nil {
		s.lastError = "Invalid Hyperliquid settings; original preserved"
		return s
	}
	s.settings = normalized
	return s
}

var addressPattern = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

func normalize(v Settings) (Settings, error) {
	v.AccountAddress = strings.ToLower(strings.TrimSpace(v.AccountAddress))
	v.PrivateKey = strings.TrimSpace(v.PrivateKey)
	if v.Network == "" {
		v.Network = "mainnet"
	}
	if v.Network != "mainnet" && v.Network != "testnet" {
		return v, errors.New("Choose mainnet or testnet")
	}
	if v.AccountAddress != "" && !addressPattern.MatchString(v.AccountAddress) {
		return v, errors.New("Enter the master trading account's 0x wallet address")
	}
	if v.PrivateKey != "" {
		key, err := keyFromString(v.PrivateKey)
		if err != nil {
			return v, err
		}
		if v.AccountAddress == "" {
			return v, errors.New("Master account address is required with an API wallet key")
		}
		if addressFor(key) == v.AccountAddress {
			return v, errors.New("Use an approved API wallet key, not the master wallet private key")
		}
	}
	v.ClearPrivateKey = false
	return v, nil
}
func (s *Store) Info() Info { s.mu.Lock(); defer s.mu.Unlock(); return s.infoLocked() }
func (s *Store) infoLocked() Info {
	v := s.settings
	i := Info{AccountAddress: v.AccountAddress, Network: v.Network, PrivateKeyConfigured: v.PrivateKey != "", Connected: s.connected, Error: s.lastError}
	if k, e := keyFromString(v.PrivateKey); e == nil {
		i.AgentAddress = addressFor(k)
	}
	return i
}
func (s *Store) Configure(v Settings) (Info, error) {
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	default:
		return s.Info(), errors.New("Wait for the active Hyperliquid order before changing credentials")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastError != "" {
		return s.infoLocked(), errors.New(s.lastError)
	}
	if v.Network == "" {
		v.Network = "mainnet"
	}
	if v.PrivateKey == "" && !v.ClearPrivateKey && strings.EqualFold(strings.TrimSpace(v.AccountAddress), s.settings.AccountAddress) && v.Network == s.settings.Network {
		v.PrivateKey = s.settings.PrivateKey
	}
	if v.ClearPrivateKey {
		v.PrivateKey = ""
	}
	v, err := normalize(v)
	if err != nil {
		return s.infoLocked(), err
	}
	if err = atomicJSON(filepath.Join(s.profile, "hyperliquid.json"), v); err != nil {
		return s.infoLocked(), errors.New("Could not save Hyperliquid settings")
	}
	s.settings = v
	s.lastError = ""
	s.connected = false
	s.previews = map[string]prepared{}
	s.transferPreviews = nil
	s.fundingRevision++
	return s.infoLocked(), nil
}
func atomicJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".hyperliquid-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func (s *Store) snapshot() (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastError != "" {
		return Settings{}, errors.New(s.lastError)
	}
	if s.settings.PrivateKey == "" {
		return Settings{}, errors.New("Configure a Hyperliquid API wallet key first")
	}
	return s.settings, nil
}
func (s *Store) base(v Settings) string {
	if s.endpoint != "" {
		return s.endpoint
	}
	if v.Network == "testnet" {
		return "https://api.hyperliquid-testnet.xyz"
	}
	return "https://api.hyperliquid.xyz"
}
func (s *Store) post(ctx context.Context, v Settings, path string, input, output any) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.base(v)+path, bytes.NewReader(raw))
	if err != nil {
		return errors.New("Cannot create Hyperliquid request")
	}
	request.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(request)
	if err != nil {
		return errors.New("Hyperliquid request failed or timed out")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("Hyperliquid rejected or could not complete the request")
	}
	if len(data) >= 4<<20 || json.Unmarshal(data, output) != nil {
		return errors.New("Hyperliquid returned an invalid response")
	}
	return nil
}
func (s *Store) checkAgent(ctx context.Context, v Settings) error {
	k, err := keyFromString(v.PrivateKey)
	if err != nil {
		return err
	}
	var role struct {
		Role string `json:"role"`
		Data struct {
			User string `json:"user"`
		} `json:"data"`
	}
	if err = s.post(ctx, v, "/info", map[string]any{"type": "userRole", "user": addressFor(k)}, &role); err != nil {
		return err
	}
	if role.Role != "agent" || !strings.EqualFold(role.Data.User, v.AccountAddress) {
		return errors.New("API wallet is not approved for this account on the selected network; create/approve it in Hyperliquid API settings")
	}
	var agents []struct {
		Address    string `json:"address"`
		ValidUntil int64  `json:"validUntil"`
	}
	if err = s.post(ctx, v, "/info", map[string]string{"type": "extraAgents", "user": v.AccountAddress}, &agents); err != nil {
		return err
	}
	for _, agent := range agents {
		if strings.EqualFold(agent.Address, addressFor(k)) && agent.ValidUntil <= s.now().UnixMilli() {
			return errors.New("Hyperliquid API wallet approval expired; renew it before trading")
		}
	}
	return nil
}
func (s *Store) Test(ctx context.Context) (Info, error) {
	v, err := s.snapshot()
	if err == nil {
		err = s.checkAgent(ctx, v)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settings == v {
		s.connected = err == nil
	}
	return s.infoLocked(), err
}
func randomID() (string, error) {
	var raw [16]byte
	_, err := rand.Read(raw[:])
	return hex.EncodeToString(raw[:]), err
}

func readBounded(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(data)) > limit {
		return nil, errors.New("Hyperliquid file exceeds its size bound")
	}
	return data, err
}

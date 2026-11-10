package hyperliquid

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const transferUSDC = "0x11111111111111111111111111111111"

type transferMock struct {
	mu            sync.Mutex
	now           atomic.Int64
	mode          string
	available     string
	tokenID       string
	weiDecimals   int
	xyzCollateral int
	failDex       string
	staleDex      string
	revoked       bool
	exchangeBody  string
	exchangeCode  int
	disconnect    bool
	writes        []map[string]json.RawMessage
	beforeInfo    func(string)
	onWrite       func(map[string]json.RawMessage)
}

func (m *transferMock) writeCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.writes)
}

func (m *transferMock) firstWrite() map[string]json.RawMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.writes[0]
}

func transferFixture(t *testing.T) (*Store, *transferMock) {
	t.Helper()
	m := &transferMock{mode: "disabled", available: "100", tokenID: transferUSDC, weiDecimals: 8,
		failDex: "absent", staleDex: "absent", exchangeCode: 200,
		exchangeBody: `{"status":"ok","response":{"type":"default"}}`}
	m.now.Store(time.Now().UnixMilli())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if r.URL.Path == "/exchange" {
			m.mu.Lock()
			m.writes = append(m.writes, input)
			callback, body, code, disconnect := m.onWrite, m.exchangeBody, m.exchangeCode, m.disconnect
			m.mu.Unlock()
			if callback != nil {
				callback(input)
			}
			if disconnect {
				connection, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = connection.Close()
				}
				return
			}
			w.WriteHeader(code)
			fmt.Fprint(w, body)
			return
		}
		var kind, dex string
		_ = json.Unmarshal(input["type"], &kind)
		_ = json.Unmarshal(input["dex"], &dex)
		m.mu.Lock()
		callback := m.beforeInfo
		m.mu.Unlock()
		if callback != nil {
			callback(kind)
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		switch kind {
		case "userRole":
			role := "agent"
			if m.revoked {
				role = "user"
			}
			fmt.Fprintf(w, `{"role":%q,"data":{"user":%q}}`, role, testAccount)
		case "extraAgents":
			key, _ := keyFromString(testPrivate)
			fmt.Fprintf(w, `[{"address":%q,"validUntil":%d}]`, addressFor(key), m.now.Load()+3600000)
		case "userAbstraction":
			_ = json.NewEncoder(w).Encode(m.mode)
		case "perpDexs":
			fmt.Fprint(w, `[null,{"name":"xyz"},{"name":"para"}]`)
		case "allPerpMetas":
			fmt.Fprintf(w, `[{"collateralToken":0,"universe":[]},{"collateralToken":%d,"universe":[]},{"collateralToken":0,"universe":[]}]`, m.xyzCollateral)
		case "spotMeta":
			fmt.Fprintf(w, `{"tokens":[{"index":0,"name":"USDC","tokenId":%q,"szDecimals":6,"weiDecimals":%d},{"index":1,"name":"USDT","tokenId":"0x22222222222222222222222222222222","szDecimals":6,"weiDecimals":8}]}`, m.tokenID, m.weiDecimals)
		case "clearinghouseState":
			if dex == m.failDex {
				w.WriteHeader(503)
				return
			}
			at := m.now.Load()
			if dex == m.staleDex {
				at -= 16000
			}
			fmt.Fprintf(w, `{"time":%d,"withdrawable":%q,"assetPositions":[],"marginSummary":{"accountValue":"100","totalRawUsd":"100","totalMarginUsed":"0"}}`, at, m.available)
		case "spotClearinghouseState":
			if m.failDex == "spot" {
				w.WriteHeader(503)
				return
			}
			fmt.Fprint(w, `{"balances":[{"token":0,"coin":"USDC","total":"20","hold":"2"},{"token":1,"coin":"USDT","total":"10","hold":"0"}]}`)
		default:
			t.Errorf("unexpected transfer info request %q", kind)
			w.WriteHeader(400)
		}
	}))
	t.Cleanup(server.Close)
	s := NewStore(t.TempDir())
	s.endpoint = server.URL
	s.now = func() time.Time { return time.UnixMilli(m.now.Load()) }
	if _, err := s.Configure(Settings{AccountAddress: testAccount, PrivateKey: testPrivate, Network: "testnet"}); err != nil {
		t.Fatal(err)
	}
	return s, m
}

func transferRequest() TransferRequest {
	return TransferRequest{SourceDex: "", DestinationDex: "xyz", Token: 0, Amount: "12.34000000"}
}

func TestInternalTransferConfirmedCanonicalWireDurableClaimAndOneUse(t *testing.T) {
	s, m := transferFixture(t)
	s.mu.Lock()
	s.previews["old-order"] = prepared{}
	s.mu.Unlock()
	p, err := s.PreviewTransfer(context.Background(), transferRequest())
	if err != nil || p.Amount != "12.34" || p.AvailableBefore != "100" || p.AvailableAfter != "87.66" || p.AccountAddress != testAccount || p.Network != "testnet" {
		t.Fatal(p, err)
	}
	if m.writeCount() != 0 || len(s.previews) != 0 {
		t.Fatal("preview wrote to exchange or retained a stale order review")
	}
	journal := filepath.Join(s.profile, "hyperliquid-transfers", p.ID+".json")
	m.onWrite = func(input map[string]json.RawMessage) {
		raw, err := os.ReadFile(journal)
		if err != nil || !strings.Contains(string(raw), `"claimed"`) {
			t.Error("exchange request preceded durable claim", err)
		}
		if strings.Contains(string(raw), testPrivate) || strings.Contains(string(raw), "signature") {
			t.Error("transfer journal contains signing material")
		}
	}
	result, err := s.SubmitTransfer(context.Background(), p.ID)
	if err != nil || result.Status != "confirmed" || m.writeCount() != 1 {
		t.Fatal(result, err, m.writeCount())
	}
	write := m.firstWrite()
	var action struct {
		Type, Destination, SourceDex, DestinationDex, Token, Amount, FromSubAccount string
		Nonce                                                                       uint64
	}
	if err := json.Unmarshal(write["action"], &action); err != nil {
		t.Fatal(err)
	}
	var nonce, expires uint64
	var got signature
	_ = json.Unmarshal(write["nonce"], &nonce)
	_ = json.Unmarshal(write["expiresAfter"], &expires)
	_ = json.Unmarshal(write["signature"], &got)
	if action.Type != "agentSendAsset" || action.Destination != testAccount || action.SourceDex != "" || action.DestinationDex != "xyz" || action.Token != "USDC:"+transferUSDC || action.Amount != "12.34" || action.FromSubAccount != "" || action.Nonce != nonce || expires != uint64(p.ExpiresAt) {
		t.Fatal("wrong transfer wire", action, nonce, expires)
	}
	if _, exists := write["vaultAddress"]; exists {
		t.Fatal("agent transfer must not use a vault address")
	}
	key, _ := keyFromString(testPrivate)
	if want := signAction(key, transferWire(p, nonce), nonce, expires, false); got != want {
		t.Fatal("wire and signed transfer differ", got, want)
	}
	if _, err := s.SubmitTransfer(context.Background(), p.ID); err == nil || m.writeCount() != 1 {
		t.Fatal("transfer replayed", err)
	}
	stat, err := os.Stat(journal)
	if err != nil || stat.Mode().Perm() != 0600 {
		t.Fatal("journal permissions", err)
	}
	raw, _ := os.ReadFile(journal)
	if !strings.Contains(string(raw), `"confirmed"`) || strings.Contains(string(raw), testPrivate) || strings.Contains(string(raw), `"signature"`) {
		t.Fatal("unsafe or missing result journal")
	}
	next, err := s.nextNonce()
	if err != nil || next <= nonce {
		t.Fatal("transfer did not advance shared persisted nonce", next, nonce, err)
	}
}

func TestInternalTransferRoutesBoundsAndPrecision(t *testing.T) {
	s, m := transferFixture(t)
	state, err := s.AccountBalances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"", "xyz"}, {"xyz", "para"}, {"para", "spot"}, {"spot", ""}} {
		if _, err := s.validateTransferBalances(s.settings, TransferRequest{SourceDex: pair[0], DestinationDex: pair[1], Token: 0, Amount: "1.00000001"}, state); err != nil {
			t.Fatalf("valid route %v: %v", pair, err)
		}
	}
	for _, amount := range []string{"", "0", "-1", "+1", "1e1", " 1", "1 ", ".", "1..0", "100.00000001", "1.000000001"} {
		r := transferRequest()
		r.Amount = amount
		if _, err := s.validateTransferBalances(s.settings, r, state); err == nil {
			t.Errorf("invalid amount accepted: %q", amount)
		}
	}
	for _, r := range []TransferRequest{{SourceDex: "", DestinationDex: "", Amount: "1"}, {SourceDex: "", DestinationDex: "missing", Amount: "1"}, {SourceDex: "", DestinationDex: "xyz", Token: 1, Amount: "1"}, {SourceDex: "spot", DestinationDex: "xyz", Token: -1, Amount: "1"}, {SourceDex: "https://evil", DestinationDex: "xyz", Amount: "1"}} {
		if _, err := s.validateTransferBalances(s.settings, r, state); err == nil {
			t.Errorf("invalid route accepted: %+v", r)
		}
	}
	m.xyzCollateral = 1
	if _, err := s.PreviewTransfer(context.Background(), transferRequest()); err == nil {
		t.Fatal("transfer silently converted USDC into USDT")
	}
}

func TestInternalTransferFreshRevalidation(t *testing.T) {
	for _, change := range []string{"balance", "tokenId", "precision", "collateral", "shared", "unknown-mode", "revoked", "stale", "source-failure", "destination-failure", "expired", "expired-during-read", "configuration"} {
		t.Run(change, func(t *testing.T) {
			s, m := transferFixture(t)
			p, err := s.PreviewTransfer(context.Background(), transferRequest())
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "balance":
				m.available = "1"
			case "tokenId":
				m.tokenID = "0x33333333333333333333333333333333"
			case "precision":
				m.weiDecimals = 7
			case "collateral":
				m.xyzCollateral = 1
			case "shared":
				m.mode = "unifiedAccount"
			case "unknown-mode":
				m.mode = "new-mode"
			case "revoked":
				m.revoked = true
			case "stale":
				m.staleDex = ""
			case "source-failure":
				m.failDex = ""
			case "destination-failure":
				m.failDex = "xyz"
			case "expired":
				m.now.Add(60000)
			case "expired-during-read":
				var once sync.Once
				m.beforeInfo = func(kind string) {
					if kind == "userAbstraction" {
						once.Do(func() { m.now.Add(60000) })
					}
				}
			case "configuration":
				if _, err := s.Configure(Settings{AccountAddress: testAccount, Network: "mainnet", PrivateKey: testPrivate}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.SubmitTransfer(context.Background(), p.ID); err == nil || m.writeCount() != 0 {
				t.Fatal("stale or invalid transfer sent", err, m.writeCount())
			}
			if _, err := s.SubmitTransfer(context.Background(), p.ID); err == nil {
				t.Fatal("failed review remained reusable")
			}
		})
	}
}

func TestInternalTransferUnrelatedVenueFailureDoesNotInventOrBlockFunds(t *testing.T) {
	s, m := transferFixture(t)
	m.failDex = "para"
	p, err := s.PreviewTransfer(context.Background(), transferRequest())
	if err != nil {
		t.Fatal("unrelated DEX failure blocked verified route", err)
	}
	if result, err := s.SubmitTransfer(context.Background(), p.ID); err != nil || result.Status != "confirmed" {
		t.Fatal(result, err)
	}
}

func TestInternalTransferAcknowledgementsNeverRetry(t *testing.T) {
	for _, test := range []struct {
		name, body, status string
		code               int
		disconnect         bool
	}{
		{"rejection", `{"status":"err","response":"insufficient balance secret-provider-text"}`, "rejected", 200, false},
		{"malformed", `{`, "uncertain", 200, false},
		{"wrong-ack", `{"status":"ok","response":{}}`, "uncertain", 200, false},
		{"wrong-error", `{"status":"err","response":{}}`, "uncertain", 200, false},
		{"http-error", `{"status":"err","response":"rejected"}`, "uncertain", 503, false},
		{"connection-lost", ``, "uncertain", 200, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, m := transferFixture(t)
			m.exchangeBody, m.exchangeCode, m.disconnect = test.body, test.code, test.disconnect
			p, err := s.PreviewTransfer(context.Background(), transferRequest())
			if err != nil {
				t.Fatal(err)
			}
			result, err := s.SubmitTransfer(context.Background(), p.ID)
			if err != nil || result.Status != test.status || m.writeCount() != 1 || strings.Contains(result.Message, "secret-provider-text") {
				t.Fatal(result, err, m.writeCount())
			}
			if _, err := s.SubmitTransfer(context.Background(), p.ID); err == nil || m.writeCount() != 1 {
				t.Fatal("uncertain request replayed", err)
			}
			raw, err := os.ReadFile(filepath.Join(s.profile, "hyperliquid-transfers", p.ID+".json"))
			if err != nil || !strings.Contains(string(raw), `"status":"`+test.status+`"`) {
				t.Fatal("result not journaled", err)
			}
		})
	}
}

func TestInternalTransferConcurrentConfirmationDurableCollisionAndNonceFailure(t *testing.T) {
	for _, mode := range []string{"concurrent", "claim-collision", "bad-nonce", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			s, m := transferFixture(t)
			p, err := s.PreviewTransfer(context.Background(), transferRequest())
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "concurrent":
				var pending sync.WaitGroup
				var successes atomic.Int64
				for range 2 {
					pending.Add(1)
					go func() {
						defer pending.Done()
						if r, err := s.SubmitTransfer(context.Background(), p.ID); err == nil && r.Status == "confirmed" {
							successes.Add(1)
						}
					}()
				}
				pending.Wait()
				if successes.Load() != 1 || m.writeCount() != 1 {
					t.Fatal("double click submitted twice", successes.Load(), m.writeCount())
				}
			case "claim-collision":
				if err := claimTransfer(filepath.Join(s.profile, "hyperliquid-transfers", p.ID+".json"), p); err != nil {
					t.Fatal(err)
				}
				if _, err := s.SubmitTransfer(context.Background(), p.ID); err == nil || m.writeCount() != 0 {
					t.Fatal("durable claim replayed", err)
				}
			case "bad-nonce":
				if err := os.WriteFile(filepath.Join(s.profile, "hyperliquid-nonce.json"), []byte("invalid"), 0600); err != nil {
					t.Fatal(err)
				}
				r, err := s.SubmitTransfer(context.Background(), p.ID)
				if err != nil || r.Status != "rejected" || m.writeCount() != 0 {
					t.Fatal(r, err)
				}
			case "cancelled":
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				if _, err := s.SubmitTransfer(ctx, p.ID); err == nil || m.writeCount() != 0 {
					t.Fatal("cancelled transfer sent", err)
				}
			}
		})
	}
}

func TestOrderPreviewRejectsConcurrentFundingReview(t *testing.T) {
	s, m := mockStore(t)
	m.beforeQuote = func() { s.mu.Lock(); s.invalidateOrderPreviewsLocked(); s.mu.Unlock() }
	if _, err := s.Preview(context.Background(), baseRequest()); err == nil || len(s.previews) != 0 {
		t.Fatal("order review survived changed funding state", err)
	}
}

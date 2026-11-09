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
	"testing"
	"time"
)

const testPrivate = "0123456789012345678901234567890123456789012345678901234567890123"
const testAccount = "0x1111111111111111111111111111111111111111"

type tradingMock struct {
	mu                          sync.Mutex
	actions                     []map[string]any
	infos                       []string
	partial, malformed, expired bool
	price                       string
	available                   string
	filledQuantity              string
	maxLeverage                 int
	collateralToken             *int
	spotQuote                   *int
	spotMetaFailure             bool
	beforeQuote                 func()
	quoteTime                   *int64
	accountMode                 string
	withdrawable                string
	accountTime                 *int64
	positions                   json.RawMessage
	marginTableID               *int
	marginTables                json.RawMessage
	metricsFailure              bool
	spotBalances                json.RawMessage
	beforeAccount               func()
	requests                    []map[string]json.RawMessage
}

func mockStore(t *testing.T) (*Store, *tradingMock) {
	t.Helper()
	mock := &tradingMock{price: "100", available: "2", filledQuantity: "1", maxLeverage: 20, accountMode: "disabled", withdrawable: "1000", positions: json.RawMessage(`[]`), marginTableID: intPointer(20)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		mock.mu.Lock()
		defer mock.mu.Unlock()
		mock.requests = append(mock.requests, input)
		if r.URL.Path == "/exchange" {
			var action map[string]any
			_ = json.Unmarshal(input["action"], &action)
			mock.actions = append(mock.actions, action)
			if action["type"] == "updateLeverage" {
				fmt.Fprint(w, `{"status":"ok","response":{"type":"default"}}`)
				return
			}
			if mock.malformed {
				fmt.Fprint(w, `{"status":"ok","response":{}}`)
				return
			}
			orders := action["orders"].([]any)
			statuses := []any{map[string]any{"filled": map[string]any{"oid": 101, "totalSz": mock.filledQuantity, "avgPx": "100"}}}
			for i := 1; i < len(orders); i++ {
				if mock.partial && i == len(orders)-1 {
					statuses = append(statuses, map[string]any{"error": "synthetic protective rejection"})
				} else {
					statuses = append(statuses, "waitingForFill")
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "response": map[string]any{"type": "order", "data": map[string]any{"statuses": statuses}}})
			return
		}
		var kind string
		_ = json.Unmarshal(input["type"], &kind)
		mock.infos = append(mock.infos, kind)
		switch kind {
		case "userRole":
			fmt.Fprintf(w, `{"role":"agent","data":{"user":%q}}`, testAccount)
		case "extraAgents":
			valid := time.Now().Add(time.Hour).UnixMilli()
			if mock.expired {
				valid = 1
			}
			key, _ := keyFromString(testPrivate)
			fmt.Fprintf(w, `[{"address":%q,"validUntil":%d}]`, addressFor(key), valid)
		case "perpDexs":
			fmt.Fprint(w, `[null,{"name":"xyz"}]`)
		case "meta":
			var dex string
			_ = json.Unmarshal(input["dex"], &dex)
			name := "BTC"
			if dex != "" {
				name = dex + ":" + name
			}
			item := map[string]any{"name": name, "szDecimals": 3, "maxLeverage": mock.maxLeverage}
			if mock.marginTableID != nil {
				item["marginTableId"] = *mock.marginTableID
			}
			meta := map[string]any{"universe": []any{item}}
			if mock.marginTables != nil {
				meta["marginTables"] = mock.marginTables
			}
			if mock.collateralToken != nil {
				meta["collateralToken"] = *mock.collateralToken
			}
			_ = json.NewEncoder(w).Encode(meta)
		case "spotMeta":
			if mock.spotMetaFailure {
				w.WriteHeader(503)
				return
			}
			quote := 0
			if mock.spotQuote != nil {
				quote = *mock.spotQuote
			}
			fmt.Fprintf(w, `{"tokens":[{"index":150,"name":"HYPE","szDecimals":2},{"index":0,"name":"USDC","szDecimals":6},{"index":1,"name":"USDT","szDecimals":6},{"index":360,"name":"USDH","szDecimals":6}],"universe":[{"name":"@107","index":107,"tokens":[150,%d]}]}`, quote)
		case "userAbstraction":
			if mock.metricsFailure {
				w.WriteHeader(503)
				return
			}
			_ = json.NewEncoder(w).Encode(mock.accountMode)
		case "clearinghouseState":
			if mock.beforeAccount != nil {
				mock.beforeAccount()
			}
			if mock.metricsFailure {
				w.WriteHeader(503)
				return
			}
			observed := time.Now().UnixMilli()
			if mock.accountTime != nil {
				observed = *mock.accountTime
			}
			fmt.Fprintf(w, `{"time":%d,"withdrawable":%q,"assetPositions":%s,"marginSummary":{"accountValue":"1000","totalRawUsd":"1000","totalMarginUsed":"0"}}`, observed, mock.withdrawable, mock.positions)
		case "spotClearinghouseState":
			if mock.spotBalances != nil {
				fmt.Fprintf(w, `{"balances":%s}`, mock.spotBalances)
				return
			}
			fmt.Fprintf(w, `{"balances":[{"token":150,"total":%q,"hold":"0.5"}]}`, mock.available)
		case "l2Book":
			var coin string
			_ = json.Unmarshal(input["coin"], &coin)
			if mock.beforeQuote != nil {
				mock.beforeQuote()
			}
			observedAt := time.Now().UnixMilli()
			if mock.quoteTime != nil {
				observedAt = *mock.quoteTime
			}
			fmt.Fprintf(w, `{"coin":%q,"time":%d,"levels":[[{"px":%q}],[{"px":%q}]]}`, coin, observedAt, mock.price, mock.price)
		default:
			t.Errorf("unexpected public request: %s", kind)
			w.WriteHeader(400)
		}
	}))
	t.Cleanup(server.Close)
	store := NewStore(t.TempDir())
	store.endpoint = server.URL
	_, err := store.Configure(Settings{AccountAddress: testAccount, PrivateKey: testPrivate, Network: "testnet"})
	if err != nil {
		t.Fatal(err)
	}
	return store, mock
}
func baseRequest() OrderRequest {
	return OrderRequest{Market: "perpetual", Instrument: "BTC", Side: "buy", Quantity: "1", StopLoss: "90", TakeProfit: "110", Leverage: 3}
}
func TestCredentialPrivateStorageReadOnlyTestAndExpiry(t *testing.T) {
	s, m := mockStore(t)
	info, err := s.Test(context.Background())
	if err != nil || !info.Connected || !info.PrivateKeyConfigured {
		t.Fatal(info, err)
	}
	raw, _ := json.Marshal(info)
	if strings.Contains(string(raw), testPrivate) {
		t.Fatal("private key leaked")
	}
	stat, err := os.Stat(filepath.Join(s.profile, "hyperliquid.json"))
	if err != nil || stat.Mode().Perm() != 0600 {
		t.Fatal("settings permissions", err)
	}
	if len(m.actions) != 0 {
		t.Fatal("connection test submitted exchange writes")
	}
	m.expired = true
	if _, err = s.Test(context.Background()); err == nil {
		t.Fatal("expired agent accepted")
	}
	info, err = s.Configure(Settings{AccountAddress: testAccount, Network: "testnet"})
	if err != nil || !info.PrivateKeyConfigured {
		t.Fatal("empty secret did not retain saved credential", info, err)
	}
	if _, err = s.Configure(Settings{AccountAddress: info.AgentAddress, PrivateKey: testPrivate, Network: "testnet"}); err == nil {
		t.Fatal("master wallet private key accepted")
	}
	info, err = s.Configure(Settings{AccountAddress: testAccount, Network: "testnet", ClearPrivateKey: true})
	if err != nil || info.PrivateKeyConfigured {
		t.Fatal("clear credential failed", info, err)
	}
}
func TestPreviewConfirmedBracketWireAndOneUseJournal(t *testing.T) {
	s, m := mockStore(t)
	preview, err := s.Preview(context.Background(), baseRequest())
	if err != nil {
		t.Fatal(err)
	}
	if len(m.actions) != 0 || preview.ReferencePrice != "100" || preview.LimitPrice != "101" {
		t.Fatal("preview performed writes or wrong price", preview)
	}
	result, err := s.Submit(context.Background(), preview.ID)
	if err != nil || result.Status != "filled" || len(result.OrderIDs) != 1 {
		t.Fatal(result, err)
	}
	if len(m.actions) != 2 || m.actions[0]["type"] != "updateLeverage" || m.actions[0]["isCross"] != false {
		t.Fatal(m.actions)
	}
	entry := m.actions[1]
	if entry["grouping"] != "normalTpsl" {
		t.Fatal(entry)
	}
	orders := entry["orders"].([]any)
	if len(orders) != 3 || orders[0].(map[string]any)["a"] != float64(0) || orders[1].(map[string]any)["r"] != true || orders[1].(map[string]any)["b"] != false {
		t.Fatal(orders)
	}
	if _, err = s.Submit(context.Background(), preview.ID); err == nil || len(m.actions) != 2 {
		t.Fatal("confirmation replayed", err)
	}
	raw, err := os.ReadFile(filepath.Join(s.profile, "hyperliquid-orders", preview.ID+".json"))
	if err != nil || strings.Contains(string(raw), testPrivate) || !strings.Contains(string(raw), `"filled"`) {
		t.Fatal("result journal missing or leaked key", err)
	}
}
func TestSubmissionUncertainOrProtectiveFailureCannotReplay(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprint(partial), func(t *testing.T) {
			s, m := mockStore(t)
			m.partial = partial
			m.malformed = !partial
			p, err := s.Preview(context.Background(), baseRequest())
			if err != nil {
				t.Fatal(err)
			}
			r, err := s.Submit(context.Background(), p.ID)
			want := "uncertain"
			if partial {
				want = "partial"
			}
			if err != nil || r.Status != want {
				t.Fatal(r, err)
			}
			if _, err = s.Submit(context.Background(), p.ID); err == nil || len(m.actions) != 2 {
				t.Fatal("ambiguous/partial write repeated")
			}
		})
	}
}
func TestChangedQuotePrecisionAndSpotInventory(t *testing.T) {
	s, m := mockStore(t)
	p, err := s.Preview(context.Background(), baseRequest())
	if err != nil {
		t.Fatal(err)
	}
	m.price = "120"
	if _, err = s.Submit(context.Background(), p.ID); err == nil || len(m.actions) != 0 {
		t.Fatal("moved quote submitted")
	}
	m.price = "100"
	r := baseRequest()
	r.Quantity = "1.0001"
	if _, err = s.Preview(context.Background(), r); err == nil {
		t.Fatal("unsupported size precision")
	}
	if _, err = s.Configure(Settings{AccountAddress: testAccount, PrivateKey: testPrivate, Network: "mainnet"}); err != nil {
		t.Fatal(err)
	}
	r = OrderRequest{Market: "spot", Instrument: "@107", Side: "sell", Quantity: "2", Leverage: 1}
	if _, err = s.Preview(context.Background(), r); err == nil {
		t.Fatal("locked spot balance sold")
	}
	r.Quantity = "1"
	r.Leverage = 0 // disabled spot leverage control uses the default
	p, err = s.Preview(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if p.Request.Leverage != 1 {
		t.Fatal("spot default must be unlevered", p.Request)
	}
	result, err := s.Submit(context.Background(), p.ID)
	if err != nil || result.Status != "filled" || len(m.actions) != 1 {
		t.Fatal(result, err)
	}
	orders := m.actions[0]["orders"].([]any)
	if orders[0].(map[string]any)["a"] != float64(10107) {
		t.Fatal("token index used instead of spot pair index")
	}
	r.StopLoss = "90"
	if _, err = s.Preview(context.Background(), r); err == nil {
		t.Fatal("unsupported spot bracket silently ignored")
	}
}
func TestSettingsFenceExpiryCorruptPersistenceAndDurableNonce(t *testing.T) {
	s, _ := mockStore(t)
	p, err := s.Preview(context.Background(), baseRequest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Configure(Settings{AccountAddress: testAccount, Network: "testnet"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Submit(context.Background(), p.ID); err == nil {
		t.Fatal("old settings confirmation accepted")
	}
	n1, err := s.nextNonce()
	if err != nil {
		t.Fatal(err)
	}
	s2 := NewStore(s.profile)
	n2, err := s2.nextNonce()
	if err != nil || n2 <= n1 {
		t.Fatal("nonce repeated on restart", n1, n2, err)
	}
	if err = os.WriteFile(filepath.Join(s.profile, "hyperliquid.json"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	s3 := NewStore(s.profile)
	if s3.Info().Error == "" {
		t.Fatal("corrupt settings not visible")
	}
	if _, err = s3.Configure(Settings{}); err == nil {
		t.Fatal("corrupt original overwritten")
	}
}

func TestPartialAndEquivalentDecimalFills(t *testing.T) {
	for _, tc := range []struct{ qty, filled, want string }{{"1", "0.5", "partial"}, {"0.1", "0.10", "filled"}, {"1", "0", "uncertain"}, {"1", "2", "uncertain"}, {"1", "invalid", "uncertain"}} {
		t.Run(tc.filled, func(t *testing.T) {
			s, m := mockStore(t)
			m.filledQuantity = tc.filled
			r := baseRequest()
			r.Quantity = tc.qty
			p, err := s.Preview(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			result, err := s.Submit(context.Background(), p.ID)
			if err != nil || result.Status != tc.want {
				t.Fatal(result, err)
			}
		})
	}
}
func TestBuilderAssetOffsetSettingsSerializationAndTestnetSpot(t *testing.T) {
	s, m := mockStore(t)
	r := baseRequest()
	r.Instrument = "xyz:BTC"
	p, err := s.Preview(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Submit(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	if m.actions[0]["asset"] != float64(110000) {
		t.Fatal("wrong HIP3 asset offset", m.actions)
	}
	s.gate <- struct{}{}
	if _, err = s.Configure(Settings{AccountAddress: testAccount, Network: "testnet", ClearPrivateKey: true}); err == nil {
		t.Fatal("settings changed during active submission")
	}
	<-s.gate
	if !s.Info().PrivateKeyConfigured {
		t.Fatal("busy Configure cleared private key")
	}
	r = OrderRequest{Market: "spot", Instrument: "@107", Side: "buy", Quantity: "1", Leverage: 1}
	if _, err = s.Preview(context.Background(), r); err == nil {
		t.Fatal("mainnet native spot ID used on testnet")
	}
}

package hyperliquid

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type balanceFixture struct {
	mu        sync.Mutex
	responses map[string]string
	requests  []map[string]string
	before    func(context.Context, string)
}

func balanceStore(t *testing.T) (*Store, *balanceFixture) {
	t.Helper()
	now := time.UnixMilli(1794268800000)
	state := func(available string) string {
		return fmt.Sprintf(`{"time":%d,"withdrawable":%q,"assetPositions":[],"marginSummary":{"totalRawUsd":"1100","accountValue":"1200","totalMarginUsed":"100"}}`, now.UnixMilli(), available)
	}
	f := &balanceFixture{responses: map[string]string{
		"userAbstraction":          `"disabled"`,
		"perpDexs":                 `[null,{"name":"xyz","fullName":"XYZ"},{"name":"para"},{"name":"other"}]`,
		"allPerpMetas":             `[{"collateralToken":0,"universe":[{"name":"HBAR"}]},{"collateralToken":0,"universe":[{"name":"xyz:NVDA"}]},{"collateralToken":2,"universe":[{"name":"para:BTC"}]},{"collateralToken":3,"universe":[{"name":"other:ETH"}]}]`,
		"spotClearinghouseState":   `{"balances":[{"token":0,"coin":"USDC","total":"25.12345678","hold":"5"},{"token":150,"coin":"HYPE","total":"1.23456789","hold":"0"}]}`,
		"clearinghouseState/":      state("1000"),
		"clearinghouseState/xyz":   state("0"),
		"clearinghouseState/para":  state("250"),
		"clearinghouseState/other": state("35"),
	}}
	var tokens []map[string]any
	for _, token := range []struct {
		index int
		name  string
	}{{0, "USDC"}, {2, "USDT"}, {3, "USDH"}, {150, "HYPE"}} {
		tokens = append(tokens, map[string]any{"index": token.index, "name": token.name, "tokenId": fmt.Sprintf("0x%032x", token.index+1), "szDecimals": 2, "weiDecimals": 8})
	}
	metadata, _ := json.Marshal(map[string]any{"tokens": tokens})
	f.responses["spotMeta"] = string(metadata)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]string
		if r.URL.Path != "/info" || json.NewDecoder(r.Body).Decode(&request) != nil {
			t.Error("balance read attempted an exchange write or malformed request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		key := request["type"]
		if key == "clearinghouseState" {
			key += "/" + request["dex"]
		}
		f.mu.Lock()
		f.requests = append(f.requests, request)
		body, found := f.responses[key]
		before := f.before
		f.mu.Unlock()
		if before != nil {
			before(r.Context(), key)
		}
		if !found {
			t.Error("unexpected account metadata request", key)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if body == "HTTP_ERROR" {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, "private provider token=secret path=/local/profile")
			return
		}
		fmt.Fprint(w, body)
	}))
	t.Cleanup(server.Close)
	store := NewStore(t.TempDir())
	store.endpoint, store.now = server.URL, func() time.Time { return now }
	if _, err := store.Configure(Settings{AccountAddress: testAccount, Network: "mainnet"}); err != nil {
		t.Fatal(err)
	}
	return store, f
}

func balanceVenue(t *testing.T, overview AccountBalances, dex string) VenueBalance {
	t.Helper()
	for _, venue := range overview.Venues {
		if venue.Dex == dex {
			return venue
		}
	}
	t.Fatal("missing venue", dex)
	return VenueBalance{}
}

func balanceCoin(t *testing.T, venue VenueBalance, coin string) TokenBalance {
	t.Helper()
	for _, value := range venue.Balances {
		if value.Coin == coin {
			return value
		}
	}
	t.Fatal("missing venue token", venue.Dex, coin, venue.Error)
	return TokenBalance{}
}

func TestAccountBalancesReadEveryNativeVenueAndRetainDistinctCollateralAndZeros(t *testing.T) {
	store, fixture := balanceStore(t)
	overview, err := store.AccountBalances(context.Background())
	if err != nil || overview.AccountAddress != testAccount || overview.Network != "mainnet" || overview.AccountMode != "disabled" || overview.Shared || len(overview.Venues) != 5 || len(overview.Tokens) != 4 {
		t.Fatal(overview, err)
	}
	for _, test := range []struct{ dex, coin, amount string }{{"", "USDC", "1000"}, {"xyz", "USDC", "0"}, {"para", "USDT", "250"}, {"other", "USDH", "35"}} {
		venue := balanceVenue(t, overview, test.dex)
		value := balanceCoin(t, venue, test.coin)
		if venue.Error != "" || value.Available != test.amount || value.AvailableError != "" || value.TokenID == "" || value.WeiDecimals != 8 || value.ObservedAt != store.now().UnixMilli() {
			t.Fatal("DEX balances or native collateral identities were mixed", venue)
		}
		if value.Total != "1100" || value.Equity != "1200" || value.UsedMargin != "100" || venue.CollateralToken == nil || *venue.CollateralToken != value.Token {
			t.Fatal("missing independent account summary", venue)
		}
	}
	spot := balanceVenue(t, overview, "spot")
	if value := balanceCoin(t, spot, "USDC"); value.Available != "20.12345678" || value.Held != "5" || value.Total != "25.12345678" {
		t.Fatal("spot reservations or native precision were lost", value)
	}
	if value := balanceCoin(t, spot, "HYPE"); value.Available != "1.23456789" || value.Token != 150 {
		t.Fatal("noncollateral spot asset was omitted or rounded", value)
	}
	for _, currency := range []string{"USDT", "USDH"} {
		if value := balanceCoin(t, spot, currency); value.Available != "0" || value.Total != "0" {
			t.Fatal("a complete spot response did not retain verified absent-token zero", value)
		}
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.requests) != 9 {
		t.Fatal("unexpected fanout or duplicate metadata lookup", fixture.requests)
	}
	for _, request := range fixture.requests {
		if strings.Contains(request["type"], "State") || request["type"] == "userAbstraction" {
			if request["user"] != testAccount {
				t.Fatal("read used an agent or another account address", request)
			}
		}
	}
	files, err := os.ReadDir(store.profile)
	if err != nil || len(files) != 1 || files[0].Name() != "hyperliquid.json" || len(store.previews) != 0 {
		t.Fatal("read-only balances wrote action/nonce state", files, err)
	}
}

func TestSharedAccountBalancesAreShownOnlyOnceAndNeverAsFreePerDEXCash(t *testing.T) {
	for _, mode := range []string{"unifiedAccount", "portfolioMargin", "dexAbstraction"} {
		t.Run(mode, func(t *testing.T) {
			store, fixture := balanceStore(t)
			fixture.responses["userAbstraction"] = fmt.Sprintf("%q", mode)
			overview, err := store.AccountBalances(context.Background())
			if err != nil || !overview.Shared || overview.Notice == "" {
				t.Fatal(overview, err)
			}
			for _, venue := range overview.Venues {
				if !venue.Shared || venue.Market == "perpetual" && len(venue.Balances) != 0 {
					t.Fatal("shared funds were duplicated as independent per-DEX balances", venue)
				}
			}
			value := balanceCoin(t, balanceVenue(t, overview, "spot"), "USDC")
			if value.Total != "25.12345678" || value.Available != "" || value.AvailableError == "" {
				t.Fatal("shared reservations were presented as transferable cash", value)
			}
			for _, request := range fixture.requests {
				if request["type"] == "clearinghouseState" {
					t.Fatal("shared account queried meaningless individual DEX cash state")
				}
			}
		})
	}
}

func TestAccountBalancesKeepPartialFailuresAndInvalidAmountsUnavailable(t *testing.T) {
	for _, test := range []struct{ name, key, response string }{
		{"failed DEX", "clearinghouseState/xyz", "HTTP_ERROR"},
		{"missing DEX time", "clearinghouseState/xyz", `{"withdrawable":"0"}`},
		{"stale DEX", "clearinghouseState/xyz", `{"time":1,"withdrawable":"0"}`},
		{"missing spot", "spotClearinghouseState", `{}`},
		{"failed spot", "spotClearinghouseState", "HTTP_ERROR"},
		{"duplicate spot", "spotClearinghouseState", `{"balances":[{"token":0,"total":"10","hold":"0"},{"token":0,"total":"20","hold":"0"}]}`},
		{"missing spot token", "spotClearinghouseState", `{"balances":[{"total":"10","hold":"0"}]}`},
		{"unknown spot token", "spotClearinghouseState", `{"balances":[{"token":999,"total":"10","hold":"0"}]}`},
		{"wrong spot name", "spotClearinghouseState", `{"balances":[{"token":0,"coin":"USDT","total":"10","hold":"0"}]}`},
		{"invalid spot amount", "spotClearinghouseState", `{"balances":[{"token":0,"total":"NaN","hold":"0"}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, fixture := balanceStore(t)
			fixture.responses[test.key] = test.response
			overview, err := store.AccountBalances(context.Background())
			if err != nil {
				t.Fatal("independent venue failed with another venue", err)
			}
			if value := balanceCoin(t, balanceVenue(t, overview, ""), "USDC"); value.Available != "1000" {
				t.Fatal("valid main DEX balance was discarded", value)
			}
			dex := "xyz"
			if test.key == "spotClearinghouseState" {
				dex = "spot"
			}
			venue := balanceVenue(t, overview, dex)
			if venue.Error == "" || len(venue.Balances) != 0 || strings.Contains(venue.Error, "secret") || strings.Contains(venue.Error, "/local") {
				t.Fatal("unavailable data became zero, stale money or raw diagnostics", venue)
			}
		})
	}
	store, fixture := balanceStore(t)
	fixture.responses["clearinghouseState/xyz"] = fmt.Sprintf(`{"time":%d,"withdrawable":"garbage","marginSummary":{"totalRawUsd":"15","accountValue":"20","totalMarginUsed":"5"}}`, store.now().UnixMilli())
	overview, err := store.AccountBalances(context.Background())
	value := balanceCoin(t, balanceVenue(t, overview, "xyz"), "USDC")
	if err != nil || value.Available != "" || value.AvailableError == "" || value.Total != "15" || value.Equity != "20" || value.UsedMargin != "5" {
		t.Fatal("independent summary fields were invented or discarded", value, err)
	}
}

func TestAccountBalanceMetadataCannotConfuseDEXOrTokenIdentity(t *testing.T) {
	for _, test := range []struct{ name, key, response string }{
		{"unknown mode", "userAbstraction", `"newMode"`},
		{"missing catalog", "perpDexs", `[]`},
		{"unidentified main", "perpDexs", `[{"name":"main"}]`},
		{"duplicate DEX", "perpDexs", `[null,{"name":"xyz"},{"name":"xyz"},{"name":"other"}]`},
		{"missing metadata", "allPerpMetas", `[]`},
		{"foreign market", "allPerpMetas", `[{"collateralToken":0,"universe":[{"name":"BTC"}]},{"collateralToken":0,"universe":[{"name":"para:BTC"}]},{"collateralToken":2},{"collateralToken":3}]`},
		{"missing tokens", "spotMeta", `{}`},
		{"missing precision", "spotMeta", `{"tokens":[{"index":0,"name":"USDC","tokenId":"0x00000000000000000000000000000001"}]}`},
		{"duplicate token index", "spotMeta", `{"tokens":[{"index":0,"name":"USDC","tokenId":"0x00000000000000000000000000000001","szDecimals":2,"weiDecimals":8},{"index":0,"name":"USDT","tokenId":"0x00000000000000000000000000000002","szDecimals":2,"weiDecimals":8}]}`},
		{"duplicate token ID", "spotMeta", `{"tokens":[{"index":0,"name":"USDC","tokenId":"0x00000000000000000000000000000001","szDecimals":2,"weiDecimals":8},{"index":2,"name":"USDT","tokenId":"0x00000000000000000000000000000001","szDecimals":2,"weiDecimals":8}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, fixture := balanceStore(t)
			fixture.responses[test.key] = test.response
			if overview, err := store.AccountBalances(context.Background()); err == nil || len(overview.Venues) != 0 {
				t.Fatal("ambiguous metadata returned actionable balances", overview, err)
			}
		})
	}
	store, fixture := balanceStore(t)
	fixture.responses["allPerpMetas"] = `[{}, {"collateralToken":0,"universe":[{"name":"xyz:NVDA"}]},{"collateralToken":2},{"collateralToken":3}]`
	overview, err := store.AccountBalances(context.Background())
	main := balanceVenue(t, overview, "")
	if err != nil || main.CollateralToken != nil || main.Error == "" || len(main.Balances) != 0 {
		t.Fatal("missing core metadata silently inferred USDC collateral", overview, err)
	}
}

func TestAccountBalancesCancellationAndSettingsFence(t *testing.T) {
	for _, change := range []bool{false, true} {
		store, fixture := balanceStore(t)
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		fixture.before = func(ctx context.Context, key string) {
			if key == "clearinghouseState/xyz" {
				once.Do(func() { close(entered) })
				select {
				case <-release:
				case <-ctx.Done():
				}
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() { _, err := store.AccountBalances(ctx); result <- err }()
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("account worker did not reach held request")
		}
		if change {
			if _, err := store.Configure(Settings{AccountAddress: testAccount, Network: "testnet"}); err != nil {
				t.Fatal(err)
			}
		} else {
			cancel()
		}
		close(release)
		select {
		case err := <-result:
			if !change && !errors.Is(err, context.Canceled) || change && (err == nil || !strings.Contains(err.Error(), "settings changed")) {
				t.Fatal("cancelled/previous-account balances escaped their fence", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("cancelled balance workers did not drain")
		}
		cancel()
	}
}

func TestAccountBalancesBoundConcurrentReadsAndDoNotRequireAnAgentKey(t *testing.T) {
	store, fixture := balanceStore(t)
	if store.Info().PrivateKeyConfigured {
		t.Fatal("fixture unexpectedly has a signer")
	}
	release, entered := make(chan struct{}), make(chan struct{}, 5)
	var mu sync.Mutex
	active, maximum := 0, 0
	fixture.before = func(ctx context.Context, key string) {
		if key != "spotClearinghouseState" && !strings.HasPrefix(key, "clearinghouseState/") {
			return
		}
		mu.Lock()
		active++
		maximum = max(active, maximum)
		mu.Unlock()
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-ctx.Done():
		}
		mu.Lock()
		active--
		mu.Unlock()
	}
	result := make(chan error, 1)
	go func() { _, err := store.AccountBalances(context.Background()); result <- err }()
	for range 4 {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("independent venue reads did not overlap")
		}
	}
	mu.Lock()
	if maximum != 4 || active != 4 {
		t.Fatal("unexpected account concurrency", maximum, active)
	}
	mu.Unlock()
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if maximum != 4 || active != 0 {
		t.Fatal("account workers escaped their bound or leaked", maximum, active)
	}
	if got := store.Info(); got.AccountAddress != testAccount || got.PrivateKeyConfigured || !reflect.DeepEqual(store.previews, map[string]prepared{}) {
		t.Fatal("read-only balance lookup changed signing state", got)
	}
}

func TestAccountBalanceGlobalReadGateBoundsOverviewsAndCancelsQueuedReads(t *testing.T) {
	store, fixture := balanceStore(t)
	entered, release := make(chan struct{}, 5), make(chan struct{})
	var countMu sync.Mutex
	active, maximum := 0, 0
	fixture.before = func(ctx context.Context, key string) {
		if key != "spotClearinghouseState" && !strings.HasPrefix(key, "clearinghouseState/") {
			return
		}
		countMu.Lock()
		active++
		maximum = max(maximum, active)
		countMu.Unlock()
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-ctx.Done():
		}
		countMu.Lock()
		active--
		countMu.Unlock()
	}
	first := make(chan error, 1)
	go func() { _, err := store.AccountBalances(context.Background()); first <- err }()
	for range 4 {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("first overview did not occupy its bounded workers")
		}
	}
	fixture.mu.Lock()
	before := len(fixture.requests)
	fixture.mu.Unlock()
	queued, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := store.AccountBalances(queued); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("a queued overview ignored its deadline", err)
	}
	fixture.mu.Lock()
	after := len(fixture.requests)
	fixture.mu.Unlock()
	countMu.Lock()
	if maximum != 4 || active != 4 || before != after {
		t.Fatal("queued overview multiplied account/metadata fanout", maximum, active, before, after)
	}
	countMu.Unlock()
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	fixture.mu.Lock()
	fixture.before = nil
	fixture.mu.Unlock()
	if _, err := store.AccountBalances(context.Background()); err != nil {
		t.Fatal("cancelled queue waiter retained the global read gate", err)
	}
}

func TestAccountBalancesReadGateIsIndependentOfTransferSigningGate(t *testing.T) {
	store, _ := balanceStore(t)
	store.gate <- struct{}{}
	defer func() { <-store.gate }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := store.AccountBalances(ctx); err != nil {
		t.Fatal("a signed transfer cannot revalidate balances while holding its signing gate", err)
	}
}

// Opt-in public metadata verification, never an account read, signer approval
// check, balance request, or exchange action. Ordinary CI stays offline.
func TestAccountBalancePublicMetadataSmoke(t *testing.T) {
	if os.Getenv("CRYPTODESK_TEST_BALANCE_METADATA") != "1" {
		t.Skip("set CRYPTODESK_TEST_BALANCE_METADATA=1 to verify current public Hyperliquid token/DEX metadata")
	}
	store := NewStore(t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var tokens balanceSpotMeta
	var dexs []*balanceDEX
	var metas []balanceMeta
	for _, request := range []struct {
		kind string
		out  any
	}{{"spotMeta", &tokens}, {"perpDexs", &dexs}, {"allPerpMetas", &metas}} {
		if err := store.post(ctx, Settings{Network: "mainnet"}, "/info", map[string]string{"type": request.kind}, request.out); err != nil {
			t.Fatal(request.kind, err)
		}
	}
	verified, err := balanceTokens(tokens)
	if err != nil {
		if tokens.Tokens != nil {
			for _, token := range *tokens.Tokens {
				if _, singleErr := balanceTokens(balanceSpotMeta{Tokens: &[]balanceSpotToken{token}}); singleErr != nil {
					t.Logf("rejected public token metadata name=%q", token.Name)
				}
			}
		}
		t.Fatal(err)
	}
	if len(dexs) == 0 || len(dexs) > balanceDEXLimit || dexs[0] != nil || len(metas) != len(dexs) {
		t.Fatal("public DEX metadata shape/bounds changed", len(dexs), len(metas))
	}
	for i, meta := range metas {
		if meta.CollateralToken == nil {
			t.Fatal("public DEX collateral token is missing", i)
		}
		if _, exists := verified[*meta.CollateralToken]; !exists {
			t.Fatal("public DEX collateral token is absent from spot metadata", i, *meta.CollateralToken)
		}
		for _, market := range meta.Universe {
			prefix, _, qualified := strings.Cut(market.Name, ":")
			if i == 0 && qualified || i > 0 && (dexs[i] == nil || !qualified || prefix != dexs[i].Name) {
				t.Fatal("public perpetual metadata does not align with perpDexs", i)
			}
		}
	}
	t.Logf("verified public metadata: %d tokens, %d DEXs; no account queries or writes", len(verified), len(dexs))
}

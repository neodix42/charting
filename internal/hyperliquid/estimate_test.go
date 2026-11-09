package hyperliquid

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func intPointer(value int) *int { return &value }

func TestEstimateActualSettlementCurrencyAndAssetLeverageWithoutCredentials(t *testing.T) {
	for _, tc := range []struct {
		name, native, currency string
		token, maximum         int
	}{
		{"native USDC settlement with USDT oracle", "BTC", "USDC", 0, 40},
		{"actual USDT collateral", "BTC", "USDT", 1, 25},
		{"builder USDH collateral", "xyz:BTC", "USDH", 360, 12},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configured, mock := mockStore(t)
			mock.collateralToken, mock.maxLeverage = intPointer(tc.token), tc.maximum
			store := NewStore(t.TempDir())
			store.endpoint = configured.endpoint
			for _, side := range []string{"buy", "sell"} {
				value, err := store.Estimate(context.Background(), "perpetual", tc.native, side)
				if err != nil || value.ReferencePrice != "100" || value.AccountCurrency != tc.currency || value.ProfitPerPriceUnit != "1" || value.MaxLeverage != tc.maximum || value.ValuationTime < time.Now().Add(-15*time.Second).UnixMilli() || value.ValuationError != "" {
					t.Fatal(value, err)
				}
			}
			if len(store.previews) != 0 || store.Info().PrivateKeyConfigured || store.Info().Connected || len(mock.actions) != 0 {
				t.Fatal("public estimate created trading state or used credentials")
			}
			files, err := os.ReadDir(store.profile)
			if err != nil || len(files) != 0 {
				t.Fatal("estimate wrote profile/nonce/order files", files, err)
			}
			for _, kind := range mock.infos {
				if kind != "meta" && kind != "perpDexs" && kind != "spotMeta" && kind != "l2Book" {
					t.Fatalf("estimate made an account/private request: %s", kind)
				}
			}
		})
	}
}

func TestSpotEstimateUsesPairQuoteTokenAndOneTimesLeverage(t *testing.T) {
	store, mock := mockStore(t)
	_, err := store.Configure(Settings{Network: "mainnet"})
	if err != nil {
		t.Fatal(err)
	}
	mock.spotQuote = intPointer(1) // The base remains HYPE; its quote is USDT.
	value, err := store.Estimate(context.Background(), "spot", "@107", "sell")
	if err != nil || value.AccountCurrency != "USDT" || value.ProfitPerPriceUnit != "1" || value.MaxLeverage != 1 || value.ReferencePrice != "100" {
		t.Fatal(value, err)
	}
	if !reflect.DeepEqual(mock.infos, []string{"spotMeta", "l2Book"}) {
		t.Fatal("spot estimate queried account balances or approval", mock.infos)
	}
}

func TestMissingCollateralValuationIsExplicitAndDoesNotBlockPreview(t *testing.T) {
	for _, mode := range []string{"missing field", "unknown token", "lookup unavailable"} {
		t.Run(mode, func(t *testing.T) {
			store, mock := mockStore(t)
			mock.maxLeverage = 17
			if mode == "unknown token" {
				mock.collateralToken = intPointer(9999)
			}
			if mode == "lookup unavailable" {
				mock.collateralToken, mock.spotMetaFailure = intPointer(0), true
			}
			value, err := store.Estimate(context.Background(), "perpetual", "BTC", "buy")
			if err != nil || value.ReferencePrice != "100" || value.MaxLeverage != 17 || value.AccountCurrency != "" || value.ProfitPerPriceUnit != "" || value.ValuationError == "" {
				t.Fatal("unknown collateral guessed or valid quote/cap lost", value, err)
			}
			preview, err := store.Preview(context.Background(), baseRequest())
			if err != nil || preview.AccountCurrency != "" || preview.ProfitPerPriceUnit != "" || preview.ValuationError == "" || preview.ValuationTime <= 0 || preview.MaxLeverage != 17 {
				t.Fatal("missing valuation blocked valid preview or fabricated rate", preview, err)
			}
			result, err := store.Submit(context.Background(), preview.ID)
			if err != nil || result.Status != "filled" {
				t.Fatal("unavailable valuation blocked otherwise valid confirmed trade", result, err)
			}
		})
	}
}

func TestPreviewValuationAndSubmitRecheckChangedAssetMaximum(t *testing.T) {
	store, mock := mockStore(t)
	mock.collateralToken, mock.maxLeverage = intPointer(360), 12
	r := baseRequest()
	r.Instrument, r.Leverage = "xyz:BTC", 10
	preview, err := store.Preview(context.Background(), r)
	if err != nil || preview.MaxLeverage != 12 || preview.AccountCurrency != "USDH" || preview.ProfitPerPriceUnit != "1" || preview.ValuationError != "" {
		t.Fatal(preview, err)
	}
	mock.maxLeverage = 5
	if _, err = store.Submit(context.Background(), preview.ID); err == nil || !strings.Contains(err.Error(), "limit changed to 5") || len(mock.actions) != 0 {
		t.Fatal("stale leverage reached signed exchange action", err, mock.actions)
	}
	if _, err := store.Preview(context.Background(), r); err == nil {
		t.Fatal("preview accepted above refreshed maximum")
	}
}

func TestEstimateFreshnessSettingsFenceAndMetadataBound(t *testing.T) {
	store, mock := mockStore(t)
	stale := time.Now().Add(-time.Minute).UnixMilli()
	mock.quoteTime = &stale
	if _, err := store.Estimate(context.Background(), "perpetual", "BTC", "buy"); err == nil {
		t.Fatal("stale quote accepted")
	}
	mock.quoteTime, mock.maxLeverage = nil, 1001
	if _, err := store.Estimate(context.Background(), "perpetual", "BTC", "buy"); err == nil {
		t.Fatal("unbounded leverage metadata accepted")
	}
	mock.maxLeverage = 20
	started, release := make(chan struct{}), make(chan struct{})
	mock.beforeQuote = func() { close(started); <-release }
	done := make(chan error, 1)
	go func() { _, err := store.Estimate(context.Background(), "perpetual", "BTC", "sell"); done <- err }()
	<-started
	if _, err := store.Configure(Settings{Network: "mainnet"}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err == nil || !strings.Contains(err.Error(), "settings changed") {
		t.Fatal("late estimate crossed settings/account/network fence", err)
	}
}

func TestTokenLookupRejectsAmbiguityInsteadOfGuessingDollarCurrency(t *testing.T) {
	for _, tokens := range [][]spotToken{
		{{Index: intPointer(0), Name: "USDC"}, {Index: intPointer(0), Name: "USDT"}},
		{{Index: nil, Name: "USDC"}},
		{{Index: intPointer(0), Name: ""}},
	} {
		if name, problem := tokenCurrency(tokens, 0); name != "" || problem == "" {
			t.Fatal("ambiguous collateral accepted", name, problem)
		}
	}
}

func TestLiveHyperliquidValuationReadOnly(t *testing.T) {
	if os.Getenv("CRYPTODESK_LIVE_HYPERLIQUID_VALUATION") != "1" {
		t.Skip("explicit opt-in only: public metadata/book; no account, credentials, signatures or profile writes")
	}
	store := NewStore(t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	value, err := store.Estimate(ctx, "perpetual", "BTC", "buy")
	if err != nil || value.ReferencePrice == "" || value.AccountCurrency == "" || value.ProfitPerPriceUnit != "1" || value.ValuationError != "" || value.MaxLeverage < 1 || value.MaxLeverage > 1000 {
		t.Fatal(value, err)
	}
	files, err := os.ReadDir(store.profile)
	if err != nil || len(files) != 0 || len(store.previews) != 0 {
		t.Fatal("public valuation created profile or order state", files, err)
	}
	t.Logf("actual BTC estimate: entry=%s collateral=%s factor=%s maximum=%dx observed=%s", value.ReferencePrice, value.AccountCurrency, value.ProfitPerPriceUnit, value.MaxLeverage, time.UnixMilli(value.ValuationTime).UTC().Format(time.RFC3339))
}

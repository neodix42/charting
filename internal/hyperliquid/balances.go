package hyperliquid

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/shopspring/decimal"
)

// AccountBalances is a read-only account snapshot. DEX identifiers are the
// exchange's native names: "" is core perps and "spot" is the spot ledger.
type AccountBalances struct {
	AccountAddress string         `json:"accountAddress"`
	Network        string         `json:"network"`
	AccountMode    string         `json:"accountMode"`
	Shared         bool           `json:"shared"`
	ObservedAt     int64          `json:"observedAt"`
	Venues         []VenueBalance `json:"venues"`
	Tokens         []TokenBalance `json:"tokens"`
	Notice         string         `json:"notice,omitempty"`
}

type VenueBalance struct {
	Dex             string         `json:"dex"`
	Name            string         `json:"name"`
	Market          string         `json:"market"`
	CollateralToken *int           `json:"collateralToken,omitempty"`
	Balances        []TokenBalance `json:"balances"`
	Shared          bool           `json:"shared"`
	Error           string         `json:"error,omitempty"`
}

// Token identity and native transfer precision are retained alongside amounts.
// An empty amount is unavailable; a confirmed zero remains the string "0".
type TokenBalance struct {
	Token          int    `json:"token"`
	Coin           string `json:"coin"`
	TokenID        string `json:"tokenId"`
	SzDecimals     int    `json:"szDecimals"`
	WeiDecimals    int    `json:"weiDecimals"`
	Total          string `json:"total,omitempty"`
	Held           string `json:"held,omitempty"`
	Available      string `json:"available,omitempty"`
	Equity         string `json:"equity,omitempty"`
	UsedMargin     string `json:"usedMargin,omitempty"`
	ObservedAt     int64  `json:"observedAt"`
	Error          string `json:"error,omitempty"`
	AvailableError string `json:"availableError,omitempty"`
	Basis          string `json:"basis,omitempty"`
}

const balanceDEXLimit = 64

var balanceTokenID = regexp.MustCompile(`^0x[0-9a-fA-F]{32}$`)

type balanceDEX struct {
	Name     string `json:"name"`
	FullName string `json:"fullName"`
}

type balanceMeta struct {
	CollateralToken *int `json:"collateralToken"`
	Universe        []struct {
		Name string `json:"name"`
	} `json:"universe"`
}

type balanceSpotMeta struct {
	Tokens *[]balanceSpotToken `json:"tokens"`
}

type balanceSpotToken struct {
	Index       *int   `json:"index"`
	Name        string `json:"name"`
	TokenID     string `json:"tokenId"`
	SzDecimals  *int   `json:"szDecimals"`
	WeiDecimals *int   `json:"weiDecimals"`
}

type balanceSpotState struct {
	Time     *int64 `json:"time"`
	Balances *[]struct {
		Token *int   `json:"token"`
		Coin  string `json:"coin"`
		Total string `json:"total"`
		Hold  string `json:"hold"`
	} `json:"balances"`
}

// AccountBalances never signs, transfers, creates an intent, or writes a nonce.
// It reads all currently discovered venues with four bounded account workers.
// The complete operation has a deadline and settings/identity fence.
func (s *Store) AccountBalances(parent context.Context) (AccountBalances, error) {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	// This is independent of the signing gate, which SubmitTransfer already
	// holds when it requests a fresh, uncached account revalidation.
	s.mu.Lock()
	if s.balanceReadGate == nil {
		s.balanceReadGate = make(chan struct{}, 1)
	}
	gate := s.balanceReadGate
	s.mu.Unlock()
	select {
	case gate <- struct{}{}:
		defer func() { <-gate }()
	case <-ctx.Done():
		return AccountBalances{}, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return AccountBalances{}, err
	}
	s.mu.Lock()
	v, problem := s.settings, s.lastError
	s.mu.Unlock()
	if problem != "" {
		return AccountBalances{}, errors.New(problem)
	}
	if !addressPattern.MatchString(v.AccountAddress) {
		return AccountBalances{}, errors.New("Configure the Hyperliquid trading account address to retrieve balances")
	}
	var mode string
	var dexs []*balanceDEX
	var metas []balanceMeta
	var spotMeta balanceSpotMeta
	requests := []func() error{
		func() error {
			return s.post(ctx, v, "/info", map[string]string{"type": "userAbstraction", "user": v.AccountAddress}, &mode)
		},
		func() error { return s.post(ctx, v, "/info", map[string]string{"type": "perpDexs"}, &dexs) },
		func() error { return s.post(ctx, v, "/info", map[string]string{"type": "allPerpMetas"}, &metas) },
		func() error { return s.post(ctx, v, "/info", map[string]string{"type": "spotMeta"}, &spotMeta) },
	}
	var pending sync.WaitGroup
	problems := make([]error, len(requests))
	for i, request := range requests {
		pending.Add(1)
		go func(i int, request func() error) { defer pending.Done(); problems[i] = request() }(i, request)
	}
	pending.Wait()
	if err := parent.Err(); err != nil {
		return AccountBalances{}, err
	}
	for _, err := range problems {
		if err != nil {
			return AccountBalances{}, errors.New("Hyperliquid account mode or venue/token metadata is unavailable; refresh balances")
		}
	}
	shared := mode == "unifiedAccount" || mode == "portfolioMargin" || mode == "dexAbstraction"
	if mode != "disabled" && !shared {
		return AccountBalances{}, errors.New("Hyperliquid returned an unknown account mode; balances are unavailable")
	}
	if len(dexs) == 0 || len(dexs) > balanceDEXLimit || dexs[0] != nil || len(metas) != len(dexs) {
		return AccountBalances{}, errors.New("Hyperliquid DEX metadata is incomplete or exceeds the supported balance-read bound")
	}
	tokens, err := balanceTokens(spotMeta)
	if err != nil {
		return AccountBalances{}, err
	}
	out := AccountBalances{AccountAddress: v.AccountAddress, Network: v.Network, AccountMode: mode, Shared: shared,
		Venues: []VenueBalance{}, Tokens: []TokenBalance{}}
	needed := map[int]bool{}
	seen := map[string]bool{"": true, "spot": true}
	for i, dex := range dexs {
		venue := VenueBalance{Market: "perpetual", Name: "Main perpetuals", Balances: []TokenBalance{}, Shared: shared}
		if i > 0 {
			if dex == nil || !validBalanceName(dex.Name, 64) || strings.ContainsAny(dex.Name, ": \t") || seen[dex.Name] {
				return AccountBalances{}, errors.New("Hyperliquid DEX identities are missing or ambiguous")
			}
			venue.Dex, venue.Name = dex.Name, dex.Name+" perpetuals"
			seen[dex.Name] = true
		}
		meta := metas[i]
		for _, item := range meta.Universe {
			prefix, _, qualified := strings.Cut(item.Name, ":")
			if !validBalanceName(item.Name, 128) || (i == 0 && qualified) || (i > 0 && (!qualified || prefix != venue.Dex)) {
				return AccountBalances{}, errors.New("Hyperliquid perpetual metadata does not match its DEX identity")
			}
		}
		if meta.CollateralToken == nil {
			venue.Error = "The DEX collateral token is not identified by exchange metadata"
		} else if _, ok := tokens[*meta.CollateralToken]; !ok {
			venue.Error = "The DEX collateral token metadata is unavailable"
		} else {
			index := *meta.CollateralToken
			venue.CollateralToken = &index
			needed[index] = true
		}
		out.Venues = append(out.Venues, venue)
	}
	spot := len(out.Venues)
	out.Venues = append(out.Venues, VenueBalance{Dex: "spot", Name: "Spot", Market: "spot", Shared: shared, Balances: []TokenBalance{}})
	if shared {
		out.Notice = "This account shares collateral. Token holdings are shown once in Spot; separate DEX cash balances and free trading margin are not inferred."
	}
	var spotState balanceSpotState
	var spotErr error
	var spotAt int64
	jobs := make(chan int)
	for range 4 {
		pending.Add(1)
		go func() {
			defer pending.Done()
			for index := range jobs {
				if index == spot {
					spotErr = s.post(ctx, v, "/info", map[string]string{"type": "spotClearinghouseState", "user": v.AccountAddress}, &spotState)
					spotAt = s.now().UnixMilli()
				} else {
					venue := &out.Venues[index]
					var state clearinghouseState
					err := s.post(ctx, v, "/info", map[string]string{"type": "clearinghouseState", "user": v.AccountAddress, "dex": venue.Dex}, &state)
					s.fillPerpBalance(venue, state, err, tokens)
				}
			}
		}()
	}
	// Spot goes first so it cannot sit behind a long DEX queue. Shared modes
	// deliberately never read/report per-DEX states as independent cash.
	jobs <- spot
	if !shared {
		for i := 0; i < spot; i++ {
			if out.Venues[i].Error == "" {
				jobs <- i
			}
		}
	}
	close(jobs)
	pending.Wait()
	if err := parent.Err(); err != nil {
		return AccountBalances{}, err
	}
	s.fillSpotBalances(&out.Venues[spot], spotState, spotAt, spotErr, tokens, needed, shared)
	for i := range out.Venues {
		venue := &out.Venues[i]
		if len(venue.Balances) > 0 && !s.freshObservation(venue.Balances[0].ObservedAt) {
			venue.Balances = []TokenBalance{}
			venue.Error = "The account balance snapshot expired while other venues were read; refresh balances"
		}
	}
	indices := make([]int, 0, len(needed))
	for index := range needed {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	for _, index := range indices {
		out.Tokens = append(out.Tokens, tokens[index])
	}
	out.ObservedAt = s.now().UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settings != v {
		return AccountBalances{}, errors.New("Hyperliquid settings changed; refresh account balances")
	}
	return out, nil
}

func validBalanceName(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value && strings.IndexFunc(value, unicode.IsControl) < 0
}

func balanceTokens(meta balanceSpotMeta) (map[int]TokenBalance, error) {
	if meta.Tokens == nil || len(*meta.Tokens) == 0 || len(*meta.Tokens) > 20000 {
		return nil, errors.New("Hyperliquid token metadata is unavailable or exceeds its safe bound")
	}
	tokens, identifiers := map[int]TokenBalance{}, map[string]bool{}
	for _, item := range *meta.Tokens {
		if item.Index == nil || *item.Index < 0 || !validBalanceName(item.Name, 128) || strings.Contains(item.Name, ":") ||
			!balanceTokenID.MatchString(item.TokenID) || item.SzDecimals == nil || item.WeiDecimals == nil ||
			*item.SzDecimals < 0 || *item.SzDecimals > 8 || *item.WeiDecimals < *item.SzDecimals || *item.WeiDecimals > 18 {
			return nil, errors.New("Hyperliquid token identities or transfer precision are incomplete")
		}
		id := strings.ToLower(item.TokenID)
		if _, exists := tokens[*item.Index]; exists || identifiers[id] {
			return nil, errors.New("Hyperliquid returned duplicate token identities")
		}
		identifiers[id] = true
		tokens[*item.Index] = TokenBalance{Token: *item.Index, Coin: item.Name, TokenID: id,
			SzDecimals: *item.SzDecimals, WeiDecimals: *item.WeiDecimals}
	}
	return tokens, nil
}

func (s *Store) fillPerpBalance(venue *VenueBalance, state clearinghouseState, requestError error, tokens map[int]TokenBalance) {
	if requestError != nil {
		venue.Error = "The perpetual DEX balance is unavailable"
		return
	}
	if state.Time == nil || !s.freshObservation(*state.Time) {
		venue.Error = "The perpetual DEX balance is stale or has no timestamp"
		return
	}
	value := tokens[*venue.CollateralToken]
	value.ObservedAt = *state.Time
	value.Basis = "Withdrawable collateral in this perpetual DEX"
	if amount, ok := metricNumber(state.Withdrawable, false); ok {
		value.Available = amount.String()
	} else {
		value.AvailableError = "The DEX did not provide a valid withdrawable collateral balance"
	}
	if amount, ok := metricNumber(state.MarginSummary.TotalRawUSD, true); ok {
		value.Total = amount.String()
	}
	if amount, ok := metricNumber(state.MarginSummary.AccountValue, true); ok {
		value.Equity = amount.String()
	}
	if amount, ok := metricNumber(state.MarginSummary.TotalMarginUsed, false); ok {
		value.UsedMargin = amount.String()
	}
	venue.Balances = []TokenBalance{value}
}

func (s *Store) fillSpotBalances(venue *VenueBalance, state balanceSpotState, observedAt int64, requestError error, tokens map[int]TokenBalance, needed map[int]bool, shared bool) {
	if requestError != nil || state.Balances == nil {
		venue.Error = "Spot token balances are unavailable"
		return
	}
	if !s.freshObservation(observedAt) || state.Time != nil && !s.freshObservation(*state.Time) {
		venue.Error = "Spot token balances are stale; refresh balances"
		return
	}
	rows := map[int]TokenBalance{}
	for _, item := range *state.Balances {
		if item.Token == nil {
			venue.Error = "Spot token balance identities are incomplete"
			return
		}
		value, known := tokens[*item.Token]
		_, duplicate := rows[*item.Token]
		if !known || duplicate || item.Coin != "" && item.Coin != value.Coin {
			venue.Error = "Spot token balance identities are missing or ambiguous"
			return
		}
		total, ok := metricNumber(item.Total, true)
		held, heldOK := metricNumber(item.Hold, false)
		if !ok || !heldOK {
			venue.Error = "Spot token balance amounts are invalid"
			return
		}
		value.Total, value.Held, value.ObservedAt = total.String(), held.String(), observedAt
		setSpotAvailability(&value, total, held, shared)
		rows[value.Token], needed[value.Token] = value, true
	}
	// A complete, valid spot response proves that an omitted known collateral
	// token has zero spot holdings. A missing/invalid response never proves zero.
	for index := range needed {
		if _, exists := rows[index]; !exists {
			value := tokens[index]
			value.Total, value.Held, value.ObservedAt = "0", "0", observedAt
			setSpotAvailability(&value, decimal.Zero, decimal.Zero, shared)
			rows[index] = value
		}
	}
	indices := make([]int, 0, len(rows))
	for index := range rows {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	for _, index := range indices {
		venue.Balances = append(venue.Balances, rows[index])
	}
}

func setSpotAvailability(value *TokenBalance, total, held decimal.Decimal, shared bool) {
	value.Basis = "Spot tokens: total minus held orders"
	if shared {
		value.Basis = "Shared collateral token holdings"
		value.AvailableError = "Shared margin and borrowing reservations are not resolved; holdings are not a verified transferable balance"
	} else if total.LessThan(held) {
		value.AvailableError = "Held tokens exceed the token balance"
	} else {
		value.Available = total.Sub(held).String()
	}
}

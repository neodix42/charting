package hyperliquid

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shopspring/decimal"
)

// No destination address or network is accepted from the webview. Transfers
// always stay within the configured account, using its approved API wallet.
type TransferRequest struct {
	SourceDex      string `json:"sourceDex"`
	DestinationDex string `json:"destinationDex"`
	Token          int    `json:"token"`
	Amount         string `json:"amount"`
}

type TransferPreview struct {
	ID              string   `json:"id"`
	SourceDex       string   `json:"sourceDex"`
	SourceName      string   `json:"sourceName"`
	DestinationDex  string   `json:"destinationDex"`
	DestinationName string   `json:"destinationName"`
	Token           int      `json:"token"`
	Coin            string   `json:"coin"`
	TokenID         string   `json:"tokenId"`
	Amount          string   `json:"amount"`
	AvailableBefore string   `json:"availableBefore"`
	AvailableAfter  string   `json:"availableAfter"`
	ObservedAt      int64    `json:"observedAt"`
	ExpiresAt       int64    `json:"expiresAt"`
	AccountAddress  string   `json:"accountAddress"`
	Network         string   `json:"network"`
	Warnings        []string `json:"warnings"`
}

type TransferResult struct {
	ID      string `json:"id"`
	Status  string `json:"status"` // confirmed, rejected or uncertain
	Message string `json:"message"`
}

type transferTokenIdentity struct {
	Token, WeiDecimals int
	Coin, TokenID      string
}

type preparedTransfer struct {
	TransferPreview
	settings Settings
	token    transferTokenIdentity
}

type checkedTransfer struct {
	identity            transferTokenIdentity
	source, destination VenueBalance
	available, amount   decimal.Decimal
	observedAt          int64
}

func validTransferDex(dex string) bool {
	if len(dex) > 64 || !utf8.ValidString(dex) {
		return false
	}
	for _, r := range dex {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func transferInput(r TransferRequest) (decimal.Decimal, error) {
	if !validTransferDex(r.SourceDex) || !validTransferDex(r.DestinationDex) || r.SourceDex == r.DestinationDex || r.Token < 0 {
		return decimal.Zero, errors.New("Choose distinct available source and destination balances and a valid token")
	}
	// Ordinary decimal strings only: no exponent, sign, whitespace or silently
	// rounded amounts. Canonical decimal serialization happens after validation.
	if len(r.Amount) == 0 || len(r.Amount) > 64 || strings.TrimSpace(r.Amount) != r.Amount {
		return decimal.Zero, errors.New("Enter a positive ordinary decimal transfer amount")
	}
	for _, c := range r.Amount {
		if !(c >= '0' && c <= '9' || c == '.') {
			return decimal.Zero, errors.New("Enter a positive ordinary decimal transfer amount")
		}
	}
	amount, err := positive(r.Amount)
	if err != nil {
		return decimal.Zero, errors.New("Enter a positive ordinary decimal transfer amount")
	}
	return amount, nil
}

func (s *Store) validateTransferBalances(v Settings, r TransferRequest, state AccountBalances) (checkedTransfer, error) {
	amount, err := transferInput(r)
	if err != nil {
		return checkedTransfer{}, err
	}
	if state.AccountAddress != v.AccountAddress || state.Network != v.Network {
		return checkedTransfer{}, errors.New("Hyperliquid account or network changed; review the transfer again")
	}
	if state.Shared || state.AccountMode != "disabled" {
		return checkedTransfer{}, errors.New("Internal balance transfers require Manual / Standard account mode; shared-collateral balances cannot be transferred independently")
	}
	var source, destination *VenueBalance
	for i := range state.Venues {
		venue := &state.Venues[i]
		if venue.Dex == r.SourceDex {
			if source != nil {
				return checkedTransfer{}, errors.New("Duplicate source balance identity")
			}
			source = venue
		}
		if venue.Dex == r.DestinationDex {
			if destination != nil {
				return checkedTransfer{}, errors.New("Duplicate destination balance identity")
			}
			destination = venue
		}
	}
	if source == nil || destination == nil {
		return checkedTransfer{}, errors.New("The selected Hyperliquid balance is unavailable on this network")
	}
	var identity *transferTokenIdentity
	for _, token := range state.Tokens {
		if token.Token != r.Token {
			continue
		}
		if identity != nil || token.Error != "" || token.Coin == "" || token.TokenID == "" || token.WeiDecimals < 0 || token.WeiDecimals > 18 {
			return checkedTransfer{}, errors.New("The transfer token metadata is unavailable or ambiguous")
		}
		identity = &transferTokenIdentity{Token: token.Token, Coin: token.Coin, TokenID: token.TokenID, WeiDecimals: token.WeiDecimals}
	}
	if identity == nil {
		return checkedTransfer{}, errors.New("The transfer token is unavailable on this network")
	}
	if !amount.Equal(amount.Truncate(int32(identity.WeiDecimals))) {
		return checkedTransfer{}, errors.New("Transfer amount exceeds the token's smallest supported unit; no rounding was applied")
	}
	var spend *TokenBalance
	for _, venue := range []*VenueBalance{source, destination} {
		if venue.Shared || venue.Error != "" {
			return checkedTransfer{}, errors.New("The selected source or destination balance could not be verified")
		}
		if venue.Dex != "spot" && (venue.CollateralToken == nil || *venue.CollateralToken != r.Token) {
			return checkedTransfer{}, errors.New("A perpetual DEX can transfer only its own collateral token; a transfer does not convert currencies")
		}
		for i := range venue.Balances {
			token := &venue.Balances[i]
			if token.Token != r.Token {
				continue
			}
			if token.Error != "" || token.Coin != identity.Coin || token.TokenID != identity.TokenID || token.WeiDecimals != identity.WeiDecimals {
				return checkedTransfer{}, errors.New("Transfer token identity or precision changed; review again")
			}
			if venue == source {
				if spend != nil {
					return checkedTransfer{}, errors.New("Duplicate source token balance")
				}
				spend = token
			}
		}
	}
	if spend == nil || spend.AvailableError != "" || spend.Available == "" || !s.freshObservation(spend.ObservedAt) {
		return checkedTransfer{}, errors.New("A fresh transferable source balance is unavailable; refresh balances and review again")
	}
	available, ok := metricNumber(spend.Available, false)
	if !ok || available.LessThan(amount) {
		return checkedTransfer{}, errors.New("Transfer amount exceeds the verified available source balance")
	}
	return checkedTransfer{identity: *identity, source: *source, destination: *destination, available: available, amount: amount, observedAt: spend.ObservedAt}, nil
}

func (s *Store) invalidateOrderPreviewsLocked() {
	s.fundingRevision++
	s.previews = map[string]prepared{}
}

func (s *Store) PreviewTransfer(ctx context.Context, r TransferRequest) (TransferPreview, error) {
	if _, err := transferInput(r); err != nil {
		return TransferPreview{}, err
	}
	v, err := s.snapshot()
	if err != nil {
		return TransferPreview{}, err
	}
	if err = s.checkAgent(ctx, v); err != nil {
		return TransferPreview{}, err
	}
	state, err := s.AccountBalances(ctx)
	if err != nil {
		return TransferPreview{}, err
	}
	checked, err := s.validateTransferBalances(v, r, state)
	if err != nil {
		return TransferPreview{}, err
	}
	if err = ctx.Err(); err != nil {
		return TransferPreview{}, err
	}
	id, err := randomID()
	if err != nil {
		return TransferPreview{}, err
	}
	p := TransferPreview{ID: id, SourceDex: r.SourceDex, SourceName: checked.source.Name, DestinationDex: r.DestinationDex, DestinationName: checked.destination.Name, Token: checked.identity.Token, Coin: checked.identity.Coin, TokenID: checked.identity.TokenID, Amount: checked.amount.String(), AvailableBefore: checked.available.String(), AvailableAfter: checked.available.Sub(checked.amount).String(), ObservedAt: checked.observedAt, ExpiresAt: s.now().Add(time.Minute).UnixMilli(), AccountAddress: v.AccountAddress, Network: v.Network, Warnings: []string{"This transfer stays within the configured trading account.", "Moving collateral reduces the source balance available for positions and orders."}}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settings != v {
		return TransferPreview{}, errors.New("Hyperliquid settings changed; review the transfer again")
	}
	if s.transferPreviews == nil || len(s.transferPreviews) >= 32 {
		s.transferPreviews = map[string]preparedTransfer{}
	}
	s.invalidateOrderPreviewsLocked()
	s.transferPreviews[id] = preparedTransfer{TransferPreview: p, settings: v, token: checked.identity}
	return p, nil
}

// Ordered exactly as the documented agent action and SDK canonical schema.
// https://hyperliquid.gitbook.io/hyperliquid-docs/for-developers/api/exchange-endpoint#agent-send-asset
func transferWire(p TransferPreview, nonce uint64) wire {
	return wire{{"type", "agentSendAsset"}, {"destination", p.AccountAddress}, {"sourceDex", p.SourceDex}, {"destinationDex", p.DestinationDex}, {"token", p.Coin + ":" + p.TokenID}, {"amount", p.Amount}, {"fromSubAccount", ""}, {"nonce", nonce}}
}

func claimTransfer(path string, p TransferPreview) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(struct {
		Preview TransferPreview `json:"preview"`
		State   string          `json:"state"`
	}{p, "claimed"})
	if err == nil {
		_, err = f.Write(raw)
	}
	err = errors.Join(err, f.Sync(), f.Close())
	if err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		directory, e := os.Open(filepath.Dir(path))
		if e != nil {
			return e
		}
		err = errors.Join(directory.Sync(), directory.Close())
	}
	return err
}

func (s *Store) SubmitTransfer(ctx context.Context, id string) (TransferResult, error) {
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	case <-ctx.Done():
		return TransferResult{}, ctx.Err()
	}
	s.mu.Lock()
	p, ok := s.transferPreviews[id]
	delete(s.transferPreviews, id)
	current := s.settings
	s.invalidateOrderPreviewsLocked()
	s.mu.Unlock()
	if !ok || p.settings != current || s.now().UnixMilli() >= p.ExpiresAt {
		return TransferResult{}, errors.New("Transfer review expired or was already used; review again")
	}
	if err := s.checkAgent(ctx, current); err != nil {
		return TransferResult{}, err
	}
	state, err := s.AccountBalances(ctx)
	if err != nil {
		return TransferResult{}, err
	}
	checked, err := s.validateTransferBalances(current, TransferRequest{SourceDex: p.SourceDex, DestinationDex: p.DestinationDex, Token: p.Token, Amount: p.Amount}, state)
	if err != nil {
		return TransferResult{}, err
	}
	if checked.identity != p.token {
		return TransferResult{}, errors.New("Hyperliquid collateral identity or token precision changed; review again")
	}
	if s.now().UnixMilli() >= p.ExpiresAt {
		return TransferResult{}, errors.New("Transfer review expired while balances were refreshed; review again")
	}
	if err = ctx.Err(); err != nil {
		return TransferResult{}, err
	}
	key, err := keyFromString(current.PrivateKey)
	if err != nil {
		return TransferResult{}, err
	}
	claim := filepath.Join(s.profile, "hyperliquid-transfers", p.ID+".json")
	if err = claimTransfer(claim, p.TransferPreview); err != nil {
		return TransferResult{}, errors.New("Transfer confirmation is already claimed or its durable intent could not be saved; no transfer sent")
	}
	result := TransferResult{ID: p.ID, Status: "rejected", Message: "No transfer was sent."}
	finish := func() {
		if e := atomicJSON(claim, struct {
			Preview TransferPreview `json:"preview"`
			Result  TransferResult  `json:"result"`
		}{p.TransferPreview, result}); e != nil {
			result.Message += " The local result journal could not be saved; inspect the exchange before creating another transfer."
		}
		s.mu.Lock()
		s.invalidateOrderPreviewsLocked()
		s.mu.Unlock()
	}
	nonce, err := s.nextNonce()
	if err != nil {
		result.Message = "The signing nonce could not be reserved; no transfer was sent."
		finish()
		return result, nil
	}
	if err = ctx.Err(); err != nil {
		result.Message = "The transfer was cancelled before submission; no transfer was sent."
		finish()
		return result, nil
	}
	action := transferWire(p.TransferPreview, nonce)
	payload := struct {
		Action       wire      `json:"action"`
		Nonce        uint64    `json:"nonce"`
		Signature    signature `json:"signature"`
		ExpiresAfter uint64    `json:"expiresAfter"`
	}{action, nonce, signAction(key, action, nonce, uint64(p.ExpiresAt), current.Network == "mainnet"), uint64(p.ExpiresAt)}
	result.Status, result.Message = s.postTransfer(ctx, current, payload)
	finish()
	return result, nil
}

// A malformed, interrupted or HTTP-error write can already have committed.
// Only a complete protocol acknowledgement or explicit rejection is definite.
func (s *Store) postTransfer(ctx context.Context, v Settings, payload any) (string, string) {
	uncertain := "The transfer may have reached Hyperliquid. No retry was sent; inspect both balances before creating another transfer."
	raw, err := json.Marshal(payload)
	if err != nil {
		return "rejected", "The transfer could not be prepared; no transfer was sent."
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.base(v)+"/exchange", bytes.NewReader(raw))
	if err != nil {
		return "rejected", "The transfer request could not be prepared; no transfer was sent."
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return "uncertain", uncertain
	}
	defer response.Body.Close()
	raw, err = io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 || response.StatusCode < 200 || response.StatusCode >= 300 {
		return "uncertain", uncertain
	}
	var envelope struct {
		Status   string          `json:"status"`
		Response json.RawMessage `json:"response"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return "uncertain", uncertain
	}
	if envelope.Status == "err" {
		var reason string
		if json.Unmarshal(envelope.Response, &reason) == nil && strings.TrimSpace(reason) != "" {
			return "rejected", "Hyperliquid rejected the internal transfer. No retry was sent; refresh balances before creating a new review."
		}
	}
	var ack struct {
		Type string `json:"type"`
	}
	if envelope.Status == "ok" && json.Unmarshal(envelope.Response, &ack) == nil && ack.Type == "default" {
		return "confirmed", "Hyperliquid confirmed the internal transfer. Refresh balances to see the updated amounts."
	}
	return "uncertain", uncertain
}

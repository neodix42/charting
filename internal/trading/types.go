// Package trading defines the desktop's explicit review/confirm order contract.
// Provider clients own validation, one-use previews and execution journals.
package trading

import "cryptodesk/internal/domain"

type Status struct {
	Configured                bool   `json:"configured"`
	Enabled                   bool   `json:"enabled"`
	Environment               string `json:"environment"`
	CanBuy                    bool   `json:"canBuy"`
	CanSell                   bool   `json:"canSell"`
	LeverageSupported         bool   `json:"leverageSupported"`
	ProtectiveOrdersSupported bool   `json:"protectiveOrdersSupported"`
	MaxLeverage               int    `json:"maxLeverage"`
	QuantityLabel             string `json:"quantityLabel"`
	Error                     string `json:"error,omitempty"`
}

type Request struct {
	FeedKey    domain.FeedKey `json:"feedKey"`
	Side       string         `json:"side"`
	Quantity   string         `json:"quantity"`
	StopLoss   string         `json:"stopLoss"`
	TakeProfit string         `json:"takeProfit"`
	Leverage   int            `json:"leverage"`
}

type Preview struct {
	ID          string         `json:"id"`
	FeedKey     domain.FeedKey `json:"feedKey"`
	Side        string         `json:"side"`
	Quantity    string         `json:"quantity"`
	EntryPrice  string         `json:"entryPrice"`
	StopLoss    string         `json:"stopLoss"`
	TakeProfit  string         `json:"takeProfit"`
	Leverage    float64        `json:"leverage"`
	Environment string         `json:"environment"`
	ExpiresAt   int64          `json:"expiresAt"`
	Warnings    []string       `json:"warnings"`
}

type Result struct {
	Status   string   `json:"status"`
	OrderIDs []string `json:"orderIds"`
	Message  string   `json:"message"`
}

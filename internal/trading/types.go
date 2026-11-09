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
	AccountMetrics
	ID                 string         `json:"id"`
	FeedKey            domain.FeedKey `json:"feedKey"`
	Side               string         `json:"side"`
	Quantity           string         `json:"quantity"`
	EntryPrice         string         `json:"entryPrice"`
	StopLoss           string         `json:"stopLoss"`
	TakeProfit         string         `json:"takeProfit"`
	Leverage           float64        `json:"leverage"`
	Environment        string         `json:"environment"`
	ExpiresAt          int64          `json:"expiresAt"`
	Warnings           []string       `json:"warnings"`
	AccountCurrency    string         `json:"accountCurrency,omitempty"`
	ProfitPerPriceUnit string         `json:"profitPerPriceUnit,omitempty"`
	ValuationTime      int64          `json:"valuationTime,omitempty"`
	ValuationError     string         `json:"valuationError,omitempty"`
	ValuationSource    string         `json:"valuationSource,omitempty"`
	ValuationNotice    string         `json:"valuationNotice,omitempty"`
	MaxLeverage        int            `json:"maxLeverage,omitempty"`
}

// Estimate is read-only valuation metadata; it is never an order confirmation.
// ProfitPerPriceUnit converts a unit price move for one base unit into the
// account currency. Quantity and direction are applied by the ticket calculator.
type Estimate struct {
	FeedKey            domain.FeedKey `json:"feedKey"`
	Side               string         `json:"side"`
	EntryPrice         string         `json:"entryPrice"`
	AccountCurrency    string         `json:"accountCurrency"`
	ProfitPerPriceUnit string         `json:"profitPerPriceUnit"`
	ValuationTime      int64          `json:"valuationTime"`
	ValuationError     string         `json:"valuationError,omitempty"`
	ValuationSource    string         `json:"valuationSource,omitempty"`
	ValuationNotice    string         `json:"valuationNotice,omitempty"`
	MaxLeverage        int            `json:"maxLeverage,omitempty"`
}

// Independent metric errors preserve a real balance when a quote, conversion,
// or hypothetical order projection is unavailable. Empty values are unknown;
// the string "0" is a valid confirmed zero.
type AccountMetrics struct {
	AvailableBalance         string `json:"availableBalance,omitempty"`
	AvailableBalanceCurrency string `json:"availableBalanceCurrency,omitempty"`
	AvailableBalanceError    string `json:"availableBalanceError,omitempty"`
	AvailableBalanceBasis    string `json:"availableBalanceBasis,omitempty"`
	AccountBalance           string `json:"accountBalance,omitempty"`
	Equity                   string `json:"equity,omitempty"`
	UsedMargin               string `json:"usedMargin,omitempty"`
	MarginRequired           string `json:"marginRequired,omitempty"`
	MarginRequiredCurrency   string `json:"marginRequiredCurrency,omitempty"`
	MarginError              string `json:"marginError,omitempty"`
	MarginBasis              string `json:"marginBasis,omitempty"`
	LiquidationPrice         string `json:"liquidationPrice,omitempty"`
	LiquidationError         string `json:"liquidationError,omitempty"`
	LiquidationBasis         string `json:"liquidationBasis,omitempty"`
	LiquidationKind          string `json:"liquidationKind,omitempty"`
	MetricsTime              int64  `json:"metricsTime,omitempty"`
}

type Metrics struct {
	AccountMetrics
	FeedKey    domain.FeedKey `json:"feedKey"`
	Side       string         `json:"side"`
	Quantity   string         `json:"quantity"`
	Leverage   int            `json:"leverage"`
	EntryPrice string         `json:"entryPrice"`
}

type Result struct {
	Status   string   `json:"status"`
	OrderIDs []string `json:"orderIds"`
	Message  string   `json:"message"`
}

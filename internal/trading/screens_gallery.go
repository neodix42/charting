package trading

import (
	"errors"
	"sort"
	"strings"
)

type TradeScreenGalleryQuery struct {
	Conditions []string `json:"conditions,omitempty"`
	Directions []string `json:"directions,omitempty"`
	Outcomes   []string `json:"outcomes,omitempty"`
	Providers  []string `json:"providers,omitempty"`
	Offset     int      `json:"offset"`
	Limit      int      `json:"limit"`
}

type TradeScreenGalleryItem struct {
	Screen         TradeScreen `json:"screen"`
	EntryCondition string      `json:"entryCondition"`
	Outcome        string      `json:"outcome"`
	ProfitLoss     string      `json:"profitLoss"`
	Currency       string      `json:"currency"`
	CloseDate      int64       `json:"closeDate"`
}

type TradeScreenGalleryPage struct {
	Items      []TradeScreenGalleryItem `json:"items"`
	Conditions []string                 `json:"conditions"`
	Total      int                      `json:"total"`
	Offset     int                      `json:"offset"`
	Limit      int                      `json:"limit"`
	Errors     []string                 `json:"errors"`
}

func gallerySelection(values []string, allowed ...string) (map[string]bool, error) {
	if len(values) > len(allowed) {
		return nil, errors.New("Invalid screen gallery filter")
	}
	selected := map[string]bool{}
	for _, value := range values {
		valid := false
		for _, option := range allowed {
			valid = valid || value == option
		}
		if !valid {
			return nil, errors.New("Invalid screen gallery filter")
		}
		selected[value] = true
	}
	return selected, nil
}

func galleryDirection(direction string) string {
	switch direction {
	case "long", "buy":
		return "long"
	case "short", "sell":
		return "short"
	}
	return ""
}

// Won/lost is a final recorded result, never an open position's fluctuating P/L.
// Decimal parsing also preserves signs for values below float precision.
func galleryOutcome(trade Trade) string {
	if trade.Status != "closed" || trade.CloseDate <= 0 || trade.ProfitLossState != "closed" || strings.TrimSpace(trade.ProfitLossCurrency) == "" {
		return ""
	}
	value, ok := Numeric(trade.ProfitLoss)
	if !ok || value.IsZero() {
		return ""
	}
	if value.IsPositive() {
		return "won"
	}
	return "lost"
}

// ScreenGallery joins the authoritative local trade snapshots to annotations.
// It returns metadata only; originals are loaded individually through ReadScreen.
// This intentionally does not use the paginated visible history table.
func (s *EntryConditions) ScreenGallery(trades []Trade, query TradeScreenGalleryQuery) (TradeScreenGalleryPage, error) {
	out := TradeScreenGalleryPage{Items: []TradeScreenGalleryItem{}, Conditions: []string{}, Offset: query.Offset, Limit: query.Limit, Errors: []string{}}
	if out.Limit == 0 {
		out.Limit = 50
	}
	if query.Offset < 0 || query.Offset > 1000000 || out.Limit < 1 || out.Limit > 100 || len(query.Conditions) > 20000 {
		return out, errors.New("Invalid screen gallery query")
	}
	conditions := map[string]bool{}
	for _, value := range query.Conditions {
		if ValidateEntryCondition(value) != nil || strings.TrimSpace(value) == "" {
			return out, errors.New("Invalid screen gallery condition")
		}
		conditions[value] = true
	}
	directions, err := gallerySelection(query.Directions, "long", "short")
	if err != nil {
		return out, err
	}
	outcomes, err := gallerySelection(query.Outcomes, "won", "lost")
	if err != nil {
		return out, err
	}
	providers, err := gallerySelection(query.Providers, "dukascopy", "hyperliquid")
	if err != nil {
		return out, err
	}
	metadata, err := s.MetadataSnapshot()
	if err != nil {
		return out, err
	}
	active := len(conditions)+len(directions)+len(outcomes)+len(providers) > 0
	facets := map[string]bool{}
	seen := map[string]bool{}
	var items []TradeScreenGalleryItem
	for _, trade := range trades {
		provider, _, _ := strings.Cut(trade.ID, ":")
		if !validAnnotatedTrade(trade.ID) || trade.Provider != provider || seen[trade.ID] {
			continue
		}
		seen[trade.ID] = true
		note := metadata[trade.ID]
		if strings.TrimSpace(note.EntryCondition) != "" {
			facets[note.EntryCondition] = true
		}
		direction, outcome := galleryDirection(trade.Direction), galleryOutcome(trade)
		if !active || len(conditions) > 0 && !conditions[note.EntryCondition] || len(directions) > 0 && !directions[direction] || len(outcomes) > 0 && !outcomes[outcome] || len(providers) > 0 && !providers[provider] {
			continue
		}
		for _, screen := range note.Screens {
			// Reflect the trade's canonical direction while retaining all stored
			// attachment identity and original-file metadata.
			screen.Direction = direction
			items = append(items, TradeScreenGalleryItem{Screen: screen, EntryCondition: note.EntryCondition, Outcome: outcome, ProfitLoss: trade.ProfitLoss, Currency: trade.ProfitLossCurrency, CloseDate: trade.CloseDate})
		}
	}
	for condition := range facets {
		out.Conditions = append(out.Conditions, condition)
	}
	sort.Strings(out.Conditions)
	sort.Slice(items, func(i, j int) bool {
		if items[i].Screen.CreatedAt != items[j].Screen.CreatedAt {
			return items[i].Screen.CreatedAt > items[j].Screen.CreatedAt
		}
		if items[i].Screen.ID != items[j].Screen.ID {
			return items[i].Screen.ID < items[j].Screen.ID
		}
		return items[i].Screen.TradeID < items[j].Screen.TradeID
	})
	out.Total = len(items)
	start := min(query.Offset, len(items))
	out.Items = append(out.Items, items[start:min(start+out.Limit, len(items))]...)
	return out, nil
}

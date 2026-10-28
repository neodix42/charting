package screener

import (
	"container/heap"
	"errors"
	"fmt"
	"math"
	"strings"
)

// Every match independently opens a long trade at the next closed bar's open.
// SL can sample an earlier bar. TP is either a matched-bar field or a multiple
// of the entry-to-SL distance; a zero multiple preserves saved field targets.
type AnalysisTradeSimulation struct {
	StopLoss           string `json:"stopLoss"`
	StopLossBarsAgo    int    `json:"stopLossBarsAgo,omitempty"`
	TakeProfit         string `json:"takeProfit"`
	TakeProfitMultiple int    `json:"takeProfitMultiple,omitempty"`
	Duration           int    `json:"duration"`
}

// Completed summaries partition all matches. ProfitablePercent deliberately
// uses all matches, including unavailable or incomplete trades, as denominator.
type AnalysisSimulationSummary struct {
	TakeProfitHits    int64   `json:"takeProfitHits"`
	StopLossHits      int64   `json:"stopLossHits"`
	Expired           int64   `json:"expired"`
	InsufficientData  int64   `json:"insufficientData"`
	InvalidLevels     int64   `json:"invalidLevels"`
	Unavailable       int64   `json:"unavailable"`
	ProfitablePercent float64 `json:"profitablePercent"`
	Reason            string  `json:"reason,omitempty"`
}

func copyAnalysisTradeSimulation(config *AnalysisTradeSimulation) *AnalysisTradeSimulation {
	if config == nil {
		return nil
	}
	copy := *config
	return &copy
}

func validateAnalysisTradeSimulation(config *AnalysisTradeSimulation, draft bool) error {
	if config == nil {
		return nil
	}
	if config.Duration < 1 || config.Duration > 10000 {
		return errors.New("trade simulation duration must be between 1 and 10000 bars")
	}
	if config.StopLossBarsAgo < 0 || config.StopLossBarsAgo > 10000 {
		return errors.New("Stop Loss Bars ago must be between 0 and 10000")
	}
	if config.TakeProfitMultiple < 0 || config.TakeProfitMultiple > 3 {
		return errors.New("Take profit must use a data field or 1xSL, 2xSL, or 3xSL")
	}
	for i, field := range []string{config.StopLoss, config.TakeProfit} {
		if len(field) > 1024 || (!draft && strings.TrimSpace(field) == "" && (i == 0 || config.TakeProfitMultiple == 0)) {
			return errors.New("choose a data field for Stop Loss and Take profit")
		}
	}
	return nil
}

type analysisSimulatedTrade struct {
	id, occurrence       int64
	stopLoss, takeProfit float64
	active               bool
}

// Two threshold heaps resolve each trade once. Expiration uses a duration-sized
// ring; lazy heap entries are compacted periodically to keep memory O(duration)
// and total work O(rows log duration), even when every bar matches.
type analysisTradeHeap struct {
	trades []*analysisSimulatedTrade
	stop   bool
}

func (h analysisTradeHeap) Len() int { return len(h.trades) }
func (h analysisTradeHeap) Less(i, j int) bool {
	if h.stop {
		return h.trades[i].stopLoss > h.trades[j].stopLoss
	}
	return h.trades[i].takeProfit < h.trades[j].takeProfit
}
func (h analysisTradeHeap) Swap(i, j int) { h.trades[i], h.trades[j] = h.trades[j], h.trades[i] }
func (h *analysisTradeHeap) Push(value any) {
	h.trades = append(h.trades, value.(*analysisSimulatedTrade))
}
func (h *analysisTradeHeap) Pop() any {
	index := len(h.trades) - 1
	trade := h.trades[index]
	h.trades[index] = nil
	h.trades = h.trades[:index]
	return trade
}

type analysisTradeSimulator struct {
	config                 AnalysisTradeSimulation
	summary                AnalysisSimulationSummary
	open, high, low, close int
	stopLoss, takeProfit   int
	unsupported            bool
	pending                *analysisSimulatedTrade
	active                 map[int64]*analysisSimulatedTrade
	expiration             []*analysisSimulatedTrade
	stops, profits         analysisTradeHeap
	index, matches         int64
	stopHistory            []analysisStopSample
	onTakeProfit           func(occurrence int64) error
	onEntry                func(occurrence int64, entry, stopLoss, takeProfit float64) error
}

type analysisStopSample struct {
	index int64
	value float64
	valid bool
}

func newAnalysisTradeSimulator(series *analysisSeries, config *AnalysisTradeSimulation) *analysisTradeSimulator {
	if config == nil {
		return nil
	}
	s := &analysisTradeSimulator{config: *config, active: map[int64]*analysisSimulatedTrade{}, stops: analysisTradeHeap{stop: true}}
	if series.metadata.Timeframe == "tick" {
		s.unsupported = true
		s.summary.Reason = "Trade simulation requires OHLC bars; tick data has no next-bar open."
		return s
	}
	for i, field := range []struct {
		name        string
		destination *int
	}{
		{"open", &s.open}, {"high", &s.high}, {"low", &s.low}, {"close", &s.close},
		{config.StopLoss, &s.stopLoss}, {config.TakeProfit, &s.takeProfit},
	} {
		if i == 5 && config.TakeProfitMultiple != 0 {
			continue
		}
		position, ok := series.fieldIndex(field.name)
		if i < 4 {
			// Price aliases remain useful for selected levels, but OHLC inputs
			// must be real candle columns rather than bid/ask tick substitutes.
			position, ok = series.positions[field.name]
		}
		if !ok {
			s.unsupported = true
			s.summary.Reason = fmt.Sprintf("Trade simulation requires the missing field %q in this file.", field.name)
			return s
		}
		*field.destination = position
	}
	s.expiration = make([]*analysisSimulatedTrade, config.Duration)
	if config.StopLossBarsAgo > 0 {
		s.stopHistory = make([]analysisStopSample, config.StopLossBarsAgo+1)
	}
	return s
}

func analysisFinite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func (s *analysisTradeSimulator) match(row analysisRow) {
	if s == nil {
		return
	}
	occurrence := s.matches
	s.matches++
	if s.unsupported {
		s.summary.Unavailable++
		return
	}
	stop := row.values[s.stopLoss]
	if s.config.StopLossBarsAgo > 0 {
		index := s.index - int64(s.config.StopLossBarsAgo)
		if index < 0 {
			s.summary.InvalidLevels++
			return
		}
		sample := s.stopHistory[index%int64(len(s.stopHistory))]
		if !sample.valid || sample.index != index {
			s.summary.InvalidLevels++
			return
		}
		stop = sample.value
	}
	var profit float64
	if s.config.TakeProfitMultiple == 0 {
		profit = row.values[s.takeProfit]
	}
	if !analysisFinite(stop) || !analysisFinite(profit) {
		s.summary.InvalidLevels++
		return
	}
	s.pending = &analysisSimulatedTrade{occurrence: occurrence, stopLoss: stop, takeProfit: profit}
}

func (s *analysisTradeSimulator) resolve(trade *analysisSimulatedTrade) {
	trade.active = false
	delete(s.active, trade.id)
}

func (s *analysisTradeSimulator) hit(h *analysisTradeHeap, price float64) error {
	for h.Len() > 0 {
		trade := h.trades[0]
		if !trade.active {
			heap.Pop(h)
			continue
		}
		if (h.stop && trade.stopLoss < price) || (!h.stop && trade.takeProfit > price) {
			break
		}
		heap.Pop(h)
		s.resolve(trade)
		if h.stop {
			s.summary.StopLossHits++
		} else {
			s.summary.TakeProfitHits++
			if s.onTakeProfit != nil {
				if err := s.onTakeProfit(trade.occurrence); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *analysisTradeSimulator) compact(h *analysisTradeHeap) {
	if h.Len() <= 2*(s.config.Duration+1) {
		return
	}
	old := h.trades
	kept := old[:0]
	for _, trade := range old {
		if trade.active {
			kept = append(kept, trade)
		}
	}
	clear(old[len(kept):])
	h.trades = kept
	heap.Init(h)
}

func (s *analysisTradeSimulator) advance(index int64, row analysisRow) error {
	if s == nil || s.unsupported {
		return nil
	}
	s.index = index
	if len(s.stopHistory) > 0 {
		s.stopHistory[index%int64(len(s.stopHistory))] = analysisStopSample{index: index, value: row.values[s.stopLoss], valid: true}
	}
	open, high, low, closeValue := row.values[s.open], row.values[s.high], row.values[s.low], row.values[s.close]
	if !analysisFinite(open) || !analysisFinite(high) || !analysisFinite(low) || !analysisFinite(closeValue) ||
		low > math.Min(open, closeValue) || high < math.Max(open, closeValue) {
		// An unreadable candle prevents determining the first exit. Previously
		// resolved trades remain resolved; only still-open trades are unavailable.
		s.summary.Unavailable += int64(len(s.active))
		for _, trade := range s.active {
			s.resolve(trade)
		}
		if s.pending != nil {
			s.summary.Unavailable++
			s.pending = nil
		}
		s.summary.Reason = "Trade simulation encountered a bar with missing or invalid OHLC prices."
		clear(s.stops.trades)
		clear(s.profits.trades)
		s.stops.trades = s.stops.trades[:0]
		s.profits.trades = s.profits.trades[:0]
		return nil
	}
	if trade := s.pending; trade != nil {
		s.pending = nil
		if s.config.TakeProfitMultiple != 0 {
			risk := open - trade.stopLoss
			trade.takeProfit = open + float64(s.config.TakeProfitMultiple)*risk
			if !analysisFinite(risk) || risk <= 0 {
				trade.takeProfit = math.NaN()
			}
		}
		if !analysisFinite(trade.takeProfit) || !(trade.stopLoss < open && open < trade.takeProfit) {
			s.summary.InvalidLevels++
		} else {
			if s.onEntry != nil {
				if err := s.onEntry(trade.occurrence, open, trade.stopLoss, trade.takeProfit); err != nil {
					return err
				}
			}
			trade.id, trade.active = index, true
			s.active[trade.id] = trade
			s.expiration[(index+int64(s.config.Duration)-1)%int64(s.config.Duration)] = trade
			heap.Push(&s.stops, trade)
			heap.Push(&s.profits, trade)
		}
	}
	// The open is observed before either intrabar extreme. An overnight gap
	// through TP wins even when the same candle later also touches SL. Without
	// that ordering evidence, a candle touching both levels is a stop loss.
	for _, price := range []struct {
		heap  *analysisTradeHeap
		value float64
	}{{&s.stops, open}, {&s.profits, open}, {&s.stops, low}, {&s.profits, high}} {
		if err := s.hit(price.heap, price.value); err != nil {
			return err
		}
	}
	slot := index % int64(s.config.Duration)
	if trade := s.expiration[slot]; trade != nil {
		if trade.active {
			s.resolve(trade)
			s.summary.Expired++
		}
		s.expiration[slot] = nil
	}
	s.compact(&s.stops)
	s.compact(&s.profits)
	return nil
}

func (s *analysisTradeSimulator) finish() {
	if s == nil || s.unsupported {
		return
	}
	s.summary.InsufficientData += int64(len(s.active))
	for _, trade := range s.active {
		s.resolve(trade)
	}
	if s.pending != nil {
		s.summary.InsufficientData++
		s.pending = nil
	}
}

func (s *analysisTradeSimulator) snapshot(matches int64) *AnalysisSimulationSummary {
	if s == nil {
		return nil
	}
	summary := s.summary
	if matches > 0 {
		summary.ProfitablePercent = 100 * float64(summary.TakeProfitHits) / float64(matches)
	}
	return &summary
}

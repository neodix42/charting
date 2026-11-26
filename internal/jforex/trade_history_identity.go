package jforex

import (
	"math"
	"strconv"
	"time"
)

func sameBrokerQuantity(a, b float64) bool {
	return !math.IsNaN(a) && !math.IsInf(a, 0) && !math.IsNaN(b) && !math.IsInf(b, 0) && math.Abs(a-b) <= math.Max(1e-7, b*1e-8)
}

// A copied SDK order can lose its original requested amount. Recovery requires
// an actual filled size already acknowledged for this exact native order, never
// the requested size or an arbitrary smaller broker amount.
func knownBrokerFilledQuantity(j *brokerJournal) float64 {
	direction := "long"
	if j.Request.Side == "sell" {
		direction = "short"
	}
	if j.History.ID != "dukascopy:"+j.ID || j.History.Instrument != j.Request.Instrument || j.History.Direction != direction || j.History.OrderID == "" || j.Result.OrderID != "" && j.Result.OrderID != j.History.OrderID {
		return 0
	}
	valid := func(q float64) bool {
		return q > 0 && !math.IsNaN(q) && !math.IsInf(q, 0) && q <= j.Request.Quantity+math.Max(1e-7, j.Request.Quantity*1e-8)
	}
	entry, entryErr := strconv.ParseFloat(j.History.EntryPrice, 64)
	filledState := j.History.Status == "open" || j.History.Status == "closing" || j.History.Status == "closed"
	if filledState && j.History.OpenDate > 0 && j.History.OpenDate <= j.History.CheckedAt+5000 && entryErr == nil && entry > 0 && !math.IsNaN(entry) && !math.IsInf(entry, 0) && j.History.CheckedAt > 0 && j.History.CheckedAt <= time.Now().Add(5*time.Second).UnixMilli() {
		q, err := strconv.ParseFloat(j.History.Size, 64)
		if err == nil && valid(q) {
			return q
		}
	}
	if (j.Result.Status == "filled" || j.Result.Status == "partial") && j.Result.OrderID == j.History.OrderID && valid(j.Result.FilledQuantity) {
		return j.Result.FilledQuantity
	}
	return 0
}

func brokerOriginalQuantityMatches(j *brokerJournal, order brokerOrder) bool {
	if order.OriginalQuantity > 0 && sameBrokerQuantity(order.OriginalQuantity, j.Request.Quantity) {
		return true
	}
	witness := knownBrokerFilledQuantity(j)
	return witness > 0 && order.OrderID == j.History.OrderID &&
		(order.OriginalQuantity == 0 || sameBrokerQuantity(order.OriginalQuantity, witness)) &&
		order.FilledQuantity > 0 && sameBrokerQuantity(order.FilledQuantity, witness) &&
		order.RemainingQuantity <= witness+math.Max(1e-7, witness*1e-8)
}

func addBrokerFillWitness(request map[string]any, j *brokerJournal) {
	if quantity := knownBrokerFilledQuantity(j); quantity > 0 {
		request["knownFilledQuantity"] = quantity
	}
}

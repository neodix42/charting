package trading

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/shopspring/decimal"
)

// Trade is a locally attributed submission, never an imported account-wide order.
// Empty monetary values/times mean unavailable, not confirmed zero.
type Trade struct {
	ID                  string `json:"id"`
	Provider            string `json:"provider"`
	Environment         string `json:"environment"`
	Account             string `json:"account"`
	Instrument          string `json:"instrument"`
	Market              string `json:"market"`
	OrderID             string `json:"orderId"`
	EntryCondition      string `json:"entryCondition,omitempty"`
	SubmittedAt         int64  `json:"submittedAt"`
	OpenDate            int64  `json:"openDate"`
	CloseDate           int64  `json:"closeDate"`
	Size                string `json:"size"`
	RemainingSize       string `json:"remainingSize"`
	Direction           string `json:"direction"`
	EntryPrice          string `json:"entryPrice"`
	ExitPrice           string `json:"exitPrice,omitempty"`
	StopLoss            string `json:"stopLoss"`
	TakeProfit          string `json:"takeProfit"`
	StopLossSource      string `json:"stopLossSource,omitempty"`
	TakeProfitSource    string `json:"takeProfitSource,omitempty"`
	SLAmount            string `json:"slAmount"`
	TPAmount            string `json:"tpAmount"`
	Currency            string `json:"currency"`
	ProfitPerPriceUnit  string `json:"profitPerPriceUnit,omitempty"`
	ValuationTime       int64  `json:"valuationTime,omitempty"`
	ValuationError      string `json:"valuationError,omitempty"`
	ProfitLoss          string `json:"profitLoss,omitempty"`
	ProfitLossCurrency  string `json:"profitLossCurrency,omitempty"`
	ProfitLossTime      int64  `json:"profitLossTime,omitempty"`
	ProfitLossState     string `json:"profitLossState,omitempty"`
	ProfitLossBasis     string `json:"profitLossBasis,omitempty"`
	ProfitLossError     string `json:"profitLossError,omitempty"`
	PipSize             string `json:"pipSize,omitempty"`
	ProfitLossPips      string `json:"profitLossPips,omitempty"`
	ProfitLossPipsUnit  string `json:"profitLossPipsUnit,omitempty"`
	ProfitLossPipsTime  int64  `json:"profitLossPipsTime,omitempty"`
	ProfitLossPipsState string `json:"profitLossPipsState,omitempty"`
	ProfitLossPipsBasis string `json:"profitLossPipsBasis,omitempty"`
	ProfitLossPipsError string `json:"profitLossPipsError,omitempty"`
	BalanceAtOpen       string `json:"balanceAtOpen"`
	BalanceCurrency     string `json:"balanceCurrency"`
	BalanceTime         int64  `json:"balanceTime"`
	BalanceBasis        string `json:"balanceBasis,omitempty"`
	MarginUsed          string `json:"marginUsed"`
	MarginCurrency      string `json:"marginCurrency"`
	MarginBasis         string `json:"marginBasis,omitempty"`
	Status              string `json:"status"`
	CanClose            bool   `json:"canClose"`
	CloseReason         string `json:"closeReason"`
	CheckedAt           int64  `json:"checkedAt"`
	Error               string `json:"error,omitempty"`
}

type HistoryPage struct {
	Rows   []Trade  `json:"rows"`
	Total  int      `json:"total"`
	Offset int      `json:"offset"`
	Limit  int      `json:"limit"`
	Errors []string `json:"errors"`
}

type ClosePreview struct {
	ID          string   `json:"id"`
	TradeID     string   `json:"tradeId"`
	Provider    string   `json:"provider"`
	Environment string   `json:"environment"`
	Account     string   `json:"account"`
	Instrument  string   `json:"instrument"`
	OrderID     string   `json:"orderId"`
	Quantity    string   `json:"quantity"`
	ExpiresAt   int64    `json:"expiresAt"`
	Warnings    []string `json:"warnings"`
}

var intentPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

func ValidIntent(id string) bool { return intentPattern.MatchString(id) }

func Numeric(raw string) (decimal.Decimal, bool) {
	if len(raw) == 0 || len(raw) > 128 {
		return decimal.Zero, false
	}
	number, err := decimal.NewFromString(raw)
	return number, err == nil && number.Exponent() >= -24 && number.Exponent() <= 24
}
func Risk(entry, target, size, factor, direction string) string {
	e, ok := Numeric(entry)
	t, tOK := Numeric(target)
	q, qOK := Numeric(size)
	f, fOK := Numeric(factor)
	if !ok || !tOK || !qOK || !fOK || !e.IsPositive() || !t.IsPositive() || !q.IsPositive() || !f.IsPositive() {
		return ""
	}
	amount := t.Sub(e).Mul(q).Mul(f)
	if direction == "short" || direction == "sell" {
		amount = amount.Neg()
	}
	return amount.String()
}
func ApplyRisk(row *Trade, factor string) {
	size := row.Size
	if row.Status == "open" || row.Status == "closing" {
		if q, ok := Numeric(row.RemainingSize); ok && !q.IsNegative() {
			size = q.String()
		}
	}
	row.ProfitPerPriceUnit = factor
	row.SLAmount = Risk(row.EntryPrice, row.StopLoss, size, factor, row.Direction)
	row.TPAmount = Risk(row.EntryPrice, row.TakeProfit, size, factor, row.Direction)
}

func ReadJournal(path string, out any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 2*1024*1024 {
		return errors.New("Trade journal must be a regular file smaller than 2 MiB; original preserved")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 2*1024*1024+1))
	if err = decoder.Decode(out); err != nil {
		return errors.New("Invalid local trade journal; original preserved")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("Invalid local trade journal trailing data; original preserved")
	}
	return nil
}
func JournalIDs(directory string) ([]string, error) {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, entry := range entries {
		name := entry.Name()
		if len(name) == 37 && filepath.Ext(name) == ".json" && ValidIntent(name[:32]) {
			ids = append(ids, name[:32])
		}
	}
	if len(ids) > 10000 {
		return nil, errors.New("Trade history exceeds 10,000 local records; original journals preserved")
	}
	sort.Strings(ids)
	return ids, nil
}
func WriteJournal(path string, value any, exclusive bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(raw) > 2*1024*1024 {
		return errors.New("Trade journal exceeds 2 MiB")
	}
	if exclusive {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, err = f.Write(raw)
		err = errors.Join(err, f.Sync(), f.Close())
		if err != nil {
			return err
		}
	} else {
		if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
			return errors.New("Trade journal is not a regular file; original preserved")
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		f, err := os.CreateTemp(filepath.Dir(path), ".trade-*")
		if err != nil {
			return err
		}
		defer os.Remove(f.Name())
		defer f.Close()
		if err = f.Chmod(0600); err != nil {
			return err
		}
		if _, err = f.Write(raw); err != nil {
			return err
		}
		if err = f.Sync(); err != nil {
			return err
		}
		if err = f.Close(); err != nil {
			return err
		}
		if err = os.Rename(f.Name(), path); err != nil {
			return err
		}
	}
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

type CloseAttempt struct {
	ID        string `json:"id"`
	Quantity  string `json:"quantity"`
	CreatedAt int64  `json:"createdAt"`
	Status    string `json:"status"`
	OrderID   string `json:"orderId,omitempty"`
	Filled    string `json:"filled,omitempty"`
	Time      int64  `json:"time,omitempty"`
	Message   string `json:"message,omitempty"`
}

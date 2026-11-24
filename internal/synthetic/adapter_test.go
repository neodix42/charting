package synthetic

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
	"cryptodesk/internal/screener"
)

type nativeFake struct {
	items                                   []domain.Instrument
	instrumentsError                        error
	history                                 func(context.Context, domain.FeedKey, int64, int64, int) ([]domain.Candle, error)
	stream                                  func(context.Context, domain.FeedKey, func([]domain.Candle, domain.Health)) error
	available                               bool
	catalogCalls, historyCalls, streamCalls atomic.Int32
}

func (n *nativeFake) Instruments(ctx context.Context) ([]domain.Instrument, error) {
	n.catalogCalls.Add(1)
	return n.items, n.instrumentsError
}
func (n *nativeFake) History(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
	n.historyCalls.Add(1)
	if n.history != nil {
		return n.history(ctx, key, from, to, limit)
	}
	return nil, nil
}
func (n *nativeFake) Stream(ctx context.Context, key domain.FeedKey, emit func([]domain.Candle, domain.Health)) error {
	n.streamCalls.Add(1)
	if n.stream != nil {
		return n.stream(ctx, key, emit)
	}
	return nil
}
func (n *nativeFake) NativeInstrumentAvailable(string, string) bool { return n.available }
func (n *nativeFake) Diagnostics() exchange.Diagnostics {
	return exchange.Diagnostics{RESTRequests: int64(n.historyCalls.Load())}
}

func candle(at int64, close float64, calculated bool) domain.Candle {
	return domain.Candle{Time: at, Open: close, High: close + 1, Low: close - 1, Close: close, Closed: true, Quality: "authoritative", Synthetic: calculated, Approximate: calculated, VolumeUnavailable: calculated}
}

func TestCatalogDecoratesNativeFamilyWithoutDuplicatesOrMutatingProviderMetadata(t *testing.T) {
	original := map[string]string{"sdkSymbol": "BTC/USD", "marketHoursStatus": "available"}
	items := []domain.Instrument{
		{NativeID: "BTCUSD", Market: "cfd", BaseAsset: "BTC", QuoteAsset: "USD", ProviderMetadata: original, SupportedIntervals: []string{"1h", "1d"}},
		{NativeID: "XAUXAG", Market: "metal", BaseAsset: "XAU", QuoteAsset: "XAG"},
		{NativeID: "BTCUSD", Market: "cfd"},
		{NativeID: "EURUSD", Market: "forex", SupportedIntervals: []string{"1h"}},
	}
	got := Catalog(items, "explicit discovery warning")
	seen := map[string]bool{}
	if len(got) != 6 {
		t.Fatal("family was duplicated", got)
	}
	for _, item := range got {
		if seen[item.NativeID] {
			t.Fatal("duplicate native ID", item)
		}
		seen[item.NativeID] = true
		definition, family := screener.SyntheticDefinitionFor(item.NativeID)
		if !family {
			continue
		}
		if item.ProviderMetadata["category"] != screener.SyntheticCategory || item.ProviderMetadata["catalogWarning"] == "" || !reflect.DeepEqual(item.SupportedIntervals, []string{"1d", "1w"}) {
			t.Fatal("family lost metadata or supported UI periods", item)
		}
		if definition.Calculated && (item.ProviderMetadata["tradable"] != "false" || item.ProviderMetadata["synthetic"] != "true") {
			t.Fatal("computed choice became executable", item)
		}
		if item.NativeID == "BTCUSD" {
			if item.ProviderMetadata["tradable"] != "true" || item.ProviderMetadata["sdkSymbol"] != "BTC/USD" {
				t.Fatal("native provider identity was replaced", item)
			}
			item.ProviderMetadata["sdkSymbol"] = "changed"
		}
	}
	if original["sdkSymbol"] != "BTC/USD" || original["category"] != "" || items[0].SupportedIntervals[0] != "1h" {
		t.Fatal("catalog decoration mutated provider input", items)
	}
	if !IsFamily(domain.FeedKey{Exchange: "dukascopy", Instrument: "BTCXAU"}) || IsFamily(domain.FeedKey{Exchange: "bybit", Instrument: "BTCXAU"}) {
		t.Fatal("family identity crossed sources")
	}
}

func TestHistoryPrefersNativeBarsAndPreservesEarlierRetainedCoverage(t *testing.T) {
	first := int64(1704067200)
	provider := &nativeFake{available: true, history: func(context.Context, domain.FeedKey, int64, int64, int) ([]domain.Candle, error) {
		return []domain.Candle{candle(first+86400, 99, false)}, nil
	}}
	reads := 0
	a := New(provider, func(context.Context, domain.FeedKey, int64, int64, int) ([]domain.Candle, error) {
		reads++
		return []domain.Candle{candle(first, 11, true), candle(first+86400, 12, true), candle(first+2*86400, 13, true)}, nil
	})
	key := domain.FeedKey{Exchange: "dukascopy", Market: "cfd", Instrument: "BTCUSD", Timeframe: "1d"}
	got, err := a.History(context.Background(), key, first, 0, 0)
	if err != nil || len(got) != 3 || got[0].Close != 11 || got[1].Close != 99 || got[1].Synthetic || got[2].Close != 13 || reads != 1 || provider.historyCalls.Load() != 1 {
		t.Fatal("native priority, retained range, or unbounded history changed", got, err, reads)
	}
	if a.Diagnostics().RESTRequests != 1 {
		t.Fatal("native diagnostics were not delegated")
	}
}

func TestHistoryNeverMasksProviderOrRetainedDataErrors(t *testing.T) {
	providerError := &exchange.Error{Kind: "restricted", Message: "explicit HTTP403"}
	provider := &nativeFake{available: true, history: func(context.Context, domain.FeedKey, int64, int64, int) ([]domain.Candle, error) {
		return nil, providerError
	}}
	retainedCalls := 0
	a := New(provider, func(context.Context, domain.FeedKey, int64, int64, int) ([]domain.Candle, error) {
		retainedCalls++
		return []domain.Candle{candle(1704067200, 11, true)}, nil
	})
	key := domain.FeedKey{Exchange: "dukascopy", Market: "metal", Instrument: "XAUXAG", Timeframe: "1d"}
	if _, err := a.History(context.Background(), key, 0, 0, 0); !errors.Is(err, providerError) || retainedCalls != 0 {
		t.Fatal("provider failure silently switched to calculated data", err, retainedCalls)
	}
	provider.history = func(context.Context, domain.FeedKey, int64, int64, int) ([]domain.Candle, error) {
		return []domain.Candle{candle(1704067200, 50, false)}, nil
	}
	corruption := errors.New("explicit retained provenance checksum mismatch")
	a.history = func(context.Context, domain.FeedKey, int64, int64, int) ([]domain.Candle, error) {
		return nil, corruption
	}
	if _, err := a.History(context.Background(), key, 0, 0, 0); !errors.Is(err, corruption) {
		t.Fatal("native data concealed retained corruption", err)
	}
	a.history = func(context.Context, domain.FeedKey, int64, int64, int) ([]domain.Candle, error) {
		return nil, screener.ErrSyntheticHistoryUnavailable
	}
	if got, err := a.History(context.Background(), key, 0, 0, 0); err != nil || len(got) != 1 || got[0].Synthetic {
		t.Fatal("missing retained data blocked verified native history", got, err)
	}
}

func TestFirstNativeXAUXAGWeekKeepsCompleteMixedHistoryThenUsesNative(t *testing.T) {
	boundary := time.Date(2024, time.September, 16, 0, 0, 0, 0, time.UTC).Unix()
	provider := &nativeFake{available: true, history: func(context.Context, domain.FeedKey, int64, int64, int) ([]domain.Candle, error) {
		return []domain.Candle{candle(boundary, 80, false), candle(boundary+604800, 90, false)}, nil
	}}
	a := New(provider, func(context.Context, domain.FeedKey, int64, int64, int) ([]domain.Candle, error) {
		return []domain.Candle{candle(boundary, 81, true), candle(boundary+604800, 91, true)}, nil
	})
	key := domain.FeedKey{Exchange: "dukascopy", Market: "metal", Instrument: "XAUXAG", Timeframe: "1w"}
	got, err := a.History(context.Background(), key, boundary, boundary+2*604800, 0)
	if err != nil || len(got) != 2 || got[0].Close != 81 || !got[0].Synthetic || got[1].Close != 90 || got[1].Synthetic {
		t.Fatal("incomplete first native week replaced its mixed full-week history", got, err)
	}
}

func TestFirstNativeXAUXAGDayKeepsTheCalculatedMorningPrefix(t *testing.T) {
	boundary := time.Date(2024, time.September, 17, 0, 0, 0, 0, time.UTC).Unix()
	provider := &nativeFake{available: true, history: func(context.Context, domain.FeedKey, int64, int64, int) ([]domain.Candle, error) {
		return []domain.Candle{candle(boundary, 80, false), candle(boundary+86400, 90, false)}, nil
	}}
	a := New(provider, func(context.Context, domain.FeedKey, int64, int64, int) ([]domain.Candle, error) {
		return []domain.Candle{candle(boundary, 81, true), candle(boundary+86400, 91, false)}, nil
	})
	got, err := a.History(context.Background(), domain.FeedKey{Exchange: "dukascopy", Market: "metal", Instrument: "XAUXAG", Timeframe: "1d"}, boundary, boundary+2*86400, 0)
	if err != nil || len(got) != 2 || got[0].Close != 81 || !got[0].Synthetic || got[1].Close != 90 || got[1].Synthetic {
		t.Fatal("first native intraday start discarded its mixed complete D1", got, err)
	}
}

func TestCalculatedHistoryUsesOnlyClosedRetainedBarsAndNeverNativeSDK(t *testing.T) {
	provider := &nativeFake{available: true}
	first := int64(1704067200)
	open := candle(first+86400, 12, true)
	open.Closed = false
	a := New(provider, func(context.Context, domain.FeedKey, int64, int64, int) ([]domain.Candle, error) {
		return []domain.Candle{candle(first, 11, true), open}, nil
	})
	key := domain.FeedKey{Exchange: "dukascopy", Market: "cfd", Instrument: "BTCXAU", Timeframe: "1d"}
	got, err := a.History(context.Background(), key, first, first+2*86400, 0)
	if err != nil || len(got) != 1 || !got[0].Closed || provider.historyCalls.Load() != 0 {
		t.Fatal("computed history reached native SDK or exposed an open retained candle", got, err)
	}
	key.Timeframe = "1h"
	if _, err := a.History(context.Background(), key, first, first+86400, 0); err == nil || provider.historyCalls.Load() != 0 {
		t.Fatal("unsupported computed intraday delegated to SDK", err)
	}
}

func TestOrdinaryAndLegacyNativeIntradayHistoryAndStreamsDelegate(t *testing.T) {
	streamError := errors.New("explicit native stream result")
	provider := &nativeFake{available: true,
		history: func(context.Context, domain.FeedKey, int64, int64, int) ([]domain.Candle, error) {
			return []domain.Candle{candle(1704067200, 10, false)}, nil
		},
		stream: func(_ context.Context, _ domain.FeedKey, emit func([]domain.Candle, domain.Health)) error {
			emit(nil, domain.Health{State: "live", Reason: "native stream"})
			return streamError
		}}
	a := New(provider, func(context.Context, domain.FeedKey, int64, int64, int) ([]domain.Candle, error) {
		t.Fatal("ordinary native chart read calculated history")
		return nil, nil
	})
	for _, key := range []domain.FeedKey{
		{Exchange: "dukascopy", Market: "forex", Instrument: "EURUSD", Timeframe: "1d"},
		{Exchange: "dukascopy", Market: "cfd", Instrument: "BTCUSD", Timeframe: "1h"},
		{Exchange: "dukascopy", Market: "metal", Instrument: "XAUXAG", Timeframe: "1m"},
	} {
		if got, err := a.History(context.Background(), key, 0, 0, 0); err != nil || len(got) != 1 {
			t.Fatal("ordinary history was intercepted", key, got, err)
		}
		emitted := false
		err := a.Stream(context.Background(), key, func(_ []domain.Candle, health domain.Health) { emitted = health.Reason == "native stream" })
		if !errors.Is(err, streamError) || !emitted {
			t.Fatal("native stream was replaced by polling", key, err)
		}
	}
	if provider.historyCalls.Load() != 3 || provider.streamCalls.Load() != 3 {
		t.Fatal("ordinary delegation counts changed", provider.historyCalls.Load(), provider.streamCalls.Load())
	}
}

func TestCalculatedPollingLabelsRetainedDataAndStopsAfterCancellation(t *testing.T) {
	provider := &nativeFake{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reads := 0
	a := New(provider, func(context.Context, domain.FeedKey, int64, int64, int) ([]domain.Candle, error) {
		reads++
		return []domain.Candle{candle(1704067200, 11, true)}, nil
	})
	a.poll = time.Millisecond
	key := domain.FeedKey{Exchange: "dukascopy", Market: "cfd", Instrument: "ETHBTC", Timeframe: "1d"}
	emits := 0
	err := a.Stream(ctx, key, func(bars []domain.Candle, health domain.Health) {
		emits++
		if len(bars) != 1 || health.State != "provisional" || !strings.Contains(health.Reason, "downloaded closed history") || health.Quote != nil {
			t.Error("calculated polling pretended to be live broker quotes", bars, health)
		}
		cancel()
	})
	if !errors.Is(err, context.Canceled) || reads != 1 || emits != 1 || provider.streamCalls.Load() != 0 {
		t.Fatal("cancelled polling retained work", err, reads, emits)
	}
	if _, err := a.Instruments(ctx); !errors.Is(err, context.Canceled) || provider.catalogCalls.Load() != 0 {
		t.Fatal("cancelled discovery returned an invented success", err)
	}
	if _, err := a.History(ctx, key, 0, 0, 0); !errors.Is(err, context.Canceled) || reads != 1 {
		t.Fatal("cancelled history still performed reads", err)
	}
}

func TestNativeFamilyFallbackHandsOffToReadySDKStream(t *testing.T) {
	for _, key := range []domain.FeedKey{
		{Exchange: "dukascopy", Market: "cfd", Instrument: "BTCUSD", Timeframe: "1d"},
		{Exchange: "dukascopy", Market: "metal", Instrument: "XAUXAG", Timeframe: "1w"},
	} {
		t.Run(key.Instrument, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			first := int64(1704067200)
			retained := []domain.Candle{candle(first, 11, key.Instrument == "XAUXAG")}
			current := []domain.Candle{candle(first+7*86400, 12, false)}
			bid := 12.0
			quote := &domain.Quote{Bid: &bid, Time: (first + 7*86400) * 1000}
			provider := &nativeFake{stream: func(nativeCtx context.Context, nativeKey domain.FeedKey, emit func([]domain.Candle, domain.Health)) error {
				if nativeKey != key {
					return errors.New("native handoff changed the requested feed")
				}
				emit(current, domain.Health{State: "live", Reason: "native stream", Quote: quote})
				<-nativeCtx.Done()
				return nativeCtx.Err()
			}}
			var reads atomic.Int32
			a := New(provider, func(context.Context, domain.FeedKey, int64, int64, int) ([]domain.Candle, error) {
				reads.Add(1)
				return retained, nil
			})
			a.poll = time.Millisecond
			type observation struct {
				bars   []domain.Candle
				health domain.Health
			}
			observed := make(chan observation)
			ready := make(chan struct{})
			done := make(chan error, 1)
			go func() {
				done <- a.Stream(ctx, key, func(bars []domain.Candle, health domain.Health) {
					select {
					case observed <- observation{bars: bars, health: health}:
					case <-ctx.Done():
						return
					}
					if health.State == "provisional" {
						select {
						case <-ready:
						case <-ctx.Done():
						}
					} else {
						cancel()
					}
				})
			}()
			receive := func() observation {
				t.Helper()
				select {
				case got := <-observed:
					return got
				case <-time.After(time.Second):
					t.Fatal("native family stream did not emit")
					return observation{}
				}
			}
			if got := receive(); !reflect.DeepEqual(got.bars, retained) || got.health.State != "provisional" || got.health.Quote != nil {
				t.Fatal("unready SDK did not use downloaded fallback", got)
			}
			// The first callback holds polling while account discovery makes
			// the actual native instrument available, then releases the next tick.
			provider.available = true
			close(ready)
			if got := receive(); !reflect.DeepEqual(got.bars, current) || got.health.State != "live" || got.health.Quote != quote {
				t.Fatal("ready SDK did not replace fallback polling with native quotes", got)
			}
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) || reads.Load() != 1 || provider.historyCalls.Load() != 0 || provider.streamCalls.Load() != 1 {
					t.Fatal("handoff retained polling or lost native stream cancellation", err, reads.Load(), provider.historyCalls.Load(), provider.streamCalls.Load())
				}
			case <-time.After(time.Second):
				t.Fatal("native stream did not stop after cancellation")
			}
		})
	}
}

func TestCalculatedPollingObservesAtomicPublicationOnNextPollAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	directory := t.TempDir()
	published := filepath.Join(directory, "published.json")
	write := func(filename string, bars []domain.Candle) {
		t.Helper()
		data, err := json.Marshal(bars)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filename, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	first := int64(1704067200)
	initial := []domain.Candle{candle(first, 11, true)}
	replacement := []domain.Candle{candle(first, 12, true), candle(first+86400, 13, true)}
	write(published, initial)
	var reads, emits atomic.Int32
	provider := &nativeFake{}
	a := New(provider, func(context.Context, domain.FeedKey, int64, int64, int) ([]domain.Candle, error) {
		reads.Add(1)
		data, err := os.ReadFile(published)
		if err != nil {
			return nil, err
		}
		var bars []domain.Candle
		err = json.Unmarshal(data, &bars)
		return bars, err
	})
	a.poll = time.Millisecond
	type observation struct {
		bars   []domain.Candle
		health domain.Health
	}
	observed := make(chan observation)
	publishedNext := make(chan struct{})
	done := make(chan error, 1)
	key := domain.FeedKey{Exchange: "dukascopy", Market: "cfd", Instrument: "ETHBTC", Timeframe: "1d"}
	go func() {
		done <- a.Stream(ctx, key, func(bars []domain.Candle, health domain.Health) {
			number := emits.Add(1)
			select {
			case observed <- observation{bars: bars, health: health}:
			case <-ctx.Done():
				return
			}
			if number == 1 {
				// Hold the first callback until the new file is atomically
				// published, so the next tick must observe that publication.
				select {
				case <-publishedNext:
				case <-ctx.Done():
				}
			} else {
				cancel()
			}
		})
	}()
	receive := func() observation {
		t.Helper()
		select {
		case got := <-observed:
			return got
		case <-time.After(time.Second):
			t.Fatal("poll did not emit published history")
			return observation{}
		}
	}
	if got := receive(); !reflect.DeepEqual(got.bars, initial) {
		t.Fatal("initial poll did not read the original publication", got.bars)
	}
	next := filepath.Join(directory, "next.json")
	write(next, replacement)
	if err := os.Rename(next, published); err != nil {
		t.Fatal(err)
	}
	close(publishedNext)
	got := receive()
	if !reflect.DeepEqual(got.bars, replacement) || got.health.State != "provisional" || got.health.Quote != nil || !strings.Contains(got.health.Reason, "downloaded closed history") {
		t.Fatal("next poll did not observe the corrected candle and new closed candle", got)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || reads.Load() != 2 || emits.Load() != 2 || provider.historyCalls.Load() != 0 || provider.streamCalls.Load() != 0 {
			t.Fatal("cancelled polling continued work or reached the native provider", err, reads.Load(), emits.Load())
		}
	case <-time.After(time.Second):
		t.Fatal("polling did not stop after the second callback cancelled it")
	}
	if _, err := a.History(ctx, key, 0, 0, 0); !errors.Is(err, context.Canceled) || reads.Load() != 2 {
		t.Fatal("cancelled history performed another publication read", err, reads.Load())
	}
}

func TestCancelledInflightCalculatedReadCannotEmitAfterChartDetaches(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	a := New(&nativeFake{}, func(ctx context.Context, _ domain.FeedKey, _, _ int64, _ int) ([]domain.Candle, error) {
		close(started)
		<-ctx.Done()
		return []domain.Candle{candle(1704067200, 11, true)}, nil
	})
	emits := atomic.Int32{}
	done := make(chan error, 1)
	go func() {
		done <- a.Stream(ctx, domain.FeedKey{Exchange: "dukascopy", Market: "cfd", Instrument: "BTCXAU", Timeframe: "1d"}, func([]domain.Candle, domain.Health) { emits.Add(1) })
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || emits.Load() != 0 {
			t.Fatal("inactive calculated chart received a callback", err, emits.Load())
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled reader did not stop")
	}
}

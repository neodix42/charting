package screener

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProviderLimiterSpacesTwentyWorkersAndPersistsCooldowns(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "rate-limit.json")
	l, err := newProviderLimiter(filename)
	if err != nil {
		t.Fatal(err)
	}
	l.interval = 15 * time.Millisecond
	var mu sync.Mutex
	var starts []int64
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if err := l.permit(context.Background(), nil); err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			starts = append(starts, time.Now().UnixMilli())
			mu.Unlock()
		})
	}
	wg.Wait()
	sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })
	if len(starts) != 20 || starts[19]-starts[0] < 280 {
		t.Fatal("workers bypassed aggregate pacing", starts)
	}
	until, err := l.observe(429, "601")
	if err != nil || until < time.Now().Add(600*time.Second).UnixMilli() {
		t.Fatal("Retry-After was not preserved", until, err)
	}
	restored, err := newProviderLimiter(filename)
	if err != nil {
		t.Fatal(err)
	}
	if err = restored.permit(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "Retry-After") {
		t.Fatal("restart bypassed provider cooldown", err)
	}
	denied, err := l.observe(403, "")
	if err != nil || denied < time.Now().Add(14*time.Minute).UnixMilli() {
		t.Fatal("access-denial cooldown missing", denied, err)
	}
	again, err := l.observe(403, "")
	if err != nil || again != denied {
		t.Fatal("blocked manual retry extended cooldown", again, denied, err)
	}
	if info, err := os.Stat(filename); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0600) {
		t.Fatal("cooldown state is not private", info, err)
	}
}

func TestProviderLimiterWakesWaitingWorkersOnDenialAndCancelsPromptly(t *testing.T) {
	l, err := newProviderLimiter(filepath.Join(t.TempDir(), "rate.json"))
	if err != nil {
		t.Fatal(err)
	}
	l.state.NextAt = time.Now().Add(time.Minute).UnixMilli()
	waiting := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- l.permit(context.Background(), func(string) {
			select {
			case waiting <- struct{}{}:
			default:
			}
		})
	}()
	<-waiting
	if _, err = l.observe(403, ""); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err == nil || !strings.Contains(err.Error(), "no request sent") {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("denial did not stop waiting workers")
	}
	l.state.DeniedTill = 0
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		done <- l.permit(ctx, func(string) {
			select {
			case waiting <- struct{}{}:
			default:
			}
		})
	}()
	<-waiting
	cancel()
	select {
	case err = <-done:
		if err != context.Canceled {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not interrupt pacing")
	}
}

func TestProviderRetryAfterParsesDeltaDateAndOverflow(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	for _, value := range []string{"120", now.Add(120 * time.Second).Format(http.TimeFormat)} {
		if got := providerRetryAfter(value, now); got != now.Add(120*time.Second).UnixMilli() {
			t.Fatal(value, got)
		}
	}
	for _, value := range []string{"", "nonsense", "-1", "0.5"} {
		if providerRetryAfter(value, now) != 0 {
			t.Fatal(value)
		}
	}
	if providerRetryAfter(strings.Repeat("9", 100), now) != 253402300799000 {
		t.Fatal("overflow did not retain long cooldown")
	}
}

func TestProductionWorkersUseOneRateQueueAndRetainProviderResponses(t *testing.T) {
	for _, scenario := range []string{"success", "429", "403", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			m := sourceEnrichmentManager(t, false)
			provider := filepath.Join(m.profile, "screener-provider")
			if err := os.MkdirAll(provider, 0700); err != nil {
				t.Fatal(err)
			}
			cache := `{"version":2,"origin":"https://jetta.dukascopy.com","token":"offline-test","userAgent":"CryptoDesk offline transport"}`
			if err := os.WriteFile(filepath.Join(provider, "waf-token.json"), []byte(cache), 0600); err != nil {
				t.Fatal(err)
			}
			trace := filepath.Join(t.TempDir(), "trace")
			traceJS, _ := json.Marshal(trace)
			preload := filepath.Join(t.TempDir(), "transport.cjs")
			script := fmt.Sprintf(`const fs=require('node:fs');global.fetch=async function(url,options){
if(options.headers.cookie!=='aws-waf-token=offline-test')throw Error('Supported session not reused');
fs.appendFileSync(%s,Date.now()+'\n');
if(%q==='429')return new Response('rate limited',{status:429,headers:{'retry-after':'601'}});
if(%q==='403')return new Response('forbidden',{status:403});
return new Response(JSON.stringify({timestamp:1704067200000,shift:86400000,multiplier:1,open:10,high:11,low:9,close:10,times:[0],opens:[0],highs:[0],lows:[0],closes:[0],volumes:[1]}),{headers:{'content-type':'application/json'}});};`, traceJS, scenario, scenario)
			if err := os.WriteFile(preload, []byte(script), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("NODE_OPTIONS", "--require="+preload)
			if scenario == "cancel" {
				if err := writeState(filepath.Join(provider, "rate-limit.json"), providerRateState{Version: 1, NextAt: time.Now().Add(time.Minute).UnixMilli()}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := m.Start(Request{Symbols: []string{"eurusd", "gbpusd"}, Timeframe: "d1", From: "2024-01-01", To: "2024-01-02", Concurrency: 20}); err != nil {
				t.Fatal(err)
			}
			if scenario == "cancel" {
				deadline := time.Now().Add(5 * time.Second)
				waiting := false
				for time.Now().Before(deadline) && !waiting {
					for _, job := range m.Info().Jobs {
						if strings.Contains(job.Message, "shared Dukascopy request pacing") {
							waiting = true
						}
					}
					if !waiting {
						time.Sleep(5 * time.Millisecond)
					}
				}
				if !waiting {
					t.Fatal("worker never entered shared queue")
				}
				start := time.Now()
				info := m.Cancel()
				if time.Since(start) > 2*time.Second || info.Active {
					t.Fatal("cancel did not drain workers waiting for permits", info)
				}
				if _, err := os.Stat(trace); !os.IsNotExist(err) {
					t.Fatal("cancelled waiting workers sent a request", err)
				}
				return
			}
			info := waitFinished(t, m)
			data, err := os.ReadFile(trace)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Fields(string(data))
			if scenario == "success" {
				if len(lines) != 2 {
					t.Fatal("missing real transport requests", string(data))
				}
				a, _ := time.ParseDuration(lines[0] + "ms")
				b, _ := time.ParseDuration(lines[1] + "ms")
				if b-a < 1900*time.Millisecond {
					t.Fatal("workers bypassed the production two-second queue", lines)
				}
				for _, job := range info.Jobs {
					if job.Status != "completed" {
						t.Fatalf("paced download failed: %+v", job)
					}
				}
			} else {
				if len(lines) != 1 {
					t.Fatal("provider refusal was repeated", string(data))
				}
				for _, job := range info.Jobs {
					if job.Status != "failed" {
						t.Fatalf("provider refusal was published: %+v", job)
					}
				}
				restored, err := newProviderLimiter(filepath.Join(provider, "rate-limit.json"))
				if err != nil {
					t.Fatal(err)
				}
				if err := restored.permit(context.Background(), nil); err == nil {
					t.Fatal("new session bypassed saved cooldown")
				}
				if _, err := m.Resume(); err != nil {
					t.Fatal(err)
				}
				waitFinished(t, m)
				after, err := os.ReadFile(trace)
				if err != nil || string(after) != string(data) {
					t.Fatal("Resume sent another request during cooldown", string(after), err)
				}
				again, err := newProviderLimiter(filepath.Join(provider, "rate-limit.json"))
				if err != nil || again.state.DeniedTill != restored.state.DeniedTill {
					t.Fatal("Resume extended the access cooldown", err)
				}
			}
		})
	}
}

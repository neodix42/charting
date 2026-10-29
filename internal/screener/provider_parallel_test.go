package screener

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Exercise the real Manager, owner-pipe permits and pinned upstream transport.
// Only global.fetch is replaced; no test contacts Dukascopy or a real profile.
func TestFiveDownloaderWorkersOverlapRealTransportAndCloseRetainsTheirQueue(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	if got := m.Info(); got.Concurrency != 5 || got.MaxConcurrency != 20 {
		t.Fatalf("unexpected default worker bounds: %+v", got)
	}
	if _, err := m.ConfigureConcurrency(5); err != nil {
		t.Fatal(err)
	}
	provider := filepath.Join(m.profile, "screener-provider")
	if err := os.MkdirAll(provider, 0700); err != nil {
		t.Fatal(err)
	}
	cache := `{"version":2,"origin":"https://jetta.dukascopy.com","token":"offline-parallel","userAgent":"CryptoDesk isolated transport"}`
	if err := os.WriteFile(filepath.Join(provider, "waf-token.json"), []byte(cache), 0600); err != nil {
		t.Fatal(err)
	}
	trace := t.TempDir()
	traceJS, _ := json.Marshal(trace)
	preload := filepath.Join(trace, "transport.cjs")
	script := fmt.Sprintf(`const fs=require('node:fs'),path=require('node:path');
const trace=%s;
global.fetch=async function(url,options){
  if(options.headers.cookie!=='aws-waf-token=offline-parallel')throw Error('Offline session was not reused');
  let initial=false;
  try{fs.writeFileSync(path.join(trace,'first-probe'),String(Date.now()),{flag:'wx'});initial=true;}
  catch(error){if(error.code!=='EEXIST')throw error;}
  if(!initial){
    fs.writeFileSync(path.join(trace,'active-'+process.pid+'.json'),JSON.stringify({pid:process.pid,url:String(url),started:Date.now()}));
    // The owner must permit other data requests before this response exists.
    // Keeping it unresolved also proves Close cancels actual in-flight work.
    await new Promise(()=>{});
  }
  const year=Number(new URL(url).pathname.split('/').at(-1));
  return new Response(JSON.stringify({timestamp:Date.UTC(year,0,1),shift:86400000,multiplier:1,
    open:10,high:11,low:9,close:10,times:[0],opens:[0],highs:[0],lows:[0],closes:[0],volumes:[1]}),
    {headers:{'content-type':'application/json'}});
};`, traceJS)
	if err := os.WriteFile(preload, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NODE_OPTIONS", "--require="+preload)
	symbols := []string{"eurusd", "gbpusd", "audusd", "nzdusd", "usdcad", "usdchf"}
	if _, err := m.Start(Request{Symbols: symbols, Timeframe: "d1", From: "2023-01-01", To: "2025-01-01"}); err != nil {
		t.Fatal(err)
	}
	var starts []int64
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(trace)
		if err != nil {
			t.Fatal(err)
		}
		starts = nil
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), "active-") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(trace, entry.Name()))
			var request struct {
				Started int64 `json:"started"`
			}
			if err != nil || json.Unmarshal(data, &request) != nil {
				continue // A child may still be writing its short observation.
			}
			starts = append(starts, request.Started)
		}
		if len(starts) >= 5 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(starts) != 5 {
		t.Fatalf("five workers never overlapped their pending network responses: starts=%v jobs=%+v", starts, m.Info().Jobs)
	}
	info := m.Info()
	running, queued := 0, 0
	for _, job := range info.Jobs {
		if job.Status == "running" {
			running++
		}
		if job.Status == "queued" {
			queued++
		}
	}
	if running != 5 || queued != 1 || info.Concurrency != 5 {
		t.Fatalf("active worker cap did not retain the sixth asset in the queue: %+v", info)
	}
	// A shared session is initialized once, while the ready marker permits
	// overlapping subsequent requests. One unresolved response must not serialize
	// the remaining four workers behind a browser or network mutex.
	entries, err := os.ReadDir(provider)
	if err != nil {
		t.Fatal(err)
	}
	batches := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "batch-") {
			batches++
			if _, err = os.Stat(filepath.Join(provider, entry.Name(), "ready")); err != nil {
				t.Fatalf("shared initialization never completed: %v", err)
			}
		}
	}
	if batches != 1 {
		t.Fatalf("workers did not share exactly one provider session: %d", batches)
	}
	startedClose := time.Now()
	m.Close()
	if elapsed := time.Since(startedClose); elapsed > 3*time.Second {
		t.Fatalf("close did not promptly cancel in-flight requests: %s", elapsed)
	}
	checkpointRetained := false
	for _, job := range m.Info().Jobs {
		if _, err := os.Stat(filepath.Join(m.jobDirectory(job), "checkpoint.json")); err == nil {
			checkpointRetained = true
		}
	}
	if !checkpointRetained {
		t.Fatal("close discarded the first successfully fetched source checkpoint")
	}
	restored := newManager(m.profile, func() (workerRuntime, error) { return workerRuntime{}, errors.New("offline queue inspection only") })
	defer restored.Close()
	info = restored.Info()
	if info.Active || info.Concurrency != 5 || len(info.Jobs) != 6 {
		t.Fatalf("closed parallel queue did not restore with its worker preference: %+v", info)
	}
	for _, job := range info.Jobs {
		if job.Status != "queued" || job.OutputPath != "" {
			t.Fatalf("interrupted data was published or its unfinished queue lost: %+v", job)
		}
	}
}

func TestProviderRefusalStopsQueuedRequestsBeyondParallelWorkerCount(t *testing.T) {
	for _, status := range []int{403, 429} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			m := sourceEnrichmentManager(t, false)
			if _, err := m.ConfigureConcurrency(2); err != nil {
				t.Fatal(err)
			}
			provider := filepath.Join(m.profile, "screener-provider")
			if err := os.MkdirAll(provider, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(provider, "waf-token.json"), []byte(`{"version":2,"origin":"https://jetta.dukascopy.com","token":"offline-denial","userAgent":"CryptoDesk isolated transport"}`), 0600); err != nil {
				t.Fatal(err)
			}
			trace := filepath.Join(t.TempDir(), "requests")
			traceJS, _ := json.Marshal(trace)
			preload := filepath.Join(t.TempDir(), "refusal.cjs")
			script := fmt.Sprintf(`const fs=require('node:fs');global.fetch=async function(url){
fs.appendFileSync(%s,String(url)+'\n');
return new Response('provider refusal',{status:%d,headers:{'retry-after':'601'}});};`, traceJS, status)
			if err := os.WriteFile(preload, []byte(script), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("NODE_OPTIONS", "--require="+preload)
			if _, err := m.Start(Request{Symbols: []string{"eurusd", "gbpusd", "audusd", "nzdusd", "usdcad", "usdchf"}, Timeframe: "d1", From: "2024-01-01", To: "2024-01-02"}); err != nil {
				t.Fatal(err)
			}
			info := waitFinished(t, m)
			for _, job := range info.Jobs {
				if job.Status != "failed" || job.OutputPath != "" {
					t.Fatalf("queued refused work did not fail without publishing: %+v", job)
				}
			}
			data, err := os.ReadFile(trace)
			if err != nil || strings.Count(string(data), "\n") != 1 {
				t.Fatalf("queued jobs repeated a provider refusal: %q %v", data, err)
			}
			limiter, err := newProviderLimiter(filepath.Join(provider, "rate-limit.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err = limiter.permit(context.Background(), nil); err == nil {
				t.Fatal("parallel batch completion discarded its persisted provider cooldown")
			}
		})
	}
}

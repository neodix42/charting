package screener

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProviderQueueGrantsInArrivalOrderAndRemovesCancelledHeadAndMiddle(t *testing.T) {
	l, err := newProviderLimiter(filepath.Join(t.TempDir(), "rate.json"))
	if err != nil {
		t.Fatal(err)
	}
	l.interval = 50 * time.Millisecond
	l.state.NextAt = time.Now().Add(time.Hour).UnixMilli()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	t.Cleanup(func() { cancel(); wg.Wait() })
	type result struct {
		index int
		err   error
	}
	done := make(chan result, 8)
	cleared := make(chan int, 8)
	var cancellations []context.CancelFunc
	for i := range 8 {
		child, stop := context.WithCancel(ctx)
		cancellations = append(cancellations, stop)
		registered := make(chan string, 1)
		var once sync.Once
		wg.Go(func() {
			err := l.permit(child, func(message string) {
				if message == "" {
					cleared <- i
					return
				}
				once.Do(func() { registered <- message })
			})
			done <- result{i, err}
		})
		select {
		case message := <-registered:
			if !strings.Contains(message, fmt.Sprintf("%d requests ahead", i)) || !strings.Contains(message, "next turn in about") {
				t.Fatalf("queue position missing: %s", message)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("worker never entered the queue")
		}
	}
	cancellations[0]()
	cancellations[3]()
	cancelled := map[int]bool{}
	for range 2 {
		select {
		case result := <-done:
			if result.err != context.Canceled {
				t.Fatal(result)
			}
			cancelled[result.index] = true
		case <-time.After(time.Second):
			t.Fatal("cancelled worker did not leave promptly")
		}
	}
	if !cancelled[0] || !cancelled[3] {
		t.Fatal(cancelled)
	}
	l.mu.Lock()
	l.state.NextAt = 0
	l.notifyLocked()
	l.mu.Unlock()
	for _, want := range []int{1, 2, 4, 5, 6, 7} {
		select {
		case result := <-done:
			if result.err != nil || result.index != want {
				t.Fatalf("newer worker overtook an earlier waiter: got %+v, want %d", result, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("a cancelled ticket blocked the queue")
		}
	}
	if len(cleared) != 8 {
		t.Fatalf("stale wait status after grant/cancel: %d clear notifications", len(cleared))
	}
}

func TestProductionH1DownloadKeepsFileProgressDuringQueueAndClearsWaitBeforeNetworkResponse(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	provider := filepath.Join(m.profile, "screener-provider")
	if err := os.MkdirAll(provider, 0700); err != nil {
		t.Fatal(err)
	}
	cache := `{"version":2,"origin":"https://jetta.dukascopy.com","token":"offline-progress","userAgent":"CryptoDesk isolated progress test"}`
	if err := os.WriteFile(filepath.Join(provider, "waf-token.json"), []byte(cache), 0600); err != nil {
		t.Fatal(err)
	}
	trace := t.TempDir()
	traceJSON, _ := json.Marshal(trace)
	preload := filepath.Join(trace, "transport.cjs")
	script := fmt.Sprintf(`const fs=require('node:fs'),path=require('node:path');
const trace=%s;let count=0;
global.fetch=async function(url,options){
 if(options.headers.cookie!=='aws-waf-token=offline-progress')throw Error('Offline session was not reused');
 const [year,month]=new URL(url).pathname.split('/').slice(-2).map(Number);
 if(++count===2){fs.writeFileSync(path.join(trace,'network-started'),'yes');while(!fs.existsSync(path.join(trace,'release')))await new Promise(resolve=>setTimeout(resolve,10));}
 return new Response(JSON.stringify({timestamp:Date.UTC(year,month,1),shift:3600000,multiplier:1,
 open:10,high:11,low:9,close:10,times:[0],opens:[0],highs:[0],lows:[0],closes:[0],volumes:[1]}),{headers:{'content-type':'application/json'}});
};`, traceJSON)
	if err := os.WriteFile(preload, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NODE_OPTIONS", "--require="+preload)
	if _, err := m.Start(Request{Symbols: []string{"eurusd"}, Timeframe: "h1", From: "2024-01-01", To: "2024-03-01", Concurrency: 1}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	sawQueue := false
	for time.Now().Before(deadline) {
		job := m.Info().Jobs[0]
		if strings.Contains(job.Message, "requests ahead") {
			if !strings.Contains(job.Message, "Fetched 1 of 2 source files") || job.Progress < 40 {
				t.Fatalf("queue replaced real progress: %+v", job)
			}
			sawQueue = true
		}
		if _, err := os.Stat(filepath.Join(trace, "network-started")); err == nil {
			if !sawQueue || strings.Contains(job.Message, "Waiting for shared") || !strings.Contains(job.Message, "Fetched 1 of 2 source files") {
				t.Fatalf("wait survived the grant: %+v; sawQueue=%v", job, sawQueue)
			}
			break
		}
		if job.Status == "failed" {
			t.Fatal(job)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(trace, "network-started")); err != nil {
		t.Fatalf("second hourly source never started: %+v", m.Info())
	}
	if err := os.WriteFile(filepath.Join(trace, "release"), []byte("release"), 0600); err != nil {
		t.Fatal(err)
	}
	info := waitFinished(t, m)
	if job := info.Jobs[0]; job.Status != "completed" || job.Progress != 100 || strings.Contains(job.Message, "Waiting for shared") {
		t.Fatalf("hourly download failed: %+v", job)
	}
}

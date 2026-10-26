package screener

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProviderSessionPersistsCacheAndDiscardsOnlyBatchGates(t *testing.T) {
	profile := t.TempDir()
	ctx, cleanup, err := withProviderSession(context.Background(), profile)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	state := sessionFromContext(ctx)
	if err = os.WriteFile(state.TokenCache, []byte("supported cache fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	cleanup()
	if _, err = os.Stat(state.Directory); !os.IsNotExist(err) {
		t.Fatal("batch gate survived completion", err)
	}
	next, closeNext, err := withProviderSession(context.Background(), profile)
	if err != nil {
		t.Fatal(err)
	}
	defer closeNext()
	if sessionFromContext(next).TokenCache != state.TokenCache || sessionFromContext(next).Directory == state.Directory {
		t.Fatal("cache was not reused independently of batch gates")
	}
	data, err := os.ReadFile(state.TokenCache)
	if err != nil || string(data) != "supported cache fixture" {
		t.Fatal("batch cleanup discarded the supported session cache", err)
	}
}

func TestProviderBrowserDenialStopsRefreshAndDownloadWithoutChangingOriginals(t *testing.T) {
	for _, operation := range []string{"refresh", "new-download", "incremental-download"} {
		t.Run(operation, func(t *testing.T) {
			m := sourceEnrichmentManager(t, false)
			originals := map[string]string{}
			if operation != "new-download" {
				for _, symbol := range []string{"eurusd", "gbpusd"} {
					name := symbol + "-d1-2024-01-02-2024-01-04.csv"
					originals[writeDataFile(t, m, name, refreshCSV)] = refreshCSV
				}
			}
			folder := t.TempDir()
			trace := filepath.Join(folder, "trace")
			module, _ := json.Marshal(filepath.Join(filepath.Dir(m.runtime.worker), "node_modules", "dukascopy-node"))
			traceJS, _ := json.Marshal(trace)
			preload := filepath.Join(folder, "denied.cjs")
			bootstrap := fmt.Sprintf(`const fs=require('node:fs');const api=require(%s);
api.BufferFetcher.prototype.fetchBuffer=async function(url){fs.appendFileSync(%s,url+'\n');throw new api.WafTokenError('Headless browser could not acquire a WAF token for '+url+' (last HTTP status: 403, WAF action: none)');};`, module, traceJS)
			if err := os.WriteFile(preload, []byte(bootstrap), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("NODE_OPTIONS", "--require="+preload)
			var messages []string
			if operation == "refresh" {
				if _, err := m.StartRefresh(); err != nil {
					t.Fatal(err)
				}
				page, err := m.Files(0, 20)
				if err != nil {
					t.Fatal(err)
				}
				ids := []string{page.Files[0].ID, page.Files[1].ID}
				info := waitRefresh(t, m, ids)
				if info.Failed != 2 || info.Completed != 0 {
					t.Fatalf("denied refresh published data: %+v", info)
				}
				for _, job := range info.Jobs {
					messages = append(messages, job.Message)
				}
			} else {
				if _, err := m.Start(Request{Symbols: []string{"eurusd", "gbpusd"}, Timeframe: "d1", From: "2024-01-01", To: "2024-01-05", Concurrency: 20}); err != nil {
					t.Fatal(err)
				}
				for _, job := range waitFinished(t, m).Jobs {
					if job.Status != "failed" || job.OutputPath != "" {
						t.Fatalf("denied download published data: %+v", job)
					}
					messages = append(messages, job.Message)
				}
			}
			for _, message := range messages {
				if !strings.Contains(message, "Dukascopy refused access (HTTP 403)") {
					t.Fatal("missing useful access-denial error", message)
				}
			}
			data, err := os.ReadFile(trace)
			if err != nil || strings.Count(string(data), "\n") != 1 {
				t.Fatalf("browser denial was retried across workers: %s (%v)", data, err)
			}
			for filename, before := range originals {
				after, err := os.ReadFile(filename)
				if err != nil || string(after) != before {
					t.Fatal("provider failure changed the original CSV", filename, err)
				}
			}
			entries, err := os.ReadDir(filepath.Join(m.profile, "screener-provider"))
			if err != nil || len(entries) != 1 || entries[0].Name() != "rate-limit.json" {
				t.Fatal("failed batch retained its access-denial gate", entries, err)
			}
			limiter, err := newProviderLimiter(filepath.Join(m.profile, "screener-provider", "rate-limit.json"))
			if err != nil || !strings.Contains(limiter.permit(context.Background(), nil).Error(), "no request sent") {
				t.Fatal("access cooldown did not survive a new batch", err)
			}
		})
	}
}

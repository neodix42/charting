//go:build desktop_smoke

package main

import (
	"context"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/storage"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// These methods exist only in explicitly tagged verification binaries. The
// normal application does not expose report writing or automation hooks.
func (a *App) DesktopSmokeReport(report string) error {
	destination := os.Getenv("CRYPTODESK_SMOKE_REPORT")
	if !filepath.IsAbs(destination) || len(report) > 256*1024 || !json.Valid([]byte(report)) {
		return errors.New("invalid desktop smoke report")
	}
	return os.WriteFile(destination, []byte(report), 0600)
}
func (a *App) DesktopSmokeClose() { runtime.Quit(a.ctx) }
func desktopSmokePrepare(store *storage.Store) error {
	if os.Getenv("CRYPTODESK_SMOKE_PHASE") != "capacity" {
		return nil
	}
	source := os.Getenv("CRYPTODESK_SMOKE_IMPORT")
	if !filepath.IsAbs(source) {
		return errors.New("capacity smoke requires an explicit absolute fixture path")
	}
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, domain.MaxImportBytes+1))
	if err != nil {
		return err
	}
	workspace, err := domain.DecodeWorkspace(data)
	if err != nil {
		return err
	}
	loaded, err := store.Load()
	if err != nil {
		return err
	}
	_, err = store.Import(loaded.Revision, workspace)
	return err
}
func (a *App) DesktopSmokeMetrics() map[string]any {
	goruntime.GC()
	var memory goruntime.MemStats
	goruntime.ReadMemStats(&memory)
	ticksPerSecond := 100.0
	if raw, err := exec.Command("getconf", "CLK_TCK").Output(); err == nil {
		if n, e := strconv.ParseFloat(strings.TrimSpace(string(raw)), 64); e == nil && n > 0 {
			ticksPerSecond = n
		}
	}
	var rssKB, cpuTicks uint64
	processes := 0
	seenProcesses := map[int]bool{}
	var inspect func(int, int)
	inspect = func(pid, depth int) {
		if depth > 16 || processes >= 128 || seenProcesses[pid] {
			return
		}
		seenProcesses[pid] = true
		raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			return
		}
		fields := strings.Fields(string(raw)[strings.LastIndex(string(raw), ")")+1:])
		if len(fields) < 22 {
			return
		}
		user, _ := strconv.ParseUint(fields[11], 10, 64)
		system, _ := strconv.ParseUint(fields[12], 10, 64)
		waitedUser, _ := strconv.ParseUint(fields[13], 10, 64)
		waitedSystem, _ := strconv.ParseUint(fields[14], 10, 64)
		cpuTicks += user + system + waitedUser + waitedSystem
		processes++
		status, _ := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
		for _, line := range strings.Split(string(status), "\n") {
			if strings.HasPrefix(line, "VmRSS:") {
				values := strings.Fields(line)
				if len(values) > 1 {
					n, _ := strconv.ParseUint(values[1], 10, 64)
					rssKB += n
				}
				break
			}
		}
		// GTK/WebKit can create children from any OS thread, not only the
		// process leader. Looking at task/PID/children alone omits those helpers.
		tasks, _ := os.ReadDir(fmt.Sprintf("/proc/%d/task", pid))
		for _, task := range tasks {
			children, _ := os.ReadFile(fmt.Sprintf("/proc/%d/task/%s/children", pid, task.Name()))
			for _, child := range strings.Fields(string(children)) {
				if n, e := strconv.Atoi(child); e == nil {
					inspect(n, depth+1)
				}
			}
		}
	}
	inspect(os.Getpid(), 0)
	return map[string]any{"at": time.Now().UTC().Format(time.RFC3339Nano), "goHeapBytes": memory.HeapAlloc, "goHeapInUseBytes": memory.HeapInuse, "goroutines": goruntime.NumGoroutine(), "processTreeRSSBytes": rssKB * 1024, "processTreeCPUSeconds": float64(cpuTicks) / ticksPerSecond, "processes": processes, "rssAccounting": "sum of all OS-thread descendants; shared pages counted per process", "cpuAccounting": "active process CPU plus waited-child CPU", "goVersion": goruntime.Version(), "logicalCPUs": goruntime.NumCPU(), "sessionType": os.Getenv("XDG_SESSION_TYPE"), "waylandDisplay": os.Getenv("WAYLAND_DISPLAY"), "display": os.Getenv("DISPLAY")}
}
func desktopSmoke(ctx context.Context, a *App) {
	phase := os.Getenv("CRYPTODESK_SMOKE_PHASE")
	if phase != "fresh" && phase != "restore" && phase != "capacity" {
		return
	}
	encoded, _ := json.Marshal(phase)
	runtime.WindowExecJS(ctx, "("+desktopSmokeJavaScript+")("+string(encoded)+")")
}

const desktopSmokeJavaScript = `async function(phase) {
 const report={phase,startedAt:new Date().toISOString(),userAgent:navigator.userAgent,checks:[],passed:false};
 const sleep=ms=>new Promise(resolve=>setTimeout(resolve,ms));
 const wait=async(predicate,label,timeout=20000)=>{const start=Date.now();while(Date.now()-start<timeout){const result=predicate();if(result)return result;await sleep(50);}throw Error('Timed out: '+label);};
 const check=(condition,name,actual)=>{report.checks.push({name,passed:!!condition,actual});if(!condition)throw Error(name+': '+JSON.stringify(actual));};
 const q=selector=>document.querySelector(selector),qa=selector=>Array.from(document.querySelectorAll(selector));
 const click=async(selector)=>{const element=await wait(()=>q(selector),selector);if(element.disabled)throw Error('Disabled control: '+selector);element.click();await sleep(100);};
 const select=async(selector,value)=>{const element=await wait(()=>q(selector),selector);Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,'value').set.call(element,value);element.dispatchEvent(new Event('change',{bubbles:true}));await sleep(100);};
 const flush=async()=>{await click('.save-indicator');await wait(()=>q('.save-indicator')?.textContent.includes('All changes saved'),'acknowledged save');};
 let api;
 try {
  await wait(()=>window.go?.main?.App&&q('[data-testid="chart-cell"]'),'native bridge and chart');api=window.go.main.App;
  check(!window.__CRYPTO_TEST__,'production public-data mode',!!window.__CRYPTO_TEST__);
  const initial=await api.LoadWorkspace();
  report.initialCounts={tabs:initial.workspace.tabs.length,charts:initial.workspace.tabs.map(tab=>tab.charts.length)};
  report.initialMetrics=await api.DesktopSmokeMetrics();
  if(phase==='fresh'){
   check(qa('[role="tab"]').length===1&&qa('[data-testid="chart-cell"]').length===1,'first launch exactly one tab and chart',report.initialCounts);
   check(initial.workspace.tabs[0].grid.rows===1&&initial.workspace.tabs[0].grid.columns===1,'initial 1×1 grid',initial.workspace.tabs[0].grid);
   await select('[aria-label="Grid layout"]','3x3');
   check(qa('[data-testid="chart-cell"]').length===1&&qa('[data-testid="chart-placeholder"]').length===8,'3×3 adds eight placeholders only',{charts:qa('[data-testid="chart-cell"]').length,placeholders:qa('[data-testid="chart-placeholder"]').length});
   await click('[aria-label="Add chart"]');await click('[aria-label="Add chart"]');
   check(qa('[data-testid="chart-cell"]').length===3,'each Add chart creates exactly one chart',qa('[data-testid="chart-cell"]').length);
   await select('[data-testid="chart-cell"] [aria-label="Timeframe"]','15m');
   await click('[data-testid="chart-cell"] [aria-label="Toggle volume"]');
   await click('[aria-label="Add tab"]');
   await wait(()=>qa('[role="tab"]').length===2,'second tab');
   check(qa('[data-testid="chart-cell"]').length===1&&q('[aria-label="Grid layout"]').value==='1x1','new tab has exactly one chart and 1×1 grid',{charts:qa('[data-testid="chart-cell"]').length,grid:q('[aria-label="Grid layout"]').value});
   qa('[role="tab"]')[0].click();await wait(()=>qa('[data-testid="chart-cell"]').length===3,'restore first tab');
   await flush();
   const saved=await api.LoadWorkspace();
   check(saved.workspace.tabs.length===2&&saved.workspace.tabs[0].charts.length===3&&saved.workspace.tabs[1].charts.length===1,'small workspace saved with exact counts',saved.workspace.tabs.map(tab=>tab.charts.length));
   check(saved.workspace.tabs[0].charts[0].timeframe==='15m'&&!saved.workspace.tabs[0].charts[0].showVolume,'independent chart settings saved',saved.workspace.tabs[0].charts[0]);
   report.savedRevision=saved.revision;
  } else if(phase==='restore') {
   check(initial.workspace.tabs.length===2&&initial.workspace.tabs[0].charts.length===3&&initial.workspace.tabs[1].charts.length===1,'restart restores actual 3+1 chart workspace',report.initialCounts);
   check(initial.workspace.activeTabId===initial.workspace.tabs[0].id,'active tab restored',initial.workspace.activeTabId);
   check(qa('[data-testid="chart-cell"]').length===3&&qa('[data-testid="chart-placeholder"]').length===6,'only selected tab is mounted after restart',{charts:qa('[data-testid="chart-cell"]').length,placeholders:qa('[data-testid="chart-placeholder"]').length});
   check(initial.workspace.tabs[0].charts[0].timeframe==='4h'&&!initial.workspace.tabs[0].charts[0].showVolume,'native close flushed the final immediate edit',initial.workspace.tabs[0].charts[0]);
  }
  else if(phase==='capacity') {
   check(initial.workspace.tabs.length===20&&initial.workspace.tabs.every(tab=>tab.charts.length===9),'explicit imported maximum restores20tabs180charts',report.initialCounts);
   check(qa('[data-testid="chart-cell"]').length===9&&qa('[data-testid="chart-canvas"]').length===9,'maximum workspace mounts nine chart instances only',{cells:qa('[data-testid="chart-cell"]').length,canvases:qa('[data-testid="chart-canvas"]').length});
   check(q('[aria-label="Add tab"]').disabled&&q('[aria-label="Duplicate tab"]').disabled&&q('[aria-label="Add chart"]').disabled,'capacity controls disabled',{addTab:q('[aria-label="Add tab"]').disabled,duplicate:q('[aria-label="Duplicate tab"]').disabled,addChart:q('[aria-label="Add chart"]').disabled});
   report.switchMetrics=[];report.switchLatenciesMS=[];const switchStart=performance.now();
   for(let index=0;index<200;index++) {
    const target=(index+1)%20,started=performance.now();
    qa('[role="tab"]')[target].click();
    await wait(()=>q('[role="tab"][aria-selected="true"]')===qa('[role="tab"]')[target]&&q('[data-testid="chart-cell"]')?.dataset.chartId===initial.workspace.tabs[target].charts[0].id,'native tab switch '+index);
    if(qa('[data-testid="chart-cell"]').length!==9)throw Error('Chart instance count after switch '+index);
    report.switchLatenciesMS.push(performance.now()-started);
    if((index+1)%50===0)report.switchMetrics.push({switches:index+1,...await api.DesktopSmokeMetrics(),backend:await api.GetDiagnostics(),frontend:window.__cryptodeskDiagnostics?.()});
   }
   report.switchDurationMS=performance.now()-switchStart;
   const latencies=[...report.switchLatenciesMS].sort((a,b)=>a-b);report.switchLatency={min:latencies[0],median:latencies[100],p95:latencies[190],max:latencies[199]};
   await sleep(2400);await flush();
   const after=await api.GetDiagnostics(),front=window.__cryptodeskDiagnostics?.();
   check(after.market.activeLeases===9&&front.chartInstances===9&&front.consumers===9&&front.bufferedEvents===0,'200 switches release inactive chart leases and callbacks',{backend:after,frontend:front});
   report.finalMetrics=await api.DesktopSmokeMetrics();
  }
  report.frontend=window.__cryptodeskDiagnostics?.();report.backend=await api.GetDiagnostics();
  report.feedStatuses=qa('.chart-status').map(element=>element.textContent);
  report.passed=true;
 } catch(error) {report.error=String(error?.stack||error);}
 report.finishedAt=new Date().toISOString();
 if(!api)api=window.go?.main?.App;
 if(api){
  await api.DesktopSmokeReport(JSON.stringify(report,null,2));
  if(phase==='fresh'&&report.passed){
   // Deliberately omit an explicit save: native prepare-close must capture and
   // acknowledge this last edit, which the restart phase verifies.
   await select('[data-testid="chart-cell"] [aria-label="Timeframe"]','4h');
  }
  await api.DesktopSmokeClose();
 }
}`

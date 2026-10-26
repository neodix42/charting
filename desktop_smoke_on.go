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
	return writeDesktopSmokeReport(report)
}
func writeDesktopSmokeReport(report string) error {
	destination := os.Getenv("CRYPTODESK_SMOKE_REPORT")
	if !filepath.IsAbs(destination) || len(report) > 256*1024 || !json.Valid([]byte(report)) {
		return errors.New("invalid desktop smoke report")
	}
	return os.WriteFile(destination, []byte(report), 0600)
}
func (a *App) DesktopSmokeClose() { runtime.Quit(a.ctx) }

// Use trusted X11 mouse input so this check would also expose a native GTK popup
// accidentally opened by the original select's default pointer behavior.
func (a *App) DesktopSmokeThemePointer(x, y int) error {
	phase := os.Getenv("CRYPTODESK_SMOKE_PHASE")
	if (phase != "themes" && phase != "themes-restore") || x < 0 || y < 0 || x > 4096 || y > 4096 {
		return errors.New("invalid theme smoke pointer")
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "python3", "-c", desktopThemePointer, fmt.Sprintf("CryptoDesk Themes Smoke %d", os.Getpid()), strconv.Itoa(x), strconv.Itoa(y))
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("native theme pointer: %w: %s", err, output)
	}
	return nil
}

const desktopThemePointer = `import ctypes,ctypes.util,os,re,subprocess,sys
title,x,y=sys.argv[1],int(sys.argv[2]),int(sys.argv[3])
info=subprocess.check_output(['xwininfo','-name',title],env={**os.environ,'LC_ALL':'C'},text=True)
number=lambda label:int(re.search(label+r':\s*(-?\d+)',info).group(1))
assert 0<=x<number('Width') and 0<=y<number('Height'),'pointer outside isolated window'
window=int(re.search(r'Window id: (0x[0-9a-fA-F]+)',info).group(1),16)
x11=ctypes.CDLL(ctypes.util.find_library('X11'));xtst=ctypes.CDLL(ctypes.util.find_library('Xtst'))
x11.XOpenDisplay.argtypes=[ctypes.c_char_p];x11.XOpenDisplay.restype=ctypes.c_void_p
x11.XRaiseWindow.argtypes=[ctypes.c_void_p,ctypes.c_ulong]
x11.XSync.argtypes=[ctypes.c_void_p,ctypes.c_int]
x11.XFlush.argtypes=[ctypes.c_void_p];x11.XCloseDisplay.argtypes=[ctypes.c_void_p]
xtst.XTestFakeMotionEvent.argtypes=[ctypes.c_void_p,ctypes.c_int,ctypes.c_int,ctypes.c_int,ctypes.c_ulong]
xtst.XTestFakeButtonEvent.argtypes=[ctypes.c_void_p,ctypes.c_uint,ctypes.c_int,ctypes.c_ulong]
display=x11.XOpenDisplay(None);assert display,'cannot open X11 display'
try:
 x11.XRaiseWindow(display,window)
 assert xtst.XTestFakeMotionEvent(display,-1,number('Absolute upper-left X')+x,number('Absolute upper-left Y')+y,0),'native mouse motion failed'
 assert xtst.XTestFakeButtonEvent(display,1,1,0),'native mouse press failed'
 assert xtst.XTestFakeButtonEvent(display,1,0,0),'native mouse release failed'
 x11.XSync(display,0)
finally:x11.XCloseDisplay(display)
`

func (a *App) DesktopSmokeThemeScreenshot(label string) (string, error) {
	if label != "dark" && label != "light" && label != "matrix" && label != "cyberpunk" && label != "red-sands" && label != "enrichment" && label != "restore" {
		return "", errors.New("invalid theme screenshot label")
	}
	report := os.Getenv("CRYPTODESK_SMOKE_REPORT")
	if !filepath.IsAbs(report) {
		return "", errors.New("theme screenshot requires an absolute smoke report path")
	}
	path := filepath.Join(filepath.Dir(report), "native-theme-"+label+".xwd")
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "xwd", "-name", fmt.Sprintf("CryptoDesk Themes Smoke %d", os.Getpid()), "-silent", "-out", path)
	if output, err := command.CombinedOutput(); err != nil {
		return "", fmt.Errorf("capture native theme window: %w: %s", err, output)
	}
	return path, nil
}
func (a *App) DesktopSmokeCandleRows(key domain.FeedKey, from, to int64) ([]domain.Candle, error) {
	return a.candles.Range(key, from, to, 1000)
}
func (a *App) DesktopSmokeScreenerCSV(symbol string) (string, error) {
	for _, job := range a.screener.Info().Jobs {
		if job.Status == "completed" && job.Symbol == symbol {
			file, err := os.Open(job.OutputPath)
			if err != nil {
				return "", err
			}
			defer file.Close()
			data, err := io.ReadAll(io.LimitReader(file, 1024*1024))
			return string(data), err
		}
	}
	return "", errors.New("no completed Screener download")
}
func (a *App) DesktopSmokeScreenerCheckpoints() map[string]any {
	info := a.screener.Info()
	result := map[string]any{}
	for _, job := range info.Jobs {
		path := filepath.Join(info.Directory, ".cryptodesk-screener-job-"+job.ID, "checkpoint.json")
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(file, 16384))
		_ = file.Close()
		var checkpoint map[string]any
		if err == nil && json.Unmarshal(data, &checkpoint) == nil {
			result[job.Symbol] = checkpoint
		}
	}
	return result
}
func (a *App) DesktopSmokeSignalExpectation() map[string]string {
	return map[string]string{"workspaceId": os.Getenv("CRYPTODESK_SMOKE_EXPECT_WORKSPACE"), "chartId": os.Getenv("CRYPTODESK_SMOKE_EXPECT_CHART"), "timeframe": os.Getenv("CRYPTODESK_SMOKE_EXPECT_TIMEFRAME")}
}
func (a *App) DesktopSmokeMarketExpectation() map[string]string {
	return map[string]string{"workspaceId": os.Getenv("CRYPTODESK_SMOKE_EXPECT_WORKSPACE"), "chartId": os.Getenv("CRYPTODESK_SMOKE_EXPECT_CHART"), "exchange": os.Getenv("CRYPTODESK_SMOKE_EXPECT_EXCHANGE"), "market": os.Getenv("CRYPTODESK_SMOKE_EXPECT_MARKET"), "instrument": os.Getenv("CRYPTODESK_SMOKE_EXPECT_INSTRUMENT"), "version": os.Getenv("CRYPTODESK_VERSION")}
}
func (a *App) DesktopSmokeSignalRetryRequested() bool {
	marker := os.Getenv("CRYPTODESK_SMOKE_SIGNAL_RETRY")
	if !filepath.IsAbs(marker) {
		return false
	}
	info, err := os.Stat(marker)
	return err == nil && info.Mode().IsRegular()
}
func desktopSmokePrepare(store *storage.Store) error {
	if os.Getenv("CRYPTODESK_SMOKE_PHASE") == "signal-early" {
		release := os.Getenv("CRYPTODESK_SMOKE_EARLY_RELEASE")
		if !filepath.IsAbs(release) {
			return errors.New("early signal smoke requires an absolute release marker path")
		}
		loaded, err := store.Load()
		if err != nil {
			return err
		}
		if len(loaded.Workspace.Tabs) != 1 || len(loaded.Workspace.Tabs[0].Charts) != 1 {
			return errors.New("early signal smoke requires an isolated one-chart workspace")
		}
		chart := loaded.Workspace.Tabs[0].Charts[0]
		report, _ := json.Marshal(map[string]any{"phase": "signal-early", "passed": true, "signalReady": true, "earlyBeforeUI": true, "workerPID": os.Getpid(), "workspaceId": loaded.Workspace.ID, "chartId": chart.ID, "originalTimeframe": chart.Timeframe, "expectedTimeframe": chart.Timeframe, "baselineRevision": loaded.Revision, "checks": []map[string]any{{"name": "early signal gate precedes desktop UI startup", "passed": true}}})
		if err := writeDesktopSmokeReport(string(report)); err != nil {
			return err
		}
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			if info, err := os.Stat(release); err == nil {
				if !info.Mode().IsRegular() {
					return errors.New("early signal release marker must be a regular file")
				}
				return nil
			} else if !os.IsNotExist(err) {
				return err
			}
			time.Sleep(10 * time.Millisecond)
		}
		return errors.New("early signal smoke timed out waiting for the harness release marker")
	}
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
	rootPID := os.Getpid()
	if goruntime.GOOS == "linux" && os.Getenv("CRYPTODESK_INTERNAL_GUI_PARENT") == strconv.Itoa(os.Getppid()) {
		rootPID = os.Getppid()
	}
	inspect(rootPID, 0)
	return map[string]any{"at": time.Now().UTC().Format(time.RFC3339Nano), "goHeapBytes": memory.HeapAlloc, "goHeapInUseBytes": memory.HeapInuse, "goroutines": goruntime.NumGoroutine(), "processTreeRootPID": rootPID, "processTreeRSSBytes": rssKB * 1024, "processTreeCPUSeconds": float64(cpuTicks) / ticksPerSecond, "processes": processes, "rssAccounting": "sum of all OS-thread descendants; shared pages counted per process", "cpuAccounting": "active process CPU plus waited-child CPU", "goVersion": goruntime.Version(), "logicalCPUs": goruntime.NumCPU(), "sessionType": os.Getenv("XDG_SESSION_TYPE"), "waylandDisplay": os.Getenv("WAYLAND_DISPLAY"), "display": os.Getenv("DISPLAY")}
}
func desktopSmoke(ctx context.Context, a *App) {
	phase := os.Getenv("CRYPTODESK_SMOKE_PHASE")
	if phase != "fresh" && phase != "restore" && phase != "capacity" && phase != "signal-edit" && phase != "signal-restore" && phase != "signal-early" && phase != "signal-failure" && phase != "markets" && phase != "markets-restore" && phase != "screener" && phase != "screener-resume" && phase != "screener-resume-restore" && phase != "enrichment" && phase != "enrichment-restore" && phase != "analysis-refresh" && phase != "analysis-refresh-restore" && phase != "themes" && phase != "themes-restore" {
		return
	}
	if phase == "themes" || phase == "themes-restore" {
		runtime.WindowSetTitle(ctx, fmt.Sprintf("CryptoDesk Themes Smoke %d", os.Getpid()))
	}
	encoded, _ := json.Marshal(phase)
	runtime.WindowExecJS(ctx, "("+desktopSmokeJavaScript+")("+string(encoded)+")")
}

const desktopSmokeJavaScript = `async function(phase) {
 if(phase==='signal-early')return; // Only the queued external signal may close this scenario.
 const report={phase,startedAt:new Date().toISOString(),userAgent:navigator.userAgent,checks:[],passed:false};
 const sleep=ms=>new Promise(resolve=>setTimeout(resolve,ms));
 const wait=async(predicate,label,timeout=20000)=>{const start=Date.now();while(Date.now()-start<timeout){const result=await predicate();if(result)return result;await sleep(50);}throw Error('Timed out: '+label);};
 const check=(condition,name,actual)=>{report.checks.push({name,passed:!!condition,actual});if(!condition)throw Error(name+': '+JSON.stringify(actual));};
 const q=selector=>document.querySelector(selector),qa=selector=>Array.from(document.querySelectorAll(selector));
 const click=async(selector)=>{const element=await wait(()=>q(selector),selector);if(element.disabled)throw Error('Disabled control: '+selector);element.click();await sleep(100);};
 const select=async(selector,value)=>{const element=await wait(()=>q(selector),selector);Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,'value').set.call(element,value);element.dispatchEvent(new Event('change',{bubbles:true}));await sleep(100);};
 const input=async(selector,value)=>{const element=await wait(()=>q(selector),selector);Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(element,String(value));element.dispatchEvent(new Event('input',{bubbles:true}));element.dispatchEvent(new Event('change',{bubbles:true}));await sleep(100);};
 const flush=async()=>{await click('.save-indicator');await wait(()=>q('.save-indicator')?.textContent.includes('All changes saved'),'acknowledged save');};
 const middleTab=async(element)=>{const rect=element.getBoundingClientRect();element.dispatchEvent(new MouseEvent('auxclick',{bubbles:true,cancelable:true,button:1,clientX:rect.left+rect.width/2,clientY:rect.top+rect.height/2}));await sleep(100);};
 const bounds=element=>{const rect=element.getBoundingClientRect();return {left:rect.left,right:rect.right,top:rect.top,bottom:rect.bottom,width:rect.width,height:rect.height,centerY:(rect.top+rect.bottom)/2};};
 const verifyTabStrip=async(label,overflow)=>{
  const strip=q('.tabs'),list=q('[role="tablist"]'),add=q('[aria-label="Add tab"]'),tabs=qa('[role="tab"][data-tab-id]'),last=tabs.at(-1);
  check(!q('.app-header [aria-label="Rename tab"]')&&!q('.app-header [aria-label="Close tab"]')&&!q('[aria-label="All tabs"]')&&!q('.tab-row .capacity'),'retired header tab actions, dropdown and tab counter are absent');
  check(list.parentElement===strip&&add.parentElement===strip&&list.nextElementSibling===add&&!list.contains(add),'Add tab follows the tab list inside the same scrollable content');
  const previousScroll=strip.scrollLeft;
  if(overflow)strip.scrollLeft=strip.scrollWidth;
  await sleep(100);
  const geometry={row:bounds(q('.tab-row')),strip:bounds(strip),last:bounds(last),add:bounds(add),scrollLeft:strip.scrollLeft,scrollWidth:strip.scrollWidth,clientWidth:strip.clientWidth};
  check(geometry.add.left>=geometry.last.right-1&&geometry.add.left-geometry.last.right<=12&&Math.abs(geometry.add.centerY-geometry.last.centerY)<=2&&geometry.add.left>=geometry.strip.left-1&&geometry.add.right<=geometry.strip.right+1,label+' plus is visible immediately after the last tab',geometry);
  if(overflow)check(strip.scrollWidth>strip.clientWidth&&strip.scrollLeft>0,'maximum-capacity tabs and plus share horizontal scrolling',geometry);
  else check(strip.scrollLeft===0&&geometry.add.right<geometry.row.left+geometry.row.width/2,'first-launch plus stays beside the only tab',geometry);
  strip.scrollLeft=previousScroll;return geometry;
 };
 const indicatorDefaults={sma:{period:20},ema:{period:20},ichimoku:{conversion:9,base:26,spanB:52,displacement:26},macd:{fast:12,slow:26,signal:9}};
 const indicatorNames={sma:'SMA',ema:'EMA',ichimoku:'Ichimoku Cloud',macd:'MACD'};
 const verifyIndicators=(chart,label)=>{const indicators=chart.indicators||[],types=Object.keys(indicatorDefaults);check(indicators.length===4&&indicators.every((indicator,index)=>indicator.type===types[index]&&Object.keys(indicator.parameters).length===Object.keys(indicatorDefaults[indicator.type]).length&&Object.entries(indicatorDefaults[indicator.type]).every(([key,value])=>indicator.parameters[key]===value)&&/^#[0-9a-f]{6}$/i.test(indicator.color)&&(indicator.type==='sma'?indicator.lineWidth===3:indicator.type==='ema'?indicator.lineWidth===1:indicator.lineWidth===undefined))&&new Set(indicators.map(indicator=>indicator.id)).size===4,label,indicators);};
 const verifySharedIndicators=(workspace,label)=>{const charts=workspace.tabs[0].charts,ids=charts.flatMap(chart=>(chart.indicators||[]).map(indicator=>indicator.id));check(charts.map(chart=>chart.indicators?.length||0).join(',')==='5,1,1'&&charts.every(chart=>{const indicator=chart.indicators.at(-1);return indicator.type==='ema'&&indicator.parameters.period===34&&indicator.lineWidth===4&&Object.keys(indicator.parameters).length===1&&/^#[0-9a-f]{6}$/i.test(indicator.color);})&&new Set(ids).size===ids.length,label,charts.map(chart=>chart.indicators));verifyIndicators({...charts[0],indicators:charts[0].indicators.slice(0,4)},'shared action preserves the first chart original four indicators');check(workspace.tabs[1].charts[0].timeframe==='1h'&&!workspace.tabs[1].charts[0].indicators?.length,'shared action leaves the hidden tab unchanged',workspace.tabs[1].charts[0]);};
 const chartShape=element=>{const host=element.querySelector('[data-testid="chart-canvas"]'),table=host?.querySelector('.tv-lightweight-charts > table');return {canvases:host?.querySelectorAll('canvas').length||0,canvasRows:table?Array.from(table.rows).filter(row=>row.querySelector('canvas')).length:0};};
 const paneHeights=element=>{const table=element.querySelector('.tv-lightweight-charts > table');return table?Array.from(table.rows).filter(row=>row.querySelector('canvas')).slice(0,-1).map(row=>row.getBoundingClientRect().height):[];};
 const resizeMACD=async(element,delta)=>{
  const handle=await wait(()=>element.querySelector('.tv-lightweight-charts > table > tr > td[colspan="3"] > div[style*="position: absolute"][style*="row-resize"]'),'real MACD separator'),rect=handle.getBoundingClientRect(),x=rect.left+rect.width/2,y=rect.top+rect.height/2;
  const options={bubbles:true,cancelable:true,view:window,button:0,buttons:1,clientX:x,clientY:y};
  handle.dispatchEvent(new MouseEvent('mousedown',options));
  for(let step=1;step<=10;step++){document.documentElement.dispatchEvent(new MouseEvent('mousemove',{...options,clientY:y+delta*step/10}));await sleep(20);}
  document.documentElement.dispatchEvent(new MouseEvent('mouseup',{...options,buttons:0,clientY:y+delta}));
  document.documentElement.dispatchEvent(new PointerEvent('pointerup',{...options,buttons:0,clientY:y+delta,pointerId:1,pointerType:'mouse'}));
  await sleep(200);
 };
 let api;
 try {
  await wait(()=>window.go?.main?.App&&q('[data-testid="chart-cell"]'),'native bridge and chart');api=window.go.main.App;
  check(!window.__CRYPTO_TEST__,'production public-data mode',!!window.__CRYPTO_TEST__);
  const initial=await api.LoadWorkspace();
  report.initialCounts={tabs:initial.workspace.tabs.length,charts:initial.workspace.tabs.map(tab=>tab.charts.length)};
  report.initialMetrics=await api.DesktopSmokeMetrics();
  if(phase==='themes'||phase==='themes-restore'){
   let trustedPointers=0;report.pointerEvents=[];const trusted=event=>{if(event.isTrusted)trustedPointers++;report.pointerEvents.push({trusted:event.isTrusted,x:event.clientX,y:event.clientY,target:event.target.outerHTML?.slice(0,200)});};document.addEventListener('pointerdown',trusted,true);
   const pointer=async element=>{const rect=element.getBoundingClientRect();await api.DesktopSmokeThemePointer(Math.round(rect.left+rect.width/2),Math.round(rect.top+rect.height/2));};
   const key=(element,value)=>element.dispatchEvent(new KeyboardEvent('keydown',{key:value,bubbles:true,cancelable:true}));
   const rgb=value=>{const probe=document.createElement('span');probe.style.color=value;document.body.appendChild(probe);const result=getComputedStyle(probe).color;probe.remove();return result;};
   const theme=()=>document.documentElement.dataset.theme;
   const rootColor=name=>rgb(getComputedStyle(document.documentElement).getPropertyValue(name).trim());
   const opened=()=>q('.themed-select-menu[role="listbox"]');
   const optionLabel=option=>option?.querySelector('span')?.textContent.trim()||option?.textContent.trim();
   const open=async selector=>{
    const element=await wait(()=>q(selector),selector);element.focus();await pointer(element);
    return wait(opened,'app-rendered opened select menu');
   };
   const choose=async(selector,label)=>{const menu=await open(selector),option=Array.from(menu.querySelectorAll('[role="option"]')).find(item=>optionLabel(item)===label);check(!!option,'opened dropdown contains '+label);await pointer(option);await wait(()=>!opened(),'menu closes after '+label+' selection');await sleep(100);};
   const verifyMenu=async(label)=>{
    const menu=await open('[aria-label="Theme"]'),style=getComputedStyle(menu),options=Array.from(menu.querySelectorAll('[role="option"]')),selected=options.find(option=>option.getAttribute('aria-selected')==='true');
    const details={theme:theme(),menuBackground:style.backgroundColor,menuColor:style.color,panel:rootColor('--panel'),elevated:rootColor('--elevated'),text:rootColor('--text'),selected:optionLabel(selected),options:options.map(option=>({label:optionLabel(option),background:getComputedStyle(option).backgroundColor,color:getComputedStyle(option).color})),bounds:bounds(menu)};
    check(style.backgroundColor===details.elevated||style.backgroundColor===details.panel,label+' opened popup uses the current app surface color',details);
    check(style.color===details.text,label+' opened popup uses the current app text color',details);
    check(options.length===5&&optionLabel(selected)===label,label+' opened popup exposes all themes and marks the saved selection',details);
    if(theme()!=='light'){
     const white=value=>/^rgba?\(255,\s*255,\s*255(?:,\s*1)?\)$/.test(value);
     check(!white(style.backgroundColor)&&details.options.every(option=>!white(option.background)),label+' opened popup and every option have no white native surface',details);
    }
    check(details.options.every(option=>[details.text,rootColor('--accent'),rootColor('--muted')].includes(option.color)),label+' every opened option inherits the current palette text',details);
    check(details.bounds.left>=0&&details.bounds.right<=innerWidth&&details.bounds.top>=0&&details.bounds.bottom<=innerHeight,label+' popup fits the native window',details.bounds);
    await sleep(250);details.screenshot=await api.DesktopSmokeThemeScreenshot(phase==='themes-restore'?'restore':theme());report.themes.push(details);
    key(menu,'Escape');await wait(()=>!opened(),label+' Escape dismisses popup');
   };
   report.themes=[];
   if(phase==='themes-restore')check(initial.workspace.theme==='red-sands'&&theme()==='red-sands','restart restores the selected Red Sands theme in workspace and UI',{saved:initial.workspace.theme,rendered:theme()});
   await click('[aria-label="Open settings"]');
   if(phase==='themes'){
    for(const [id,label] of [['dark','Dark'],['light','Light'],['matrix','Matrix'],['cyberpunk','Cyberpunk'],['red-sands','Red Sands']]){
     await choose('[aria-label="Theme"]',label);await wait(()=>theme()===id,'selected theme '+id);await verifyMenu(label);
     check(window.__cryptodeskDiagnostics?.().chartInstances===1,id+' palette change retains exactly one live chart',window.__cryptodeskDiagnostics?.());
    }
    const selectElement=q('[aria-label="Theme"]');selectElement.focus();key(selectElement,'ArrowDown');const menu=await wait(opened,'keyboard opens app dropdown');
    key(menu,'Home');key(menu,'ArrowDown');key(menu,'ArrowDown');key(menu,'Enter');
    await wait(()=>theme()==='matrix'&&!opened(),'keyboard commits Matrix theme');check(q('[aria-label="Theme"]').value==='matrix','keyboard selection updates the actual native form control');
    await choose('[aria-label="Theme"]','Red Sands');await wait(()=>theme()==='red-sands','restore final Red Sands selection');
   }else{
    await verifyMenu('Red Sands');
   }
   await click('[aria-label="Close settings"]');await flush();
   check((await api.LoadWorkspace()).workspace.theme==='red-sands','Red Sands is acknowledged in the persisted workspace');
   await click('[role="tab"][aria-controls="screener-panel"]');await click('#screener-enrichment-tab');
   await wait(()=>q('[aria-label="Enrichment indicator"]').options.length===42,'full enrichment indicator selector');
   const menu=await open('[aria-label="Enrichment indicator"]'),options=Array.from(menu.querySelectorAll('[role="option"]')),groups=Array.from(menu.querySelectorAll('[role="group"],.themed-select-group'));
   report.enrichmentMenu={theme:theme(),background:getComputedStyle(menu).backgroundColor,color:getComputedStyle(menu).color,options:options.length,groups:groups.map(group=>group.getAttribute('aria-label')||group.textContent.trim().slice(0,80)),bounds:bounds(menu)};
   check(options.length===42&&groups.length>=2,'opened Enrichment menu preserves all indicator options and category groups',report.enrichmentMenu);
   check(getComputedStyle(menu).backgroundColor===rootColor('--elevated')||getComputedStyle(menu).backgroundColor===rootColor('--panel'),'opened Enrichment menu uses Red Sands surface',report.enrichmentMenu);
   if(phase==='themes'){await sleep(250);report.enrichmentMenu.screenshot=await api.DesktopSmokeThemeScreenshot('enrichment');}
   key(menu,'Escape');await wait(()=>!opened(),'Escape dismisses grouped Enrichment dropdown');
   await wait(async()=>window.__cryptodeskDiagnostics?.().chartInstances===0&&(await api.GetDiagnostics()).market.activeLeases===0,'Screener releases all chart renderers and leases');
   check(report.initialCounts.tabs===1&&report.initialCounts.charts.join(',')==='1','theme verification preserves first-launch one-tab one-chart workspace',report.initialCounts);
   report.trustedPointers=trustedPointers;check(trustedPointers>=2,'opened menus were exercised with trusted native mouse input',trustedPointers);document.removeEventListener('pointerdown',trusted,true);
  }else if(phase==='analysis-refresh'||phase==='analysis-refresh-restore'){
   await click('[role="tab"][aria-controls="screener-panel"]');
   if(phase==='analysis-refresh'){
    await click('#screener-downloader-tab');await wait(()=>qa('.screener-instrument').length===1499,'complete native instrument catalog');
    for(const symbol of ['btcusd','ethusd']){await input('[aria-label="Search Dukascopy instruments"]',symbol);await click('.screener-instrument input');}
    await select('[aria-label="Download timeframe"]','d1');await select('[aria-label="Parallel downloads"]','2');
    await input('[aria-label="Download from date"]','2024-01-01');await input('[aria-label="Download to date"]','2024-01-02');
    await click('[aria-label="Download historical data"] button[type="submit"]');
    await wait(async()=>{const value=await api.GetScreener();if(value.jobs.some(job=>job.status==='failed'))throw Error(JSON.stringify(value));return value.jobs.length===2&&value.jobs.every(job=>job.status==='completed');},'two genuine original daily CSVs',90000);
    const downloaded=await api.GetScreener(),initialFiles=await api.GetScreenerFiles(0,50);
    check(downloaded.downloadedSymbols.join(',')==='btcusd,ethusd'&&initialFiles.files.every(file=>file.firstTimestamp===1704067200000&&file.lastTimestamp===1704067200000),'new downloads report published assets and actual CSV times before any refresh',initialFiles);
    await input('[aria-label="Search Dukascopy instruments"]','eurusd');
    await wait(()=>q('.screener-instrument input:not(:disabled)'),'completed download selection controls');await click('.screener-instrument input');
    await click('[aria-label="Select all downloaded assets"]');
    await wait(async()=>{const value=await api.GetScreenerView();return value.symbols.length===2&&value.symbols.includes('btcusd')&&value.symbols.includes('ethusd');},'native downloaded-only selection replaces unrelated symbols despite search');
    check(qa('.screener-instrument input:checked').length===0,'downloaded-only selection excludes the searched undownloaded asset');
    await click('#screener-enrichment-tab');await wait(()=>q('[aria-label="Enrich downloaded data"] button[type="submit"]:not(:disabled)'),'native enrichment ready');
    await input('[aria-label="Period"]','2');await click('[aria-label="Enrich downloaded data"] button[type="submit"]');
    const page=await api.GetScreenerFiles(0,50),ids=page.files.map(file=>file.id);
    await wait(async()=>{const value=await api.GetScreenerEnrichment(ids);if(value.failed)throw Error(JSON.stringify(value));return !value.active&&value.completed===2;},'initial native SMA 2');
    const originals={};for(const symbol of ['btcusd','ethusd'])originals[symbol]=await api.DesktopSmokeScreenerCSV(symbol);
    await wait(()=>Array.from(q('#screener-enrichment').querySelectorAll('button')).find(button=>button.textContent==='Refresh data and indicators'&&!button.disabled),'native Refresh action ready');
    const refreshButton=Array.from(q('#screener-enrichment').querySelectorAll('button')).find(button=>button.textContent==='Refresh data and indicators');refreshButton.click();
    const refreshed=await wait(async()=>{const value=await api.GetScreenerRefresh(ids);if(value.failed)throw Error(JSON.stringify(value));return !value.active&&value.completed===2?value:false;},'real missing/new public data and saved indicator refresh',180000);
    await wait(()=>qa('#screener-enrichment tbody tr').every(row=>row.textContent.includes('Available bar open times')&&row.querySelector('progress')?.value===100),'native refreshed periods and per-file completion');
    const csv={},counts={};
    for(const symbol of ['btcusd','ethusd']){
     csv[symbol]=await api.DesktopSmokeScreenerCSV(symbol);
     const rows=csv[symbol].trim().split('\n'),values=rows.slice(1).map(line=>line.split(','));counts[symbol]=values.length;
     check(rows[0]==='timestamp,open,high,low,close,volume,sma_2'&&values.length>2&&values[0][6]===''&&values.every((row,i)=>i===0||Math.abs(Number(row[6])-(Number(values[i-1][4])+Number(row[4]))/2)<1e-8),symbol+' refresh appends real missing/new bars and calculates the existing SMA column for each new row',{rows:values.length,last:values.at(-1)});
     check(values.every((row,i)=>Number(row[0])+86400000<=refreshed.asOf&&(i===0||Number(row[0])>Number(values[i-1][0]))),symbol+' refreshed candles are closed at the shared execution cutoff, unique and chronological');
     const file=(await api.GetScreenerFiles(0,50)).files.find(file=>file.symbol===symbol);
     check(file.firstTimestamp===Number(values[0][0])&&file.lastTimestamp===Number(values.at(-1)[0])&&file.lastTimestamp>Date.parse(file.to+'T00:00:00Z'),symbol+' UTC period follows the actual appended CSV through its newest bar',file);
    }
    check(refreshed.jobs.every(job=>job.addedRows>0&&job.columns.includes('sma_2')&&job.backupPath),'all refreshed files report appends, applied indicators and retained originals',refreshed.jobs);
    await click('#screener-analysis-tab');await wait(()=>q('[aria-label="Analysis conditions"] button[type="submit"]:not(:disabled)'),'native visual conditions ready');
    await click('.analysis-add');await select('[aria-label="Condition 2 left field"]','sma_2');
    await wait(()=>q('.analysis-save-condition:not(:disabled)'),'native Safe Condition button');await click('.analysis-save-condition');
    await input('[aria-label="Condition name"]','Native two-row condition');await click('[aria-label="Save named condition"] button[type="submit"]');
    await wait(()=>!q('[aria-label="Save named condition"]')&&q('[aria-label="Load condition Native two-row condition"]'),'native named condition saved on the right');
    const named=await api.GetScreenerSavedConditions();
    check(named.length===1&&named[0].request.conditions.length===2&&named[0].request.conditions[1].left.field==='sma_2','Safe Condition saves the complete native row set',named);
    await click('[aria-label="Remove condition 2"]');await click('[aria-label="Load condition Native two-row condition"]');
    await wait(()=>q('[aria-label="Condition 2 left field"]')?.value==='sma_2','native saved condition restores all rows');
    check(!(await api.GetScreenerAnalysis(0,50)).active&&(await api.GetScreenerAnalysis(0,50)).total===0,'loading a named condition waits for Apply');
    await click('[aria-label="Analysis conditions"] button[type="submit"]');
    const analysis=await wait(async()=>{const value=await api.GetScreenerAnalysis(0,50);if(value.failed)throw Error(JSON.stringify(value));return !value.active&&value.total===2&&value.completed===2?value:false;},'every downloaded bar evaluated against both AND conditions');
    check(analysis.results.every(result=>result.matches===counts[result.symbol]-1)&&analysis.totalMatches===counts.btcusd+counts.ethusd-2,'matching asset table counts every occurrence of both conditions',analysis);
    await wait(()=>qa('.analysis-file-match').length===2,'native matching asset rows');await click('.analysis-file-match');
    await wait(()=>q('[role="dialog"][aria-label="Analysis match chart"] [data-testid="analysis-chart"] canvas'),'native bars and enriched indicator chart');
    const first=analysis.results[0],chart=await api.GetScreenerAnalysisChart(first.id,0,120,80);
    check(chart.matchIndex===1&&chart.fields.includes('sma_2')&&chart.bars.length<=201&&chart.bars.some(bar=>bar.index===chart.matchIndex&&Number.isFinite(bar.values.sma_2)),'native popup receives bounded genuine bars and existing enriched values',chart);
    await click('[aria-label="Matching occurrences"] button:last-child');await wait(()=>q('.analysis-chart-modal nav strong')?.textContent.includes('Occurrence 2 of'),'next occurrence label');
    const next=await api.GetScreenerAnalysisChart(first.id,1,120,80);
    await wait(()=>q('.analysis-chart-readout strong')?.textContent.includes(new Date(next.matchTime).toISOString().slice(0,10)),'chart moves to the next occurrence');
    check(next.matchIndex===2&&next.matchTime>chart.matchTime,'Next navigates chronologically to the second match');
    await click('[aria-label="Matching occurrences"] button:first-child');await wait(()=>q('.analysis-chart-modal nav strong')?.textContent.includes('Occurrence 1 of'),'previous occurrence label');
    await click('[aria-label="Close analysis chart"]');await wait(()=>!q('.analysis-chart-modal')&&!q('.tv-lightweight-charts'),'native chart popup unmounts');
    check((await api.GetDiagnostics()).market.activeLeases===0,'historical rule scanning and chart popup create no live chart subscriptions');
    report.analysisRefresh={originals,csv,counts,refreshed,analysis,view:await api.GetScreenerAnalysisView(),named};
    await click('[aria-label="Remove condition 2"]');await wait(async()=>(await api.GetScreenerAnalysisView()).conditions.length===1,'changed draft saved independently of the named snapshot');
   }else{
    await wait(()=>qa('.analysis-condition').length===1&&q('[aria-label="Load condition Native two-row condition"]'),'native restored saved-condition list and independent draft');
    const named=await api.GetScreenerSavedConditions();
    check(named.length===1&&named[0].request.conditions.length===2,'restart preserves named conditions independently of draft edits',named);
    await click('[aria-label="Load condition Native two-row condition"]');await wait(()=>q('[aria-label="Condition 2 left field"]')?.value==='sma_2','native named condition reload after restart');
    await wait(async()=>(await api.GetScreenerAnalysisView()).conditions.length===2,'loaded named condition becomes the editable draft');
    check(!(await api.GetScreenerAnalysis(0,50)).active&&(await api.GetScreenerAnalysis(0,50)).total===0,'restarted named-condition load does not apply automatically');
    const view=await api.GetScreenerAnalysisView(),files=await api.GetScreenerFiles(0,50);
    check(view.conditions.length===2&&view.conditions[0].left.field==='close'&&view.conditions[1].left.field==='sma_2','restart restores both visual AND rows and operands',view);
    check(files.files.length===2&&files.files.every(file=>file.fields.includes('sma_2')&&file.firstTimestamp&&file.lastTimestamp),'restart discovers refreshed periods and saved indicator columns',files);
    const downloadView=await api.GetScreenerView(),downloaded=await api.GetScreener();
    check(downloadView.symbols.length===2&&downloadView.symbols.every(symbol=>downloaded.downloadedSymbols.includes(symbol)),'restart remembers downloaded-only symbol selection and discovers published CSVs',downloaded.downloadedSymbols);
    await click('[aria-label="Analysis conditions"] button[type="submit"]');
    const result=await wait(async()=>{const value=await api.GetScreenerAnalysis(0,50);if(value.failed)throw Error(JSON.stringify(value));return !value.active&&value.completed===2?value:false;},'restarted rule scan');
    check(result.results.every(file=>file.matches>1),'restored conditions scan all saved historical occurrences',result);
    const csv={};for(const symbol of ['btcusd','ethusd'])csv[symbol]=await api.DesktopSmokeScreenerCSV(symbol);
    report.analysisRefresh={view,files,result,csv,named};
   }
  }else if(phase==='enrichment'||phase==='enrichment-restore'){
   await click('[role="tab"][aria-controls="screener-panel"]');
   if(phase==='enrichment'){
    await click('#screener-downloader-tab');await wait(()=>qa('.screener-instrument').length===1499,'full download catalog');
    for(const symbol of ['btcusd','ethusd']){await input('[aria-label="Search Dukascopy instruments"]',symbol);await click('.screener-instrument input');}
    await select('[aria-label="Download timeframe"]','d1');await select('[aria-label="Parallel downloads"]','2');
    await input('[aria-label="Download from date"]','2024-01-01');await input('[aria-label="Download to date"]','2024-01-10');
    await click('[aria-label="Download historical data"] button[type="submit"]');
    const downloaded=await wait(async()=>{const value=await api.GetScreener();if(value.jobs.some(job=>job.status==='failed'))throw Error(JSON.stringify(value));return value.jobs.length===2&&value.jobs.every(job=>job.status==='completed')?value:false;},'two real daily downloads',90000);
    const originals={};for(const symbol of ['btcusd','ethusd'])originals[symbol]=await api.DesktopSmokeScreenerCSV(symbol);
    await click('#screener-enrichment-tab');
    await wait(()=>qa('#screener-enrichment tbody tr').length===2&&q('[aria-label="Enrichment indicator"]').options.length===42,'native file list and indicator catalog');
    const page=await api.GetScreenerFiles(0,50),ids=page.files.map(file=>file.id),runtime=await api.GetScreenerEnrichment(ids);
    check(runtime.runtimeAvailable&&page.files.every(file=>file.from==='2024-01-01'&&file.to==='2024-01-10'&&file.timeframe==='d1'&&file.fields.join(',')==='timestamp,open,high,low,close,volume'),'native Enrichment lists genuine files, periods and CSV headers with preinstalled trading-signals',{runtime,page});
    await input('[aria-label="Period"]','3');await click('[aria-label="Enrich downloaded data"] button[type="submit"]');
    const sma=await wait(async()=>{const value=await api.GetScreenerEnrichment(ids);if(value.failed)throw Error(JSON.stringify(value));return !value.active&&value.completed===2?value:false;},'native SMA enrichment');
    await wait(()=>qa('#screener-enrichment tbody tr').every(row=>row.textContent.includes('sma_3')&&row.querySelector('progress')?.value===100),'native completed enrichment progress and refreshed headers');
    const smaCSV={};
    for(const symbol of ['btcusd','ethusd']){
     smaCSV[symbol]=await api.DesktopSmokeScreenerCSV(symbol);
     const old=originals[symbol].trim().split('\n').slice(1).map(line=>line.split(',')),next=smaCSV[symbol].trim().split('\n');
     check(next[0]==='timestamp,open,high,low,close,volume,sma_3'&&next.length===old.length+1&&next.slice(1).every((line,i)=>{const row=line.split(',');return row.slice(0,6).join(',')===old[i].join(',')&&(i<2?row[6]==='':Math.abs(Number(row[6])-old.slice(i-2,i+1).reduce((sum,item)=>sum+Number(item[4]),0)/3)<1e-8);}),symbol+' SMA values align to unchanged original rows and warm-up cells stay blank',smaCSV[symbol]);
    }
    await select('[aria-label="Enrichment indicator"]','tenkan');await wait(()=>q('[aria-label="Period"]').value==='9','Tenkan default');await click('[aria-label="Enrich downloaded data"] button[type="submit"]');
    const tenkan=await wait(async()=>{const value=await api.GetScreenerEnrichment(ids);if(value.failed)throw Error(JSON.stringify(value));return !value.active&&value.completed===2&&value.indicator==='tenkan'?value:false;},'native Tenkan enrichment');
    await wait(()=>qa('#screener-enrichment tbody tr').every(row=>row.textContent.includes('tenkan_9')&&row.querySelector('progress')?.value===100),'native second enrichment headers');
    const finalCSV={};
    for(const symbol of ['btcusd','ethusd']){
     finalCSV[symbol]=await api.DesktopSmokeScreenerCSV(symbol);
     const old=smaCSV[symbol].trim().split('\n').slice(1).map(line=>line.split(',')),next=finalCSV[symbol].trim().split('\n');
     check(next[0]==='timestamp,open,high,low,close,volume,sma_3,tenkan_9'&&next.slice(1).every((line,i)=>{const row=line.split(',');return row.slice(0,7).join(',')===old[i].join(',')&&(i<8?row[7]==='':Math.abs(Number(row[7])-(Math.max(...old.slice(i-8,i+1).map(item=>Number(item[2])))+Math.min(...old.slice(i-8,i+1).map(item=>Number(item[3]))))/2)<1e-8);}),symbol+' Tenkan values preserve prior enrichment and use only current/past highs and lows',finalCSV[symbol]);
    }
    const tenkanCSV={...finalCSV},weekdayCSV={},calendar={};
    for(const indicator of ['weekday','hour']){
     await select('[aria-label="Enrichment indicator"]',indicator);
     await wait(()=>!q('[aria-label="Period"]')&&q('[aria-label="Enrichment price source"]').disabled,'timestamp-only calendar controls');
     await click('[aria-label="Enrich downloaded data"] button[type="submit"]');
     calendar[indicator]=await wait(async()=>{const value=await api.GetScreenerEnrichment(ids);if(value.failed)throw Error(JSON.stringify(value));return !value.active&&value.completed===2&&value.indicator===indicator?value:false;},'native '+indicator+' enrichment');
     await wait(()=>qa('#screener-enrichment tbody tr').every(row=>row.textContent.includes(indicator)&&row.querySelector('progress')?.value===100),'native '+indicator+' headers');
     for(const symbol of ['btcusd','ethusd']){
      const old=finalCSV[symbol].trim().split('\n'),next=(await api.DesktopSmokeScreenerCSV(symbol)).trim().split('\n');
      check(next[0]===old[0]+','+indicator&&next.length===old.length&&next.slice(1).every((line,i)=>{const cells=line.split(','),date=new Date(Number(cells[0])),expected=indicator==='weekday'?((date.getUTCDay()+6)%7)+1:date.getUTCHours();return cells.slice(0,-1).join(',')===old[i+1]&&Number(cells.at(-1))===expected;}),symbol+' '+indicator+' derives exact UTC timestamp values and preserves all prior fields');
      finalCSV[symbol]=next.join('\n')+'\n';if(indicator==='weekday')weekdayCSV[symbol]=finalCSV[symbol];
     }
    }
    check(sma.jobs.every(job=>job.backupPath)&&tenkan.jobs.every(job=>job.backupPath)&&Object.values(calendar).every(info=>info.jobs.every(job=>job.backupPath)),'each successful enrichment reports an original backup',{sma:sma.jobs,tenkan:tenkan.jobs});
    check((await api.GetDiagnostics()).market.activeLeases===0,'enrichment runs without chart subscriptions');
    report.enrichment={directory:downloaded.directory,originals,smaCSV,tenkanCSV,weekdayCSV,finalCSV,sma,tenkan,calendar};
   }else{
    await click('#screener-enrichment-tab');await wait(()=>qa('#screener-enrichment tbody tr').length===2,'native restored enrichment list');
    const page=await api.GetScreenerFiles(0,50);
    check(page.files.length===2&&page.files.every(file=>file.fields.join(',')==='timestamp,open,high,low,close,volume,sma_3,tenkan_9,weekday,hour'),'restart discovers all enriched files and persisted columns',page);
    check(qa('#screener-enrichment tbody tr').every(row=>row.textContent.includes('sma_3, tenkan_9, weekday, hour')),'native restarted file table shows price and calendar indicators');
    const csv={};for(const symbol of ['btcusd','ethusd'])csv[symbol]=await api.DesktopSmokeScreenerCSV(symbol);
    report.enrichment={page,csv};
   }
  }else if(phase==='screener-resume'||phase==='screener-resume-restore'){
   await click('[role="tab"][aria-controls="screener-panel"]');await click('#screener-downloader-tab');
   await wait(()=>qa('.screener-instrument').length===1499,'native resumed catalog');
   if(phase==='screener-resume'){
    for(const symbol of ['btcusd','ethusd']){await input('[aria-label="Search Dukascopy instruments"]',symbol);await click('.screener-instrument input');}
    await input('[aria-label="Search Dukascopy instruments"]','');
    await select('[aria-label="Download timeframe"]','m1');await select('[aria-label="Parallel downloads"]','2');
    await input('[aria-label="Download from date"]','2024-01-01');await input('[aria-label="Download to date"]','2024-01-10');
    q('.screener-instrument-group summary').click();await sleep(100);
    await click('[aria-label="Download historical data"] button[type="submit"]');
    const checkpoints=await wait(async()=>{const values=await api.DesktopSmokeScreenerCheckpoints();return ['btcusd','ethusd'].every(symbol=>values[symbol]?.nextIndex>0&&values[symbol].nextIndex<values[symbol].totalSources)?values:false;},'both downloads have committed partial CSV checkpoints',90000);
    const info=await api.GetScreener();
    check(info.active&&info.jobs.every(job=>job.status==='running'),'close while both actual parallel downloads are unfinished',info);
    check(Object.values(checkpoints).every(value=>value.bytes>0&&value.rows>0),'real CSV prefixes committed before closing',checkpoints);
    report.checkpoints=checkpoints;report.jobs=info.jobs;report.view=await api.GetScreenerView();
   }else{
    const view=await api.GetScreenerView();
    check(view.symbols.join(',')==='btcusd,ethusd'&&view.timeframe==='m1'&&view.concurrency===2&&view.from==='2024-01-01'&&view.to==='2024-01-10'&&view.collapsedCategories.length===1,'native restart restores exact selections, period, dates, parallelism and category state',view);
    check(q('[aria-label="Download timeframe"]').value==='m1'&&q('[aria-label="Parallel downloads"]').value==='2'&&!q('.screener-instrument-group').open&&qa('.screener-instrument input:checked').length===2,'native restored controls and closed category are rendered correctly');
    const info=await wait(async()=>{const value=await api.GetScreener();if(value.jobs.some(job=>job.status==='failed'))throw Error(JSON.stringify(value));return value.jobs.length===2&&value.jobs.every(job=>job.status==='completed')?value:false;},'saved real downloads resume automatically and finish',90000);
    report.jobs=info.jobs;report.view=view;report.csv={};
    for(const symbol of ['btcusd','ethusd']){
     const csv=await api.DesktopSmokeScreenerCSV(symbol),lines=csv.trim().split('\n'),times=lines.slice(1).map(line=>Number(line.split(',')[0]));
     check(lines[0]==='timestamp,open,high,low,close,volume'&&lines.length>100&&times.every((time,index)=>Number.isInteger(time)&&time>=1704067200000&&time<1704844800000&&(index===0||time>times[index-1])),symbol+' resumed CSV has one header and strictly ordered unique minute candles',{rows:times.length,first:lines[1],last:lines.at(-1)});
     report.csv[symbol]={rows:times.length,first:lines[1],last:lines.at(-1)};
    }
   }
  }else if(phase==='screener'){
   await click('[role="tab"][aria-controls="screener-panel"]');
   await wait(()=>!q('[data-testid="chart-cell"]'),'native Screener chart cleanup');
   await click('#screener-downloader-tab');
   await wait(()=>qa('.screener-instrument').length===1499,'full Dukascopy catalog');
   const runtime=await api.GetScreener();
   check(runtime.runtimeAvailable,'preinstalled native downloader is available',runtime);
   await input('[aria-label="Search Dukascopy instruments"]','btcusd');
   await click('.screener-instrument input');
   await input('[aria-label="Search Dukascopy instruments"]','ethusd');
   await click('.screener-instrument input');
   await select('[aria-label="Download timeframe"]','d1');
   check(q('[aria-label="Parallel downloads"]').options.length===20&&q('[aria-label="Parallel downloads"]').value==='20','parallel download control defaults to twenty and exposes one through twenty');
   await select('[aria-label="Parallel downloads"]','2');
   await input('[aria-label="Download from date"]','2024-01-01');
   await input('[aria-label="Download to date"]','2024-01-03');
   await click('[aria-label="Download historical data"] button[type="submit"]');
   let concurrentObserved=false;
   const finished=await wait(async()=>{
    const value=await api.GetScreener();
    if(value.jobs.filter(job=>job.status==='running').length===2)concurrentObserved=true;
    if(value.jobs.some(job=>job.status==='failed'))throw Error(JSON.stringify(value.jobs));
    return value.jobs.length&&value.jobs.every(job=>job.status==='completed')?value:false;
   },'real native Dukascopy CSV download',90000);
   await wait(()=>qa('.screener-job.completed progress').length===2&&qa('.screener-job.completed progress').every(progress=>progress.value===100),'completed per-symbol native progress');
   check(concurrentObserved&&finished.concurrency===2&&finished.maxConcurrency===20,'two native worker processes download concurrently within the selected limit',finished);
   check(finished.jobs.length===2&&finished.jobs.map(job=>job.symbol).join(',')==='btcusd,ethusd'&&finished.jobs.every(job=>job.timeframe==='d1'&&job.progress===100),'parallel download preserves selected symbols, timeframe and displayed order',finished.jobs);
   const csv={};
   for(const symbol of ['btcusd','ethusd']){
    csv[symbol]=await api.DesktopSmokeScreenerCSV(symbol);const rows=csv[symbol].trim().split('\n');
    check(rows.length===3&&rows[0]==='timestamp,open,high,low,close,volume'&&rows[1].startsWith('1704067200000,')&&rows[2].startsWith('1704153600000,'),symbol+' CSV contains exactly two genuine ordered daily candles in the requested UTC range',csv[symbol]);
   }
   check(finished.jobs.every(job=>job.outputPath.startsWith(finished.directory+'/')),'downloaded files are published inside the applied Screener folder',finished.jobs.map(job=>job.outputPath));
   check((await api.GetDiagnostics()).market.activeLeases===0,'downloader operates without chart subscriptions');
   await api.CancelScreenerDownload();
   check((await api.DesktopSmokeScreenerCSV('btcusd'))===csv.btcusd&&(await api.DesktopSmokeScreenerCSV('ethusd'))===csv.ethusd,'cancellation retains both completed CSV files');
   report.screener={...finished,csv};
  }else if(phase==='markets'||phase==='markets-restore'){
   const expected=await api.DesktopSmokeMarketExpectation(),diagnostics=await api.GetDiagnostics(),cell=q('[data-testid="chart-cell"]');
   check(initial.workspace.tabs.length===1&&initial.workspace.tabs[0].charts.length===1&&qa('[data-testid="chart-cell"]').length===1,'market smoke keeps exactly one tab and chart',report.initialCounts);
   check(initial.workspace.tabs[0].grid.rows===1&&initial.workspace.tabs[0].grid.columns===1,'market smoke keeps the original 1×1 grid',initial.workspace.tabs[0].grid);
   check(!q('.app-header').textContent.toUpperCase().includes('SPOT TERMINAL'),'SPOT TERMINAL label is absent from the native header',q('.app-header').textContent);
   const version=q('[aria-label="Application version"]');
   check(expected.version&&diagnostics.version===expected.version&&version?.textContent==='v'+expected.version&&q('.brand').contains(version),'native header and backend use the built release version',{expected:expected.version,backend:diagnostics.version,header:version?.textContent});
   check(!q('select[aria-label="Market"]')&&!q('select[aria-label="Market type"]')&&q('[aria-label="Selected market"]').tagName==='SPAN','market type appears as a label without a separate selector');
   report.marketSelections=[];report.catalogs=[];
   await api.ConfigureCandleStorage(true,'');
   const selected=()=>q('[data-testid="chart-cell"] [aria-label="Selected market"]')?.textContent;
   const identity=chart=>({exchange:chart.exchange,market:chart.market,instrument:chart.instrument,timeframe:chart.timeframe});
   const verifyPublicBars=async(key,label)=>{
    const entry={...key,label,passed:false};report.marketSelections.push(entry);
    try{
     const now=Math.floor(Date.now()/1000),step={ '1m':60,'5m':300,'15m':900,'1h':3600,'4h':14400,'1d':86400,'1w':604800 }[key.timeframe];
     const history=await api.GetHistory('native-markets-'+Date.now(),key,Math.max(0,now-step*6),now,6);
     check(JSON.stringify(history.feedKey)===JSON.stringify(key),label+' history retains the exact market identity',history.feedKey);
     check(history.bars.length>0&&history.bars.every(bar=>Number(bar.open)>0&&Number(bar.low)>0&&Number(bar.low)<=Number(bar.open)&&Number(bar.high)>=Number(bar.close)&&Number(bar.volume)>=0&&bar.time<now&&bar.time>1230768000),label+' returns genuine public OHLCV bars',{count:history.bars.length,last:history.bars.at(-1)});
     await wait(()=>q('.ohlcv b')&&q('.chart-status > span:nth-child(2)')?.textContent.startsWith('live'),label+' renders real public bars in WebKit',30000);
     const disk=await wait(async()=>{const bars=await api.DesktopSmokeCandleRows(key,0,0);return bars.length?bars:false;},label+' candles persist under their full feed key',10000);
     entry.passed=true;entry.historyBars=history.bars.length;entry.diskBars=disk.length;entry.last=history.bars.at(-1);entry.status=q('.chart-status').textContent;entry.ohlcv=q('.ohlcv').textContent;
    }catch(error){entry.error=String(error);entry.status=q('.chart-status')?.textContent;report.checks.push({name:label+' real public data and rendered candles',passed:false,actual:entry});}
   };
   const choose=async(provider,symbol,market)=>{
    const label=market==='perpetual'?'Perpetual':'Spot';
    await click('[data-testid="chart-cell"] [aria-label="Select instrument"]');
    await input('[aria-label="Search instruments"]',symbol);
    const button=await wait(()=>q('.product-list button[aria-label="'+symbol+' '+label+'"]'),provider+' '+symbol+' '+label+' listed',30000);
    const badge=button.querySelector('.product-market'),details=button.querySelector('.product-details'),geometry={row:bounds(button),badge:bounds(badge),details:bounds(details)};
    check(badge.textContent===label&&geometry.badge.left>=geometry.details.right-1&&geometry.row.right-geometry.badge.right<=30&&Math.abs(geometry.badge.centerY-geometry.details.centerY)<=3,provider+' '+symbol+' '+label+' badge is at the right of its symbol row',geometry);
    button.click();await wait(()=>!q('.instrument-picker')&&selected()===label,provider+' '+symbol+' '+label+' selection');
    await flush();const saved=await api.LoadWorkspace(),chart=saved.workspace.tabs[0].charts[0],key=identity(chart);
    check(chart.exchange===provider&&chart.instrument===symbol&&chart.market===market,provider+' '+symbol+' '+label+' saves the chosen market to SQLite',key);
    await verifyPublicBars(key,provider+' '+symbol+' '+label);
    return saved;
   };
   if(phase==='markets'){
    check(initial.workspace.tabs[0].charts[0].exchange==='binance'&&initial.workspace.tabs[0].charts[0].market==='spot'&&initial.workspace.tabs[0].charts[0].instrument==='BTCUSDT','new native workspace preserves its spot starter',identity(initial.workspace.tabs[0].charts[0]));
    for(const provider of ['binance','bybit']){
     await select('[data-testid="chart-cell"] [aria-label="Exchange"]',provider);
     const list=await api.ListInstruments(provider),bitcoin=list.filter(item=>item.nativeId==='BTCUSDT'),silver=list.find(item=>item.nativeId==='XAGUSDT'&&item.market==='perpetual');
     report.catalogs.push({provider,count:list.length,bitcoin:bitcoin.map(item=>({nativeId:item.nativeId,market:item.market,priceIncrement:item.priceIncrement})),silver,warnings:Array.from(new Set(list.map(item=>item.providerMetadata?.catalogWarning).filter(Boolean)))});
     check(bitcoin.some(item=>item.market==='spot')&&bitcoin.some(item=>item.market==='perpetual')&&!!silver,provider+' combined public catalog contains BTCUSDT Spot, BTCUSDT Perpetual and XAGUSDT Perpetual',report.catalogs.at(-1));
     await click('[data-testid="chart-cell"] [aria-label="Select instrument"]');await input('[aria-label="Search instruments"]','BTCUSDT');
     await wait(()=>q('.product-list button[aria-label="BTCUSDT Spot"]')&&q('.product-list button[aria-label="BTCUSDT Perpetual"]'),provider+' same-symbol spot and perpetual rows together');
     check(qa('.product-list button').filter(button=>button.querySelector('strong')?.textContent==='BTCUSDT').length===2,provider+' displays both native BTCUSDT markets in the same symbol list');
     await click('[aria-label="Close instrument selector"]');
     await choose(provider,'BTCUSDT','spot');await choose(provider,'BTCUSDT','perpetual');await choose(provider,'XAGUSDT','perpetual');
    }
    await flush();const saved=await api.LoadWorkspace(),chart=saved.workspace.tabs[0].charts[0];
    report.savedSelection={workspaceId:saved.workspace.id,chartId:chart.id,...identity(chart),revision:saved.revision};
    check(saved.workspace.id===initial.workspace.id&&chart.id===initial.workspace.tabs[0].charts[0].id&&saved.workspace.tabs.length===1&&saved.workspace.tabs[0].charts.length===1,'changing markets preserves the original workspace and chart identities',report.savedSelection);
   }else{
    const chart=initial.workspace.tabs[0].charts[0];
    check(initial.workspace.id===expected.workspaceId&&chart.id===expected.chartId,'native restart reopens the same workspace and chart',{expected,workspaceId:initial.workspace.id,chartId:chart.id});
    check(chart.exchange===expected.exchange&&chart.market===expected.market&&chart.instrument===expected.instrument&&chart.market==='perpetual','native restart restores the exact saved perpetual selection',identity(chart));
    check(q('[aria-label="Exchange"]').value===expected.exchange&&q('[aria-label="Select instrument"]').textContent.includes(expected.instrument)&&selected()==='Perpetual','WebKit renders the restored provider, symbol and Perpetual label');
    await verifyPublicBars(identity(chart),'restored '+chart.exchange+' '+chart.instrument+' Perpetual');
    await flush();report.savedSelection={workspaceId:initial.workspace.id,chartId:chart.id,...identity(chart),revision:(await api.LoadWorkspace()).revision};
   }
  }else if(phase==='signal-edit'||phase==='signal-failure'){
   check(initial.workspace.tabs.length===1&&initial.workspace.tabs[0].charts.length===1,'signal test uses an isolated one-chart workspace',report.initialCounts);
   await flush();
   const baseline=await api.LoadWorkspace(),original=baseline.workspace.tabs[0].charts[0],next=original.timeframe==='4h'?'15m':'4h';
   const originalSave=api.SaveWorkspace.bind(api),heldSaves=[];let injectedFailures=0;
   if(phase==='signal-failure'){
    // Explicit test-only failure injection. One call rejects; later background
    // autosaves are held so they cannot bypass the paused-close assertion.
    // Retry restores the real Wails/SQLite writer and releases those calls.
    report.syntheticSaveFailure=true;
    api.SaveWorkspace=async(...args)=>{if(!injectedFailures){injectedFailures++;throw Error('Explicit signal-smoke synthetic SaveWorkspace failure');}await new Promise(resolve=>heldSaves.push(resolve));return originalSave(...args);};
   }
   q('[data-testid="chart-cell"] [aria-label="Timeframe '+next+'"]').click();
   await wait(()=>q('[data-testid="chart-cell"] [aria-label="Timeframe '+next+'"]').getAttribute('aria-pressed')==='true'&&q('.save-indicator.pending'),'pending production timeframe edit');
   const persisted=await api.LoadWorkspace();
   check(persisted.workspace.tabs[0].charts[0].timeframe===original.timeframe,'signal is requested before the pending edit is saved',persisted.workspace.tabs[0].charts[0].timeframe);
   report.signalReady=true;report.passed=true;report.workspaceId=baseline.workspace.id;report.chartId=original.id;
   report.originalTimeframe=original.timeframe;report.expectedTimeframe=next;report.baselineRevision=baseline.revision;report.readyAt=new Date().toISOString();
   await api.DesktopSmokeReport(JSON.stringify(report,null,2));
   if(phase==='signal-failure'){
    const retry=await wait(()=>qa('button').find(button=>button.textContent==='Retry save and close'),'paused-close retry after synthetic failure');
    const failedSaved=await api.LoadWorkspace();
    check(injectedFailures===1&&failedSaved.workspace.tabs[0].charts[0].timeframe===original.timeframe,'synthetic save failure preserves the committed workspace while close stays paused',{injectedFailures,savedTimeframe:failedSaved.workspace.tabs[0].charts[0].timeframe});
    report.signalSaveFailureObserved=true;report.injectedFailureCount=injectedFailures;
    await api.DesktopSmokeReport(JSON.stringify(report,null,2));
    const retryDeadline=Date.now()+15000;while(!(await api.DesktopSmokeSignalRetryRequested())){if(Date.now()>retryDeadline)throw Error('Timed out waiting for external signal-failure retry marker');await sleep(25);}
    api.SaveWorkspace=originalSave;report.heldAutosaves=heldSaves.length;report.realBackendRetryRequested=true;
    heldSaves.splice(0).forEach(resolve=>resolve());
    await api.DesktopSmokeReport(JSON.stringify(report,null,2));
    retry.click();
   }
   // An external harness now sends an actual OS signal. No explicit frontend
   // save, normal quit request or fixture bridge may make this test pass.
   return;
  }else if(phase==='signal-restore'){
   const expected=await api.DesktopSmokeSignalExpectation(),restored=initial.workspace.tabs[0]?.charts[0];
   check(initial.workspace.tabs.length===1&&initial.workspace.tabs[0].charts.length===1,'signal restart restores the exact one-chart workspace',report.initialCounts);
   check(initial.workspace.id===expected.workspaceId&&restored.id===expected.chartId,'signal restart reopens the same workspace and chart',{workspaceId:initial.workspace.id,chartId:restored.id});
   check(restored.timeframe===expected.timeframe,'OS signal saved the pending timeframe to SQLite',{expected:expected.timeframe,actual:restored.timeframe});
   check(q('[data-testid="chart-cell"] [aria-label="Timeframe '+expected.timeframe+'"]').getAttribute('aria-pressed')==='true','signal restart renders the durable timeframe selection');
  }else if(phase==='fresh'){
   check(qa('[role="tab"][data-tab-id]').length===1&&qa('[data-testid="chart-cell"]').length===1,'first launch exactly one tab and chart',report.initialCounts);
   check(initial.workspace.tabs[0].grid.rows===1&&initial.workspace.tabs[0].grid.columns===1,'initial 1×1 grid',initial.workspace.tabs[0].grid);
   check(initial.workspace.chartRightGap?.unit==='bars'&&initial.workspace.chartRightGap.value===10,'first launch defaults to a ten-bar right gap',initial.workspace.chartRightGap);
   report.initialTabStrip=await verifyTabStrip('first launch',false);
   await flush();
   const saveGeometry=()=>({save:bounds(q('.save-indicator')),previous:bounds(q('[aria-label="Duplicate tab"]')),next:bounds(q('[aria-label="Open settings"]'))});
   report.saveIndicatorGeometry={saved:saveGeometry(),syntheticFailure:'One explicitly injected SaveWorkspace rejection; retry uses the real native SQLite writer'};
   const nativeSave=api.SaveWorkspace.bind(api),originalPeriod=initial.workspace.tabs[0].charts[0].timeframe,nextPeriod=originalPeriod==='4h'?'15m':'4h';
   let releaseSave;const blockedSave=new Promise(resolve=>{releaseSave=resolve;});
   api.SaveWorkspace=async()=>{await blockedSave;throw Error('Explicit native save-indicator geometry failure');};
   try{
    q('[data-testid="chart-cell"] [aria-label="Timeframe '+nextPeriod+'"]').click();
    await wait(()=>q('.save-indicator.pending'),'pending save geometry');report.saveIndicatorGeometry.pending=saveGeometry();
    await wait(()=>q('.save-indicator.saving'),'saving geometry');report.saveIndicatorGeometry.saving=saveGeometry();
    releaseSave();await wait(()=>q('.save-indicator.error'),'failed save geometry');report.saveIndicatorGeometry.error=saveGeometry();
   }finally{releaseSave();api.SaveWorkspace=nativeSave;}
   await flush();report.saveIndicatorGeometry.recovered=saveGeometry();
   const savedGeometry=report.saveIndicatorGeometry.saved;
   check(['pending','saving','error','recovered'].every(state=>['save','previous','next'].every(control=>['left','right','width'].every(edge=>Math.abs(report.saveIndicatorGeometry[state][control][edge]-savedGeometry[control][edge])<.5))),'save indicator and neighboring controls keep the same geometry across saved, pending, saving and error states',report.saveIndicatorGeometry);
   q('[data-testid="chart-cell"] [aria-label="Timeframe '+originalPeriod+'"]').click();await flush();
   q('[role="tab"][data-tab-id]').dispatchEvent(new MouseEvent('dblclick',{bubbles:true,cancelable:true,button:0}));
   await wait(()=>q('[role="dialog"][aria-label="Rename tab"]'),'native double-click rename dialog');
   await input('[aria-label="Tab name"]','Native renamed tab');await click('[role="dialog"] button[type="submit"]');await flush();
   check((await api.LoadWorkspace()).workspace.tabs[0].name==='Native renamed tab','double-click tab rename persists through native SQLite');
   q('[role="tab"][data-tab-id]').dispatchEvent(new MouseEvent('dblclick',{bubbles:true,cancelable:true,button:0}));
   await input('[aria-label="Tab name"]',initial.workspace.tabs[0].name);await click('[role="dialog"] button[type="submit"]');await flush();
   const screenerTab=q('[role="tab"][aria-controls="screener-panel"]');
   check(!!screenerTab&&!screenerTab.draggable&&!screenerTab.dataset.tabId,'Screener is a fixed tab outside the saved chart-tab list');
   screenerTab.click();
   await wait(()=>q('[id="screener-analysis"]')&&!q('[id="screener-analysis"]').hidden&&!q('[data-testid="chart-cell"]'),'native Screener opens Analysis and unmounts charts');
   await wait(async()=>(await api.GetDiagnostics()).market.activeLeases===0,'Screener releases every chart lease');
   check(q('[id="screener-analysis"]').textContent.includes('Visual condition rows'),'Analysis exposes the visual condition builder');
   await click('#screener-downloader-tab');
   const screenerCatalog=await api.GetScreenerCatalog(),screenerCount=screenerCatalog.groups.reduce((count,group)=>count+group.instruments.length,0);
   await wait(()=>qa('.screener-instrument').length===screenerCount,'complete native Dukascopy catalog');
   check(screenerCount>1000&&screenerCatalog.groups.length>20&&screenerCatalog.timeframes.includes('tick')&&screenerCatalog.timeframes.includes('d1'),'native downloader exposes full grouped instrument catalog and supported timeframes',{version:screenerCatalog.version,groups:screenerCatalog.groups.length,instruments:screenerCount,timeframes:screenerCatalog.timeframes});
   q('[aria-label="Select all symbols"]').click();await sleep(100);
   check(qa('.screener-instrument input:checked').length===screenerCount,'native Select all includes every instrument');
   q('[aria-label="Select all symbols"]').click();
   const screenerDefault=await api.GetScreener();
   check(screenerDefault.directory.endsWith('/screener')&&screenerDefault.jobs.length===0,'isolated native Screener defaults to its profile data folder',screenerDefault);
   q('[role="tab"][data-tab-id]').click();await wait(()=>q('[data-testid="chart-cell"]'),'chart restored after Screener');
   check((await api.LoadWorkspace()).workspace.activeTabId===initial.workspace.activeTabId,'Screener preserves the actual saved chart-tab selection');
   await click('[aria-label="Open settings"]');
   await click('#settings-tab-screener');
   await wait(()=>q('[aria-label="Screener download folder"]:not(:disabled)'),'native Screener settings');
   await input('[aria-label="Screener download folder"]',screenerDefault.directory+'-selected');
   await click('[aria-label="Apply Screener download folder"]');
   await wait(async()=>(await api.GetScreener()).directory===screenerDefault.directory+'-selected','native Screener folder preference committed');
   report.screener={directory:(await api.GetScreener()).directory,catalogVersion:screenerCatalog.version,instruments:screenerCount};
   await click('#settings-tab-storage');
   await wait(()=>q('.candle-storage-toggle input:not(:disabled)'),'native candle storage settings');
   const storageDefault=await api.GetCandleStorage();
   check(!q('.candle-storage-toggle input').checked&&!storageDefault.enabled&&storageDefault.directory.endsWith('/candles'),'native candle storage defaults off in the isolated profile',storageDefault);
   q('.candle-storage-toggle input').click();await sleep(100);
   await input('[aria-label="Candle storage folder"]',storageDefault.directory+'-selected');
   await click('[aria-label="Apply candle storage settings"]');
   await wait(()=>q('[aria-label="Applied candle storage settings"]').textContent==='Applied: Enabled','native cache preference acknowledged');
   const storageApplied=await api.GetCandleStorage();
   check(storageApplied.enabled&&storageApplied.directory===storageDefault.directory+'-selected'&&storageApplied.sizeBytes>0&&!storageApplied.error,'native settings open a real candle database and show its measured size',storageApplied);
   const cacheKey={exchange:initial.workspace.tabs[0].charts[0].exchange,market:'spot',instrument:initial.workspace.tabs[0].charts[0].instrument,timeframe:'1h'},cacheTo=Math.floor(Date.now()/3600000)*3600-7200,cacheFrom=cacheTo-14400;
   const downloaded=await api.GetHistory('desktop-cache-real-download',cacheKey,cacheFrom,cacheTo,4);
   const storedBars=await api.DesktopSmokeCandleRows(cacheKey,cacheFrom,cacheTo-1);
   check(downloaded.bars.length>0&&JSON.stringify(storedBars)===JSON.stringify(downloaded.bars),'genuine public history is persisted with exact OHLCV through the native bridge',{key:cacheKey,downloaded:downloaded.bars,stored:storedBars});
   report.candleStorage={...storageApplied,key:cacheKey,from:cacheFrom,to:cacheTo,downloadedBars:downloaded.bars.length};
   await click('[aria-label="Refresh candle storage size"]');
   check(q('[aria-label="Candle storage folder size"]').textContent!=='0 B','native Settings refreshes actual candle folder size',q('[aria-label="Candle storage folder size"]').textContent);
   await click('[aria-label="Close settings"]');
   await middleTab(q('[role="tab"][data-tab-id]'));
   check(qa('[role="tab"][data-tab-id]').length===1&&!q('[role="menu"]')&&!q('[role="dialog"]'),'middle-click protects the final tab without opening a menu or dialog');
   check(q('[aria-label="Remove chart"]').disabled,'chart X protects the final chart');
   if(q('[aria-label="Dismiss notification"]'))await click('[aria-label="Dismiss notification"]');
   await wait(()=>chartShape(q('[data-testid="chart-cell"]')).canvases>0,'initial native chart canvases');
   report.beforeIndicators=chartShape(q('[data-testid="chart-cell"]'));
   const periods=qa('[data-testid="chart-cell"] [aria-label="Timeframe"] button'),tops=periods.map(button=>button.getBoundingClientRect().top);
   check(periods.length===7&&Math.max(...tops)-Math.min(...tops)<1&&!q('select[aria-label="Timeframe"]'),'seven horizontal timeframe buttons replace the dropdown',periods.map(button=>button.textContent));
   await click('[data-testid="chart-cell"] [aria-label="Timeframe 1w"]');
   check(q('[data-testid="chart-cell"] [aria-label="Timeframe 1w"]').getAttribute('aria-pressed')==='true','weekly timeframe selection is visible');
   for(const [type,name] of Object.entries(indicatorNames)){
    const before=qa('[data-testid="chart-cell"] .indicator-chip').length;
    await select('[data-testid="chart-cell"] [aria-label="Indicators"]',type);
    await wait(()=>q('[role="dialog"][aria-label="'+name+' parameters"]'),'native '+name+' parameter dialog');
    check(qa('[data-testid="chart-cell"] .indicator-chip').length===before,name+' waits for parameter confirmation',before);
    if(type==='sma'||type==='ema'){check(q('[role="dialog"] [aria-label="Line width"]').value==='2',name+' native default width is two pixels');await select('[role="dialog"] [aria-label="Line width"]',type==='sma'?'3':'1');}
    await click('[role="dialog"] button[type="submit"]');
    await wait(()=>!q('[role="dialog"]')&&qa('[data-testid="chart-cell"] .indicator-chip').length===before+1,'apply '+name+' to chart');
   }
   await wait(()=>chartShape(q('[data-testid="chart-cell"]')).canvasRows===report.beforeIndicators.canvasRows+1,'MACD separate native pane');
   report.afterIndicators=chartShape(q('[data-testid="chart-cell"]'));
   check(report.afterIndicators.canvases>report.beforeIndicators.canvases,'MACD creates additional pane canvases',report.afterIndicators);
   const beforeResize=paneHeights(q('[data-testid="chart-cell"]'));
   await resizeMACD(q('[data-testid="chart-cell"]'),-85);
   await flush();
   const weeklySaved=await api.LoadWorkspace();
   check(weeklySaved.workspace.tabs[0].charts[0].timeframe==='1w','weekly timeframe persisted through Wails to SQLite',weeklySaved.workspace.tabs[0].charts[0].timeframe);
   verifyIndicators(weeklySaved.workspace.tabs[0].charts[0],'all four indicators and custom SMA/EMA widths persisted through Wails to SQLite');
   const macdSaved=weeklySaved.workspace.tabs[0].charts[0].indicators.find(indicator=>indicator.type==='macd'),resizedHeights=paneHeights(q('[data-testid="chart-cell"]'));
   report.macdPane={before:beforeResize,after:resizedHeights,paneStretchFactor:macdSaved.paneStretchFactor};
   check(Number.isFinite(macdSaved.paneStretchFactor)&&resizedHeights[1]>beforeResize[1]+50&&Math.abs(resizedHeights[1]/resizedHeights[0]-macdSaved.paneStretchFactor)<.025,'resized MACD height persists through Wails to SQLite',report.macdPane);
   report.indicatorRevision=weeklySaved.revision;
   await select('[aria-label="Grid layout"]','3x3');
   check(qa('[data-testid="chart-cell"]').length===1&&qa('[data-testid="chart-placeholder"]').length===8,'3×3 adds eight placeholders only',{charts:qa('[data-testid="chart-cell"]').length,placeholders:qa('[data-testid="chart-placeholder"]').length});
   await click('[aria-label="Add chart"]');await click('[aria-label="Add chart"]');
   check(qa('[data-testid="chart-cell"]').length===3,'each Add chart creates exactly one chart',qa('[data-testid="chart-cell"]').length);
   await click('[data-testid="chart-cell"] [aria-label="Timeframe 15m"]');
   await click('[data-testid="chart-cell"] [aria-label="Toggle volume"]');
   await click('[aria-label="Add tab"]');
   await wait(()=>qa('[role="tab"][data-tab-id]').length===2,'second tab');
   check(qa('[data-testid="chart-cell"]').length===1&&q('[aria-label="Grid layout"]').value==='1x1','new tab has exactly one chart and 1×1 grid',{charts:qa('[data-testid="chart-cell"]').length,grid:q('[aria-label="Grid layout"]').value});
   const keyboardTab=q('[role="tab"][data-tab-id][aria-selected="true"]');keyboardTab.focus();keyboardTab.dispatchEvent(new KeyboardEvent('keydown',{key:'w',ctrlKey:true,bubbles:true,cancelable:true}));
   await wait(()=>q('[role="dialog"].confirmation'),'native Ctrl+W confirmation');
   check(qa('[role="tab"][data-tab-id]').length===2&&q('[role="dialog"]').textContent.includes('Remove this tab and all 1 chart'),'Ctrl+W keeps its whole-tab confirmation after removing the header Close button');
   await click('[role="dialog"] button[type="button"]');
   qa('[role="tab"][data-tab-id]')[0].click();await wait(()=>qa('[data-testid="chart-cell"]').length===3,'restore first tab');
   await flush();
   const saved=await api.LoadWorkspace();
   check(saved.workspace.tabs.length===2&&saved.workspace.tabs[0].charts.length===3&&saved.workspace.tabs[1].charts.length===1,'small workspace saved with exact counts',saved.workspace.tabs.map(tab=>tab.charts.length));
   check(saved.workspace.tabs[0].charts[0].timeframe==='15m'&&!saved.workspace.tabs[0].charts[0].showVolume,'independent chart settings saved',saved.workspace.tabs[0].charts[0]);
   verifyIndicators(saved.workspace.tabs[0].charts[0],'indicator settings survive chart and tab additions');
   check(saved.workspace.tabs[0].charts.slice(1).every(chart=>!chart.indicators?.length)&&!saved.workspace.tabs[1].charts[0].indicators?.length,'new chart indicators remain independent',saved.workspace.tabs.map(tab=>tab.charts.map(chart=>chart.indicators?.length||0)));
   report.savedRevision=saved.revision;
   const shared=await wait(()=>q('[data-testid="tab-chart-controls"]'),'shared current tab controls'),header=q('.app-header');
   const headerControls=[q('.brand'),shared,q('[aria-label="Grid layout"]'),q('[aria-label="Add chart"]'),q('[aria-label="Duplicate tab"]'),q('.save-indicator'),q('[aria-label="Open settings"]')],headerRect=bounds(header),headerBounds=headerControls.map(bounds),tabsRect=bounds(q('.tab-row')),chartsRect=bounds(q('.workspace-content'));
   report.sharedToolbarGeometry={header:headerRect,controls:headerBounds,tabTop:tabsRect.top,tabBottom:tabsRect.bottom,chartTop:chartsRect.top,windowWidth:window.innerWidth};
   check(headerControls.every(element=>header.contains(element))&&headerBounds.every(rect=>rect.top>=headerRect.top&&rect.bottom<=headerRect.bottom&&Math.abs(rect.centerY-headerRect.centerY)<=2)&&headerBounds.slice(1).every((rect,index)=>rect.left>=headerBounds[index].right)&&Math.abs((headerBounds[1].left+headerBounds[1].right)/2-window.innerWidth/2)<=3,'periods, indicators and workspace actions share the top logo row in order',report.sharedToolbarGeometry);
   check(!q('.workspace-title')&&chartsRect.top-tabsRect.bottom<=20,'duplicate workspace title row is removed and charts follow the tabs',report.sharedToolbarGeometry);
   await click('[data-testid="chart-cell"] [aria-label="Maximize chart"]');
   await click('[data-testid="tab-chart-controls"] [aria-label="Apply 1w to current tab"]');
   await wait(()=>qa('[data-testid="chart-cell"] [aria-label="Timeframe 1w"][aria-pressed="true"]').length===3,'shared weekly timeframe reaches all three charts while maximized');
   await flush();
   const sharedPeriodSaved=await api.LoadWorkspace();
   check(sharedPeriodSaved.workspace.tabs[0].charts.every(chart=>chart.timeframe==='1w')&&sharedPeriodSaved.workspace.tabs[1].charts[0].timeframe==='1h','shared weekly timeframe persists for the active tab only while maximized',sharedPeriodSaved.workspace.tabs.map(tab=>tab.charts.map(chart=>chart.timeframe)));
   await select('[data-testid="tab-chart-controls"] [aria-label="Indicators for current tab"]','ema');
   await wait(()=>q('[role="dialog"][aria-label="EMA parameters"]'),'shared EMA parameter dialog');
   check(qa('[role="dialog"]').length===1&&qa('[data-testid="chart-cell"] .indicator-chip').length===4,'shared indicator opens one dialog and waits for OK',qa('[role="dialog"]').length);
   await input('[role="dialog"] [aria-label="Period"]',34);
   await select('[role="dialog"] [aria-label="Line width"]','4');
   await click('[role="dialog"] button[type="submit"]');
   await wait(()=>!q('[role="dialog"]')&&qa('[data-testid="chart-cell"] .indicator-chip').length===7,'shared EMA applies to all three charts');
   await click('[data-testid="chart-cell"] [aria-label="Restore chart"]');
   await flush();
   const sharedSaved=await api.LoadWorkspace();
   verifySharedIndicators(sharedSaved.workspace,'shared EMA custom parameters persist with a distinct ID on every active chart');
   report.sharedRevision=sharedSaved.revision;
   await click('[aria-label="Open settings"]');
   check(q('[aria-label="Chart right gap"]').value==='10'&&q('[aria-label="Chart right gap unit"]').value==='bars','native Settings shows the default ten-bar gap');
   await input('[aria-label="Chart right gap"]',29);
   await click('[aria-label="Apply chart gap"]');
   await click('[aria-label="Close settings"]');
   await flush();
   const barGapSaved=await api.LoadWorkspace();
   check(barGapSaved.workspace.chartRightGap?.unit==='bars'&&barGapSaved.workspace.chartRightGap.value===29&&barGapSaved.workspace.tabs.every(tab=>tab.charts.every(chart=>chart.view.mode==='live')),'bar gap applies to every chart and persists through Wails to SQLite',barGapSaved.workspace.chartRightGap);
   await click('[aria-label="Open settings"]');
   await select('[aria-label="Chart right gap unit"]','pixels');
   await input('[aria-label="Chart right gap"]',79);
   await click('[aria-label="Apply chart gap"]');
   await click('[aria-label="Close settings"]');
   await flush();
   const pixelGapSaved=await api.LoadWorkspace();
   check(pixelGapSaved.workspace.chartRightGap?.unit==='pixels'&&pixelGapSaved.workspace.chartRightGap.value===79&&qa('[data-testid="chart-cell"]').length===3,'pixel gap persists without replacing chart instances or layout',pixelGapSaved.workspace.chartRightGap);
   report.gapRevision=pixelGapSaved.revision;
   await click('[aria-label="Duplicate tab"]');await wait(()=>qa('[role="tab"][data-tab-id]').length===3&&qa('[data-testid="chart-cell"]').length===3,'temporary duplicate with all three charts');
   const chartCloseDuplicateID=q('[role="tab"][data-tab-id][aria-selected="true"]').dataset.tabId,chartCloseTarget=q('[data-testid="chart-cell"]'),chartCloseID=chartCloseTarget.dataset.chartId;
   await click('[data-testid="chart-cell"] [aria-label="Maximize chart"]');
   const chartCloseButton=chartCloseTarget.querySelector('[aria-label="Remove chart"]'),chartCloseRect=chartCloseButton.getBoundingClientRect();
   check(chartCloseRect.width>=30&&chartCloseRect.height>=30&&parseFloat(getComputedStyle(chartCloseButton).fontSize)>=22,'chart X uses a larger glyph and hit target',{width:chartCloseRect.width,height:chartCloseRect.height,fontSize:getComputedStyle(chartCloseButton).fontSize});
   chartCloseButton.click();await wait(()=>qa('[data-testid="chart-cell"]').length===2,'immediate native chart X removal');
   check(!q('[role="dialog"]')&&!q('.cell-wrapper.maximized')&&!q('.maximized-hidden')&&!qa('[data-testid="chart-cell"]').some(cell=>cell.dataset.chartId===chartCloseID),'chart X closes a maximized chart immediately and restores its siblings');
   await middleTab(q('[role="tab"][data-tab-id][aria-selected="true"]'));await wait(()=>qa('[role="tab"][data-tab-id]').length===2&&qa('[data-testid="chart-cell"]').length===1,'middle-close temporary active tab');
   check(!q('[role="dialog"]')&&!q('[role="menu"]'),'active tab middle-click closes its remaining charts without confirmation');
   await wait(()=>window.__cryptodeskDiagnostics?.().consumers===1,'active middle-close releases chart consumers');
   await flush();const activeClosed=await api.LoadWorkspace();
   check(!activeClosed.workspace.tabs.some(tab=>tab.id===chartCloseDuplicateID)&&activeClosed.workspace.tabs.map(tab=>tab.charts.length).join(',')==='3,1','chart X and active middle-close persist while preserving the original 3+1 workspace',activeClosed.workspace.tabs.map(tab=>({id:tab.id,charts:tab.charts.length})));
   qa('[role="tab"][data-tab-id]')[0].click();await wait(()=>qa('[data-testid="chart-cell"]').length===3,'original charts after active closure');
   await click('[aria-label="Duplicate tab"]');await wait(()=>qa('[role="tab"][data-tab-id]').length===3&&qa('[data-testid="chart-cell"]').length===3,'temporary inactive-close duplicate');
   const duplicateID=q('[role="tab"][data-tab-id][aria-selected="true"]').dataset.tabId;
   qa('[role="tab"][data-tab-id]')[0].click();await wait(()=>q('[role="tab"][data-tab-id][aria-selected="true"]').dataset.tabId===pixelGapSaved.workspace.tabs[0].id,'original active tab before inactive closure');
   const mountedBeforeClose=qa('[data-testid="chart-cell"]').map(cell=>cell.dataset.chartId);
   await middleTab(qa('[role="tab"][data-tab-id]')[2]);
   await wait(()=>qa('[role="tab"][data-tab-id]').length===2,'remove inactive middle-click target');await flush();
   const closedMiddle=await api.LoadWorkspace();
   check(!q('[role="menu"]')&&!q('[role="dialog"]')&&closedMiddle.workspace.tabs.length===2&&!closedMiddle.workspace.tabs.some(tab=>tab.id===duplicateID)&&closedMiddle.workspace.activeTabId===pixelGapSaved.workspace.activeTabId&&closedMiddle.workspace.tabs.map(tab=>tab.charts.length).join(',')==='3,1'&&JSON.stringify(qa('[data-testid="chart-cell"]').map(cell=>cell.dataset.chartId))===JSON.stringify(mountedBeforeClose),'inactive middle-click removes its whole tab without confirmation, preserves active charts and persists to SQLite',closedMiddle.workspace.tabs.map(tab=>({id:tab.id,charts:tab.charts.length})));
   const drawingCell=q('[data-testid="chart-cell"]');
   await wait(()=>drawingCell.querySelector('.ohlcv b'),'price data available for native drawing tools');
   const drawingPlot=drawingCell.querySelector('.chart-canvas tr td:nth-child(2) canvas');
   const drawPoint=(x,y)=>{const target=drawingCell.querySelector('.chart-drawing-overlay'),rect=drawingPlot.getBoundingClientRect(),point={bubbles:true,cancelable:true,pointerId:1,pointerType:'mouse',isPrimary:true,button:0,clientX:rect.left+rect.width*x,clientY:rect.top+rect.height*y};target.dispatchEvent(new PointerEvent('pointerdown',{...point,buttons:1}));target.dispatchEvent(new PointerEvent('pointerup',{...point,buttons:0}));};
   for(const [label,x,y] of [['Arrow up',.65,.55],['Arrow down',.8,.3],['Horizontal line',.75,.45],['Trendline',.65,.55]]){drawingCell.querySelector('[aria-label="'+label+'"]').click();await sleep(100);if(label==='Arrow up'){const overlay=bounds(drawingCell.querySelector('.chart-drawing-overlay')),plot=bounds(drawingPlot);report.drawingPointerOrigin={overlay,plot};check(Math.abs(overlay.left-plot.left)<1&&Math.abs(overlay.top-plot.top)<1,'native drawing input and rendered price pane share the exact pointer origin',report.drawingPointerOrigin);}drawPoint(x,y);await sleep(100);if(label==='Trendline'){drawPoint(.85,.3);await sleep(100);}}
   await flush();const drawingSaved=(await api.LoadWorkspace()).workspace.tabs[0].charts[0].drawings;
   check(drawingSaved?.map(d=>d.type).join(',')==='arrowUp,arrowDown,horizontal,trendline'&&drawingSaved.every(d=>d.points.length===(d.type==='trendline'?2:1)&&d.points.every(p=>Number.isInteger(p.time)&&p.time>0&&Number.isFinite(p.price)&&p.price>0)),'native drawing toolbar places all four tools and commits UTC/price anchors to SQLite',drawingSaved);
   check(drawingCell.querySelector('[aria-label="Cursor"]').getAttribute('aria-pressed')==='true','completed native drawings return to cursor mode');
   const moveRect=drawingPlot.getBoundingClientRect();
   drawingPlot.dispatchEvent(new MouseEvent('dblclick',{bubbles:true,cancelable:true,button:0,clientX:moveRect.left+moveRect.width*.2,clientY:moveRect.top+moveRect.height*.45}));
   await wait(()=>drawingCell.querySelector('.drawing-hint').textContent.includes('Move drawing'),'native double-click selects the horizontal drawing');
   drawingCell.querySelector('.chart-drawing-overlay').dispatchEvent(new PointerEvent('pointermove',{bubbles:true,cancelable:true,pointerId:1,pointerType:'mouse',isPrimary:true,clientX:moveRect.left+moveRect.width*.25,clientY:moveRect.top+moveRect.height*.6}));
   await flush();const moveDraft=(await api.LoadWorkspace()).workspace.tabs[0].charts[0].drawings;
   check(JSON.stringify(moveDraft)===JSON.stringify(drawingSaved),'native moving preview is not persisted before placement',moveDraft);
   drawPoint(.25,.6);await flush();const movedDrawings=(await api.LoadWorkspace()).workspace.tabs[0].charts[0].drawings;
   check(movedDrawings.length===4&&movedDrawings.every((drawing,index)=>drawing.id===drawingSaved[index].id&&drawing.type===drawingSaved[index].type&&drawing.exchange===drawingSaved[index].exchange&&drawing.market===drawingSaved[index].market&&drawing.instrument===drawingSaved[index].instrument)&&movedDrawings[2].points[0].price!==drawingSaved[2].points[0].price&&movedDrawings.every((drawing,index)=>index===2||JSON.stringify(drawing)===JSON.stringify(drawingSaved[index])),'native double-click move commits new anchors on the same drawing without changing its siblings',movedDrawings);
   check(!drawingCell.querySelector('.drawing-hint').textContent.includes('Move drawing')&&!drawingCell.querySelector('[aria-label="Delete selected drawing"]').disabled,'native drawing placement exits move mode and keeps the item selected');
   report.drawingMove={before:drawingSaved[2],after:movedDrawings[2]};
   const resizePoint=(target,type,x,y,buttons)=>{const rect=drawingPlot.getBoundingClientRect();target.dispatchEvent(new PointerEvent(type,{bubbles:true,cancelable:true,pointerId:7,pointerType:'mouse',isPrimary:true,button:0,buttons,clientX:rect.left+x,clientY:rect.top+rect.height*y}));};
   resizePoint(drawingPlot,'pointerdown',8,.6,1);
   await wait(()=>drawingCell.querySelector('.drawing-hint').textContent.includes('Adjust endpoint'),'native selected horizontal endpoint starts resizing');
   resizePoint(drawingCell.querySelector('.chart-drawing-overlay'),'pointermove',80,.72,1);
   await flush();const resizeDraft=(await api.LoadWorkspace()).workspace.tabs[0].charts[0].drawings;
   check(JSON.stringify(resizeDraft)===JSON.stringify(movedDrawings),'native endpoint preview leaves the committed SQLite anchors intact',resizeDraft);
   resizePoint(drawingCell.querySelector('.chart-drawing-overlay'),'pointerup',80,.72,0);
   await flush();const resizedDrawings=(await api.LoadWorkspace()).workspace.tabs[0].charts[0].drawings;
   check(resizedDrawings.length===4&&resizedDrawings[2].id===movedDrawings[2].id&&resizedDrawings[2].points.length===2&&resizedDrawings[2].points[0].time!==resizedDrawings[2].points[1].time&&resizedDrawings[2].points[0].price===resizedDrawings[2].points[1].price&&resizedDrawings[2].points[0].price!==movedDrawings[2].points[0].price&&resizedDrawings.every((drawing,index)=>index===2||JSON.stringify(drawing)===JSON.stringify(movedDrawings[index])),'native endpoint release persists a finite horizontal segment on the same ID and preserves its siblings',resizedDrawings);
   report.drawingResize={before:movedDrawings[2],after:resizedDrawings[2]};
   for(const method of ['button','keyboard']){
    drawingCell.querySelector('[aria-label="Arrow up"]').click();await sleep(100);drawPoint(.12,.18);await flush();
    const temporary=(await api.LoadWorkspace()).workspace.tabs[0].charts[0].drawings.at(-1),selectRect=drawingPlot.getBoundingClientRect(),selectPoint={bubbles:true,cancelable:true,pointerId:9,pointerType:'mouse',isPrimary:true,button:0,clientX:selectRect.left+selectRect.width*.12,clientY:selectRect.top+selectRect.height*.18};
    drawingPlot.dispatchEvent(new PointerEvent('pointerdown',{...selectPoint,buttons:1}));drawingPlot.dispatchEvent(new PointerEvent('pointerup',{...selectPoint,buttons:0}));
    await wait(()=>!drawingCell.querySelector('[aria-label="Delete selected drawing"]').disabled,'native single click selects the temporary arrow for '+method+' deletion');
    if(method==='button')drawingCell.querySelector('[aria-label="Delete selected drawing"]').click();
    else{document.activeElement?.blur();document.body.dispatchEvent(new KeyboardEvent('keydown',{key:'Delete',bubbles:true,cancelable:true}));}
    await flush();const deleted=(await api.LoadWorkspace()).workspace.tabs[0].charts[0].drawings;
    check(!deleted.some(drawing=>drawing.id===temporary.id)&&JSON.stringify(deleted)===JSON.stringify(resizedDrawings),'native '+method+' deletion removes only the single-clicked item and persists its removal',deleted);
   }
   report.savedDrawings=resizedDrawings;
  } else if(phase==='restore') {
   const screenerRestored=await api.GetScreener();
   check(screenerRestored.directory.endsWith('/screener-selected')&&screenerRestored.jobs.length===0,'native restart restores Screener folder independently of chart workspace',screenerRestored);
   const storageRestored=await api.GetCandleStorage(),cacheKey={exchange:initial.workspace.tabs[0].charts[0].exchange,market:'spot',instrument:initial.workspace.tabs[0].charts[0].instrument,timeframe:'1h'},restoredBars=await api.DesktopSmokeCandleRows(cacheKey,0,0);
   check(storageRestored.enabled&&storageRestored.directory.endsWith('/candles-selected')&&storageRestored.sizeBytes>0&&restoredBars.length>=4,'native restart restores machine-local settings and the actual downloaded candle data',{storage:storageRestored,bars:restoredBars.length});
   check(initial.workspace.tabs.length===2&&initial.workspace.tabs[0].charts.length===3&&initial.workspace.tabs[1].charts.length===1,'restart restores actual 3+1 chart workspace',report.initialCounts);
   check(initial.workspace.activeTabId===initial.workspace.tabs[0].id,'active tab restored',initial.workspace.activeTabId);
   const restoredDrawings=initial.workspace.tabs[0].charts[0].drawings;
   check(restoredDrawings?.map(d=>d.type).join(',')==='arrowUp,arrowDown,horizontal,trendline'&&restoredDrawings.every(d=>d.points.length===(d.type==='trendline'||d.type==='horizontal'?2:1)&&d.points.every(p=>Number.isInteger(p.time)&&p.time>0&&Number.isFinite(p.price)&&p.price>0)),'native restart restores all drawing types, resized horizontal segment and saved price/time anchors',restoredDrawings);
   check(qa('[role="group"][aria-label="Drawing tools"]').length===3&&q('[aria-label="Drawing count"]').textContent==='4','restored WebKit chart exposes its saved drawings and each chart retains its toolbar');
   report.restoredDrawings=restoredDrawings;
   check(qa('[data-testid="chart-cell"]').length===3&&qa('[data-testid="chart-placeholder"]').length===6,'only selected tab is mounted after restart',{charts:qa('[data-testid="chart-cell"]').length,placeholders:qa('[data-testid="chart-placeholder"]').length});
   check(initial.workspace.tabs[0].charts[0].timeframe==='4h'&&!initial.workspace.tabs[0].charts[0].showVolume,'native close flushed the final immediate edit',initial.workspace.tabs[0].charts[0]);
   verifySharedIndicators(initial.workspace,'restart restores shared indicators and custom parameters from SQLite');
   check(initial.workspace.tabs[0].charts.slice(1).every(chart=>chart.timeframe==='1w'),'restart restores shared weekly timeframes on the other active charts',initial.workspace.tabs[0].charts.map(chart=>chart.timeframe));
   check(q('[data-testid="chart-cell"] [aria-label="Timeframe 4h"]').getAttribute('aria-pressed')==='true','restart restores the selected horizontal timeframe');
   check(qa('[data-testid="chart-cell"]').map(chart=>chart.querySelectorAll('.indicator-chip').length).join(',')==='5,1,1','restart renders every chart own indicator configuration',qa('[data-testid="chart-cell"]').map(chart=>chart.querySelectorAll('.indicator-chip').length));
   await wait(()=>{const cells=qa('[data-testid="chart-cell"]'),first=chartShape(cells[0]),second=chartShape(cells[1]);return second.canvases>0&&first.canvases>second.canvases&&first.canvasRows===second.canvasRows+1;},'restart renders the separate MACD pane');
   report.restoredIndicatorChart=chartShape(qa('[data-testid="chart-cell"]')[0]);
   const restoredMACD=initial.workspace.tabs[0].charts[0].indicators.find(indicator=>indicator.type==='macd');
   await wait(()=>{const heights=paneHeights(qa('[data-testid="chart-cell"]')[0]);return heights.length===2&&Math.abs(heights[1]/heights[0]-restoredMACD.paneStretchFactor)<.04;},'restored MACD pane height ratio');
   report.restoredMACDPane={heights:paneHeights(qa('[data-testid="chart-cell"]')[0]),paneStretchFactor:restoredMACD.paneStretchFactor};
   check(Number.isFinite(restoredMACD.paneStretchFactor)&&restoredMACD.paneStretchFactor>.2,'restart restores the resized MACD pane from SQLite',report.restoredMACDPane);
   check(initial.workspace.chartRightGap?.unit==='pixels'&&initial.workspace.chartRightGap.value===79,'restart restores the saved pixel gap from SQLite',initial.workspace.chartRightGap);
   await click('[aria-label="Open settings"]');
   check(q('[aria-label="Chart right gap"]').value==='79'&&q('[aria-label="Chart right gap unit"]').value==='pixels','native Settings restores the saved value and unit');
   await click('[aria-label="Close settings"]');
  }
  else if(phase==='capacity') {
   check(initial.workspace.tabs.length===20&&initial.workspace.tabs.every(tab=>tab.charts.length===9),'explicit imported maximum restores20tabs180charts',report.initialCounts);
   check(qa('[data-testid="chart-cell"]').length===9&&qa('[data-testid="chart-canvas"]').length===9,'maximum workspace mounts nine chart instances only',{cells:qa('[data-testid="chart-cell"]').length,canvases:qa('[data-testid="chart-canvas"]').length});
   check(q('[aria-label="Add tab"]').disabled&&q('[aria-label="Duplicate tab"]').disabled&&q('[aria-label="Add chart"]').disabled,'capacity controls disabled',{addTab:q('[aria-label="Add tab"]').disabled,duplicate:q('[aria-label="Duplicate tab"]').disabled,addChart:q('[aria-label="Add chart"]').disabled});
   report.capacityTabStrip=await verifyTabStrip('maximum capacity',true);
   report.switchMetrics=[];report.switchLatenciesMS=[];const switchStart=performance.now();
   for(let index=0;index<200;index++) {
    const target=(index+1)%20,started=performance.now();
    qa('[role="tab"][data-tab-id]')[target].scrollIntoView({block:'nearest',inline:'nearest'});
    qa('[role="tab"][data-tab-id]')[target].click();
    await wait(()=>q('[role="tab"][data-tab-id][aria-selected="true"]')===qa('[role="tab"][data-tab-id]')[target]&&q('[data-testid="chart-cell"]')?.dataset.chartId===initial.workspace.tabs[target].charts[0].id,'native tab switch '+index);
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
  report.passed=report.checks.every(check=>check.passed);
 } catch(error) {report.error=String(error)+'\n'+String(error?.stack||'');}
 report.finishedAt=new Date().toISOString();
 if(!api)api=window.go?.main?.App;
 if(api){
  await api.DesktopSmokeReport(JSON.stringify(report,null,2));
  if(phase==='fresh'&&report.passed){
   // Deliberately omit an explicit save: native prepare-close must capture and
   // acknowledge this last edit, which the restart phase verifies.
   await click('[data-testid="chart-cell"] [aria-label="Timeframe 4h"]');
  }
  await api.DesktopSmokeClose();
 }
}`

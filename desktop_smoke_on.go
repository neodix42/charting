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
 const input=async(selector,value)=>{const element=await wait(()=>q(selector),selector);Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(element,String(value));element.dispatchEvent(new Event('input',{bubbles:true}));element.dispatchEvent(new Event('change',{bubbles:true}));await sleep(100);};
 const flush=async()=>{await click('.save-indicator');await wait(()=>q('.save-indicator')?.textContent.includes('All changes saved'),'acknowledged save');};
 const middleTab=async(element)=>{const rect=element.getBoundingClientRect();element.dispatchEvent(new MouseEvent('auxclick',{bubbles:true,cancelable:true,button:1,clientX:rect.left+rect.width/2,clientY:rect.top+rect.height/2}));await sleep(100);};
 const indicatorDefaults={sma:{period:20},ema:{period:20},ichimoku:{conversion:9,base:26,spanB:52,displacement:26},macd:{fast:12,slow:26,signal:9}};
 const indicatorNames={sma:'SMA',ema:'EMA',ichimoku:'Ichimoku Cloud',macd:'MACD'};
 const verifyIndicators=(chart,label)=>{const indicators=chart.indicators||[],types=Object.keys(indicatorDefaults);check(indicators.length===4&&indicators.every((indicator,index)=>indicator.type===types[index]&&Object.keys(indicator.parameters).length===Object.keys(indicatorDefaults[indicator.type]).length&&Object.entries(indicatorDefaults[indicator.type]).every(([key,value])=>indicator.parameters[key]===value)&&/^#[0-9a-f]{6}$/i.test(indicator.color))&&new Set(indicators.map(indicator=>indicator.id)).size===4,label,indicators);};
 const verifySharedIndicators=(workspace,label)=>{const charts=workspace.tabs[0].charts,ids=charts.flatMap(chart=>(chart.indicators||[]).map(indicator=>indicator.id));check(charts.map(chart=>chart.indicators?.length||0).join(',')==='5,1,1'&&charts.every(chart=>{const indicator=chart.indicators.at(-1);return indicator.type==='ema'&&indicator.parameters.period===34&&Object.keys(indicator.parameters).length===1&&/^#[0-9a-f]{6}$/i.test(indicator.color);})&&new Set(ids).size===ids.length,label,charts.map(chart=>chart.indicators));verifyIndicators({...charts[0],indicators:charts[0].indicators.slice(0,4)},'shared action preserves the first chart original four indicators');check(workspace.tabs[1].charts[0].timeframe==='1h'&&!workspace.tabs[1].charts[0].indicators?.length,'shared action leaves the hidden tab unchanged',workspace.tabs[1].charts[0]);};
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
  if(phase==='fresh'){
   check(qa('[role="tab"]').length===1&&qa('[data-testid="chart-cell"]').length===1,'first launch exactly one tab and chart',report.initialCounts);
   check(initial.workspace.tabs[0].grid.rows===1&&initial.workspace.tabs[0].grid.columns===1,'initial 1×1 grid',initial.workspace.tabs[0].grid);
   check(initial.workspace.chartRightGap?.unit==='bars'&&initial.workspace.chartRightGap.value===10,'first launch defaults to a ten-bar right gap',initial.workspace.chartRightGap);
   await middleTab(q('[role="tab"]'));
   check(qa('[role="tab"]').length===1&&!q('[role="menu"]')&&!q('[role="dialog"]'),'middle-click protects the final tab without opening a menu or dialog');
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
   verifyIndicators(weeklySaved.workspace.tabs[0].charts[0],'all four indicator defaults persisted through Wails to SQLite');
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
   await wait(()=>qa('[role="tab"]').length===2,'second tab');
   check(qa('[data-testid="chart-cell"]').length===1&&q('[aria-label="Grid layout"]').value==='1x1','new tab has exactly one chart and 1×1 grid',{charts:qa('[data-testid="chart-cell"]').length,grid:q('[aria-label="Grid layout"]').value});
   qa('[role="tab"]')[0].click();await wait(()=>qa('[data-testid="chart-cell"]').length===3,'restore first tab');
   await flush();
   const saved=await api.LoadWorkspace();
   check(saved.workspace.tabs.length===2&&saved.workspace.tabs[0].charts.length===3&&saved.workspace.tabs[1].charts.length===1,'small workspace saved with exact counts',saved.workspace.tabs.map(tab=>tab.charts.length));
   check(saved.workspace.tabs[0].charts[0].timeframe==='15m'&&!saved.workspace.tabs[0].charts[0].showVolume,'independent chart settings saved',saved.workspace.tabs[0].charts[0]);
   verifyIndicators(saved.workspace.tabs[0].charts[0],'indicator settings survive chart and tab additions');
   check(saved.workspace.tabs[0].charts.slice(1).every(chart=>!chart.indicators?.length)&&!saved.workspace.tabs[1].charts[0].indicators?.length,'new chart indicators remain independent',saved.workspace.tabs.map(tab=>tab.charts.map(chart=>chart.indicators?.length||0)));
   report.savedRevision=saved.revision;
   const shared=await wait(()=>q('[data-testid="tab-chart-controls"]'),'shared current tab controls'),header=q('.app-header');
   const bounds=element=>{const rect=element.getBoundingClientRect();return {left:rect.left,right:rect.right,top:rect.top,bottom:rect.bottom,centerY:(rect.top+rect.bottom)/2};};
   const headerControls=[q('.brand'),shared,q('[aria-label="Grid layout"]'),q('[aria-label="Add chart"]'),q('[aria-label="Rename tab"]'),q('[aria-label="Duplicate tab"]'),q('[aria-label="Close tab"]'),q('.save-indicator'),q('[aria-label="Open settings"]')],headerRect=bounds(header),headerBounds=headerControls.map(bounds),tabsRect=bounds(q('.tab-row')),chartsRect=bounds(q('.workspace-content'));
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
   await click('[aria-label="Duplicate tab"]');await wait(()=>qa('[role="tab"]').length===3&&qa('[data-testid="chart-cell"]').length===3,'temporary duplicate with all three charts');
   const chartCloseDuplicateID=q('[role="tab"][aria-selected="true"]').dataset.tabId,chartCloseTarget=q('[data-testid="chart-cell"]'),chartCloseID=chartCloseTarget.dataset.chartId;
   await click('[data-testid="chart-cell"] [aria-label="Maximize chart"]');
   const chartCloseButton=chartCloseTarget.querySelector('[aria-label="Remove chart"]'),chartCloseRect=chartCloseButton.getBoundingClientRect();
   check(chartCloseRect.width>=30&&chartCloseRect.height>=30&&parseFloat(getComputedStyle(chartCloseButton).fontSize)>=22,'chart X uses a larger glyph and hit target',{width:chartCloseRect.width,height:chartCloseRect.height,fontSize:getComputedStyle(chartCloseButton).fontSize});
   chartCloseButton.click();await wait(()=>qa('[data-testid="chart-cell"]').length===2,'immediate native chart X removal');
   check(!q('[role="dialog"]')&&!q('.cell-wrapper.maximized')&&!q('.maximized-hidden')&&!qa('[data-testid="chart-cell"]').some(cell=>cell.dataset.chartId===chartCloseID),'chart X closes a maximized chart immediately and restores its siblings');
   await middleTab(q('[role="tab"][aria-selected="true"]'));await wait(()=>qa('[role="tab"]').length===2&&qa('[data-testid="chart-cell"]').length===1,'middle-close temporary active tab');
   check(!q('[role="dialog"]')&&!q('[role="menu"]'),'active tab middle-click closes its remaining charts without confirmation');
   await wait(()=>window.__cryptodeskDiagnostics?.().consumers===1,'active middle-close releases chart consumers');
   await flush();const activeClosed=await api.LoadWorkspace();
   check(!activeClosed.workspace.tabs.some(tab=>tab.id===chartCloseDuplicateID)&&activeClosed.workspace.tabs.map(tab=>tab.charts.length).join(',')==='3,1','chart X and active middle-close persist while preserving the original 3+1 workspace',activeClosed.workspace.tabs.map(tab=>({id:tab.id,charts:tab.charts.length})));
   qa('[role="tab"]')[0].click();await wait(()=>qa('[data-testid="chart-cell"]').length===3,'original charts after active closure');
   await click('[aria-label="Duplicate tab"]');await wait(()=>qa('[role="tab"]').length===3&&qa('[data-testid="chart-cell"]').length===3,'temporary inactive-close duplicate');
   const duplicateID=q('[role="tab"][aria-selected="true"]').dataset.tabId;
   qa('[role="tab"]')[0].click();await wait(()=>q('[role="tab"][aria-selected="true"]').dataset.tabId===pixelGapSaved.workspace.tabs[0].id,'original active tab before inactive closure');
   const mountedBeforeClose=qa('[data-testid="chart-cell"]').map(cell=>cell.dataset.chartId);
   await middleTab(qa('[role="tab"]')[2]);
   await wait(()=>qa('[role="tab"]').length===2,'remove inactive middle-click target');await flush();
   const closedMiddle=await api.LoadWorkspace();
   check(!q('[role="menu"]')&&!q('[role="dialog"]')&&closedMiddle.workspace.tabs.length===2&&!closedMiddle.workspace.tabs.some(tab=>tab.id===duplicateID)&&closedMiddle.workspace.activeTabId===pixelGapSaved.workspace.activeTabId&&closedMiddle.workspace.tabs.map(tab=>tab.charts.length).join(',')==='3,1'&&JSON.stringify(qa('[data-testid="chart-cell"]').map(cell=>cell.dataset.chartId))===JSON.stringify(mountedBeforeClose),'inactive middle-click removes its whole tab without confirmation, preserves active charts and persists to SQLite',closedMiddle.workspace.tabs.map(tab=>({id:tab.id,charts:tab.charts.length})));
  } else if(phase==='restore') {
   check(initial.workspace.tabs.length===2&&initial.workspace.tabs[0].charts.length===3&&initial.workspace.tabs[1].charts.length===1,'restart restores actual 3+1 chart workspace',report.initialCounts);
   check(initial.workspace.activeTabId===initial.workspace.tabs[0].id,'active tab restored',initial.workspace.activeTabId);
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
   await click('[data-testid="chart-cell"] [aria-label="Timeframe 4h"]');
  }
  await api.DesktopSmokeClose();
 }
}`

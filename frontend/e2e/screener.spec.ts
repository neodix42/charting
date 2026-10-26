import {test,expect,type Page} from '@playwright/test';
import capacity from '../../examples/capacity-20-tabs-180-charts.json' with {type:'json'};
import type {ScreenerDataFile,ScreenerInfo,Workspace} from '../src/types';

const chartTabs=(page:Page)=>page.locator('[role="tab"][data-tab-id]');
const mainTab=(page:Page)=>page.getByRole('tab',{name:'Screener',exact:true});
const saved=(page:Page):Promise<Workspace>=>page.evaluate(()=>(window as any).__testWorkspace());
async function downloader(page:Page){await mainTab(page).click();await page.getByRole('tab',{name:'Downloader',exact:true}).click();await expect(page.getByRole('checkbox',{name:'Select all symbols',exact:true})).toBeEnabled();}

test('downloaded asset selection replaces unrelated symbols, ignores search and persists across restart',async({page})=>{
 await page.goto('/');
 const files:ScreenerDataFile[]=[
  {id:'btc-daily',name:'btcusd-d1-2024-01-01-2024-02-01.csv',symbol:'btcusd',timeframe:'d1',fields:['timestamp','close'],bytes:200},
  {id:'btc-minute',name:'btcusd-m1-2024-01-01-2024-02-01.csv',symbol:'btcusd',timeframe:'m1',fields:['timestamp','close'],bytes:300},
  {id:'eth-daily',name:'ethusd-d1-2024-01-01-2024-02-01.csv',symbol:'ethusd',timeframe:'d1',fields:['timestamp','close'],bytes:200},
  {id:'unknown',name:'custom.csv',symbol:'unknown',fields:['timestamp','close'],bytes:100},
 ];
 await page.evaluate(files=>(window as any).__testSetEnrichmentFiles(files),files);
 await downloader(page);
 const downloaded=page.getByRole('checkbox',{name:'Select all downloaded assets',exact:true});
 await expect(downloaded).toBeEnabled();
 await page.getByRole('checkbox',{name:'Select all symbols',exact:true}).check();
 await page.getByRole('searchbox',{name:'Search Dukascopy instruments',exact:true}).fill('Gold');
 await downloaded.check();
 await expect(page.getByLabel('Selected symbol count',{exact:true})).toHaveText('2 / 4 selected');
 await expect(page.getByRole('checkbox',{name:'XAUUSD — Gold vs US Dollar',exact:true})).not.toBeChecked();
 await page.getByRole('combobox',{name:'Download timeframe',exact:true}).selectOption('m1');
 await page.reload();await downloader(page);
 await expect(downloaded).toBeChecked();
 await expect(page.getByRole('combobox',{name:'Download timeframe',exact:true})).toHaveValue('m1');
 await expect(page.getByRole('checkbox',{name:'BTCUSD — Bitcoin vs US Dollar',exact:true})).toBeChecked();
 await expect(page.getByRole('checkbox',{name:'ETHUSD — Ethereum vs US Dollar',exact:true})).toBeChecked();
 await downloaded.uncheck();await expect(page.getByLabel('Selected symbol count',{exact:true})).toHaveText('0 / 4 selected');
 await downloaded.check();
 await page.getByRole('button',{name:'Download 2 symbols',exact:true}).click();
 expect((await page.evaluate(()=>(window as any).__testScreenerRequest())).symbols).toEqual(['btcusd','ethusd']);
 await expect(downloaded).toBeDisabled();
 await page.getByRole('button',{name:'Cancel downloads',exact:true}).click();
 await page.evaluate(files=>(window as any).__testSetEnrichmentFiles(files.filter(file=>file.symbol==='btcusd')),files);
 await expect.poll(()=>downloaded.evaluate(input=>(input as HTMLInputElement).indeterminate)).toBe(true);
 await downloaded.check();await expect(page.getByLabel('Selected symbol count',{exact:true})).toHaveText('1 / 4 selected');
 await page.evaluate(()=>(window as any).__testSetScreener({directory:'/new/empty/folder',downloadedSymbols:[]}));
 await expect(downloaded).toBeDisabled();
});

test('completed queue history alone does not count as an existing downloaded asset',async({page})=>{
 await page.goto('/');await downloader(page);
 const downloaded=page.getByRole('checkbox',{name:'Select all downloaded assets',exact:true});
 await expect(downloaded).toBeDisabled();
 await page.evaluate(()=>(window as any).__testSetScreener({jobs:[{id:'old',symbol:'btcusd',timeframe:'d1',from:'2024-01-01',to:'2024-02-01',status:'completed',progress:100,message:'Old queue; CSV was removed',outputPath:'/removed.csv'}]}));
 await expect(page.getByLabel('Download summary',{exact:true})).toContainText('1 completed');
 await expect(downloaded).toBeDisabled();
 await expect(page.getByRole('form',{name:'Download historical data',exact:true})).toContainText('Download creates a new CSV for the full selected date range.');
});

test('reopening restores selected symbols, timeframe, dates, parallelism and every category expansion',async({page})=>{
 await page.goto('/');await downloader(page);
 await page.getByRole('checkbox',{name:'Select all Cryptocurrencies',exact:true}).check();
 await page.getByRole('combobox',{name:'Download timeframe',exact:true}).selectOption('m15');
 await page.getByRole('combobox',{name:'Parallel downloads',exact:true}).selectOption('7');
 await page.getByLabel('Download from date',{exact:true}).fill('2021-01-01');await page.getByLabel('Download to date',{exact:true}).fill('2024-01-01');
 const forex=page.locator('details').filter({has:page.locator('summary',{hasText:'Forex'})}),crypto=page.locator('details').filter({has:page.locator('summary',{hasText:'Cryptocurrencies'})});
 await forex.locator('summary').click();await expect(forex).not.toHaveAttribute('open','');
 await expect.poll(()=>page.evaluate(()=>JSON.parse(sessionStorage.getItem('cryptodesk-explicit-test-screener-view')||'null'))).toMatchObject({symbols:['btcusd','ethusd'],timeframe:'m15',concurrency:7,collapsedCategories:['Forex']});
 await page.reload();await downloader(page);
 await expect(page.getByRole('combobox',{name:'Download timeframe',exact:true})).toHaveValue('m15');await expect(page.getByRole('combobox',{name:'Parallel downloads',exact:true})).toHaveValue('7');
 await expect(page.getByLabel('Download from date',{exact:true})).toHaveValue('2021-01-01');await expect(page.getByLabel('Download to date',{exact:true})).toHaveValue('2024-01-01');
 await expect(forex).not.toHaveAttribute('open','');await expect(crypto).toHaveAttribute('open','');
 await expect(page.getByRole('checkbox',{name:'BTCUSD — Bitcoin vs US Dollar',exact:true})).toBeChecked();await expect(page.getByRole('checkbox',{name:'ETHUSD — Ethereum vs US Dollar',exact:true})).toBeChecked();
 await forex.locator('summary').click();await expect.poll(()=>page.evaluate(()=>JSON.parse(sessionStorage.getItem('cryptodesk-explicit-test-screener-view')||'null').collapsedCategories)).toEqual([]);
 await page.reload();await downloader(page);await expect(forex).toHaveAttribute('open','');
});

test('normal close waits for the latest Screener choices and reports a failed preference write',async({page})=>{
 await page.goto('/');await downloader(page);
 await page.evaluate(()=>{window.go!.main!.App!.SaveScreenerView=async()=>{throw Error('Explicit Screener preference write failure');};});
 await page.getByRole('combobox',{name:'Download timeframe',exact:true}).selectOption('h4');
 await expect(page.getByRole('alert')).toContainText('Explicit Screener preference write failure');
 await page.evaluate(()=>(window as any).__testEmit('prepare-close',{token:'screener-save-test'}));
 await expect(page.getByText('Save failed during shutdown. Your changes remain open.')).toBeVisible();
 expect(await page.evaluate(()=>(window as any).__testCloseConfirmation())).toBeNull();
});

test('Screener remains fixed beside twenty chart tabs, releases all charts and preserves the workspace',async({page})=>{
 await page.addInitScript(workspace=>sessionStorage.setItem('cryptodesk-explicit-test-workspace',JSON.stringify(workspace)),capacity);
 await page.goto('/');await expect(chartTabs(page)).toHaveCount(20);await expect(page.getByTestId('chart-cell')).toHaveCount(9);
 const before=await saved(page),active=before.activeTabId;
 await expect(mainTab(page)).toBeVisible();expect(await mainTab(page).evaluate(element=>!element.closest('.tabs')&&element.parentElement?.classList.contains('tab-row'))).toBe(true);
 await mainTab(page).click();await expect(mainTab(page)).toHaveAttribute('aria-selected','true');await expect(page.getByRole('tab',{name:'Analysis',exact:true})).toHaveAttribute('aria-selected','true');await expect(page.locator('#screener-analysis').getByRole('heading',{name:'Visual condition rows',exact:true})).toBeVisible();
 await expect(page.getByTestId('chart-cell')).toHaveCount(0);await expect.poll(()=>page.evaluate(()=>(window as any).__testLeases())).toBe(0);await expect.poll(()=>page.evaluate(()=>window.__cryptodeskDiagnostics?.())).toMatchObject({chartInstances:0,consumers:0,savedTabs:20,savedCharts:180});
 await expect(page.getByRole('button',{name:'Add chart',exact:true})).toHaveCount(0);await expect(page.getByRole('button',{name:'Duplicate tab',exact:true})).toHaveCount(0);await expect(page.getByRole('combobox',{name:'Grid layout',exact:true})).toHaveCount(0);await expect(page.getByRole('button',{name:'Add tab',exact:true})).toBeDisabled();
 await mainTab(page).click({button:'middle'});await mainTab(page).dblclick();await mainTab(page).focus();await page.keyboard.press('Control+Shift+n');await page.keyboard.press('Control+w');await expect(page.getByRole('dialog')).toHaveCount(0);expect((await saved(page)).activeTabId).toBe(active);await expect(chartTabs(page)).toHaveCount(20);
 await page.locator(`[data-tab-id="${active}"]`).click();await expect(page.getByTestId('chart-cell')).toHaveCount(9);await expect(mainTab(page)).toHaveAttribute('aria-selected','false');await expect.poll(()=>page.evaluate(()=>(window as any).__testLeases())).toBe(9);
 const after=await saved(page);expect(after.activeTabId).toBe(active);expect(after.tabs.map(tab=>({id:tab.id,charts:tab.charts.map(chart=>({id:chart.id,instrument:chart.instrument,timeframe:chart.timeframe}))}))).toEqual(before.tabs.map(tab=>({id:tab.id,charts:tab.charts.map(chart=>({id:chart.id,instrument:chart.instrument,timeframe:chart.timeframe}))})));
});

test('grouped symbols, global selection, shared timeframe and UTC dates produce per-symbol progress and errors',async({page})=>{
 await page.goto('/');await downloader(page);
 await expect(page.locator('.screener-instrument-group')).toHaveCount(3);await page.getByRole('checkbox',{name:'Select all Cryptocurrencies',exact:true}).check();await expect(page.getByLabel('Selected symbol count',{exact:true})).toHaveText('2 / 4 selected');
 await page.getByRole('checkbox',{name:'Select all symbols',exact:true}).check();await expect(page.getByLabel('Selected symbol count',{exact:true})).toHaveText('4 / 4 selected');await page.getByRole('checkbox',{name:'Select all symbols',exact:true}).uncheck();await page.getByRole('checkbox',{name:'Select all Cryptocurrencies',exact:true}).check();
 await page.getByRole('searchbox',{name:'Search Dukascopy instruments',exact:true}).fill('bitcoin');await expect(page.locator('.screener-instrument')).toHaveCount(1);await expect(page.getByRole('checkbox',{name:'BTCUSD — Bitcoin vs US Dollar',exact:true})).toBeChecked();await page.getByRole('searchbox',{name:'Search Dukascopy instruments',exact:true}).clear();
 await page.getByRole('combobox',{name:'Download timeframe',exact:true}).selectOption('m1');await page.getByLabel('Download from date',{exact:true}).fill('2024-01-01');await page.getByLabel('Download to date',{exact:true}).fill('2024-01-03');await page.getByRole('button',{name:'Download 2 symbols',exact:true}).click();
 expect(await page.evaluate(()=>(window as any).__testScreenerRequest())).toEqual({symbols:['btcusd','ethusd'],timeframe:'m1',from:'2024-01-01',to:'2024-01-03',concurrency:20});
 const btc=page.getByRole('article',{name:'BTCUSD download',exact:true}),eth=page.getByRole('article',{name:'ETHUSD download',exact:true});await expect(btc).toContainText('queued');await expect(page.getByRole('combobox',{name:'Download timeframe',exact:true})).toBeDisabled();
 await page.evaluate(()=>{const info=(window as any).__testScreener() as ScreenerInfo;(window as any).__testSetScreener({jobs:info.jobs.map(job=>({...job,status:job.symbol==='btcusd'?'running':'queued',progress:job.symbol==='btcusd'?43:0,message:'Explicit test download progress'}))});});
 await expect(btc.getByRole('progressbar')).toHaveAttribute('value','43');await page.getByRole('tab',{name:'Analysis',exact:true}).click();await expect(page.locator('#screener-analysis').getByRole('heading',{name:'Visual condition rows',exact:true})).toBeVisible();await page.getByRole('tab',{name:'Downloader',exact:true}).click();await expect(btc.getByRole('progressbar')).toHaveAttribute('value','43');
 await page.evaluate(()=>{const info=(window as any).__testScreener() as ScreenerInfo;(window as any).__testSetScreener({jobs:info.jobs.map(job=>({...job,status:job.symbol==='btcusd'?'completed':'failed',progress:job.symbol==='btcusd'?100:12,message:job.symbol==='btcusd'?'Saved CSV':'Explicit provider connection failure',outputPath:job.symbol==='btcusd'?'/explicit-test/screener/btcusd.csv':undefined}))});});
 await expect(btc).toContainText('/explicit-test/screener/btcusd.csv');await expect(eth).toContainText('Explicit provider connection failure');await expect(eth).toContainText('failed');await expect(page.getByRole('combobox',{name:'Download timeframe',exact:true})).toBeEnabled();
 await page.getByRole('button',{name:'Download 2 symbols',exact:true}).click();await page.getByRole('button',{name:'Cancel downloads',exact:true}).click();await expect(btc).toContainText('cancelled');await expect(eth).toContainText('cancelled');
});

test('download status filters precede ten-row pagination and pages clamp as live jobs change',async({page})=>{
 await page.goto('/');await downloader(page);
 await page.evaluate(()=>{
  (window as any).__testSetScreener({active:true,jobs:Array.from({length:27},(_,index)=>({id:`job-${index}`,symbol:`symbol${index}`,timeframe:'d1',from:'2024-01-01',to:'2024-02-01',status:index<12?'completed':index<24?'failed':index===24?'running':index===25?'queued':'cancelled',progress:index<12?100:index===24?35:0,message:index>=12&&index<24?'Explicit download failure':'Explicit download progress'}))});
 });
 const jobs=page.locator('.screener-job'),filter=page.getByRole('combobox',{name:'Download status filter',exact:true}),pages=page.getByRole('navigation',{name:'Download pages',exact:true});
 await expect(jobs).toHaveCount(10);await expect(filter).toHaveValue('all');
 await expect(page.getByLabel('Download summary',{exact:true})).toContainText('12 completed · 12 failed · 27 total');
 await expect(pages).toContainText('1–10 of 27 downloads');
 await page.getByRole('button',{name:'Next downloads',exact:true}).click();await expect(pages).toContainText('11–20 of 27 downloads');
 await page.getByRole('button',{name:'Next downloads',exact:true}).click();await expect(jobs).toHaveCount(7);await expect(pages).toContainText('21–27 of 27 downloads');
 await expect(page.getByRole('button',{name:'Next downloads',exact:true})).toBeDisabled();
 await filter.selectOption('failed');await expect(jobs).toHaveCount(10);await expect(pages).toContainText('1–10 of 12 downloads');await expect(jobs.first()).toHaveAttribute('data-symbol','symbol12');
 await page.getByRole('button',{name:'Next downloads',exact:true}).click();await expect(jobs).toHaveCount(2);await expect(pages).toContainText('11–12 of 12 downloads');
 await page.evaluate(()=>{const info=(window as any).__testScreener() as ScreenerInfo;(window as any).__testSetScreener({jobs:info.jobs.map((job,index)=>index===22?{...job,status:'completed',progress:100}:index===23?{...job,status:'running',progress:12}:job)});});
 await expect(jobs).toHaveCount(10);await expect(pages).toContainText('1–10 of 10 downloads');
 await expect(page.getByLabel('Download summary',{exact:true})).toContainText('13 completed · 10 failed · 27 total');
 await filter.selectOption('completed');await expect(jobs).toHaveCount(10);await expect(jobs.locator('.screener-job-status')).toHaveText(Array(10).fill('completed'));
 await page.getByRole('button',{name:'Next downloads',exact:true}).click();await expect(jobs).toHaveCount(3);await expect(pages).toContainText('11–13 of 13 downloads');await expect(jobs.last()).toHaveAttribute('data-symbol','symbol22');
 await filter.selectOption('running');await expect(jobs).toHaveCount(2);
 await page.evaluate(()=>{const info=(window as any).__testScreener() as ScreenerInfo;(window as any).__testSetScreener({jobs:info.jobs.map(job=>job.symbol==='symbol24'?{...job,progress:85}:job)});});
 await expect(page.getByRole('progressbar',{name:'SYMBOL24 download progress',exact:true})).toHaveAttribute('value','85');
 await page.getByRole('button',{name:'Cancel downloads',exact:true}).click();await expect(jobs).toHaveCount(0);await expect(page.getByText('No running downloads.',{exact:true})).toBeVisible();
 await filter.selectOption('cancelled');await expect(jobs).toHaveCount(4);
 await filter.selectOption('completed');await expect(jobs).toHaveCount(10);await expect(page.getByLabel('Download summary',{exact:true})).toContainText('13 completed · 10 failed · 27 total');
});

test('all Analysis operators describe closed-bar semantics on hover and keyboard focus',async({page})=>{
 await page.goto('/');await mainTab(page).click();
 const operator=page.getByRole('combobox',{name:'Condition 1 operator',exact:true}),tooltip=page.getByRole('tooltip');
 const descriptions:[string,string][]=[['above','Close is above SMA(200).'],['below','Kijun is below Tenkan.'],['crossesAbove','preceding and current closed bars'],['crossesBelow','preceding and current closed bars'],['between','including both boundaries'],['withinDistance','0.2 × ATR(14)'],['occurredWithin','last 3 closed bars'],['remainedFor','5 closed bars'],['highest','current bar is excluded'],['lowest','current bar is excluded']];
 await expect(operator.locator('option')).toHaveCount(10);
 for(const [value,example] of descriptions){
  await operator.selectOption(value);await page.mouse.move(0,0);await operator.hover();
  await expect(tooltip).toBeVisible();await expect(tooltip).toContainText(example);
  await expect(operator).not.toHaveAttribute('title');
  await expect(operator.locator('option[title]')).toHaveCount(0);
  await page.keyboard.press('Escape');await expect(tooltip).toBeHidden();
 }
 await page.mouse.move(0,0);await page.getByRole('button',{name:'Help for Condition 1 operator',exact:true}).focus();await expect(tooltip).toContainText('previous 20-bar Low');
 await page.keyboard.press('Escape');await expect(tooltip).toBeHidden();
 await operator.selectOption('occurredWithin');const comparison=page.getByRole('combobox',{name:'Condition 1 comparison',exact:true});
 await comparison.selectOption('crossesAbove');await page.mouse.move(0,0);await comparison.hover();await expect(tooltip).toContainText('preceding and current closed bars');await expect(comparison.locator('option')).toHaveCount(6);
 await expect(comparison).not.toHaveAttribute('title');await expect(comparison.locator('option[title]')).toHaveCount(0);
 await page.mouse.move(0,0);await page.getByRole('button',{name:'Help for Condition 1 comparison',exact:true}).focus();await expect(tooltip).toBeVisible();await expect(tooltip).toContainText('Tenkan crosses above Kijun');
 await page.keyboard.press('Escape');await expect(tooltip).toBeHidden();
});

test('parallel download limits default to twenty, stay within bounds and survive panel switches',async({page})=>{
 await page.goto('/');await downloader(page);const parallel=page.getByRole('combobox',{name:'Parallel downloads',exact:true});await expect(parallel).toHaveValue('20');await expect(parallel.locator('option')).toHaveCount(20);expect(await parallel.locator('option').evaluateAll(options=>options.map(option=>(option as HTMLOptionElement).value))).toEqual(Array.from({length:20},(_,index)=>String(index+1)));
 await page.getByRole('checkbox',{name:'Select all symbols',exact:true}).check();await parallel.selectOption('1');await page.getByRole('tab',{name:'Analysis',exact:true}).click();await page.getByRole('tab',{name:'Downloader',exact:true}).click();await expect(parallel).toHaveValue('1');await chartTabs(page).first().click();await mainTab(page).click();await expect(parallel).toHaveValue('1');
 await parallel.evaluate(element=>{const select=element as HTMLSelectElement;select.append(new Option('21','21'));select.value='21';select.dispatchEvent(new Event('change',{bubbles:true}));});await expect(page.getByRole('button',{name:'Download 4 symbols',exact:true})).toBeDisabled();expect(await page.evaluate(()=>(window as any).__testScreenerRequest())).toBeNull();
 await parallel.selectOption('1');await page.getByRole('button',{name:'Download 4 symbols',exact:true}).click();expect(await page.evaluate(()=>(window as any).__testScreenerRequest().concurrency)).toBe(1);await expect(parallel).toBeDisabled();await expect(page.getByLabel('Applied parallel download limit',{exact:true})).toHaveText('Up to 1 parallel');await page.getByRole('button',{name:'Cancel downloads',exact:true}).click();await expect(parallel).toBeEnabled();
 await parallel.selectOption('20');await page.getByRole('button',{name:'Download 4 symbols',exact:true}).click();expect(await page.evaluate(()=>(window as any).__testScreenerRequest().concurrency)).toBe(20);await expect(page.getByLabel('Applied parallel download limit',{exact:true})).toHaveText('Up to 20 parallel');await page.getByRole('button',{name:'Cancel downloads',exact:true}).click();
});

test('concurrent symbols keep independent progress, stable queue order and cancel every active job',async({page})=>{
 await page.goto('/');await downloader(page);await page.getByRole('checkbox',{name:'Select all symbols',exact:true}).check();await page.getByRole('combobox',{name:'Parallel downloads',exact:true}).selectOption('2');await page.getByRole('button',{name:'Download 4 symbols',exact:true}).click();expect(await page.evaluate(()=>(window as any).__testScreenerRequest().concurrency)).toBe(2);
 await page.evaluate(()=>{const info=(window as any).__testScreener() as ScreenerInfo;(window as any).__testSetScreener({jobs:info.jobs.map((job,index)=>({...job,status:index<2?'running':'queued',progress:index===0?68:index===1?24:0,message:index<2?'Explicit concurrent progress':'Queued behind running symbols'}))});});
 await expect(page.locator('.screener-job.running')).toHaveCount(2);await expect(page.locator('.screener-job.queued')).toHaveCount(2);expect(await page.locator('.screener-job').evaluateAll(jobs=>jobs.map(job=>job.getAttribute('data-symbol')))).toEqual(['eurusd','btcusd','ethusd','xauusd']);await expect(page.getByRole('progressbar',{name:'EURUSD download progress',exact:true})).toHaveAttribute('value','68');await expect(page.getByRole('progressbar',{name:'BTCUSD download progress',exact:true})).toHaveAttribute('value','24');
 await page.evaluate(()=>{const info=(window as any).__testScreener() as ScreenerInfo;(window as any).__testSetScreener({jobs:info.jobs.map(job=>job.symbol==='btcusd'?{...job,progress:47}:job)});});await expect(page.getByRole('progressbar',{name:'BTCUSD download progress',exact:true})).toHaveAttribute('value','47');await expect(page.getByRole('progressbar',{name:'EURUSD download progress',exact:true})).toHaveAttribute('value','68');
 await page.getByRole('button',{name:'Cancel downloads',exact:true}).click();await expect(page.locator('.screener-job.cancelled')).toHaveCount(4);await expect(page.locator('.screener-job.running,.screener-job.queued')).toHaveCount(0);await expect(page.getByRole('combobox',{name:'Parallel downloads',exact:true})).toBeEnabled();await expect(page.getByRole('combobox',{name:'Parallel downloads',exact:true})).toHaveValue('2');
});

test('Screener directory is applied separately from the workspace and restored after reload',async({page})=>{
 await page.goto('/');await page.getByRole('button',{name:'Open settings',exact:true}).click();await page.getByRole('dialog',{name:'Workspace settings',exact:true}).getByRole('tab',{name:'Screener',exact:true}).click();const form=page.getByRole('form',{name:'Screener data storage',exact:true});
 await expect(form.getByLabel('Screener download folder',{exact:true})).toHaveValue('/explicit-test/screener');
 await page.evaluate(()=>{window.go!.main!.App!.ChooseScreenerDirectory=async()=>'/chosen folder/historical data';});await form.getByRole('button',{name:'Choose Screener download folder',exact:true}).click();await expect(form.getByLabel('Screener download folder',{exact:true})).toHaveValue('/chosen folder/historical data');expect(await page.evaluate(()=>(window as any).__testScreener().directory)).toBe('/explicit-test/screener');
 await form.getByRole('button',{name:'Apply Screener download folder',exact:true}).click();await expect(form.getByRole('status',{name:'Applied Screener folder',exact:true})).toHaveText('Current folder: /chosen folder/historical data');expect(JSON.stringify(await saved(page))).not.toContain('/chosen folder/historical data');
 await page.getByRole('button',{name:'Close settings',exact:true}).click();await downloader(page);await expect(page.locator('.screener-download-directory')).toContainText('/chosen folder/historical data');
 await page.reload();await page.getByRole('button',{name:'Open settings',exact:true}).click();await page.getByRole('dialog',{name:'Workspace settings',exact:true}).getByRole('tab',{name:'Screener',exact:true}).click();await expect(form.getByLabel('Screener download folder',{exact:true})).toHaveValue('/chosen folder/historical data');
 await page.evaluate(()=>{window.go!.main!.App!.ConfigureScreenerDirectory=async()=>{throw Error('Explicit folder permission failure');};});await form.getByLabel('Screener download folder',{exact:true}).fill('/unwritable');await form.getByRole('button',{name:'Apply Screener download folder',exact:true}).click();await expect(form.getByRole('alert')).toContainText('Explicit folder permission failure');await expect(form.getByRole('status',{name:'Applied Screener folder',exact:true})).toContainText('/chosen folder/historical data');expect(await page.evaluate(()=>(window as any).__testScreener().directory)).toBe('/chosen folder/historical data');
});

test('runtime, catalog and start errors remain visible without creating fake downloads',async({page})=>{
 await page.goto('/');await page.evaluate(()=>{const api=window.go!.main!.App!;(window as any).__originalScreenerCatalog=api.GetScreenerCatalog;api.GetScreenerCatalog=async()=>{throw Error('Explicit catalog read failure');};(window as any).__testSetScreener({runtimeAvailable:false,runtimeError:'Explicit missing downloader runtime'});});await mainTab(page).click();await page.getByRole('tab',{name:'Downloader',exact:true}).click();await expect(page.getByRole('alert')).toContainText('Explicit catalog read failure');
 await page.evaluate(()=>{window.go!.main!.App!.GetScreenerCatalog=(window as any).__originalScreenerCatalog;});await page.getByRole('button',{name:'Retry downloader',exact:true}).click();await expect(page.getByRole('alert')).toContainText('Explicit missing downloader runtime');await page.getByRole('checkbox',{name:'Select all symbols',exact:true}).check();await expect(page.getByRole('button',{name:'Download 4 symbols',exact:true})).toBeDisabled();
 await page.evaluate(()=>{(window as any).__testSetScreener({runtimeAvailable:true,runtimeError:''});window.go!.main!.App!.StartScreenerDownload=async()=>{throw Error('Explicit process launch failure');};});await expect(page.getByRole('button',{name:'Download 4 symbols',exact:true})).toBeEnabled();await page.getByRole('button',{name:'Download 4 symbols',exact:true}).click();await expect(page.getByRole('alert')).toContainText('Explicit process launch failure');await expect(page.locator('.screener-job')).toHaveCount(0);
});

test('polling stops while chart tabs are visible and cannot replace a newer start result',async({page})=>{
 await page.clock.install();await page.goto('/');await downloader(page);await page.getByRole('checkbox',{name:'Select all Cryptocurrencies',exact:true}).check();
 await page.evaluate(()=>{const api=window.go!.main!.App!,original=api.GetScreener;(window as any).__originalScreenerGet=original;api.GetScreener=async()=>{api.GetScreener=original;const before=await original();return new Promise(resolve=>(window as any).__releaseOldScreenerRead=()=>resolve(before));};});await page.clock.fastForward(800);await expect.poll(()=>page.evaluate(()=>typeof(window as any).__releaseOldScreenerRead)).toBe('function');
 await page.getByRole('button',{name:'Download 2 symbols',exact:true}).click();await expect(page.locator('.screener-job')).toHaveCount(2);await page.evaluate(()=>(window as any).__releaseOldScreenerRead());await expect(page.locator('.screener-job')).toHaveCount(2);
 await chartTabs(page).first().click();const reads=await page.evaluate(()=>(window as any).__testScreenerReads());await page.clock.fastForward(10000);expect(await page.evaluate(()=>(window as any).__testScreenerReads())).toBe(reads);await mainTab(page).click();await expect(page.locator('.screener-job')).toHaveCount(2);await expect(page.getByRole('combobox',{name:'Download timeframe',exact:true})).toHaveValue('d1');
});

test('the last navigation click wins when saving before opening Screener is delayed',async({page})=>{
 await page.goto('/');await expect(page.getByTestId('chart-cell')).toHaveCount(1);await page.evaluate(()=>{const api=window.go!.main!.App!,original=api.SaveWorkspace;api.SaveWorkspace=async(...args)=>{api.SaveWorkspace=original;await new Promise<void>(resolve=>(window as any).__releaseScreenerSave=resolve);return original(...args);};});
 await page.getByRole('button',{name:'Timeframe 4h',exact:true}).click();await mainTab(page).click();await expect.poll(()=>page.evaluate(()=>typeof(window as any).__releaseScreenerSave)).toBe('function');await chartTabs(page).first().click();await page.evaluate(()=>(window as any).__releaseScreenerSave());await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();await expect(mainTab(page)).toHaveAttribute('aria-selected','false');await expect(page.getByTestId('chart-cell')).toHaveCount(1);
});

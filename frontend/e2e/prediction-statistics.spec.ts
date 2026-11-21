import {test,expect,type Page} from '@playwright/test';
import {openTestApp} from './helpers/testApp';
import type {PredictionStatisticsForecast} from '../src/workspace/predictionStatistics';
import type {ScreenerInfo} from '../src/types';

const panel=(page:Page)=>page.getByRole('tabpanel',{name:'Predictions',exact:true});
const section=(page:Page,name:'LLM'|'Statistics')=>panel(page).getByRole('tab',{name,exact:true});
const statisticsCalls=(page:Page)=>page.evaluate(()=>(window as any).__testStatisticsCalls());
const llmCalls=(page:Page)=>page.evaluate(()=>(window as any).__testPredictionCalls());
const chart=(page:Page)=>panel(page).getByTestId('statistics-chart');
const scenarios=(page:Page)=>panel(page).getByRole('table',{name:'Remaining-week scenarios',exact:true});
const source=(page:Page)=>panel(page).getByLabel('Statistics data source',{exact:true});
const asset=(page:Page)=>panel(page).getByLabel('Statistics asset',{exact:true});
const picker=(page:Page)=>panel(page).getByLabel('Statistics assets',{exact:true});
async function chooseAsset(page:Page,symbol:string){await asset(page).click();await picker(page).getByRole('button',{name:'All',exact:true}).click();await picker(page).locator(`[data-asset-id="${symbol}"]`).click();}
const period=(page:Page)=>panel(page).getByLabel('Statistics time period',{exact:true});
const calculate=(page:Page)=>panel(page).getByRole('button',{name:/^(Calculate scenarios|Calculating…)$/});
const downloadButton=(page:Page)=>panel(page).getByRole('button',{name:'Download missing data',exact:true});
const downloadRegion=(page:Page)=>panel(page).getByRole('region',{name:'Statistics data download',exact:true});
const cancelDownload=(page:Page)=>downloadRegion(page).getByRole('button',{name:'Cancel data download',exact:true});
const downloadCalls=(page:Page)=>page.evaluate(()=>(window as any).__testStatisticsDownloadCalls());
const downloadInfo=(page:Page):Promise<ScreenerInfo>=>page.evaluate(()=>(window as any).__testStatisticsDownloadInfo());
async function startDownload(page:Page){await downloadButton(page).click();await expect(downloadRegion(page)).toBeVisible();await expect(cancelDownload(page)).toBeEnabled();}


async function enter(page:Page){
 await openTestApp(page);
 await page.locator('#predictions-tab').click();
 await expect(section(page,'LLM')).toHaveAttribute('aria-selected','true');
 await section(page,'Statistics').click();
 await expect(source(page)).toBeEnabled();
 await chooseAsset(page,'eurusd');
}
async function forecast(page:Page){
 return page.evaluate(()=>window.go!.main!.App!.GetPredictionStatistics({dataSource:'dukascopy',asset:'eurusd',period:'d1',lookbackWeeks:260}));
}
async function setForecast(page:Page,value:PredictionStatisticsForecast|{error:string}|null){
 await page.evaluate(value=>(window as any).__testSetStatisticsForecast(value),value);
}
async function completed(page:Page){await calculate(page).click();await expect(chart(page)).toBeVisible();await expect(scenarios(page)).toBeVisible();}

test('Predictions defaults to LLM and Statistics uses source-specific daily data without model work',async({page})=>{
 await enter(page);
 const before=await llmCalls(page);
 await expect(panel(page).getByLabel('Filter statistics assets',{exact:true})).toHaveCount(0);
 await asset(page).click();await picker(page).getByRole('textbox',{name:'Search instruments',exact:true}).fill('Euro');
 await expect(picker(page).locator('[data-asset-id="eurusd"]')).toBeVisible();
 await expect(picker(page).locator('[data-asset-id="btcusd"]')).toHaveCount(0);
 await picker(page).getByRole('button',{name:'Close instrument selector',exact:true}).click();
 await panel(page).getByLabel('Statistics history period',{exact:true}).selectOption('260');
 await completed(page);
 expect((await statisticsCalls(page)).requests.at(-1)).toEqual({dataSource:'dukascopy',asset:'eurusd',period:'d1',lookbackWeeks:260});
 await source(page).selectOption('hyperliquid');
 await expect(chart(page)).toHaveCount(0);
 await asset(page).click();
 await expect(picker(page).locator('[data-asset-id="eurusd"]')).toHaveCount(0);
 await expect(picker(page).locator('[data-asset-id="perp-BTC"]')).toBeVisible();
 await picker(page).getByRole('button',{name:'Close instrument selector',exact:true}).click();
 await chooseAsset(page,'perp-BTC');await completed(page);
 expect((await statisticsCalls(page)).requests.at(-1)).toEqual({dataSource:'hyperliquid',asset:'perp-BTC',period:'d1',lookbackWeeks:260});
 await expect(panel(page)).toContainText('hyperliquid-perp-BTC-d1.csv');
 const after=await llmCalls(page);expect(after.prepare).toBe(before.prepare);expect(after.start).toBe(before.start);
});

test('weekly scenarios paint Monday anchor, Friday prices and joint probabilities with historical counts',async({page})=>{
 await page.addInitScript(()=>{
  const original=CanvasRenderingContext2D.prototype.fillText;
  (window as any).__statisticsPaintedText=[];
  CanvasRenderingContext2D.prototype.fillText=function(...args:Parameters<CanvasRenderingContext2D['fillText']>){(window as any).__statisticsPaintedText.push(String(args[0]));return original.apply(this,args);};
 });
 await enter(page);await completed(page);
 await expect(panel(page).locator('.prediction-metadata')).toContainText('First closed bar 2018-01-01 00:00:00 UTC');
 await expect(panel(page)).toContainText(/Matching weeks\s*4/);
 await expect(panel(page)).toContainText(/Eligible weeks\s*12/);
 await expect(panel(page)).toContainText(/Remaining-week sequences\s*16/);
 await expect(panel(page)).toContainText(/small sample/i);
 const rows=scenarios(page).getByRole('row');await expect(rows).toHaveCount(3);
 await expect(rows.nth(1)).toContainText(/75(?:\.0+)?%/);await expect(rows.nth(1)).toContainText('98');
 await expect(rows.nth(2)).toContainText(/25(?:\.0+)?%/);await expect(rows.nth(2)).toContainText('102');
 await expect(chart(page).locator('canvas').first()).toBeVisible();
 await expect(chart(page)).toHaveAttribute('data-anchor-time',String(Date.UTC(2026,10,16)));
 await expect(chart(page)).toHaveAttribute('data-friday-time',String(Date.UTC(2026,10,20)));
 await expect.poll(()=>page.evaluate(()=>(window as any).__statisticsPaintedText.some((text:string)=>text.includes('UDDD')&&/75(?:\.0+)?%/.test(text)))).toBe(true);
 await expect.poll(()=>page.evaluate(()=>(window as any).__statisticsPaintedText.some((text:string)=>/^98(?:\.0+)?$/.test(text)))).toBe(true);
 await expect.poll(()=>page.evaluate(()=>(window as any).__statisticsPaintedText.some((text:string)=>/Fri.*20.*Nov/.test(text)))).toBe(true);
 expect(await page.evaluate(()=>(window as any).__statisticsPaintedText.some((text:string)=>text.includes('1970')))).toBe(false);
 await page.screenshot({path:'test-results/prediction-statistics-weekly.png'});
});

test('time period uses downloaded H1 D1 and W1 bars independently from historical lookback',async({page})=>{
 await page.addInitScript(()=>{
  const original=CanvasRenderingContext2D.prototype.fillText;
  (window as any).__statisticsHourlyText=[];
  CanvasRenderingContext2D.prototype.fillText=function(...args:Parameters<CanvasRenderingContext2D['fillText']>){(window as any).__statisticsHourlyText.push(String(args[0]));return original.apply(this,args);};
 });
 await enter(page);
 expect(await period(page).locator('option').evaluateAll(options=>options.map(option=>(option as HTMLOptionElement).value))).toEqual(['h1','d1','w1']);
 await expect(period(page)).toHaveValue('d1');
 await panel(page).getByLabel('Statistics history period',{exact:true}).selectOption('156');
 await period(page).selectOption('h1');await completed(page);
 expect((await statisticsCalls(page)).requests.at(-1)).toEqual({dataSource:'dukascopy',asset:'eurusd',period:'h1',lookbackWeeks:156});
 await expect(chart(page)).toHaveAttribute('data-period','h1');
 await expect(panel(page).locator('.prediction-metadata')).toContainText('First closed bar 2019-01-01 00:00:00 UTC');
 await expect(chart(page)).toHaveAttribute('data-anchor-time',String(Date.UTC(2026,10,17,11)));
 await expect(chart(page)).toHaveAttribute('data-friday-time',String(Date.UTC(2026,10,20,23)));
 await expect(chart(page)).toHaveAttribute('data-forecast-grid-bars','84');
 await chart(page).screenshot({path:'test-results/statistics-hourly.png'});
 await expect(panel(page).getByRole('heading',{level:2}).filter({hasText:'Euro vs US Dollar'})).toContainText('H1');
 await expect(panel(page)).toContainText('eurusd-h1.csv');await expect(panel(page)).toContainText('D1 companion: eurusd-d1.csv');
 await expect(scenarios(page).getByRole('columnheader',{name:/Friday close/})).toBeVisible();
 await expect.poll(()=>page.evaluate(()=>(window as any).__statisticsHourlyText.some((text:string)=>/(?:Mon|Tue|Wed|Thu|Fri).*Nov.*\d{2}:\d{2}/.test(text)))).toBe(true);
 await period(page).selectOption('w1');await expect(chart(page)).toHaveCount(0);await completed(page);
 expect((await statisticsCalls(page)).requests.at(-1)).toEqual({dataSource:'dukascopy',asset:'eurusd',period:'w1',lookbackWeeks:156});
 await expect(chart(page)).toHaveAttribute('data-period','w1');
 await expect(panel(page).locator('.prediction-metadata')).toContainText('First closed bar 2017-01-02 00:00:00 UTC');
 await expect(chart(page)).toHaveAttribute('data-anchor-time',String(Date.UTC(2026,10,9)));
 await expect(chart(page)).toHaveAttribute('data-friday-time',String(Date.UTC(2026,10,20)));
 await chart(page).screenshot({path:'test-results/statistics-weekly.png'});
 await expect(panel(page).getByRole('heading',{level:2}).filter({hasText:'Euro vs US Dollar'})).toContainText('W1');
 await expect(panel(page)).toContainText('eurusd-w1.csv');
 await expect(scenarios(page).getByRole('columnheader',{name:/Monday close.*observed/i})).toBeVisible();
 await chooseAsset(page,'btcusd');await expect(chart(page)).toHaveCount(0);await expect(period(page)).toHaveValue('d1');
 expect(await period(page).locator('option').evaluateAll(options=>options.map(option=>(option as HTMLOptionElement).value))).toEqual(['d1']);
 await expect(panel(page).getByLabel('Statistics history period',{exact:true})).toHaveValue('156');
 await source(page).selectOption('hyperliquid');await chooseAsset(page,'perp-BTC');
 expect(await period(page).locator('option').evaluateAll(options=>options.map(option=>(option as HTMLOptionElement).value))).toEqual(['h1','d1','w1']);
 await period(page).selectOption('h1');await completed(page);
 expect((await statisticsCalls(page)).requests.at(-1)).toEqual({dataSource:'hyperliquid',asset:'perp-BTC',period:'h1',lookbackWeeks:156});
 await expect(panel(page)).toContainText('hyperliquid-perp-BTC-h1.csv');
});

test('no matching history and a finished Friday show empty outcomes without invented scenarios',async({page})=>{
 await enter(page);const base=await forecast(page);
 await setForecast(page,{...base,matchedWeeks:0,scenarios:[],warnings:['No historical weeks match the observed daily directions.']});
 await calculate(page).click();await expect(panel(page)).toContainText('No historical weeks match');await expect(scenarios(page).getByRole('row')).toHaveCount(0);
 const friday=Date.UTC(2026,10,20),fridayBar={...base.anchor,time:friday,open:99,close:100};
 const remainingDays=['Tue','Wed','Thu','Fri'] as const;
 const completedWeek=remainingDays.map((day,index)=>({time:base.weekStart+(index+1)*86400000,day,direction:'up' as const}));
 await setForecast(page,{...base,asOf:friday+86400000,anchor:fridayBar,history:[...base.history,...completedWeek.map(value=>({...fridayBar,time:value.time}))],prefix:[...base.prefix,...completedWeek],theoreticalScenarios:0,scenarios:[],warnings:['Friday is closed. No weekdays remain in this week.']});
 await calculate(page).click();await expect(panel(page)).toContainText('No weekdays remain');await expect(scenarios(page).getByRole('row')).toHaveCount(0);
 await expect(panel(page)).not.toContainText(/75(?:\.0+)?%/);
});

test('completed bearish weeks retain historical match counts and distinguish lookback from file coverage',async({page})=>{
 await enter(page);const base=await forecast(page);
 const prefix=['Mon','Tue','Wed','Thu','Fri'].map((day,index)=>({time:base.weekStart+index*86400000,day,direction:'down' as const}));
 const friday={time:prefix[4].time,open:100,high:101,low:98,close:99};
 await panel(page).getByLabel('Statistics history period',{exact:true}).selectOption('520');
 await setForecast(page,{...base,lookbackWeeks:520,firstClosedBar:Date.UTC(2006,0,1),asOf:friday.time+86400000,anchor:friday,history:[friday],prefix,matchedWeeks:13,eligibleWeeks:513,theoreticalScenarios:0,scenarios:[],warnings:[]});
 await calculate(page).click();await expect(chart(page)).toBeVisible();
 await expect(panel(page)).toContainText('Mon ↓ · Tue ↓ · Wed ↓ · Thu ↓ · Fri ↓');
 await expect(panel(page).getByLabel('Statistics sample scope',{exact:true})).toContainText('last 520 weeks');
 await expect(panel(page).locator('.prediction-metadata')).toContainText('First closed bar 2006-01-01');
 await expect(panel(page).locator('.statistics-summary>div').filter({hasText:'Matching weeks'}).locator('strong')).toHaveText('13');
 await expect(panel(page).locator('.statistics-summary>div').filter({hasText:'Eligible weeks'}).locator('strong')).toHaveText('513');
 await expect(panel(page).locator('.statistics-summary>div').filter({hasText:'Remaining-week sequences'}).locator('strong')).toHaveText('0');
 await expect(panel(page)).toContainText('The observed sequence matches 13 of 513 eligible historical weeks.');
 await expect(panel(page)).toContainText('Friday is already complete');
 await expect(scenarios(page)).toHaveCount(0);await expect(chart(page)).toHaveAttribute('data-scenario-count','0');
 await panel(page).locator('.prediction-statistics').screenshot({path:'test-results/statistics-completed-bearish-week.png'});
 await panel(page).getByLabel('Statistics history period',{exact:true}).selectOption('0');
 await setForecast(page,{...base,lookbackWeeks:0,firstClosedBar:Date.UTC(2006,0,1),asOf:friday.time+86400000,anchor:friday,history:[friday],prefix,matchedWeeks:29,eligibleWeeks:1080,theoreticalScenarios:0,scenarios:[],warnings:[]});
 await calculate(page).click();await expect(panel(page).getByLabel('Statistics sample scope',{exact:true})).toContainText('all downloaded history');
 await expect(panel(page)).toContainText('The observed sequence matches 29 of 1080 eligible historical weeks.');
 await expect(scenarios(page)).toHaveCount(0);await expect(chart(page)).toHaveAttribute('data-scenario-count','0');
});

test('statistics calculation errors remain visible until an explicit retry',async({page})=>{
 await enter(page);await setForecast(page,{error:'Daily CSV checksum failed in explicit fixture'});
 await calculate(page).click();await expect(panel(page).getByRole('alert')).toContainText('Daily CSV checksum failed');
 const before=await statisticsCalls(page);await expect.poll(()=>statisticsCalls(page)).toEqual(before);
 await expect(chart(page)).toHaveCount(0);
 await setForecast(page,null);await completed(page);
 expect((await statisticsCalls(page)).calculate).toBe(before.calculate+1);
 await expect(panel(page).getByRole('alert')).toHaveCount(0);
});

test('a failed daily-data catalog stays explicit and retries only on request',async({page})=>{
 await openTestApp(page);
 await page.evaluate(()=>{
  const api=window.go!.main!.App!,list=api.GetPredictionStatisticsDatasets;
  (window as any).__statisticsCatalogFailures=0;
  api.GetPredictionStatisticsDatasets=async()=>{(window as any).__statisticsCatalogFailures++;throw Error('Daily data directory unavailable in explicit fixture');};
  (window as any).__restoreStatisticsDatasets=()=>{api.GetPredictionStatisticsDatasets=list;};
 });
 await page.locator('#predictions-tab').click();await section(page,'Statistics').click();
 await expect(panel(page).getByRole('alert')).toContainText('Daily data directory unavailable');
 await expect(calculate(page)).toBeDisabled();
 expect(await page.evaluate(()=>(window as any).__statisticsCatalogFailures)).toBe(1);
 await page.evaluate(()=>(window as any).__restoreStatisticsDatasets());
 await panel(page).getByRole('button',{name:'Retry assets',exact:true}).click();
 await chooseAsset(page,'eurusd');await completed(page);
 await expect(panel(page).getByRole('alert')).toHaveCount(0);
});

test('changing source rejects a stalled old result and allows only one calculation in flight',async({page})=>{
 await enter(page);
 await page.evaluate(()=>{
  const api=window.go!.main!.App!,run=api.GetPredictionStatistics;
  api.GetPredictionStatistics=async request=>{const result=await run(request);await new Promise<void>(resolve=>(window as any).__releaseStatistics=resolve);return result;};
  (window as any).__restoreStatistics=()=>{api.GetPredictionStatistics=run;};
 });
 await calculate(page).click();await expect(calculate(page)).toBeDisabled();
 await source(page).selectOption('hyperliquid');await chooseAsset(page,'perp-BTC');
 await expect(chart(page)).toHaveCount(0);await expect(calculate(page)).toBeDisabled();
 await page.evaluate(()=>(window as any).__releaseStatistics());await expect(calculate(page)).toBeEnabled();
 await expect(chart(page)).toHaveCount(0);expect((await statisticsCalls(page)).calculate).toBe(1);
 await page.evaluate(()=>(window as any).__restoreStatistics());await completed(page);
 await expect(panel(page)).toContainText('hyperliquid-perp-BTC-d1.csv');
 expect((await statisticsCalls(page)).requests.at(-1).dataSource).toBe('hyperliquid');
});

test('a changed time period rejects stale native-bar results without overlapping calculations',async({page})=>{
 await enter(page);
 await page.evaluate(()=>{
  const api=window.go!.main!.App!,run=api.GetPredictionStatistics;
  api.GetPredictionStatistics=async request=>{const result=await run(request);await new Promise<void>(resolve=>(window as any).__releasePeriodStatistics=resolve);return result;};
  (window as any).__restorePeriodStatistics=()=>{api.GetPredictionStatistics=run;};
 });
 await calculate(page).click();await expect(calculate(page)).toBeDisabled();
 await period(page).selectOption('h1');await expect(chart(page)).toHaveCount(0);await expect(calculate(page)).toBeDisabled();
 await page.evaluate(()=>(window as any).__releasePeriodStatistics());await expect(calculate(page)).toBeEnabled();
 await expect(chart(page)).toHaveCount(0);expect((await statisticsCalls(page)).calculate).toBe(1);
 await page.evaluate(()=>(window as any).__restorePeriodStatistics());await completed(page);
 expect((await statisticsCalls(page)).requests.at(-1).period).toBe('h1');await expect(chart(page)).toHaveAttribute('data-period','h1');
});

test('a missing daily companion is reported instead of inventing non-daily scenarios',async({page})=>{
 await enter(page);await period(page).selectOption('h1');
 await setForecast(page,{error:'Downloaded D1 companion data is required for H1 weekly scenarios.'});
 await calculate(page).click();await expect(panel(page).getByRole('alert')).toContainText('D1 companion data is required');
 await expect(chart(page)).toHaveCount(0);await expect(scenarios(page)).toHaveCount(0);
 expect((await statisticsCalls(page)).requests.at(-1).period).toBe('h1');
 await setForecast(page,null);await completed(page);await expect(chart(page)).toHaveAttribute('data-period','h1');
});

test('statistics releases charts on navigation and leaves saved LLM results untouched',async({page})=>{
 await openTestApp(page);await page.evaluate(()=>(window as any).__testPredictionReady());
 await page.locator('#predictions-tab').click();
 await panel(page).getByLabel('Prediction asset',{exact:true}).selectOption('btcusd');
 await panel(page).getByRole('button',{name:'Start prediction',exact:true}).click();
 await page.evaluate(()=>(window as any).__testFinishPrediction());
 await expect(panel(page).getByTestId('prediction-chart')).toBeVisible();
 const before=await page.evaluate(()=>(window as any).__testPredictionInfo());
 await section(page,'Statistics').click();await chooseAsset(page,'eurusd');await completed(page);
 await expect.poll(()=>page.evaluate(()=>(window as any).__testPredictionResources())).toEqual({charts:0,observers:0,listeners:0});
 await expect.poll(()=>page.evaluate(()=>(window as any).__testStatisticsResources().charts)).toBe(1);
 await section(page,'LLM').click();
 await expect.poll(()=>page.evaluate(()=>(window as any).__testStatisticsResources())).toEqual({charts:0,observers:0,listeners:0});
 await expect(panel(page).getByTestId('prediction-chart')).toBeVisible();
 await expect(panel(page).getByLabel('Selected forecast',{exact:true})).toContainText('btcusd-d1.csv');
 const after=await page.evaluate(()=>(window as any).__testPredictionInfo());expect(after.forecasts).toEqual(before.forecasts);expect(after.selection).toEqual(before.selection);
 await section(page,'Statistics').click();await completed(page);
 await page.getByRole('tab',{name:'Screener',exact:true}).click();
 await expect.poll(()=>page.evaluate(()=>(window as any).__testStatisticsResources())).toEqual({charts:0,observers:0,listeners:0});
 await page.locator('[data-tab-id]').first().click();await expect.poll(()=>page.evaluate(()=>(window as any).__testLeases())).toBe(1);
});


test('Download missing data reports D1 progress and recalculates from the newly closed candle once',async({page})=>{
 await enter(page);const base=await forecast(page);await completed(page);
 const before=await statisticsCalls(page),models=await llmCalls(page);
 await expect(chart(page)).toHaveAttribute('data-anchor-time',String(Date.UTC(2026,10,16)));
 await startDownload(page);
 expect((await downloadCalls(page)).requests).toEqual([{dataSource:'dukascopy',asset:'eurusd',period:'d1',lookbackWeeks:260}]);
 const queued=await downloadInfo(page);expect(queued.jobs.map(job=>job.timeframe)).toEqual(['d1']);
 await expect(downloadButton(page)).toBeDisabled();await expect(calculate(page)).toBeDisabled();
 await expect(panel(page).getByRole('button',{name:'Refresh assets',exact:true})).toBeDisabled();
 await expect(downloadRegion(page).getByRole('progressbar')).toHaveCount(1);
 await page.evaluate(()=>{
  const info=(window as any).__testStatisticsDownloadInfo();
  (window as any).__testSetStatisticsDownloadInfo({jobs:info.jobs.map((job:any)=>({...job,status:'running',progress:42,message:'Downloading missing daily candles'}))});
 });
 await expect(downloadRegion(page)).toContainText('Downloading missing daily candles');
 await expect(downloadRegion(page).getByRole('progressbar')).toHaveAttribute('value','42');
 const anchor={time:Date.UTC(2026,10,17),open:100,high:106,low:99,close:105};
 const refreshed:PredictionStatisticsForecast={...base,asOf:Date.UTC(2026,10,18),anchor,history:[...base.history,anchor],prefix:[...base.prefix,{time:anchor.time,day:'Tue',direction:'up'}],scenarios:base.scenarios.map(scenario=>({...scenario,id:scenario.id.slice(1),label:scenario.label.split(' · ').slice(1).join(' · '),points:scenario.points.slice(1)})),theoreticalScenarios:8};
 await page.evaluate(value=>(window as any).__testFinishStatisticsDownload(value),refreshed);
 await expect(chart(page)).toHaveAttribute('data-anchor-time',String(anchor.time));
 await expect(downloadButton(page)).toBeEnabled();
 expect((await statisticsCalls(page)).calculate).toBe(before.calculate+1);
 expect((await statisticsCalls(page)).datasets).toBeGreaterThan(before.datasets);
 expect((await downloadCalls(page)).start).toBe(1);
 const after=await llmCalls(page);expect(after.prepare).toBe(models.prepare);expect(after.start).toBe(models.start);
});

test('non-daily download repairs missing companions and scopes each H1 or W1 batch to its D1 dependency',async({page})=>{
 await enter(page);await period(page).selectOption('h1');
 await setForecast(page,{error:'Downloaded D1 companion data is required for H1 weekly scenarios.'});
 await calculate(page).click();await expect(panel(page).getByRole('alert')).toContainText('D1 companion data is required');
 for(const selectedPeriod of ['h1','w1']){
  await period(page).selectOption(selectedPeriod);await startDownload(page);
  const info=await downloadInfo(page);
  expect(info.jobs.map(job=>job.timeframe).sort()).toEqual(['d1',selectedPeriod].sort());
  expect(info.jobs.every(job=>job.symbol==='eurusd'&&job.dataSource==='dukascopy')).toBe(true);
  expect(new Set(info.jobs.map(job=>job.id)).size).toBe(2);
  await expect(downloadRegion(page).getByRole('progressbar')).toHaveCount(2);
  await page.evaluate(()=>(window as any).__testFinishStatisticsDownload(null));
  await expect(chart(page)).toHaveAttribute('data-period',selectedPeriod);await expect(downloadButton(page)).toBeEnabled();
  await expect(panel(page).getByRole('alert')).toHaveCount(0);
 }
 expect((await downloadCalls(page)).requests.map((request:any)=>request.period)).toEqual(['h1','w1']);
});

test('a failed companion remains visible while another job is running and never causes an automatic retry',async({page})=>{
 await enter(page);await page.clock.install();await period(page).selectOption('h1');await startDownload(page);
 const before=await statisticsCalls(page);
 await page.evaluate(()=>{
  const info=(window as any).__testStatisticsDownloadInfo();
  (window as any).__testSetStatisticsDownloadInfo({active:true,downloadsPaused:true,jobs:info.jobs.map((job:any)=>job.timeframe==='d1'?{...job,status:'failed',progress:20,message:'Daily provider rejected the missing range'}:{...job,status:'running',progress:40,message:'Hourly candles still downloading'})});
 });
 await expect(downloadRegion(page)).toContainText('Daily provider rejected the missing range');
 await expect(downloadRegion(page)).toContainText(/paused/i);
 await expect(downloadButton(page)).toBeDisabled();await expect(calculate(page)).toBeDisabled();await expect(cancelDownload(page)).toBeEnabled();
 expect((await statisticsCalls(page)).calculate).toBe(before.calculate);
 await page.evaluate(()=>{
  const info=(window as any).__testStatisticsDownloadInfo();
  (window as any).__testSetStatisticsDownloadInfo({active:false,downloadsPaused:false,jobs:info.jobs.map((job:any)=>job.status==='running'?{...job,status:'completed',progress:100,message:'Hourly candles completed'}:job)});
 });
 await expect(downloadButton(page)).toBeEnabled();
 const terminal=await downloadCalls(page);await page.clock.fastForward(2500);
 expect((await downloadCalls(page)).start).toBe(1);expect((await downloadCalls(page)).status).toBe(terminal.status);
 expect((await statisticsCalls(page)).calculate).toBe(before.calculate);await expect(chart(page)).toHaveCount(0);
});

test('Cancel data download cancels only the owned source asset and period jobs',async({page})=>{
 await enter(page);await source(page).selectOption('hyperliquid');await chooseAsset(page,'perp-BTC');await period(page).selectOption('h1');
 await startDownload(page);const owned=(await downloadInfo(page)).jobs.map(job=>job.id),before=await statisticsCalls(page);
 await page.evaluate(()=>{
  const info=(window as any).__testStatisticsDownloadInfo();
  (window as any).__testSetStatisticsDownloadInfo({jobs:[...info.jobs,{id:'unrelated-screener-download',dataSource:'dukascopy',symbol:'eurusd',timeframe:'d1',from:'2026-01-01',to:'2026-11-20',status:'running',progress:30,message:'Unrelated active screener job'}]});
 });
 await cancelDownload(page).click();await expect(downloadButton(page)).toBeEnabled();
 const calls=await downloadCalls(page);expect(calls.cancelRequests).toHaveLength(1);expect([...calls.cancelRequests[0]].sort()).toEqual([...owned].sort());
 const info=await downloadInfo(page);expect(info.jobs.filter(job=>owned.includes(job.id)).every(job=>job.status==='cancelled')).toBe(true);
 expect(info.jobs.find(job=>job.id==='unrelated-screener-download')?.status).toBe('running');
 expect((await statisticsCalls(page)).calculate).toBe(before.calculate);await expect(chart(page)).toHaveCount(0);
});

test('hidden Statistics pauses download status polling and an old selection completion cannot paint the new source',async({page})=>{
 await enter(page);await page.clock.install();await startDownload(page);
 await expect.poll(async()=>(await downloadCalls(page)).status).toBeGreaterThan(0);
 await source(page).selectOption('hyperliquid');await chooseAsset(page,'perp-BTC');await period(page).selectOption('h1');
 await page.getByRole('tab',{name:'Screener',exact:true}).click();await expect(downloadRegion(page)).not.toBeVisible();
 const hidden=await downloadCalls(page),before=await statisticsCalls(page);await page.clock.fastForward(2500);
 expect((await downloadCalls(page)).status).toBe(hidden.status);
 await page.evaluate(()=>(window as any).__testFinishStatisticsDownload(null));
 expect((await statisticsCalls(page)).calculate).toBe(before.calculate);
 await page.locator('#predictions-tab').click();await expect(downloadButton(page)).toBeEnabled();
 await expect.poll(async()=>(await downloadCalls(page)).status).toBeGreaterThan(hidden.status);
 expect((await statisticsCalls(page)).calculate).toBe(before.calculate);await expect(chart(page)).toHaveCount(0);
 await completed(page);await expect(chart(page)).toHaveAttribute('data-period','h1');
 await expect(panel(page)).toContainText('hyperliquid-perp-BTC-h1.csv');
});

test('a missing or replaced download job stops monitoring without recalculating or cancelling another job',async({page})=>{
 await enter(page);await page.clock.install();await startDownload(page);const before=await statisticsCalls(page);
 await page.evaluate(()=>{
  const info=(window as any).__testStatisticsDownloadInfo();
  (window as any).__testSetStatisticsDownloadInfo({jobs:info.jobs.map((job:any)=>({...job,id:'replacement-download',symbol:'btcusd',status:'running',progress:50,message:'Different replacement download'}))});
 });
 await expect(downloadRegion(page)).toContainText(/missing or were replaced/i);
 const stopped=await downloadCalls(page);await page.clock.fastForward(2500);
 expect((await downloadCalls(page)).status).toBe(stopped.status);expect((await downloadCalls(page)).start).toBe(1);
 expect((await downloadCalls(page)).cancel).toBe(0);expect((await statisticsCalls(page)).calculate).toBe(before.calculate);
 expect((await downloadInfo(page)).jobs[0].status).toBe('running');await expect(chart(page)).toHaveCount(0);
 await downloadRegion(page).getByRole('button',{name:'Stop tracking download',exact:true}).click();
 await expect(downloadButton(page)).toBeEnabled();await expect(calculate(page)).toBeEnabled();
 expect((await downloadCalls(page)).cancel).toBe(0);expect((await downloadCalls(page)).start).toBe(1);
 expect((await downloadInfo(page)).jobs[0].status).toBe('running');
});

test('Statistics uses the chart asset window with source market filters and keeps the current selection when filtering',async({page})=>{
 await enter(page);await asset(page).click();
 await picker(page).getByRole('button',{name:'Forex',exact:true}).click();
 await expect(picker(page).locator('[data-asset-id="eurusd"]')).toBeVisible();
 await expect(picker(page).locator('[data-asset-id="btcusd"]')).toHaveCount(0);
 await picker(page).getByRole('button',{name:'CFD',exact:true}).click();
 await expect(picker(page).locator('[data-asset-id="eurusd"]')).toHaveCount(0);
 await picker(page).locator('[data-asset-id="btcusd"]').click();
 await expect(asset(page)).toContainText('Bitcoin vs US Dollar');
 await asset(page).click();await picker(page).getByRole('button',{name:'Forex',exact:true}).click();
 await picker(page).getByRole('textbox',{name:'Search instruments',exact:true}).fill('Bitcoin');
 await expect(picker(page)).toContainText('No matching instruments');
 await picker(page).getByRole('textbox',{name:'Search instruments',exact:true}).press('Escape');
 await expect(picker(page)).toHaveCount(0);await expect(asset(page)).toBeFocused();
 await expect(asset(page)).toContainText('Bitcoin vs US Dollar');
 await source(page).selectOption('hyperliquid');await asset(page).click();
 await expect(picker(page).getByRole('button',{name:'Forex',exact:true})).toHaveCount(0);
 await picker(page).getByRole('button',{name:'Spot',exact:true}).click();
 await expect(picker(page).locator('[data-asset-id="perp-BTC"]')).toHaveCount(0);
 await picker(page).locator('[data-asset-id="spot-UBTC-USDC"]').click();
 await expect(asset(page)).toContainText('UBTC / USDC');await completed(page);
 expect((await statisticsCalls(page)).requests.at(-1)).toMatchObject({dataSource:'hyperliquid',asset:'spot-UBTC-USDC',period:'d1'});
 await asset(page).click();await picker(page).getByRole('button',{name:'Perpetual',exact:true}).click();
 await expect(picker(page).locator('[data-asset-id="spot-UBTC-USDC"]')).toHaveCount(0);
 await picker(page).locator('[data-asset-id="perp-BTC"]').click();
 await expect(asset(page)).toContainText('Bitcoin perpetual');
 await expect(chart(page)).toHaveCount(0);
 await asset(page).click();await period(page).click();await expect(picker(page)).toHaveCount(0);
});

import {test,expect,type Page} from '@playwright/test';
import {openTestApp} from './helpers/testApp';
import type {PredictionStatisticsForecast} from '../src/workspace/predictionStatistics';
import type {ScreenerInfo} from '../src/types';

const panel=(page:Page)=>page.getByRole('tabpanel',{name:'Predictions',exact:true});
const section=(page:Page,name:'LLM'|'Statistics')=>panel(page).getByRole('tab',{name,exact:true});
const statisticsCalls=(page:Page)=>page.evaluate(()=>(window as any).__testStatisticsCalls());
const llmCalls=(page:Page)=>page.evaluate(()=>(window as any).__testPredictionCalls());
const chart=(page:Page)=>panel(page).getByTestId('statistics-chart');
const scenarios=(page:Page)=>panel(page).getByRole('table',{name:'Next-bar outcomes',exact:true});
const source=(page:Page)=>panel(page).getByLabel('Statistics data source',{exact:true});
const asset=(page:Page)=>panel(page).getByLabel('Statistics asset',{exact:true});
const picker=(page:Page)=>panel(page).getByLabel('Statistics assets',{exact:true});
async function chooseAsset(page:Page,symbol:string){await asset(page).click();await picker(page).getByRole('button',{name:'All',exact:true}).click();await picker(page).locator(`[data-asset-id="${symbol}"]`).click();}
const period=(page:Page)=>panel(page).getByLabel('Statistics time period',{exact:true});
const calculate=(page:Page)=>panel(page).getByRole('button',{name:/^(Calculate next bar|Calculating…)$/});
const downloadButton=(page:Page)=>panel(page).getByRole('button',{name:'Download missing data',exact:true});
const downloadRegion=(page:Page)=>panel(page).getByRole('region',{name:'Statistics data download',exact:true});
const cancelDownload=(page:Page)=>downloadRegion(page).getByRole('button',{name:'Cancel data download',exact:true});
const patternSource=(page:Page,mode:'Custom bars'|'Current bars')=>panel(page).getByRole('radiogroup',{name:'Pattern source',exact:true}).getByRole('radio',{name:mode,exact:true});
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
 return page.evaluate(()=>window.go!.main!.App!.GetPredictionStatistics({dataSource:'dukascopy',asset:'eurusd',period:'d1',lookbackWeeks:260,pattern:['up','up','up','up','up']}));
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
 expect((await statisticsCalls(page)).requests.at(-1)).toEqual({dataSource:'dukascopy',asset:'eurusd',period:'d1',lookbackWeeks:260,pattern:['up','up','up','up','up']});
 await source(page).selectOption('hyperliquid');
 await expect(chart(page)).toHaveCount(0);
 await asset(page).click();
 await expect(picker(page).locator('[data-asset-id="eurusd"]')).toHaveCount(0);
 await expect(picker(page).locator('[data-asset-id="perp-BTC"]')).toBeVisible();
 await picker(page).getByRole('button',{name:'Close instrument selector',exact:true}).click();
 await chooseAsset(page,'perp-BTC');await completed(page);
 expect((await statisticsCalls(page)).requests.at(-1)).toEqual({dataSource:'hyperliquid',asset:'perp-BTC',period:'d1',lookbackWeeks:260,pattern:['up','up','up','up','up']});
 await expect(panel(page)).toContainText('hyperliquid-perp-BTC-d1.csv');
 const after=await llmCalls(page);expect(after.prepare).toBe(before.prepare);expect(after.start).toBe(before.start);
});

test('selected patterns project one next bar with historical counts and chart probabilities',async({page})=>{
 await page.addInitScript(()=>{
  const original=CanvasRenderingContext2D.prototype.fillText;
  (window as any).__statisticsPaintedText=[];
  CanvasRenderingContext2D.prototype.fillText=function(...args:Parameters<CanvasRenderingContext2D['fillText']>){(window as any).__statisticsPaintedText.push(String(args[0]));return original.apply(this,args);};
 });
 await enter(page);await completed(page);
 await expect(panel(page).locator('.prediction-metadata')).toContainText('First closed bar 2018-01-01 00:00:00 UTC');
 await expect(panel(page)).toContainText(/Matching patterns\s*4/);await expect(panel(page)).toContainText(/Eligible patterns\s*12/);
 await expect(panel(page)).toContainText(/small sample/i);
 const rows=scenarios(page).getByRole('row');await expect(rows).toHaveCount(3);
 await expect(rows.nth(1)).toContainText(/75(?:\.0+)?%/);await expect(rows.nth(1)).toContainText('102');
 await expect(rows.nth(2)).toContainText(/25(?:\.0+)?%/);await expect(rows.nth(2)).toContainText('98');
 const outcomes=await scenarios(page).boundingBox(),plot=await chart(page).boundingBox();
 expect(outcomes!.y+outcomes!.height).toBeLessThan(plot!.y);
 await expect(chart(page).locator('canvas').first()).toBeVisible();
 await expect(chart(page)).toHaveAttribute('data-anchor-time',String(Date.UTC(2026,10,16)));
 await expect(chart(page)).toHaveAttribute('data-next-bar-time',String(Date.UTC(2026,10,17)));
 await expect(chart(page)).toHaveAttribute('data-forecast-grid-bars','1');
 await expect.poll(()=>page.evaluate(()=>(window as any).__statisticsPaintedText.some((text:string)=>text.includes('U')&&/75(?:\.0+)?%/.test(text)))).toBe(true);
 await expect.poll(()=>page.evaluate(()=>(window as any).__statisticsPaintedText.some((text:string)=>text==='Next D1 bar'))).toBe(true);
 expect(await page.evaluate(()=>(window as any).__statisticsPaintedText.some((text:string)=>text.includes('1970')))).toBe(false);
 await page.screenshot({path:'test-results/prediction-statistics-pattern.png'});
});

test('time period projects the next native H1 D1 or W1 bar independently from historical lookback',async({page})=>{
 await enter(page);
 expect(await period(page).locator('option').evaluateAll(options=>options.map(option=>(option as HTMLOptionElement).value))).toEqual(['h1','d1','w1']);
 await panel(page).getByLabel('Statistics history period',{exact:true}).selectOption('156');
 for(const [selectedPeriod,anchor,next,first] of [['h1',Date.UTC(2026,10,17,11),Date.UTC(2026,10,17,12),'2019-01-01'],['w1',Date.UTC(2026,10,9),Date.UTC(2026,10,16),'2017-01-02']] as const){
  await period(page).selectOption(selectedPeriod);await completed(page);
  expect((await statisticsCalls(page)).requests.at(-1)).toEqual({dataSource:'dukascopy',asset:'eurusd',period:selectedPeriod,lookbackWeeks:156,pattern:['up','up','up','up','up']});
  await expect(chart(page)).toHaveAttribute('data-period',selectedPeriod);await expect(chart(page)).toHaveAttribute('data-anchor-time',String(anchor));await expect(chart(page)).toHaveAttribute('data-next-bar-time',String(next));await expect(chart(page)).toHaveAttribute('data-forecast-grid-bars','1');
  await expect(panel(page).locator('.prediction-metadata')).toContainText('First closed bar '+first);
  await expect(panel(page)).not.toContainText('D1 companion:');await expect(scenarios(page).getByRole('columnheader',{name:`Next available ${selectedPeriod.toUpperCase()} bar`,exact:true})).toBeVisible();
 }
 await chooseAsset(page,'btcusd');await expect(chart(page)).toHaveCount(0);await expect(period(page)).toHaveValue('d1');
 expect(await period(page).locator('option').evaluateAll(options=>options.map(option=>(option as HTMLOptionElement).value))).toEqual(['d1']);
 await expect(panel(page).getByLabel('Statistics history period',{exact:true})).toHaveValue('156');
 await source(page).selectOption('hyperliquid');await chooseAsset(page,'perp-BTC');await period(page).selectOption('h1');await completed(page);
 expect((await statisticsCalls(page)).requests.at(-1)).toMatchObject({dataSource:'hyperliquid',asset:'perp-BTC',period:'h1',lookbackWeeks:156});
 await expect(panel(page)).toContainText('hyperliquid-perp-BTC-h1.csv');
});

test('no matches remain explicit and Sunday patterns still forecast one next bar',async({page})=>{
 await enter(page);const base=await forecast(page);
 await setForecast(page,{...base,matchedPatterns:0,scenarios:[],warnings:[]});
 await calculate(page).click();await expect(panel(page)).toContainText('No historical patterns match');await expect(scenarios(page)).toHaveCount(0);
 const sunday=Date.UTC(2026,10,22),monday=sunday+86400000;
 const anchor={...base.anchor,time:sunday};
 await setForecast(page,{...base,asOf:monday,anchor,history:[anchor],nextBarTime:monday,scenarios:base.scenarios.map(scenario=>({...scenario,points:scenario.points.map(point=>({...point,time:monday}))}))});
 await completed(page);await expect(chart(page)).toHaveAttribute('data-next-bar-time',String(monday));
 await expect(scenarios(page).getByRole('row')).toHaveCount(3);await expect(panel(page)).not.toContainText('remaining weekdays');await expect(panel(page)).not.toContainText('Friday is already complete');
});

test('Observed sequence edits 3 to 10 ordered bars and includes next-bar doji outcomes in probabilities',async({page})=>{
 await enter(page);const count=panel(page).getByLabel('Pattern bar count',{exact:true});
 expect(await count.locator('option').evaluateAll(options=>options.map(option=>(option as HTMLOptionElement).value))).toEqual(['3','4','5','6','7','8','9','10']);
 await expect(count).toHaveValue('5');await count.selectOption('3');
 await panel(page).getByLabel('Bar 1 direction',{exact:true}).getByRole('radio',{name:'Down',exact:true}).check();await panel(page).getByLabel('Bar 3 direction',{exact:true}).getByRole('radio',{name:'Down',exact:true}).check();
 await completed(page);expect((await statisticsCalls(page)).requests.at(-1).pattern).toEqual(['down','up','down']);
 await count.selectOption('10');await expect(chart(page)).toHaveCount(0);
 for(let index=1;index<=10;index++)await panel(page).getByLabel(`Bar ${index} direction`,{exact:true}).getByRole('radio',{name:'Down',exact:true}).check();
 const base=await page.evaluate(()=>window.go!.main!.App!.GetPredictionStatistics({dataSource:'dukascopy',asset:'eurusd',period:'d1',lookbackWeeks:260,pattern:Array.from({length:10},()=> 'down' as const)}));
 await setForecast(page,{...base,matchedPatterns:5,nextBarDojiCount:1,warnings:['Only 5 matching patterns in this explicit fixture.'],scenarios:[{...base.scenarios[0],count:2,probability:.4},{...base.scenarios[1],count:2,probability:.4},{id:'0',label:'Next bar doji',count:1,probability:.2,points:[{time:base.nextBarTime!,close:100,direction:'doji'}]}]});
 await completed(page);expect((await statisticsCalls(page)).requests.at(-1).pattern).toEqual(Array.from({length:10},()=> 'down'));
 const rows=scenarios(page).getByRole('row');await expect(rows).toHaveCount(4);await expect(rows.nth(1)).toContainText('40.0%');await expect(rows.nth(3)).toContainText('20.0%');await expect(rows.nth(3)).toContainText('1 / 5');
 await expect(panel(page)).toContainText(/Next-bar doji outcomes\s*1/);
 await panel(page).locator('.prediction-statistics').screenshot({path:'test-results/statistics-ten-bar-pattern.png'});
 for(const width of [1600,900]){
  await page.setViewportSize({width,height:1100});
  for(let index=1;index<=10;index++){
   const radios=panel(page).getByRole('radiogroup',{name:`Bar ${index} direction`,exact:true});
   const up=await radios.getByRole('radio',{name:'Up',exact:true}).boundingBox(),down=await radios.getByRole('radio',{name:'Down',exact:true}).boundingBox();
   expect(up).not.toBeNull();expect(down).not.toBeNull();expect(up!.y).toBeCloseTo(down!.y,1);expect(down!.x).toBeGreaterThan(up!.x);
  }
 }
 await page.setViewportSize({width:900,height:1100});
 await expect(panel(page).getByLabel('Bar 10 direction',{exact:true})).toBeVisible();
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 await panel(page).locator('.prediction-statistics').screenshot({path:'test-results/statistics-pattern-narrow.png'});
 await count.selectOption('3');await expect(panel(page).getByLabel('Bar 4 direction',{exact:true})).toHaveCount(0);await expect(chart(page)).toHaveCount(0);
});

test('Current bars resolves the last selected count from each native timeframe and preserves the custom radio draft',async({page})=>{
 await enter(page);
 const first=panel(page).getByRole('radiogroup',{name:'Bar 1 direction',exact:true});
 await first.getByRole('radio',{name:'Up',exact:true}).focus();await page.keyboard.press('ArrowRight');
 await expect(first.getByRole('radio',{name:'Down',exact:true})).toBeChecked();
 const count=panel(page).getByLabel('Pattern bar count',{exact:true});await count.selectOption('3');
 await patternSource(page,'Current bars').check();
 await expect(panel(page).getByRole('radiogroup',{name:'Bar 1 direction',exact:true})).toHaveCount(0);
 await expect(panel(page)).toContainText('Calculate to load the latest 3 closed D1 bars');
 for(const selectedPeriod of ['d1','h1','w1']){
  await period(page).selectOption(selectedPeriod);await completed(page);
  const request=(await statisticsCalls(page)).requests.at(-1);
  expect(request).toMatchObject({period:selectedPeriod,patternMode:'current',patternLength:3});expect(request.pattern).toBeUndefined();
  const resolved=panel(page).getByLabel('Current closed-bar sequence',{exact:true});
  await expect(resolved.locator('.statistics-pattern-bar')).toHaveCount(3);await expect(resolved.locator('strong')).toHaveText(['Down','Up','Down']);
  const response=await page.evaluate(request=>window.go!.main!.App!.GetPredictionStatistics(request),request);
  await expect(resolved.locator('time').last()).toHaveAttribute('datetime',new Date(response.anchor.time).toISOString());
 }
 await panel(page).locator('.statistics-pattern').screenshot({path:'test-results/statistics-current-bars.png'});
 await patternSource(page,'Custom bars').check();await expect(count).toHaveValue('3');
 await expect(first.getByRole('radio',{name:'Down',exact:true})).toBeChecked();
 await expect(panel(page).getByRole('radiogroup',{name:'Bar 2 direction',exact:true}).getByRole('radio',{name:'Up',exact:true})).toBeChecked();
 await expect(chart(page)).toHaveCount(0);await completed(page);expect((await statisticsCalls(page)).requests.at(-1).pattern).toEqual(['down','up','up']);
});

test('changing Current bars mode or count rejects stale calculations and old download completion',async({page})=>{
 await enter(page);await patternSource(page,'Current bars').check();
 await page.evaluate(()=>{const api=window.go!.main!.App!,run=api.GetPredictionStatistics;api.GetPredictionStatistics=async request=>{const result=await run(request);await new Promise<void>(resolve=>(window as any).__releaseCurrentStatistics=resolve);return result;};(window as any).__restoreCurrentStatistics=()=>{api.GetPredictionStatistics=run;};});
 await calculate(page).click();await expect.poll(()=>page.evaluate(()=>typeof(window as any).__releaseCurrentStatistics)).toBe('function');
 await patternSource(page,'Custom bars').check();await page.evaluate(()=>(window as any).__releaseCurrentStatistics());
 await expect(calculate(page)).toBeEnabled();await expect(chart(page)).toHaveCount(0);
 await page.evaluate(()=>(window as any).__restoreCurrentStatistics());await patternSource(page,'Current bars').check();await completed(page);
 await period(page).selectOption('h1');await startDownload(page);
 expect((await downloadInfo(page)).jobs.map(job=>job.timeframe)).toEqual(['h1']);
 await panel(page).getByLabel('Pattern bar count',{exact:true}).selectOption('6');const before=await statisticsCalls(page);
 await page.evaluate(()=>(window as any).__testFinishStatisticsDownload(null));await expect(downloadButton(page)).toBeEnabled();
 expect((await statisticsCalls(page)).calculate).toBe(before.calculate);await expect(chart(page)).toHaveCount(0);
 await completed(page);expect((await statisticsCalls(page)).requests.at(-1)).toMatchObject({patternMode:'current',patternLength:6,period:'h1'});
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

test('editing a pattern rejects its stalled calculation and prevents old download completion from repainting it',async({page})=>{
 await enter(page);await page.evaluate(()=>{const api=window.go!.main!.App!,run=api.GetPredictionStatistics;api.GetPredictionStatistics=async request=>{const result=await run(request);await new Promise<void>(resolve=>(window as any).__releasePatternStatistics=resolve);return result;};(window as any).__restorePatternStatistics=()=>{api.GetPredictionStatistics=run;};});
 await calculate(page).click();await panel(page).getByLabel('Bar 1 direction',{exact:true}).getByRole('radio',{name:'Down',exact:true}).check();
 await page.evaluate(()=>(window as any).__releasePatternStatistics());await expect(calculate(page)).toBeEnabled();await expect(chart(page)).toHaveCount(0);
 await page.evaluate(()=>(window as any).__restorePatternStatistics());await completed(page);expect((await statisticsCalls(page)).requests.at(-1).pattern[0]).toBe('down');
 await startDownload(page);await panel(page).getByLabel('Bar 2 direction',{exact:true}).getByRole('radio',{name:'Down',exact:true}).check();const before=await statisticsCalls(page);await page.evaluate(()=>(window as any).__testFinishStatisticsDownload(null));await expect(downloadButton(page)).toBeEnabled();expect((await statisticsCalls(page)).calculate).toBe(before.calculate);await expect(chart(page)).toHaveCount(0);
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

test('native H1 statistics work with no downloaded D1 companion',async({page})=>{
 await openTestApp(page);await page.evaluate(()=>{const api=window.go!.main!.App!,list=api.GetPredictionStatisticsDatasets;api.GetPredictionStatisticsDatasets=async()=> (await list()).filter(item=>item.period!=='d1');});
 await page.locator('#predictions-tab').click();await section(page,'Statistics').click();await chooseAsset(page,'eurusd');await expect(period(page)).toHaveValue('h1');await completed(page);
 await expect(panel(page)).not.toContainText('D1 companion:');expect((await statisticsCalls(page)).requests.at(-1).period).toBe('h1');
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
 expect((await downloadCalls(page)).requests).toEqual([{dataSource:'dukascopy',asset:'eurusd',period:'d1',lookbackWeeks:260,pattern:['up','up','up','up','up']}]);
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
 const refreshed:PredictionStatisticsForecast={...base,asOf:Date.UTC(2026,10,18),anchor,history:[...base.history,anchor],prefix:[...base.prefix,{time:anchor.time,day:'Tue',direction:'up'}],nextBarTime:anchor.time+86400000,scenarios:base.scenarios.map(scenario=>({...scenario,points:scenario.points.map(point=>({...point,time:anchor.time+86400000}))}))};
 await page.evaluate(value=>(window as any).__testFinishStatisticsDownload(value),refreshed);
 await expect(chart(page)).toHaveAttribute('data-anchor-time',String(anchor.time));
 await expect(downloadButton(page)).toBeEnabled();
 expect((await statisticsCalls(page)).calculate).toBe(before.calculate+1);
 expect((await statisticsCalls(page)).datasets).toBeGreaterThan(before.datasets);
 expect((await downloadCalls(page)).start).toBe(1);
 const after=await llmCalls(page);expect(after.prepare).toBe(models.prepare);expect(after.start).toBe(models.start);
});

test('non-daily pattern downloads request only their selected native timeframe',async({page})=>{
 await enter(page);
 for(const selectedPeriod of ['h1','w1']){
  await period(page).selectOption(selectedPeriod);await startDownload(page);const info=await downloadInfo(page);
  expect(info.jobs.map(job=>job.timeframe)).toEqual([selectedPeriod]);expect(info.jobs.every(job=>job.symbol==='eurusd'&&job.dataSource==='dukascopy')).toBe(true);
  await expect(downloadRegion(page).getByRole('progressbar')).toHaveCount(1);
  await page.evaluate(()=>(window as any).__testFinishStatisticsDownload(null));
  await expect(chart(page)).toHaveAttribute('data-period',selectedPeriod);await expect(downloadButton(page)).toBeEnabled();
 }
 expect((await downloadCalls(page)).requests.map((request:any)=>request.period)).toEqual(['h1','w1']);
});

test('a failed native download stays visible without automatic retry or recalculation',async({page})=>{
 await enter(page);await page.clock.install();await period(page).selectOption('h1');await startDownload(page);const before=await statisticsCalls(page);
 await page.evaluate(()=>{const info=(window as any).__testStatisticsDownloadInfo();(window as any).__testSetStatisticsDownloadInfo({active:false,jobs:info.jobs.map((job:any)=>({...job,status:'failed',progress:20,message:'Hourly provider rejected the missing range'}))});});
 await expect(downloadRegion(page)).toContainText('Hourly provider rejected the missing range');await expect(downloadButton(page)).toBeEnabled();
 const terminal=await downloadCalls(page);await page.clock.fastForward(2500);expect((await downloadCalls(page)).start).toBe(1);expect((await downloadCalls(page)).status).toBe(terminal.status);expect((await statisticsCalls(page)).calculate).toBe(before.calculate);await expect(chart(page)).toHaveCount(0);
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

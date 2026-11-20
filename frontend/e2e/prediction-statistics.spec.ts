import {test,expect,type Page} from '@playwright/test';
import {openTestApp} from './helpers/testApp';
import type {PredictionStatisticsForecast} from '../src/workspace/predictionStatistics';

const panel=(page:Page)=>page.getByRole('tabpanel',{name:'Predictions',exact:true});
const section=(page:Page,name:'LLM'|'Statistics')=>panel(page).getByRole('tab',{name,exact:true});
const statisticsCalls=(page:Page)=>page.evaluate(()=>(window as any).__testStatisticsCalls());
const llmCalls=(page:Page)=>page.evaluate(()=>(window as any).__testPredictionCalls());
const chart=(page:Page)=>panel(page).getByTestId('statistics-chart');
const scenarios=(page:Page)=>panel(page).getByRole('table',{name:'Remaining-week scenarios',exact:true});
const source=(page:Page)=>panel(page).getByLabel('Statistics data source',{exact:true});
const asset=(page:Page)=>panel(page).getByLabel('Statistics asset',{exact:true});
const period=(page:Page)=>panel(page).getByLabel('Statistics time period',{exact:true});
const calculate=(page:Page)=>panel(page).getByRole('button',{name:/^(Calculate scenarios|Calculating…)$/});

async function enter(page:Page){
 await openTestApp(page);
 await page.locator('#predictions-tab').click();
 await expect(section(page,'LLM')).toHaveAttribute('aria-selected','true');
 await section(page,'Statistics').click();
 await expect(source(page)).toBeEnabled();
 await expect(asset(page).locator('option[value="eurusd"]')).toHaveCount(1);
 await asset(page).selectOption('eurusd');
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
 await panel(page).getByLabel('Filter statistics assets',{exact:true}).fill('Euro');
 await expect(asset(page).locator('option[value="eurusd"]')).toHaveCount(1);
 await expect(asset(page).locator('option[value="btcusd"]')).toHaveCount(0);
 await panel(page).getByLabel('Statistics history period',{exact:true}).selectOption('260');
 await completed(page);
 expect((await statisticsCalls(page)).requests.at(-1)).toEqual({dataSource:'dukascopy',asset:'eurusd',period:'d1',lookbackWeeks:260});
 await panel(page).getByLabel('Filter statistics assets',{exact:true}).fill('');
 await source(page).selectOption('hyperliquid');
 await expect(chart(page)).toHaveCount(0);
 await expect(asset(page).locator('option[value="eurusd"]')).toHaveCount(0);
 await expect(asset(page).locator('option[value="perp-BTC"]')).toHaveCount(1);
 await asset(page).selectOption('perp-BTC');await completed(page);
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
 await expect(panel(page)).toContainText(/Matching weeks\s*4/);
 await expect(panel(page)).toContainText(/Eligible weeks\s*12/);
 await expect(panel(page)).toContainText(/Possible sequences\s*16/);
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
 await expect(chart(page)).toHaveAttribute('data-anchor-time',String(Date.UTC(2026,10,9)));
 await expect(chart(page)).toHaveAttribute('data-friday-time',String(Date.UTC(2026,10,20)));
 await chart(page).screenshot({path:'test-results/statistics-weekly.png'});
 await expect(panel(page).getByRole('heading',{level:2}).filter({hasText:'Euro vs US Dollar'})).toContainText('W1');
 await expect(panel(page)).toContainText('eurusd-w1.csv');
 await expect(scenarios(page).getByRole('columnheader',{name:/Monday close.*observed/i})).toBeVisible();
 await asset(page).selectOption('btcusd');await expect(chart(page)).toHaveCount(0);await expect(period(page)).toHaveValue('d1');
 expect(await period(page).locator('option').evaluateAll(options=>options.map(option=>(option as HTMLOptionElement).value))).toEqual(['d1']);
 await expect(panel(page).getByLabel('Statistics history period',{exact:true})).toHaveValue('156');
 await source(page).selectOption('hyperliquid');await asset(page).selectOption('perp-BTC');
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
 await asset(page).selectOption('eurusd');await completed(page);
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
 await source(page).selectOption('hyperliquid');await asset(page).selectOption('perp-BTC');
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
 await section(page,'Statistics').click();await asset(page).selectOption('eurusd');await completed(page);
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

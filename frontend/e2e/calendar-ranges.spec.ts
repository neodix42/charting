import {test,expect,type Page} from '@playwright/test';
import {openTestApp,reloadTestApp} from './helpers/testApp';
import type {ScreenerDataFile} from '../src/types';
import {calendarRangeNames} from '../../screener-runtime/calendar-ranges.mjs';

const ids=Object.keys(calendarRangeNames),names=Object.values(calendarRangeNames);
const fields=['timestamp','open','high','low','close','volume'];
const file=(id:string,timeframe:string,dataSource:'dukascopy'|'hyperliquid'='dukascopy'):ScreenerDataFile=>({id:`${id}-${timeframe}`,name:`${id}-${timeframe}-2024-01-01-2024-02-01.csv`,symbol:id,timeframe,dataSource,fields:[...fields],bytes:1000});
const control=(page:Page,name:string)=>page.getByLabel(name,{exact:true});
async function screener(page:Page,tab:string){await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:tab,exact:true}).click();}

test('Enrichment offers all twelve parameterless UTC ranges only below D1 and All periods targets only intraday candles',async({page})=>{
 await openTestApp(page);await page.evaluate(files=>(window as any).__testSetEnrichmentFiles(files),[file('eurusd','h1'),file('btcusd','m5'),file('eurusd','d1'),file('eurusd','w1'),file('eurusd','mn1'),file('eurusd','tick'),{...file('custom',''),name:'custom.csv'},file('btc','d1','hyperliquid')]);
 await screener(page,'Enrichment');const indicator=control(page,'Enrichment indicator'),period=control(page,'Enrichment period');
 await expect(indicator.locator('optgroup[label="Day / Week ranges"] option')).toHaveText(['All Day / Week ranges indicators',...names]);
 for(const id of ids){
  await indicator.selectOption(id);await expect(control(page,'Period')).toHaveCount(0);await expect(control(page,'Enrichment price source')).toBeDisabled();
  await expect(page.getByRole('button',{name:'Enrich 2 files',exact:true})).toBeEnabled();
  await page.getByRole('button',{name:'Enrich 2 files',exact:true}).click();
  expect(await page.evaluate(()=>(window as any).__testEnrichmentRequest())).toEqual({indicator:id,parameters:{},source:'auto'});
  expect(await page.evaluate(()=>(window as any).__testEnrichment().total)).toBe(2);
  await page.getByRole('button',{name:'Cancel enrichment',exact:true}).click();
 }
 for(const timeframe of ['d1','w1','mn1','tick']){await period.selectOption(timeframe);await expect(indicator.locator('optgroup[label="Day / Week ranges"]')).toHaveCount(0);await expect(indicator).toHaveValue('sma');}
 await period.selectOption('h1');await indicator.selectOption('today_low');await expect(page.getByRole('button',{name:'Enrich 1 file',exact:true})).toBeEnabled();
 await page.getByRole('button',{name:'Enrich 1 file',exact:true}).click();expect(await page.evaluate(()=>(window as any).__testEnrichmentRequest())).toMatchObject({indicator:'today_low',period:'h1'});await page.getByRole('button',{name:'Cancel enrichment',exact:true}).click();
 await control(page,'Enrichment data source').selectOption('hyperliquid');await indicator.selectOption('week_high');await expect(page.getByRole('button',{name:'Enrich 0 files',exact:true})).toBeDisabled();
});

test('Analysis compares Today and Previous day fields on hourly files, reports missing enrichment and saves the rule',async({page})=>{
 await openTestApp(page);await page.evaluate(files=>{(window as any).__testSetEnrichmentFiles(files);(window as any).__testSetAnalysisView({...((window as any).__testAnalysisView()),period:'h1',profitabilityFilter:0});},[file('eurusd','h1'),file('eurusd','d1')]);
 await screener(page,'Analysis');const left=control(page,'Condition 1 left field');await expect(left.locator('optgroup[label="Day / Week ranges"] option')).toHaveText(names);
 await left.selectOption('today_low');await control(page,'Condition 1 operator').selectOption('below');await control(page,'Condition 1 right type').selectOption('field');await control(page,'Condition 1 right field').selectOption('previous_day_low');
 await expect(control(page,'Condition 1 left lookback bars')).toHaveCount(0);
 await expect(page.getByRole('group',{name:'Condition 1 left',exact:true}).getByRole('status')).toContainText("add Today's low");
 await page.evaluate(ids=>(window as any).__testSetEnrichmentFiles((window as any).__testEnrichmentFiles().map((file:ScreenerDataFile)=>({...file,fields:file.timeframe==='h1'?[...file.fields,...ids]:file.fields}))),ids);
 await page.getByRole('button',{name:'Refresh fields',exact:true}).click();await expect(page.locator('.analysis-enrichment-message')).toHaveCount(0);
 await page.getByRole('button',{name:'Save as new',exact:true}).click();const dialog=page.getByRole('dialog',{name:'Save condition',exact:true});await dialog.getByLabel('Condition name',{exact:true}).fill('Today below yesterday');await dialog.getByRole('button',{name:'Save condition',exact:true}).click();await expect(dialog).toHaveCount(0);
 await reloadTestApp(page);await screener(page,'Analysis');await expect(left).toHaveValue('today_low');await expect(control(page,'Condition 1 right field')).toHaveValue('previous_day_low');await expect(control(page,'Condition 1 left timeframe')).toHaveValue('same');
 await control(page,'Condition 1 left timeframe').selectOption('d1');await expect(page.getByRole('button',{name:'Apply conditions',exact:true})).toBeDisabled();await expect(page.locator('.analysis-apply-row')).toContainText('intraday candle timeframe below D1');
 await control(page,'Condition 1 left timeframe').selectOption('same');await page.getByRole('button',{name:'Apply conditions',exact:true}).click();
 expect(await page.evaluate(()=>(window as any).__testAnalysisRequest())).toMatchObject({period:'h1',conditions:[{operator:'below',left:{field:'today_low',timeframe:'same'},right:{field:'previous_day_low',timeframe:'same'}}]});
});

test('all twelve chart overlays configure without a lookback, persist, suspend on D1/W1 and keep the existing feed lease',async({page})=>{
 const errors:string[]=[];page.on('pageerror',error=>errors.push(error.message));await openTestApp(page);
 const chart=page.getByTestId('chart-cell');await expect(chart.getByLabel('OHLCV')).toContainText('C');const attachments=await page.evaluate(()=>(window as any).__testAttachCount());
 for(const [id,name]of Object.entries(calendarRangeNames)){
  await chart.getByLabel('Indicators',{exact:true}).selectOption(id);const dialog=page.getByRole('dialog',{name:`${name} parameters`,exact:true});
  await expect(dialog.getByLabel('Period',{exact:true})).toHaveCount(0);await expect(dialog).toContainText('UTC');await dialog.getByRole('button',{name:'OK',exact:true}).click();
 }
 await expect(chart.locator('.indicator-chip')).toHaveCount(ids.length);await expect(page.getByRole('button',{name:'All changes saved'})).toBeVisible();
 const saved=await page.evaluate(()=>(window as any).__testWorkspace().tabs[0].charts[0].indicators);expect(saved.map((item:any)=>item.type)).toEqual(ids);expect(saved.every((item:any)=>Object.keys(item.parameters).length===0)).toBe(true);
 expect(await page.evaluate(()=>(window as any).__testAttachCount())).toBe(attachments);
 await reloadTestApp(page);await expect(chart.locator('.indicator-chip')).toHaveCount(ids.length);
 await chart.getByRole('button',{name:"Edit Today's low",exact:true}).click();let dialog=page.getByRole('dialog',{name:"Today's low parameters",exact:true});await dialog.getByLabel('Indicator color',{exact:true}).fill('#123456');await dialog.getByRole('button',{name:'OK',exact:true}).click();
 for(const timeframe of ['1d','1w']){
  await chart.getByRole('button',{name:`Timeframe ${timeframe}`,exact:true}).click();
  for(const id of ids)await expect(chart.getByLabel('Indicators',{exact:true}).locator(`option[value="${id}"]`)).toBeDisabled();
  await expect(chart.locator('.indicator-chip .indicator-warmup')).toHaveText(Array(ids.length).fill(' · intraday only'));
 }
 await chart.getByRole('button',{name:'Timeframe 1h',exact:true}).click();for(const id of ids)await expect(chart.getByLabel('Indicators',{exact:true}).locator(`option[value="${id}"]`)).toBeEnabled();
 await expect(chart.locator('.indicator-chip')).toHaveCount(ids.length);await chart.getByRole('button',{name:"Remove Today's low",exact:true}).click();await expect(chart.locator('.indicator-chip')).toHaveCount(ids.length-1);
 expect(errors).toEqual([]);
});

test('previous-week overlays can load a bounded full two weeks of minute bars without another market subscription',async({page})=>{
 await openTestApp(page);const chart=page.getByTestId('chart-cell');await chart.getByRole('button',{name:'Timeframe 1m',exact:true}).click();await expect(chart.getByLabel('OHLCV')).toContainText('C');
 const attachments=await page.evaluate(()=>(window as any).__testAttachCount());
 await page.evaluate(()=>{(window as any).__calendarHistory=[];window.go!.main!.App!.GetHistory=async(requestId,feedKey,from,to,maxBars)=>{
  (window as any).__calendarHistory.push({from,to,maxBars});const bars=[];
  for(let time=from;time<to;time+=60)bars.push({time,open:10,high:12,low:8,close:11,volume:1,closed:true,quality:'authoritative',sourceRevision:1});
  return{requestId,feedKey,bars,moreHistory:true};
 };});
 await chart.getByLabel('Indicators',{exact:true}).selectOption('previous_week_low');await page.getByRole('dialog',{name:'Previous week low parameters',exact:true}).getByRole('button',{name:'OK',exact:true}).click();
 await expect(chart.locator('.indicator-warmup')).toHaveCount(0,{timeout:30000});
 const requests=await page.evaluate(()=>(window as any).__calendarHistory);expect(requests.length).toBeGreaterThan(10);expect(requests.length).toBeLessThanOrEqual(21);expect(requests.every((request:any)=>request.maxBars===1000&&(request.to-request.from)<=60000)).toBe(true);
 expect(await page.evaluate(()=>(window as any).__testAttachCount())).toBe(attachments);
});

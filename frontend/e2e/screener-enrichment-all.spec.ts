import {test,expect,type Page} from '@playwright/test';
import {openTestApp} from './helpers/testApp';
import type {ScreenerDataFile,ScreenerDataSource,ScreenerEnrichmentInfo} from '../src/types';

const panel=(page:Page)=>page.locator('#screener-enrichment');
const file=(index:number,dataSource:ScreenerDataSource,timeframe:string):ScreenerDataFile=>({
 id:`${dataSource}-${index}`,name:`${dataSource}-asset${index}-${timeframe}.csv`,dataSource,timeframe,
 fields:['timestamp','open','high','low','close','volume'],bytes:2048,
});
async function open(page:Page,files:ScreenerDataFile[]){
 await openTestApp(page);
 await page.evaluate(value=>(window as any).__testSetEnrichmentFiles(value),files);
 await page.getByRole('tab',{name:'Screener',exact:true}).click();
 await page.getByRole('tab',{name:'Enrichment',exact:true}).click();
 await expect(page.getByLabel('Enrichment data source',{exact:true})).toBeEnabled();
}

test('All enrichment keeps a combined five-file page and submits one configured sequential batch',async({page})=>{
 const files=Array.from({length:12},(_,index)=>file(index,index%2?'hyperliquid':'dukascopy',index<7?'h1':index===11?'w1':'d1'));
 await open(page,files);
 await page.getByLabel('Enrichment indicator',{exact:true}).selectOption('macd');
 await page.getByLabel('Fast period',{exact:true}).fill('5');
 await page.getByLabel('Slow period',{exact:true}).fill('10');
 await page.getByLabel('Signal period',{exact:true}).fill('4');
 await page.getByLabel('Enrichment price source',{exact:true}).selectOption('open');
 await page.getByLabel('Enrichment data source',{exact:true}).selectOption('all');
 await expect(page.getByLabel('Enrichment file scope',{exact:true})).toHaveText('12 of 12 files selected · All periods');
 await expect(panel(page).locator('tbody tr')).toHaveCount(5);
 await expect(panel(page)).toContainText('1–5 of 12 files');
 await expect(panel(page).locator('tbody tr td:first-child small')).toHaveText(['Dukascopy · h1 · 2.0 KiB','Hyperliquid · h1 · 2.0 KiB','Dukascopy · h1 · 2.0 KiB','Hyperliquid · h1 · 2.0 KiB','Dukascopy · h1 · 2.0 KiB']);
 await panel(page).screenshot({path:test.info().outputPath('all-enrichment-files.png')});
 await expect(page.getByRole('button',{name:'Refresh Dukascopy data and indicators',exact:true})).toHaveCount(0);
 await page.getByRole('button',{name:'Next files',exact:true}).click();
 await expect(panel(page)).toContainText('6–10 of 12 files');
 await page.getByLabel('Enrichment period',{exact:true}).selectOption('h1');
 await expect(page.getByLabel('Enrichment file scope',{exact:true})).toHaveText('7 of 12 files selected · h1');
 await expect(panel(page)).toContainText('1–5 of 7 files');
 await expect(page.getByLabel('Enrichment indicator',{exact:true})).toHaveValue('macd');
 await expect(page.getByLabel('Fast period',{exact:true})).toHaveValue('5');
 await expect(page.getByLabel('Enrichment price source',{exact:true})).toHaveValue('open');
 await page.getByRole('button',{name:'Next files',exact:true}).click();
 await expect(panel(page)).toContainText('6–7 of 7 files');
 await expect(page.getByRole('button',{name:'Enrich 7 files',exact:true})).toBeEnabled();
 await page.getByRole('button',{name:'Enrich 7 files',exact:true}).click();
 expect(await page.evaluate(()=>(window as any).__testEnrichmentRequests())).toEqual([{dataSource:'all',indicator:'macd',parameters:{fast:5,slow:10,signal:4},source:'open',period:'h1'}]);
 const info=await page.evaluate(()=>(window as any).__testEnrichment()) as ScreenerEnrichmentInfo;
 expect(info.total).toBe(7);
 expect(info.sourceOrder).toEqual(['dukascopy','hyperliquid']);
 expect(info.jobs.map(job=>job.dataSource)).toEqual(['dukascopy','dukascopy','dukascopy','dukascopy','hyperliquid','hyperliquid','hyperliquid']);
 await expect(page.getByLabel('Enrichment source progress',{exact:true})).toHaveText('Source order: Dukascopy → Hyperliquid · Current source: Dukascopy');
 await expect(page.getByLabel('Enrichment data source',{exact:true})).toBeDisabled();
 const current=info.jobs.at(-1)!;
 await page.evaluate(({info,current})=>(window as any).__testSetEnrichment({...info,activeDataSource:'hyperliquid',completed:4,currentFile:current.name,currentProgress:43,jobs:info.jobs.map(job=>({...job,status:job.dataSource==='dukascopy'?'completed':job.id===current.id?'running':'queued',progress:job.dataSource==='dukascopy'?100:job.id===current.id?43:0,message:job.dataSource==='dukascopy'?'Finished Dukascopy':'Hyperliquid batch'}))}),{info,current});
 await expect(page.getByLabel('Enrichment source progress',{exact:true})).toHaveText('Source order: Dukascopy → Hyperliquid · Current source: Hyperliquid');
 await expect(panel(page).locator(`tr[data-file="${current.name}"]`).getByRole('progressbar')).toHaveAttribute('value','43');
 await panel(page).screenshot({path:test.info().outputPath('all-enrichment-progress.png')});
 await page.getByRole('button',{name:'Cancel enrichment',exact:true}).click();
 await expect(panel(page)).toContainText('4 / 7 complete · 0 failed · 3 cancelled');
 await expect(page.getByLabel('Enrichment source progress',{exact:true})).toHaveText('Source order: Dukascopy → Hyperliquid');
 await expect(panel(page).locator(`tr[data-file="${current.name}"]`)).toContainText('cancelled');
 await expect(panel(page).locator(`tr[data-file="${files[6].name}"]`)).toContainText('completed · 100%');
 expect(await page.evaluate(()=>(window as any).__testEnrichmentRequests().length)).toBe(1);
});

test('All enrichment retains Dukascopy-only periods and applies intraday group eligibility across sources',async({page})=>{
 const files=[file(0,'dukascopy','m1'),file(1,'hyperliquid','d1'),file(2,'dukascopy','d1'),file(3,'dukascopy','m1'),file(4,'hyperliquid','w1'),file(5,'hyperliquid','h1')];
 await open(page,files);
 await page.getByLabel('Enrichment data source',{exact:true}).selectOption('all');
 const period=page.getByLabel('Enrichment period',{exact:true}),scope=page.getByLabel('Enrichment file scope',{exact:true});
 await expect(period.locator('option')).toHaveText(['All periods','tick','s1','m1','m5','m15','m30','h1','h4','d1','w1','mn1']);
 await period.selectOption('m1');
 await page.getByLabel('Enrichment indicator',{exact:true}).selectOption('group:Day / Week ranges');
 await expect(scope).toHaveText('2 of 6 files selected · m1');
 await page.getByRole('button',{name:'Enrich 2 files',exact:true}).click();
 expect(await page.evaluate(()=>(window as any).__testEnrichmentRequest())).toEqual({dataSource:'all',indicator:'',group:'Day / Week ranges',parameters:{},source:'auto',period:'m1'});
 expect(await page.evaluate(()=>(window as any).__testEnrichment().sourceOrder)).toEqual(['dukascopy']);
 await expect(page.getByLabel('Enrichment source progress',{exact:true})).toHaveText('Source order: Dukascopy · Current source: Dukascopy');
 await page.getByRole('button',{name:'Cancel enrichment',exact:true}).click();
 await period.selectOption('all');
 await expect(scope).toHaveText('3 of 6 files selected · All periods');
 await page.getByRole('button',{name:'Enrich 3 files',exact:true}).click();
 const info=await page.evaluate(()=>(window as any).__testEnrichment()) as ScreenerEnrichmentInfo;
 expect(info.jobs.map(job=>job.fileId)).toEqual([files[0].id,files[3].id,files[5].id]);
 expect(info.sourceOrder).toEqual(['dukascopy','hyperliquid']);
 await page.getByRole('button',{name:'Cancel enrichment',exact:true}).click();
 await period.selectOption('tick');
 await expect(scope).toHaveText('0 of 6 files selected · tick');
 await expect(page.getByRole('button',{name:'Enrich 0 files',exact:true})).toBeDisabled();
 await expect(panel(page)).toContainText('No downloaded tick CSV files.');
});

test('a delayed All file response cannot replace the more recently selected source',async({page})=>{
 const files=[file(0,'dukascopy','m1'),file(1,'hyperliquid','h1'),file(2,'dukascopy','d1'),file(3,'hyperliquid','w1')];
 await open(page,files);
 await expect(page.getByLabel('Enrichment file scope',{exact:true})).toHaveText('2 of 2 files selected · All periods');
 await page.evaluate(()=>{
  const api=window.go!.main!.App!,original=api.GetScreenerFilesForSource;
  api.GetScreenerFilesForSource=(offset,limit,period,source)=>source==='all'?new Promise(resolve=>{(window as any).__releaseAllEnrichmentFiles=async()=>resolve(await original(offset,limit,period,source));}):original(offset,limit,period,source);
 });
 await page.getByLabel('Enrichment data source',{exact:true}).selectOption('all');
 await expect.poll(()=>page.evaluate(()=>typeof (window as any).__releaseAllEnrichmentFiles)).toBe('function');
 await expect(page.getByRole('button',{name:'Enrich 0 files',exact:true})).toBeDisabled();
 await page.getByLabel('Enrichment data source',{exact:true}).selectOption('hyperliquid');
 await expect(page.getByLabel('Enrichment file scope',{exact:true})).toHaveText('2 of 2 files selected · All periods');
 await page.evaluate(()=>(window as any).__releaseAllEnrichmentFiles());
 await expect(page.getByLabel('Enrichment data source',{exact:true})).toHaveValue('hyperliquid');
 await expect(panel(page).locator('tbody tr td:first-child strong')).toHaveText([files[1].name,files[3].name]);
 await expect(page.getByLabel('Enrichment file scope',{exact:true})).toHaveText('2 of 2 files selected · All periods');
 await expect(page.getByRole('button',{name:'Refresh Dukascopy data and indicators',exact:true})).toHaveCount(0);
});

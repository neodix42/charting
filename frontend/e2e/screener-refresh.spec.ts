import {test,expect,type Page} from '@playwright/test';
import {openTestApp} from './helpers/testApp';
import type {ScreenerDataFile,ScreenerRefreshInfo} from '../src/types';

const fields=['timestamp','open','high','low','close','volume','sma_200','macd_histogram_12_26_9'];
const file=(index:number):ScreenerDataFile=>({id:`refresh-${index}`,name:`asset${index}-d1-2024-01-01-2024-02-01.csv`,symbol:`asset${index}`,timeframe:'d1',from:'2024-01-01',to:'2024-02-01',fields:[...fields],bytes:2000});
async function open(page:Page,count=2){
 await openTestApp(page);await page.evaluate(files=>(window as any).__testSetEnrichmentFiles(files),Array.from({length:count},(_,i)=>file(i)));
 await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Enrichment',exact:true}).click();
 await expect(page.getByRole('button',{name:'Refresh data and indicators',exact:true})).toBeEnabled();
}
const panel=(page:Page)=>page.locator('#screener-enrichment');

test('Refresh updates all existing files and applied indicators, independently of the new indicator controls',async({page})=>{
 await open(page,57);
 await page.getByLabel('Period',{exact:true}).fill('3');
 await page.getByRole('button',{name:'Next files',exact:true}).click();await expect(panel(page).locator('tbody tr')).toHaveCount(20);
 await page.getByRole('button',{name:'Next files',exact:true}).click();await expect(panel(page).locator('tbody tr')).toHaveCount(17);
 await page.getByRole('button',{name:'Refresh data and indicators',exact:true}).click();
 const info=await page.evaluate(()=>(window as any).__testRefresh()) as ScreenerRefreshInfo;
 expect(info.jobs).toHaveLength(57);expect(info.jobs[0].columns).toEqual(fields.slice(6));
 expect(await page.evaluate(()=>(window as any).__testEnrichmentRequest())).toBeNull();
 expect(await page.evaluate(()=>(window as any).__testRefreshStarts())).toBe(1);
 await expect(panel(page).locator('.enrichment-refresh-help')).toContainText('This refresh checks through');
 await page.evaluate(info=>(window as any).__testSetRefresh({...info,currentFile:info.jobs[56].name,currentProgress:36,jobs:info.jobs.map((job,i)=>({...job,status:i===56?'running':'queued',progress:i===56?36:0,message:'Downloading new rows and recalculating saved indicators'}))}),info);
 await expect(panel(page).locator('tbody tr').last().getByRole('progressbar')).toHaveAttribute('value','36');
 await expect(page.getByRole('button',{name:'Enrich 57 files',exact:true})).toBeDisabled();
 await expect(page.getByRole('button',{name:'Refresh data and indicators',exact:true})).toBeDisabled();
 await page.getByRole('tab',{name:'Downloader',exact:true}).click();await page.getByRole('tab',{name:'Enrichment',exact:true}).click();
 await expect(panel(page).locator('tbody tr').last()).toContainText('Downloading new rows');
 await page.getByRole('button',{name:'Cancel refresh',exact:true}).click();
 await expect(panel(page)).toContainText('57 cancelled');
 expect((await page.evaluate(()=>(window as any).__testEnrichmentFiles()))[0].fields).toEqual(fields);
});

test('completed refresh shows new row counts, recalculated columns and the available UTC period',async({page})=>{
 await open(page);await page.getByRole('button',{name:'Refresh data and indicators',exact:true}).click();
 await page.evaluate(()=>{
  const files=(window as any).__testEnrichmentFiles() as ScreenerDataFile[];
  (window as any).__testSetEnrichmentFiles(files.map(file=>({...file,bytes:3000,firstTimestamp:1704067200000,lastTimestamp:1706832000000})));
  const info=(window as any).__testRefresh() as ScreenerRefreshInfo;
  (window as any).__testSetRefresh({...info,active:false,completed:2,jobs:info.jobs.map(job=>({...job,status:'completed',progress:100,rows:35,addedRows:10,updatedRows:1,message:'Added 10 rows and recalculated applied indicators',backupPath:'/explicit-test/original.csv'}))});
 });
 const first=panel(page).locator('tbody tr').first();
 await expect(first).toContainText('10 rows added · 1 rows updated');
 await expect(first).toContainText('2024-02-02 00:00:00 UTC');
 await expect(first).toContainText('Available bar open times');
 await expect(first.locator('td').nth(2)).toContainText('sma_200, macd_histogram_12_26_9');
 await expect(panel(page).getByLabel('Data refresh progress',{exact:true})).toContainText('2 / 2 complete · 0 failed');
 await expect(page.getByRole('button',{name:'Refresh data and indicators',exact:true})).toBeEnabled();
});

for(const action of ['refresh','enrichment'] as const){
 test(`${action} follows a range rename while running and reloads a failed job's consolidated file`,async({page})=>{
  await open(page,1);
  await page.getByRole('button',{name:action==='refresh'?'Refresh data and indicators':'Enrich 1 file',exact:true}).click();
  const renamed={...file(0),id:'renamed-history',name:'asset0-d1-2023-01-01-2024-02-01.csv',from:'2023-01-01'};
  await page.evaluate(({action,renamed})=>{
   const win=window as any,api=win.go.main.App;
   const getter=action==='refresh'?'GetScreenerRefresh':'GetScreenerEnrichment';
   const read=action==='refresh'?'__testRefresh':'__testEnrichment';
   const write=action==='refresh'?'__testSetRefresh':'__testSetEnrichment';
   const original=api[getter];
   // Production polling accepts the old file ID as an alias after publication.
   api[getter]=(ids:string[])=>original(ids.map(id=>id==='refresh-0'?renamed.id:id));
   win.__testSetEnrichmentFiles([renamed]);
   const info=win[read]();
   win[write]({...info,jobs:info.jobs.map((job:object)=>({...job,fileId:renamed.id,name:renamed.name,status:'running',progress:35,message:'Merged saved histories; processing the consolidated file'}))});
  },{action,renamed});
  const row=panel(page).locator('tbody tr');
  await expect(row).toHaveCount(1);
  await expect(row).toContainText(renamed.name);
  await expect(row.getByRole('progressbar')).toHaveAttribute('value','35');
  await page.evaluate(action=>{
   const win=window as any,read=action==='refresh'?'__testRefresh':'__testEnrichment',write=action==='refresh'?'__testSetRefresh':'__testSetEnrichment';
   win.__testSetEnrichmentFiles(win.__testEnrichmentFiles().map((file:ScreenerDataFile)=>({...file,bytes:9000})));
   const info=win[read]();
   win[write]({...info,active:false,failed:1,jobs:info.jobs.map((job:object)=>({...job,status:'failed',message:'Processing stopped; consolidated history retained'}))});
  },action);
  await expect(row).toContainText('failed');
  await expect(row).toContainText('8.8 KiB');
  await expect(row).toContainText('consolidated history retained');
  await expect(page.getByRole('button',{name:'Refresh data and indicators',exact:true})).toBeEnabled();
 });
}

test('a stale refresh poll cannot replace a newer started batch',async({page})=>{
 await open(page,1);
 await page.evaluate(()=>{
  const api=window.go!.main!.App!,original=api.GetScreenerRefresh,old=(window as any).__testRefresh();
  (window as any).__releaseRefreshPoll=null;
  api.GetScreenerRefresh=()=>new Promise(resolve=>{(window as any).__releaseRefreshPoll=()=>{api.GetScreenerRefresh=original;resolve(old);};});
 });
 await expect.poll(()=>page.evaluate(()=>!!(window as any).__releaseRefreshPoll)).toBe(true);
 await page.getByRole('button',{name:'Refresh data and indicators',exact:true}).click();
 await page.evaluate(()=>(window as any).__releaseRefreshPoll());
 await expect(page.getByRole('button',{name:'Cancel refresh',exact:true})).toBeVisible();
 await expect(page.getByRole('button',{name:'Refresh data and indicators',exact:true})).toBeDisabled();
 await page.getByRole('button',{name:'Cancel refresh',exact:true}).click();
});

test('refresh runtime and write errors are visible and leave listed data intact',async({page})=>{
 await open(page,1);
 await page.evaluate(()=>(window as any).__testSetRefresh({runtimeAvailable:false,runtimeError:'Explicit missing refresh worker'}));
 await expect(panel(page).getByRole('alert')).toContainText('Explicit missing refresh worker');
 await expect(page.getByRole('button',{name:'Refresh data and indicators',exact:true})).toBeDisabled();
 await page.evaluate(()=>{(window as any).__testSetRefresh({runtimeAvailable:true,runtimeError:''});(window as any).__testRefreshError('Explicit original backup failure');});
 await expect(page.getByRole('button',{name:'Refresh data and indicators',exact:true})).toBeEnabled();
 await page.getByRole('button',{name:'Refresh data and indicators',exact:true}).click();
 await expect(panel(page).getByRole('alert')).toContainText('Explicit original backup failure');
 expect(await page.evaluate(()=>(window as any).__testRefreshStarts())).toBe(0);
 expect((await page.evaluate(()=>(window as any).__testEnrichmentFiles()))[0].fields).toEqual(fields);
});

test('reloading the file list performs no download or indicator calculation',async({page})=>{
 await open(page,1);const before=await page.evaluate(()=>(window as any).__testEnrichmentReads());
 await page.getByRole('button',{name:'Reload file list',exact:true}).click();
 await expect.poll(()=>page.evaluate(()=>(window as any).__testEnrichmentReads())).toBeGreaterThan(before);
 expect(await page.evaluate(()=>(window as any).__testRefreshStarts())).toBe(0);
 expect(await page.evaluate(()=>(window as any).__testEnrichmentRequest())).toBeNull();
});

test('sequential refresh keeps whole-batch progress across completed, active and queued files',async({page})=>{
 await open(page,25);await expect(panel(page).locator('.enrichment-refresh-help')).toContainText('one asset at a time');await page.getByRole('button',{name:'Refresh data and indicators',exact:true}).click();
 await page.evaluate(()=>{
  const info=(window as any).__testRefresh() as ScreenerRefreshInfo;
  (window as any).__testSetRefresh({...info,completed:10,progress:42,currentFile:info.jobs[10].name,currentProgress:50,jobs:info.jobs.map((job,index)=>({...job,status:index<10?'completed':index===10?'running':'queued',progress:index<10?100:index===10?50:0}))});
 });
 await expect(page.getByRole('progressbar',{name:'Overall data refresh progress',exact:true})).toHaveAttribute('value','42');
 await page.getByRole('button',{name:'Next files',exact:true}).click();await expect(panel(page).locator('tbody tr')).toHaveCount(5);
 await expect(page.getByRole('progressbar',{name:'Overall data refresh progress',exact:true})).toHaveAttribute('value','42');
 await expect(page.getByRole('button',{name:'Cancel refresh',exact:true})).toBeEnabled();
});

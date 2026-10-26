import {test,expect,type Page} from '@playwright/test';
import type {ScreenerDataFile,ScreenerRefreshInfo} from '../src/types';

const fields=['timestamp','open','high','low','close','volume','sma_200','macd_histogram_12_26_9'];
const file=(index:number):ScreenerDataFile=>({id:`refresh-${index}`,name:`asset${index}-d1-2024-01-01-2024-02-01.csv`,symbol:`asset${index}`,timeframe:'d1',from:'2024-01-01',to:'2024-02-01',fields:[...fields],bytes:2000});
async function open(page:Page,count=2){
 await page.goto('/');await page.evaluate(files=>(window as any).__testSetEnrichmentFiles(files),Array.from({length:count},(_,i)=>file(i)));
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

test('parallel refresh uses whole-batch progress independently of one current file and the visible page',async({page})=>{
 await open(page,25);await page.getByRole('button',{name:'Refresh data and indicators',exact:true}).click();
 await page.evaluate(()=>{
  const info=(window as any).__testRefresh() as ScreenerRefreshInfo;
  (window as any).__testSetRefresh({...info,progress:40,currentFile:info.jobs[19].name,currentProgress:50,jobs:info.jobs.map((job,index)=>({...job,status:index<20?'running':'queued',progress:index<20?50:0}))});
 });
 await expect(page.getByRole('progressbar',{name:'Overall data refresh progress',exact:true})).toHaveAttribute('value','40');
 await page.getByRole('button',{name:'Next files',exact:true}).click();await expect(panel(page).locator('tbody tr')).toHaveCount(5);
 await expect(page.getByRole('progressbar',{name:'Overall data refresh progress',exact:true})).toHaveAttribute('value','40');
 await expect(page.getByRole('button',{name:'Cancel refresh',exact:true})).toBeEnabled();
});

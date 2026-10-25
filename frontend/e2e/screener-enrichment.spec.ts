import {test,expect,type Page} from '@playwright/test';
import type {ScreenerDataFile,ScreenerEnrichmentInfo} from '../src/types';

const fields=['timestamp','open','high','low','close','volume'];
const file=(index:number):ScreenerDataFile=>({id:`file-${index}`,name:`symbol${String(index).padStart(3,'0')}-d1-2024-01-01-2024-02-01.csv`,symbol:`symbol${index}`,timeframe:'d1',from:'2024-01-01',to:'2024-02-01',fields:[...fields],bytes:2048});
async function open(page:Page,count=2){
 await page.goto('/');
 await page.evaluate(value=>(window as any).__testSetEnrichmentFiles(value),Array.from({length:count},(_,i)=>file(i)));
 await page.getByRole('tab',{name:'Screener',exact:true}).click();
 await page.getByRole('tab',{name:'Enrichment',exact:true}).click();
 await expect(page.getByRole('button',{name:`Enrich ${count} ${count===1?'file':'files'}`,exact:true})).toBeEnabled();
}
const panel=(page:Page)=>page.locator('#screener-enrichment');
async function progress(page:Page,value:Partial<ScreenerEnrichmentInfo>){await page.evaluate(value=>(window as any).__testSetEnrichment(value),value);}

test('Enrichment loads CSV periods and comma separated headers only when opened, with all reviewed indicators',async({page})=>{
 await page.goto('/');
 expect(await page.evaluate(()=>(window as any).__testEnrichmentReads())).toBe(0);
 await page.getByRole('tab',{name:'Screener',exact:true}).click();
 await expect(page.locator('#screener-analysis').getByRole('heading',{name:'Visual condition rows',exact:true})).toBeVisible();
 expect(await page.evaluate(()=>(window as any).__testEnrichmentReads())).toBe(0);
 await page.evaluate(value=>(window as any).__testSetEnrichmentFiles([value]),file(0));
 await page.getByRole('tab',{name:'Enrichment',exact:true}).click();
 await expect(panel(page).locator('tbody tr')).toHaveCount(1);
 await expect(panel(page).locator('tbody tr')).toContainText('2024-01-01 → 2024-02-01');
 await expect(panel(page).locator('tbody tr code')).toHaveText(fields.join(', '));
 await expect(panel(page).getByText('/explicit-test/screener',{exact:true})).toBeVisible();
 await expect(page.getByLabel('Enrichment indicator',{exact:true}).locator('option')).toHaveCount(40);
 await expect(page.getByLabel('Period',{exact:true})).toHaveValue('200');
 await expect.poll(()=>page.evaluate(()=>(window as any).__testLeases())).toBe(0);
});

test('SMA enrichment submits parameters for all files and displays per-file progress, columns and durable headers',async({page})=>{
 await open(page);
 await page.getByLabel('Period',{exact:true}).fill('3');
 await page.getByRole('button',{name:'Enrich 2 files',exact:true}).click();
 expect(await page.evaluate(()=>(window as any).__testEnrichmentRequest())).toEqual({indicator:'sma',parameters:{period:3},source:'auto'});
 const info=await page.evaluate(()=>(window as any).__testEnrichment()) as ScreenerEnrichmentInfo;
 await progress(page,{currentFile:info.jobs[0].name,currentProgress:42,jobs:info.jobs.map((job,i)=>({...job,status:i===0?'running':'queued',progress:i===0?42:0,rows:i===0?100:0,columns:['sma_3'],message:i===0?'Calculated 100 rows':'Waiting for enrichment'}))});
 const row=panel(page).locator('tbody tr').first();
 await expect(row.getByRole('progressbar')).toHaveAttribute('value','42');
 await expect(row).toContainText('Calculated 100 rows');
 await expect(page.getByLabel('Period',{exact:true})).toBeDisabled();
 await page.getByRole('tab',{name:'Analysis',exact:true}).click();await expect(page.locator('#screener-analysis').getByRole('heading',{name:'Visual condition rows',exact:true})).toBeVisible();
 await page.getByRole('tab',{name:'Enrichment',exact:true}).click();
 await expect(row.getByRole('progressbar')).toHaveAttribute('value','42');
 await page.evaluate(()=>{const files=(window as any).__testEnrichmentFiles() as ScreenerDataFile[];(window as any).__testSetEnrichmentFiles(files.map(file=>({...file,fields:[...file.fields,'sma_3']})));});
 await progress(page,{active:false,completed:2,currentFile:'',currentProgress:0,jobs:info.jobs.map(job=>({...job,status:'completed',progress:100,rows:200,columns:['sma_3'],message:'Enriched 200 rows',backupPath:'/explicit-test/screener/.cryptodesk-enrichment-backups/original.csv'}))});
 await expect(panel(page).getByText('2 / 2 complete · 0 failed',{exact:true})).toBeVisible();
 await expect(row.locator('td').nth(2)).toContainText('volume, sma_3');
 await expect(row).toContainText('Original backup retained');
 await expect(page.getByRole('button',{name:'Enrich 2 files',exact:true})).toBeEnabled();
 await page.reload();await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Enrichment',exact:true}).click();
 await expect(panel(page).locator('tbody tr').first().locator('td').nth(2)).toContainText('volume, sma_3');
});

test('indicator changes reset parameter controls, validate periods and expose tick price sources',async({page})=>{
 await open(page,1);
 await page.getByLabel('Enrichment indicator',{exact:true}).selectOption('macd');
 await expect(page.getByLabel('Fast period',{exact:true})).toHaveValue('12');
 await expect(page.getByLabel('Slow period',{exact:true})).toHaveValue('26');
 await expect(page.getByLabel('Signal period',{exact:true})).toHaveValue('9');
 await page.getByLabel('Fast period',{exact:true}).fill('26');
 await expect(page.getByRole('button',{name:'Enrich 1 file',exact:true})).toBeDisabled();
 await expect(panel(page)).toContainText('Fast period must be smaller than slow period.');
 await page.getByLabel('Fast period',{exact:true}).fill('5');await page.getByLabel('Slow period',{exact:true}).fill('10');await page.getByLabel('Signal period',{exact:true}).fill('4');
 await page.getByLabel('Enrichment price source',{exact:true}).selectOption('askprice');
 await page.getByRole('button',{name:'Enrich 1 file',exact:true}).click();
 expect(await page.evaluate(()=>(window as any).__testEnrichmentRequest())).toEqual({indicator:'macd',parameters:{fast:5,slow:10,signal:4},source:'askprice'});
 await page.getByRole('button',{name:'Cancel enrichment',exact:true}).click();
 await page.getByLabel('Enrichment indicator',{exact:true}).selectOption('tenkan');
 await expect(page.getByLabel('Period',{exact:true})).toHaveValue('9');
 await expect(page.getByLabel('Enrichment price source',{exact:true})).toBeDisabled();
 await page.getByLabel('Period',{exact:true}).fill('0');await expect(page.getByRole('button',{name:'Enrich 1 file',exact:true})).toBeDisabled();
 await page.getByLabel('Enrichment indicator',{exact:true}).selectOption('ichimoku');
 await expect(page.getByLabel('Tenkan period',{exact:true})).toHaveValue('9');
 await expect(panel(page)).toContainText('no future or displaced rows');
});

test('file pagination keeps the entire batch in scope and shows progress beyond the first page',async({page})=>{
 await open(page,57);
 await expect(panel(page).locator('tbody tr')).toHaveCount(50);
 await expect(panel(page)).toContainText('1–50 of 57 files');
 await page.getByRole('button',{name:'Enrich 57 files',exact:true}).click();
 const info=await page.evaluate(()=>(window as any).__testEnrichment()) as ScreenerEnrichmentInfo;
 expect(info.jobs).toHaveLength(57);
 await progress(page,{currentFile:info.jobs[56].name,currentProgress:75,jobs:info.jobs.map((job,i)=>({...job,status:i===56?'running':'queued',progress:i===56?75:0,message:'Explicit progress'}))});
 await page.getByRole('button',{name:'Next files',exact:true}).click();
 await expect(panel(page).locator('tbody tr')).toHaveCount(7);
 await expect(panel(page)).toContainText('51–57 of 57 files');
 await expect(panel(page).locator('tbody tr').last().getByRole('progressbar')).toHaveAttribute('value','75');
 await expect(page.getByRole('button',{name:'Next files',exact:true})).toBeDisabled();
 await page.getByRole('button',{name:'Previous files',exact:true}).click();
 await expect(panel(page).locator('tbody tr')).toHaveCount(50);
 await page.getByRole('button',{name:'Cancel enrichment',exact:true}).click();
 await expect(panel(page)).toContainText('57 cancelled');
});

test('failed files stay visible beside completed files and cancellation preserves completed results',async({page})=>{
 await open(page,3);await page.getByRole('button',{name:'Enrich 3 files',exact:true}).click();
 const info=await page.evaluate(()=>(window as any).__testEnrichment()) as ScreenerEnrichmentInfo;
 await progress(page,{completed:1,failed:1,currentFile:info.jobs[2].name,currentProgress:10,jobs:info.jobs.map((job,i)=>({...job,status:i===0?'completed':i===1?'failed':'running',progress:i===0?100:10,message:i===1?'Requires high, low fields. Tick prices are not converted into candles.':i===0?'Enriched 500 rows':'Calculating rows',columns:i===0?['sma_200']:[]}))});
 await expect(panel(page).locator('tbody tr').nth(1)).toContainText('Requires high, low fields');
 await page.getByRole('button',{name:'Cancel enrichment',exact:true}).click();
 await expect(panel(page)).toContainText('1 / 3 complete · 1 failed · 1 cancelled');
 await expect(panel(page).locator('tbody tr').first()).toContainText('completed · 100%');
 await expect(panel(page).locator('tbody tr').last()).toContainText('original file kept');
});

test('missing runtime, folder errors and start failures are reported without changing files',async({page})=>{
 await open(page,1);
 await progress(page,{runtimeAvailable:false,runtimeError:'Explicit missing trading-signals package'});
 await expect(panel(page).getByRole('alert')).toContainText('Explicit missing trading-signals package');
 await expect(page.getByRole('button',{name:'Enrich 1 file',exact:true})).toBeDisabled();
 await progress(page,{runtimeAvailable:true,runtimeError:''});
 await expect(page.getByRole('button',{name:'Enrich 1 file',exact:true})).toBeEnabled();
 await page.evaluate(()=>(window as any).__testEnrichmentErrors('','Explicit read-only folder'));
 await page.getByRole('button',{name:'Enrich 1 file',exact:true}).click();
 await expect(panel(page).getByRole('alert')).toContainText('Explicit read-only folder');
 expect(await page.evaluate(()=>(window as any).__testEnrichmentRequest())).toBeNull();
 await page.evaluate(()=>(window as any).__testEnrichmentErrors('Explicit folder unavailable',''));
 await page.getByRole('button',{name:'Reload file list',exact:true}).click();
 await expect(panel(page).getByRole('alert')).toContainText('Explicit read-only folder');
 await page.getByRole('button',{name:'Retry loading',exact:true}).click();
 await expect(panel(page).getByRole('alert')).toContainText('Explicit folder unavailable');
 await page.evaluate(()=>(window as any).__testEnrichmentErrors('',''));
 await page.getByRole('button',{name:'Retry loading',exact:true}).click();
 await expect(panel(page).getByRole('alert')).toHaveCount(0);
});

test('empty folders show a clear download prompt and never enable enrichment',async({page})=>{
 await page.goto('/');await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Enrichment',exact:true}).click();
 await expect(panel(page)).toContainText('No downloaded CSV files.');
 await expect(page.getByRole('button',{name:'Enrich 0 files',exact:true})).toBeDisabled();
 await expect(panel(page).locator('tbody tr')).toHaveCount(0);
});

test('fast consecutive batches refresh headers when their completed file counts match',async({page})=>{
 await open(page,1);
 for(const [indicator,column] of [['sma','sma_200'],['tenkan','tenkan_9']]){
  await page.getByLabel('Enrichment indicator',{exact:true}).selectOption(indicator);
  await page.getByRole('button',{name:'Enrich 1 file',exact:true}).click();
  await page.evaluate(column=>{
   const info=(window as any).__testEnrichment() as ScreenerEnrichmentInfo;
   const files=(window as any).__testEnrichmentFiles() as ScreenerDataFile[];
   (window as any).__testSetEnrichmentFiles(files.map(file=>({...file,fields:[...file.fields,column]})));
   (window as any).__testSetEnrichment({...info,active:false,completed:1,jobs:info.jobs.map(job=>({...job,status:'completed',progress:100,columns:[column],message:'Completed before the next poll'}))});
  },column);
  await expect(panel(page).locator('tbody tr td').nth(2)).toContainText(column);
  await expect(page.getByRole('button',{name:'Enrich 1 file',exact:true})).toBeEnabled();
 }
});

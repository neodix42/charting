import {test,expect,type Page} from '@playwright/test';
import {openTestApp,reloadTestApp} from './helpers/testApp';

const btc='hyperliquid-perpetual'+'b'.repeat(64),spot='hyperliquid-spot'+'a'.repeat(64);
const source=(page:Page)=>page.getByRole('combobox',{name:'Download data source',exact:true});
const all=(page:Page)=>page.getByRole('checkbox',{name:'Select all symbols',exact:true});
const asset=(page:Page,name:string)=>page.getByRole('checkbox',{name,exact:true});
const download=(page:Page,count:number)=>page.getByRole('button',{name:`Download ${count} ${count===1?'symbol':'symbols'}`,exact:true});
async function open(page:Page){await openTestApp(page);await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Downloader',exact:true}).click();await expect(all(page)).toBeEnabled();}
async function selectAllSources(page:Page){await source(page).selectOption('all');await expect(page.getByLabel('Selected data sources',{exact:true})).toContainText('Hyperliquid: 3 selected');}
async function clearSelection(page:Page){await all(page).check();await all(page).uncheck();}
const eurName='Dukascopy · EURUSD — Euro vs US Dollar',btcName='Hyperliquid · BTC — BTC · perpetual',spotName='Hyperliquid · @107 — HYPE/USDC · @107 · spot';

test('All data sources preserves independent selections and submits one ordered batch with shared settings',async({page})=>{
 await open(page);await asset(page,'EURUSD — Euro vs US Dollar').check();await page.getByLabel('Download timeframe',{exact:true}).selectOption('m15');
 await selectAllSources(page);await expect(page.getByLabel('Download timeframe',{exact:true})).toHaveValue('d1');
 expect(await page.getByLabel('Download timeframe',{exact:true}).locator('option').allTextContents()).toEqual(['h1','d1','w1']);
 await expect(asset(page,eurName)).toBeChecked();await expect(page.getByLabel('Selected data sources',{exact:true})).toContainText('Dukascopy: 1 selected');
 await clearSelection(page);await asset(page,eurName).check();await asset(page,btcName).check();
 await expect(page.getByLabel('Selected symbol count',{exact:true})).toHaveText('2 / 7 selected');
 await page.getByLabel('Download from date',{exact:true}).fill('2024-01-01');await page.getByLabel('Download to date',{exact:true}).fill('2024-02-01');
 await expect.poll(()=>page.evaluate(()=>JSON.parse(sessionStorage.getItem('cryptodesk-explicit-test-screener-view')||'null'))).toMatchObject({dataSource:'all',symbols:['eurusd'],hyperliquidSymbols:[btc],hyperliquidSelectionInitialized:true});
 await reloadTestApp(page);await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Downloader',exact:true}).click();
 await expect(source(page)).toHaveValue('all');await expect(asset(page,eurName)).toBeChecked();await expect(asset(page,btcName)).toBeChecked();await expect(asset(page,spotName)).not.toBeChecked();
 await page.getByLabel('Download to date',{exact:true}).fill('2024-02-01');await page.evaluate(()=>window.go!.main!.App!.ConfigureScreenerConcurrency(7));
 await download(page,2).click();
 expect(await page.evaluate(()=>(window as any).__testScreenerRequest())).toEqual({dataSource:'all',sourceSelections:[{dataSource:'dukascopy',symbols:['eurusd']},{dataSource:'hyperliquid',symbols:[btc]}],symbols:[],timeframe:'d1',from:'2024-01-01',to:'2024-02-01',concurrency:7});
 await expect(page.getByLabel('Download source sequence',{exact:true})).toHaveText('Download order: Dukascopy → Hyperliquid · Current source: Dukascopy');
 await page.screenshot({path:'../.tools/verification/all-sources-downloader.png'});
 await expect(source(page)).toBeDisabled();
 await page.evaluate(()=>{const state=(window as any).__testScreener();(window as any).__testSetScreener({activeDataSource:'hyperliquid',jobs:state.jobs.map((job:any)=>job.dataSource==='dukascopy'?{...job,status:'completed',progress:100}:{...job,status:'running',progress:30})});});
 await expect(page.getByLabel('Download source sequence',{exact:true})).toContainText('Current source: Hyperliquid');
 await expect(page.locator('.screener-job-title').filter({hasText:'EURUSD'})).toContainText('Dukascopy');
 await expect(page.locator('.screener-job-title').filter({hasText:'BTC'})).toContainText('Hyperliquid');
 await page.getByRole('button',{name:'Cancel downloads',exact:true}).click();
 const finished=await page.evaluate(()=>(window as any).__testScreener());expect(finished.jobs.map((job:any)=>job.status)).toEqual(['completed','cancelled']);
 await source(page).selectOption('dukascopy');await expect(page.getByLabel('Download source sequence',{exact:true})).toHaveText('Download order: Dukascopy → Hyperliquid');
});

test('All discovery errors block submission and Retry cannot silently omit the failed source',async({page})=>{
 await open(page);await asset(page,'EURUSD — Euro vs US Dollar').check();
 await page.evaluate(()=>{const api=window.go!.main!.App!,read=api.GetScreenerCatalogForSource.bind(api);(window as any).__restoreAllDiscovery=()=>{api.GetScreenerCatalogForSource=read;};api.GetScreenerCatalogForSource=async()=>{throw Error('Explicit Hyperliquid catalog outage');};});
 await source(page).selectOption('all');await expect(page.getByRole('alert')).toContainText('Hyperliquid discovery failed');
 await expect(page.locator('.screener-instrument-group')).toHaveCount(0);await expect(page.getByRole('button',{name:/^Download .*symbols?$/})).toBeDisabled();
 expect(await page.evaluate(()=>(window as any).__testScreenerRequest())).toBeNull();
 await page.evaluate(()=>(window as any).__restoreAllDiscovery());await page.getByRole('button',{name:'Retry downloader',exact:true}).click();
 await expect(asset(page,eurName)).toBeChecked();await expect(asset(page,btcName)).toBeChecked();await expect(download(page,4)).toBeEnabled();
});

test('All downloaded selection spans both sources, ignores search and keeps original category persistence keys',async({page})=>{
 await open(page);await selectAllSources(page);await asset(page,eurName).check();
 await page.evaluate(({spot})=>(window as any).__testSetScreener({downloadedSymbols:['btcusd',spot]}),{spot});
 await expect(page.getByRole('checkbox',{name:'Select all downloaded assets',exact:true})).toBeEnabled();
 await page.getByRole('searchbox',{name:'Search All data sources instruments',exact:true}).fill('unmatched-search');
 await page.getByRole('checkbox',{name:'Select all downloaded assets',exact:true}).check();
 await expect(page.getByLabel('Selected data sources',{exact:true})).toContainText('Dukascopy: 1 selected · Hyperliquid: 1 selected');
 await page.getByRole('searchbox',{name:'Search All data sources instruments',exact:true}).fill('');
 await expect(asset(page,'Dukascopy · BTCUSD — Bitcoin vs US Dollar')).toBeChecked();await expect(asset(page,spotName)).toBeChecked();await expect(asset(page,eurName)).not.toBeChecked();await expect(asset(page,btcName)).not.toBeChecked();
 const forex=page.locator('details').filter({has:page.locator('summary',{hasText:'Dukascopy · Forex'})});await forex.locator('summary').click();
 await expect.poll(()=>page.evaluate(()=>JSON.parse(sessionStorage.getItem('cryptodesk-explicit-test-screener-view')||'null'))).toMatchObject({dataSource:'all',collapsedCategories:['Forex'],symbols:['btcusd'],hyperliquidSymbols:[spot]});
 await source(page).selectOption('hyperliquid');await expect(asset(page,'@107 — HYPE/USDC · @107 · spot')).toBeChecked();await expect(asset(page,'BTC — BTC · perpetual')).not.toBeChecked();
 await source(page).selectOption('dukascopy');await expect(asset(page,'BTCUSD — Bitcoin vs US Dollar')).toBeChecked();
 await source(page).selectOption('all');await expect(forex).not.toHaveAttribute('open','');await expect(page.getByLabel('Selected symbol count',{exact:true})).toHaveText('2 / 7 selected');
});

test('All runtime requirements follow selected symbols while Resume follows unfinished queue sources',async({page})=>{
 await open(page);await selectAllSources(page);await clearSelection(page);await asset(page,btcName).check();
 await page.evaluate(()=>(window as any).__testSetScreener({runtimeAvailable:false,runtimeError:'Explicit missing Dukascopy runtime'}));
 await expect(download(page,1)).toBeEnabled();await expect(page.getByRole('alert')).toHaveCount(0);
 await asset(page,eurName).check();await expect(download(page,2)).toBeDisabled();await expect(page.getByRole('alert')).toContainText('Explicit missing Dukascopy runtime');
 await asset(page,eurName).uncheck();await download(page,1).click();
 expect((await page.evaluate(()=>(window as any).__testScreenerRequest())).sourceSelections).toEqual([{dataSource:'hyperliquid',symbols:[btc]}]);
 await page.getByRole('button',{name:'Cancel downloads',exact:true}).click();
 await page.evaluate(({btc})=>(window as any).__testSetScreener({active:false,sourceOrder:['dukascopy','hyperliquid'],activeDataSource:undefined,jobs:[{id:'done-duka',dataSource:'dukascopy',symbol:'eurusd',timeframe:'d1',from:'2024-01-01',to:'2024-02-01',status:'completed',progress:100,message:'Completed first source'},{id:'pending-hl',dataSource:'hyperliquid',symbol:btc,nativeId:'BTC',timeframe:'d1',from:'2024-01-01',to:'2024-02-01',status:'failed',progress:0,message:'Retry second source'}]}),{btc});
 await expect(source(page)).toBeEnabled();await source(page).selectOption('dukascopy');
 const resume=page.getByRole('button',{name:'Resume downloads',exact:true});await expect(resume).toBeEnabled();await resume.click();
 await expect(page.getByLabel('Download source sequence',{exact:true})).toContainText('Current source: Hyperliquid');
 await page.getByRole('button',{name:'Cancel downloads',exact:true}).click();
});

test('a whole-batch active flag keeps controls locked and Cancel available during a source handoff',async({page})=>{
 await open(page);await selectAllSources(page);
 await page.evaluate(()=>{const api=window.go!.main!.App!,read=api.GetScreener.bind(api),cancel=api.CancelScreenerDownload.bind(api);let handoff=true;api.GetScreener=async()=>({...await read(),active:handoff});api.CancelScreenerDownload=async()=>{handoff=false;return cancel();};(window as any).__testSetScreener({sourceOrder:['dukascopy','hyperliquid'],activeDataSource:'dukascopy',jobs:[]});});
 await expect(source(page)).toBeDisabled();await expect(page.getByLabel('Download timeframe',{exact:true})).toBeDisabled();
 await expect(page.getByRole('button',{name:'Cancel downloads',exact:true})).toBeEnabled();await page.getByRole('button',{name:'Cancel downloads',exact:true}).click();await expect(source(page)).toBeEnabled();
 expect(await page.evaluate(()=>(window as any).__testScreenerRequest())).toBeNull();
});

test('saved IDs absent from current catalogs do not inflate All selection counts or enable an empty request',async({page})=>{
 await openTestApp(page);
 const unavailable='hyperliquid-perpetual'+'f'.repeat(64);
 await page.evaluate(async unavailable=>{await window.go!.main!.App!.SaveScreenerView({dataSource:'all',symbols:[],hyperliquidSymbols:[unavailable],hyperliquidSelectionInitialized:true,timeframe:'d1',from:'2024-01-01',to:'2024-02-01',concurrency:5,collapsedCategories:[]});},unavailable);
 await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Downloader',exact:true}).click();await expect(all(page)).toBeEnabled();
 await expect(source(page)).toHaveValue('all');await expect(page.getByLabel('Selected symbol count',{exact:true})).toHaveText('0 / 7 selected');
 await expect(page.getByLabel('Selected data sources',{exact:true})).toContainText('Dukascopy: 0 selected · Hyperliquid: 0 selected');
 await expect(page.getByRole('button',{name:/^Download .*symbols?$/})).toBeDisabled();
 expect(JSON.parse(await page.evaluate(()=>sessionStorage.getItem('cryptodesk-explicit-test-screener-view'))||'null').hyperliquidSymbols).toEqual([unavailable]);
 expect(await page.evaluate(()=>(window as any).__testScreenerRequest())).toBeNull();
 await asset(page,eurName).check();await expect(page.getByLabel('Selected symbol count',{exact:true})).toHaveText('1 / 7 selected');
 await expect.poll(()=>page.evaluate(()=>JSON.parse(sessionStorage.getItem('cryptodesk-explicit-test-screener-view')||'null'))).toMatchObject({symbols:['eurusd'],hyperliquidSymbols:[unavailable]});
});

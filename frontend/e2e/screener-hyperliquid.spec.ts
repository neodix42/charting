import {test,expect,type Page} from '@playwright/test';
import {openTestApp,reloadTestApp} from './helpers/testApp';
import type {ScreenerDataFile} from '../src/types';

async function downloader(page:Page){
 await page.getByRole('tab',{name:'Screener',exact:true}).click();
 await page.getByRole('tab',{name:'Downloader',exact:true}).click();
 await expect(page.getByLabel('Download data source',{exact:true})).toBeEnabled();
}

test('Hyperliquid starts with all spot and perpetual assets enabled and retains separate selections across restart',async({page})=>{
 await openTestApp(page);await downloader(page);
 await expect(page.getByLabel('Download data source',{exact:true})).toHaveValue('dukascopy');
 await page.getByRole('checkbox',{name:'BTCUSD — Bitcoin vs US Dollar',exact:true}).check();
 await page.getByLabel('Download timeframe',{exact:true}).selectOption('h1');
 await page.getByLabel('Download data source',{exact:true}).selectOption('hyperliquid');
 await expect(page.getByLabel('Selected symbol count',{exact:true})).toHaveText('3 / 3 selected');
 await expect(page.getByLabel('Download timeframe',{exact:true})).toHaveValue('h1');
 await expect(page.getByLabel('Download timeframe',{exact:true}).locator('option')).toHaveText(['h1','d1','w1']);
 await page.getByRole('searchbox',{name:'Search Hyperliquid instruments',exact:true}).fill('xyz:');
 await expect(page.locator('.screener-instrument')).toHaveCount(1);
 await page.locator('.screener-instrument input').uncheck();
 await page.getByRole('searchbox',{name:'Search Hyperliquid instruments',exact:true}).clear();
 await expect(page.getByLabel('Selected symbol count',{exact:true})).toHaveText('2 / 3 selected');
 await expect.poll(()=>page.evaluate(()=>JSON.parse(sessionStorage.getItem('cryptodesk-explicit-test-screener-view')||'null').hyperliquidSymbols.length)).toBe(2);
 await reloadTestApp(page);await downloader(page);
 await expect(page.getByLabel('Download data source',{exact:true})).toHaveValue('hyperliquid');
 await expect(page.getByLabel('Selected symbol count',{exact:true})).toHaveText('2 / 3 selected');
 await page.getByLabel('Download data source',{exact:true}).selectOption('dukascopy');
 await expect(page.getByLabel('Selected symbol count',{exact:true})).toHaveText('1 / 4 selected');
 await expect(page.getByRole('checkbox',{name:'BTCUSD — Bitcoin vs US Dollar',exact:true})).toBeChecked();
 await page.getByLabel('Download data source',{exact:true}).selectOption('hyperliquid');
 await expect(page.getByLabel('Selected symbol count',{exact:true})).toHaveText('2 / 3 selected');
 await page.getByRole('checkbox',{name:'Select all symbols',exact:true}).check();
 await page.getByRole('checkbox',{name:'Select all symbols',exact:true}).uncheck();
 await expect.poll(()=>page.evaluate(()=>JSON.parse(sessionStorage.getItem('cryptodesk-explicit-test-screener-view')||'null').hyperliquidSymbols)).toEqual([]);
 await reloadTestApp(page);await downloader(page);
 await expect(page.getByLabel('Selected symbol count',{exact:true})).toHaveText('0 / 3 selected');
 await expect(page.locator('#screener-downloader button[type="submit"]')).toBeDisabled();
});

test('Hyperliquid weekly submission retains the selected UTC dates and does not require the Dukascopy runtime',async({page})=>{
 await openTestApp(page);await downloader(page);
 await page.evaluate(()=>(window as any).__testSetScreener({runtimeAvailable:false,runtimeError:'Dukascopy runtime missing'}));
 await page.getByLabel('Download data source',{exact:true}).selectOption('hyperliquid');
 await expect(page.getByLabel('Selected symbol count',{exact:true})).toHaveText('3 / 3 selected');
 await page.getByLabel('Download timeframe',{exact:true}).selectOption('w1');
 await page.getByLabel('Download from date',{exact:true}).fill('2010-01-01');
 await page.getByLabel('Download to date',{exact:true}).fill('2026-11-09');
 await page.getByRole('checkbox',{name:'Select all Spot',exact:true}).uncheck();
 await page.getByRole('button',{name:'Download 2 symbols',exact:true}).click();
 const request=await page.evaluate(()=>(window as any).__testScreenerRequest());
 expect(request).toMatchObject({dataSource:'hyperliquid',timeframe:'w1',from:'2010-01-01',to:'2026-11-09',concurrency:5});
 expect(request.symbols).toHaveLength(2);expect(request.symbols.every((id:string)=>id.startsWith('hyperliquid-perpetual'))).toBe(true);
 await expect(page.getByLabel('Download data source',{exact:true})).toBeDisabled();
 await expect(page.locator('.screener-job')).toHaveCount(4);
 await expect(page.locator('#screener-downloader')).toContainText('kept alongside the weekly CSV');
 await page.getByRole('button',{name:'Cancel downloads',exact:true}).click();
});

test('enrichment filters the complete file collection by source and submits only selected Hyperliquid periods',async({page})=>{
 await openTestApp(page);
 const base={fields:['timestamp','open','high','low','close','volume'],bytes:200};
 const files:ScreenerDataFile[]=[...Array.from({length:23},(_,i)=>({...base,id:`duk-${i}`,name:`eurusd-${i}.csv`,timeframe:'d1'})),{...base,id:'hl-d1',name:'hyperliquid-perpetualbtc-d1.csv',dataSource:'hyperliquid',timeframe:'d1'},{...base,id:'hl-w1',name:'hyperliquid-perpetualbtc-w1.csv',dataSource:'hyperliquid',timeframe:'w1'}];
 await page.evaluate(value=>(window as any).__testSetEnrichmentFiles(value),files);
 await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Enrichment',exact:true}).click();
 await expect(page.getByRole('button',{name:'Enrich 23 files',exact:true})).toBeEnabled();
 await page.getByLabel('Enrichment data source',{exact:true}).selectOption('hyperliquid');
 await expect(page.getByRole('button',{name:'Enrich 2 files',exact:true})).toBeEnabled();
 await expect(page.locator('#screener-enrichment tbody tr')).toHaveCount(2);
 await expect(page.locator('#screener-enrichment tbody')).not.toContainText('eurusd');
 await expect(page.getByRole('button',{name:'Refresh Dukascopy data and indicators',exact:true})).toHaveCount(0);
 await page.getByLabel('Enrichment period',{exact:true}).selectOption('w1');
 await expect(page.getByRole('button',{name:'Enrich 1 file',exact:true})).toBeEnabled();
 await page.getByLabel('Period',{exact:true}).fill('3');
 await page.getByRole('button',{name:'Enrich 1 file',exact:true}).click();
 expect(await page.evaluate(()=>(window as any).__testEnrichmentRequest())).toEqual({dataSource:'hyperliquid',indicator:'sma',parameters:{period:3},source:'auto',period:'w1'});
 expect((await page.evaluate(()=>(window as any).__testEnrichment())).jobs.map((job:{fileId:string})=>job.fileId)).toEqual(['hl-w1']);
 await expect(page.getByLabel('Enrichment data source',{exact:true})).toBeDisabled();
});

test('a delayed Hyperliquid catalog cannot replace the source selected more recently',async({page})=>{
 await openTestApp(page);await downloader(page);
 await page.evaluate(()=>{
  const api=window.go!.main!.App!,original=api.GetScreenerCatalogForSource;
  api.GetScreenerCatalogForSource=source=>source==='hyperliquid'?new Promise(resolve=>{(window as any).__releaseHyperliquidCatalog=async()=>resolve(await original(source));}):original(source);
 });
 await page.getByLabel('Download data source',{exact:true}).selectOption('hyperliquid');
 await expect.poll(()=>page.evaluate(()=>typeof (window as any).__releaseHyperliquidCatalog)).toBe('function');
 await page.getByLabel('Download data source',{exact:true}).selectOption('dukascopy');
 await expect(page.getByLabel('Selected symbol count',{exact:true})).toHaveText('0 / 4 selected');
 await page.evaluate(()=>(window as any).__releaseHyperliquidCatalog());
 await expect(page.getByRole('searchbox',{name:'Search Dukascopy instruments',exact:true})).toBeVisible();
 await expect(page.getByRole('checkbox',{name:'BTCUSD — Bitcoin vs US Dollar',exact:true})).toBeVisible();
 await expect(page.getByLabel('Selected symbol count',{exact:true})).toHaveText('0 / 4 selected');
});

import {test,expect,type Page} from '@playwright/test';
import {openTestApp,reloadTestApp} from './helpers/testApp';
import type {AnalysisFile,AnalysisResult} from '../src/workspace/screenerAnalysis';

const files:AnalysisFile[]=[
 {id:'duka-d1',name:'eurusd-d1-2024-01-01-2024-02-01.csv',symbol:'eurusd',timeframe:'d1',dataSource:'dukascopy',fields:['timestamp','open','high','low','close','ema_20'],bytes:1000,category:'Forex'},
 {id:'duka-h1',name:'eurusd-h1-2024-01-01-2024-02-01.csv',symbol:'eurusd',timeframe:'h1',fields:['timestamp','open','high','low','close','ema_20'],bytes:1000,category:'Forex'},
 {id:'hyper-d1',name:'hyperliquid_btc-d1-2024-01-01-2024-02-01.csv',symbol:'hyperliquid-btc',timeframe:'d1',dataSource:'hyperliquid',fields:['timestamp','open','high','low','close','sma_50'],bytes:1000,category:'Hyperliquid perpetual'},
 {id:'hyper-w1',name:'hyperliquid_btc-w1-2024-01-01-2024-02-01.csv',symbol:'hyperliquid-btc',timeframe:'w1',dataSource:'hyperliquid',fields:['timestamp','open','high','low','close','sma_50'],bytes:1000,category:'Hyperliquid perpetual'},
];
const source=(page:Page)=>page.getByLabel('Analysis data source',{exact:true});
const period=(page:Page)=>page.getByLabel('Analysis period of data',{exact:true});
async function analysis(page:Page){await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Analysis',exact:true}).click();await expect(source(page)).toBeEnabled();}
async function open(page:Page){await openTestApp(page);await page.evaluate(files=>{(window as any).__testSetEnrichmentFiles(files);const {dataSource,...legacy}=(window as any).__testAnalysisView();(window as any).__testSetAnalysisView({...legacy,profitabilityFilter:0,categories:['Forex']});},files);await analysis(page);}
async function saveNew(page:Page,name:string){await page.getByRole('button',{name:'Save as new',exact:true}).click();const dialog=page.getByRole('dialog',{name:'Save condition',exact:true});await dialog.getByLabel('Condition name',{exact:true}).fill(name);await dialog.getByRole('button',{name:'Save condition',exact:true}).click();await expect(dialog).toHaveCount(0);}

test('restoring Hyperliquid waits for the saved draft before requesting its field catalog',async({page})=>{
 await openTestApp(page);await page.evaluate(files=>{
  (window as any).__testSetEnrichmentFiles(files);(window as any).__testSetAnalysisView({...((window as any).__testAnalysisView()),dataSource:'hyperliquid',categories:null,profitabilityFilter:0});
  const api=window.go!.main!.App!,read=api.GetScreenerAnalysisView,catalog=api.GetScreenerAnalysisCatalogForSource;
  (window as any).__sourceCatalogRequests=[];
  api.GetScreenerAnalysisView=()=>new Promise(resolve=>{(window as any).__releaseSourceDraft=async()=>resolve(await read());});
  api.GetScreenerAnalysisCatalogForSource=selected=>{(window as any).__sourceCatalogRequests.push(selected);return catalog(selected);};
 },files);
 await page.getByRole('tab',{name:'Screener',exact:true}).click();await expect.poll(()=>page.evaluate(()=>typeof(window as any).__releaseSourceDraft)).toBe('function');
 expect(await page.evaluate(()=>(window as any).__sourceCatalogRequests)).toEqual([]);
 await page.evaluate(()=>(window as any).__releaseSourceDraft());await expect(source(page)).toHaveValue('hyperliquid');
 await expect(page.getByLabel('Condition 1 left field',{exact:true}).locator('option[value="sma_50"]')).toHaveCount(1);
 expect(await page.evaluate(()=>(window as any).__sourceCatalogRequests)).toEqual(['hyperliquid']);
});

test('Analysis defaults to Dukascopy, selects only downloaded source fields and saves/restores the source with a named condition',async({page})=>{
 await page.setViewportSize({width:1100,height:900});await open(page);
 await expect(source(page)).toHaveValue('dukascopy');await expect(source(page).locator('option')).toHaveText(['Dukascopy','Hyperliquid']);
 const a=await source(page).boundingBox(),b=await period(page).boundingBox();expect(a&&b).toBeTruthy();expect(a!.x+a!.width).toBeLessThanOrEqual(b!.x);
 const field=page.getByLabel('Condition 1 left field',{exact:true});await expect(field.locator('option[value="ema_20"]')).toHaveCount(1);await expect(field.locator('option[value="sma_50"]')).toHaveCount(0);
 await source(page).selectOption('hyperliquid');await expect(field.locator('option[value="sma_50"]')).toHaveCount(1);await expect(field.locator('option[value="ema_20"]')).toHaveCount(0);await expect(page.getByLabel('Scan Hyperliquid perpetual',{exact:true})).toBeChecked();await expect.poll(()=>page.evaluate(()=>(window as any).__testAnalysisView().categories)).toBeNull();
 await field.selectOption('sma_50');await period(page).selectOption('d1');await saveNew(page,'Hyperliquid daily');await expect.poll(()=>page.evaluate(()=>(window as any).__testSavedAnalysisConditions()[0].request.dataSource)).toBe('hyperliquid');
 await source(page).selectOption('dukascopy');await expect(page.getByRole('button',{name:'Load condition Hyperliquid daily',exact:true})).toContainText('Modified draft');await page.getByRole('button',{name:'Load condition Hyperliquid daily',exact:true}).click();await expect(source(page)).toHaveValue('hyperliquid');await expect(period(page)).toHaveValue('d1');await expect(field).toHaveValue('sma_50');await expect(page.getByRole('button',{name:'Load condition Hyperliquid daily',exact:true})).not.toContainText('Modified draft');
 await source(page).selectOption('dukascopy');await page.getByRole('button',{name:'Save condition',exact:true}).click();await expect.poll(()=>page.evaluate(()=>(window as any).__testSavedAnalysisConditions()[0].request.dataSource)).toBe('dukascopy');await source(page).selectOption('hyperliquid');await page.getByRole('button',{name:'Save condition',exact:true}).click();await expect.poll(()=>page.evaluate(()=>(window as any).__testSavedAnalysisConditions()[0].request.dataSource)).toBe('hyperliquid');
 await expect.poll(()=>page.evaluate(()=>(window as any).__testAnalysisView().dataSource)).toBe('hyperliquid');await reloadTestApp(page);await analysis(page);await expect(source(page)).toHaveValue('hyperliquid');await expect(period(page)).toHaveValue('d1');await expect(field).toHaveValue('sma_50');await expect.poll(()=>page.evaluate(()=>(window as any).__testSavedAnalysisConditions()[0].request.dataSource)).toBe('hyperliquid');
});

test('source switches fence delayed catalogs and do not restore old-source results through polling',async({page})=>{
 await open(page);const old:AnalysisResult={id:'old-duka-result',fileId:'duka-d1',name:files[0].name,symbol:'eurusd',timeframe:'d1',status:'completed',rows:30,matches:1,firstTime:1704240000000};
 await page.evaluate(old=>(window as any).__testSetAnalysisInfo({dataSource:'dukascopy',active:false,phase:'completed',total:1,completed:1,totalMatches:1,matchedFiles:1,progress:100,results:[old]}),old);await expect(page.getByRole('button',{name:`Inspect ${old.name} matches`,exact:true})).toBeVisible();
 await page.evaluate(()=>{const api=window.go!.main!.App!,original=api.GetScreenerAnalysisCatalogForSource;(window as any).__originalAnalysisCatalog=original;api.GetScreenerAnalysisCatalogForSource=selected=>selected==='hyperliquid'?new Promise(resolve=>{(window as any).__releaseAnalysisCatalog=async()=>resolve(await original(selected));}):original(selected);});
 await source(page).selectOption('hyperliquid');await expect(page.getByRole('button',{name:`Inspect ${old.name} matches`,exact:true})).toHaveCount(0);await expect(page.getByRole('button',{name:'Apply conditions',exact:true})).toBeDisabled();await expect.poll(()=>page.evaluate(()=>typeof (window as any).__releaseAnalysisCatalog)).toBe('function');
 await source(page).selectOption('dukascopy');await expect(page.getByLabel('Condition 1 left field',{exact:true}).locator('option[value="ema_20"]')).toHaveCount(1);await page.evaluate(()=>(window as any).__releaseAnalysisCatalog());await expect(page.getByLabel('Condition 1 left field',{exact:true}).locator('option[value="sma_50"]')).toHaveCount(0);
 await page.evaluate(()=>{window.go!.main!.App!.GetScreenerAnalysisCatalogForSource=(window as any).__originalAnalysisCatalog;(window as any).__testSetEnrichmentFiles((window as any).__testEnrichmentFiles().filter((file:AnalysisFile)=>file.dataSource!=='hyperliquid'));});
 await source(page).selectOption('hyperliquid');await expect(page.getByText('No downloaded Hyperliquid files.',{exact:false})).toBeVisible();await expect(page.getByRole('button',{name:'Apply conditions',exact:true})).toBeDisabled();await page.waitForTimeout(900);await expect(page.getByRole('button',{name:`Inspect ${old.name} matches`,exact:true})).toHaveCount(0);
});

test('Apply sends the selected source and scans only its selected period',async({page})=>{
 await open(page);await source(page).selectOption('hyperliquid');await period(page).selectOption('d1');await expect(page.getByRole('button',{name:'Apply conditions',exact:true})).toBeEnabled();await page.getByRole('button',{name:'Apply conditions',exact:true}).click();
 await expect.poll(()=>page.evaluate(()=>(window as any).__testAnalysisRequest())).toMatchObject({dataSource:'hyperliquid',period:'d1',categories:null});await expect.poll(()=>page.evaluate(()=>(window as any).__testAnalysisInfo())).toMatchObject({dataSource:'hyperliquid',total:1,active:true});
});

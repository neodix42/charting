import {test,expect,type Page} from '@playwright/test';
import {openTestApp,reloadTestApp} from './helpers/testApp';
import {intervals,type Instrument,type ScreenerCatalog} from '../src/types';
import {firstWorkspace} from '../src/workspace/model';

const category='Synthetic Instruments';
const definitions=[
 {id:'BTCUSD',name:'Bitcoin / US Dollar',market:'cfd' as const,base:'BTC',quote:'USD',calculated:false},
 {id:'BTCXAU',name:'Bitcoin / Gold',market:'cfd' as const,base:'BTC',quote:'XAU',calculated:true},
 {id:'XAUSPX',name:'Gold / S&P 500 Index',market:'cfd' as const,base:'XAU',quote:'SPX',calculated:true},
 {id:'ETHBTC',name:'Ethereum / Bitcoin',market:'cfd' as const,base:'ETH',quote:'BTC',calculated:true},
 {id:'XAUXAG',name:'Gold / Silver',market:'metal' as const,base:'XAU',quote:'XAG',calculated:false},
];
const chartCatalog:Instrument[]=[{nativeId:'EURUSD',market:'forex',baseAsset:'EUR',quoteAsset:'USD',priceIncrement:'0.00001',sizeIncrement:'0.001',availability:'available',supportedIntervals:intervals,providerMetadata:{sdkSymbol:'EUR/USD'}},...definitions.map(item=>({nativeId:item.id,market:item.market,baseAsset:item.base,quoteAsset:item.quote,priceIncrement:'0.000001',sizeIncrement:'',availability:'available',supportedIntervals:item.calculated?['1d','1w'] as Instrument['supportedIntervals']:intervals,providerMetadata:{sdkSymbol:item.id,displaySymbol:item.id,syntheticCategory:'true',...(item.calculated?{synthetic:'true',tradable:'false',approximate:'true',volumeUnavailable:'true',formula:`${item.base} / ${item.quote}`}:{tradable:'true'})}}))];
const downloadCatalog:ScreenerCatalog={version:'explicit-synthetic-fixture',timeframes:['m1','h1','d1','w1'],groups:[{name:'Forex',instruments:[{id:'eurusd',name:'Euro vs US Dollar',earliestDate:'2003-05-04'}]},{name:category,instruments:definitions.map(item=>({id:item.id.toLowerCase(),nativeId:item.id,name:item.name,earliestDate:'2024-01-01',synthetic:item.calculated,category,supportedTimeframes:['d1','w1'],formula:item.calculated?`${item.base} / ${item.quote}`:undefined}))}]};
const ratioFile={id:'ratio-d1',name:'btcxau-d1-2024-01-01-2024-02-01.csv',symbol:'btcxau',timeframe:'d1',fields:['timestamp','open','high','low','close','sma_10'],bytes:1024,synthetic:true,provenance:{version:1,kind:'synchronized-bid-ratio',numerator:'btcusd',denominator:'xauusd',priceType:'bid',maxQuoteAgeMs:0,sessionPolicy:'exact-timestamp-no-carry-v1',volume:'unavailable',approximate:true,observationResolution:'simultaneous millisecond BID ticks'}};

async function installCatalogs(page:Page,charts=chartCatalog){
 await page.evaluate(async({charts,downloads})=>{
  const api=window.go!.main!.App!,original=api.ListInstruments.bind(api);
  await api.ConfigureDucascopy({username:'synthetic-offline',password:'explicit-fixture',environment:'demo'});
  api.ListInstruments=async source=>source==='dukascopy'?structuredClone(charts):original(source);
  api.RefreshDucascopyInstruments=async()=>structuredClone(charts);
  api.GetScreenerCatalog=async()=>structuredClone(downloads);
  const status=api.GetTradingStatus.bind(api);(window as any).__syntheticTradingReads=0;
  api.GetTradingStatus=async key=>{(window as any).__syntheticTradingReads++;return status(key);};
 },{charts,downloads:downloadCatalog});
}
async function selectFamily(page:Page,id:string){
 await page.getByRole('button',{name:'Select instrument',exact:true}).click();
 const picker=page.getByLabel('Instruments',{exact:true});
 await picker.getByRole('button',{name:category,exact:true}).click();
 await picker.getByRole('button',{name:`${id} ${category}`,exact:true}).click();
}
async function downloader(page:Page){
 await page.getByRole('tab',{name:'Screener',exact:true}).click();
 await page.getByRole('tab',{name:'Downloader',exact:true}).click();
 await expect(page.getByRole('checkbox',{name:'Select all symbols',exact:true})).toBeEnabled();
}

test('calculated chart selection is daily/weekly, clearly labelled, non-tradable and restored independently of native family trading',async({page})=>{
 await openTestApp(page);await installCatalogs(page);
 await page.getByRole('combobox',{name:'Exchange',exact:true}).selectOption('dukascopy');
 await page.getByRole('button',{name:'Select instrument',exact:true}).click();
 const picker=page.getByLabel('Instruments',{exact:true});
 await picker.getByRole('button',{name:category,exact:true}).click();
 await expect(picker.locator('.product-list > button')).toHaveCount(5);
 await picker.getByRole('textbox',{name:'Search instruments',exact:true}).fill('BTC');
 await expect(picker.locator('.product-list > button')).toHaveCount(3);
 await picker.getByRole('button',{name:`BTCXAU ${category}`,exact:true}).click();
 const chart=page.getByTestId('chart-cell');
 await expect(chart.getByLabel('Calculated instrument',{exact:true})).toHaveText('Calculated · Approximate · Non-tradable');
 await expect(chart.getByRole('group',{name:'Timeframe',exact:true}).getByRole('button')).toHaveText(['1d','1w']);
 await expect(chart.getByRole('button',{name:'Timeframe 1d',exact:true})).toHaveAttribute('aria-pressed','true');
 await expect(chart.getByRole('button',{name:'Toggle volume',exact:true})).toBeDisabled();
 await expect(chart.getByLabel('Dukascopy trading hours',{exact:true})).toHaveCount(0);
 await expect(chart.getByLabel('Dukascopy leverage and margin',{exact:true})).toHaveCount(0);
 await chart.getByTestId('chart-canvas').click({button:'right',position:{x:350,y:180}});
 const menu=page.getByRole('menu',{name:'Chart trading',exact:true});
 await expect(menu.getByRole('menuitem',{name:'Buy at market…',exact:true})).toBeDisabled();
 await expect(menu).toContainText('cannot be traded');
 expect(await page.evaluate(()=>(window as any).__syntheticTradingReads)).toBe(0);
 await page.keyboard.press('Escape');
 await chart.getByRole('button',{name:'Timeframe 1w',exact:true}).click();
 await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();
 await page.getByTestId('chart-cell').screenshot({path:'test-results/synthetic-chart.png'});
 await reloadTestApp(page);
 await expect(page.getByLabel('Calculated instrument',{exact:true})).toBeVisible();
 await expect.poll(()=>page.evaluate(()=>(window as any).__testWorkspace().tabs[0].charts[0])).toMatchObject({exchange:'dukascopy',market:'cfd',instrument:'BTCXAU',timeframe:'1w'});
 await installCatalogs(page);
 // Switching source away and back reloads the explicit synthetic catalog after restart.
 await page.getByRole('combobox',{name:'Exchange',exact:true}).selectOption('binance');
 await page.getByRole('combobox',{name:'Exchange',exact:true}).selectOption('dukascopy');
 await selectFamily(page,'BTCUSD');
 await expect(chart.getByLabel('Calculated instrument',{exact:true})).toHaveCount(0);
 await expect(chart.getByRole('button',{name:'Toggle volume',exact:true})).toBeEnabled();
 await chart.getByTestId('chart-canvas').click({button:'right',position:{x:350,y:180}});
 await expect(menu.getByRole('menuitem',{name:'Buy at market…',exact:true})).toBeEnabled();
 expect(await page.evaluate(()=>(window as any).__syntheticTradingReads)).toBeGreaterThan(0);
 expect(await page.evaluate(()=>(window as any).__testTradeSubmissions())).toEqual([]);
});

for(const scenario of [
 {instrument:'BTCXAU',state:'unavailable',reason:"download this instrument's D1/W1 history in Screener before opening its calculated chart"},
 {instrument:'XAUSPX',state:'unknownInstrument',reason:'Instrument XAUSPX is not offered by the connected JForex account'},
])test(`empty calculated ${scenario.instrument} shows downloaded-history guidance and Retry for ${scenario.state}`,async({page})=>{
 await openTestApp(page);
 const charts=chartCatalog.map(item=>item.nativeId===scenario.instrument?{...item,providerMetadata:{...item.providerMetadata,catalogWarning:'Native JForex account discovery is unavailable'}}:item);
 await installCatalogs(page,charts);
 await page.evaluate(({instrument,state,reason})=>{
  const api=window.go!.main!.App!,attach=api.AttachChart.bind(api),history=api.GetHistory.bind(api);
  (window as any).__missingSyntheticAttach=0;(window as any).__missingSyntheticHistory=0;(window as any).__automaticSyntheticDownloads=0;
  api.AttachChart=async(...args)=>{
   if(args[2].exchange!=='dukascopy'||args[2].instrument!==instrument)return attach(...args);
   (window as any).__missingSyntheticAttach++;
   // Match the empty market-service snapshot and provider health contract;
   // do not call the success fixture, which would invent calculated candles.
   return {leaseId:`missing-synthetic-${(window as any).__missingSyntheticAttach}`,revision:0,bars:[],health:{state,reason}};
  };
  api.GetHistory=async(...args)=>{
   if(args[1].exchange!=='dukascopy'||args[1].instrument!==instrument)return history(...args);
   (window as any).__missingSyntheticHistory++;
   throw Error("download this instrument's D1/W1 history in Screener before opening its calculated chart");
  };
  api.StartScreenerDownload=async()=>{(window as any).__automaticSyntheticDownloads++;throw Error('Opening a chart must not start a history download');};
 },scenario);
 await page.getByRole('combobox',{name:'Exchange',exact:true}).selectOption('dukascopy');
 await selectFamily(page,scenario.instrument);
 const chart=page.getByTestId('chart-cell'),readout=chart.getByLabel('OHLCV',{exact:true}),message=chart.locator('.chart-message');
 await expect(readout).toContainText(`${scenario.instrument} · 1d · awaiting downloaded calculated history`);
 await expect(readout).not.toContainText('awaiting JForex');
 await expect(readout.locator('b')).toHaveCount(0);
 await expect(message).toContainText(scenario.reason);
 await expect(message).toContainText(`Download ${scenario.instrument} in Screener, then retry.`);
 await expect(message).not.toContainText('Native JForex account discovery is unavailable');
 const retry=message.getByRole('button',{name:'Retry feed',exact:true});
 await expect(retry).toBeVisible();await retry.click();
 await expect.poll(()=>page.evaluate(()=>(window as any).__missingSyntheticAttach)).toBe(2);
 await expect(message).toContainText(`Download ${scenario.instrument} in Screener, then retry.`);
 await chart.getByRole('button',{name:'Return to live',exact:true}).click();
 await expect.poll(()=>page.evaluate(()=>(window as any).__missingSyntheticHistory)).toBe(1);
 await expect(chart.locator('.chart-status')).toContainText("download this instrument's D1/W1 history in Screener");
 await expect(readout.locator('b')).toHaveCount(0);await expect(retry).toBeVisible();
 expect(await page.evaluate(()=>(window as any).__automaticSyntheticDownloads)).toBe(0);
});

test('saved native family intraday charts remain unchanged and ordinary symbols retain every timeframe',async({page})=>{
 const workspace=firstWorkspace();Object.assign(workspace.tabs[0].charts[0],{exchange:'dukascopy',market:'cfd',instrument:'BTCUSD',timeframe:'1h'});
 await page.addInitScript(workspace=>sessionStorage.setItem('cryptodesk-explicit-test-workspace',JSON.stringify(workspace)),workspace);
 await openTestApp(page);await installCatalogs(page);
 await page.getByRole('button',{name:'Select instrument',exact:true}).click();await page.keyboard.press('Escape');
 // Refresh metadata without selecting an asset or rewriting the saved native feed.
 await page.evaluate(async()=>{const path='/src/bridge/instruments.ts';const {refreshDucascopyCatalog}=await import(path);await refreshDucascopyCatalog(window.go!.main!.App!,true);});
 await expect(page.getByLabel('Saved native timeframe',{exact:true})).toHaveText('1h (saved)');
 await expect(page.getByTestId('chart-cell').getByRole('group',{name:'Timeframe',exact:true}).getByRole('button')).toHaveText(['1d','1w']);
 await expect.poll(()=>page.evaluate(()=>(window as any).__testSubscriptions()[0].feedKey)).toMatchObject({instrument:'BTCUSD',timeframe:'1h'});
 expect((await page.evaluate(()=>(window as any).__testWorkspace())).tabs[0].charts[0].timeframe).toBe('1h');
 await page.getByRole('combobox',{name:'Exchange',exact:true}).selectOption('binance');
 await expect(page.getByTestId('chart-cell').getByRole('group',{name:'Timeframe',exact:true}).getByRole('button')).toHaveText(intervals);
});

test('native XAUXAG labels earlier calculated bars without disabling current native trading or inventing volume',async({page})=>{
 await openTestApp(page);await installCatalogs(page);
 await page.evaluate(()=>{
  const api=window.go!.main!.App!,attach=api.AttachChart.bind(api);
  api.AttachChart=async(...args)=>{
   const snapshot=await attach(...args);
   return args[2].instrument==='XAUXAG'?{...snapshot,bars:snapshot.bars.map((bar,index)=>index<90?{...bar,synthetic:true,approximate:true,volumeUnavailable:true}:bar)}:snapshot;
  };
 });
 await page.getByRole('combobox',{name:'Exchange',exact:true}).selectOption('dukascopy');await selectFamily(page,'XAUXAG');
 const chart=page.getByTestId('chart-cell'),box=(await chart.getByTestId('chart-canvas').boundingBox())!;
 await expect.poll(()=>page.evaluate(()=>(window as any).__testChartGaps()[0]?.latestLogical)).toBe(99);
 const geometry=await page.evaluate(()=>(window as any).__testChartGaps()[0]);
 await page.mouse.move(box.x+geometry.lastX-50*geometry.barSpacing,box.y+box.height*.4);
 await expect(chart.getByLabel('Approximate history',{exact:true})).toHaveText('Calculated · Approximate history');
 await expect(chart.getByLabel('OHLCV',{exact:true}).locator('span').filter({hasText:/^V /})).toHaveCount(0);
 await expect(chart.getByLabel('Calculated instrument',{exact:true})).toHaveCount(0);
 await page.mouse.move(0,0);
 await expect(chart.getByLabel('Approximate history',{exact:true})).toHaveCount(0);
 await expect(chart.getByLabel('OHLCV',{exact:true}).locator('span').filter({hasText:/^V /})).toHaveCount(1);
 await chart.getByTestId('chart-canvas').click({button:'right',position:{x:350,y:180}});
 await expect(page.getByRole('menu',{name:'Chart trading',exact:true}).getByRole('menuitem',{name:'Buy at market…',exact:true})).toBeEnabled();
 expect(await page.evaluate(()=>(window as any).__testTradeSubmissions())).toEqual([]);
});

test('Downloader keeps one family category, restricts only its selected periods, persists choices and reports calculated progress',async({page})=>{
 await openTestApp(page);await installCatalogs(page);await downloader(page);
 const group=page.locator('details.screener-instrument-group').filter({has:page.locator('summary').filter({hasText:category})});
 await expect(group).toHaveCount(1);await expect(group.locator('.screener-instrument')).toHaveCount(5);
 const period=page.getByRole('combobox',{name:'Download timeframe',exact:true});
 await period.selectOption('m1');
 await page.getByRole('checkbox',{name:'EURUSD — Euro vs US Dollar',exact:true}).check();
 await page.getByRole('checkbox',{name:'BTCXAU — Bitcoin / Gold',exact:true}).check();
 await expect(period).toHaveValue('d1');await expect(period.locator('option')).toHaveText(['d1','w1']);
 await page.getByRole('searchbox',{name:'Search Dukascopy instruments',exact:true}).fill('ETHBTC');
 await expect(group.locator('.screener-instrument')).toHaveCount(1);
 await page.getByRole('searchbox',{name:'Search Dukascopy instruments',exact:true}).fill('');
 await reloadTestApp(page);await installCatalogs(page);await downloader(page);
 await expect(page.getByRole('checkbox',{name:'BTCXAU — Bitcoin / Gold',exact:true})).toBeChecked();
 await expect(period).toHaveValue('d1');
 await page.getByRole('button',{name:'Download 2 symbols',exact:true}).click();
 expect(await page.evaluate(()=>(window as any).__testScreenerRequest())).toMatchObject({symbols:['eurusd','btcxau'],timeframe:'d1'});
 await page.evaluate(()=>{const info=(window as any).__testScreener();(window as any).__testSetScreener({jobs:info.jobs.map((job:any)=>({...job,status:'running',progress:37,message:'Synchronizing BID observations for calculated ratio'}))});});
 const job=page.getByRole('article',{name:'BTCXAU download',exact:true});
 await expect(job).toContainText('Calculated · Approximate');await expect(job).toContainText('Synchronizing BID observations');
 await expect(job.getByRole('progressbar')).toHaveAttribute('value','37');
 await page.getByRole('button',{name:'Cancel downloads',exact:true}).click();
 await page.getByRole('checkbox',{name:'BTCXAU — Bitcoin / Gold',exact:true}).uncheck();
 await expect(period.locator('option')).toHaveText(['m1','h1','d1','w1']);
 await period.selectOption('h1');
 await page.getByRole('checkbox',{name:'BTCUSD — Bitcoin / US Dollar',exact:true}).check();
 await expect(period).toHaveValue('d1');await expect(period.locator('option')).toHaveText(['d1','w1']);
});

test('Enrichment and Analysis preserve calculated provenance and synthetic-only operand periods',async({page})=>{
 await openTestApp(page);await installCatalogs(page);
 await page.evaluate(file=>{
  (window as any).__testSetEnrichmentFiles([file]);
  (window as any).__testSetAnalysisCatalog({categories:['Forex','Synthetic Instruments'],timeframes:['h1','d1','w1'],files:[{...file,category:'Synthetic Instruments'}]});
 },ratioFile);
 await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Enrichment',exact:true}).click();
 await expect(page.getByRole('row').filter({hasText:ratioFile.name})).toContainText('Calculated · Approximate · Non-tradable');
 await page.getByRole('tab',{name:'Analysis',exact:true}).click();
 await page.getByRole('button',{name:'Deselect all scan categories',exact:true}).click();
 await page.getByRole('checkbox',{name:'Scan Synthetic Instruments',exact:true}).check();
 await expect(page.getByRole('combobox',{name:'Analysis period of data',exact:true}).locator('option')).toHaveText(['All periods','w1 · Weekly','d1 · Daily']);
 await expect(page.getByRole('combobox',{name:'Condition 1 left timeframe',exact:true}).locator('option')).toHaveText(['Same as file','d1','w1']);
 await page.getByRole('combobox',{name:'Analysis profitability filter',exact:true}).selectOption('0');
 await page.getByRole('combobox',{name:'Analysis period of data',exact:true}).selectOption('d1');
 await page.getByRole('button',{name:'Apply conditions',exact:true}).click();
 await page.evaluate(file=>(window as any).__testSetAnalysisInfo({active:false,phase:'completed',failed:0,totalMatches:2,matchedFiles:1,results:[{...file,id:'ratio-result',fileId:file.id,category:'Synthetic Instruments',status:'completed',rows:100,matches:2,simulationBasis:'Synthetic returns are analytical and do not represent an executable position.'}]}),ratioFile);
 await expect(page.locator('.analysis-results')).toContainText('Calculated · Approximate · Non-tradable');
 await page.getByRole('button',{name:'Inspect '+ratioFile.name+' matches',exact:true}).click();
 await expect(page.getByLabel('Synthetic chart provenance',{exact:true})).toContainText('do not represent an executable position');
});

test('synthetic-only saved conditions offer D1/W1 Live Alerts while ordinary conditions keep H1',async({page})=>{
 await openTestApp(page);await installCatalogs(page);
 await page.evaluate(async()=>{
  const api=window.go!.main!.App!,base=await api.GetScreenerAnalysisView();
  await api.SaveScreenerAnalysisCondition('Synthetic condition',{...base,period:'all',categories:['Synthetic Instruments'],profitabilityFilter:0});
  await api.SaveScreenerAnalysisCondition('Ordinary condition',{...base,period:'all',categories:['Forex'],profitabilityFilter:0});
 });
 await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Live Alerts',exact:true}).click();
 await page.getByRole('button',{name:'Add alert',exact:true}).click();
 const dialog=page.getByRole('dialog',{name:'Add alert',exact:true});
 await dialog.getByRole('combobox',{name:'Saved condition',exact:true}).selectOption({label:'Synthetic condition'});
 await expect(dialog.getByRole('combobox',{name:'Alert period',exact:true})).toHaveValue('d1');
 await expect(dialog.getByRole('combobox',{name:'Alert period',exact:true}).locator('option')).toHaveText(['Daily (D1)','Weekly (W1)']);
 await expect(dialog).toContainText('approximate, non-tradable');
 await dialog.getByRole('textbox',{name:'Alert name',exact:true}).fill('Ratio daily');
 await dialog.getByRole('button',{name:'Add alert',exact:true}).click();
 await expect(page.getByRole('table',{name:'Configured alerts',exact:true}).getByRole('row',{name:/Ratio daily/})).toContainText('Daily (D1)');
 await page.getByRole('button',{name:'Add alert',exact:true}).click();
 await dialog.getByRole('combobox',{name:'Saved condition',exact:true}).selectOption({label:'Ordinary condition'});
 await expect(dialog.getByRole('combobox',{name:'Alert period',exact:true}).locator('option')).toHaveText(['Hourly (H1)','Daily (D1)','Weekly (W1)']);
 await dialog.getByRole('button',{name:'Cancel',exact:true}).click();
});

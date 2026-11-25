import {test,expect,type Page} from '@playwright/test';
import {openTestApp,reloadTestApp} from './helpers/testApp';
import {comparisonDate,defaultComparisonView} from '../src/workspace/comparison';
import {getThemePalette,THEME_OPTIONS} from '../src/workspace/themes';

const defaults=['BTC','ETH','TON','HYPE','ZEN','APT','NEAR'];
const tab=(page:Page)=>page.getByRole('tab',{name:'Comparison',exact:true});
const panel=(page:Page)=>page.locator('#comparison-panel');
const chart=(page:Page)=>page.getByTestId('comparison-chart');
const weeks=(page:Page)=>page.getByRole('spinbutton',{name:'Comparison weeks',exact:true});
const calls=(page:Page)=>page.evaluate(()=>(window as any).__testComparisonCalls());
const resources=(page:Page)=>page.evaluate(()=>(window as any).__testComparisonChartResources());
const selected=(page:Page)=>page.getByLabel('Selected comparison assets',{exact:true});
async function enter(page:Page){await tab(page).click();await expect(panel(page)).toBeVisible();}
async function loaded(page:Page,count=7){await expect(chart(page)).toHaveAttribute('data-series-count',String(count));await expect.poll(()=>resources(page)).toEqual({charts:1,observers:2,listeners:1});}
async function frames(page:Page){await page.evaluate(()=>new Promise<void>(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>resolve()))));}
async function add(page:Page,symbol:string,market:'Perpetual'|'Spot'='Perpetual'){
 await page.getByRole('button',{name:'Add comparison asset',exact:true}).click();
 const picker=page.getByLabel('Comparison assets',{exact:true});await picker.getByRole('button',{name:'All',exact:true}).click();
 await picker.getByRole('textbox',{name:'Search instruments',exact:true}).fill(symbol);
 await picker.getByRole('button',{name:`${symbol} ${market}`,exact:true}).click();
}

test('Comparison is a fixed Daily change tool with seven common-baseline percentage lines and explicit venue labels',async({page},testInfo)=>{
 await page.setViewportSize({width:1600,height:1200});
 await openTestApp(page);await expect(page.getByTestId('chart-cell')).toHaveCount(1);
 expect(await calls(page)).toEqual({view:0,save:0,catalog:0,data:0,cancel:0});
 await expect(page.locator('.tab-row > [role="tab"]')).toHaveText(['Screener','Predictions','Comparison','Trade history']);
 const before=await page.evaluate(()=>(window as any).__testAttachCount());
 await enter(page);await loaded(page);
 await expect(panel(page).getByRole('tab',{name:'Daily change',exact:true})).toHaveAttribute('aria-selected','true');
 await expect(weeks(page)).toHaveValue('4');await expect(selected(page).locator('strong')).toHaveText(defaults.map(symbol=>symbol==='TON'?'TON (GRAM)':symbol));
 await expect(selected(page).locator('.comparison-asset').filter({has:page.getByText('ZEN',{exact:true})})).toContainText('Binance · Spot · USDT');
 await expect(selected(page).locator('.comparison-asset').filter({has:page.getByText('BTC',{exact:true})})).toContainText('Hyperliquid · Perpetual · USDC');
 const result=await page.evaluate(()=>(window as any).__testComparisonLastResult());
 expect(result.lastClosedTime-result.baselineTime).toBe(28*86400);expect(result.baselineTime%86400).toBe(0);
 for(const series of result.series){expect(series.points).toHaveLength(29);expect(series.points[0]).toMatchObject({time:result.baselineTime,pct:0});expect(series.points.at(-1).time).toBe(result.lastClosedTime);}
 await expect(page.getByLabel('Comparison legend',{exact:true}).getByRole('button').filter({has:page.getByText('BTC',{exact:true})})).toContainText('+28.00%');
 await expect(page.getByLabel('Comparison legend',{exact:true}).getByRole('button').filter({has:page.getByText('ETH',{exact:true})})).toContainText('-14.00%');
 await expect(panel(page)).toContainText('Latest completed daily close');expect(await page.evaluate(()=>(window as any).__testSubscriptions())).toHaveLength(0);expect(await page.evaluate(()=>(window as any).__testAttachCount())).toBe(before);
 await page.evaluate(()=>{document.documentElement.dataset.theme='dark';});await frames(page);await panel(page).screenshot({path:testInfo.outputPath('comparison-default-seven-assets.png')});
 await page.locator('[data-tab-id]').first().click();await expect.poll(()=>resources(page)).toEqual({charts:0,observers:0,listeners:0});await expect(page.getByTestId('chart-cell')).toHaveCount(1);
 expect(await page.evaluate(()=>(window as any).__testWorkspace().tabs)).toHaveLength(1);
});

test('the verified TON rename displays GRAM while keeping canonical selection and the actual native feed',async({page})=>{
 await openTestApp(page);await enter(page);await loaded(page);
 const renamed=selected(page).locator('.comparison-asset').filter({has:page.getByText('TON (GRAM)',{exact:true})});
 await expect(renamed).toContainText('Hyperliquid · Perpetual · USDC');
 const result=await page.evaluate(()=>(window as any).__testComparisonLastResult()),ton=result.series.find((series:any)=>series.asset.symbol==='TON');
 expect(ton.asset).toMatchObject({symbol:'TON',displaySymbol:'TON (GRAM)',name:'Gram (formerly Toncoin)',nativeId:'GRAM',feedKey:{instrument:'GRAM'}});
 await expect(page.getByLabel('Comparison legend',{exact:true}).getByRole('button').filter({has:page.getByText('TON (GRAM)',{exact:true})})).toHaveAttribute('title','Hyperliquid · Perpetual · USDC · GRAM');
 await page.getByRole('button',{name:'Remove TON from comparison',exact:true}).click();await loaded(page,6);
 await page.getByRole('button',{name:'Add comparison asset',exact:true}).click();const picker=page.getByLabel('Comparison assets',{exact:true});
 await picker.getByRole('textbox',{name:'Search instruments',exact:true}).fill('GRAM');await expect(picker.locator('.product-list > button')).toHaveCount(1);
 await picker.getByRole('button',{name:'TON (GRAM) Perpetual',exact:true}).click();await loaded(page);
 await expect.poll(()=>page.evaluate(()=>(window as any).__testComparisonView().assets)).toEqual(['BTC','ETH','HYPE','ZEN','APT','NEAR','TON']);
 expect((await page.evaluate(()=>(window as any).__testComparisonRequests().at(-1).view.assets)).includes('GRAM')).toBe(false);
 await reloadTestApp(page);await enter(page);await loaded(page);await expect(selected(page).locator('strong')).toHaveText(['BTC','ETH','HYPE','ZEN','APT','NEAR','TON (GRAM)']);
});

test('DCR compares Binance spot daily closes without requiring a Hyperliquid market',async({page},testInfo)=>{
 await page.setViewportSize({width:1600,height:1200});await openTestApp(page);await enter(page);await loaded(page);
 await add(page,'DCR','Spot');await loaded(page,8);
 const asset=selected(page).locator('.comparison-asset').filter({has:page.getByText('DCR',{exact:true})});
 await expect(asset).toContainText('Binance · Spot · USDT');
 const result=await page.evaluate(()=>(window as any).__testComparisonLastResult()),dcr=result.series.find((series:any)=>series.asset.symbol==='DCR');
 expect(dcr.asset).toMatchObject({symbol:'DCR',name:'Decred',exchange:'binance',market:'spot',nativeId:'DCRUSDT',quoteAsset:'USDT',feedKey:{exchange:'binance',market:'spot',instrument:'DCRUSDT',timeframe:'1d'}});
 expect(dcr.error).toBe('');expect(dcr.points).toHaveLength(29);expect(dcr.points[0]).toMatchObject({time:result.baselineTime,pct:0});expect(dcr.points.at(-1).time).toBe(result.lastClosedTime);
 expect(dcr.points.every((point:any)=>Number.isFinite(point.close)&&Number.isFinite(point.pct))).toBe(true);
 await expect(panel(page)).toContainText('DCR: using Binance spot DCRUSDT (USDT)');
 await expect(panel(page)).not.toContainText('no eligible USD-quoted Hyperliquid market');await expect(panel(page)).not.toContainText('context canceled');await expect(panel(page).getByRole('alert')).toHaveCount(0);
 const legend=page.getByLabel('Comparison legend',{exact:true}).getByRole('button',{name:'DCR',exact:true});
 await expect(legend).toHaveAttribute('title','Binance · Spot · USDT · DCRUSDT');await expect(legend).toHaveAttribute('aria-pressed','true');await expect(legend).toContainText('+4.20%');
 await frames(page);const before=await calls(page),read=()=>page.evaluate(()=>(window as any).__testComparisonChart().series.find((series:any)=>series.symbol==='DCR'));
 await legend.click();await expect(legend).toHaveAttribute('aria-pressed','false');await expect.poll(async()=>{const line=await read();return[line.visible,line.dotsVisible];}).toEqual([false,false]);
 await legend.click();await expect(legend).toHaveAttribute('aria-pressed','true');await expect.poll(async()=>{const line=await read();return[line.visible,line.dotsVisible];}).toEqual([true,true]);expect(await calls(page)).toEqual(before);
 await panel(page).locator('.comparison-chart').scrollIntoViewIfNeeded();await frames(page);await panel(page).locator('.comparison-chart').screenshot({path:testInfo.outputPath('comparison-dcr-binance.png')});
});

test('refreshed actual sources override stale catalog labels and unavailable assets never retain an old provider',async({page})=>{
 await openTestApp(page);await enter(page);await loaded(page);
 const btc=selected(page).locator('.comparison-asset').filter({has:page.getByText('BTC',{exact:true})});
 const legend=page.getByLabel('Comparison legend',{exact:true}).getByRole('button').filter({has:page.getByText('BTC',{exact:true})});
 await expect(btc.locator('small')).toHaveText('Hyperliquid · Perpetual · USDC');
 const refreshActual=async(unavailable:boolean)=>{
  await page.evaluate(()=>(window as any).__testComparisonHoldNextData());await panel(page).getByRole('button',{name:'Refresh',exact:true}).click();
  await expect.poll(()=>page.evaluate(()=>(window as any).__testComparisonPending().length)).toBe(1);
  await page.evaluate(unavailable=>{
   const fixture=window as any,id=fixture.__testComparisonPending()[0],result=fixture.__testComparisonLastResult();
   const series=result.series.map((series:any)=>series.asset.symbol!=='BTC'?series:unavailable?
    {...series,asset:{symbol:'BTC',name:'Bitcoin',exchange:'',market:'',nativeId:'',quoteAsset:''},points:[],error:'No matching asset is available in the verified provider catalog'}:
    {...series,asset:{...series.asset,exchange:'binance',market:'spot',nativeId:'BTCUSDT',quoteAsset:'USDT',feedKey:{exchange:'binance',market:'spot',instrument:'BTCUSDT',timeframe:'1d'}}});
   fixture.__testComparisonReleaseData(id,{series});
  },unavailable);
 };
 await refreshActual(false);await loaded(page);
 await expect(btc.locator('small')).toHaveText('Binance · Spot · USDT');await expect(btc).not.toContainText('Hyperliquid');
 await expect(legend).toHaveAttribute('title','Binance · Spot · USDT · BTCUSDT');await expect(legend).toContainText('+28.00%');
 expect((await calls(page)).catalog).toBe(1);
 await refreshActual(true);await loaded(page,6);
 await expect(btc.locator('small')).toHaveText('Unavailable');await expect(btc).not.toContainText('Hyperliquid');await expect(btc).not.toContainText('Binance');
 await expect(legend).toHaveCount(0);await expect(page.getByLabel('Unavailable comparison assets',{exact:true})).toContainText('No matching asset is available');
 expect((await calls(page)).catalog).toBe(1);
});

test('weeks edits request the selected period once, validate bounds and restore the saved choice',async({page})=>{
 await openTestApp(page);await enter(page);await loaded(page);
 await panel(page).getByRole('button',{name:'2w',exact:true}).click();await loaded(page);
 await expect.poll(()=>page.evaluate(()=>(window as any).__testComparisonRequests().at(-1).view.weeks)).toBe(2);
 await expect.poll(()=>page.evaluate(()=>(window as any).__testComparisonView().weeks)).toBe(2);
 expect((await page.evaluate(()=>(window as any).__testComparisonLastResult())).series[0].points).toHaveLength(15);
 const count=(await calls(page)).data;await panel(page).getByRole('button',{name:'2w',exact:true}).click();await frames(page);expect((await calls(page)).data).toBe(count);
 await weeks(page).fill('3');await weeks(page).press('Enter');await expect.poll(()=>page.evaluate(()=>(window as any).__testComparisonView().weeks)).toBe(3);
 await expect.poll(()=>page.evaluate(()=>(window as any).__testComparisonRequests().at(-1).view.weeks)).toBe(3);
 const writes=(await calls(page)).save,requests=(await calls(page)).data;
 for(const value of ['0','1.5','105']){await weeks(page).fill(value);await weeks(page).blur();await expect(panel(page).getByRole('alert')).toContainText('whole number from 1 to 104');}
 expect((await calls(page)).save).toBe(writes);expect((await calls(page)).data).toBe(requests);
 await weeks(page).fill('104');await weeks(page).press('Enter');await expect.poll(()=>page.evaluate(()=>(window as any).__testComparisonRequests().at(-1).view.weeks)).toBe(104);await loaded(page);
 expect((await page.evaluate(()=>(window as any).__testComparisonLastResult())).series[0].points).toHaveLength(729);
 await weeks(page).fill('3');await weeks(page).blur();await expect.poll(()=>page.evaluate(()=>(window as any).__testComparisonView().weeks)).toBe(3);await reloadTestApp(page);await enter(page);await loaded(page);await expect(weeks(page)).toHaveValue('3');
 expect((await page.evaluate(()=>(window as any).__testComparisonLastResult())).series[0].points).toHaveLength(22);
});

test('asset removal, search and market filters keep per-asset sources and persist an intentionally empty selection',async({page})=>{
 await openTestApp(page);await enter(page);await loaded(page);
 await page.getByRole('button',{name:'Remove ETH from comparison',exact:true}).click();await loaded(page,6);
 await add(page,'LTC');await loaded(page,7);await expect(selected(page).locator('strong')).toHaveText(['BTC','TON (GRAM)','HYPE','ZEN','APT','NEAR','LTC']);
 await page.getByRole('button',{name:'Remove ZEN from comparison',exact:true}).click();
 await page.getByRole('button',{name:'Add comparison asset',exact:true}).click();const picker=page.getByLabel('Comparison assets',{exact:true});
 await picker.getByRole('button',{name:'Spot',exact:true}).click();await picker.getByRole('textbox',{name:'Search instruments',exact:true}).fill('Horizen');
 await expect(picker.locator('.product-list > button')).toHaveCount(1);await picker.getByRole('button',{name:'ZEN Spot',exact:true}).click();
 await add(page,'SOL');await loaded(page,8);
 await expect(selected(page).locator('.comparison-asset').filter({has:page.getByText('ZEN',{exact:true})})).toContainText('Binance · Spot');
 const symbols=await page.evaluate(()=>(window as any).__testComparisonView().assets);
 for(const symbol of symbols)await page.getByRole('button',{name:`Remove ${symbol} from comparison`,exact:true}).click();
 await expect(panel(page)).toContainText('Add assets to compare');await expect.poll(()=>resources(page)).toEqual({charts:0,observers:0,listeners:0});
 await expect.poll(()=>page.evaluate(()=>(window as any).__testComparisonView().assets)).toEqual([]);
 await reloadTestApp(page);await enter(page);await expect(panel(page)).toContainText('Add assets to compare');await expect(selected(page).locator('.comparison-asset')).toHaveCount(0);expect((await calls(page)).data).toBe(0);
 await add(page,'SOL');await loaded(page,1);await expect(selected(page).locator('strong')).toHaveText(['SOL']);
});

test('normal close commits an unblurred weeks edit and waits for its durable save',async({page})=>{
 await openTestApp(page);await enter(page);await loaded(page);await page.evaluate(()=>(window as any).__testComparisonHoldSaves());
 await weeks(page).fill('6');await page.evaluate(()=>(window as any).__testEmit('prepare-close',{token:'comparison-weeks'}));
 await expect.poll(()=>calls(page)).toMatchObject({save:1});expect(await page.evaluate(()=>(window as any).__testComparisonView().weeks)).toBe(4);expect(await page.evaluate(()=>(window as any).__testCloseConfirmation())).toBeNull();
 await page.evaluate(()=>(window as any).__testComparisonReleaseSaves());await expect.poll(()=>page.evaluate(()=>(window as any).__testCloseConfirmation())).toEqual({token:'comparison-weeks',discard:false});
 expect(await page.evaluate(()=>(window as any).__testComparisonView().weeks)).toBe(6);
});

test('failed saves keep Comparison edits, navigation and shutdown guarded until retry succeeds',async({page})=>{
 await openTestApp(page);await enter(page);await loaded(page);await page.evaluate(()=>(window as any).__testComparisonErrors({save:'Comparison choices could not be saved: disk full'}));
 await panel(page).getByRole('button',{name:'8w',exact:true}).click();await expect(panel(page).getByRole('alert')).toContainText('disk full');
 await page.locator('[data-tab-id]').first().click();await expect(tab(page)).toHaveAttribute('aria-selected','true');expect(await page.evaluate(()=>(window as any).__testComparisonView().weeks)).toBe(4);
 await page.evaluate(()=>(window as any).__testEmit('prepare-close',{token:'comparison-save-error'}));await expect(page.getByText('Save failed during shutdown. Your changes remain open.',{exact:true})).toBeVisible();expect(await page.evaluate(()=>(window as any).__testCloseConfirmation())).toBeNull();
 await page.evaluate(()=>(window as any).__testComparisonErrors({}));await panel(page).getByRole('button',{name:'Retry saving',exact:true}).click();await page.getByRole('button',{name:'Retry save and close',exact:true}).click();
 await expect.poll(()=>page.evaluate(()=>(window as any).__testCloseConfirmation())).toEqual({token:'comparison-save-error',discard:false});expect(await page.evaluate(()=>(window as any).__testComparisonView().weeks)).toBe(8);
});

test('saved-choice failures never overwrite the stored selection or fetch default prices and retry remains available',async({page})=>{
 await openTestApp(page);await page.evaluate(()=>(window as any).__testComparisonErrors({view:'Saved Comparison choices are unavailable'}));
 await enter(page);await expect(panel(page).getByRole('alert')).toContainText('Saved Comparison choices are unavailable');await expect(weeks(page)).toBeDisabled();expect((await calls(page)).data).toBe(0);expect((await calls(page)).save).toBe(0);
 await page.evaluate(()=>(window as any).__testComparisonErrors({}));await panel(page).getByRole('button',{name:'Retry saved choices',exact:true}).click();await loaded(page);expect((await calls(page)).save).toBe(0);expect(await page.evaluate(()=>(window as any).__testComparisonView())).toEqual(defaultComparisonView());
});

test('catalog and data errors are retryable without inventing prices or overriding other assets',async({page})=>{
 await openTestApp(page);await page.evaluate(()=>(window as any).__testComparisonErrors({catalog:'Hyperliquid asset discovery is unavailable'}));
 await enter(page);await expect(panel(page).getByRole('alert')).toContainText('discovery is unavailable');expect((await calls(page)).data).toBe(0);await expect(chart(page)).toHaveCount(0);
 await page.evaluate(()=>(window as any).__testComparisonErrors({data:'Historical daily closes timed out'}));await panel(page).getByRole('button',{name:'Retry assets',exact:true}).click();await expect(panel(page).getByRole('alert')).toContainText('daily closes timed out');await expect.poll(()=>resources(page)).toEqual({charts:0,observers:0,listeners:0});
 await page.evaluate(()=>{(window as any).__testComparisonErrors({});(window as any).__testComparisonSeries({ETH:{missingBaseline:true},TON:{error:'Daily source is unavailable'},BTC:{gaps:[10]}});});
 await panel(page).getByRole('button',{name:'Retry daily data',exact:true}).click();await loaded(page,5);
 const unavailable=page.getByLabel('Unavailable comparison assets',{exact:true});await expect(unavailable).toContainText('ETH');await expect(unavailable).toContainText('common UTC baseline');await expect(unavailable).toContainText('not rebased');await expect(unavailable).toContainText('TON');await expect(unavailable).toContainText('Daily source is unavailable');
 const result=await page.evaluate(()=>(window as any).__testComparisonLastResult()),btc=result.series.find((series:any)=>series.asset.symbol==='BTC');
 expect(btc.points[0]).toMatchObject({time:result.baselineTime,pct:0});expect(btc.points[10]).toMatchObject({close:null,pct:null});expect(btc.points[11].pct).toBe(11);expect(btc.points.at(-1).pct).toBe(28);
 expect(result.series.find((series:any)=>series.asset.symbol==='ETH').points).toEqual([]);
 await expect(page.getByLabel('Comparison legend',{exact:true})).not.toContainText('ETH');await expect(page.getByLabel('Comparison legend',{exact:true})).not.toContainText('TON');
 const rendered=await page.evaluate(()=>(window as any).__testComparisonChart()),line=rendered.series.find((series:any)=>series.symbol==='BTC');
 const at=(data:any[],offset:number)=>data.find(point=>point.time===btc.points[offset].time);
 expect(at(line.line,9)).toMatchObject({value:9,color:'rgba(0, 0, 0, 0)'});expect(at(line.line,10)?.value).toBeUndefined();expect(at(line.line,11)).toMatchObject({value:11});expect(at(line.dots,9)).toMatchObject({value:9});expect(at(line.dots,10)?.value).toBeUndefined();
 const gap=await page.evaluate(time=>(window as any).__testComparisonChart(time),btc.points[10].time),bounds=(await chart(page).boundingBox())!;
 await page.mouse.move(bounds.x+gap.leftWidth+gap.x,bounds.y+200);await expect(panel(page).locator('.comparison-readout')).toContainText(comparisonDate(btc.points[10].time));
 await expect(page.getByLabel('Comparison legend',{exact:true}).getByRole('button').filter({has:page.getByText('BTC',{exact:true})})).toContainText('—');await page.mouse.move(0,0);
});

test('late daily results cannot replace a new period, hiding cancels work and releases every chart resource',async({page})=>{
 await openTestApp(page);await page.evaluate(()=>(window as any).__testComparisonHoldNextData());await enter(page);
 await expect.poll(()=>page.evaluate(()=>(window as any).__testComparisonPending().length)).toBe(1);const old=await page.evaluate(()=>(window as any).__testComparisonPending()[0]);
 await panel(page).getByRole('button',{name:'2w',exact:true}).click();await loaded(page);const baseline=await chart(page).getAttribute('data-baseline-time');
 await expect.poll(()=>page.evaluate(()=>(window as any).__testComparisonCancelled())).toContain(old);
 await page.evaluate(id=>(window as any).__testComparisonReleaseData(id),old);await frames(page);await expect(chart(page)).toHaveAttribute('data-baseline-time',baseline!);await expect(weeks(page)).toHaveValue('2');
 await page.evaluate(()=>(window as any).__testComparisonHoldNextData());await panel(page).getByRole('button',{name:'Refresh',exact:true}).click();await expect.poll(()=>page.evaluate(()=>(window as any).__testComparisonPending().length)).toBe(1);const hidden=await page.evaluate(()=>(window as any).__testComparisonPending()[0]);
 await page.getByRole('tab',{name:'Screener',exact:true}).click();await expect.poll(()=>resources(page)).toEqual({charts:0,observers:0,listeners:0});await expect.poll(()=>page.evaluate(()=>(window as any).__testComparisonCancelled())).toContain(hidden);
 const count=(await calls(page)).data;await page.evaluate(id=>(window as any).__testComparisonReleaseData(id),hidden);await frames(page);expect((await calls(page)).data).toBe(count);await expect(chart(page)).toHaveCount(0);
 await enter(page);await loaded(page);await expect(weeks(page)).toHaveValue('2');
});

test('a saved-choice response arriving after hiding cannot restore an older selection',async({page})=>{
 await openTestApp(page);await page.evaluate(()=>(window as any).__testComparisonHoldView());await enter(page);await expect.poll(()=>calls(page)).toMatchObject({view:1});await page.getByRole('tab',{name:'Screener',exact:true}).click();
 await page.evaluate(async()=>{await window.go!.main!.App!.SaveComparisonView({weeks:3,assets:['BTC','SOL']});(window as any).__testComparisonReleaseView();});await frames(page);expect((await calls(page)).data).toBe(0);
 await enter(page);await loaded(page,2);await expect(weeks(page)).toHaveValue('3');await expect(selected(page).locator('strong')).toHaveText(['BTC','SOL']);
});

test('Comparison accepts twenty saved assets, disables additions at the limit and keeps the next selection bounded',async({page})=>{
 const symbols=Array.from({length:20},(_,index)=>`ASSET${index}`);
 await page.addInitScript(symbols=>sessionStorage.setItem('cryptodesk-explicit-test-comparison-view',JSON.stringify({weeks:1,assets:symbols})),symbols);await openTestApp(page);
 await page.evaluate(symbols=>{const assets=[...symbols,'SOL'].map(symbol=>({symbol,name:symbol,exchange:'hyperliquid',market:'perpetual',nativeId:symbol,quoteAsset:'USDC'}));(window as any).__testComparisonCatalog({assets,warnings:[]});},symbols);
 await enter(page);await loaded(page,20);await expect(page.getByRole('button',{name:'Add comparison asset',exact:true})).toBeDisabled();await expect(selected(page).locator('.comparison-asset')).toHaveCount(20);
 await page.getByRole('button',{name:'Remove ASSET0 from comparison',exact:true}).click();await expect(page.getByRole('button',{name:'Add comparison asset',exact:true})).toBeEnabled();await add(page,'SOL');await loaded(page,20);
 await expect.poll(()=>page.evaluate(()=>(window as any).__testComparisonView().assets.length)).toBe(20);await expect(page.getByRole('button',{name:'Add comparison asset',exact:true})).toBeDisabled();
});

test('legend toggles hide lines and markers without reloading or resetting zoom, with mouse and keyboard',async({page},testInfo)=>{
 await openTestApp(page);await enter(page);await loaded(page);await panel(page).locator('.comparison-chart').scrollIntoViewIfNeeded();await frames(page);
 const legend=page.getByLabel('Comparison legend',{exact:true}),btc=legend.getByRole('button',{name:'BTC',exact:true}),eth=legend.getByRole('button',{name:'ETH',exact:true});
 const result=await page.evaluate(()=>(window as any).__testComparisonLastResult()),time=result.baselineTime+14*86400;
 const read=()=>page.evaluate(time=>(window as any).__testComparisonChart(time),time);
 const visibility=async()=>Object.fromEntries((await read()).series.map((series:any)=>[series.symbol,[series.visible,series.dotsVisible]]));
 const beforeCalls=await calls(page),beforeView=await page.evaluate(()=>(window as any).__testComparisonView()),canvas=await chart(page).locator('canvas').first().elementHandle();
 const initial=(await read()).logicalRange,bounds=(await chart(page).boundingBox())!;
 await page.mouse.move(bounds.x+bounds.width/2,bounds.y+200);await page.mouse.wheel(0,-180);
 await expect.poll(async()=>{const range=(await read()).logicalRange;return range.to-range.from;}).toBeLessThan(initial.to-initial.from);
 await page.mouse.move(0,0);await frames(page);const zoomed=(await read()).logicalRange;
 const colors=async(button:typeof btc)=>button.evaluate(element=>({text:getComputedStyle(element).color,label:getComputedStyle(element.querySelector('strong')!).color,value:getComputedStyle(element.querySelector('span')!).color,dot:getComputedStyle(element.querySelector('i')!).backgroundColor}));
 const shownBTC=await colors(btc),shownETH=await colors(eth);
 await btc.click();await expect(btc).toHaveAttribute('aria-pressed','false');
 await expect.poll(visibility).toEqual(Object.fromEntries(defaults.map(symbol=>[symbol,[symbol!=='BTC',symbol!=='BTC']])));
 await expect.poll(async()=>(await read()).referenceSymbol).toBe('ETH');
 const hiddenBTC=await colors(btc);expect(new Set(Object.values(hiddenBTC)).size).toBe(1);expect(hiddenBTC.text).not.toBe(shownBTC.text);expect(hiddenBTC.dot).not.toBe(shownBTC.dot);
 const hiddenRange=(await read()).logicalRange;expect(hiddenRange.from).toBeCloseTo(zoomed.from,5);expect(hiddenRange.to).toBeCloseTo(zoomed.to,5);
 expect(await canvas!.evaluate(element=>element.isConnected)).toBe(true);expect(await resources(page)).toEqual({charts:1,observers:2,listeners:1});
 await panel(page).locator('.comparison-chart').scrollIntoViewIfNeeded();await frames(page);
 const hidden=await read(),hiddenBounds=(await chart(page).boundingBox())!,hiddenLine=hidden.series.find((series:any)=>series.symbol==='BTC');
 expect(Number.isFinite(hidden.x)).toBe(true);expect(Number.isFinite(hiddenLine.y)).toBe(true);
 await page.mouse.move(hiddenBounds.x+hidden.leftWidth+hidden.x,hiddenBounds.y+hiddenLine.y);
 await expect(panel(page).getByRole('tooltip')).toHaveCount(0);expect((await read()).hoveredSymbol).toBeNull();expect((await read()).series.find((series:any)=>series.symbol==='BTC').lineWidth).toBe(2);
 await eth.focus();await eth.press('Space');await expect(eth).toHaveAttribute('aria-pressed','false');
 const hiddenETH=await colors(eth);expect(new Set(Object.values(hiddenETH)).size).toBe(1);expect(hiddenETH.value).not.toBe(shownETH.value);
 await page.evaluate(()=>{document.documentElement.dataset.theme='dark';});await frames(page);
 for(const button of [btc,eth])expect(new Set(Object.values(await colors(button))).size).toBe(1);
 await panel(page).locator('.comparison-chart').screenshot({path:testInfo.outputPath('comparison-hidden-btc-eth.png')});
 await eth.press('Enter');await expect(eth).toHaveAttribute('aria-pressed','true');await btc.focus();await btc.press('Space');await expect(btc).toHaveAttribute('aria-pressed','true');
 await expect.poll(visibility).toEqual(Object.fromEntries(defaults.map(symbol=>[symbol,[true,true]])));await expect.poll(async()=>(await read()).referenceSymbol).toBe('BTC');
 const restored=(await read()).logicalRange;expect(restored.from).toBeCloseTo(zoomed.from,5);expect(restored.to).toBeCloseTo(zoomed.to,5);
 await panel(page).locator('.comparison-chart').scrollIntoViewIfNeeded();await frames(page);const visible=await read(),visibleBounds=(await chart(page).boundingBox())!,visibleBTC=visible.series.find((series:any)=>series.symbol==='BTC');
 await page.mouse.move(visibleBounds.x+visible.leftWidth+visible.x,visibleBounds.y+visibleBTC.y);await expect(panel(page).getByRole('tooltip')).toContainText('Bitcoin');
 await expect(selected(page).locator('strong')).toHaveText(defaults.map(symbol=>symbol==='TON'?'TON (GRAM)':symbol));
 expect(await page.evaluate(()=>(window as any).__testComparisonView())).toEqual(beforeView);expect(await calls(page)).toEqual(beforeCalls);expect(await canvas!.evaluate(element=>element.isConnected)).toBe(true);
});

test('all legend assets may be hidden and canonical TON visibility survives refresh and navigation',async({page},testInfo)=>{
 await openTestApp(page);await enter(page);await loaded(page);await frames(page);
 const legend=page.getByLabel('Comparison legend',{exact:true}),ton=legend.getByRole('button',{name:'TON (GRAM)',exact:true});
 const read=()=>page.evaluate(()=>(window as any).__testComparisonChart());
 const visibility=async()=>Object.fromEntries((await read()).series.map((series:any)=>[series.symbol,[series.visible,series.dotsVisible]]));
 const beforeCalls=await calls(page),beforeView=await page.evaluate(()=>(window as any).__testComparisonView()),canvas=await chart(page).locator('canvas').first().elementHandle();
 for(const symbol of defaults)await legend.getByRole('button',{name:symbol==='TON'?'TON (GRAM)':symbol,exact:true}).click();
 await expect(legend.getByRole('button',{pressed:false})).toHaveCount(7);await expect.poll(visibility).toEqual(Object.fromEntries(defaults.map(symbol=>[symbol,[false,false]])));
 await expect.poll(async()=>(await read()).referenceSymbol).toBeNull();expect(await canvas!.evaluate(element=>element.isConnected)).toBe(true);expect(await resources(page)).toEqual({charts:1,observers:2,listeners:1});
 await expect(panel(page).getByRole('alert')).toHaveCount(0);await panel(page).locator('.comparison-chart').scrollIntoViewIfNeeded();await frames(page);const bounds=(await chart(page).boundingBox())!;
 await page.mouse.move(bounds.x+bounds.width/2,bounds.y+200);await expect(panel(page).getByRole('tooltip')).toHaveCount(0);expect((await read()).hoveredSymbol).toBeNull();
 await ton.focus();await ton.press('Enter');await expect(ton).toHaveAttribute('aria-pressed','true');
 const onlyTON=Object.fromEntries(defaults.map(symbol=>[symbol,[symbol==='TON',symbol==='TON']]));await expect.poll(visibility).toEqual(onlyTON);await expect.poll(async()=>(await read()).referenceSymbol).toBe('TON');
 const result=await page.evaluate(()=>(window as any).__testComparisonLastResult()),time=result.baselineTime+14*86400;
 await panel(page).locator('.comparison-chart').scrollIntoViewIfNeeded();await frames(page);const rendered=await page.evaluate(time=>(window as any).__testComparisonChart(time),time),tonBounds=(await chart(page).boundingBox())!,line=rendered.series.find((series:any)=>series.symbol==='TON');
 expect(Number.isFinite(line.y)).toBe(true);await page.mouse.move(tonBounds.x+rendered.leftWidth+rendered.x,tonBounds.y+line.y);
 await expect(panel(page).getByRole('tooltip')).toContainText('TON (GRAM)');await expect(panel(page).getByRole('tooltip')).toContainText('Gram (formerly Toncoin)');
 await panel(page).locator('.comparison-chart').screenshot({path:testInfo.outputPath('comparison-only-ton-gram.png')});
 expect(await calls(page)).toEqual(beforeCalls);expect(await page.evaluate(()=>(window as any).__testComparisonView())).toEqual(beforeView);
 await panel(page).getByRole('button',{name:'Refresh',exact:true}).click();await expect.poll(async()=>(await calls(page)).data).toBe(beforeCalls.data+1);await loaded(page);
 await expect.poll(visibility).toEqual(onlyTON);await expect(legend.getByRole('button',{pressed:false})).toHaveCount(6);await expect(ton).toHaveAttribute('aria-pressed','true');await expect.poll(async()=>(await read()).referenceSymbol).toBe('TON');
 expect((await calls(page)).save).toBe(beforeCalls.save);expect(await page.evaluate(()=>(window as any).__testComparisonView())).toEqual(beforeView);
 await page.getByRole('tab',{name:'Screener',exact:true}).click();await expect.poll(()=>resources(page)).toEqual({charts:0,observers:0,listeners:0});
 await enter(page);await loaded(page);await expect.poll(visibility).toEqual(onlyTON);await expect(legend.getByRole('button',{pressed:false})).toHaveCount(6);await expect(ton).toHaveAttribute('aria-pressed','true');await expect.poll(async()=>(await read()).referenceSymbol).toBe('TON');
 expect((await calls(page)).save).toBe(beforeCalls.save);expect(await page.evaluate(()=>(window as any).__testComparisonView())).toEqual(beforeView);await expect(selected(page).locator('strong')).toHaveText(defaults.map(symbol=>symbol==='TON'?'TON (GRAM)':symbol));
});

test('canvas line hover reveals the asset name and highlights only real observations, then clears on leave and hide',async({page},testInfo)=>{
 await openTestApp(page);await enter(page);await loaded(page);await panel(page).locator('.comparison-chart').scrollIntoViewIfNeeded();await frames(page);
 const result=await page.evaluate(()=>(window as any).__testComparisonLastResult()),time=result.baselineTime+14*86400;
 const read=()=>page.evaluate(time=>(window as any).__testComparisonChart(time),time);
 const widths=async()=>Object.fromEntries((await read()).series.map((series:any)=>[series.symbol,series.lineWidth]));
 const original=await widths(),tooltip=panel(page).getByRole('tooltip');
 const canvas=await chart(page).locator('canvas').first().elementHandle();
 const viewport=()=>page.evaluate(times=>times.map(time=>(window as any).__testComparisonChart(time).x),[result.baselineTime,result.lastClosedTime]);
 const initial=await viewport(),bounds=(await chart(page).boundingBox())!;
 await page.mouse.move(bounds.x+bounds.width/2,bounds.y+200);await page.mouse.wheel(0,-180);
 await expect.poll(async()=>{const positions=await viewport();return positions[1]-positions[0];}).toBeGreaterThan(initial[1]-initial[0]);
 await page.mouse.move(0,0);await expect.poll(widths).toEqual(original);const zoomed=await viewport();
 const moveTo=async(symbol:string,value?:number,betweenObservations=false)=>{
  await frames(page);const rendered=await read(),bounds=(await chart(page).boundingBox())!;
  let x=rendered.x,y=rendered.series.find((series:any)=>series.symbol===symbol)?.y;
  if(value!==undefined){
   const anchors=rendered.series.map((series:any)=>({y:series.y,value:series.line.find((point:any)=>Number(point.time)===time)?.value})).filter((point:any)=>Number.isFinite(point.y)&&Number.isFinite(point.value));
   const first=anchors[0],second=anchors.find((point:any)=>point.value!==first.value);
   expect(first).toBeDefined();expect(second).toBeDefined();y=first.y+(value-first.value)*(second.y-first.y)/(second.value-first.value);
  }
  if(betweenObservations){const next=await page.evaluate(time=>(window as any).__testComparisonChart(time),time+86400);x=(x+next.x)/2;y=(y+next.series.find((series:any)=>series.symbol===symbol).y)/2;}
  expect(Number.isFinite(x)).toBe(true);expect(Number.isFinite(y)).toBe(true);
  await page.mouse.move(bounds.x+rendered.leftWidth+x,bounds.y+y);
 };
 await moveTo('BTC',undefined,true);await expect(tooltip).toContainText('Bitcoin');await expect(tooltip).toContainText('BTC');
 await expect.poll(async()=>(await widths()).BTC).toBeGreaterThan(original.BTC);
 expect(Object.entries(await widths()).filter(([symbol])=>symbol!=='BTC').every(([symbol,width])=>width===original[symbol])).toBe(true);
 expect(await canvas!.evaluate(element=>element.isConnected)).toBe(true);
 const highlighted=await viewport();expect(highlighted[0]).toBeCloseTo(zoomed[0],5);expect(highlighted[1]).toBeCloseTo(zoomed[1],5);
 await panel(page).locator('.comparison-chart').screenshot({path:testInfo.outputPath('comparison-hover-bitcoin.png')});
 await moveTo('ETH');await expect(tooltip).toContainText('Ethereum');await expect(tooltip).not.toContainText('Bitcoin');
 await expect.poll(async()=>(await widths()).ETH).toBeGreaterThan(original.ETH);expect((await widths()).BTC).toBe(original.BTC);
 const size=page.viewportSize()!;await page.setViewportSize({width:size.width+1,height:size.height});
 await expect(tooltip).toHaveCount(0);await expect.poll(widths).toEqual(original);
 await page.mouse.move(0,0);await expect(tooltip).toHaveCount(0);await expect.poll(widths).toEqual(original);
 await page.evaluate(()=>(window as any).__testComparisonSeries({BTC:{gaps:[14]},ETH:{error:'Daily source is unavailable'}}));
 await panel(page).getByRole('button',{name:'Refresh',exact:true}).click();await loaded(page,6);await panel(page).locator('.comparison-chart').scrollIntoViewIfNeeded();await frames(page);
 const remaining=await widths();expect(remaining.ETH).toBeUndefined();
 const previous=(symbol:string)=>result.series.find((series:any)=>series.asset.symbol===symbol).points[14].pct;
 await moveTo('BTC',previous('BTC'));await expect(tooltip).toHaveCount(0);await expect.poll(widths).toEqual(remaining);
 await moveTo('ETH',previous('ETH'));await expect(tooltip).toHaveCount(0);await expect.poll(widths).toEqual(remaining);
 await moveTo('HYPE');await expect(tooltip).toContainText('Hyperliquid');await expect.poll(async()=>(await widths()).HYPE).toBeGreaterThan(remaining.HYPE);
 await page.getByRole('tab',{name:'Screener',exact:true}).click();await expect.poll(()=>resources(page)).toEqual({charts:0,observers:0,listeners:0});
 await expect(tooltip).toHaveCount(0);expect(await read()).toBeNull();const hidden=(await calls(page)).data;
 await page.mouse.move(500,500);await frames(page);expect((await calls(page)).data).toBe(hidden);
 await enter(page);await loaded(page,6);await expect(tooltip).toHaveCount(0);await expect.poll(widths).toEqual(remaining);
});

test('Comparison redraws theme in place, applies font changes and fits all controls at 900px',async({page},testInfo)=>{
 await page.setViewportSize({width:900,height:1050});await openTestApp(page);await enter(page);await loaded(page);
 const canvas=chart(page).locator('canvas').first(),handle=await canvas.elementHandle(),before=(await calls(page)).data;
 const background=()=>canvas.evaluate((element:HTMLCanvasElement)=>{const context=element.getContext('2d')!,data=context.getImageData(0,0,element.width,element.height).data,counts=new Map<string,number>();for(let i=0;i<data.length;i+=4)if(data[i+3]===255){const color='#'+[data[i],data[i+1],data[i+2]].map(value=>value.toString(16).padStart(2,'0')).join('');counts.set(color,(counts.get(color)??0)+1);}return[...counts].sort((a,b)=>b[1]-a[1])[0]?.[0];});
 for(const theme of THEME_OPTIONS){
  await page.evaluate(theme=>{document.documentElement.dataset.theme=theme;},theme.id);await expect.poll(background).toBe(getThemePalette(theme.id).chartBackground);expect(await handle!.evaluate(element=>element.isConnected)).toBe(true);expect((await calls(page)).data).toBe(before);
  await panel(page).locator('.comparison-chart').scrollIntoViewIfNeeded();await expect(page.getByLabel('Comparison legend',{exact:true})).toBeInViewport({ratio:1});await panel(page).locator('.comparison-chart').screenshot({path:testInfo.outputPath(`comparison-${theme.id}-900.png`)});
 }
 const bounds=await chart(page).boundingBox();expect(bounds).not.toBeNull();expect(bounds!.width).toBeLessThanOrEqual(900);expect(await page.evaluate(()=>document.documentElement.scrollWidth)).toBeLessThanOrEqual(900);
 await page.getByRole('button',{name:'Open settings',exact:true}).click();const settings=page.getByRole('dialog',{name:'Workspace settings',exact:true});await expect.poll(()=>resources(page)).toEqual({charts:0,observers:0,listeners:0});const hidden=(await calls(page)).data;
 await settings.getByLabel('Font size',{exact:true}).selectOption('20');await frames(page);expect((await calls(page)).data).toBe(hidden);await settings.getByRole('button',{name:'Close settings',exact:true}).click();await loaded(page);await expect.poll(()=>page.evaluate(()=>getComputedStyle(document.documentElement).fontSize)).toBe('20px');
 await page.locator('[data-tab-id]').first().click();await expect.poll(()=>resources(page)).toEqual({charts:0,observers:0,listeners:0});
});

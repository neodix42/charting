import {test,expect,type Page} from '@playwright/test';
import {openTestApp} from './helpers/testApp';

const files=[
 {id:'assets-btc-d1',name:'btcusd-d1.csv',symbol:'btcusd',timeframe:'d1'},
 {id:'assets-eth-d1',name:'ethusd-d1.csv',symbol:'ethusd',timeframe:'d1'},
].map(file=>({...file,fields:['timestamp','open','high','low','close'],bytes:1024}));

async function openAssetsApp(page:Page,allMatched=false){
 await openTestApp(page);
 await expect(page.getByLabel('OHLCV')).toBeVisible();
 await page.evaluate(async({fixtures,allMatched})=>{
  (window as any).__testSetEnrichmentFiles(fixtures);
  const api=window.go!.main!.App!,view={...await api.GetScreenerAnalysisView(),period:'d1',profitabilityFilter:0};
  await api.StartScreenerAnalysis(view);
  (window as any).__testSetAnalysisInfo({active:false,phase:'completed',failed:0,results:(allMatched?fixtures:[fixtures[0]]).map(file=>({...file,id:`matched-${file.id}`,fileId:file.id,status:'completed',rows:100,matches:2}))});
  await api.SaveScreenerAnalysisCondition('Assets rule',view);
  const original=api.ListInstruments.bind(api);(window as any).__assetCatalogCalls=[];
  api.ListInstruments=async source=>{(window as any).__assetCatalogCalls.push(source);return original(source);};
 },{fixtures:files,allMatched});
 await page.getByRole('tab',{name:'Screener',exact:true}).click();
 await page.getByRole('tab',{name:'Live Alerts',exact:true}).click();
 await expect(page.getByRole('button',{name:'Add alert',exact:true})).toBeEnabled();
}

async function addDisabled(page:Page,name:string,source='hyperliquid'){
 // Legacy public-source rows remain inspectable; these sources are no longer
 // offered by Add/Edit. Seed an existing persisted row instead of creating it.
 if(source==='binance'||source==='bybit'){
  await page.evaluate(async({name,source})=>{const condition=(await window.go!.main!.App!.GetScreenerSavedConditions()).find(condition=>condition.name==='Assets rule')!,state=(window as any).__testLiveAlertsInfo();(window as any).__testSetLiveAlertsInfo({config:{...state.config,alerts:[...(state.config.alerts??[]),{id:crypto.randomUUID().replaceAll('-',''),name,conditionId:condition.id,period:'d1',dataSource:source,enabled:false}]}});},{name,source});
  await expect(page.getByRole('checkbox',{name:`Enable alert ${name}`,exact:true})).not.toBeChecked();return;
 }
 await page.getByRole('button',{name:'Add alert',exact:true}).click();
 const dialog=page.getByRole('dialog',{name:'Add alert',exact:true});
 await dialog.getByRole('textbox',{name:'Alert name',exact:true}).fill(name);
 await dialog.getByRole('combobox',{name:'Saved condition',exact:true}).selectOption({label:'Assets rule'});
 await dialog.getByRole('combobox',{name:'Alert period',exact:true}).selectOption('d1');
 await dialog.getByRole('combobox',{name:'Alert data source',exact:true}).selectOption(source);
 await dialog.getByRole('button',{name:'Add alert',exact:true}).click();
 await expect(dialog).toHaveCount(0);
 await page.getByRole('checkbox',{name:`Enable alert ${name}`,exact:true}).uncheck();
 await expect.poll(()=>page.evaluate(name=>(window as any).__testLiveAlertsInfo().config.alerts?.find((entry:any)=>entry.name===name)?.enabled,name)).toBe(false);
}

const trigger=(page:Page,name:string)=>page.getByRole('button',{name:`View assets for ${name}`,exact:true});
const modal=(page:Page,name:string)=>page.getByRole('dialog',{name:`Monitored assets for ${name}`,exact:true});
const rows=(page:Page,name:string)=>modal(page,name).locator('.live-alert-assets-list li');
const calls=(page:Page)=>page.evaluate(()=>((window as any).__assetCatalogCalls??[]) as string[]);
const writes=(page:Page)=>page.evaluate(()=>(window as any).__testLiveAlertsWrites().length as number);

test('disabled public rows discover catalogs only when Assets opens and viewing never saves or fetches history',async({page})=>{
 await openAssetsApp(page);
 await addDisabled(page,'Paused Hyperliquid','hyperliquid');
 await addDisabled(page,'Paused Binance','binance');
 await addDisabled(page,'Paused Bybit','bybit');
 const beforeCalls=await calls(page),beforeWrites=await writes(page),beforeHistory=await page.evaluate(()=>(window as any).__testHistoryKeys().length),beforeReads=await page.evaluate(()=>(window as any).__testLiveAlertsReads());
 await expect.poll(()=>page.evaluate(()=>(window as any).__testLiveAlertsReads())).toBeGreaterThan(beforeReads);
 expect(await calls(page)).toEqual(beforeCalls);
 await trigger(page,'Paused Hyperliquid').click();
 await expect(rows(page,'Paused Hyperliquid')).toHaveCount(4);
 await expect(modal(page,'Paused Hyperliquid')).toContainText('Disabled alert');
 await expect(modal(page,'Paused Hyperliquid')).toContainText('Opening this list downloads no history');
 expect((await calls(page)).slice(beforeCalls.length)).toEqual(['hyperliquid']);
 await modal(page,'Paused Hyperliquid').getByRole('button',{name:'Close monitored assets',exact:true}).click();
 await trigger(page,'Paused Hyperliquid').click();
 await expect(rows(page,'Paused Hyperliquid')).toHaveCount(4);
 expect((await calls(page)).slice(beforeCalls.length)).toEqual(['hyperliquid']);
 expect(await writes(page)).toBe(beforeWrites);
 expect(await page.evaluate(()=>(window as any).__testHistoryKeys().length)).toBe(beforeHistory);
 expect(await page.evaluate(()=>(window as any).__testLiveAlertsInfo().config.enabled)).toBe(false);
});

test('public assets match the monitor availability filter and preserve distinct spot and perpetual native identities',async({page})=>{
 await openAssetsApp(page);await addDisabled(page,'Bybit exact universe','bybit');
 await page.evaluate(()=>{
  const api=window.go!.main!.App!,original=api.ListInstruments.bind(api);
  api.ListInstruments=async source=>{
   if(source!=='bybit')return original(source);
   (window as any).__assetCatalogCalls.push(source);
   const item={nativeId:'BTCUSDT',market:'spot' as const,baseAsset:'BTC',quoteAsset:'USDT',priceIncrement:'0.01',sizeIncrement:'0.1',availability:'available',supportedIntervals:['1h' as const]};
   return [item,{...item,availability:'Trading'},{...item,market:'perpetual' as const,availability:'ONLINE'},{...item,nativeId:'XAGUSDT',market:'perpetual' as const,baseAsset:'XAG',availability:'TRADING'},{...item,nativeId:'币安人生USDT',market:'perpetual' as const,baseAsset:'币安人生',availability:'Trading'},{...item,nativeId:'OLDUSDT',availability:'disabled'},{...item,nativeId:'HALTEDUSDT',availability:'halted'},{...item,nativeId:'CFD',market:'cfd' as const}];
  };
 });
 await trigger(page,'Bybit exact universe').click();
 await expect(rows(page,'Bybit exact universe')).toHaveCount(4);
 await expect(rows(page,'Bybit exact universe').filter({hasText:'BTCUSDT'})).toHaveCount(2);
 await expect(rows(page,'Bybit exact universe').filter({hasText:'BTCUSDT'}).locator('.product-market')).toHaveText(['Spot','Perpetual']);
 await expect(modal(page,'Bybit exact universe')).not.toContainText('OLDUSDT');
 await expect(modal(page,'Bybit exact universe')).not.toContainText('HALTEDUSDT');
 await expect(modal(page,'Bybit exact universe')).not.toContainText('CFD');
 const search=modal(page,'Bybit exact universe').getByRole('searchbox',{name:'Filter monitored assets',exact:true});
 await search.fill('币安人生');await expect(rows(page,'Bybit exact universe')).toHaveCount(1);
 await expect(rows(page,'Bybit exact universe')).toContainText('币安人生USDT');
 await search.fill('no such asset');await expect(rows(page,'Bybit exact universe')).toHaveCount(0);await expect(modal(page,'Bybit exact universe')).toContainText('No matching assets.');
});

test('Hyperliquid spot aliases and builder perpetual names remain readable and searchable without changing native case',async({page})=>{
 await openAssetsApp(page);await addDisabled(page,'Native Hyperliquid','hyperliquid');
 await trigger(page,'Native Hyperliquid').click();await expect(rows(page,'Native Hyperliquid')).toHaveCount(4);
 await expect(rows(page,'Native Hyperliquid').filter({hasText:'@107'})).toContainText('HYPE/USDC');
 await expect(rows(page,'Native Hyperliquid').filter({hasText:'PURR/USDC'})).toHaveCount(1);
 await expect(rows(page,'Native Hyperliquid').filter({hasText:'xyz:XYZ100'})).toContainText('Perpetual');
 const search=modal(page,'Native Hyperliquid').getByRole('searchbox',{name:'Filter monitored assets',exact:true});
 await search.fill('XYZ:');await expect(rows(page,'Native Hyperliquid')).toHaveCount(1);await expect(rows(page,'Native Hyperliquid')).toContainText('xyz:XYZ100');
 await search.fill('hype');await expect(rows(page,'Native Hyperliquid')).toHaveCount(1);await expect(rows(page,'Native Hyperliquid')).toContainText('@107');
});

test('catalog failures and partial discovery remain visible and retry into the complete asset list',async({page})=>{
 await openAssetsApp(page);await addDisabled(page,'Retry public assets','hyperliquid');
 await page.evaluate(()=>{
  const api=window.go!.main!.App!,original=api.ListInstruments.bind(api);let attempt=0;
  api.ListInstruments=async source=>{
   if(source!=='hyperliquid')return original(source);
   if(++attempt===1){(window as any).__assetCatalogCalls.push(source);throw Error('Explicit public catalog unavailable');}
   const items=await original(source);
   return attempt===2?[{...items[0],providerMetadata:{...items[0].providerMetadata,catalogWarning:'Explicit builder catalog restricted; spot remains available'}}]:items;
  };
 });
 const beforeWrites=await writes(page);
 await trigger(page,'Retry public assets').click();
 const dialog=modal(page,'Retry public assets');
 await expect(dialog.getByRole('alert')).toContainText('Explicit public catalog unavailable');
 await expect(rows(page,'Retry public assets')).toHaveCount(0);
 await dialog.getByRole('button',{name:'Retry assets',exact:true}).click();
 await expect(dialog.getByRole('alert')).toContainText('Explicit builder catalog restricted');
 await expect(rows(page,'Retry public assets')).toHaveCount(1);
 await dialog.getByRole('button',{name:'Retry assets',exact:true}).click();
 await expect(rows(page,'Retry public assets')).toHaveCount(4);
 await expect(dialog.getByRole('alert')).toHaveCount(0);
 expect((await calls(page)).filter(source=>source==='hyperliquid')).toHaveLength(3);
 expect(await writes(page)).toBe(beforeWrites);
});

test('asset dialogs trap keyboard focus, restore their trigger and ignore delayed catalogs after close or navigation',async({page})=>{
 await openAssetsApp(page);await addDisabled(page,'Delayed Hyperliquid','hyperliquid');await addDisabled(page,'Ready Bybit','bybit');
 await page.evaluate(()=>{
  const api=window.go!.main!.App!,original=api.ListInstruments.bind(api);
  api.ListInstruments=source=>source==='hyperliquid'?new Promise(resolve=>{(window as any).__assetCatalogCalls.push(source);(window as any).__releaseAssetCatalog=async()=>resolve(await original(source));}):original(source);
 });
 const origin=trigger(page,'Delayed Hyperliquid');
 await origin.focus();await page.keyboard.press('Enter');
 const delayed=modal(page,'Delayed Hyperliquid'),close=delayed.getByRole('button',{name:'Close monitored assets',exact:true});
 await expect(delayed.getByRole('status')).toContainText('Loading Hyperliquid assets');
 await expect(close).toBeFocused();await page.keyboard.press('Tab');await expect(close).toBeFocused();await page.keyboard.press('Shift+Tab');await expect(close).toBeFocused();
 await page.keyboard.press('Escape');await expect(delayed).toHaveCount(0);await expect(origin).toBeFocused();
 await trigger(page,'Ready Bybit').click();await expect(rows(page,'Ready Bybit')).toHaveCount(3);
 await page.evaluate(()=>(window as any).__releaseAssetCatalog());
 await expect(modal(page,'Ready Bybit')).toBeVisible();await expect(rows(page,'Ready Bybit')).toHaveCount(3);await expect(modal(page,'Ready Bybit')).not.toContainText('HYPE/USDC');
 await modal(page,'Ready Bybit').getByRole('button',{name:'Close monitored assets',exact:true}).click();
 await origin.click();await expect(rows(page,'Delayed Hyperliquid')).toHaveCount(4);
 const lastAction=delayed.getByRole('button',{name:'Cancel',exact:true});
 await close.focus();await page.keyboard.press('Shift+Tab');await expect(lastAction).toBeFocused();await page.keyboard.press('Tab');await expect(close).toBeFocused();
 await page.locator('.live-alert-assets-backdrop').click({position:{x:5,y:5}});await expect(delayed).toHaveCount(0);await expect(origin).toBeFocused();
 await page.evaluate(async()=>{const modulePath='/src/bridge/instruments.ts';const {clearInstrumentCatalog}=await import(modulePath);clearInstrumentCatalog('hyperliquid');});
 await origin.click();await expect(delayed.getByRole('status')).toContainText('Loading Hyperliquid assets');
 // A tool navigation can also arrive externally while the modal is open.
 await page.getByRole('tab',{name:'Analysis',exact:true}).evaluate((element:HTMLElement)=>element.click());
 await expect(delayed).toHaveCount(0);
 await page.evaluate(()=>(window as any).__releaseAssetCatalog());
 await expect(page.getByRole('dialog',{name:/Monitored assets for/})).toHaveCount(0);
 await page.getByRole('tab',{name:'Live Alerts',exact:true}).click();await expect(origin).toBeVisible();await expect(page.getByRole('dialog',{name:/Monitored assets for/})).toHaveCount(0);
});

test('a disabled Dukascopy alert lists only its analyzed matching targets without provider discovery',async({page})=>{
 await openAssetsApp(page);await addDisabled(page,'Paused analyzed Dukascopy','dukascopy');
 const beforeCalls=await calls(page),beforeWrites=await writes(page);
 await trigger(page,'Paused analyzed Dukascopy').click();
 const dialog=modal(page,'Paused analyzed Dukascopy');
 await expect(dialog).toContainText('Disabled alert');
 await expect(rows(page,'Paused analyzed Dukascopy')).toHaveCount(1);
 await expect(rows(page,'Paused analyzed Dukascopy')).toContainText('BTCUSD');
 await expect(dialog).not.toContainText('ETHUSD');
 expect(await calls(page)).toEqual(beforeCalls);
 expect(await writes(page)).toBe(beforeWrites);
 await dialog.getByRole('button',{name:'Close monitored assets',exact:true}).click();await expect(trigger(page,'Paused analyzed Dukascopy')).toBeFocused();
});

test('Dukascopy asset choices stage locally, retain unchecked choices, survive reload and restore all',async({page})=>{
 await openAssetsApp(page,true);await addDisabled(page,'Selected Dukascopy','dukascopy');
 const before=await writes(page),beforeCalls=await calls(page);
 await trigger(page,'Selected Dukascopy').click();
 let dialog=modal(page,'Selected Dukascopy');
 await expect(dialog.getByRole('checkbox',{name:'Monitor btcusd',exact:true})).toBeChecked();
 await expect(dialog.getByRole('checkbox',{name:'Monitor ethusd',exact:true})).toBeChecked();
 await dialog.getByRole('checkbox',{name:'Monitor ethusd',exact:true}).uncheck();
 expect(await writes(page)).toBe(before);
 await dialog.getByRole('button',{name:'Cancel',exact:true}).click();
 await trigger(page,'Selected Dukascopy').click();
 await expect(dialog.getByRole('checkbox',{name:'Monitor ethusd',exact:true})).toBeChecked();
 await dialog.getByRole('checkbox',{name:'Monitor ethusd',exact:true}).uncheck();
 await dialog.getByRole('button',{name:'Save assets',exact:true}).click();
 await expect.poll(()=>writes(page)).toBe(before+1);
 expect(await page.evaluate(()=>(window as any).__testLiveAlertsInfo().config.alerts[0].assetSelections)).toEqual([{dataSource:'dukascopy',assets:[{symbol:'btcusd'}]}]);
 await page.reload();await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Live Alerts',exact:true}).click();
 await trigger(page,'Selected Dukascopy').click();dialog=modal(page,'Selected Dukascopy');
 await expect(dialog.getByRole('checkbox',{name:'Monitor btcusd',exact:true})).toBeChecked();
 await expect(dialog.getByRole('checkbox',{name:'Monitor ethusd',exact:true})).not.toBeChecked();
 await dialog.getByRole('button',{name:'Select all',exact:true}).click();
 await dialog.getByRole('button',{name:'Save assets',exact:true}).click();
 await expect.poll(()=>page.evaluate(()=>(window as any).__testLiveAlertsInfo().config.alerts[0].assetSelections??null)).toBe(null);
 expect(await calls(page)).toEqual(beforeCalls);
 expect(await page.evaluate(()=>(window as any).__testLiveAlertsInfo().config.enabled)).toBe(false);
});

test('Hyperliquid supports a custom spot choice with no history download and an empty draft cannot be saved',async({page})=>{
 await openAssetsApp(page);await addDisabled(page,'Selected Hyperliquid','hyperliquid');
 const beforeHistory=await page.evaluate(()=>(window as any).__testHistoryKeys().length);
 await trigger(page,'Selected Hyperliquid').click();const dialog=modal(page,'Selected Hyperliquid');
 await expect(dialog.getByRole('checkbox')).toHaveCount(4);
 for(const checkbox of await dialog.getByRole('checkbox').all())await expect(checkbox).toBeChecked();
 await dialog.getByRole('button',{name:'Deselect all',exact:true}).click();
 await expect(dialog.getByRole('button',{name:'Save assets',exact:true})).toBeDisabled();
 await dialog.getByRole('checkbox',{name:'Monitor @107 spot',exact:true}).check();
 await dialog.getByRole('button',{name:'Save assets',exact:true}).click();
 await expect.poll(()=>page.evaluate(()=>(window as any).__testLiveAlertsInfo().config.alerts[0].assetSelections)).toEqual([{dataSource:'hyperliquid',assets:[{market:'spot',symbol:'@107'}]}]);
 await page.reload();await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Live Alerts',exact:true}).click();
 await trigger(page,'Selected Hyperliquid').click();
 await expect(dialog.getByRole('checkbox',{name:'Monitor @107 spot',exact:true})).toBeChecked();
 await expect(dialog.getByRole('checkbox',{name:'Monitor xyz:XYZ100 perpetual',exact:true})).not.toBeChecked();
 expect(await page.evaluate(()=>(window as any).__testHistoryKeys().length)).toBe(beforeHistory);
});

test('asset selections stay independent across sources and spot/perpetual identities',async({page})=>{
 await openAssetsApp(page);await addDisabled(page,'Source choices','hyperliquid');
 await trigger(page,'Source choices').click();let dialog=modal(page,'Source choices');
 await expect(rows(page,'Source choices')).toHaveCount(4);
 await dialog.getByRole('button',{name:'Deselect all',exact:true}).click();await dialog.getByRole('checkbox',{name:'Monitor xyz:XYZ100 perpetual',exact:true}).check();await dialog.getByRole('button',{name:'Save assets',exact:true}).click();
 const editSource=async(source:string)=>{
  if(source==='bybit'){
   // Restore the retained legacy venue preference through persisted fixture data.
   await page.evaluate(()=>{const state=(window as any).__testLiveAlertsInfo();(window as any).__testSetLiveAlertsInfo({config:{...state.config,alerts:state.config.alerts.map((entry:any)=>({...entry,dataSource:'bybit'}))}});});
   await expect.poll(()=>page.evaluate(()=>(window as any).__testLiveAlertsInfo().config.alerts[0].dataSource)).toBe('bybit');return;
  }
  await page.getByRole('button',{name:'Edit alert Source choices',exact:true}).click();const editor=page.getByRole('dialog',{name:'Edit alert',exact:true});await editor.getByRole('combobox',{name:'Alert data source',exact:true}).selectOption(source);await editor.getByRole('button',{name:'Save alert',exact:true}).click();await expect.poll(()=>page.evaluate(()=>(window as any).__testLiveAlertsInfo().config.alerts[0].dataSource)).toBe(source);};
 await editSource('bybit');await trigger(page,'Source choices').click();dialog=modal(page,'Source choices');
 await expect(dialog.getByRole('checkbox',{name:'Monitor BTCUSDT spot',exact:true})).toBeChecked();
 await expect(dialog.getByRole('checkbox',{name:'Monitor BTCUSDT perpetual',exact:true})).toBeChecked();
 await dialog.getByRole('button',{name:'Deselect all',exact:true}).click();await dialog.getByRole('checkbox',{name:'Monitor BTCUSDT spot',exact:true}).check();await dialog.getByRole('button',{name:'Save assets',exact:true}).click();
 await editSource('hyperliquid');await trigger(page,'Source choices').click();
 await expect(dialog.getByRole('checkbox',{name:'Monitor xyz:XYZ100 perpetual',exact:true})).toBeChecked();
 await expect(dialog.getByRole('checkbox',{name:'Monitor @107 spot',exact:true})).not.toBeChecked();
 await dialog.getByRole('button',{name:'Cancel',exact:true}).click();
 await editSource('bybit');await trigger(page,'Source choices').click();
 await expect(dialog.getByRole('checkbox',{name:'Monitor BTCUSDT spot',exact:true})).toBeChecked();
 await expect(dialog.getByRole('checkbox',{name:'Monitor BTCUSDT perpetual',exact:true})).not.toBeChecked();
});

test('failed asset selection save remains retryable without changing the alert or its saved history',async({page})=>{
 await openAssetsApp(page);await addDisabled(page,'Retry selection','hyperliquid');
 await page.evaluate(()=>{(window as any).__testLiveAlertsErrors({save:'Synthetic asset save failed'});(window as any).__testSetLiveAlertsInfo({sent:3});});
 await trigger(page,'Retry selection').click();const dialog=modal(page,'Retry selection');await expect(rows(page,'Retry selection')).toHaveCount(4);
 await dialog.getByRole('checkbox',{name:'Monitor @107 spot',exact:true}).uncheck();await dialog.getByRole('button',{name:'Save assets',exact:true}).click();
 await expect(page.locator('.live-alerts-configuration-error')).toContainText('Synthetic asset save failed');
 await expect(page.locator('.live-alerts-heading + .live-alerts-error')).toHaveCount(0);
 expect(await page.evaluate(()=>(window as any).__testLiveAlertsInfo().config.alerts[0].assetSelections??null)).toBe(null);
 await page.evaluate(()=>(window as any).__testLiveAlertsErrors({}));
 await page.locator('.live-alerts-configuration-error').getByRole('button',{name:'Retry',exact:true}).click();
 await expect(page.locator('.live-alerts-configuration-error')).toHaveCount(0);
 expect(await page.evaluate(()=>(window as any).__testLiveAlertsInfo().config.alerts[0].assetSelections[0].assets.length)).toBe(3);
 expect(await page.evaluate(()=>(window as any).__testLiveAlertsInfo().sent)).toBe(3);
 expect(await page.evaluate(()=>(window as any).__testLiveAlertsInfo().config.enabled)).toBe(false);
});

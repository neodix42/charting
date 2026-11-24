import {test,expect,type Page} from '@playwright/test';
import {openTestApp} from './helpers/testApp';
import type {Instrument} from '../src/types';
import type {TradeRecord} from '../src/workspace/tradeHistory';

const hours=(page:Page)=>page.getByLabel('Dukascopy trading hours',{exact:true});
const picker=(page:Page)=>page.locator('.instrument-picker');
const trigger=(page:Page)=>page.getByRole('button',{name:'Select instrument',exact:true});
const exchange=(page:Page)=>page.getByRole('combobox',{name:'Exchange',exact:true});
const session={marketHoursStatus:'available',marketHoursFirstDay:'1',marketHoursLastDay:'5',marketHoursOpen:'09:30:00',marketHoursClose:'16:00:00',marketHoursTimeZone:'Asia/Hong_Kong',marketHoursSource:'JForex regular market session'};
// These schedules are synthetic browser fixtures, not live Dukascopy trading hours.
const item=(nativeId:string,sdkSymbol:string,providerMetadata:Record<string,string>):Instrument=>({nativeId,market:'cfd',baseAsset:sdkSymbol.split('/')[0],quoteAsset:'HKD',priceIncrement:'0.01',sizeIncrement:'1',availability:'available',supportedIntervals:['1h'],providerMetadata:{sdkSymbol,...providerMetadata}});
const hk=item('0941HKHKD','0941.HK/HKD',session);
const unavailable=item('UNKNOWNHKHKD','UNKNOWN.HK/HKD',{marketHoursStatus:'unavailable'});
const calendar={marketHoursStatus:'available',marketHoursSource:'JForex instrument calendar',marketHoursTimeZone:'UTC',marketHoursFrom:String(Date.parse('2026-11-13T00:00:00Z')),marketHoursTo:String(Date.parse('2026-11-29T00:00:00Z')),marketHoursClosedIntervals:JSON.stringify([
 [Date.parse('2026-11-13T22:00:00Z'),Date.parse('2026-11-15T22:00:00Z')],
 [Date.parse('2026-11-20T22:00:00Z'),Date.parse('2026-11-22T22:00:00Z')],
 [Date.parse('2026-11-27T22:00:00Z'),Date.parse('2026-11-29T22:00:00Z')],
])};
const forex:Instrument={...item('EURUSD','EUR/USD',calendar),market:'forex',baseAsset:'EUR',quoteAsset:'USD',priceIncrement:'0.00001'};

test('Dukascopy order separates account environment from current market closure and updates while open',async({page})=>{
 await page.clock.install({time:new Date('2026-11-23T12:00:00Z')});
 await dukascopyFixtures(page,[{...forex,providerMetadata:{...session,marketHoursTimeZone:'UTC'}}]);
 await page.evaluate(async()=>{
  await window.go!.main!.App!.ConfigureDucascopy({username:'hours-offline-live',password:'explicit-fixture',environment:'live'});
  (window as any).__testSetHealth({state:'marketClosed',reason:'JForex reports this market is closed'});
 });
 await expect(page.locator('.chart-status')).toContainText('marketClosed');
 await page.getByTestId('chart-canvas').click({button:'right',position:{x:350,y:180}});
 await page.getByRole('menuitem',{name:'Buy at market…',exact:true}).click();
 const ticket=page.getByRole('dialog',{name:'Market order',exact:true}),status=ticket.getByLabel('Order market status',{exact:true});
 await expect(ticket).toContainText('Live account');await expect(status).toHaveText('Market closed');
 await expect(status).toHaveAttribute('title','JForex reports this market is closed');
 await page.evaluate(()=>(window as any).__testSetHealth({state:'live',reason:'Connected to JForex Bid feed'}));
 await expect(status).toHaveText('Market opened');
 await page.clock.setSystemTime(new Date('2026-11-23T18:00:00Z'));await page.clock.fastForward(1000);
 await expect(status).toHaveText('Market closed');
 await ticket.getByRole('button',{name:'Close market order',exact:true}).click();
 await selectInstrument(page,'UNKNOWN.HK/HKD CFD');
 await page.getByTestId('chart-canvas').click({button:'right',position:{x:350,y:180}});
 await page.getByRole('menuitem',{name:'Buy at market…',exact:true}).click();
 await expect(ticket.getByLabel('Order market status',{exact:true})).toHaveText('Market status unavailable');
});

async function dukascopyFixtures(page:Page,extra:Instrument[]=[]){
 await openTestApp(page);
 await expect(page.getByTestId('chart-cell')).toHaveCount(1);
 await page.evaluate(async items=>{
  const api=window.go!.main!.App!,original=api.ListInstruments.bind(api);
  await api.ConfigureDucascopy({username:'hours-offline-demo',password:'explicit-fixture',environment:'demo'});
  api.ListInstruments=async source=>source==='dukascopy'?structuredClone(items):original(source);
  api.RefreshDucascopyInstruments=async()=>structuredClone(items);
 },[hk,unavailable,...extra]);
 await exchange(page).selectOption('dukascopy');
}

async function selectInstrument(page:Page,name:string){
 await trigger(page).click();
 await picker(page).getByRole('button',{name,exact:true}).click();
 await expect(picker(page)).toHaveCount(0);
}

test('Dukascopy UTC hours paint at the top left with larger corner legends across themes and widths',async({page})=>{
 await page.clock.install({time:new Date('2026-11-23T01:29:59Z')});
 await dukascopyFixtures(page);
 const position:TradeRecord={id:'dukascopy:'+'a'.repeat(32),provider:'dukascopy',environment:'demo',account:'hours-offline-demo',instrument:hk.nativeId,market:'jforex',orderId:'hours-fixture',submittedAt:Date.now()-2000,openDate:Date.now()-1000,closeDate:0,size:'10',remainingSize:'10',direction:'long',entryPrice:'65',stopLoss:'60',takeProfit:'70',slAmount:'-50',tpAmount:'50',currency:'HKD',balanceAtOpen:'1000',balanceCurrency:'HKD',balanceTime:Date.now()-2000,marginUsed:'65',marginCurrency:'HKD',status:'open',canClose:true,closeReason:'Explicit offline fixture',checkedAt:Date.now()};
 await page.evaluate(position=>(window as any).__testSetTradeHistory([position]),position);
 await selectInstrument(page,'0941.HK/HKD CFD');
 const lines=hours(page).locator(':scope > span');
 await expect(lines).toHaveText(['Opens at 2026-11-23 01:30 UTC','Closes at 2026-11-23 08:00 UTC']);
 await expect(hours(page)).toHaveAttribute('title',/breaks.*holidays|holidays.*breaks/i);
 await expect(hours(page)).not.toContainText('Asia/Hong_Kong');
 await expect(hours(page).getByLabel('Asset regular-session status',{exact:true})).toHaveCount(0);
 await page.clock.fastForward(2000);
 await expect(lines).toHaveText(['Opens at 2026-11-23 01:30 UTC','Closes at 2026-11-23 08:00 UTC']);
 const levels=page.getByRole('complementary',{name:'Last trade levels',exact:true});
 const margin=page.getByRole('complementary',{name:'Dukascopy leverage and margin',exact:true});
 await expect(levels).toContainText('hours-fixture');
 await expect(margin).toContainText('JForex margin');
 for(const [theme,width] of [['light',1600],['dark',1600],['light',900]] as const){
  await page.setViewportSize({width,height:1000});
  if(await page.evaluate(()=>document.documentElement.dataset.theme)!==theme){
   await page.getByRole('button',{name:'Open settings',exact:true}).click();
   await page.getByLabel('Theme',{exact:true}).selectOption(theme);
   await page.getByRole('button',{name:'Close settings',exact:true}).click();
   await expect(page.locator('html')).toHaveAttribute('data-theme',theme);
  }
  const badge=await hours(page).boundingBox(),chart=await page.getByTestId('chart-canvas').boundingBox(),trade=await levels.boundingBox(),account=await margin.boundingBox();
  expect(badge).not.toBeNull();expect(chart).not.toBeNull();expect(trade).not.toBeNull();expect(account).not.toBeNull();
  expect(badge!.x).toBeGreaterThanOrEqual(chart!.x);
  expect(badge!.x).toBeLessThan(chart!.x+80);
  expect(badge!.y).toBeGreaterThanOrEqual(chart!.y);
  expect(badge!.y).toBeLessThan(chart!.y+30);
  expect(trade!.y).toBeLessThan(chart!.y+30);
  expect(badge!.x+badge!.width).toBeLessThanOrEqual(trade!.x-8);
  expect(account!.x+account!.width).toBeGreaterThan(chart!.x+chart!.width-100);
  expect(account!.x+account!.width).toBeLessThanOrEqual(chart!.x+chart!.width-70);
  expect(account!.y+account!.height).toBeLessThanOrEqual(chart!.y+chart!.height-25);
  expect(account!.y).toBeGreaterThanOrEqual(trade!.y+trade!.height+8);
  for(const legend of [hours(page),levels,margin])expect(await legend.evaluate(element=>parseFloat(getComputedStyle(element).fontSize))).toBeGreaterThanOrEqual(14);
  expect(await margin.locator('small').evaluate(element=>parseFloat(getComputedStyle(element).fontSize))).toBeGreaterThanOrEqual(14);
  for(const legend of [hours(page),margin])await expect(legend).toHaveCSS('pointer-events','none');
  await page.getByTestId('chart-cell').screenshot({path:`test-results/chart-corner-legends-${theme}-${width}.png`});
 }
 await page.clock.setSystemTime(new Date('2026-11-23T08:00:01Z'));await page.clock.fastForward(1000);
 await expect(lines).toHaveText(['Opens at 2026-11-24 01:30 UTC','Closes at 2026-11-24 08:00 UTC']);
 await selectInstrument(page,'UNKNOWN.HK/HKD CFD');
 await expect(hours(page).locator(':scope > span')).toHaveText(['Opens at — UTC','Closes at — UTC']);
 await expect(hours(page)).toContainText('Hours unavailable: provider schedule not supplied.');
 await exchange(page).selectOption('binance');
 await expect(hours(page)).toHaveCount(0);
});

test('missing or malformed Dukascopy session metadata leaves a visible unavailable explanation',async({page})=>{
 const invalid=item('INVALIDHKHKD','INVALID.HK/HKD',{...session,marketHoursOpen:'09:60:00'});
 await dukascopyFixtures(page,[invalid]);
 // The default EURUSD is absent from this synthetic catalog.
 await expect(hours(page).locator(':scope > span')).toHaveText(['Opens at — UTC','Closes at — UTC']);
 await expect(hours(page)).toContainText('Hours unavailable: provider schedule not supplied.');
 await selectInstrument(page,'UNKNOWN.HK/HKD CFD');
 await expect(hours(page)).toContainText('Hours unavailable: provider schedule not supplied.');
 await selectInstrument(page,'INVALID.HK/HKD CFD');
 await expect(hours(page)).toContainText('Hours unavailable: provider schedule cannot be resolved.');
});

test('Dukascopy forex calendar paints actual next opening and updates across the weekend boundary',async({page})=>{
 await page.clock.install({time:new Date('2026-11-21T12:00:00Z')});
 await dukascopyFixtures(page,[forex]);
 await expect(hours(page).locator(':scope > span')).toHaveText(['Opens at 2026-11-22 22:00 UTC','Closes at 2026-11-27 22:00 UTC']);
 await expect(hours(page)).not.toContainText('broker calendar');
 await expect(hours(page)).toHaveAttribute('title',/dated coverage.*trading halts/);
 const badge=await hours(page).boundingBox(),chart=await page.getByTestId('chart-canvas').boundingBox();
 expect(badge!.x).toBeLessThan(chart!.x+80);expect(badge!.y).toBeLessThan(chart!.y+30);
 await page.getByTestId('chart-cell').screenshot({path:'test-results/chart-forex-calendar.png'});
 await page.clock.setSystemTime(new Date('2026-11-22T21:59:59Z'));await page.clock.fastForward(1000);
 await expect(hours(page).locator(':scope > span')).toHaveText(['Opens at 2026-11-22 22:00 UTC','Closes at 2026-11-27 22:00 UTC']);
 await page.clock.setSystemTime(new Date('2026-11-29T00:00:00Z'));await page.clock.fastForward(1000);
 await expect(hours(page).locator(':scope > span')).toHaveText(['Opens at — UTC','Closes at — UTC']);
 await expect(hours(page)).toContainText('Hours expired: refresh needed.');
});

test('an active Dukascopy chart picks up delayed calendar discovery and stops checks when its source changes',async({page})=>{
 await page.clock.install({time:new Date('2026-11-21T12:00:00Z')});
 await dukascopyFixtures(page);
 await expect(hours(page)).toContainText('Hours unavailable: provider schedule not supplied.');
 await page.evaluate(items=>{
  (window as any).__hoursRefreshes=0;
  window.go!.main!.App!.RefreshDucascopyInstruments=async()=>{(window as any).__hoursRefreshes++;return structuredClone(items);};
 },[hk,unavailable,forex]);
 await page.clock.fastForward(5*60000+1000);
 await expect(hours(page)).toContainText('Opens at 2026-11-22 22:00 UTC');
 expect(await page.evaluate(()=>(window as any).__hoursRefreshes)).toBeGreaterThan(0);
 await exchange(page).selectOption('binance');
 await expect(hours(page)).toHaveCount(0);
 const count=await page.evaluate(()=>(window as any).__hoursRefreshes);
 await page.clock.fastForward(10*60000);
 expect(await page.evaluate(()=>(window as any).__hoursRefreshes)).toBe(count);
});

test('Dukascopy hours stay visible through unavailable catalog refreshes and recover on fresh data',async({page})=>{
 await page.clock.install({time:new Date('2026-11-24T02:00:00Z')});
 const available={...forex,providerMetadata:{...session,sdkSymbol:'EUR/USD'}};
 await dukascopyFixtures(page,[available]);
 const lines=hours(page).locator(':scope > span');
 await expect(lines).toHaveText(['Opens at 2026-11-24 01:30 UTC','Closes at 2026-11-24 08:00 UTC']);
 await expect(hours(page).locator('.market-hours-notice')).toHaveCount(0);
 await page.evaluate(items=>{window.go!.main!.App!.RefreshDucascopyInstruments=async()=>structuredClone(items);},[hk,unavailable,{...forex,providerMetadata:{sdkSymbol:'EUR/USD',marketHoursStatus:'unavailable'}}]);
 await page.clock.fastForward(5*60000+1000);
 await expect(lines).toHaveText(['Opens at — UTC','Closes at — UTC']);
 await expect(hours(page)).toContainText('Hours unavailable: provider schedule not supplied.');
 await expect(page.getByRole('complementary',{name:'Dukascopy leverage and margin',exact:true})).toContainText('JForex margin');
 await page.getByTestId('chart-cell').screenshot({path:'test-results/chart-hours-unavailable-visible.png'});
 await page.evaluate(items=>{window.go!.main!.App!.RefreshDucascopyInstruments=async()=>structuredClone(items);},[hk,unavailable,available]);
 await page.clock.fastForward(5*60000+1000);
 await expect(lines).toHaveText(['Opens at 2026-11-24 01:30 UTC','Closes at 2026-11-24 08:00 UTC']);
 await expect(hours(page).locator('.market-hours-notice')).toHaveCount(0);
});

test('Dukascopy hours show loading while catalog discovery is pending and preserve picker and tab context',async({page})=>{
 await page.clock.install({time:new Date('2026-11-24T02:00:00Z')});
 await openTestApp(page);
 await page.evaluate(async()=>{
  const api=window.go!.main!.App!,original=api.ListInstruments.bind(api);
  await api.ConfigureDucascopy({username:'hours-loading-demo',password:'explicit-fixture',environment:'demo'});
  const pending=new Promise<Instrument[]>(resolve=>{(window as any).__hoursRelease=resolve;});
  api.ListInstruments=async source=>source==='dukascopy'?pending:original(source);
  api.RefreshDucascopyInstruments=async()=>pending;
 });
 await exchange(page).selectOption('dukascopy');
 await expect(hours(page).locator(':scope > span')).toHaveText(['Opens at — UTC','Closes at — UTC']);
 await expect(hours(page)).toContainText('Hours loading…');
 await page.evaluate(items=>(window as any).__hoursRelease(items),[hk,unavailable,{...forex,providerMetadata:{...session,sdkSymbol:'EUR/USD'}}]);
 await expect(hours(page).locator(':scope > span')).toHaveText(['Opens at 2026-11-24 01:30 UTC','Closes at 2026-11-24 08:00 UTC']);
 await selectInstrument(page,'UNKNOWN.HK/HKD CFD');
 await expect(hours(page)).toContainText('Hours unavailable: provider schedule not supplied.');
 await selectInstrument(page,'EUR/USD Forex');
 await expect(hours(page)).toContainText('Opens at 2026-11-24 01:30 UTC');
 await page.getByLabel('Add tab',{exact:true}).click();
 await expect(hours(page)).toHaveCount(0);
 await page.locator('[role="tab"][data-tab-id]').first().click();
 await expect(hours(page).locator(':scope > span')).toHaveText(['Opens at 2026-11-24 01:30 UTC','Closes at 2026-11-24 08:00 UTC']);
 await expect(hours(page).locator('.market-hours-notice')).toHaveCount(0);
});

test('retained provider hours keep their original capture time visible until a fresh schedule arrives',async({page})=>{
 await page.clock.install({time:new Date('2026-11-24T02:00:00Z')});
 const available={...forex,providerMetadata:{...session,sdkSymbol:'EUR/USD'}};
 await dukascopyFixtures(page,[{...available,providerMetadata:{...available.providerMetadata,marketHoursRetained:'true',marketHoursFetchedAt:String(Date.parse('2026-11-23T06:01:49.519Z'))}}]);
 await expect(hours(page).locator(':scope > span')).toHaveText(['Opens at 2026-11-24 01:30 UTC','Closes at 2026-11-24 08:00 UTC']);
 await expect(hours(page)).toContainText('Last-known provider schedule. Saved 2026-11-23 06:01:49 UTC.');
 await page.getByTestId('chart-cell').screenshot({path:'test-results/chart-hours-retained-visible.png'});
 await page.evaluate(items=>{window.go!.main!.App!.RefreshDucascopyInstruments=async()=>structuredClone(items);},[hk,unavailable,available]);
 await page.clock.fastForward(5*60000+1000);
 await expect(hours(page)).toContainText('Opens at 2026-11-24 01:30 UTC');
 await expect(hours(page).locator('.market-hours-notice')).toHaveCount(0);
});

test('instrument search preserves inside interaction and closes on Escape, chart or toolbar clicks',async({page})=>{
 await openTestApp(page);
 await trigger(page).click();
 const search=picker(page).getByRole('textbox',{name:'Search instruments',exact:true});
 await search.click();await search.fill('BTC');
 await expect(picker(page)).toHaveCount(1);
 await expect(picker(page).getByRole('button',{name:'BTCUSDT Spot',exact:true})).toBeVisible();
 await search.press('Escape');
 await expect(picker(page)).toHaveCount(0);await expect(trigger(page)).toBeFocused();
 await trigger(page).click();await expect(picker(page)).toHaveCount(1);
 await trigger(page).click();await expect(picker(page)).toHaveCount(0);
 await trigger(page).click();
 await page.getByTestId('chart-canvas').click({position:{x:500,y:250}});
 await expect(picker(page)).toHaveCount(0);
 await trigger(page).click();
 await page.getByRole('button',{name:'Timeframe 1m',exact:true}).click();
 await expect(picker(page)).toHaveCount(0);
 await expect(page.getByRole('button',{name:'Timeframe 1m',exact:true})).toHaveAttribute('aria-pressed','true');
 await trigger(page).click();
 await picker(page).getByRole('button',{name:'BTCUSDT Perpetual',exact:true}).click();
 await expect(picker(page)).toHaveCount(0);
 await expect(page.getByLabel('Selected market',{exact:true})).toHaveText('Perpetual');
});

test('opening another chart asset selector dismisses the previous selector and outside controls still work',async({page})=>{
 await openTestApp(page);
 await page.getByRole('button',{name:'Add chart',exact:true}).click();
 const charts=page.getByTestId('chart-cell');await expect(charts).toHaveCount(2);
 await charts.nth(0).getByRole('button',{name:'Select instrument',exact:true}).click();
 await expect(charts.nth(0).locator('.instrument-picker')).toHaveCount(1);
 await charts.nth(1).getByRole('button',{name:'Select instrument',exact:true}).click();
 await expect(charts.nth(0).locator('.instrument-picker')).toHaveCount(0);
 await expect(charts.nth(1).locator('.instrument-picker')).toHaveCount(1);
 await charts.nth(0).getByTestId('chart-canvas').click({position:{x:350,y:170}});
 await expect(picker(page)).toHaveCount(0);
 await charts.nth(1).getByRole('button',{name:'Select instrument',exact:true}).click();
 await page.getByRole('button',{name:'Open settings',exact:true}).click();
 await expect(picker(page)).toHaveCount(0);
 await expect(page.getByRole('dialog',{name:'Workspace settings',exact:true})).toBeVisible();
});

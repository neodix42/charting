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

test('Dukascopy regular hours and session status paint at the bottom left while last-trade prices stay at the top right',async({page})=>{
 await page.clock.install({time:new Date('2026-11-23T01:29:59Z')});
 await dukascopyFixtures(page);
 const position:TradeRecord={id:'dukascopy:'+'a'.repeat(32),provider:'dukascopy',environment:'demo',account:'hours-offline-demo',instrument:hk.nativeId,market:'jforex',orderId:'hours-fixture',submittedAt:Date.now()-2000,openDate:Date.now()-1000,closeDate:0,size:'10',remainingSize:'10',direction:'long',entryPrice:'65',stopLoss:'60',takeProfit:'70',slAmount:'-50',tpAmount:'50',currency:'HKD',balanceAtOpen:'1000',balanceCurrency:'HKD',balanceTime:Date.now()-2000,marginUsed:'65',marginCurrency:'HKD',status:'open',canClose:true,closeReason:'Explicit offline fixture',checkedAt:Date.now()};
 await page.evaluate(position=>(window as any).__testSetTradeHistory([position]),position);
 await selectInstrument(page,'0941.HK/HKD CFD');
 await expect(hours(page)).toContainText('Regular hours');
 for(const text of ['Mon','Fri','09:30','16:00','Asia/Hong_Kong'])await expect(hours(page)).toContainText(text);
 await expect(hours(page)).toHaveAttribute('title',/breaks.*holidays|holidays.*breaks/i);
 const status=hours(page).getByLabel('Asset regular-session status',{exact:true});
 await expect(status).toHaveText('Closed');await expect(hours(page)).toContainText('regular schedule');
 await page.clock.fastForward(2000);await expect(status).toHaveText('Opened');
 const levels=page.getByRole('complementary',{name:'Last trade levels',exact:true});
 await expect(levels).toContainText('hours-fixture');
 const badge=await hours(page).boundingBox(),chart=await page.getByTestId('chart-canvas').boundingBox(),trade=await levels.boundingBox();
 expect(badge).not.toBeNull();expect(chart).not.toBeNull();expect(trade).not.toBeNull();
 expect(badge!.x).toBeGreaterThanOrEqual(chart!.x);
 expect(badge!.x).toBeLessThan(chart!.x+80);
 const attribution=await page.getByTestId('chart-canvas').locator('a[href*="tradingview.com"]').boundingBox();
 expect(attribution).not.toBeNull();expect(badge!.x).toBeGreaterThan(attribution!.x+attribution!.width);
 expect(badge!.y).toBeGreaterThan(chart!.y+chart!.height/2);
 expect(badge!.y+badge!.height).toBeLessThanOrEqual(chart!.y+chart!.height-25);
 expect(trade!.y).toBeLessThan(chart!.y+100);
 expect(await hours(page).evaluate(element=>getComputedStyle(element).pointerEvents)).toBe('none');
 await page.getByTestId('chart-cell').screenshot({path:'test-results/chart-bottom-corners.png'});
 await page.clock.setSystemTime(new Date('2026-11-23T08:00:01Z'));await page.clock.fastForward(1000);await expect(status).toHaveText('Closed');
 await selectInstrument(page,'UNKNOWN.HK/HKD CFD');
 await expect(hours(page)).toHaveCount(0);
 await exchange(page).selectOption('binance');
 await expect(hours(page)).toHaveCount(0);
});

test('missing or malformed Dukascopy session metadata hides the hours badge',async({page})=>{
 const invalid=item('INVALIDHKHKD','INVALID.HK/HKD',{...session,marketHoursOpen:'09:60:00'});
 await dukascopyFixtures(page,[invalid]);
 // The default EURUSD is absent from this synthetic catalog.
 await expect(hours(page)).toHaveCount(0);
 await selectInstrument(page,'UNKNOWN.HK/HKD CFD');
 await expect(hours(page)).toHaveCount(0);
 await selectInstrument(page,'INVALID.HK/HKD CFD');
 await expect(hours(page)).toHaveCount(0);
});

test('Dukascopy forex calendar paints actual next opening and updates across the weekend boundary',async({page})=>{
 await page.clock.install({time:new Date('2026-11-21T12:00:00Z')});
 await dukascopyFixtures(page,[forex]);
 await expect(hours(page)).toContainText('broker calendar');
 await expect(hours(page)).toContainText('Next opens Sun 2026-11-22 22:00 UTC');
 await expect(hours(page)).toContainText('closes Fri 2026-11-27 22:00 UTC');
 const status=hours(page).getByLabel('Asset regular-session status',{exact:true});
 await expect(status).toHaveText('Closed');
 await expect(hours(page)).toHaveAttribute('title',/dated coverage.*trading halts/);
 const badge=await hours(page).boundingBox(),chart=await page.getByTestId('chart-canvas').boundingBox();
 expect(badge!.x).toBeLessThan(chart!.x+80);expect(badge!.y).toBeGreaterThan(chart!.y+chart!.height/2);
 await page.getByTestId('chart-cell').screenshot({path:'test-results/chart-forex-calendar.png'});
 await page.clock.setSystemTime(new Date('2026-11-22T21:59:59Z'));await page.clock.fastForward(1000);
 await expect(status).toHaveText('Opened');await expect(hours(page)).toContainText('Opened Sun 2026-11-22 22:00 UTC');
 await page.clock.setSystemTime(new Date('2026-11-29T00:00:00Z'));await page.clock.fastForward(1000);
 await expect(hours(page)).toHaveCount(0);
});

test('an active Dukascopy chart picks up delayed calendar discovery and stops checks when its source changes',async({page})=>{
 await page.clock.install({time:new Date('2026-11-21T12:00:00Z')});
 await dukascopyFixtures(page);
 await expect(hours(page)).toHaveCount(0);
 await page.evaluate(items=>{
  (window as any).__hoursRefreshes=0;
  window.go!.main!.App!.RefreshDucascopyInstruments=async()=>{(window as any).__hoursRefreshes++;return structuredClone(items);};
 },[hk,unavailable,forex]);
 await page.clock.fastForward(5*60000+1000);
 await expect(hours(page)).toContainText('Next opens Sun 2026-11-22 22:00 UTC');
 expect(await page.evaluate(()=>(window as any).__hoursRefreshes)).toBeGreaterThan(0);
 await exchange(page).selectOption('binance');
 await expect(hours(page)).toHaveCount(0);
 const count=await page.evaluate(()=>(window as any).__hoursRefreshes);
 await page.clock.fastForward(10*60000);
 expect(await page.evaluate(()=>(window as any).__hoursRefreshes)).toBe(count);
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

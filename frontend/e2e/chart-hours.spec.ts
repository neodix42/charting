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

test('Dukascopy regular hours follow the instrument and coexist with last-trade prices at the top right',async({page})=>{
 await dukascopyFixtures(page);
 const position:TradeRecord={id:'dukascopy:'+'a'.repeat(32),provider:'dukascopy',environment:'demo',account:'hours-offline-demo',instrument:hk.nativeId,market:'jforex',orderId:'hours-fixture',submittedAt:Date.now()-2000,openDate:Date.now()-1000,closeDate:0,size:'10',remainingSize:'10',direction:'long',entryPrice:'65',stopLoss:'60',takeProfit:'70',slAmount:'-50',tpAmount:'50',currency:'HKD',balanceAtOpen:'1000',balanceCurrency:'HKD',balanceTime:Date.now()-2000,marginUsed:'65',marginCurrency:'HKD',status:'open',canClose:true,closeReason:'Explicit offline fixture',checkedAt:Date.now()};
 await page.evaluate(position=>(window as any).__testSetTradeHistory([position]),position);
 await selectInstrument(page,'0941.HK/HKD CFD');
 await expect(hours(page)).toContainText('Regular hours');
 for(const text of ['Mon','Fri','09:30','16:00','Asia/Hong_Kong'])await expect(hours(page)).toContainText(text);
 await expect(hours(page)).toHaveAttribute('title',/breaks.*holidays|holidays.*breaks/i);
 const levels=page.getByRole('complementary',{name:'Last trade levels',exact:true});
 await expect(levels).toContainText('hours-fixture');
 const badge=await hours(page).boundingBox(),chart=await page.getByTestId('chart-canvas').boundingBox(),trade=await levels.boundingBox();
 expect(badge).not.toBeNull();expect(chart).not.toBeNull();expect(trade).not.toBeNull();
 expect(badge!.x).toBeGreaterThan(chart!.x+chart!.width/2);
 expect(badge!.y).toBeGreaterThanOrEqual(chart!.y);
 expect(badge!.y).toBeLessThan(chart!.y+35);
 expect(trade!.y).toBeGreaterThanOrEqual(badge!.y+badge!.height);
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

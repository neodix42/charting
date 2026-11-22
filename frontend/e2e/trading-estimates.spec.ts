import {test,expect,type Page} from '@playwright/test';
import {openTestApp} from './helpers/testApp';

const ticket=(page:Page)=>page.getByRole('dialog',{name:'Market order',exact:true});
const sl=(page:Page)=>ticket(page).getByLabel('Stop loss estimate',{exact:true});
const tp=(page:Page)=>ticket(page).getByLabel('Take profit estimate',{exact:true});
async function setup(page:Page){
 await openTestApp(page);await page.evaluate(async()=>{await window.go!.main!.App!.ConfigureDucascopy({username:'estimate-demo',password:'explicit-fixture',environment:'demo'});});await page.getByRole('combobox',{name:'Exchange',exact:true}).selectOption('dukascopy');
}
async function valuation(page:Page,patch:Record<string,unknown>){await page.evaluate(patch=>(window as any).__testSetTradeValuation(patch),patch);}
async function openOrder(page:Page,side='Buy'){
 await page.getByTestId('chart-canvas').click({button:'right',position:{x:350,y:180}});await page.getByRole('menuitem',{name:`${side} at market…`,exact:true}).click();await expect(ticket(page)).toBeVisible();
}

for(const width of [1600,900]){
 test(`SL and TP inputs stay fixed with estimates below them at ${width}px`,async({page})=>{
  await page.setViewportSize({width,height:1100});await setup(page);await page.clock.install();await valuation(page,{entryPrice:'100',accountCurrency:'USD',profitPerPriceUnit:'1'});await openOrder(page);
  const quantity=ticket(page).getByLabel('Order quantity',{exact:true}),stop=ticket(page).getByLabel('Order stop loss',{exact:true}),profit=ticket(page).getByLabel('Order take profit',{exact:true});
  await quantity.fill('2');await page.clock.fastForward(350);
  const before=[await stop.boundingBox(),await profit.boundingBox()];expect(before.every(Boolean)).toBe(true);
  const fixed=async()=>{
   for(const [index,field] of [stop,profit].entries()){
    const box=await field.boundingBox();expect(box).not.toBeNull();expect(box!.x).toBeCloseTo(before[index]!.x,3);expect(box!.y).toBeCloseTo(before[index]!.y,3);expect(box!.width).toBeCloseTo(before[index]!.width,3);
   }
   for(const [field,estimate] of [[stop,sl(page)],[profit,tp(page)]]){const input=await field.boundingBox(),helper=await estimate.boundingBox();expect(helper!.y).toBeGreaterThanOrEqual(input!.y+input!.height);}
  };
  await stop.fill('95');await expect(sl(page)).toHaveText('-500.00 bps · -10.0000 USD');await fixed();
  await profit.fill('110');await expect(tp(page)).toHaveText('+1,000.00 bps · +20.0000 USD');await fixed();
  await quantity.fill('1234567890123456789');await stop.fill('1');await profit.fill('9999999999');await expect(sl(page)).toContainText('-9,900.00 bps');await expect(tp(page)).toContainText('USD');await fixed();
  await quantity.fill('');await expect(sl(page)).toHaveText('-9,900.00 bps · Account amount unavailable');await fixed();
  await page.screenshot({path:`../.tools/verification/fixed-protection-fields-${width}.png`});
  await page.evaluate(()=>{const api=window.go!.main!.App!,read=api.EstimateTrade;api.EstimateTrade=(key,side)=>new Promise(resolve=>{(window as any).__releaseFixedFieldQuote=async()=>resolve({...await read(key,side),entryPrice:'150'});});});
  await ticket(page).getByRole('button',{name:'Refresh quote',exact:true}).click();await expect(sl(page)).toHaveText('bps unavailable · Account amount unavailable');await fixed();
  await page.evaluate(()=>(window as any).__releaseFixedFieldQuote());await expect(sl(page)).toContainText('-9,933.33 bps');await fixed();
  await stop.fill('');await profit.fill('');await expect(sl(page)).toHaveText('Enter a price to estimate');await expect(tp(page)).toHaveText('Enter a price to estimate');await fixed();
  expect(await page.evaluate(()=>(window as any).__testTradeSubmissions())).toEqual([]);
 });
}

test('Dukascopy buy loss/profit estimates recalculate immediately for quantity, levels and refreshed entry',async({page})=>{
 await setup(page);await valuation(page,{entryPrice:'100',accountCurrency:'USD',profitPerPriceUnit:'1'});await openOrder(page);await expect(ticket(page).getByLabel('Order valuation')).toContainText('Indicative entry: 100');
 await ticket(page).getByLabel('Order quantity').fill('2');await ticket(page).getByLabel('Order stop loss').fill('95');await ticket(page).getByLabel('Order take profit').fill('110');await expect(sl(page)).toHaveText('-500.00 bps · -10.0000 USD');await expect(tp(page)).toHaveText('+1,000.00 bps · +20.0000 USD');
 const reads=await page.evaluate(()=>(window as any).__testTradeValuationReads());await ticket(page).getByLabel('Order quantity').fill('4');await expect(sl(page)).toHaveText('-500.00 bps · -20.0000 USD');await ticket(page).getByLabel('Order stop loss').fill('98');await expect(sl(page)).toHaveText('-200.00 bps · -8.0000 USD');await ticket(page).getByLabel('Order take profit').fill('115');await expect(tp(page)).toHaveText('+1,500.00 bps · +60.0000 USD');expect(await page.evaluate(()=>(window as any).__testTradeValuationReads())).toBe(reads);
 await valuation(page,{entryPrice:'120'});await ticket(page).getByRole('button',{name:'Refresh quote',exact:true}).click();await expect(sl(page)).toHaveText('-1,833.33 bps · -88.0000 USD');await expect(tp(page)).toHaveText('-416.67 bps · -20.0000 USD');expect(await page.evaluate(()=>(window as any).__testTradeSubmissions())).toEqual([]);
});

test('sell estimates use EUR conversion and reviewed quote/account values remain frozen until explicitly refreshed',async({page})=>{
 await setup(page);await valuation(page,{entryPrice:'200',accountCurrency:'EUR',profitPerPriceUnit:'0.9'});await openOrder(page,'Sell');await ticket(page).getByLabel('Order quantity').fill('10');await ticket(page).getByLabel('Order stop loss').fill('210');await ticket(page).getByLabel('Order take profit').fill('180');await expect(sl(page)).toHaveText('-500.00 bps · -90.0000 EUR');await expect(tp(page)).toHaveText('+1,000.00 bps · +180.0000 EUR');await ticket(page).getByRole('button',{name:'Review order',exact:true}).click();await expect(ticket(page).getByRole('button',{name:'Confirm Sell at market',exact:true})).toBeEnabled();
 const reads=await page.evaluate(()=>(window as any).__testTradeValuationReads());await page.clock.install();await valuation(page,{entryPrice:'300',accountCurrency:'USD',profitPerPriceUnit:'1'});await page.clock.fastForward(12000);expect(await page.evaluate(()=>(window as any).__testTradeValuationReads())).toBe(reads);await expect(sl(page)).toHaveText('-500.00 bps · -90.0000 EUR');await expect(ticket(page).getByLabel('Review stop loss estimate',{exact:true})).toHaveText('-500.00 bps · -90.0000 EUR');await expect(ticket(page).getByLabel('Review take profit estimate',{exact:true})).toHaveText('+1,000.00 bps · +180.0000 EUR');await expect(ticket(page).getByLabel('Review stop loss estimate').locator('..')).toContainText('210');await expect(ticket(page).getByLabel('Order valuation')).toContainText('Indicative entry: 200');
 await ticket(page).getByRole('button',{name:'Refresh quote',exact:true}).click();await expect(ticket(page).getByRole('button',{name:'Confirm Sell at market',exact:true})).toHaveCount(0);await expect(sl(page)).toHaveText('+3,000.00 bps · +900.0000 USD');await expect(ticket(page).getByRole('button',{name:'Review order',exact:true})).toBeVisible();
});

test('editing polls entry/conversion every ten seconds and never substitutes a currency when FX is unavailable',async({page})=>{
 await setup(page);await page.clock.install();await valuation(page,{entryPrice:'100',accountCurrency:'EUR',profitPerPriceUnit:'0.8'});await openOrder(page);await ticket(page).getByLabel('Order quantity').fill('2');await ticket(page).getByLabel('Order stop loss').fill('95');await expect(sl(page)).toHaveText('-500.00 bps · -8.0000 EUR');
 await valuation(page,{entryPrice:'200',profitPerPriceUnit:'0.9'});await page.clock.fastForward(10000);await expect(sl(page)).toHaveText('-5,250.00 bps · -189.0000 EUR');
 await valuation(page,{profitPerPriceUnit:'',valuationError:'Conversion quote unavailable'});await page.clock.fastForward(10000);await expect(sl(page)).toHaveText('-5,250.00 bps · Account amount unavailable');await expect(ticket(page)).toContainText('Conversion quote unavailable');await expect(sl(page)).not.toContainText('USD');await ticket(page).getByLabel('Order stop loss').fill('');await expect(sl(page)).toHaveText('Enter a price to estimate');
});

test('pending quote reads do not overlap and old ticket/side results cannot replace a new estimate',async({page})=>{
 await setup(page);await page.clock.install();await page.evaluate(()=>{const api=window.go!.main!.App!,estimate=api.EstimateTrade;(window as any).__quoteReleases=[];(window as any).__quoteStarts=0;api.EstimateTrade=(key,side)=>new Promise(resolve=>{(window as any).__quoteStarts++;(window as any).__quoteReleases.push(async(entry:string)=>resolve({...await estimate(key,side),entryPrice:entry}));});});await openOrder(page);await page.clock.fastForward(30000);expect(await page.evaluate(()=>(window as any).__quoteStarts)).toBe(1);await ticket(page).getByRole('button',{name:'Close market order',exact:true}).click();await openOrder(page,'Sell');await page.evaluate(()=>(window as any).__quoteReleases[1]('200'));await expect(ticket(page).getByLabel('Order valuation')).toContainText('Indicative entry: 200');await page.evaluate(()=>(window as any).__quoteReleases[0]('999'));await expect(ticket(page).getByLabel('Order valuation')).not.toContainText('999');await expect(ticket(page).getByLabel('Order valuation')).toContainText('Indicative entry: 200');
 await ticket(page).getByRole('button',{name:'Refresh quote',exact:true}).click();await page.evaluate(()=>window.dispatchEvent(new Event('cryptodesk:trading-configured')));await expect(ticket(page)).toHaveCount(0);await page.evaluate(()=>(window as any).__quoteReleases[2]('888'));await expect(ticket(page)).toHaveCount(0);
});

test('a stalled refresh cannot keep showing an old account-currency estimate as current',async({page})=>{
 await setup(page);await page.clock.install();await valuation(page,{entryPrice:'100',accountCurrency:'USD',profitPerPriceUnit:'1'});await openOrder(page);await ticket(page).getByLabel('Order quantity').fill('2');await ticket(page).getByLabel('Order stop loss').fill('95');await expect(sl(page)).toHaveText('-500.00 bps · -10.0000 USD');
 await page.evaluate(()=>{window.go!.main!.App!.EstimateTrade=()=>new Promise(()=>{});});
 await page.clock.fastForward(10000);await page.clock.fastForward(21000);await expect(ticket(page).getByLabel('Order valuation')).toContainText('Quote stale');await expect(sl(page)).toHaveText('-500.00 bps · Account amount unavailable');
});

async function closedCFD(page:Page,conversion=true){
 await openTestApp(page);await page.clock.install({time:Date.UTC(2026,10,21,12)});
 await page.evaluate(async conversion=>{
  const api=window.go!.main!.App!,list=api.ListInstruments.bind(api);
  await api.ConfigureDucascopy({username:'estimate-demo',password:'explicit-fixture',environment:'demo'});
  api.ListInstruments=async source=>source==='dukascopy'?[{nativeId:'BAMITEUR',market:'cfd',baseAsset:'BAM.IT',quoteAsset:'EUR',priceIncrement:'0.001',sizeIncrement:'1',availability:'available',supportedIntervals:['1h'],providerMetadata:{sdkSymbol:'BAM.IT/EUR'}}]:list(source);
  api.RefreshDucascopyInstruments=async()=>api.ListInstruments('dukascopy');
  (window as any).__testSetTradeValuation({entryPrice:'15',accountCurrency:'USD',profitPerPriceUnit:conversion?'1.1':'',referenceOnly:true,valuationTime:Date.UTC(2026,10,20,16,30),valuationError:conversion?'':'EUR/USD conversion quote unavailable',valuationNotice:'Last available JForex quotes for planning only; fresh quotes required before review.'});
  (window as any).__testSetTradeMetrics({availableBalance:'1104.55',availableBalanceCurrency:'USD',maximumQuantity:'',maximumQuantityError:'A fresh executable quote is required'},'dukascopy','BAMITEUR');
 },conversion);
 await page.getByRole('combobox',{name:'Exchange',exact:true}).selectOption('dukascopy');
 await page.getByRole('button',{name:'Select instrument',exact:true}).click();
 await page.locator('.instrument-picker [data-asset-id="cfd|BAMITEUR"]').click();
 await openOrder(page);await ticket(page).getByLabel('Order quantity',{exact:true}).fill('1000');await ticket(page).getByLabel('Order stop loss',{exact:true}).fill('14');
 await page.clock.fastForward(350);
}

test('closed CFD estimates use dated last quotes without treating them as executable or losing the independent account balance',async({page})=>{
 await closedCFD(page);const orderValuation=ticket(page).getByLabel('Order valuation',{exact:true});
 await expect(orderValuation).toContainText('Last market quote (reference only): 15');await expect(orderValuation).toContainText('2026-11-20 16:30:00 UTC');
 await expect(ticket(page).getByLabel('Available balance',{exact:true})).toHaveText('1,104.5500 USD');
 await expect(sl(page)).toHaveText('-666.67 bps · -1,100.0000 USD');await expect(tp(page)).toHaveText('Enter a price to estimate');
 await ticket(page).getByLabel('Order take profit',{exact:true}).fill('16');await expect(tp(page)).toHaveText('+666.67 bps · +1,100.0000 USD');
 await expect(ticket(page).getByRole('button',{name:'Review order',exact:true})).toBeDisabled();
 await ticket(page).locator('form').dispatchEvent('submit');expect(await page.evaluate(()=>(window as any).__testTradeRequests())).toEqual([]);
 await page.clock.fastForward(60000);await expect(sl(page)).toHaveText('-666.67 bps · -1,100.0000 USD');
 await expect(orderValuation).toContainText('Last market quote (reference only)');await expect(orderValuation).toContainText('A fresh executable quote is required');
 await valuation(page,{entryPrice:'15.5',profitPerPriceUnit:'1.2',referenceOnly:false,valuationNotice:''});await ticket(page).getByRole('button',{name:'Refresh quote',exact:true}).click();
 await expect(orderValuation).toContainText('Indicative entry: 15.5');await expect(orderValuation).not.toContainText('reference only');
 await expect(sl(page)).toHaveText('-967.74 bps · -1,800.0000 USD');await expect(ticket(page).getByRole('button',{name:'Review order',exact:true})).toBeEnabled();
 expect(await page.evaluate(()=>(window as any).__testTradeSubmissions())).toEqual([]);
});

test('a last CFD quote provides bps when FX conversion is missing without using available balance as a conversion',async({page})=>{
 await closedCFD(page,false);await expect(sl(page)).toHaveText('-666.67 bps · Account amount unavailable');
 await expect(ticket(page).getByLabel('Available balance',{exact:true})).toHaveText('1,104.5500 USD');await expect(ticket(page)).toContainText('EUR/USD conversion quote unavailable');
 await expect(ticket(page).getByRole('button',{name:'Review order',exact:true})).toBeDisabled();
 await expect(ticket(page).getByLabel('Order valuation',{exact:true})).toContainText('Last market quote (reference only): 15');
});

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

test('Dukascopy buy loss/profit estimates recalculate immediately for quantity, levels and refreshed entry',async({page})=>{
 await setup(page);await valuation(page,{entryPrice:'100',accountCurrency:'USD',profitPerPriceUnit:'1'});await openOrder(page);await expect(ticket(page).getByLabel('Order valuation')).toContainText('Indicative entry: 100');
 await ticket(page).getByLabel('Order quantity').fill('2');await ticket(page).getByLabel('Order stop loss').fill('95');await ticket(page).getByLabel('Order take profit').fill('110');await expect(sl(page)).toHaveText('-500.00 bps · -10.00 USD');await expect(tp(page)).toHaveText('+1,000.00 bps · +20.00 USD');
 const reads=await page.evaluate(()=>(window as any).__testTradeValuationReads());await ticket(page).getByLabel('Order quantity').fill('4');await expect(sl(page)).toHaveText('-500.00 bps · -20.00 USD');await ticket(page).getByLabel('Order stop loss').fill('98');await expect(sl(page)).toHaveText('-200.00 bps · -8.00 USD');await ticket(page).getByLabel('Order take profit').fill('115');await expect(tp(page)).toHaveText('+1,500.00 bps · +60.00 USD');expect(await page.evaluate(()=>(window as any).__testTradeValuationReads())).toBe(reads);
 await valuation(page,{entryPrice:'120'});await ticket(page).getByRole('button',{name:'Refresh quote',exact:true}).click();await expect(sl(page)).toHaveText('-1,833.33 bps · -88.00 USD');await expect(tp(page)).toHaveText('-416.67 bps · -20.00 USD');expect(await page.evaluate(()=>(window as any).__testTradeSubmissions())).toEqual([]);
});

test('sell estimates use EUR conversion and reviewed quote/account values remain frozen until explicitly refreshed',async({page})=>{
 await setup(page);await valuation(page,{entryPrice:'200',accountCurrency:'EUR',profitPerPriceUnit:'0.9'});await openOrder(page,'Sell');await ticket(page).getByLabel('Order quantity').fill('10');await ticket(page).getByLabel('Order stop loss').fill('210');await ticket(page).getByLabel('Order take profit').fill('180');await expect(sl(page)).toHaveText('-500.00 bps · -90.00 EUR');await expect(tp(page)).toHaveText('+1,000.00 bps · +180.00 EUR');await ticket(page).getByRole('button',{name:'Review order',exact:true}).click();await expect(ticket(page).getByRole('button',{name:'Confirm Sell at market',exact:true})).toBeEnabled();
 const reads=await page.evaluate(()=>(window as any).__testTradeValuationReads());await page.clock.install();await valuation(page,{entryPrice:'300',accountCurrency:'USD',profitPerPriceUnit:'1'});await page.clock.fastForward(12000);expect(await page.evaluate(()=>(window as any).__testTradeValuationReads())).toBe(reads);await expect(sl(page)).toHaveText('-500.00 bps · -90.00 EUR');await expect(ticket(page).getByLabel('Review stop loss estimate',{exact:true})).toHaveText('-500.00 bps · -90.00 EUR');await expect(ticket(page).getByLabel('Review take profit estimate',{exact:true})).toHaveText('+1,000.00 bps · +180.00 EUR');await expect(ticket(page).getByLabel('Review stop loss estimate').locator('..')).toContainText('210');await expect(ticket(page).getByLabel('Order valuation')).toContainText('Indicative entry: 200');
 await ticket(page).getByRole('button',{name:'Refresh quote',exact:true}).click();await expect(ticket(page).getByRole('button',{name:'Confirm Sell at market',exact:true})).toHaveCount(0);await expect(sl(page)).toHaveText('+3,000.00 bps · +900.00 USD');await expect(ticket(page).getByRole('button',{name:'Review order',exact:true})).toBeVisible();
});

test('editing polls entry/conversion every ten seconds and never substitutes a currency when FX is unavailable',async({page})=>{
 await setup(page);await page.clock.install();await valuation(page,{entryPrice:'100',accountCurrency:'EUR',profitPerPriceUnit:'0.8'});await openOrder(page);await ticket(page).getByLabel('Order quantity').fill('2');await ticket(page).getByLabel('Order stop loss').fill('95');await expect(sl(page)).toHaveText('-500.00 bps · -8.00 EUR');
 await valuation(page,{entryPrice:'200',profitPerPriceUnit:'0.9'});await page.clock.fastForward(10000);await expect(sl(page)).toHaveText('-5,250.00 bps · -189.00 EUR');
 await valuation(page,{profitPerPriceUnit:'',valuationError:'Conversion quote unavailable'});await page.clock.fastForward(10000);await expect(sl(page)).toHaveText('-5,250.00 bps · Account amount unavailable');await expect(ticket(page)).toContainText('Conversion quote unavailable');await expect(sl(page)).not.toContainText('USD');await ticket(page).getByLabel('Order stop loss').fill('');await expect(sl(page)).toHaveText('bps unavailable · Account amount unavailable');
});

test('pending quote reads do not overlap and old ticket/side results cannot replace a new estimate',async({page})=>{
 await setup(page);await page.clock.install();await page.evaluate(()=>{const api=window.go!.main!.App!,estimate=api.EstimateTrade;(window as any).__quoteReleases=[];(window as any).__quoteStarts=0;api.EstimateTrade=(key,side)=>new Promise(resolve=>{(window as any).__quoteStarts++;(window as any).__quoteReleases.push(async(entry:string)=>resolve({...await estimate(key,side),entryPrice:entry}));});});await openOrder(page);await page.clock.fastForward(30000);expect(await page.evaluate(()=>(window as any).__quoteStarts)).toBe(1);await ticket(page).getByRole('button',{name:'Close market order',exact:true}).click();await openOrder(page,'Sell');await page.evaluate(()=>(window as any).__quoteReleases[1]('200'));await expect(ticket(page).getByLabel('Order valuation')).toContainText('Indicative entry: 200');await page.evaluate(()=>(window as any).__quoteReleases[0]('999'));await expect(ticket(page).getByLabel('Order valuation')).not.toContainText('999');await expect(ticket(page).getByLabel('Order valuation')).toContainText('Indicative entry: 200');
 await ticket(page).getByRole('button',{name:'Refresh quote',exact:true}).click();await page.evaluate(()=>window.dispatchEvent(new Event('cryptodesk:trading-configured')));await expect(ticket(page)).toHaveCount(0);await page.evaluate(()=>(window as any).__quoteReleases[2]('888'));await expect(ticket(page)).toHaveCount(0);
});

test('a stalled refresh cannot keep showing an old account-currency estimate as current',async({page})=>{
 await setup(page);await page.clock.install();await valuation(page,{entryPrice:'100',accountCurrency:'USD',profitPerPriceUnit:'1'});await openOrder(page);await ticket(page).getByLabel('Order quantity').fill('2');await ticket(page).getByLabel('Order stop loss').fill('95');await expect(sl(page)).toHaveText('-500.00 bps · -10.00 USD');
 await page.evaluate(()=>{window.go!.main!.App!.EstimateTrade=()=>new Promise(()=>{});});
 await page.clock.fastForward(10000);await page.clock.fastForward(21000);await expect(ticket(page).getByLabel('Order valuation')).toContainText('Quote stale');await expect(sl(page)).toHaveText('-500.00 bps · Account amount unavailable');
});

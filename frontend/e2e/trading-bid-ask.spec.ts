import {test,expect,type Page} from '@playwright/test';
import {openTestApp} from './helpers/testApp';

const ticket=(page:Page)=>page.getByRole('dialog',{name:'Market order',exact:true});
async function setup(page:Page,exchange='dukascopy'){
 await openTestApp(page);
 await page.evaluate(async exchange=>{
  if(exchange==='dukascopy')await window.go!.main!.App!.ConfigureDucascopy({username:'bid-ask-demo',password:'explicit-offline-fixture',environment:'demo'});
  else await window.go!.main!.App!.ConfigureHyperliquid({accountAddress:'0x'+'1'.repeat(40),privateKey:'2'.repeat(64),network:'testnet'});
 },exchange);
 await page.getByRole('combobox',{name:'Exchange',exact:true}).selectOption(exchange);
}
async function openOrder(page:Page,side='Buy'){
 await page.getByTestId('chart-canvas').click({button:'right',position:{x:350,y:180}});
 await page.getByRole('menuitem',{name:`${side} at market…`,exact:true}).click();await expect(ticket(page)).toBeVisible();
}

for(const side of ['Buy','Sell'])test(`${side} ticket shows both actual quote sides and freezes its reviewed pair`,async({page},testInfo)=>{
 await setup(page);await page.evaluate(side=>(window as any).__testSetTradeValuation({entryPrice:side==='Buy'?'101.12345':'99.12345',bidPrice:'99.12345',askPrice:'101.12345',accountCurrency:'USD',profitPerPriceUnit:'1'}),side);
 await openOrder(page,side);
 await expect(ticket(page).getByLabel('Order bid price',{exact:true})).toHaveText('99.12345');await expect(ticket(page).getByLabel('Order ask price',{exact:true})).toHaveText('101.12345');
 await ticket(page).getByLabel('Order quantity',{exact:true}).fill('1000');await ticket(page).getByRole('button',{name:'Review order',exact:true}).click();
 await expect(ticket(page).getByLabel('Review bid price',{exact:true})).toHaveText('99.12345');await expect(ticket(page).getByLabel('Review ask price',{exact:true})).toHaveText('101.12345');
 await expect(ticket(page).getByLabel('Order review',{exact:true})).toContainText('Bid / Ask');await expect(ticket(page)).not.toContainText('Indicative market entry');
 await page.clock.install();const reads=await page.evaluate(()=>(window as any).__testTradeValuationReads());
 await page.evaluate(()=>(window as any).__testSetTradeValuation({entryPrice:'201.12345',bidPrice:'199.12345',askPrice:'201.12345'}));await page.clock.fastForward(12000);
 expect(await page.evaluate(()=>(window as any).__testTradeValuationReads())).toBe(reads);
 await expect(ticket(page).getByLabel('Review bid price',{exact:true})).toHaveText('99.12345');await expect(ticket(page).getByLabel('Review ask price',{exact:true})).toHaveText('101.12345');
 await page.screenshot({path:testInfo.outputPath(`ticket-${side.toLowerCase()}-bid-ask.png`)});
 await ticket(page).getByRole('button',{name:'Refresh quote',exact:true}).click();await expect(ticket(page).getByLabel('Review bid price',{exact:true})).toHaveCount(0);
 await expect(ticket(page).getByLabel('Order bid price',{exact:true})).toHaveText('199.12345');await expect(ticket(page).getByLabel('Order ask price',{exact:true})).toHaveText('201.12345');
 expect(await page.evaluate(()=>(window as any).__testTradeSubmissions())).toEqual([]);
});

test('Hyperliquid ticket preserves sub-cent bid/ask precision instead of rounding the spread away',async({page})=>{
 await setup(page,'hyperliquid');await page.evaluate(()=>(window as any).__testSetHyperliquidTradeValuation({entryPrice:'0.00125',bidPrice:'0.00123',askPrice:'0.00125'}));await openOrder(page);
 await expect(ticket(page).getByLabel('Order bid price',{exact:true})).toHaveText('0.00123');await expect(ticket(page).getByLabel('Order ask price',{exact:true})).toHaveText('0.00125');
 await ticket(page).getByLabel('Order quantity',{exact:true}).fill('1');await ticket(page).getByRole('button',{name:'Review order',exact:true}).click();
 await expect(ticket(page).getByLabel('Review bid price',{exact:true})).toHaveText('0.00123');await expect(ticket(page).getByLabel('Review ask price',{exact:true})).toHaveText('0.00125');
 expect(await page.evaluate(()=>(window as any).__testTradeSubmissions())).toEqual([]);
});

test('missing or crossed quote sides are unavailable and a stalled quote is visibly stale',async({page})=>{
 await setup(page);await page.clock.install();await page.evaluate(()=>(window as any).__testSetTradeValuation({entryPrice:'100',bidPrice:undefined,askPrice:undefined}));await openOrder(page);
 await expect(ticket(page).getByLabel('Order bid price',{exact:true})).toHaveText('Unavailable');await expect(ticket(page).getByLabel('Order ask price',{exact:true})).toHaveText('Unavailable');
 await expect(ticket(page).getByLabel('Estimate entry basis',{exact:true})).toContainText('100');
 await page.evaluate(()=>(window as any).__testSetTradeValuation({bidPrice:'102',askPrice:'101'}));await ticket(page).getByRole('button',{name:'Refresh quote',exact:true}).click();
 await expect(ticket(page).getByLabel('Order bid price',{exact:true})).toHaveText('Unavailable');await expect(ticket(page).getByLabel('Order ask price',{exact:true})).toHaveText('Unavailable');
 await page.evaluate(()=>(window as any).__testSetTradeValuation({bidPrice:'99',askPrice:'101'}));await ticket(page).getByRole('button',{name:'Refresh quote',exact:true}).click();
 await expect(ticket(page).getByLabel('Order bid price',{exact:true})).toHaveText('99.0000');await expect(ticket(page).getByLabel('Order ask price',{exact:true})).toHaveText('101.0000');
 await page.evaluate(()=>{window.go!.main!.App!.EstimateTrade=()=>new Promise(()=>{});});await page.clock.fastForward(31000);
 await expect(ticket(page).getByLabel('Order valuation',{exact:true})).toContainText('(stale)');await expect(ticket(page)).toContainText('Quote stale.');
 expect(await page.evaluate(()=>(window as any).__testTradeSubmissions())).toEqual([]);
});

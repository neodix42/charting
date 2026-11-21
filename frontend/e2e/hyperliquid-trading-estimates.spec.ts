import {test,expect,type Page} from '@playwright/test';
import {openTestApp} from './helpers/testApp';

const ticket=(page:Page)=>page.getByRole('dialog',{name:'Market order',exact:true});
const leverage=(page:Page)=>ticket(page).getByRole('combobox',{name:'Order leverage',exact:true});
const sl=(page:Page)=>ticket(page).getByLabel('Stop loss estimate',{exact:true});
const tp=(page:Page)=>ticket(page).getByLabel('Take profit estimate',{exact:true});
async function setup(page:Page){
 await openTestApp(page);await page.evaluate(()=>window.go!.main!.App!.ConfigureHyperliquid({accountAddress:'0x'+'1'.repeat(40),privateKey:'2'.repeat(64),network:'testnet'}));await page.getByRole('combobox',{name:'Exchange',exact:true}).selectOption('hyperliquid');
}
async function valuation(page:Page,patch:Record<string,unknown>,instrument='BTC'){await page.evaluate(({patch,instrument})=>(window as any).__testSetHyperliquidTradeValuation(patch,instrument),{patch,instrument});}
async function openOrder(page:Page,side='Buy'){
 await page.getByTestId('chart-canvas').click({button:'right',position:{x:350,y:180}});await page.getByRole('menuitem',{name:`${side} at market…`,exact:true}).click();await expect(ticket(page)).toBeVisible();
}
async function selectBuilder(page:Page){await page.getByRole('button',{name:'Select instrument'}).click();await page.getByRole('button',{name:'xyz:XYZ100 Perpetual',exact:true}).click();}

test('leverage choices wait for the real asset limit, preserve valid choices and clamp refreshed limits',async({page})=>{
 await setup(page);await page.clock.install();await page.evaluate(()=>{const api=window.go!.main!.App!,estimate=api.EstimateTrade;api.EstimateTrade=(key,side)=>new Promise(resolve=>{(window as any).__releaseLeverageQuote=async()=>resolve(await estimate(key,side));});});await openOrder(page);await ticket(page).getByLabel('Order quantity').fill('1');await expect(leverage(page)).toBeDisabled();await expect(leverage(page).getByRole('option')).toHaveCount(1);await expect(ticket(page).getByRole('button',{name:'Review order',exact:true})).toBeDisabled();
 await page.evaluate(()=>(window as any).__releaseLeverageQuote());await expect(leverage(page)).toBeEnabled();await expect(leverage(page)).toHaveValue('40');await expect(leverage(page).getByRole('option')).toHaveCount(40);await expect(leverage(page).getByRole('option').first()).toHaveText('1×');await expect(leverage(page).getByRole('option').last()).toHaveText('40×');await expect(ticket(page).getByLabel('Maximum leverage')).toHaveText('Max 40×');await leverage(page).selectOption('20');
 await valuation(page,{maxLeverage:5});await page.clock.fastForward(10000);await page.evaluate(()=>(window as any).__releaseLeverageQuote());await expect(leverage(page)).toHaveValue('5');await expect(leverage(page).getByRole('option')).toHaveCount(5);await expect(ticket(page)).toContainText('Leverage adjusted to 5×');await leverage(page).selectOption('3');await valuation(page,{maxLeverage:10});await page.clock.fastForward(10000);await page.evaluate(()=>(window as any).__releaseLeverageQuote());await expect(leverage(page)).toHaveValue('3');await expect(ticket(page).getByLabel('Maximum leverage')).toHaveText('Max 10×');
});

test('long USDC estimates update quantity, levels and entry without multiplying P/L by leverage',async({page})=>{
 await setup(page);await page.clock.install();await valuation(page,{entryPrice:'100',accountCurrency:'USDC',profitPerPriceUnit:'1'});await openOrder(page);await ticket(page).getByLabel('Order quantity').fill('2');await ticket(page).getByLabel('Order stop loss').fill('95');await ticket(page).getByLabel('Order take profit').fill('110');await expect(sl(page)).toHaveText('-500.00 bps · -10.00 USDC');await expect(tp(page)).toHaveText('+1,000.00 bps · +20.00 USDC');await leverage(page).selectOption('20');await expect(sl(page)).toHaveText('-500.00 bps · -10.00 USDC');
 await ticket(page).getByLabel('Order quantity').fill('4');await ticket(page).getByLabel('Order stop loss').fill('98');await ticket(page).getByLabel('Order take profit').fill('115');await expect(sl(page)).toHaveText('-200.00 bps · -8.00 USDC');await expect(tp(page)).toHaveText('+1,500.00 bps · +60.00 USDC');await valuation(page,{entryPrice:'120'});await ticket(page).getByRole('button',{name:'Refresh quote',exact:true}).click();await expect(sl(page)).toHaveText('-1,833.33 bps · -88.00 USDC');await expect(tp(page)).toHaveText('-416.67 bps · -20.00 USDC');expect(await page.evaluate(()=>(window as any).__testTradeSubmissions())).toEqual([]);
});

test('builder short uses its USDT collateral and frozen reviewed estimates until a new review',async({page})=>{
 await setup(page);await page.clock.install();await selectBuilder(page);await valuation(page,{entryPrice:'200',accountCurrency:'USDT',profitPerPriceUnit:'1'},'xyz:XYZ100');await openOrder(page,'Sell');await expect(ticket(page).getByLabel('Maximum leverage')).toHaveText('Max 5×');await ticket(page).getByLabel('Order quantity').fill('10');await ticket(page).getByLabel('Order stop loss').fill('210');await ticket(page).getByLabel('Order take profit').fill('180');await leverage(page).selectOption('4');await expect(sl(page)).toHaveText('-500.00 bps · -100.00 USDT');await expect(tp(page)).toHaveText('+1,000.00 bps · +200.00 USDT');await ticket(page).getByRole('button',{name:'Review order',exact:true}).click();await expect(ticket(page).getByLabel('Review stop loss estimate',{exact:true})).toHaveText('-500.00 bps · -100.00 USDT');await expect(ticket(page).getByLabel('Review take profit estimate',{exact:true})).toHaveText('+1,000.00 bps · +200.00 USDT');await expect(leverage(page)).toBeDisabled();await expect(leverage(page)).toHaveValue('4');
 const reads=await page.evaluate(()=>(window as any).__testTradeValuationReads());await valuation(page,{entryPrice:'300',accountCurrency:'USDH',maxLeverage:2},'xyz:XYZ100');await page.clock.fastForward(12000);expect(await page.evaluate(()=>(window as any).__testTradeValuationReads())).toBe(reads);await expect(ticket(page).getByLabel('Review stop loss estimate')).toHaveText('-500.00 bps · -100.00 USDT');await expect(ticket(page).getByLabel('Maximum leverage')).toHaveText('Max 5×');await ticket(page).getByRole('button',{name:'Refresh quote',exact:true}).click();await expect(ticket(page).getByRole('button',{name:'Confirm Sell at market',exact:true})).toHaveCount(0);await expect(leverage(page)).toHaveValue('2');await expect(ticket(page).getByLabel('Maximum leverage')).toHaveText('Max 2×');await expect(sl(page)).toHaveText('+3,000.00 bps · +900.00 USDH');
});

test('unknown or stale limits disable leverage/review, but missing collateral never invents USD',async({page})=>{
 await setup(page);await page.clock.install();await valuation(page,{entryPrice:'100',maxLeverage:0});await openOrder(page);await ticket(page).getByLabel('Order quantity').fill('2');await ticket(page).getByLabel('Order stop loss').fill('95');await expect(leverage(page)).toBeDisabled();await expect(ticket(page).getByRole('button',{name:'Review order',exact:true})).toBeDisabled();await expect(ticket(page)).toContainText('current leverage limit is unavailable');
 await valuation(page,{maxLeverage:40,accountCurrency:'',profitPerPriceUnit:'',valuationError:'Collateral token metadata unavailable'});await ticket(page).getByRole('button',{name:'Refresh quote',exact:true}).click();await expect(leverage(page)).toBeEnabled();await expect(ticket(page).getByRole('button',{name:'Review order',exact:true})).toBeEnabled();await expect(sl(page)).toHaveText('-500.00 bps · Collateral amount unavailable');await expect(sl(page)).not.toContainText('USD');
 await page.evaluate(()=>{window.go!.main!.App!.EstimateTrade=()=>new Promise(()=>{});});await page.clock.fastForward(10000);await page.clock.fastForward(21000);await expect(leverage(page)).toBeDisabled();await expect(ticket(page).getByRole('button',{name:'Review order',exact:true})).toBeDisabled();await expect(ticket(page)).toContainText('Quote stale');
});

test('late metadata for an old asset cannot replace the newly selected asset limit',async({page})=>{
 await setup(page);await page.evaluate(()=>{const api=window.go!.main!.App!,estimate=api.EstimateTrade;api.EstimateTrade=(key,side)=>key.instrument==='BTC'?new Promise(resolve=>{(window as any).__releaseOldAsset=async()=>resolve(await estimate(key,side));}):estimate(key,side);});await openOrder(page);await expect(leverage(page)).toBeDisabled();await ticket(page).getByRole('button',{name:'Close market order',exact:true}).click();await selectBuilder(page);await openOrder(page);await expect(ticket(page).getByLabel('Maximum leverage')).toHaveText('Max 5×');await page.evaluate(()=>(window as any).__releaseOldAsset());await expect(ticket(page).getByLabel('Maximum leverage')).toHaveText('Max 5×');await expect(leverage(page).getByRole('option')).toHaveCount(5);
});

test('each new perpetual Buy or Sell defaults to its asset maximum while refresh preserves a manual leverage',async({page})=>{
 await setup(page);await openOrder(page);await expect(leverage(page)).toHaveValue('40');
 await leverage(page).selectOption('7');await ticket(page).getByRole('button',{name:'Refresh quote',exact:true}).click();await expect(leverage(page)).toHaveValue('7');
 await ticket(page).getByRole('button',{name:'Close market order',exact:true}).click();await openOrder(page,'Sell');await expect(leverage(page)).toHaveValue('40');
 await ticket(page).getByRole('button',{name:'Close market order',exact:true}).click();await selectBuilder(page);await openOrder(page);await expect(leverage(page)).toHaveValue('5');
 await expect(ticket(page).getByRole('region',{name:'Hyperliquid risk and margin',exact:true})).toContainText('Position sizing · target loss 50% of equity');
 await ticket(page).getByRole('button',{name:'Close market order',exact:true}).click();
 await page.evaluate(()=>window.go!.main!.App!.ConfigureHyperliquid({accountAddress:'0x'+'1'.repeat(40),privateKey:'2'.repeat(64),network:'mainnet'}));
 await page.getByRole('button',{name:'Select instrument',exact:true}).click();await page.getByRole('button',{name:'HYPE/USDC Spot',exact:true}).click();await openOrder(page);
 await expect(ticket(page).getByLabel('Order leverage',{exact:true})).toHaveValue('1×');await expect(ticket(page).getByRole('region',{name:'Hyperliquid risk and margin',exact:true})).toHaveCount(0);
});

test('perpetual risk sizing uses the provider stop and selected-leverage constraints without a Forex margin buffer',async({page})=>{
 await setup(page);await page.clock.install();await valuation(page,{entryPrice:'100',maxLeverage:5,accountCurrency:'USDC',profitPerPriceUnit:'1'});
 await page.evaluate(()=>{const api=window.go!.main!.App!,read=api.GetTradeMetrics;api.GetTradeMetrics=async request=>{
  const next=await read(request);
  // Explicit provider-result fixtures; production sizing is computed in Go.
  if(request.stopLoss==='75')return {...next,equity:'1000',riskSizingError:'Stop loss must precede isolated liquidation at the selected leverage'};
  if(request.stopLoss!=='95')return {...next,equity:'1000',riskSizingError:'Enter a stop loss'};
  return {...next,equity:'1000',availableBalanceCurrency:'USDC',riskSizingQuantity:'100',riskSizingSafeQuantity:request.leverage===1?'20':'10',riskSizingLoss:request.leverage===1?'100':'50',riskSizingLiquidationPrice:request.leverage===1?'0':'80',riskSizingCanTarget:false,riskSizingError:'',riskSizingBasis:'Provider-verified isolated margin and visible book-depth cap; stop precedes estimated liquidation.'};
 };});
 await openOrder(page);await expect(leverage(page)).toHaveValue('5');const risk=ticket(page).getByRole('region',{name:'Hyperliquid risk and margin',exact:true});
 await expect(risk).toContainText('Enter a stop loss');await expect(risk).not.toContainText('150%');await expect(risk).not.toContainText('Forex');
 await ticket(page).getByLabel('Order stop loss',{exact:true}).fill('95');await page.clock.fastForward(350);
 await expect(risk).toContainText('50% risk quantity: 100');await expect(risk).toContainText('Risk-sized quantity (margin and liquidity capped): 10');await expect(risk).toContainText('50 USDC');await expect(risk).toContainText('5% equity');await expect(risk).toContainText('liquidation price: 80');
 await risk.getByRole('button',{name:'Use risk-sized quantity',exact:true}).click();await expect(ticket(page).getByLabel('Order quantity',{exact:true})).toHaveValue('10');
 expect(await page.evaluate(()=>(window as any).__testTradeRequests())).toEqual([]);expect(await page.evaluate(()=>(window as any).__testTradeSubmissions())).toEqual([]);
 await leverage(page).selectOption('1');await page.clock.fastForward(350);await expect(risk).toContainText('Risk-sized quantity (margin and liquidity capped): 20');await expect(risk).toContainText('No positive price threshold');
 expect((await page.evaluate(()=>(window as any).__testTradeMetricRequests())).at(-1)).toMatchObject({stopLoss:'95',leverage:1});
 await leverage(page).selectOption('5');await ticket(page).getByLabel('Order stop loss',{exact:true}).fill('75');await page.clock.fastForward(350);await expect(risk).toContainText('Stop loss must precede isolated liquidation');await expect(risk.getByRole('button',{name:'Use risk-sized quantity',exact:true})).toHaveCount(0);
});

test('perpetual risk suggestions expire when account reads stall and independent balances survive an unavailable risk calculation',async({page})=>{
 await setup(page);await page.clock.install();await valuation(page,{entryPrice:'100',maxLeverage:1});
 await page.evaluate(()=>(window as any).__testSetTradeMetrics({availableBalance:'500',equity:'1000',riskSizingError:'Existing netted position risk cannot be sized as a standalone order'},'hyperliquid','BTC'));
 await openOrder(page);await ticket(page).getByLabel('Order stop loss',{exact:true}).fill('95');await page.clock.fastForward(350);const risk=ticket(page).getByRole('region',{name:'Hyperliquid risk and margin',exact:true});
 await expect(risk).toContainText('Existing netted position');await expect(ticket(page).getByLabel('Available balance',{exact:true})).toHaveText('500.00 USDC');
 await page.evaluate(()=>(window as any).__testSetTradeMetrics({riskSizingQuantity:'100',riskSizingSafeQuantity:'5',riskSizingLoss:'25',riskSizingLiquidationPrice:'0',riskSizingCanTarget:false,riskSizingError:'',riskSizingBasis:'Verified isolated result fixture'},'hyperliquid','BTC'));
 await ticket(page).getByRole('button',{name:'Refresh quote',exact:true}).click();await page.clock.fastForward(350);await expect(risk.getByRole('button',{name:'Use risk-sized quantity',exact:true})).toBeEnabled();
 await page.evaluate(()=>{window.go!.main!.App!.GetTradeMetrics=()=>new Promise(()=>{});});await page.clock.fastForward(10000);await page.clock.fastForward(21000);
 await expect(risk.getByRole('button',{name:'Use risk-sized quantity',exact:true})).toHaveCount(0);await expect(risk).toContainText('Risk sizing requires a fresh account snapshot');
});

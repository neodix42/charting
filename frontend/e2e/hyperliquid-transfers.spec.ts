import {test,expect,type Page} from '@playwright/test';
import {openTestApp} from './helpers/testApp';

const ticket=(page:Page)=>page.getByRole('dialog',{name:'Market order',exact:true});
const panel=(page:Page)=>ticket(page).getByRole('region',{name:'Hyperliquid balances',exact:true});
const transfer=(page:Page)=>panel(page).getByRole('form',{name:'Internal transfer',exact:true});
async function setup(page:Page){
 await openTestApp(page);
 await page.evaluate(()=>window.go!.main!.App!.ConfigureHyperliquid({accountAddress:'0x'+'1'.repeat(40),privateKey:'2'.repeat(64),network:'testnet'}));
 await page.getByRole('combobox',{name:'Exchange',exact:true}).selectOption('hyperliquid');
}
async function openOrder(page:Page){
 await page.getByTestId('chart-canvas').click({button:'right',position:{x:350,y:180}});
 await page.getByRole('menuitem',{name:'Buy at market…',exact:true}).click();
 await expect(ticket(page)).toBeVisible();
 await expect(panel(page).getByLabel('Main perps available',{exact:true})).toHaveText(/1,000\.00 USDC/);
}
async function openTransfer(page:Page){await panel(page).getByRole('button',{name:'Move assets between balances…',exact:true}).click();await expect(transfer(page)).toBeVisible();}
async function reviewTransfer(page:Page,amount='25.5',destination='xyz'){
 await openTransfer(page);await transfer(page).getByLabel('Transfer to',{exact:true}).selectOption(destination);await transfer(page).getByLabel('Transfer amount',{exact:true}).fill(amount);await transfer(page).getByRole('button',{name:'Review transfer',exact:true}).click();await expect(panel(page).getByRole('region',{name:'Transfer review',exact:true})).toBeVisible();
}

test('Hyperliquid order tickets show separate known-zero balances and one shared account address',async({page})=>{
 await setup(page);await openOrder(page);
 await expect(panel(page)).toContainText('Manual / Standard');await expect(panel(page)).toContainText('0x'+'1'.repeat(40));
 await expect(panel(page).getByLabel('xyz available',{exact:true})).toHaveText(/0\.00 USDC/);
 await expect(panel(page).getByLabel('para available',{exact:true})).toHaveText(/0\.00 USDC/);
 await expect(panel(page).getByLabel('Spot USDC available',{exact:true})).toHaveText(/10\.00 USDC/);
 await expect(panel(page).getByLabel('Spot HYPE available',{exact:true})).toHaveText(/2\.00 HYPE/);
 await expect(panel(page)).toContainText('no separate DEX address is needed');
 await expect(panel(page).getByText('Other DEXs (1)',{exact:true})).toBeVisible();
 await page.screenshot({path:'../.tools/verification/hyperliquid-ticket-initial-1600.png'});
 await panel(page).screenshot({path:'../.tools/verification/hyperliquid-balances-panel.png'});
 expect(await page.evaluate(()=>(window as any).__testHyperliquidTransferSubmissions())).toEqual([]);
});

test('a reviewed internal transfer clears the order review, retains inputs and refreshes source and destination balances',async({page})=>{
 await setup(page);await openOrder(page);
 await ticket(page).getByLabel('Order quantity',{exact:true}).fill('.001');await ticket(page).getByLabel('Order stop loss',{exact:true}).fill('59000');await ticket(page).getByLabel('Order take profit',{exact:true}).fill('65000');
 await ticket(page).getByRole('button',{name:'Review order',exact:true}).click();await expect(ticket(page).getByLabel('Order review',{exact:true})).toBeVisible();
 await reviewTransfer(page);
 await expect(ticket(page).getByRole('button',{name:'Confirm Buy at market',exact:true})).toHaveCount(0);
 const review=panel(page).getByRole('region',{name:'Transfer review',exact:true});
 await expect(review).toContainText('testnet');await expect(review).toContainText('25.5 USDC');await expect(review).toContainText('974.5 USDC');await expect(review).toContainText('0x'+'1'.repeat(40));
 expect(await page.evaluate(()=>(window as any).__testHyperliquidTransferSubmissions())).toEqual([]);
 await review.getByRole('button',{name:'Confirm transfer',exact:true}).click();await expect(panel(page).getByRole('heading',{name:'Transfer confirmed',exact:true})).toBeVisible();
 await expect(panel(page).getByLabel('Main perps available',{exact:true})).toHaveText(/974\.50 USDC/);await expect(panel(page).getByLabel('xyz available',{exact:true})).toHaveText(/25\.50 USDC/);
 expect((await page.evaluate(()=>(window as any).__testHyperliquidTransferSubmissions())).length).toBe(1);
 expect(await page.evaluate(()=>(window as any).__testTradeSubmissions())).toEqual([]);
 await panel(page).getByRole('button',{name:'Close transfer',exact:true}).click();
 await expect(ticket(page).getByLabel('Order quantity',{exact:true})).toHaveValue('.001');await expect(ticket(page).getByLabel('Order stop loss',{exact:true})).toHaveValue('59000');await expect(ticket(page).getByLabel('Order take profit',{exact:true})).toHaveValue('65000');
 await expect(ticket(page).getByRole('button',{name:'Review order',exact:true})).toBeEnabled();
});

test('transfer routes require matching collateral and enforce exact source amounts and token precision',async({page})=>{
 await setup(page);await openOrder(page);await openTransfer(page);
 await transfer(page).getByLabel('Transfer to',{exact:true}).selectOption('cash');await expect(transfer(page)).toContainText('no compatible collateral token');await expect(transfer(page).getByRole('button',{name:'Review transfer',exact:true})).toBeDisabled();
 await transfer(page).getByLabel('Transfer from',{exact:true}).selectOption('spot');await expect(transfer(page).getByLabel('Transfer token',{exact:true})).toHaveValue('1');
 await transfer(page).getByLabel('Transfer amount',{exact:true}).fill('25.000000001');await expect(transfer(page)).toContainText('at most 8 decimal places');await expect(transfer(page).getByRole('button',{name:'Review transfer',exact:true})).toBeDisabled();
 await transfer(page).getByLabel('Transfer amount',{exact:true}).fill('26');await expect(transfer(page)).toContainText('exceeds');
 await transfer(page).getByRole('button',{name:'Max',exact:true}).click();await expect(transfer(page).getByLabel('Transfer amount',{exact:true})).toHaveValue('25');await expect(transfer(page).getByRole('button',{name:'Review transfer',exact:true})).toBeEnabled();
 await transfer(page).getByRole('button',{name:'Review transfer',exact:true}).click();await expect(panel(page).getByRole('region',{name:'Transfer review',exact:true})).toContainText('25 USDT');
 expect(await page.evaluate(()=>(window as any).__testHyperliquidTransferSubmissions())).toEqual([]);
});

test('pending transfer submissions block duplicate clicks, close and workspace keyboard navigation',async({page})=>{
 await setup(page);await page.evaluate(()=>{const api=window.go!.main!.App!,submit=api.SubmitHyperliquidTransfer;(window as any).__transferStarts=0;api.SubmitHyperliquidTransfer=id=>new Promise(resolve=>{(window as any).__transferStarts++;(window as any).__releaseTransfer=async()=>resolve(await submit(id));});});
 await openOrder(page);await ticket(page).getByLabel('Order quantity',{exact:true}).fill('.001');await reviewTransfer(page);
 await panel(page).getByRole('button',{name:'Confirm transfer',exact:true}).evaluate(button=>{(button as HTMLButtonElement).click();(button as HTMLButtonElement).click();});
 await expect.poll(()=>page.evaluate(()=>(window as any).__transferStarts)).toBe(1);await expect(ticket(page).getByRole('button',{name:'Close market order',exact:true})).toBeDisabled();
 await page.keyboard.press('Escape');await page.keyboard.press('Control+t');await page.keyboard.press('Control+Tab');await expect(ticket(page)).toBeVisible();await expect(page.locator('[role="tab"][data-tab-id]')).toHaveCount(1);
 expect(await page.evaluate(()=>(window as any).__testTradeSubmissions())).toEqual([]);
 await page.evaluate(()=>window.dispatchEvent(new Event('cryptodesk:trading-configured')));await expect(ticket(page)).toContainText('settings changed');
 await page.evaluate(()=>(window as any).__releaseTransfer());await expect(panel(page).getByRole('heading',{name:'Transfer confirmed',exact:true})).toBeVisible();
 expect((await page.evaluate(()=>(window as any).__testHyperliquidTransferSubmissions())).length).toBe(1);
 await panel(page).getByRole('button',{name:'Close transfer',exact:true}).click();await expect(ticket(page).getByRole('button',{name:'Review order',exact:true})).toBeDisabled();
});

test('uncertain transfer outcomes stay visible without retry and require a new empty transfer draft',async({page})=>{
 await setup(page);await page.evaluate(()=>(window as any).__testSetHyperliquidTransferOutcome('uncertain'));await openOrder(page);await reviewTransfer(page);
 await panel(page).getByRole('button',{name:'Confirm transfer',exact:true}).click();await expect(panel(page).getByRole('heading',{name:'Transfer status unknown',exact:true})).toBeVisible();
 await expect(panel(page)).toContainText('will not be resent');await expect(panel(page).getByRole('button',{name:'Confirm transfer',exact:true})).toHaveCount(0);
 expect((await page.evaluate(()=>(window as any).__testHyperliquidTransferSubmissions())).length).toBe(1);
 await expect(panel(page).getByRole('button',{name:'Start a new transfer',exact:true})).toBeEnabled();await panel(page).getByRole('button',{name:'Start a new transfer',exact:true}).click();await expect(transfer(page).getByLabel('Transfer amount',{exact:true})).toHaveValue('');
 expect((await page.evaluate(()=>(window as any).__testHyperliquidTransferSubmissions())).length).toBe(1);
});

test('shared collateral is shown once through spot and mismatched transfer reviews cannot be confirmed',async({page})=>{
 await setup(page);await page.evaluate(async()=>{const api=window.go!.main!.App!,value=await api.GetHyperliquidBalances();value.shared=true;value.accountMode='unifiedAccount';value.venues=value.venues.map(venue=>venue.market==='perpetual'?{...venue,shared:true,balances:[]}:{...venue,shared:true,balances:venue.balances.map(token=>({...token,available:undefined,availableError:'Shared account free trading balance is unavailable'}))});(window as any).__testSetHyperliquidBalances(value);});await openOrderShared(page);
 await expect(panel(page)).toContainText('Unified Account');await expect(panel(page).getByLabel('Main perps available',{exact:true})).toHaveText('See spot balances');await expect(panel(page).getByRole('button',{name:'Move assets between balances…',exact:true})).toBeDisabled();
 await expect(panel(page).getByLabel('Spot USDC holdings',{exact:true})).toContainText('10.00 USDC');await expect(panel(page).getByLabel('Spot USDC holdings',{exact:true})).toContainText('free trading balance is not determined');
 await ticket(page).getByRole('button',{name:'Close market order',exact:true}).click();await page.evaluate(()=>{(window as any).__testSetHyperliquidBalances({});const api=window.go!.main!.App!,preview=api.PreviewHyperliquidTransfer;api.PreviewHyperliquidTransfer=async request=>({...await preview(request),accountAddress:'0x'+'f'.repeat(40)});});await openOrder(page);await openTransfer(page);await transfer(page).getByLabel('Transfer amount',{exact:true}).fill('1');await transfer(page).getByRole('button',{name:'Review transfer',exact:true}).click();
 await expect(panel(page).getByRole('alert')).toContainText('does not match');await expect(panel(page).getByRole('button',{name:'Confirm transfer',exact:true})).toHaveCount(0);expect(await page.evaluate(()=>(window as any).__testHyperliquidTransferSubmissions())).toEqual([]);
});

async function openOrderShared(page:Page){await page.getByTestId('chart-canvas').click({button:'right',position:{x:350,y:180}});await page.getByRole('menuitem',{name:'Buy at market…',exact:true}).click();await expect(panel(page)).toBeVisible();}

test('late overview responses cannot revive a ticket after credentials change',async({page})=>{
 await setup(page);await page.evaluate(()=>{const api=window.go!.main!.App!,read=api.GetHyperliquidBalances;api.GetHyperliquidBalances=()=>new Promise(resolve=>{(window as any).__releaseOldBalances=async()=>resolve(await read());});});
 await page.getByTestId('chart-canvas').click({button:'right',position:{x:350,y:180}});await page.getByRole('menuitem',{name:'Buy at market…',exact:true}).click();await expect(panel(page)).toContainText('Loading Hyperliquid balances');
 await page.evaluate(()=>window.dispatchEvent(new Event('cryptodesk:trading-configured')));await expect(ticket(page)).toHaveCount(0);await page.evaluate(()=>(window as any).__releaseOldBalances());await expect(ticket(page)).toHaveCount(0);
});

test('stale and wrong-network balances cannot authorize transfers and compact review stays reachable',async({page})=>{
 await page.setViewportSize({width:1100,height:600});await setup(page);await page.clock.install();await openOrder(page);await page.screenshot({path:'../.tools/verification/hyperliquid-ticket-initial-1100.png'});await reviewTransfer(page,'1');
 const confirm=panel(page).getByRole('button',{name:'Confirm transfer',exact:true});await confirm.scrollIntoViewIfNeeded();await expect(confirm).toBeInViewport();await ticket(page).screenshot({path:'../.tools/verification/hyperliquid-transfer-compact.png'});
 await panel(page).press('Escape');await expect(ticket(page)).toBeVisible();
 await page.evaluate(()=>{window.go!.main!.App!.GetHyperliquidBalances=()=>new Promise(()=>{});});await page.clock.fastForward(31000);
 await expect(panel(page).getByLabel('Main perps available',{exact:true})).toContainText('Unavailable');await expect(panel(page).getByRole('button',{name:'Move assets between balances…',exact:true})).toBeDisabled();
 await ticket(page).getByRole('button',{name:'Close market order',exact:true}).click();
 await page.evaluate(()=>{window.go!.main!.App!.GetHyperliquidBalances=async()=>({accountAddress:'0x'+'1'.repeat(40),network:'mainnet',accountMode:'disabled',shared:false,observedAt:Date.now(),venues:[],tokens:[]});});
 await openOrderShared(page);await expect(panel(page).getByRole('alert')).toContainText('do not match');await expect(panel(page).getByRole('button',{name:'Move assets between balances…',exact:true})).toBeDisabled();
 expect(await page.evaluate(()=>(window as any).__testHyperliquidTransferSubmissions())).toEqual([]);
});

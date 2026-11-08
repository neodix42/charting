import {test,expect,type Page} from '@playwright/test';
import {openTestApp,reloadTestApp} from './helpers/testApp';

const address='0x'+'1'.repeat(40),key='2'.repeat(64);
const settings=(page:Page)=>page.getByRole('dialog',{name:'Workspace settings',exact:true});
const hyperliquid=(page:Page)=>page.getByRole('form',{name:'Hyperliquid settings',exact:true});
const ticket=(page:Page)=>page.getByRole('dialog',{name:'Market order',exact:true});
async function openSettings(page:Page){await page.getByRole('button',{name:'Open settings'}).click();await settings(page).getByRole('tab',{name:'Hyperliquid',exact:true}).click();await expect(hyperliquid(page).getByLabel('Hyperliquid account address')).toBeEnabled();}
async function configured(page:Page,exchange='hyperliquid'){
 await page.evaluate(async({address,key,exchange})=>{if(exchange==='hyperliquid')await window.go!.main!.App!.ConfigureHyperliquid({accountAddress:address,privateKey:key,network:'testnet'});else await window.go!.main!.App!.ConfigureDucascopy({username:'demo-trading',password:'fixture-only-password',environment:'demo'});},{address,key,exchange});
 await page.getByRole('combobox',{name:'Exchange',exact:true}).selectOption(exchange);
}
async function openOrder(page:Page,side='Buy'){
 await page.getByTestId('chart-canvas').click({button:'right',position:{x:350,y:180}});
 await page.getByRole('menuitem',{name:`${side} at market…`,exact:true}).click();await expect(ticket(page)).toBeVisible();
}

test('Hyperliquid API settings keep secrets write-only, explain approval and test only when requested',async({page})=>{
 await openTestApp(page);await openSettings(page);
 const form=hyperliquid(page),secret=form.getByLabel('Hyperliquid API private key');
 await expect(secret).toHaveAttribute('type','password');await expect(secret).toHaveAttribute('autocomplete','off');await expect(form.getByRole('button',{name:'Test connection',exact:true})).toBeDisabled();
 await form.getByRole('button',{name:'How to create a Hyperliquid API key'}).click();await expect(page.getByRole('dialog',{name:'Create a Hyperliquid API key'})).toContainText('never your main wallet’s private key');await page.getByRole('button',{name:'Official API wallet documentation',exact:true}).click();expect(await page.evaluate(()=>(window as any).__testHyperliquidHelp)).toBe('docs');await page.getByRole('button',{name:'Close API key help'}).click();
 await form.getByLabel('Hyperliquid account address').fill(address);await form.getByLabel('Hyperliquid network').selectOption('testnet');await secret.fill(key);await form.getByRole('button',{name:'Save credentials',exact:true}).click();await expect(form.getByRole('status')).toHaveText('Hyperliquid credentials saved.');await expect(secret).toHaveValue('');
 expect(await page.evaluate(()=>(window as any).__testHyperliquidTests())).toBe(0);
 expect(JSON.stringify(await page.evaluate(()=>window.go!.main!.App!.GetHyperliquid()))).not.toContain(key);
 expect(JSON.stringify(await page.evaluate(()=>(window as any).__testWorkspace()))).not.toContain(address);
 await form.getByRole('button',{name:'Test connection',exact:true}).click();await expect(form.getByRole('status')).toContainText('API agent verified. No order was placed.');expect(await page.evaluate(()=>(window as any).__testTradeSubmissions())).toEqual([]);
 await reloadTestApp(page);await openSettings(page);await expect(secret).toHaveValue('');await expect(secret).toHaveAttribute('placeholder','Saved key · leave blank to keep');
 await form.getByRole('checkbox',{name:'Remove saved API key'}).check();await form.getByRole('button',{name:'Save credentials',exact:true}).click();await expect(form.getByRole('status')).toHaveText('Saved Hyperliquid API key removed.');
});

test('configured perpetual charts require review and explicit one-shot market confirmation',async({page})=>{
 await openTestApp(page);await page.getByRole('combobox',{name:'Exchange',exact:true}).selectOption('hyperliquid');await page.getByTestId('chart-canvas').click({button:'right',position:{x:350,y:180}});await expect(page.getByRole('menu',{name:'Chart trading'})).toHaveCount(0);
 await configured(page);await openOrder(page);
 await ticket(page).getByLabel('Order quantity').fill('0.01');await ticket(page).getByLabel('Order stop loss').fill('60000');await ticket(page).getByLabel('Order take profit').fill('63000');await ticket(page).getByLabel('Order leverage').fill('3');await ticket(page).getByRole('button',{name:'Review order'}).click();await expect(ticket(page).getByLabel('Order review')).toContainText('61000');
 expect(await page.evaluate(()=>(window as any).__testTradeSubmissions())).toEqual([]);
 expect(await page.evaluate(()=>(window as any).__testTradeRequests())).toMatchObject([{side:'buy',quantity:'0.01',stopLoss:'60000',takeProfit:'63000',leverage:3,feedKey:{exchange:'hyperliquid',instrument:'BTC',market:'perpetual'}}]);
 await ticket(page).getByRole('button',{name:'Confirm Buy at market'}).click();await expect(ticket(page).getByRole('status')).toContainText('Order submitted');expect(await page.evaluate(()=>(window as any).__testTradeSubmissions())).toHaveLength(1);await expect(ticket(page).getByRole('button',{name:'Confirm Buy at market'})).toHaveCount(0);
});

test('uncertain submission cannot be retried and pending submission cannot be double confirmed',async({page})=>{
 await openTestApp(page);await configured(page);await page.evaluate(()=>{window.go!.main!.App!.SubmitTrade=()=>new Promise((_resolve,reject)=>{(window as any).__rejectTrade=()=>reject(Error('transport failure'));});});await openOrder(page,'Sell');await ticket(page).getByLabel('Order quantity').fill('0.02');await ticket(page).getByRole('button',{name:'Review order'}).click();await ticket(page).getByRole('button',{name:'Confirm Sell at market'}).click();await expect(ticket(page).getByRole('button',{name:'Submitting…'})).toBeDisabled();await expect(ticket(page).getByRole('button',{name:'Close market order'})).toBeDisabled();await page.evaluate(()=>(window as any).__rejectTrade());await expect(ticket(page).getByRole('alert')).toContainText('Submission result unknown');await expect(ticket(page).getByRole('button',{name:/Confirm|Review order|Edit order/})).toHaveCount(0);
});

test('spot controls prohibit borrowing/protection and Dukascopy shows account leverage at review',async({page})=>{
 await openTestApp(page);await configured(page);await page.evaluate(async({address,key})=>window.go!.main!.App!.ConfigureHyperliquid({accountAddress:address,privateKey:key,network:'mainnet'}),{address,key});await page.getByRole('button',{name:'Select instrument'}).click();await page.getByRole('button',{name:'HYPE/USDC Spot',exact:true}).click();await openOrder(page,'Sell');await expect(ticket(page)).toContainText('does not open a short');await expect(ticket(page).getByLabel('Order stop loss')).toBeDisabled();await expect(ticket(page).getByLabel('Order take profit')).toBeDisabled();await expect(ticket(page).getByLabel('Order leverage')).toHaveValue('1×');await ticket(page).getByRole('button',{name:'Close market order'}).click();
 await configured(page,'dukascopy');await openOrder(page);await expect(ticket(page)).toContainText('cannot be changed per order');await expect(ticket(page).getByLabel('Order leverage')).toBeDisabled();await ticket(page).getByLabel('Order quantity').fill('1000');await ticket(page).getByLabel('Order stop loss').fill('1.09');await ticket(page).getByLabel('Order take profit').fill('1.12');await ticket(page).getByRole('button',{name:'Review order'}).click();await expect(ticket(page).getByLabel('Order review')).toContainText('100×');expect((await page.evaluate(()=>(window as any).__testTradeRequests())).at(-1)).toMatchObject({leverage:0,feedKey:{exchange:'dukascopy'}});
});

test('expired and stale previews never submit after chart or credential changes',async({page})=>{
 await openTestApp(page);await configured(page);await page.evaluate(()=>{const api=window.go!.main!.App!,preview=api.PreviewTrade;api.PreviewTrade=async request=>({...await preview(request),expiresAt:Date.now()-1});});await openOrder(page);await ticket(page).getByLabel('Order quantity').fill('1');await ticket(page).getByRole('button',{name:'Review order'}).click();await expect(ticket(page)).toContainText('This review expired');await expect(ticket(page).getByRole('button',{name:'Confirm Buy at market'})).toBeDisabled();await ticket(page).getByRole('button',{name:'Close market order'}).click();
 await page.evaluate(()=>{const api=window.go!.main!.App!,preview=api.PreviewTrade;api.PreviewTrade=request=>new Promise(resolve=>{(window as any).__resolvePreview=async()=>resolve(await preview(request));});});await openOrder(page);await ticket(page).getByLabel('Order quantity').fill('1');await ticket(page).getByRole('button',{name:'Review order'}).click();await page.evaluate(()=>window.dispatchEvent(new Event('cryptodesk:trading-configured')));await expect(ticket(page)).toHaveCount(0);await page.evaluate(()=>(window as any).__resolvePreview());await expect(ticket(page)).toHaveCount(0);expect(await page.evaluate(()=>(window as any).__testTradeSubmissions())).toEqual([]);
});

test('testnet spot menus explain unavailable matching and keyboard focus stays in an order',async({page})=>{
 await openTestApp(page);await configured(page);await page.getByRole('button',{name:'Select instrument'}).click();await page.getByRole('button',{name:'HYPE/USDC Spot',exact:true}).click();await page.getByTestId('chart-canvas').click({button:'right',position:{x:350,y:180}});const menu=page.getByRole('menu',{name:'Chart trading'});await expect(menu).toContainText('testnet spot trading is unavailable');await expect(menu.getByRole('menuitem',{name:'Buy at market…'})).toBeDisabled();await expect(menu.getByRole('menuitem',{name:'Sell at market…'})).toBeDisabled();await page.keyboard.press('Escape');
 await page.getByRole('button',{name:'Select instrument'}).click();await page.getByRole('button',{name:'BTC Perpetual',exact:true}).click();await openOrder(page);await ticket(page).getByLabel('Order quantity').fill('1');const review=ticket(page).getByRole('button',{name:'Review order'});await review.focus();await page.keyboard.press('Tab');await expect(ticket(page).getByRole('button',{name:'Close market order'})).toBeFocused();await page.keyboard.press('Shift+Tab');await expect(review).toBeFocused();
});

test('a safe settings error explains rejected API-wallet credentials and redacts echoed secrets',async({page})=>{
 await openTestApp(page);await openSettings(page);const form=hyperliquid(page);await page.evaluate(()=>{window.go!.main!.App!.ConfigureHyperliquid=async request=>{throw Error('Use a dedicated API agent, not the account wallet key: '+request.privateKey);};});await form.getByLabel('Hyperliquid account address').fill(address);await form.getByLabel('Hyperliquid API private key').fill(key);await form.getByRole('button',{name:'Save credentials',exact:true}).click();await expect(form.getByRole('alert')).toContainText('Use a dedicated API agent');await expect(form.getByRole('alert')).not.toContainText(key);await expect(form.getByRole('alert')).toContainText('[redacted]');
});

import {test,expect,type Page} from '@playwright/test';
import {openTestApp,reloadTestApp} from './helpers/testApp';
import type {TradeRecord} from '../src/workspace/tradeHistory';
const levels=(page:Page)=>page.getByRole('complementary',{name:'Open trade levels',exact:true});
const record=(patch:Partial<TradeRecord>={}):TradeRecord=>({id:'hyperliquid:'+'a'.repeat(32),provider:'hyperliquid',environment:'testnet',account:'0x'+'1'.repeat(40),instrument:'BTC',market:'perpetual',orderId:'101',submittedAt:Date.now()-2000,openDate:Date.now()-1000,closeDate:0,size:'0.01',remainingSize:'0.01',direction:'long',entryPrice:'61000',stopLoss:'60700',takeProfit:'61100',slAmount:'-3',tpAmount:'1',currency:'USDC',balanceAtOpen:'1000',balanceCurrency:'USDC',balanceTime:Date.now()-2000,marginUsed:'203.33',marginCurrency:'USDC',status:'open',canClose:true,closeReason:'Verified',checkedAt:Date.now(),...patch});
async function seed(page:Page,rows:TradeRecord[],notify=false){await page.evaluate(({rows,notify})=>{(window as any).__testSetTradeHistory(rows);if(notify)window.dispatchEvent(new Event('cryptodesk:trades-changed'));},{rows,notify});}
async function hyperliquid(page:Page){await page.evaluate(async()=>window.go!.main!.App!.ConfigureHyperliquid({accountAddress:'0x'+'1'.repeat(40),privateKey:'2'.repeat(64),network:'testnet'}));await page.getByRole('combobox',{name:'Exchange',exact:true}).selectOption('hyperliquid');}

test('open trade prices paint native horizontal lines, share one refresh across charts and survive reload',async({page})=>{
 await page.addInitScript(()=>{const original=CanvasRenderingContext2D.prototype.fillText;(window as any).__tradePaint=[];CanvasRenderingContext2D.prototype.fillText=function(...args:Parameters<CanvasRenderingContext2D['fillText']>){if(/entry #|SL #|TP #/.test(args[0]))(window as any).__tradePaint.push(args[0]);return original.apply(this,args);};});
 await openTestApp(page);await seed(page,[record(),record({id:'hyperliquid:'+'b'.repeat(32),instrument:'xyz:BTC',orderId:'outside-symbol'}),record({id:'hyperliquid:'+'c'.repeat(32),status:'closed',closeDate:Date.now(),remainingSize:'0',orderId:'closed-id'})]);await hyperliquid(page);
 await expect(levels(page).getByRole('listitem')).toHaveCount(3);await expect(levels(page)).toContainText('Long entry #101');await expect(levels(page)).toContainText('61,000.0');await expect(levels(page)).toContainText('SL #101');await expect(levels(page)).toContainText('TP #101');await expect(levels(page)).not.toContainText('outside-symbol');await expect(levels(page)).not.toContainText('closed-id');
 await expect.poll(()=>page.evaluate(()=>(window as any).__tradePaint)).toEqual(expect.arrayContaining(['Long entry #101','SL #101','TP #101']));
 await page.evaluate(()=>(window as any).__tradePaint=[]);await page.getByRole('button',{name:'Chart style',exact:true}).click();await expect.poll(()=>page.evaluate(()=>(window as any).__tradePaint)).toEqual(expect.arrayContaining(['Long entry #101','SL #101','TP #101']));
 const reads=await page.evaluate(()=>(window as any).__testChartTradeReads());await page.getByRole('button',{name:'Add chart',exact:true}).click();await expect(page.getByTestId('chart-cell')).toHaveCount(2);await expect(levels(page)).toHaveCount(2);expect(await page.evaluate(()=>(window as any).__testChartTradeReads())).toBe(reads);
 await page.locator('[data-tab-id]').first().dblclick();await page.getByRole('textbox',{name:'Tab name',exact:true}).fill('Trade levels');await page.getByRole('button',{name:'Rename',exact:true}).click();await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();await reloadTestApp(page);await expect(levels(page)).toHaveCount(2);await expect(levels(page).first()).toContainText('Long entry #101');
});

test('Dukascopy displays multiple orders and removes confirmed closed positions when returning from Trade history',async({page})=>{
 await openTestApp(page);await page.evaluate(async()=>window.go!.main!.App!.ConfigureDucascopy({username:'demo-trading',password:'fixture-password',environment:'demo'}));
 const first=record({id:'dukascopy:'+'d'.repeat(32),provider:'dukascopy',market:'jforex',instrument:'EURUSD',orderId:'D-101',environment:'demo',entryPrice:'1.1003',stopLoss:'1.09',takeProfit:'1.12'}),second={...first,id:'dukascopy:'+'e'.repeat(32),orderId:'D-102',direction:'short',stopLoss:'1.12',takeProfit:'1.09'};
 await seed(page,[first,second]);await page.getByRole('combobox',{name:'Exchange',exact:true}).selectOption('dukascopy');await expect(levels(page).getByRole('listitem')).toHaveCount(6);await expect(levels(page)).toContainText('Short entry #D-102');
 await page.getByRole('tab',{name:'Trade history',exact:true}).click();await expect(levels(page)).toHaveCount(0);const reads=await page.evaluate(()=>(window as any).__testChartTradeReads());await page.clock.install();await page.clock.fastForward(25000);expect(await page.evaluate(()=>(window as any).__testChartTradeReads())).toBe(reads);
 const history=page.getByRole('tabpanel',{name:'Trade history',exact:true});await history.getByRole('radio',{name:'Select trade dukascopy D-101',exact:true}).check();await expect(history.getByRole('button',{name:'Close',exact:true})).toBeEnabled();await history.getByRole('button',{name:'Close',exact:true}).click();await page.getByRole('dialog',{name:'Close trade',exact:true}).getByRole('button',{name:'Confirm market close',exact:true}).click();await expect(page.getByRole('dialog',{name:'Close trade',exact:true})).toHaveCount(0);
 await page.locator('[data-tab-id]').first().click();await expect(levels(page).getByRole('listitem')).toHaveCount(3);await expect(levels(page)).not.toContainText('D-101');await expect(levels(page)).toContainText('D-102');
});

test('trade updates repaint protection without changing the chart feed and discard late prices after source changes',async({page})=>{
 await openTestApp(page);await seed(page,[record()]);await hyperliquid(page);await expect(levels(page).getByRole('listitem')).toHaveCount(3);const before=await page.evaluate(()=>(window as any).__testAttachCount());
 await seed(page,[record({stopLoss:'60800',takeProfit:''})],true);await expect(levels(page).getByRole('listitem')).toHaveCount(2);await expect(levels(page)).toContainText('60,800.0');expect(await page.evaluate(()=>(window as any).__testAttachCount())).toBe(before);
 await page.evaluate(()=>{const api=window.go!.main!.App!,refresh=api.RefreshTradeHistory;api.RefreshTradeHistory=ids=>new Promise(resolve=>(window as any).__releaseTradeLevels=async()=>resolve(await refresh(ids)));window.dispatchEvent(new Event('cryptodesk:trades-changed'));});await expect(levels(page)).toHaveCount(0);await expect.poll(()=>page.evaluate(()=>typeof(window as any).__releaseTradeLevels)).toBe('function');
 await page.getByRole('combobox',{name:'Exchange',exact:true}).selectOption('binance');await page.evaluate(()=>(window as any).__releaseTradeLevels());await expect(levels(page)).toHaveCount(0);
});

test('a confirmed new chart trade refreshes its entry and protection immediately',async({page})=>{
 await openTestApp(page);await hyperliquid(page);await expect(levels(page)).toHaveCount(0);
 const position=record();await page.evaluate(position=>{const api=window.go!.main!.App!,submit=api.SubmitTrade;api.SubmitTrade=async id=>{const result=await submit(id);(window as any).__testSetTradeHistory([position]);return result;};},position);
 await page.getByTestId('chart-canvas').click({button:'right',position:{x:350,y:180}});await page.getByRole('menuitem',{name:'Buy at market…',exact:true}).click();const ticket=page.getByRole('dialog',{name:'Market order',exact:true});await ticket.getByLabel('Order quantity').fill('0.01');await ticket.getByLabel('Order stop loss').fill('60700');await ticket.getByLabel('Order take profit').fill('61100');await ticket.getByRole('button',{name:'Review order',exact:true}).click();await ticket.getByRole('button',{name:'Confirm Buy at market',exact:true}).click();await expect(ticket.getByRole('status')).toContainText('Order submitted');await ticket.getByRole('button',{name:'Close',exact:true}).click();await expect(levels(page).getByRole('listitem')).toHaveCount(3);
});


test('account changes clear old levels and a stalled refresh expires without overlapping requests',async({page})=>{
 await openTestApp(page);await seed(page,[record()]);await hyperliquid(page);await expect(levels(page).getByRole('listitem')).toHaveCount(3);await page.clock.install();
 await page.evaluate(()=>{const api=window.go!.main!.App!,refresh=api.RefreshTradeHistory;(window as any).__levelRefreshCalls=0;api.RefreshTradeHistory=ids=>{(window as any).__levelRefreshCalls++;return new Promise(resolve=>(window as any).__releaseOldAccountLevels=async()=>{api.RefreshTradeHistory=refresh;resolve(await refresh(ids));});};window.dispatchEvent(new Event('cryptodesk:trading-configured'));});
 await expect(levels(page)).toHaveCount(0);await expect.poll(()=>page.evaluate(()=>typeof(window as any).__releaseOldAccountLevels)).toBe('function');await page.clock.fastForward(36000);await expect(levels(page)).toContainText('Trade levels expired');expect(await page.evaluate(()=>(window as any).__levelRefreshCalls)).toBe(1);
 await page.evaluate(()=>(window as any).__releaseOldAccountLevels());await expect(levels(page).getByRole('listitem')).toHaveCount(0);
 await seed(page,[record({orderId:'new-account-order'})],true);await expect(levels(page).getByRole('listitem')).toHaveCount(3);await expect(levels(page)).toContainText('new-account-order');
});

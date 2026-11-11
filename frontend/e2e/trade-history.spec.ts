import {test,expect,type Page} from '@playwright/test';
import {openTestApp,reloadTestApp} from './helpers/testApp';
import type {TradeRecord} from '../src/workspace/tradeHistory';
const panel=(page:Page)=>page.getByRole('tabpanel',{name:'Trade history',exact:true});
const close=(page:Page)=>panel(page).getByRole('button',{name:'Close',exact:true});
const review=(page:Page)=>page.getByRole('dialog',{name:'Close trade',exact:true});
const record=(overrides:Partial<TradeRecord>={}):TradeRecord=>({id:'hyperliquid:'+'a'.repeat(32),provider:'hyperliquid',environment:'testnet',account:'0x'+'1'.repeat(40),instrument:'BTC',market:'perpetual',orderId:'101',submittedAt:Date.UTC(2026,10,11,7),openDate:Date.UTC(2026,10,11,7,0,1),closeDate:0,size:'0.01',remainingSize:'0.01',direction:'long',entryPrice:'61000',stopLoss:'60000',takeProfit:'63000',slAmount:'-10',tpAmount:'20',currency:'USDC',balanceAtOpen:'1000',balanceCurrency:'USDC',balanceTime:Date.UTC(2026,10,11,7),balanceBasis:'Submission-time exchange snapshot',marginUsed:'203.33',marginCurrency:'USDC',marginBasis:'Current margin for verified position',status:'open',canClose:true,closeReason:'Verified platform position',checkedAt:Date.now(),...overrides});
async function seed(page:Page,rows:TradeRecord[]){await page.evaluate(items=>(window as any).__testSetTradeHistory(items),rows);}
async function enter(page:Page){await page.getByRole('tab',{name:'Trade history',exact:true}).click();await expect(panel(page)).toBeVisible();await expect(panel(page).getByText('Refreshing exchange statuses… Saved records remain visible.',{exact:true})).toHaveCount(0);}

test('Trade history follows Predictions, stays lazy, labels actual currencies and pauses chart leases',async({page})=>{
 await openTestApp(page);await expect(page.getByTestId('chart-cell')).toHaveCount(1);expect(await page.evaluate(()=>(window as any).__testTradeHistoryReads())).toBe(0);
 await seed(page,[record(),record({id:'dukascopy:'+'b'.repeat(32),provider:'dukascopy',environment:'demo',account:'demo-account',instrument:'EUR/USD',orderId:'D-202',size:'1000',remainingSize:'1000',entryPrice:'1.1003',stopLoss:'1.09',takeProfit:'1.12',slAmount:'-9.27',tpAmount:'17.73',currency:'EUR',balanceAtOpen:'0',balanceCurrency:'EUR',marginUsed:'',marginCurrency:'',marginBasis:'JForex does not expose reliable per-position margin'})]);
 expect(await page.locator('.tab-row > button[role=tab]').allTextContents()).toEqual(['Screener','Predictions','Trade history']);await enter(page);
 await expect.poll(()=>page.evaluate(()=>(window as any).__testLeases())).toBe(0);await expect(page.getByTestId('chart-cell')).toHaveCount(0);
 const headers=await panel(page).getByRole('columnheader').allTextContents();for(const name of ['Order ID','Open date','Close date','Size','Direction','SL price','TP price','SL amount (est.)','TP amount (est.)','Balance at open','Margin used'])expect(headers).toContain(name);
 const eur=panel(page).getByRole('row').filter({hasText:'EUR/USD'});await expect(eur).toContainText('-9.27 EUR');await expect(eur).toContainText('17.73 EUR');await expect(eur).toContainText('0.00 EUR');await expect(eur.getByRole('cell').nth(14)).toHaveText('—');
 const btc=panel(page).getByRole('row').filter({hasText:'BTC'});await expect(btc).toContainText('-10.00 USDC');await expect(btc).toContainText('20.00 USDC');await expect(btc).toContainText('203.33 USDC');await expect(close(page)).toBeDisabled();
 await page.clock.install();const reads=await page.evaluate(()=>(window as any).__testTradeHistoryReads());await page.locator('[data-tab-id]').first().click();await expect.poll(()=>page.evaluate(()=>(window as any).__testLeases())).toBe(1);await page.clock.fastForward(30000);expect(await page.evaluate(()=>(window as any).__testTradeHistoryReads())).toBe(reads);
});

test('Close requires selection and confirmation, then stores the confirmed date across reload',async({page})=>{
 await openTestApp(page);await seed(page,[record(),record({id:'dukascopy:'+'b'.repeat(32),provider:'dukascopy',instrument:'EUR/USD',orderId:'D-202',status:'closed',canClose:false,remainingSize:'0',closeDate:Date.UTC(2026,10,11,8)})]);await enter(page);
 await panel(page).getByRole('radio',{name:'Select trade dukascopy D-202',exact:true}).check();await expect(close(page)).toBeDisabled();await panel(page).getByRole('radio',{name:'Select trade hyperliquid 101',exact:true}).check();await expect(close(page)).toBeEnabled();
 await close(page).click();await expect(review(page)).toContainText('Remaining size 0.01 base units');expect(await page.evaluate(()=>(window as any).__testTradeHistoryCloses())).toEqual([]);await review(page).getByRole('button',{name:'Cancel',exact:true}).click();expect(await page.evaluate(()=>(window as any).__testTradeHistoryCloses())).toEqual([]);
 await close(page).click();const cancel=review(page).getByRole('button',{name:'Cancel',exact:true}),confirm=review(page).getByRole('button',{name:'Confirm market close',exact:true});await cancel.focus();await page.keyboard.press('Shift+Tab');await expect(confirm).toBeFocused();await page.keyboard.press('Tab');await expect(cancel).toBeFocused();await confirm.click();await expect(review(page)).toHaveCount(0);
 const row=panel(page).getByRole('row').filter({hasText:'BTC'});await expect(row).toContainText('closed');await expect(row.getByRole('cell').nth(5)).not.toHaveText('—');await expect(close(page)).toBeDisabled();expect(await page.evaluate(()=>(window as any).__testTradeHistoryCloses())).toEqual(['hyperliquid:'+'a'.repeat(32)]);
 const date=await row.getByRole('cell').nth(5).textContent();await reloadTestApp(page);await enter(page);await expect(row.getByRole('cell').nth(5)).toHaveText(date!);await expect(row).toContainText('closed');await panel(page).getByRole('radio',{name:'Select trade hyperliquid 101',exact:true}).check();await expect(close(page)).toBeDisabled();
});

test('uncertain closes remain visible and never resend automatically',async({page})=>{
 await openTestApp(page);await seed(page,[record()]);await enter(page);
 await page.evaluate(()=>{(window as any).__attempts=0;window.go!.main!.App!.ConfirmTradeClose=async()=>{(window as any).__attempts++;const rows=(window as any).__testTradeHistory();Object.assign(rows[0],{status:'closing',canClose:false,error:'Close outcome is uncertain; no automatic resend',closeReason:'Awaiting exchange confirmation'});(window as any).__testSetTradeHistory(rows);return rows[0];};});
 await panel(page).getByRole('radio').check();await close(page).click();await review(page).getByRole('button',{name:'Confirm market close',exact:true}).click();await expect(review(page)).toHaveCount(0);await expect(panel(page)).toContainText('Close outcome is uncertain');await expect(close(page)).toBeDisabled();await panel(page).getByRole('button',{name:'Refresh',exact:true}).click();await expect(close(page)).toBeDisabled();expect(await page.evaluate(()=>(window as any).__attempts)).toBe(1);
});

test('delayed close previews are discarded after navigation and error-only refreshes preserve saved fields',async({page})=>{
 await openTestApp(page);await seed(page,[record()]);await enter(page);
 await page.evaluate(()=>{const api=window.go!.main!.App!,prepare=api.PreviewTradeClose;api.PreviewTradeClose=id=>new Promise(resolve=>(window as any).__releaseClosePreview=async()=>resolve(await prepare(id)));});await panel(page).getByRole('radio').check();await close(page).click();await expect.poll(()=>page.evaluate(()=>typeof(window as any).__releaseClosePreview)).toBe('function');await page.locator('[data-tab-id]').first().click();await page.evaluate(()=>(window as any).__releaseClosePreview());await expect(review(page)).toHaveCount(0);
 await page.evaluate(()=>{window.go!.main!.App!.RefreshTradeHistory=async ids=>ids.map(id=>({id,error:'Exchange temporarily unavailable',canClose:false,closeReason:'Retry Refresh'})) as any;});await enter(page);await expect(review(page)).toHaveCount(0);const row=panel(page).getByRole('row').filter({hasText:'BTC'});await expect(row).toContainText('Exchange temporarily unavailable');await expect(row).toContainText('61,000');await expect(row).toContainText('-10.00 USDC');await expect(close(page)).toBeDisabled();
});

test('pagination refreshes only the displayed platform records',async({page})=>{
 await openTestApp(page);await seed(page,Array.from({length:27},(_,i)=>record({id:'hyperliquid:'+i.toString(16).padStart(32,'0'),orderId:String(1000+i),instrument:'ASSET'+i,status:'closed',canClose:false,remainingSize:'0',closeDate:Date.UTC(2026,10,11,8)})));await enter(page);await expect(panel(page).getByRole('radio')).toHaveCount(25);await expect(panel(page).getByRole('navigation',{name:'Trade history pages'})).toContainText('1–25 of 27 trades');expect((await page.evaluate(()=>(window as any).__testTradeHistoryRefreshes())).at(-1)).toHaveLength(25);
 await panel(page).getByRole('button',{name:'Next',exact:true}).click();await expect(panel(page).getByRole('radio')).toHaveCount(2);await expect(panel(page).getByRole('navigation',{name:'Trade history pages'})).toContainText('26–27 of 27 trades');expect((await page.evaluate(()=>(window as any).__testTradeHistoryRefreshes())).at(-1)).toHaveLength(2);
});


test('a rejected close confirmation remains explained after the automatic refresh',async({page})=>{
 await openTestApp(page);await seed(page,[record()]);await enter(page);
 await page.evaluate(()=>{(window as any).__attempts=0;window.go!.main!.App!.ConfirmTradeClose=async()=>{(window as any).__attempts++;throw Error('Position changed; refresh and review again');};});
 await panel(page).getByRole('radio').check();await close(page).click();await review(page).getByRole('button',{name:'Confirm market close',exact:true}).click();await expect(review(page)).toHaveCount(0);
 await expect(panel(page)).toContainText('Close was not confirmed: Position changed; refresh and review again. No automatic resend was made.');await expect(panel(page).getByText('Refreshing exchange statuses… Saved records remain visible.',{exact:true})).toHaveCount(0);await expect(panel(page).getByRole('status')).toContainText('Position changed');expect(await page.evaluate(()=>(window as any).__attempts)).toBe(1);
});

import {test,expect,type Page} from '@playwright/test';
import {openTestApp} from './helpers/testApp';
import type {MarketEvent} from '../src/types';

const legend=(page:Page)=>page.getByLabel('Bid and Ask',{exact:true});
const active=(page:Page)=>page.evaluate(()=>(window as any).__testSubscriptions()[0]);
async function publish(page:Page,bid:number|null,ask:number|null,age=0,state='live'){
 await expect.poll(()=>page.evaluate(()=>(window as any).__testSubscriptions().length)).toBe(1);
 await page.evaluate(({bid,ask,age,state})=>(window as any).__testSetHealth({state,quote:{bid,ask,time:Date.now()-age}}), {bid,ask,age,state});
}

test('all public chart sources show real event Bid/Ask and midpoint spread at top left without rebuilding the canvas',async({page},testInfo)=>{
 await openTestApp(page);
 for(const source of ['binance','bybit','coinbase','hyperliquid']){
  await page.getByRole('combobox',{name:'Exchange',exact:true}).selectOption(source);
  await expect.poll(async()=>(await active(page)).feedKey.exchange).toBe(source);
  const canvas=await page.getByTestId('chart-canvas').locator('canvas').first().elementHandle();
  const calls=await page.evaluate(()=>(window as any).__testAttachCount());
  await publish(page,99,101);
  await expect(legend(page)).toContainText('Bid 99');await expect(legend(page)).toContainText('Ask 101');await expect(legend(page)).toContainText('Spread 200.00 bps');
  const quote=(await legend(page).boundingBox())!,plot=(await page.getByTestId('chart-canvas').boundingBox())!;
  expect(quote.x).toBeGreaterThanOrEqual(plot.x);expect(quote.x).toBeLessThan(plot.x+80);expect(quote.y).toBeGreaterThanOrEqual(plot.y);expect(quote.y).toBeLessThan(plot.y+30);
  expect(await canvas!.evaluate(element=>element.isConnected)).toBe(true);expect(await page.evaluate(()=>(window as any).__testAttachCount())).toBe(calls);
 }
 await page.getByTestId('chart-cell').screenshot({path:testInfo.outputPath('chart-top-left-bid-ask.png')});
});

test('JForex quotes sit below UTC opening hours and stale, partial or unavailable books stay explicit',async({page},testInfo)=>{
 await openTestApp(page);
 await page.evaluate(async()=>{await window.go!.main!.App!.ConfigureDucascopy({username:'quote-offline-fixture',password:'explicit-fixture',environment:'demo'});});
 await page.getByRole('combobox',{name:'Exchange',exact:true}).selectOption('dukascopy');
 await expect.poll(async()=>(await active(page)).feedKey.exchange).toBe('dukascopy');
 await publish(page,1.1001,1.1003);
 await expect(legend(page)).toContainText('Bid 1.10010');await expect(legend(page)).toContainText('Ask 1.10030');await expect(legend(page)).toContainText('Spread 1.8179 bps');
 const hours=(await page.getByLabel('Dukascopy trading hours',{exact:true}).boundingBox())!,quote=(await legend(page).boundingBox())!;
 expect(quote.y).toBeGreaterThanOrEqual(hours.y+hours.height+5);expect(quote.x).toBe(hours.x);
 await publish(page,1.1001,1.1003,31000);await expect(legend(page)).toContainText('Stale quote');await expect(legend(page)).toContainText('UTC');
 await publish(page,null,1.1003);await expect(legend(page)).toContainText('Bid —');await expect(legend(page)).toContainText('Ask 1.10030');await expect(legend(page)).toContainText('Spread — bps');
 await publish(page,1.2,1.1);await expect(legend(page)).toContainText('Invalid quote');await expect(legend(page)).toContainText('Bid —');
 const subscription=await active(page);await page.evaluate(value=>(window as any).__testPublish({...value,revision:value.revision+1,kind:'health',bars:[],health:{state:'disconnected'}} satisfies MarketEvent),subscription);
 await expect(legend(page)).toContainText('Awaiting quotes');await expect(legend(page)).toContainText('Ask —');
 await page.getByTestId('chart-cell').screenshot({path:testInfo.outputPath('jforex-quotes-below-hours.png')});
});

test('quiet quotes age visibly without new requests and an asset change clears the old book',async({page})=>{
 await page.clock.install({time:new Date('2026-11-26T12:00:00Z')});await openTestApp(page);
 await publish(page,99,101);await expect(legend(page)).toContainText('Top of book');
 const requests=await page.evaluate(()=>(window as any).__testAttachCount()),canvas=await page.getByTestId('chart-canvas').locator('canvas').first().elementHandle();
 await page.clock.fastForward(31000);await expect(legend(page)).toContainText('Stale quote');await expect(legend(page)).toContainText('Bid 99.00');
 expect(await page.evaluate(()=>(window as any).__testAttachCount())).toBe(requests);expect(await canvas!.evaluate(element=>element.isConnected)).toBe(true);
 await page.getByRole('combobox',{name:'Exchange',exact:true}).selectOption('bybit');
 await expect.poll(async()=>(await active(page))?.feedKey.exchange).toBe('bybit');
 await expect(legend(page)).toContainText('Awaiting quotes');await expect(legend(page)).toContainText('Bid —');await expect(legend(page)).toContainText('Ask —');
});

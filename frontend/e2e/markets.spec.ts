import {test,expect,type Page} from '@playwright/test';
import type {ChartConfig,FeedKey,Instrument,MarketEvent} from '../src/types';

const picker=(page:Page)=>page.getByLabel('Instruments',{exact:true});
const savedChart=(page:Page):Promise<ChartConfig>=>page.evaluate(()=>(window as any).__testWorkspace().tabs[0].charts[0]);
async function openPicker(page:Page){await page.getByRole('button',{name:'Select instrument',exact:true}).first().click();await expect(picker(page)).toBeVisible();}
async function subscriptions(page:Page){return page.evaluate(()=>(window as any).__testSubscriptions() as Array<{leaseId:string;chartId:string;generation:number;feedKey:FeedKey;revision:number}>);}
async function select(page:Page,symbol:string,market:'Spot'|'Perpetual'){
 await openPicker(page);await picker(page).getByRole('button',{name:`${symbol} ${market}`,exact:true}).click();
 await expect(page.locator('.save-indicator.saved')).toBeVisible();
 await expect.poll(()=>subscriptions(page)).toMatchObject([{feedKey:{instrument:symbol,market:market.toLowerCase()}}]);
}

for(const viewport of [{width:1600,height:1000},{width:1100,height:720}])test(`one symbol list labels and selects identical spot/perpetual names at ${viewport.width}px`,async({page})=>{
 await page.setViewportSize(viewport);await page.goto('/');await expect(page.getByLabel('OHLCV')).toContainText('61,000.00');
 await expect(page.getByText('SPOT TERMINAL',{exact:true})).toHaveCount(0);await expect(page.getByLabel('Application version',{exact:true})).toBeVisible();
 await openPicker(page);await expect(picker(page).getByRole('combobox')).toHaveCount(0);
 await expect(picker(page).getByRole('button',{name:'BTCUSDT Spot',exact:true})).toHaveAttribute('aria-pressed','true');
 await expect(picker(page).getByRole('button',{name:'BTCUSDT Perpetual',exact:true})).toBeVisible();
 await expect(picker(page).getByRole('button',{name:'XAGUSDT Perpetual',exact:true})).toBeVisible();
 const geometry=await picker(page).locator('.product-list button').evaluateAll(rows=>rows.map(row=>{const details=row.querySelector('.product-details')!.getBoundingClientRect(),label=row.querySelector('.product-market')!.getBoundingClientRect();return{detailsRight:details.right,labelLeft:label.left,labelRight:label.right,rowRight:row.getBoundingClientRect().right};}));
 for(const row of geometry){expect(row.labelLeft).toBeGreaterThan(row.detailsRight);expect(row.labelRight).toBeLessThanOrEqual(row.rowRight);}
 await page.screenshot({path:`test-results/combined-symbols-${viewport.width}.png`});
 await picker(page).getByRole('button',{name:'BTCUSDT Perpetual',exact:true}).click();
 await expect(page.getByLabel('OHLCV')).toContainText('62,000.00');await expect(page.getByLabel('Selected market',{exact:true})).toHaveText('Perpetual');
 await expect(page.locator('.save-indicator.saved')).toBeVisible();expect(await savedChart(page)).toMatchObject({market:'perpetual',instrument:'BTCUSDT'});
 await expect.poll(()=>subscriptions(page)).toMatchObject([{feedKey:{market:'perpetual',instrument:'BTCUSDT'}}]);
 const active=(await subscriptions(page))[0];
 await page.evaluate(subscription=>{const time=Math.floor(Date.now()/3600000)*3600;(window as any).__testPublish({...subscription,feedKey:{...subscription.feedKey,market:'spot'},revision:subscription.revision+1,kind:'bars',bars:[{time,open:99999,high:100000,low:99998,close:99999,volume:1,closed:false,quality:'live',sourceRevision:100}],health:{state:'live'}} satisfies MarketEvent);},active);
 await page.waitForTimeout(150);await expect(page.getByLabel('OHLCV')).toContainText('62,000.00');
 await select(page,'BTCUSDT','Spot');await expect(page.getByLabel('OHLCV')).toContainText('61,000.00');expect(await savedChart(page)).toMatchObject({market:'spot',instrument:'BTCUSDT'});
});

test('silver perpetual selection persists across restart and tabs and uses perpetual history',async({page})=>{
 await page.goto('/');await expect(page.getByLabel('OHLCV')).toContainText('61,000.00');
 await page.evaluate(async()=>{const modulePath='/src/charts/priceScale.ts';const {chartPriceScaleDiagnostics}=await import(modulePath);chartPriceScaleDiagnostics.values().next().value.setVisibleRange({from:59000,to:63000});});
 await page.getByTestId('chart-canvas').dispatchEvent('pointerup');await page.locator('.save-indicator').click();await expect(page.locator('.save-indicator.saved')).toBeVisible();expect((await savedChart(page)).priceScale).toMatchObject({autoScale:false,manualRange:{min:59000,max:63000}});
 await select(page,'XAGUSDT','Perpetual');await expect(page.getByLabel('OHLCV')).toContainText('31.00');expect((await savedChart(page)).priceScale).toMatchObject({autoScale:true,manualRange:null});
 await page.getByRole('button',{name:'Return to live',exact:true}).click();
 await expect.poll(()=>page.evaluate(()=>(window as any).__testHistoryKeys())).toContainEqual({exchange:'binance',market:'perpetual',instrument:'XAGUSDT',timeframe:'1h'});
 await page.getByRole('button',{name:'Add tab',exact:true}).click();await expect(page.getByLabel('Selected market',{exact:true})).toHaveText('Spot');
 await page.locator('[role="tab"][data-tab-id]').first().click();await expect(page.getByLabel('Selected market',{exact:true})).toHaveText('Perpetual');await expect(page.getByLabel('OHLCV')).toContainText('31.00');
 await expect(page.locator('.save-indicator.saved')).toBeVisible();await page.reload();await expect(page.getByLabel('Selected market',{exact:true})).toHaveText('Perpetual');await expect(page.getByLabel('OHLCV')).toContainText('31.00');
 expect(await savedChart(page)).toMatchObject({market:'perpetual',instrument:'XAGUSDT'});
 await page.getByRole('combobox',{name:'Exchange',exact:true}).selectOption('coinbase');
 await expect.poll(()=>subscriptions(page)).toMatchObject([{feedKey:{exchange:'coinbase',market:'spot',instrument:'BTC-USD'}}]);
 await openPicker(page);await expect(picker(page).getByRole('button',{name:'BTC-USD Spot',exact:true})).toBeVisible();await expect(picker(page).locator('.product-market.perpetual')).toHaveCount(0);
});

test('the complete catalog remains selectable beyond seventy rows and supports Unicode symbols',async({page})=>{
 await page.goto('/');await page.evaluate(()=>{
  const item:Instrument={nativeId:'BTCUSDT',market:'spot',baseAsset:'BTC',quoteAsset:'USDT',priceIncrement:'0.01',sizeIncrement:'0.1',availability:'Trading',supportedIntervals:['1h']};
  window.go!.main!.App!.ListInstruments=async()=>[...Array.from({length:90},(_,index)=>({...item,nativeId:`ASSET${index.toString().padStart(3,'0')}USDT`})),{...item,nativeId:'币安人生USDT',market:'perpetual',baseAsset:'币安人生'}];
 });
 await page.getByRole('combobox',{name:'Exchange',exact:true}).selectOption('bybit');await openPicker(page);
 await expect(picker(page).locator('.product-list button')).toHaveCount(91);
 await picker(page).getByRole('button',{name:'ASSET089USDT Spot',exact:true}).click();await expect.poll(()=>subscriptions(page)).toMatchObject([{feedKey:{instrument:'ASSET089USDT',market:'spot'}}]);
 await openPicker(page);await page.getByRole('textbox',{name:'Search instruments',exact:true}).fill('币安人生');await picker(page).getByRole('button',{name:'币安人生USDT Perpetual',exact:true}).click();
 await expect.poll(()=>subscriptions(page)).toMatchObject([{feedKey:{instrument:'币安人生USDT',market:'perpetual'}}]);
 await expect(page.locator('.save-indicator.saved')).toBeVisible();expect(await savedChart(page)).toMatchObject({instrument:'币安人生USDT',market:'perpetual'});
});

test('partial discovery shows its warning while working markets stay selectable and retries without reconnecting the chart',async({page})=>{
 await page.goto('/');await page.evaluate(()=>{
  const api=window.go!.main!.App!,original=api.ListInstruments;let calls=0;
  api.ListInstruments=async exchange=>{calls++;const items=await original(exchange);return calls===1?[{...items[0],providerMetadata:{catalogWarning:'Explicit linear catalog restriction; spot remains available'}}]:items;};
 });
 await page.getByRole('combobox',{name:'Exchange',exact:true}).selectOption('bybit');await openPicker(page);
 await expect(picker(page).getByRole('alert')).toContainText('Explicit linear catalog restriction');await expect(picker(page).getByRole('button',{name:'BTCUSDT Spot',exact:true})).toBeEnabled();
 const attachments=await page.evaluate(()=>(window as any).__testAttachCount());
 await picker(page).getByRole('button',{name:'Retry discovery',exact:true}).click();await expect(picker(page).getByRole('alert')).toHaveCount(0);await expect(picker(page).getByRole('button',{name:'XAGUSDT Perpetual',exact:true})).toBeVisible();
 expect(await page.evaluate(()=>(window as any).__testAttachCount())).toBe(attachments);
 await picker(page).getByRole('button',{name:'XAGUSDT Perpetual',exact:true}).click();await expect.poll(()=>subscriptions(page)).toMatchObject([{feedKey:{exchange:'bybit',market:'perpetual',instrument:'XAGUSDT'}}]);
});

test('a delayed perpetual history result cannot replace candles after switching the same symbol to spot',async({page})=>{
 await page.goto('/');await select(page,'BTCUSDT','Perpetual');
 await page.evaluate(()=>{window.go!.main!.App!.GetHistory=async(requestId,feedKey)=>new Promise(resolve=>{(window as any).__releasePerpetualHistory=()=>{const time=Math.floor(Date.now()/3600000)*3600;resolve({requestId,feedKey,moreHistory:false,bars:[{time,open:99999,high:100000,low:99998,close:99999,volume:1,closed:false,quality:'live',sourceRevision:100}]});};});});
 await page.getByRole('button',{name:'Return to live',exact:true}).click();await expect.poll(()=>page.evaluate(()=>typeof(window as any).__releasePerpetualHistory)).toBe('function');
 await select(page,'BTCUSDT','Spot');await page.evaluate(()=>(window as any).__releasePerpetualHistory());
 await page.waitForTimeout(150);await expect(page.getByLabel('OHLCV')).toContainText('61,000.00');await expect.poll(()=>subscriptions(page)).toMatchObject([{feedKey:{market:'spot'}}]);
});

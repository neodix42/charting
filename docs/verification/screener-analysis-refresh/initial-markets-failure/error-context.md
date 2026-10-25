# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: markets.spec.ts >> partial discovery shows its warning while working markets stay selectable and retries without reconnecting the chart
- Location: e2e/markets.spec.ts:63:1

# Error details

```
Error: expect(locator).toContainText(expected) failed

Locator: getByLabel('Instruments', { exact: true }).getByRole('alert')
Expected substring: "Explicit linear catalog restriction"
Timeout: 10000ms
Error: element(s) not found

Call log:
  - Expect "toContainText" getByLabel('Instruments', { exact: true }).getByRole('alert') with timeout 10000ms
  - waiting for getByLabel('Instruments', { exact: true }).getByRole('alert')

```

```yaml
- banner:
  - link "CryptoDesk":
    - /url: "#"
    - text: CryptoDesk v0.1.0
  - toolbar "Current tab chart controls":
    - group "Period for current tab":
      - button "Apply 1m to current tab": 1m
      - button "Apply 5m to current tab": 5m
      - button "Apply 15m to current tab": 15m
      - button "Apply 1h to current tab" [pressed]: 1h
      - button "Apply 4h to current tab": 4h
      - button "Apply 1d to current tab": 1d
      - button "Apply 1w to current tab": 1w
    - combobox "Indicators for current tab":
      - option "Indicators" [selected]
      - option "SMA"
      - option "EMA"
      - option "Ichimoku Cloud"
      - option "MACD"
  - group "Workspace controls":
    - text: Charts 1/9 Grid
    - combobox "Grid layout":
      - option "1 × 1" [selected]
      - option "1 × 2"
      - option "2 × 1"
      - option "2 × 2"
      - option "2 × 3"
      - option "3 × 2"
      - option "3 × 3"
    - button "Add chart"
    - button "Duplicate tab": Duplicate
  - button "All changes saved"
  - button "Open settings": ⚙ Settings
- navigation "Workspace tabs":
  - tablist:
    - tab "▦ Tab 1 1" [selected]
  - button "Add tab": ＋
  - text: EXPLICIT TEST FIXTURE MODE
  - tab "Screener"
- main:
  - text: ⠿
  - combobox "Exchange":
    - option "Binance"
    - option "Bybit" [selected]
    - option "Coinbase"
  - button "Select instrument": BTCUSDT ⌄
  - text: Spot
  - group "Timeframe":
    - button "Timeframe 1m": 1m
    - button "Timeframe 5m": 5m
    - button "Timeframe 15m": 15m
    - button "Timeframe 1h" [pressed]: 1h
    - button "Timeframe 4h": 4h
    - button "Timeframe 1d": 1d
    - button "Timeframe 1w": 1w
  - combobox "Indicators":
    - option "Indicators" [selected]
    - option "SMA"
    - option "EMA"
    - option "Ichimoku Cloud"
    - option "MACD"
  - button "Toggle volume": Vol
  - button "Chart style": ▥
  - button "Toggle logarithmic scale": Log
  - button "Autoscale price": A
  - button "Return to live": →|
  - button "Maximize chart": ⛶
  - button "Remove chart" [disabled]
  - group "Drawing tools":
    - text: Draw
    - button "Cursor" [pressed]
    - button "Arrow up"
    - button "Arrow down"
    - button "Horizontal line"
    - button "Trendline"
    - button "Delete selected drawing" [disabled]: Delete
    - button "Undo drawing" [disabled]
    - button "Clear drawings" [disabled]
    - text: 0 Click to select · double-click to move
  - textbox "Search instruments":
    - /placeholder: Search native instrument, base or quote…
  - button "Close instrument selector": ×
  - button "BTCUSDT Spot" [pressed]:
    - strong: BTCUSDT
    - text: BTC / USDT Spot
  - button "BTCUSDT Perpetual":
    - strong: BTCUSDT
    - text: BTC / USDT Perpetual
  - button "XAGUSDT Perpetual":
    - strong: XAGUSDT
    - text: XAG / USDT Perpetual
  - text: O 60,990.00 H 61,020.00 L 60,970.00 C 61,000.00 V 2 BTC provisional
  - table:
    - row "Charting by TradingView":
      - cell
      - cell "Charting by TradingView":
        - link "Charting by TradingView":
          - /url: https://www.tradingview.com/?utm_medium=lwc-link&utm_campaign=lwc-chart&utm_source=127.0.0.1/
          - img
      - cell
    - row:
      - cell
      - cell
      - cell
  - text: provisional · Explicit test fixtures; not live market data Following live · Feed 9s ago · trade unknown UTC
- contentinfo:
  - text: Only the active tab is connected 1 chart mounted · 1 saved configurations UTC · Public market data
  - button "Charts by TradingView ↗"
```

# Test source

```ts
  1  | import {test,expect,type Page} from '@playwright/test';
  2  | import type {ChartConfig,FeedKey,Instrument,MarketEvent} from '../src/types';
  3  |
  4  | const picker=(page:Page)=>page.getByLabel('Instruments',{exact:true});
  5  | const savedChart=(page:Page):Promise<ChartConfig>=>page.evaluate(()=>(window as any).__testWorkspace().tabs[0].charts[0]);
  6  | async function openPicker(page:Page){await page.getByRole('button',{name:'Select instrument',exact:true}).first().click();await expect(picker(page)).toBeVisible();}
  7  | async function subscriptions(page:Page){return page.evaluate(()=>(window as any).__testSubscriptions() as Array<{leaseId:string;chartId:string;generation:number;feedKey:FeedKey;revision:number}>);}
  8  | async function select(page:Page,symbol:string,market:'Spot'|'Perpetual'){
  9  |  await openPicker(page);await picker(page).getByRole('button',{name:`${symbol} ${market}`,exact:true}).click();
  10 |  await expect(page.locator('.save-indicator.saved')).toBeVisible();
  11 |  await expect.poll(()=>subscriptions(page)).toMatchObject([{feedKey:{instrument:symbol,market:market.toLowerCase()}}]);
  12 | }
  13 |
  14 | for(const viewport of [{width:1600,height:1000},{width:1100,height:720}])test(`one symbol list labels and selects identical spot/perpetual names at ${viewport.width}px`,async({page})=>{
  15 |  await page.setViewportSize(viewport);await page.goto('/');await expect(page.getByLabel('OHLCV')).toContainText('61,000.00');
  16 |  await expect(page.getByText('SPOT TERMINAL',{exact:true})).toHaveCount(0);await expect(page.getByLabel('Application version',{exact:true})).toBeVisible();
  17 |  await openPicker(page);await expect(picker(page).getByRole('combobox')).toHaveCount(0);
  18 |  await expect(picker(page).getByRole('button',{name:'BTCUSDT Spot',exact:true})).toHaveAttribute('aria-pressed','true');
  19 |  await expect(picker(page).getByRole('button',{name:'BTCUSDT Perpetual',exact:true})).toBeVisible();
  20 |  await expect(picker(page).getByRole('button',{name:'XAGUSDT Perpetual',exact:true})).toBeVisible();
  21 |  const geometry=await picker(page).locator('.product-list button').evaluateAll(rows=>rows.map(row=>{const details=row.querySelector('.product-details')!.getBoundingClientRect(),label=row.querySelector('.product-market')!.getBoundingClientRect();return{detailsRight:details.right,labelLeft:label.left,labelRight:label.right,rowRight:row.getBoundingClientRect().right};}));
  22 |  for(const row of geometry){expect(row.labelLeft).toBeGreaterThan(row.detailsRight);expect(row.labelRight).toBeLessThanOrEqual(row.rowRight);}
  23 |  await page.screenshot({path:`test-results/combined-symbols-${viewport.width}.png`});
  24 |  await picker(page).getByRole('button',{name:'BTCUSDT Perpetual',exact:true}).click();
  25 |  await expect(page.getByLabel('OHLCV')).toContainText('62,000.00');await expect(page.getByLabel('Selected market',{exact:true})).toHaveText('Perpetual');
  26 |  await expect(page.locator('.save-indicator.saved')).toBeVisible();expect(await savedChart(page)).toMatchObject({market:'perpetual',instrument:'BTCUSDT'});
  27 |  await expect.poll(()=>subscriptions(page)).toMatchObject([{feedKey:{market:'perpetual',instrument:'BTCUSDT'}}]);
  28 |  const active=(await subscriptions(page))[0];
  29 |  await page.evaluate(subscription=>{const time=Math.floor(Date.now()/3600000)*3600;(window as any).__testPublish({...subscription,feedKey:{...subscription.feedKey,market:'spot'},revision:subscription.revision+1,kind:'bars',bars:[{time,open:99999,high:100000,low:99998,close:99999,volume:1,closed:false,quality:'live',sourceRevision:100}],health:{state:'live'}} satisfies MarketEvent);},active);
  30 |  await page.waitForTimeout(150);await expect(page.getByLabel('OHLCV')).toContainText('62,000.00');
  31 |  await select(page,'BTCUSDT','Spot');await expect(page.getByLabel('OHLCV')).toContainText('61,000.00');expect(await savedChart(page)).toMatchObject({market:'spot',instrument:'BTCUSDT'});
  32 | });
  33 |
  34 | test('silver perpetual selection persists across restart and tabs and uses perpetual history',async({page})=>{
  35 |  await page.goto('/');await expect(page.getByLabel('OHLCV')).toContainText('61,000.00');
  36 |  await page.evaluate(async()=>{const modulePath='/src/charts/priceScale.ts';const {chartPriceScaleDiagnostics}=await import(modulePath);chartPriceScaleDiagnostics.values().next().value.setVisibleRange({from:59000,to:63000});});
  37 |  await page.getByTestId('chart-canvas').dispatchEvent('pointerup');await page.locator('.save-indicator').click();await expect(page.locator('.save-indicator.saved')).toBeVisible();expect((await savedChart(page)).priceScale).toMatchObject({autoScale:false,manualRange:{min:59000,max:63000}});
  38 |  await select(page,'XAGUSDT','Perpetual');await expect(page.getByLabel('OHLCV')).toContainText('31.00');expect((await savedChart(page)).priceScale).toMatchObject({autoScale:true,manualRange:null});
  39 |  await page.getByRole('button',{name:'Return to live',exact:true}).click();
  40 |  await expect.poll(()=>page.evaluate(()=>(window as any).__testHistoryKeys())).toContainEqual({exchange:'binance',market:'perpetual',instrument:'XAGUSDT',timeframe:'1h'});
  41 |  await page.getByRole('button',{name:'Add tab',exact:true}).click();await expect(page.getByLabel('Selected market',{exact:true})).toHaveText('Spot');
  42 |  await page.locator('[role="tab"][data-tab-id]').first().click();await expect(page.getByLabel('Selected market',{exact:true})).toHaveText('Perpetual');await expect(page.getByLabel('OHLCV')).toContainText('31.00');
  43 |  await expect(page.locator('.save-indicator.saved')).toBeVisible();await page.reload();await expect(page.getByLabel('Selected market',{exact:true})).toHaveText('Perpetual');await expect(page.getByLabel('OHLCV')).toContainText('31.00');
  44 |  expect(await savedChart(page)).toMatchObject({market:'perpetual',instrument:'XAGUSDT'});
  45 |  await page.getByRole('combobox',{name:'Exchange',exact:true}).selectOption('coinbase');
  46 |  await expect.poll(()=>subscriptions(page)).toMatchObject([{feedKey:{exchange:'coinbase',market:'spot',instrument:'BTC-USD'}}]);
  47 |  await openPicker(page);await expect(picker(page).getByRole('button',{name:'BTC-USD Spot',exact:true})).toBeVisible();await expect(picker(page).locator('.product-market.perpetual')).toHaveCount(0);
  48 | });
  49 |
  50 | test('the complete catalog remains selectable beyond seventy rows and supports Unicode symbols',async({page})=>{
  51 |  await page.goto('/');await page.evaluate(()=>{
  52 |   const item:Instrument={nativeId:'BTCUSDT',market:'spot',baseAsset:'BTC',quoteAsset:'USDT',priceIncrement:'0.01',sizeIncrement:'0.1',availability:'Trading',supportedIntervals:['1h']};
  53 |   window.go!.main!.App!.ListInstruments=async()=>[...Array.from({length:90},(_,index)=>({...item,nativeId:`ASSET${index.toString().padStart(3,'0')}USDT`})),{...item,nativeId:'币安人生USDT',market:'perpetual',baseAsset:'币安人生'}];
  54 |  });
  55 |  await page.getByRole('combobox',{name:'Exchange',exact:true}).selectOption('bybit');await openPicker(page);
  56 |  await expect(picker(page).locator('.product-list button')).toHaveCount(91);
  57 |  await picker(page).getByRole('button',{name:'ASSET089USDT Spot',exact:true}).click();await expect.poll(()=>subscriptions(page)).toMatchObject([{feedKey:{instrument:'ASSET089USDT',market:'spot'}}]);
  58 |  await openPicker(page);await page.getByRole('textbox',{name:'Search instruments',exact:true}).fill('币安人生');await picker(page).getByRole('button',{name:'币安人生USDT Perpetual',exact:true}).click();
  59 |  await expect.poll(()=>subscriptions(page)).toMatchObject([{feedKey:{instrument:'币安人生USDT',market:'perpetual'}}]);
  60 |  await expect(page.locator('.save-indicator.saved')).toBeVisible();expect(await savedChart(page)).toMatchObject({instrument:'币安人生USDT',market:'perpetual'});
  61 | });
  62 |
  63 | test('partial discovery shows its warning while working markets stay selectable and retries without reconnecting the chart',async({page})=>{
  64 |  await page.goto('/');await page.evaluate(()=>{
  65 |   const api=window.go!.main!.App!,original=api.ListInstruments;let calls=0;
  66 |   api.ListInstruments=async exchange=>{calls++;const items=await original(exchange);return calls===1?[{...items[0],providerMetadata:{catalogWarning:'Explicit linear catalog restriction; spot remains available'}}]:items;};
  67 |  });
  68 |  await page.getByRole('combobox',{name:'Exchange',exact:true}).selectOption('bybit');await openPicker(page);
> 69 |  await expect(picker(page).getByRole('alert')).toContainText('Explicit linear catalog restriction');await expect(picker(page).getByRole('button',{name:'BTCUSDT Spot',exact:true})).toBeEnabled();
     |                                                ^ Error: expect(locator).toContainText(expected) failed
  70 |  const attachments=await page.evaluate(()=>(window as any).__testAttachCount());
  71 |  await picker(page).getByRole('button',{name:'Retry discovery',exact:true}).click();await expect(picker(page).getByRole('alert')).toHaveCount(0);await expect(picker(page).getByRole('button',{name:'XAGUSDT Perpetual',exact:true})).toBeVisible();
  72 |  expect(await page.evaluate(()=>(window as any).__testAttachCount())).toBe(attachments);
  73 |  await picker(page).getByRole('button',{name:'XAGUSDT Perpetual',exact:true}).click();await expect.poll(()=>subscriptions(page)).toMatchObject([{feedKey:{exchange:'bybit',market:'perpetual',instrument:'XAGUSDT'}}]);
  74 | });
  75 |
  76 | test('a delayed perpetual history result cannot replace candles after switching the same symbol to spot',async({page})=>{
  77 |  await page.goto('/');await select(page,'BTCUSDT','Perpetual');
  78 |  await page.evaluate(()=>{window.go!.main!.App!.GetHistory=async(requestId,feedKey)=>new Promise(resolve=>{(window as any).__releasePerpetualHistory=()=>{const time=Math.floor(Date.now()/3600000)*3600;resolve({requestId,feedKey,moreHistory:false,bars:[{time,open:99999,high:100000,low:99998,close:99999,volume:1,closed:false,quality:'live',sourceRevision:100}]});};});});
  79 |  await page.getByRole('button',{name:'Return to live',exact:true}).click();await expect.poll(()=>page.evaluate(()=>typeof(window as any).__releasePerpetualHistory)).toBe('function');
  80 |  await select(page,'BTCUSDT','Spot');await page.evaluate(()=>(window as any).__releasePerpetualHistory());
  81 |  await page.waitForTimeout(150);await expect(page.getByLabel('OHLCV')).toContainText('61,000.00');await expect.poll(()=>subscriptions(page)).toMatchObject([{feedKey:{market:'spot'}}]);
  82 | });
  83 |
```

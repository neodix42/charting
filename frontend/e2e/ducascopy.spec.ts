import {test,expect,type Page} from '@playwright/test';
import {openTestApp,reloadTestApp} from './helpers/testApp';

const dialog=(page:Page)=>page.getByRole('dialog',{name:'Workspace settings',exact:true});
const form=(page:Page)=>dialog(page).getByRole('form',{name:'Dukascopy settings',exact:true});
const username=(page:Page)=>form(page).getByLabel('JForex Username',{exact:true});
const password=(page:Page)=>form(page).getByLabel('JForex Password',{exact:true});
const environment=(page:Page)=>form(page).getByLabel('JForex environment',{exact:true});
const save=(page:Page)=>form(page).getByRole('button',{name:'Save credentials',exact:true});
const connectionTest=(page:Page)=>form(page).getByRole('button',{name:'Test connection',exact:true});
const picker=(page:Page)=>page.getByLabel('Instruments',{exact:true});
async function openSettings(page:Page){await page.getByRole('button',{name:'Open settings',exact:true}).click();await dialog(page).getByRole('tab',{name:'Dukascopy',exact:true}).click();await expect(username(page)).toBeEnabled();}
async function credentials(page:Page){await page.evaluate(()=>window.go!.main!.App!.ConfigureDucascopy({username:'explicit-demo',password:'explicit-private-fixture-password',environment:'demo'}));}
async function selectSource(page:Page){await page.getByRole('combobox',{name:'Exchange',exact:true}).selectOption('dukascopy');}

test('JForex credentials save privately, retain a masked password across restart and test only on request',async({page})=>{
 const remoteRequests:string[]=[];page.on('request',request=>{if(/dukascopy|jforex/i.test(request.url())&&!request.url().startsWith('http://127.0.0.1'))remoteRequests.push(request.url());});
 await openTestApp(page);await openSettings(page);
 await expect(environment(page)).toHaveValue('demo');await expect(password(page)).toHaveAttribute('type','password');await expect(password(page)).toHaveAttribute('autocomplete','off');await expect(save(page)).toBeDisabled();await expect(connectionTest(page)).toBeDisabled();
 expect(await page.evaluate(()=>(window as any).__testDucascopyTests())).toBe(0);
 await username(page).fill('  explicit-demo  ');await password(page).fill('private-test-password');await save(page).click();
 await expect(form(page).getByRole('status')).toHaveText('JForex credentials saved.');await expect(username(page)).toHaveValue('explicit-demo');await expect(password(page)).toHaveValue('');await expect(password(page)).toHaveAttribute('placeholder','Saved password · leave blank to keep');
 expect(await page.evaluate(()=>window.go!.main!.App!.GetDucascopy())).toMatchObject({username:'explicit-demo',passwordConfigured:true,connected:false});
 expect(JSON.stringify(await page.evaluate(()=>window.go!.main!.App!.GetDucascopy()))).not.toContain('private-test-password');
 expect(JSON.stringify(await page.evaluate(()=>(window as any).__testWorkspace()))).not.toContain('explicit-demo');
 await dialog(page).getByRole('tab',{name:'Debug',exact:true}).click();await dialog(page).getByRole('button',{name:'Refresh diagnostics',exact:true}).click();await expect(dialog(page).locator('.diagnostics')).not.toContainText('private-test-password');
 await reloadTestApp(page);await openSettings(page);await expect(username(page)).toHaveValue('explicit-demo');await expect(password(page)).toHaveValue('');await expect(connectionTest(page)).toBeEnabled();
 expect(await page.evaluate(()=>(window as any).__testDucascopyTests())).toBe(0);
 await connectionTest(page).click();await expect(form(page).getByRole('status')).toHaveText('Connected to JForex Demo · 3 forex pairs available.');expect(await page.evaluate(()=>(window as any).__testDucascopyTests())).toBe(1);
 await environment(page).selectOption('live');await expect(connectionTest(page)).toBeDisabled();await expect(save(page)).toBeDisabled();await expect(form(page)).toContainText('Enter a password for the new username or environment.');
 await environment(page).selectOption('demo');await form(page).getByRole('checkbox',{name:'Remove saved password',exact:true}).check();await expect(connectionTest(page)).toBeDisabled();await save(page).click();await expect(form(page).getByRole('status')).toHaveText('Saved JForex password removed.');
 expect((await page.evaluate(()=>window.go!.main!.App!.GetDucascopy())).passwordConfigured).toBe(false);expect(remoteRequests).toEqual([]);
});

test('a pending JForex save waits through page changes, settings close and normal application shutdown',async({page})=>{
 await openTestApp(page);await page.evaluate(()=>{const api=window.go!.main!.App!,configure=api.ConfigureDucascopy;api.ConfigureDucascopy=request=>new Promise(resolve=>{(window as any).__releaseJForexSave=async()=>resolve(await configure(request));});});
 await openSettings(page);await username(page).fill('pending-demo');await password(page).fill('private-pending-password');await save(page).click();
 await expect.poll(()=>page.evaluate(()=>typeof(window as any).__releaseJForexSave)).toBe('function');
 await dialog(page).getByRole('tab',{name:'Appearance',exact:true}).click();await dialog(page).getByRole('button',{name:'Close settings',exact:true}).click();await page.evaluate(()=>(window as any).__testEmit('prepare-close',{token:'jforex-pending'}));
 await page.waitForTimeout(100);await expect(dialog(page)).toBeVisible();expect(await page.evaluate(()=>(window as any).__testCloseConfirmation())).toBeNull();
 await page.evaluate(()=>(window as any).__releaseJForexSave());await expect(dialog(page)).toHaveCount(0);await expect.poll(()=>page.evaluate(()=>(window as any).__testCloseConfirmation())).toEqual({token:'jforex-pending',discard:false});
 expect((await page.evaluate(()=>window.go!.main!.App!.GetDucascopy())).passwordConfigured).toBe(true);
});

test('JForex save and transport failures preserve drafts and hide sensitive backend payloads',async({page})=>{
 await openTestApp(page);await page.evaluate(()=>{const api=window.go!.main!.App!,configure=api.ConfigureDucascopy;let first=true;api.ConfigureDucascopy=request=>{if(first){first=false;return Promise.reject(Error(`provider echoed ${request.password}`));}return configure(request);};api.TestDucascopyConnection=async()=>{throw Error('provider echoed private-retry-password');};});
 await openSettings(page);await username(page).fill('retry-demo');await password(page).fill('private-retry-password');await save(page).click();
 await expect(form(page).getByRole('alert')).toHaveText('Could not save JForex credentials. Please retry.');await expect(password(page)).toHaveValue('private-retry-password');await expect(page.locator('body')).not.toContainText('private-retry-password');
 expect((await page.evaluate(()=>window.go!.main!.App!.GetDucascopy())).passwordConfigured).toBe(false);
 await connectionTest(page).click();await expect(form(page).getByRole('alert')).toHaveText('Could not test the JForex connection. Check your connection, credentials and Java runtime, then retry.');await expect(password(page)).toHaveValue('');await expect(page.locator('body')).not.toContainText('private-retry-password');
 expect((await page.evaluate(()=>window.go!.main!.App!.GetDucascopy())).passwordConfigured).toBe(true);
});

test('changing an active JForex identity explains how to release charts without exposing the password',async({page})=>{
 await openTestApp(page);await credentials(page);await page.evaluate(()=>{window.go!.main!.App!.ConfigureDucascopy=async()=>{throw Error('Close or reselect active Dukascopy charts before changing JForex credentials');};});
 await openSettings(page);await username(page).fill('another-demo');await password(page).fill('private-replacement-password');await save(page).click();
 await expect(form(page).getByRole('alert')).toHaveText('Close or reselect active Dukascopy charts before changing JForex credentials');await expect(password(page)).toHaveValue('private-replacement-password');await expect(page.locator('body')).not.toContainText('private-replacement-password');
});

test('late hidden settings reads cannot replace current JForex drafts and load failures retry without secrets',async({page})=>{
 await openTestApp(page);await page.evaluate(()=>{const api=window.go!.main!.App!,read=api.GetDucascopy;let count=0;api.GetDucascopy=async()=>{if(++count===1)throw Error('private-load-password');if(count===2)return new Promise(resolve=>{(window as any).__releaseOldJForex=async()=>resolve({...await read(),username:'obsolete-demo'});});return read();};});
 await page.getByRole('button',{name:'Open settings',exact:true}).click();await dialog(page).getByRole('tab',{name:'Dukascopy',exact:true}).click();await expect(form(page).getByRole('alert')).toContainText('Could not load JForex settings. Saved credentials have been preserved.');await expect(page.locator('body')).not.toContainText('private-load-password');
 await form(page).getByRole('button',{name:'Retry JForex settings',exact:true}).click();await expect.poll(()=>page.evaluate(()=>typeof(window as any).__releaseOldJForex)).toBe('function');
 await dialog(page).getByRole('tab',{name:'Appearance',exact:true}).click();await dialog(page).getByRole('tab',{name:'Dukascopy',exact:true}).click();await expect(username(page)).toBeEnabled();await username(page).fill('current-demo');await password(page).fill('current-private-password');await page.evaluate(()=>(window as any).__releaseOldJForex());await expect(username(page)).toHaveValue('current-demo');await expect(password(page)).toHaveValue('current-private-password');
});

test('Live PIN challenge stays transient, tests once and clears after use and identity changes',async({page})=>{
 await openTestApp(page);await page.evaluate(()=>{const api=window.go!.main!.App!,test=api.TestDucascopyConnectionWithPIN;let pins:string[]=[];api.TestDucascopyConnectionWithPIN=pin=>new Promise(resolve=>{pins.push(pin);(window as any).__releaseJForexPIN=async()=>resolve(await test(pin));});(window as any).__testJForexPINs=()=>pins;});
 await openSettings(page);await username(page).fill('explicit-live');await environment(page).selectOption('live');await password(page).fill('private-live-password');
 await form(page).getByRole('button',{name:'Load PIN challenge',exact:true}).click();await expect(form(page).getByRole('img',{name:'JForex login PIN challenge',exact:true})).toBeVisible();await expect(password(page)).toHaveValue('');
 const pin=form(page).getByLabel('JForex PIN',{exact:true});await pin.fill('54792');const before=await connectionTest(page).boundingBox();await connectionTest(page).dblclick();const testing=form(page).getByRole('button',{name:'Testing connection…',exact:true});await expect(testing).toBeDisabled();const after=await testing.boundingBox();expect(before).not.toBeNull();expect(after).not.toBeNull();expect(Math.abs(before!.x+before!.width-after!.x-after!.width)).toBeLessThan(1);
 expect(await page.evaluate(()=>(window as any).__testJForexPINs())).toEqual(['54792']);expect(JSON.stringify(await page.evaluate(()=>(window as any).__testWorkspace()))).not.toContain('54792');expect(await page.evaluate(()=>sessionStorage.getItem('cryptodesk-explicit-test-ducascopy'))).not.toContain('54792');
 await page.evaluate(()=>(window as any).__releaseJForexPIN());await expect(form(page).getByRole('status')).toHaveText('Connected to JForex Live · 3 forex pairs available.');await expect(pin).toHaveCount(0);
 await form(page).getByRole('button',{name:'Load PIN challenge',exact:true}).click();await pin.fill('87654');await username(page).fill('another-live');await expect(pin).toHaveCount(0);await expect(form(page).getByRole('img')).toHaveCount(0);
});

test('JForex runtime prerequisites and returned authentication failures are visible without retry loops',async({page})=>{
 await openTestApp(page);await credentials(page);await page.evaluate(()=>{const api=window.go!.main!.App!,read=api.GetDucascopy;api.GetDucascopy=async()=>({...await read(),runtimeAvailable:false,javaPath:'',error:'Java 17 or newer is required. Install a Java runtime and restart CryptoDesk.'});api.TestDucascopyConnection=async()=>({...await read(),connected:false,error:'JForex rejected the credentials. Check the selected environment and password.'});});
 await openSettings(page);await expect(form(page)).toContainText('Java 17 or newer is required. Install a Java runtime and restart CryptoDesk.');await connectionTest(page).click();await expect(form(page).getByRole('alert')).toHaveText('JForex rejected the credentials. Check the selected environment and password.');expect(await page.evaluate(()=>(window as any).__testDucascopyTests())).toBe(0);
});

test('Dukascopy lists discovered forex pairs, displays Bid quotes and persists the selected forex feed',async({page})=>{
 await openTestApp(page);await credentials(page);await selectSource(page);await expect(page.getByLabel('Selected market',{exact:true})).toHaveText('Forex');await expect(page.getByRole('button',{name:'Select instrument',exact:true})).toContainText('EUR/USD');await expect(page.getByLabel('OHLCV',{exact:true})).toContainText('1.06000');await expect(page.getByLabel('Price side',{exact:true})).toHaveText('Bid');await expect(page.getByLabel('OHLCV',{exact:true})).toContainText('million EUR');await expect(page.getByLabel('Last quote',{exact:true})).toContainText('Quote');
 await page.getByRole('button',{name:'Select instrument',exact:true}).click();await expect(picker(page).getByRole('button',{name:'EUR/USD Forex',exact:true})).toBeVisible();await expect(picker(page).getByRole('button',{name:'GBP/USD Forex',exact:true})).toBeVisible();await expect(picker(page).getByRole('button',{name:'USD/JPY Forex',exact:true})).toBeVisible();await expect(picker(page).locator('.product-market.perpetual')).toHaveCount(0);
 await picker(page).getByRole('textbox',{name:'Search instruments',exact:true}).fill('uSd/JpY');await expect(picker(page).getByRole('button')).toHaveCount(2);await picker(page).getByRole('button',{name:'USD/JPY Forex',exact:true}).click();await expect(page.getByLabel('OHLCV',{exact:true})).toContainText('151.000');
 await expect.poll(()=>page.evaluate(()=>(window as any).__testSubscriptions())).toMatchObject([{feedKey:{exchange:'dukascopy',market:'forex',instrument:'USDJPY',timeframe:'1h'}}]);await page.getByRole('button',{name:'Return to live',exact:true}).click();await expect.poll(()=>page.evaluate(()=>(window as any).__testHistoryKeys())).toContainEqual({exchange:'dukascopy',market:'forex',instrument:'USDJPY',timeframe:'1h'});
 await expect(page.locator('.save-indicator.saved')).toBeVisible();await reloadTestApp(page);await expect(page.getByRole('combobox',{name:'Exchange',exact:true})).toHaveValue('dukascopy');await expect(page.getByRole('button',{name:'Select instrument',exact:true})).toContainText('USD/JPY');await expect(page.getByLabel('Selected market',{exact:true})).toHaveText('Forex');
 await page.getByRole('tab',{name:'Screener',exact:true}).click();await expect.poll(()=>page.evaluate(()=>(window as any).__testLeases())).toBe(0);await page.locator('[role="tab"][data-tab-id]').first().click();await expect.poll(()=>page.evaluate(()=>(window as any).__testLeases())).toBe(1);
 await page.getByRole('combobox',{name:'Exchange',exact:true}).selectOption('coinbase');await expect(page.getByLabel('Selected market',{exact:true})).toHaveText('Spot');await expect(page.getByLabel('Price side',{exact:true})).toHaveCount(0);await expect.poll(()=>page.evaluate(()=>(window as any).__testSubscriptions())).toMatchObject([{feedKey:{exchange:'coinbase',market:'spot',instrument:'BTC-USD'}}]);
});

test('saving credentials recovers a Dukascopy source selected before setup and refreshes its instrument catalog',async({page})=>{
 await openTestApp(page);expect(await page.evaluate(()=>window.go!.main!.App!.GetDucascopy())).toMatchObject({username:'',passwordConfigured:false});await selectSource(page);await expect(page.locator('.chart-message')).toContainText('Save JForex credentials in Settings → Dukascopy.');await page.getByRole('button',{name:'Select instrument',exact:true}).click();await expect(picker(page).getByRole('alert')).toContainText('Save JForex credentials');await page.getByRole('button',{name:'Close instrument selector',exact:true}).click();
 await openSettings(page);await username(page).fill('recovered-demo');await password(page).fill('private-recovered-password');await save(page).click();await expect(form(page).getByRole('status')).toHaveText('JForex credentials saved.');await dialog(page).getByRole('button',{name:'Close settings',exact:true}).click();
 await expect(page.getByLabel('OHLCV',{exact:true})).toContainText('1.06000');await page.getByRole('button',{name:'Select instrument',exact:true}).click();await expect(picker(page).getByRole('button',{name:'EUR/USD Forex',exact:true})).toBeVisible();expect(await page.evaluate(()=>(window as any).__testDucascopyTests())).toBe(0);
});

test('an obsolete JForex discovery response and feed event cannot overwrite a crypto source',async({page})=>{
 await openTestApp(page);await credentials(page);await page.evaluate(()=>{const api=window.go!.main!.App!,list=api.ListInstruments;api.ListInstruments=exchange=>exchange==='dukascopy'?new Promise(resolve=>{(window as any).__releaseJForexCatalog=async()=>resolve(await list(exchange));}):list(exchange);});
 await selectSource(page);await expect.poll(()=>page.evaluate(()=>typeof(window as any).__releaseJForexCatalog)).toBe('function');const old=await page.evaluate(()=>(window as any).__testSubscriptions()[0]);
 await page.getByRole('combobox',{name:'Exchange',exact:true}).selectOption('coinbase');await expect(page.getByLabel('OHLCV',{exact:true})).toContainText('61,000.00');await page.evaluate(({old})=>{(window as any).__testPublish({...old,kind:'bars',revision:old.revision+1,bars:[{time:Math.floor(Date.now()/3600000)*3600,open:99,high:100,low:98,close:99,volume:1,closed:false,quality:'live',sourceRevision:2}],health:{state:'live'}});(window as any).__releaseJForexCatalog();},{old});
 await expect(page.getByLabel('OHLCV',{exact:true})).toContainText('61,000.00');await expect(page.getByRole('button',{name:'Select instrument',exact:true})).toContainText('BTC-USD');await page.getByRole('button',{name:'Select instrument',exact:true}).click();await expect(picker(page).getByRole('button',{name:'BTC-USD Spot',exact:true})).toBeVisible();await expect(picker(page).locator('.product-market.forex')).toHaveCount(0);
});

for(const kind of ['test','challenge'] as const)test(`a pending JForex ${kind} can be canceled without losing credentials or closing before cancellation finishes`,async({page})=>{
 await openTestApp(page);await credentials(page);
 await page.evaluate(({kind})=>{
  const api=window.go!.main!.App!,read=api.GetDucascopy;let cancelCalls=0,rejectOperation:((error:Error)=>void)|undefined;
  const pending=()=>new Promise<never>((_,reject)=>{rejectOperation=reject;});
  if(kind==='test')api.TestDucascopyConnection=pending;else api.GetDucascopyLoginChallenge=pending;
  api.CancelDucascopyConnectionTest=()=>new Promise(resolve=>{cancelCalls++;(window as any).__releaseJForexCancel=async()=>{rejectOperation?.(Error('private-fixture-password in canceled transport'));resolve(await read());};});
  (window as any).__testJForexCancelCalls=()=>cancelCalls;
 },{kind});
 await openSettings(page);
 if(kind==='challenge'){await environment(page).selectOption('live');await password(page).fill('private-fixture-password');await form(page).getByRole('button',{name:'Load PIN challenge',exact:true}).click();}else await connectionTest(page).click();
 const cancel=form(page).getByRole('button',{name:'Cancel test',exact:true});await expect(cancel).toBeEnabled();await cancel.dblclick();await expect(form(page).getByRole('button',{name:'Cancelling…',exact:true})).toBeDisabled();expect(await page.evaluate(()=>(window as any).__testJForexCancelCalls())).toBe(1);
 await dialog(page).getByRole('tab',{name:'Appearance',exact:true}).click();await dialog(page).getByRole('button',{name:'Close settings',exact:true}).click();await page.waitForTimeout(100);await expect(dialog(page)).toBeVisible();
 await page.evaluate(()=>(window as any).__releaseJForexCancel());await expect(dialog(page)).toHaveCount(0);await expect(page.locator('body')).not.toContainText('private-fixture-password');
 await openSettings(page);await expect(username(page)).toHaveValue('explicit-demo');await expect(password(page)).toHaveValue('');await expect(connectionTest(page)).toBeEnabled();await expect(form(page).getByRole('button',{name:'Cancel test',exact:true})).toHaveCount(0);expect((await page.evaluate(()=>window.go!.main!.App!.GetDucascopy())).passwordConfigured).toBe(true);
});

for(const kind of ['test','challenge'] as const)test(`closing Settings cancels a pending JForex ${kind} without waiting for its stale response`,async({page})=>{
 await openTestApp(page);await credentials(page);
 await page.evaluate(({kind})=>{
  const api=window.go!.main!.App!,read=api.GetDucascopy;let cancelCalls=0;
  if(kind==='test')api.TestDucascopyConnection=()=>new Promise(resolve=>(window as any).__lateJForex=async()=>resolve({...await read(),username:'obsolete-login',connected:true}));
  else api.GetDucascopyLoginChallenge=()=>new Promise(resolve=>(window as any).__lateJForex=()=>resolve({imageDataUrl:'data:image/png;base64,AAAA'}));
  api.CancelDucascopyConnectionTest=async()=>{cancelCalls++;return read();};
  (window as any).__testAutomaticCancelCalls=()=>cancelCalls;
 },{kind});
 await openSettings(page);
 if(kind==='challenge'){await environment(page).selectOption('live');await password(page).fill('private-close-password');await form(page).getByRole('button',{name:'Load PIN challenge',exact:true}).click();}else await connectionTest(page).click();
 await expect.poll(()=>page.evaluate(()=>typeof(window as any).__lateJForex)).toBe('function');
 await dialog(page).getByRole('button',{name:'Close settings',exact:true}).click();await expect(dialog(page)).toHaveCount(0);
 expect(await page.evaluate(()=>(window as any).__testAutomaticCancelCalls())).toBe(1);
 await openSettings(page);await expect(username(page)).toHaveValue('explicit-demo');await page.evaluate(()=>(window as any).__lateJForex());
 await expect(username(page)).toHaveValue('explicit-demo');await expect(form(page).getByRole('img',{name:'JForex login PIN challenge',exact:true})).toHaveCount(0);
 await expect(connectionTest(page)).toBeEnabled();await expect(password(page)).toHaveValue('');
});

test('Cancel test releases the controls while an obsolete transport response is still pending',async({page})=>{
 await openTestApp(page);await credentials(page);
 await page.evaluate(()=>{
  const api=window.go!.main!.App!,test=api.TestDucascopyConnection,read=api.GetDucascopy;let calls=0;
  api.TestDucascopyConnection=()=>++calls===1?new Promise(resolve=>(window as any).__obsoleteJForexTest=async()=>resolve({...await read(),username:'obsolete-login',connected:false,error:'obsolete failure'})):test();
 });
 await openSettings(page);await connectionTest(page).click();await expect.poll(()=>page.evaluate(()=>typeof(window as any).__obsoleteJForexTest)).toBe('function');
 await form(page).getByRole('button',{name:'Cancel test',exact:true}).click();await expect(connectionTest(page)).toBeEnabled();
 await connectionTest(page).click();await expect(form(page).getByRole('status')).toHaveText('Connected to JForex Demo · 3 forex pairs available.');
 await page.evaluate(()=>(window as any).__obsoleteJForexTest());await expect(username(page)).toHaveValue('explicit-demo');await expect(form(page).getByRole('status')).toHaveText('Connected to JForex Demo · 3 forex pairs available.');await expect(form(page).getByRole('alert')).toHaveCount(0);
});

test('a cancellation failure preserves Settings and credentials until close is retried',async({page})=>{
 await openTestApp(page);await credentials(page);
 await page.evaluate(()=>{const api=window.go!.main!.App!,read=api.GetDucascopy;let attempts=0;api.TestDucascopyConnection=()=>new Promise(()=>{});api.CancelDucascopyConnectionTest=async()=>{if(++attempts===1)throw Error('private-cancel-payload');return read();};});
 await openSettings(page);await connectionTest(page).click();await expect(form(page).getByRole('button',{name:'Cancel test',exact:true})).toBeVisible();
 await dialog(page).getByRole('button',{name:'Close settings',exact:true}).click();await expect(form(page).getByRole('alert')).toHaveText('Could not cancel the JForex connection check. Please retry.');await expect(dialog(page)).toBeVisible();await expect(page.locator('body')).not.toContainText('private-cancel-payload');
 await dialog(page).getByRole('button',{name:'Close settings',exact:true}).click();await expect(dialog(page)).toHaveCount(0);expect((await page.evaluate(()=>window.go!.main!.App!.GetDucascopy())).passwordConfigured).toBe(true);
});

test('wider Settings displays every renamed tab and wraps them inside narrow windows',async({page})=>{
 await openTestApp(page);await page.getByRole('button',{name:'Open settings',exact:true}).click();
 expect((await dialog(page).boundingBox())!.width).toBeGreaterThanOrEqual(1000);
 const tabs=dialog(page).getByRole('tablist',{name:'Settings pages',exact:true});await expect(tabs.getByRole('tab',{name:'Dukascopy',exact:true})).toBeVisible();
 const fits=()=>tabs.evaluate(element=>element.scrollWidth<=element.clientWidth+1&&[...element.children].every(child=>{const box=child.getBoundingClientRect(),bounds=element.getBoundingClientRect();return box.left>=bounds.left-1&&box.right<=bounds.right+1&&box.top>=bounds.top-1&&box.bottom<=bounds.bottom+1;}));
 expect(await fits()).toBe(true);await page.setViewportSize({width:700,height:900});expect(await fits()).toBe(true);
 await tabs.getByRole('tab',{name:'Appearance',exact:true}).focus();await page.keyboard.press('End');await expect(tabs.getByRole('tab',{name:'Debug',exact:true})).toBeFocused();await expect(dialog(page).getByRole('tabpanel')).toContainText('Dukascopy uses your saved JForex credentials');
});

test('Sunday forex requests cross the weekend for initial, older, refreshed and indicator history',async({page})=>{
 await page.clock.setFixedTime(new Date('2026-11-01T22:05:30Z'));await openTestApp(page);await credentials(page);
 await page.evaluate(()=>{
  const api=window.go!.main!.App!,attach=api.AttachChart,history=api.GetHistory;
  const sunday=Date.parse('2026-11-01T22:00:00Z')/1000,friday=Date.parse('2026-10-30T21:59:00Z')/1000;
  const candle=(time:number)=>({time,open:1.05,high:1.06,low:1.04,close:1.055,volume:2,closed:true,quality:'provisional',sourceRevision:1});
  const previous=Array.from({length:2200},(_,index)=>candle(friday-(2199-index)*60)),current=Array.from({length:5},(_,index)=>candle(sunday+index*60));
  const requests:Array<{from:number;to:number;maxBars:number;returned:number}>=[];
  api.AttachChart=async(id,generation,key,from,to)=>{const snapshot=await attach(id,generation,key,from,to);if(key.exchange!=='dukascopy')return snapshot;(window as any).__weekendAttach={from,to};return{...snapshot,bars:current};};
  api.GetHistory=async(requestId,key,from,to,maxBars)=>{
   if(key.exchange!=='dukascopy')return history(requestId,key,from,to,maxBars);
   const bars=[...previous,...current].filter(bar=>bar.time>=from&&bar.time<to).slice(-maxBars);requests.push({from,to,maxBars,returned:bars.length});
   return{requestId,feedKey:key,bars,moreHistory:bars.length>0};
  };
  (window as any).__weekendRequests=()=>requests;
 });
 const chart=page.getByTestId('chart-cell');await chart.getByRole('button',{name:'Timeframe 1m',exact:true}).click();await selectSource(page);await expect(page.getByLabel('OHLCV',{exact:true})).toContainText('1.05500');
 const friday=Date.parse('2026-10-30T21:59:00Z')/1000,sunday=Date.parse('2026-11-01T22:00:00Z')/1000;
 expect((await page.evaluate(()=>(window as any).__weekendAttach)).from).toBeLessThan(friday);
 await page.evaluate(()=>{const state=(window as any).__testWorkspace(),id=state.tabs[0].charts[0].id;(window as any).__testChartPan(id,-80);});
 await expect.poll(()=>page.evaluate(()=>(window as any).__weekendRequests().some((request:any)=>request.to<=Date.parse('2026-11-01T22:00:00Z')/1000&&request.maxBars===500&&request.returned>0))).toBe(true);
 const older=await page.evaluate(()=>(window as any).__weekendRequests().find((request:any)=>request.to<=Date.parse('2026-11-01T22:00:00Z')/1000&&request.maxBars===500));expect(older.from).toBeLessThan(friday);expect(older.returned).toBe(500);await expect(chart.locator('.chart-status')).not.toContainText('Beginning of available history');
 await chart.getByLabel('Indicators',{exact:true}).selectOption('sma');const parameters=page.getByRole('dialog',{name:'SMA parameters',exact:true});await parameters.getByLabel('Period',{exact:true}).fill('2000');await parameters.getByRole('button',{name:'OK',exact:true}).click();await expect(chart.locator('.indicator-warmup')).toHaveCount(0);await expect(chart.getByRole('button',{name:'Edit SMA (2000)',exact:true})).toHaveAttribute('title','Edit indicator parameters');
 const warmup=await page.evaluate(()=>(window as any).__weekendRequests().filter((request:any)=>request.maxBars===1000));expect(warmup.length).toBeGreaterThan(0);expect(warmup.every((request:any)=>request.returned>0)).toBe(true);
 await chart.getByRole('button',{name:'Return to live',exact:true}).click();await expect.poll(()=>page.evaluate(()=>(window as any).__weekendRequests().some((request:any)=>request.to>Date.parse('2026-11-01T22:00:00Z')/1000&&request.maxBars===500))).toBe(true);
 const refreshed=await page.evaluate(()=>(window as any).__weekendRequests().find((request:any)=>request.to>Date.parse('2026-11-01T22:00:00Z')/1000&&request.maxBars===500));expect(refreshed.from).toBeLessThan(friday);expect(refreshed.to).toBeGreaterThan(sunday);
});

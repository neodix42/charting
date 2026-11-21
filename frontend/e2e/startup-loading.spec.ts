import {test,expect,type Page,type Route} from '@playwright/test';
import {waitForTestBridge} from './helpers/testApp';
import {firstWorkspace} from '../src/workspace/model';

async function holdResource(page:Page,pattern:string){
 let release!:()=>void,requested=false;
 const gate=new Promise<void>(resolve=>{release=resolve;});
 const handler=async(route:Route)=>{requested=true;await gate;await route.continue();};
 await page.route(pattern,handler);
 return {requested:()=>requested,release,close:async()=>{release();await page.unroute(pattern,handler);}};
}

async function expectOneWorkspace(page:Page){
 await waitForTestBridge(page);
 await expect(page.getByTestId('chart-cell')).toHaveCount(1);
 await expect(page.getByText('Starting CryptoDesk…',{exact:true})).toHaveCount(0);
 await expect.poll(()=>page.evaluate(()=>window.__cryptodeskDiagnostics?.())).toMatchObject({chartInstances:1,consumers:1,savedTabs:1,savedCharts:1});
 await expect.poll(()=>page.evaluate(()=>(window as any).__testLeases())).toBe(1);
}

test('production HTML paints a branded loading status without JavaScript or external stylesheets',async({browser})=>{
 const context=await browser.newContext({javaScriptEnabled:false,colorScheme:'dark',viewport:{width:1100,height:600}});
 try{
  const page=await context.newPage();await page.route('**/*.css',route=>route.abort());
  await page.goto('http://127.0.0.1:4174/');
  await expect(page.getByRole('heading',{name:'CryptoDesk',exact:true})).toBeVisible();
  await expect(page.getByRole('status')).toHaveText('Starting CryptoDesk…');
  await expect(page.locator('main')).toHaveAttribute('aria-busy','true');
  await expect(page.locator('head link[rel="stylesheet"]')).toHaveCount(0);
  await expect(page.locator('.boot-logo')).toBeVisible();
  await expect(page.getByTestId('chart-cell')).toHaveCount(0);
  expect(await page.locator('body').evaluate(body=>body.scrollWidth<=window.innerWidth)).toBe(true);
 }finally{await context.close();}
});

test('production loading shell stays visible while the application stylesheet is delayed and never installs fixtures',async({page})=>{
 const held=await holdResource(page,'**/assets/Application-*.css');
 try{
  await page.goto('http://127.0.0.1:4174/',{waitUntil:'commit'});
  await expect.poll(held.requested).toBe(true);
  await expect(page.getByRole('status')).toHaveText('Starting CryptoDesk…');
  await expect(page.getByRole('heading',{name:'CryptoDesk',exact:true})).toBeVisible();
  await expect(page.getByTestId('chart-cell')).toHaveCount(0);
  expect(await page.evaluate(()=>typeof(window as any).__testWorkspace)).toBe('undefined');
  held.release();
  await expect(page.getByText('Unable to open workspace',{exact:true})).toBeVisible();
  await expect(page.getByText(/The Wails desktop bridge is unavailable/)).toBeVisible();
  expect(await page.evaluate(()=>window.__CRYPTO_TEST__)).toBeUndefined();
 }finally{await held.close();}
});

test('the loading shell remains painted while the charting application module is delayed',async({page})=>{
 const errors:string[]=[];page.on('pageerror',error=>errors.push(error.message));
 const held=await holdResource(page,'**/src/App.tsx*');
 try{
  await page.goto('/',{waitUntil:'commit'});await expect.poll(held.requested).toBe(true);
  await expect(page.getByRole('status')).toHaveText('Starting CryptoDesk…');
  await expect(page.getByTestId('chart-cell')).toHaveCount(0);
  expect(await page.evaluate(()=>(window as any).__testLeases())).toBe(0);
  held.release();await expectOneWorkspace(page);expect(errors).toEqual([]);
 }finally{await held.close();}
});

test('the HTML loader stays visible before the explicit fixture bridge finishes importing',async({page})=>{
 const held=await holdResource(page,'**/src/test/browserBridge.ts*');
 try{
  await page.goto('/',{waitUntil:'commit'});await expect.poll(held.requested).toBe(true);
  await expect(page.getByRole('status')).toHaveText('Starting CryptoDesk…');
  expect(await page.evaluate(()=>typeof(window as any).__testWorkspace)).toBe('undefined');
  expect(await page.evaluate(()=>(window as any).__testBridgeReady)).not.toBe(true);
  await expect(page.getByTestId('chart-cell')).toHaveCount(0);
  held.release();await expectOneWorkspace(page);
 }finally{await held.close();}
});

test('the workspace loading status remains visible without chart subscriptions until native initialization is ready',async({page})=>{
 await page.addInitScript(()=>{
  let release!:()=>void,bridge:any;
  const ready=new Promise<void>(resolve=>{release=resolve;});
  (window as any).__releaseStartupWorkspace=release;
  Object.defineProperty(window,'go',{configurable:true,get:()=>bridge,set:value=>{
   bridge=value;const load=bridge.main.App.LoadWorkspace;
   bridge.main.App.LoadWorkspace=async()=>{await ready;return load();};
  }});
 });
 await page.goto('/');await waitForTestBridge(page);
 await expect(page.getByRole('status')).toHaveText('Opening saved workspace…');
 await expect(page.getByRole('status')).toHaveAttribute('aria-busy','true');
 await expect(page.getByTestId('chart-cell')).toHaveCount(0);
 expect(await page.evaluate(()=>(window as any).__testLeases())).toBe(0);
 await page.evaluate(()=>(window as any).__releaseStartupWorkspace());await expectOneWorkspace(page);
});

test('a failed application import shows an actionable retry and reload mounts one workspace',async({page})=>{
 const pattern='**/src/Application.tsx*';
 await page.route(pattern,route=>route.abort('failed'));
 await page.goto('/');
 await expect(page.getByRole('alert')).toContainText('Unable to load the application');
 await expect(page.getByTestId('chart-cell')).toHaveCount(0);
 expect(await page.evaluate(()=>(window as any).__testLeases())).toBe(0);
 await page.unroute(pattern);
 const reloaded=page.waitForEvent('load');
 await page.getByRole('button',{name:'Retry loading',exact:true}).click();await reloaded;
 await expectOneWorkspace(page);
 await expect(page.getByRole('alert')).toHaveCount(0);
});

test('splash hides the mounted restored layout until native maximization and close listeners are ready',async({page})=>{
 const workspace=firstWorkspace();workspace.theme='dark';workspace.tabs[0].name='Restored ready workspace';
 await page.addInitScript(workspace=>{
  sessionStorage.setItem('cryptodesk-explicit-test-workspace',JSON.stringify(workspace));
  let nativeReady!:()=>void,listenersReady!:()=>void,bridge:any;
  const native=new Promise<void>(resolve=>{nativeReady=resolve;}),listeners=new Promise<void>(resolve=>{listenersReady=resolve;});
  (window as any).__releaseNativePresentation=nativeReady;(window as any).__releaseCloseListeners=listenersReady;(window as any).__startupPresentations=0;
  Object.defineProperty(window,'go',{configurable:true,get:()=>bridge,set:value=>{
   bridge=value;bridge.main.App.StartupPainted=async()=>{(window as any).__startupPresentations++;await native;};
   bridge.main.App.FrontendReady=async()=>{await listeners;};
  }});
 },workspace);
 await page.goto('/');await waitForTestBridge(page);
 await expect(page.getByTestId('chart-cell')).toHaveCount(1);
 await expect(page.getByTestId('startup-splash')).toContainText('Preparing saved layout…');
 await expect(page.locator('.app-shell')).toHaveAttribute('inert','');
 await expect(page.locator('.app-shell')).toHaveAttribute('aria-hidden','true');
 await expect(page.getByTestId('chart-cell')).toBeHidden();
 await expect(page.getByTestId('startup-splash')).toHaveCSS('background-color','rgb(238, 242, 245)');
 await expect(page.locator('html')).toHaveAttribute('data-theme','dark');
 await page.keyboard.press('Control+t');
 await page.keyboard.press('Control+Shift+n');
 expect(await page.evaluate(()=>window.__cryptodeskDiagnostics?.())).toMatchObject({savedTabs:1,savedCharts:1});
 await expect(page.getByTestId('chart-cell')).toHaveCount(1);
 await expect.poll(()=>page.evaluate(()=>(window as any).__startupPresentations)).toBeGreaterThan(0);
 await page.evaluate(()=>(window as any).__releaseNativePresentation());
 await expect(page.getByTestId('startup-splash')).toBeVisible();await expect(page.getByTestId('chart-cell')).toBeHidden();
 await page.evaluate(()=>(window as any).__releaseCloseListeners());
 await expect(page.getByTestId('startup-splash')).toHaveCount(0);
 await expect(page.locator('.app-shell')).toHaveAttribute('data-startup-ready','true');
 await expect(page.getByTestId('chart-cell')).toBeVisible();
 await expect(page.getByRole('tab',{name:/Restored ready workspace/})).toBeVisible();
 await expect.poll(()=>page.evaluate(()=>(window as any).__testLeases())).toBe(1);
 await page.keyboard.press('Control+t');
 await expect.poll(()=>page.evaluate(()=>window.__cryptodeskDiagnostics?.())).toMatchObject({savedTabs:2,savedCharts:2});
 await expect(page.getByTestId('chart-cell')).toHaveCount(1);
});

test('workspace failure remains visible and receives the initial native splash handshake',async({page})=>{
 await page.addInitScript(()=>{
  let bridge:any;(window as any).__startupPresentations=0;
  Object.defineProperty(window,'go',{configurable:true,get:()=>bridge,set:value=>{
   bridge=value;bridge.main.App.StartupPainted=async()=>{(window as any).__startupPresentations++;};
   bridge.main.App.LoadWorkspace=async()=>{throw Error('Explicit startup storage failure');};
  }});
 });
 await page.goto('/');await waitForTestBridge(page);
 await expect(page.getByText('Unable to open workspace',{exact:true})).toBeVisible();
 await expect(page.getByText('Explicit startup storage failure',{exact:true})).toBeVisible();
 await expect(page.getByRole('button',{name:'Retry loading',exact:true})).toBeVisible();
 await expect.poll(()=>page.evaluate(()=>(window as any).__startupPresentations)).toBeGreaterThan(0);
 await expect(page.getByTestId('chart-cell')).toHaveCount(0);
});

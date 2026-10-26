import {test,expect,type Route} from '@playwright/test';
import {openTestApp,reloadTestApp} from './helpers/testApp';
import type {ScreenerDataFile} from '../src/types';

test('fixture access waits for a delayed bridge import on navigation and reload',async({page})=>{
 const fixture:ScreenerDataFile={id:'delayed-bridge-file',name:'btcusd-d1-2024-01-01-2024-01-02.csv',symbol:'btcusd',timeframe:'d1',from:'2024-01-01',to:'2024-01-02',firstTimestamp:1704067200000,lastTimestamp:1704067200000,fields:['timestamp','open','high','low','close','volume'],bytes:128};
 for(const navigate of [openTestApp,reloadTestApp]){
  let release!:()=>void,moduleRequested=false,navigationReady=false;
  const released=new Promise<void>(resolve=>{release=resolve;});
  const holdBridge=async(route:Route)=>{moduleRequested=true;await released;await route.continue();};
  await page.route('**/src/test/browserBridge.ts*',holdBridge);
  const navigation=navigate(page).then(()=>{navigationReady=true;});
  try{
   await expect.poll(()=>moduleRequested).toBe(true);
   await page.waitForLoadState('load');
   // The browser's load event has fired, but the asynchronous bridge import is held.
   expect(await page.evaluate(()=>({
    ready:(window as any).__testBridgeReady,
    setter:typeof(window as any).__testSetEnrichmentFiles,
   }))).toEqual({ready:undefined,setter:'undefined'});
   expect(navigationReady).toBe(false);
   release();
   await navigation;
   await page.evaluate(file=>(window as any).__testSetEnrichmentFiles([file]),fixture);
   expect(await page.evaluate(()=>(window as any).__testEnrichmentFiles())).toEqual([fixture]);
   await page.getByRole('tab',{name:'Screener',exact:true}).click();
   await page.getByRole('tab',{name:'Enrichment',exact:true}).click();
   await expect(page.locator('#screener-enrichment tbody tr')).toContainText(fixture.name);
  }finally{
   release();
   await navigation;
   await page.unroute('**/src/test/browserBridge.ts*',holdBridge);
  }
 }
});

import {test,expect,type Page} from '@playwright/test';
import {openTestApp} from './helpers/testApp';

const names=['Top Bybit','Middle Binance','Bottom Bybit','Disabled Hyperliquid'];
const previousClose=Date.UTC(2026,10,5,10),currentClose=Date.UTC(2026,10,6,12);
const history=(page:Page)=>page.getByRole('table',{name:'Alert check history',exact:true});
const row=(page:Page,name:string)=>history(page).locator('tbody tr').filter({has:page.getByRole('rowheader',{name,exact:true})});
const progress=(page:Page,name:string)=>row(page,name).getByRole('cell').first();

test('finished scans with skipped assets reach 100 percent and show errors only in the row details',async({page})=>{
 await openProgressApp(page);
 const message='xyz:RELIANCE needs 28 closed bars; provider has 10\nTelegram delivery: 1 of 2 recipients confirmed; recipientNotFound';
 await page.evaluate(({message,currentClose})=>{
  const checks=(window as any).__progressRows.map((old:any,index:number)=>({...old,lastChecked:currentClose,status:'completed',manual:true,progress:100,progressState:'completed',phase:index===0?'Completed with errors':'Completed',assetsSkipped:index===0?1:0,error:index===0?message:''}));
  (window as any).__testSetLiveAlertsInfo({checks,running:false,error:message,status:'Waiting for next closed bar'});
 },{message,currentClose});
 await expect(progress(page,names[0])).toContainText('100%');await expect(progress(page,names[0])).toContainText('1 asset skipped');
 await expect(progress(page,names[1])).toContainText('100%');
 await expect(page.locator('.live-alerts > [role="alert"]')).toHaveCount(0);
 await expect(page.locator('.live-alerts-configuration-error')).toHaveCount(0);
 await expect(page.getByRole('button',{name:'Check now',exact:true})).toBeEnabled();
 await row(page,names[0]).getByRole('button',{name:'Manual · Completed with errors',exact:true}).click();
 const details=page.getByRole('dialog',{name:`Check for ${names[0]}`,exact:true});
 await expect(details).toContainText('xyz:RELIANCE needs 28');await expect(details).toContainText('recipientNotFound');
 await page.keyboard.press('Escape');await expect(details).toHaveCount(0);
});

async function openProgressApp(page:Page){
 await openTestApp(page);
 await page.evaluate(async({names,previousClose})=>{
  const api=window.go!.main!.App!,file={id:'row-progress-file',name:'btcusd-d1.csv',symbol:'btcusd',timeframe:'d1',fields:['timestamp','open','high','low','close'],bytes:1024};
  (window as any).__testSetEnrichmentFiles([file]);
  const view={...await api.GetScreenerAnalysisView(),period:'d1',profitabilityFilter:0};
  await api.StartScreenerAnalysis(view);
  (window as any).__testSetAnalysisInfo({active:false,phase:'completed',failed:0,results:[{...file,id:'row-progress-result',fileId:file.id,status:'completed',rows:10,matches:2}]});
  const condition=await api.SaveScreenerAnalysisCondition('Reusable progress rule',view);
  const ids=['c','a','b','d'],sources=['bybit','binance','bybit','hyperliquid'];
  const alerts=names.map((name,index)=>({id:ids[index].repeat(32),name,conditionId:condition.id,period:'h1',dataSource:sources[index],enabled:index!==3}));
  const checks=alerts.map((entry,index)=>({alertId:entry.id,savedConditionId:condition.id,conditionId:entry.id,name:entry.name,assetsScanned:10+index,lastChecked:previousClose,nextCheck:0,sentAlerts:3+index,status:'completed',manual:true,error:''}));
  (window as any).__progressRows=checks;
  (window as any).__testSetLiveAlertsInfo({config:{enabled:true,alerts},checks:[...checks].reverse(),running:false,status:'Waiting for next closed bar',sent:18});
  const originalRead=api.GetScreenerLiveAlerts.bind(api);
  api.GetScreenerLiveAlerts=async()=>{const value=await originalRead();return{...value,checks:[...value.checks].reverse()};};
 },{names,previousClose});
 await page.getByRole('tab',{name:'Screener',exact:true}).click();
 await page.getByRole('tab',{name:'Live Alerts',exact:true}).click();
 await expect(page.getByRole('button',{name:'Check now',exact:true})).toBeEnabled();
 await expect(history(page).getByRole('columnheader')).toHaveText(['Alert name','Progress','Number of assets scanned','Last checked','Next check','Sent alerts','Duration']);
 await expect(history(page).locator('tbody th')).toHaveText(names);
}

async function setProgress(page:Page,mode:'manual'|'scheduled',active:number,percent:number){
 await page.evaluate(({mode,active,percent,currentClose})=>{
  const checks=(window as any).__progressRows.map((old:any,index:number)=>{
   if(index===3)return{...old,progress:100,progressState:'completed',progressMode:'manual',phase:'Previous completed check'};
   if(index<active)return{...old,lastChecked:currentClose,status:'completed',manual:mode==='manual',progress:100,progressState:'completed',progressMode:mode,phase:'Completed',currentAsset:''};
   if(index===active)return{...old,lastChecked:currentClose,status:'running',manual:mode==='manual',progress:percent,progressState:'running',progressMode:mode,phase:'Preparing required history',currentAsset:index===0?'BTCUSDT':'ETHUSDT'};
   return{...old,progress:0,progressState:'queued',progressMode:mode,phase:'Queued',currentAsset:''};
  });
  (window as any).__testSetLiveAlertsInfo({checks:checks.reverse(),running:active<3,status:'Checking latest closed bars',currentAsset:'GLOBAL ASSET MUST STAY HIDDEN',progress:37});
 },{mode,active,percent,currentClose});
}

test('Check now immediately queues enabled rows without rewriting prior history, then displays independent manual alert progress',async({page})=>{
 await openProgressApp(page);
 await page.evaluate(()=>{
  const api=window.go!.main!.App!,original=api.CheckScreenerLiveAlerts.bind(api);
  api.CheckScreenerLiveAlerts=()=>new Promise(resolve=>{(window as any).__releaseProgressCheck=async()=>resolve(await original());});
 });
 await page.getByRole('button',{name:'Check now',exact:true}).click();
 for(const name of names.slice(0,3)){
  await expect(progress(page,name)).toContainText('Requesting check');
  await expect(progress(page,name)).toContainText('Manual check');
  await expect(row(page,name).getByRole('cell').nth(2)).toContainText('2026-11-05 10:00:00 UTC');
  await expect(row(page,name).getByRole('cell').nth(2)).toContainText('Manual · Completed');
 }
 await expect(progress(page,names[3])).not.toContainText('Queued');
 await expect(page.getByRole('button',{name:'Check now',exact:true})).toBeDisabled();
 await page.evaluate(()=>(window as any).__releaseProgressCheck());
 await setProgress(page,'manual',0,25);
 await expect(progress(page,names[0])).toContainText('25%');await expect(progress(page,names[0])).toContainText('BTCUSDT');
 await expect(progress(page,names[0]).getByRole('progressbar',{name:`Progress for ${names[0]}`,exact:true})).toHaveAttribute('value','25');
 await expect(progress(page,names[1])).toContainText('Queued');
 await setProgress(page,'manual',1,60);
 await expect(progress(page,names[0])).toContainText('Completed');await expect(progress(page,names[0])).toContainText('100%');
 await expect(progress(page,names[1])).toContainText('ETHUSDT');await expect(progress(page,names[1])).toContainText('60%');
 await expect(progress(page,names[2])).toContainText('Queued');
 await expect(history(page).locator('tbody th')).toHaveText(names);
 await expect(page.getByRole('heading',{name:'Checking latest closed bars',exact:true})).toHaveCount(0);
 await expect(page.getByText('GLOBAL ASSET MUST STAY HIDDEN',{exact:true})).toHaveCount(0);
 await expect(page.getByRole('progressbar',{name:'Live alerts progress',exact:true})).toHaveCount(0);
 expect(await page.evaluate(()=>(window as any).__testLiveAlertsWrites())).toEqual([]);
});

test('parallel checks keep independent progress and completion in stable table rows, then Stop cancels every active row',async({page})=>{
 await openProgressApp(page);
 await page.evaluate(({currentClose})=>{
  const checks=(window as any).__progressRows.map((old:any,index:number)=>{
   if(index===3)return old;
   return{...old,lastChecked:currentClose,status:'running',manual:true,progress:[24,67,41][index],progressState:'running',progressMode:'manual',phase:index===1?'Checking latest closed public bars':'Fetching public market history and indicators',currentAsset:['bybit BTCUSDT','binance ETHUSDT','bybit SOLUSDT'][index]};
  });
  (window as any).__testSetLiveAlertsInfo({checks:checks.reverse(),running:true});
 },{currentClose});
 await expect(progress(page,names[0])).toContainText('24%');await expect(progress(page,names[0])).toContainText('bybit BTCUSDT');
 await expect(progress(page,names[1])).toContainText('67%');await expect(progress(page,names[1])).toContainText('binance ETHUSDT');
 await expect(progress(page,names[2])).toContainText('41%');await expect(progress(page,names[2])).toContainText('bybit SOLUSDT');
 await expect(page.getByRole('button',{name:'Check now',exact:true})).toBeDisabled();
 await page.evaluate(()=>{
  const info=(window as any).__testLiveAlertsInfo();
  (window as any).__testSetLiveAlertsInfo({checks:info.checks.map((check:any)=>check.name==='Middle Binance'?{...check,status:'completed',progress:100,progressState:'completed',phase:'Completed',currentAsset:''}:check)});
 });
 await expect(progress(page,names[1])).toContainText('100%');await expect(progress(page,names[0])).toContainText('24%');
 await expect(history(page).locator('tbody th')).toHaveText(names);
 await page.getByRole('button',{name:'Stop alerts',exact:true}).click();
 await expect(progress(page,names[0])).toContainText('Cancelled');await expect(progress(page,names[2])).toContainText('Cancelled');
 await expect(progress(page,names[1])).toContainText('Completed');await expect(progress(page,names[1])).toContainText('100%');
 await expect(row(page,names[3]).getByRole('cell').nth(2)).toContainText('2026-11-05 10:00:00 UTC');
 await expect(page.getByRole('button',{name:'Start alerts',exact:true})).toBeVisible();
});

test('scheduled queue progress follows visible row order while retaining previous attempt metadata for waiting and disabled rows',async({page})=>{
 await openProgressApp(page);
 await setProgress(page,'scheduled',0,12);
 await expect(progress(page,names[0])).toContainText('12%');await expect(progress(page,names[0])).toContainText('Scheduled check');
 await expect(row(page,names[0]).getByRole('cell').nth(2)).toContainText('Scheduled · Running');
 for(const name of names.slice(1,3)){
  await expect(progress(page,name)).toContainText('Queued');await expect(progress(page,name)).toContainText('Scheduled check');
  await expect(row(page,name).getByRole('cell').nth(2)).toContainText('Manual · Completed');
  await expect(row(page,name).getByRole('cell').nth(2)).toContainText('2026-11-05 10:00:00 UTC');
 }
 await expect(progress(page,names[3])).toContainText('Previous completed check');
 await expect(progress(page,names[3])).not.toContainText('Scheduled check');
 await setProgress(page,'scheduled',2,75);
 await expect(progress(page,names[0])).toContainText('100%');await expect(progress(page,names[1])).toContainText('100%');
 await expect(progress(page,names[2])).toContainText('75%');await expect(progress(page,names[2])).toContainText('Scheduled check');
 await setProgress(page,'scheduled',3,100);
 for(const name of names.slice(0,3))await expect(progress(page,name)).toContainText('Completed');
 await expect(history(page).locator('tbody th')).toHaveText(names);
 await expect(page.getByRole('button',{name:'Check now',exact:true})).toBeEnabled();
 expect(await page.evaluate(()=>(window as any).__testLiveAlertsChecks())).toBe(0);
});

test('failed progress stays distinct from check history and a manual retry can be stopped without an aggregate progress bar',async({page})=>{
 await openProgressApp(page);
 await page.evaluate(()=>{
  const checks=(window as any).__progressRows.map((old:any,index:number)=>index===0?{...old,status:'failed',manual:false,error:'Explicit public history failure',progress:38,progressState:'failed',progressMode:'scheduled',phase:'Public history failed',currentAsset:'BTCUSDT'}:old);
  (window as any).__testSetLiveAlertsInfo({checks,running:false});
 });
 await expect(progress(page,names[0])).toContainText('Public history failed');await expect(progress(page,names[0])).toContainText('38%');
 await row(page,names[0]).getByRole('button',{name:'Scheduled · Failed',exact:true}).click();
 await expect(page.getByRole('dialog',{name:`Check for ${names[0]}`,exact:true})).toContainText('Explicit public history failure');
 await page.getByRole('button',{name:'Close check error',exact:true}).click();
 await page.getByRole('button',{name:'Check now',exact:true}).click();
 await expect(progress(page,names[0])).toContainText('Queued');await expect(progress(page,names[0])).toContainText('Manual check');
 await setProgress(page,'manual',0,20);
 await expect(progress(page,names[0])).toContainText('20%');
 await page.getByRole('button',{name:'Stop alerts',exact:true}).click();
 for(const name of names.slice(0,3))await expect(progress(page,name)).toContainText('Cancelled');
 await expect(page.getByRole('button',{name:'Start alerts',exact:true})).toBeVisible();
 await expect(page.getByRole('progressbar',{name:'Live alerts progress',exact:true})).toHaveCount(0);
 expect(await page.evaluate(()=>(window as any).__testLiveAlertsWrites().at(-1).enabled)).toBe(false);
 expect(await page.evaluate(()=>(window as any).__testLiveAlertsInfo().sent)).toBe(18);
});

test('live steps keep updating while metadata, saved rules and the Check now response are blocked',async({page})=>{
 await openProgressApp(page);
 await page.evaluate(()=>{
  const api=window.go!.main!.App!,read=api.GetScreenerLiveAlerts.bind(api),saved=api.GetScreenerSavedConditions.bind(api),check=api.CheckScreenerLiveAlerts.bind(api);
  api.GetScreenerLiveAlerts=async()=>{const old=await read();(window as any).__fullProgressReadBlocked=true;await new Promise<void>(resolve=>{(window as any).__releaseFullProgressRead=resolve;});return{...old,savedConditionsRevision:old.savedConditionsRevision+1};};
  api.GetScreenerSavedConditions=async()=>{const value=await saved();(window as any).__savedProgressReadBlocked=true;await new Promise<void>(resolve=>{(window as any).__releaseSavedProgressRead=resolve;});return value;};
  api.CheckScreenerLiveAlerts=async()=>{const old=await check();await new Promise<void>(resolve=>{(window as any).__releaseSlowProgressCheck=resolve;});return old;};
 });
 await expect.poll(()=>page.evaluate(()=>(window as any).__fullProgressReadBlocked)).toBe(true);
 await page.getByRole('button',{name:'Check now',exact:true}).click();
 for(const phase of ['Loading saved rules','Downloading history · waiting for provider pacing or network','Enriching indicators']){
  await page.evaluate(({phase,currentClose})=>{
   const checks=(window as any).__progressRows.map((old:any,index:number)=>index===0?{...old,lastChecked:currentClose,status:'running',manual:true,progress:35,progressState:'running',phase,currentAsset:'Hyperliquid BTC perpetual d1'}:old);
   (window as any).__testSetLiveAlertsInfo({checks,running:true,status:phase});
  },{phase,currentClose});
  await expect(progress(page,names[0])).toContainText(phase);
  await expect(progress(page,names[0])).toContainText('Hyperliquid BTC perpetual d1');
  await expect(progress(page,names[0])).not.toContainText('Queued');
 }
 // Release stale responses; they may refresh metadata but must not restore Queued.
 await page.evaluate(()=>(window as any).__releaseSlowProgressCheck());
 await page.evaluate(()=>(window as any).__releaseFullProgressRead());
 await expect.poll(()=>page.evaluate(()=>(window as any).__savedProgressReadBlocked)).toBe(true);
 await page.evaluate(()=>(window as any).__releaseSavedProgressRead());
 await expect(progress(page,names[0])).toContainText('Enriching indicators');
 await expect(progress(page,names[0])).toContainText('35%');
 await expect(history(page).locator('tbody th')).toHaveText(names);
});

test('a progress sample taken during Stop cannot restore the old enabled check after Stop finishes',async({page})=>{
 await openProgressApp(page);await setProgress(page,'manual',0,20);
 await page.evaluate(()=>{
  const api=window.go!.main!.App!,configure=api.ConfigureScreenerLiveAlerts.bind(api);
  api.ConfigureScreenerLiveAlerts=async value=>{if(!value.enabled){(window as any).__stopProgressActionBlocked=true;await new Promise<void>(resolve=>{(window as any).__releaseStopProgressAction=resolve;});}return configure(value);};
 });
 await page.getByRole('button',{name:'Stop alerts',exact:true}).click();
 await expect.poll(()=>page.evaluate(()=>(window as any).__stopProgressActionBlocked)).toBe(true);
 await page.evaluate(()=>{
  const api=window.go!.main!.App!,read=api.GetScreenerLiveAlertsProgress.bind(api);let held=false;
  api.GetScreenerLiveAlertsProgress=async()=>{
   if(held)return new Promise(()=>{});
   held=true;const old=await read();(window as any).__oldStopProgressBlocked=true;
   await new Promise<void>(resolve=>{(window as any).__releaseOldStopProgress=resolve;});return old;
  };
 });
 await expect.poll(()=>page.evaluate(()=>(window as any).__oldStopProgressBlocked)).toBe(true);
 await page.evaluate(()=>(window as any).__releaseStopProgressAction());
 await expect(page.getByRole('button',{name:'Start alerts',exact:true})).toBeVisible();
 await page.evaluate(()=>(window as any).__releaseOldStopProgress());
 await expect(progress(page,names[0])).toContainText('Cancelled');
 await expect(page.getByRole('button',{name:'Start alerts',exact:true})).toBeVisible();
 await expect(page.getByRole('button',{name:'Stop alerts',exact:true})).toHaveCount(0);
});


test('young listings finish without red errors and show a bounded history-wait count until ready',async({page})=>{
 await openProgressApp(page);
 await page.evaluate(({currentClose})=>{
  const checks=(window as any).__progressRows.map((old:any,index:number)=>({...old,lastChecked:currentClose,status:'completed',manual:false,progress:100,progressState:'completed',phase:index===0?'Completed; waiting for more history':'Completed',assetsWarmingUp:index===0?12:0,assetsSkipped:0,error:''}));
  (window as any).__testSetLiveAlertsInfo({checks,running:false,error:'',status:'Waiting for next closed bar'});
 },{currentClose});
 await expect(progress(page,names[0])).toContainText('12 assets waiting for more history');
 await expect(progress(page,names[0])).toContainText('100%');
 await expect(row(page,names[0]).getByRole('button',{name:/Completed with errors/})).toHaveCount(0);
 await expect(row(page,names[0])).toContainText('Scheduled · Completed');
 await page.evaluate(({currentClose})=>{
  const checks=(window as any).__progressRows.map((old:any)=>({...old,lastChecked:currentClose+3600000,status:'completed',manual:false,progress:100,progressState:'completed',phase:'Completed',assetsScanned:22,assetsSkipped:0,error:''}));
  (window as any).__testSetLiveAlertsInfo({checks,running:false,error:'',status:'Waiting for next closed bar'});
 },{currentClose});
 await expect(progress(page,names[0])).not.toContainText('waiting for more history');
 await expect(row(page,names[0]).getByRole('cell').nth(1)).toHaveText('22');
});


test('Sent alerts resets for each real check while lifetime totals remain separate from legacy records',async({page})=>{
 await openProgressApp(page);
 await page.evaluate(({currentClose})=>{
  const checks=(window as any).__progressRows.map((old:any,index:number)=>({...old,lastChecked:currentClose,status:'completed',manual:true,progressState:'completed',phase:'Completed',progress:100,sentAlerts:0,sentAlertsKnown:false,lifetimeSentAlerts:index===0?37:old.sentAlerts}));
  (window as any).__testSetLiveAlertsInfo({checks,sent:52,running:false,error:'',status:'Waiting for next closed bar'});
  const api=window.go!.main!.App!,original=api.CheckScreenerLiveAlerts.bind(api);
  api.CheckScreenerLiveAlerts=async()=>{
   await original();const info=(window as any).__testLiveAlertsInfo();
   const enabled=new Set(info.config.alerts.filter((entry:any)=>entry.enabled).map((entry:any)=>entry.id));
   const checks=info.checks.map((old:any)=>enabled.has(old.alertId??old.conditionId)?{...old,lastChecked:currentClose+1000,status:'running',manual:true,progressState:'running',phase:'Checking latest closed bars',progress:50,sentAlerts:0,sentAlertsKnown:true}:old);
   (window as any).__testSetLiveAlertsInfo({checks,running:true,error:'',status:'Checking latest closed bars'});
   return api.GetScreenerLiveAlerts();
  };
 },{currentClose});
 const count=row(page,names[0]).getByRole('cell').nth(4);
 await expect(count).toHaveText('—');await expect(count).toHaveAttribute('title',/Lifetime confirmed signals: 37/);
 await expect(page.getByText('Lifetime total: 52',{exact:true})).toBeVisible();
 await page.getByRole('button',{name:'Check now',exact:true}).click();
 await expect(count).toHaveText('0');await expect(count).toHaveAttribute('title',/at least one recipient/);
 await expect(page.getByText('Lifetime total: 52',{exact:true})).toBeVisible();
 await page.evaluate(()=>{
  const info=(window as any).__testLiveAlertsInfo();const checks=info.checks.map((old:any)=>({...old,status:'completed',progressState:'completed',progress:100,phase:'Completed',...(old.name==='Top Bybit'?{sentAlerts:1,lifetimeSentAlerts:38}:{})}));
  (window as any).__testSetLiveAlertsInfo({checks,running:false,sent:53,status:'Waiting for next closed bar'});
 });
 await expect(count).toHaveText('1');await expect(count).toHaveAttribute('title',/Lifetime confirmed signals: 38/);
 await expect(page.getByRole('button',{name:'Check now',exact:true})).toBeEnabled();
 await page.getByRole('button',{name:'Check now',exact:true}).click();
 await expect(count).toHaveText('0');await expect(count).toHaveAttribute('title',/Lifetime confirmed signals: 38/);
 await expect(page.getByText('Lifetime total: 53',{exact:true})).toBeVisible();
});

test('Duration shows total backend elapsed time and trigger, updates through all phases and keeps finished and legacy rows stable',async({page})=>{
 await openProgressApp(page);
 const duration=(name:string)=>row(page,name).getByRole('cell').last();
 await expect(duration(names[0])).toHaveText('—');
 await expect(duration(names[0])).toHaveAttribute('title','Duration was not recorded for this check.');
 await page.evaluate(({currentClose})=>{
  const checks=(window as any).__progressRows.map((old:any,index:number)=>index===0?{...old,triggeredAt:currentClose,durationKnown:true,durationMs:65000,status:'running',progressState:'running',progress:20,phase:'Downloading required history',currentAsset:'BTCUSDT'}:old);
  (window as any).__testSetLiveAlertsInfo({checks,running:true});
 },{currentClose});
 await expect(duration(names[0])).toContainText('1m 5s');
 await expect(duration(names[0])).toContainText('Triggered 2026-11-06 12:00:00 UTC');
 await expect(duration(names[0])).toHaveAttribute('title',/worker wait, downloads, enrichment, evaluation and delivery/);
 await page.evaluate(()=>{
  const info=(window as any).__testLiveAlertsInfo();
  (window as any).__testSetLiveAlertsInfo({checks:info.checks.map((old:any)=>old.name==='Top Bybit'?{...old,durationMs:126000,progress:60,phase:'Enriching all required assets',currentAsset:'ETHUSDT'}:old)});
 });
 await expect(progress(page,names[0])).toContainText('Enriching all required assets');
 await expect(duration(names[0])).toContainText('2m 6s');
 await page.evaluate(()=>{
  const info=(window as any).__testLiveAlertsInfo();
  (window as any).__testSetLiveAlertsInfo({checks:info.checks.map((old:any)=>old.name==='Top Bybit'?{...old,durationMs:130000,status:'completed',progressState:'completed',progress:100,phase:'Completed',currentAsset:''}:old),running:false});
 });
 await expect(duration(names[0])).toContainText('2m 10s');
 await expect(duration(names[1])).toHaveText('—');
 await page.getByRole('button',{name:'Check now',exact:true}).click();
 await expect(progress(page,names[0])).toContainText('Queued');
 await expect(duration(names[0])).toContainText('2m 10s');
 await page.evaluate(()=>{
  const info=(window as any).__testLiveAlertsInfo();
  (window as any).__testSetLiveAlertsInfo({checks:info.checks.map((old:any)=>old.name==='Top Bybit'?{...old,durationKnown:true,triggeredAt:old.triggeredAt+300000,durationMs:0,status:'running',progressState:'running',phase:'Downloading required history'}:old),running:true});
 });
 await expect(duration(names[0])).toContainText('0s');
 await expect(duration(names[0])).toContainText('Triggered 2026-11-06 12:05:00 UTC');
});

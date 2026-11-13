import {test,expect,type Page} from '@playwright/test';
import {openTestApp} from './helpers/testApp';

const names=['Dukascopy daily','Hyperliquid hourly','Paused weekly'];
const ids=['a'.repeat(32),'b'.repeat(32),'c'.repeat(32)];
const table=(page:Page)=>page.getByRole('table',{name:'Configured alerts',exact:true});
const history=(page:Page)=>page.getByRole('table',{name:'Alert check history',exact:true});
const historyRow=(page:Page,name:string)=>history(page).locator('tbody tr').filter({has:page.getByRole('rowheader',{name,exact:true})});
const progress=(page:Page,name:string)=>historyRow(page,name).locator('..');
const check=(page:Page,name:string)=>table(page).getByRole('button',{name:`Check alert ${name} now`,exact:true});

async function open(page:Page,enabled=true){
 await openTestApp(page);
 await page.evaluate(async({names,ids,enabled})=>{
  const api=window.go!.main!.App!,file={id:'row-check-d1',name:'btcusd-d1.csv',symbol:'btcusd',timeframe:'d1',fields:['timestamp','open','high','low','close'],bytes:1024};
  (window as any).__testSetEnrichmentFiles([file]);
  const request={...await api.GetScreenerAnalysisView(),period:'d1',profitabilityFilter:0};
  await api.StartScreenerAnalysis(request);
  (window as any).__testSetAnalysisInfo({active:false,phase:'completed',failed:0,results:[{...file,id:'row-check-result',fileId:file.id,status:'completed',rows:10,matches:2}]});
  const condition=await api.SaveScreenerAnalysisCondition('Shared saved rule',request);
  const alerts=names.map((name,index)=>({id:ids[index],name,conditionId:condition.id,period:['d1','h1','w1'][index],dataSource:index===0?'dukascopy':'hyperliquid',enabled:index!==2}));
  const checks=alerts.map((entry,index)=>({alertId:entry.id,savedConditionId:condition.id,conditionId:entry.id,name:entry.name,assetsScanned:7+index,lastChecked:Date.UTC(2026,10,8,12),nextCheck:Date.UTC(2026,10,10,12),sentAlerts:2+index,status:'completed',manual:false,error:'',progress:100,progressState:'completed',phase:'Previous completed check'}));
  (window as any).__testSetLiveAlertsInfo({config:{enabled,alerts},checks,running:false,status:enabled?'Waiting for next closed bar':'Disabled',sent:9});
 },{names,ids,enabled});
 await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Live Alerts',exact:true}).click();
 await expect(table(page).locator('tbody tr')).toHaveCount(3);
}

test('row Check now queues only the exact alert, preserving sibling history and config when rows share one saved condition',async({page})=>{
 await page.setViewportSize({width:1100,height:900});await open(page);
 await expect(table(page).getByRole('columnheader')).toHaveText(['','Alert name','Saved condition','Period','Data source','Assets','Actions','Check']);
 const before=await page.evaluate(()=>(window as any).__testLiveAlertsInfo());
 expect(new Set(before.config.alerts.map((entry:any)=>entry.conditionId)).size).toBe(1);
 await page.evaluate(()=>{const api=window.go!.main!.App!,original=api.CheckScreenerLiveAlert.bind(api);(window as any).__rowCheckCalls=[];api.CheckScreenerLiveAlert=id=>new Promise(resolve=>{(window as any).__rowCheckCalls.push(id);(window as any).__releaseRowCheck=async()=>resolve(await original(id));});});
 await check(page,names[1]).click();
 await expect(progress(page,names[1])).toContainText('Requesting check');await expect(progress(page,names[1])).toContainText('Manual check');
 for(const name of [names[0],names[2]]){await expect(progress(page,name)).toContainText('Previous completed check');await expect(progress(page,name)).not.toContainText('Requesting check');}
 await expect(check(page,names[1])).toBeDisabled();await expect(check(page,names[0])).toBeDisabled();await expect(page.getByRole('button',{name:'Check now',exact:true})).toBeDisabled();
 await check(page,names[1]).evaluate((button:HTMLButtonElement)=>button.click());expect(await page.evaluate(()=>(window as any).__rowCheckCalls)).toEqual([ids[1]]);
 await page.evaluate(()=>(window as any).__releaseRowCheck());
 await expect.poll(()=>page.evaluate(()=>(window as any).__testLiveAlertCheckRequests())).toEqual([ids[1]]);
 const after=await page.evaluate(()=>(window as any).__testLiveAlertsInfo());
 expect(after.config).toEqual(before.config);expect(after.checks.filter((row:any)=>row.alertId!==ids[1])).toEqual(before.checks.filter((row:any)=>row.alertId!==ids[1]));
 expect(after.checks.find((row:any)=>row.alertId===ids[1])).toMatchObject({progressState:'queued',progressMode:'manual',lastChecked:before.checks[1].lastChecked,sentAlerts:before.checks[1].sentAlerts});
 expect(await page.evaluate(()=>(window as any).__testLiveAlertsChecks())).toBe(0);expect(await page.evaluate(()=>(window as any).__testLiveAlertsWrites())).toEqual([]);
});

test('row check waits for pending alert saves and only its row shows the pending phases',async({page})=>{
 await open(page);
 await page.evaluate(()=>{(window as any).__testHoldLiveAlertsSaves();const api=window.go!.main!.App!,original=api.CheckScreenerLiveAlert.bind(api);api.CheckScreenerLiveAlert=async id=>{(window as any).__configAtRowCheck=(window as any).__testLiveAlertsInfo().config;return original(id);};});
 await page.getByRole('checkbox',{name:`Enable alert ${names[0]}`,exact:true}).uncheck();
 await check(page,names[1]).click();await expect(progress(page,names[1])).toContainText('Saving alert settings');await expect(progress(page,names[0])).not.toContainText('Saving alert settings');
 expect(await page.evaluate(()=>(window as any).__testLiveAlertCheckRequests())).toEqual([]);
 await page.evaluate(()=>(window as any).__testReleaseLiveAlertsSaves());
 await expect.poll(()=>page.evaluate(()=>(window as any).__testLiveAlertCheckRequests())).toEqual([ids[1]]);
 const atCheck=await page.evaluate(()=>(window as any).__configAtRowCheck);expect(atCheck.enabled).toBe(true);expect(atCheck.alerts.find((entry:any)=>entry.id===ids[0]).enabled).toBe(false);
 expect(await page.evaluate(()=>(window as any).__testLiveAlertsChecks())).toBe(0);
});

test('row checks follow master and row enabled state while the global Check now still queues every enabled alert',async({page})=>{
 await open(page,false);
 await expect(check(page,names[0])).toBeDisabled();await expect(check(page,names[1])).toHaveAttribute('title','Start Live Alerts before checking this alert.');
 await page.getByRole('button',{name:'Start alerts',exact:true}).click();await expect(check(page,names[0])).toBeEnabled();await expect(check(page,names[1])).toBeEnabled();
 await expect(check(page,names[2])).toBeDisabled();await expect(check(page,names[2])).toHaveAttribute('title','Enable this alert before checking it.');
 await page.getByRole('button',{name:'Check now',exact:true}).click();
 await expect(progress(page,names[0])).toContainText('Queued');await expect(progress(page,names[1])).toContainText('Queued');await expect(progress(page,names[2])).toContainText('Previous completed check');
 expect(await page.evaluate(()=>(window as any).__testLiveAlertsChecks())).toBe(1);expect(await page.evaluate(()=>(window as any).__testLiveAlertCheckRequests())).toEqual([]);
});

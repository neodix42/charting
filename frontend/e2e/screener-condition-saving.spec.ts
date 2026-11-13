import {test,expect,type Page} from '@playwright/test';
import {openTestApp,reloadTestApp} from './helpers/testApp';

async function open(page:Page){
 await openTestApp(page);await page.evaluate(()=>(window as any).__testSetEnrichmentFiles([{id:'btc-h1',name:'btcusd-h1.csv',symbol:'btcusd',timeframe:'h1',fields:['timestamp','open','high','low','close'],bytes:1024}]));await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Analysis',exact:true}).click();await expect(page.getByRole('button',{name:'Save as new',exact:true})).toBeEnabled();
}
async function create(page:Page,name:string){
 await page.getByRole('button',{name:'Save as new',exact:true}).click();await page.getByLabel('Condition name',{exact:true}).fill(name);const dialog=page.getByRole('dialog',{name:'Save condition',exact:true});await dialog.getByRole('button',{name:'Save condition',exact:true}).click();await expect(dialog).toHaveCount(0);
}

test('Save condition updates the loaded identity without a prompt, while Save as new preserves it',async({page})=>{
 await open(page);const save=page.getByRole('button',{name:'Save condition',exact:true});await expect(save).toBeDisabled();await expect(page.getByText(/Loaded condition:/)).toHaveCount(0);
 await create(page,'Original');const original=await page.evaluate(()=>(window as any).__testSavedAnalysisConditions()[0]);
 await page.getByLabel('Condition 1 right constant',{exact:true}).fill('2');await create(page,'Second');const second=await page.evaluate(()=>(window as any).__testSavedAnalysisConditions()[1]);expect(second.request.conditions[0].right.value).toBe(2);expect((await page.evaluate(()=>(window as any).__testSavedAnalysisConditions()[0])).request).toEqual(original.request);
 const selected=page.getByRole('button',{name:'Load condition Original',exact:true});await selected.click();await page.getByLabel('Condition 1 right constant',{exact:true}).fill('3');await page.getByLabel('Analysis period of data',{exact:true}).selectOption('h1');await page.getByLabel('Analysis profitability filter',{exact:true}).selectOption('95');await expect(selected).toContainText('Modified draft');await save.click();await expect(page.getByRole('dialog')).toHaveCount(0);await expect(selected).not.toContainText('Modified draft');
 const updated=await page.evaluate(()=>(window as any).__testSavedAnalysisConditions());expect(updated).toHaveLength(2);expect(updated[0]).toMatchObject({id:original.id,name:'Original',request:{period:'h1',profitabilityFilter:95}});expect(updated[0].request.conditions[0].right.value).toBe(3);expect(updated[1]).toEqual(second);
 await expect(save).toBeEnabled();await save.click();await expect(selected).not.toContainText('Modified draft');
 await reloadTestApp(page);await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Analysis',exact:true}).click();await expect(selected).toHaveAttribute('aria-pressed','true');await expect(page.getByLabel('Condition 1 right constant',{exact:true})).toHaveValue('3');await expect(save).toBeEnabled();
});

test('failed updates preserve the saved rule and draft, retry without naming, and follow rename/delete selection',async({page})=>{
 await open(page);await create(page,'Selected');const before=await page.evaluate(()=>(window as any).__testSavedAnalysisConditions()[0]);const save=page.getByRole('button',{name:'Save condition',exact:true}),selected=page.getByRole('button',{name:'Load condition Selected',exact:true});
 await page.getByLabel('Condition 1 right constant',{exact:true}).fill('4');await page.evaluate(()=>(window as any).__testAnalysisErrors({savedUpdate:'Cannot update condition: disk full'}));await save.click();await expect(page.getByRole('alert')).toContainText('disk full');expect(await page.evaluate(()=>(window as any).__testSavedAnalysisConditions()[0])).toEqual(before);await expect(selected).toContainText('Modified draft');await expect(page.getByLabel('Condition 1 right constant',{exact:true})).toHaveValue('4');await expect(page.getByRole('dialog')).toHaveCount(0);
 await page.evaluate(()=>{(window as any).__testAnalysisErrors({});const api=window.go!.main!.App!,update=api.UpdateScreenerAnalysisCondition;api.UpdateScreenerAnalysisCondition=(id,request)=>update(id,{...request,period:request.period??'',categories:request.categories??null,conditions:request.conditions.map(row=>({...row,right:{field:'',timeframe:'',offset:0,aggregate:'',window:0,...row.right}}))});});await save.click();await expect(page.getByRole('alert')).toHaveCount(0);await expect(selected).not.toContainText('Modified draft');
 await selected.click({button:'right'});await page.getByRole('menuitem',{name:'Rename',exact:true}).click();await page.getByLabel('Condition name',{exact:true}).fill('Renamed');await page.getByRole('dialog',{name:'Rename condition',exact:true}).getByRole('button',{name:'Rename',exact:true}).click();const renamed=page.getByRole('button',{name:'Load condition Renamed',exact:true});await expect(renamed).toHaveAttribute('aria-pressed','true');await expect(save).toBeEnabled();await renamed.click({button:'right'});await page.getByRole('menuitem',{name:'Delete',exact:true}).click();await expect(renamed).toHaveCount(0);await expect(save).toBeDisabled();await expect(page.getByRole('button',{name:'Save as new',exact:true})).toBeEnabled();
});

test('normal close waits for the loaded-condition update to persist',async({page})=>{
 await open(page);await create(page,'Await update');await page.getByLabel('Condition 1 right constant',{exact:true}).fill('7');
 await page.evaluate(()=>{const api=window.go!.main!.App!,update=api.UpdateScreenerAnalysisCondition;api.UpdateScreenerAnalysisCondition=(id,request)=>new Promise(resolve=>{(window as any).__releaseConditionUpdate=async()=>resolve(await update(id,request));});});await page.getByRole('button',{name:'Save condition',exact:true}).click();await expect.poll(()=>page.evaluate(()=>typeof(window as any).__releaseConditionUpdate)).toBe('function');await page.evaluate(()=>(window as any).__testEmit('prepare-close',{token:'condition-update'}));expect(await page.evaluate(()=>(window as any).__testCloseConfirmation())).toBeNull();await page.evaluate(()=>(window as any).__releaseConditionUpdate());await expect.poll(()=>page.evaluate(()=>(window as any).__testCloseConfirmation())).toEqual({token:'condition-update',discard:false});expect((await page.evaluate(()=>(window as any).__testSavedAnalysisConditions()[0])).request.conditions[0].right.value).toBe(7);
});

test('manually matching another saved rule does not select it or enable overwrite',async({page})=>{
 await openTestApp(page);await page.evaluate(async()=>{
  (window as any).__testSetEnrichmentFiles([{id:'btc-h1',name:'btcusd-h1.csv',symbol:'btcusd',timeframe:'h1',fields:['timestamp','open','high','low','close'],bytes:1024}]);
  const api=window.go!.main!.App!,draft=await api.GetScreenerAnalysisView();await api.SaveScreenerAnalysisCondition('Unloaded peer',{...draft,conditions:draft.conditions.map(row=>({...row,right:{kind:'number',value:2}}))});
 });
 await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Analysis',exact:true}).click();
 const peer=page.getByRole('button',{name:'Load condition Unloaded peer',exact:true}),save=page.getByRole('button',{name:'Save condition',exact:true});await expect(peer).toBeEnabled();await expect(peer).toHaveAttribute('aria-pressed','false');await expect(save).toBeDisabled();
 await page.getByLabel('Condition 1 right constant',{exact:true}).fill('2');await expect.poll(()=>page.evaluate(()=>(window as any).__testAnalysisView().conditions[0].right.value)).toBe(2);await expect(peer).toHaveAttribute('aria-pressed','false');await expect(save).toBeDisabled();
 await peer.click();await expect(peer).toHaveAttribute('aria-pressed','true');await expect(save).toBeEnabled();
});

async function addLiveAlert(page:Page,name:string,condition:string){
 await page.getByRole('button',{name:'Add alert',exact:true}).click();const dialog=page.getByRole('dialog',{name:'Add alert',exact:true});await dialog.getByRole('textbox',{name:'Alert name',exact:true}).fill(name);await dialog.getByRole('combobox',{name:'Saved condition',exact:true}).selectOption({label:condition});await dialog.getByRole('button',{name:'Add alert',exact:true}).click();await expect(dialog).toHaveCount(0);
}

test('Live metadata follows delayed update and delete completion without rereading unchanged evidence',async({page})=>{
 await open(page);await create(page,'Pending metadata');const id=await page.evaluate(()=>(window as any).__testSavedAnalysisConditions()[0].id);
 await page.getByLabel('Analysis profitability filter',{exact:true}).selectOption('80');await page.evaluate(()=>{const api=window.go!.main!.App!,update=api.UpdateScreenerAnalysisCondition;api.UpdateScreenerAnalysisCondition=(id,request)=>new Promise(resolve=>{(window as any).__releaseMetadataUpdate=async()=>{api.UpdateScreenerAnalysisCondition=update;resolve(await update(id,request));};});});await page.getByRole('button',{name:'Save condition',exact:true}).click();await expect.poll(()=>page.evaluate(()=>typeof(window as any).__releaseMetadataUpdate)).toBe('function');
 await page.getByRole('tab',{name:'Live Alerts',exact:true}).click();await expect(page.getByRole('button',{name:'Add alert',exact:true})).toBeEnabled();await addLiveAlert(page,'Metadata watcher','Pending metadata');
 const enabled=page.getByRole('checkbox',{name:'Enable alert Metadata watcher',exact:true}),row=page.getByRole('table',{name:'Configured alerts',exact:true}).getByRole('row').filter({has:enabled}),scope=page.getByRole('region',{name:'Monitored assets for Metadata watcher',exact:true});await expect(row).toContainText('Pending metadata');await row.getByRole('button',{name:'View assets for Metadata watcher',exact:true}).click();await expect(scope).toContainText('Minimum profitability: 85%');
 await page.evaluate(()=>(window as any).__releaseMetadataUpdate());await expect(scope).toContainText('Minimum profitability: 80%');await expect(enabled).toBeChecked();await page.getByRole('button',{name:'Close monitored assets',exact:true}).click();await page.getByRole('button',{name:'Save config',exact:true}).click();await expect(page.getByRole('status')).toContainText('Alert configuration saved.');
 const reads=await page.evaluate(()=>(window as any).__testSavedAnalysisConditionsReads()),revision=await page.evaluate(()=>(window as any).__testLiveAlertsInfo().savedConditionsRevision),statusReads=await page.evaluate(()=>(window as any).__testLiveAlertsReads());
 await page.evaluate(async()=>{const api=window.go!.main!.App!,saved=(await api.GetScreenerSavedConditions())[0];await api.UpdateScreenerAnalysisCondition(saved.id,saved.request);});expect(await page.evaluate(()=>(window as any).__testLiveAlertsInfo().savedConditionsRevision)).toBe(revision);
 await expect.poll(()=>page.evaluate(()=>(window as any).__testLiveAlertsReads())).toBeGreaterThan(statusReads);expect(await page.evaluate(()=>(window as any).__testSavedAnalysisConditionsReads())).toBe(reads+1);
 await page.getByRole('tab',{name:'Analysis',exact:true}).click();await page.evaluate(()=>{const api=window.go!.main!.App!,remove=api.DeleteScreenerAnalysisCondition;api.DeleteScreenerAnalysisCondition=id=>new Promise(resolve=>{(window as any).__releaseMetadataDelete=async()=>resolve(await remove(id));});});await page.getByRole('button',{name:'Load condition Pending metadata',exact:true}).click({button:'right'});await page.getByRole('menuitem',{name:'Delete',exact:true}).click();await expect.poll(()=>page.evaluate(()=>typeof(window as any).__releaseMetadataDelete)).toBe('function');
 await page.getByRole('tab',{name:'Live Alerts',exact:true}).click();await expect(enabled).toBeVisible();await page.evaluate(()=>(window as any).__releaseMetadataDelete());await expect(enabled).toHaveCount(0);await expect(page.getByText('Unavailable saved condition',{exact:true})).toHaveCount(0);expect((await page.evaluate(()=>(window as any).__testLiveAlertsInfo().config)).alerts.some((entry:any)=>entry.conditionId===id)).toBe(false);await expect(page.getByRole('table',{name:'Alert check history',exact:true}).getByRole('row')).toHaveCount(1);
});

test('a delayed saved-rule rename updates its condition cell while preserving the custom alert name',async({page})=>{
 await open(page);await create(page,'Before rename');const id=await page.evaluate(()=>(window as any).__testSavedAnalysisConditions()[0].id);
 await page.evaluate(async id=>{const api=window.go!.main!.App!;await api.ConfigureScreenerLiveAlerts({enabled:false,alerts:[{id,conditionId:id,name:'Custom signal',period:'h1',dataSource:'dukascopy',enabled:true}]});const rename=api.RenameScreenerAnalysisCondition;api.RenameScreenerAnalysisCondition=(id,name)=>new Promise(resolve=>{(window as any).__releaseLiveRename=async()=>{api.RenameScreenerAnalysisCondition=rename;resolve(await rename(id,name));};});void api.RenameScreenerAnalysisCondition(id,'After rename');},id);
 await expect.poll(()=>page.evaluate(()=>typeof(window as any).__releaseLiveRename)).toBe('function');
 await page.getByRole('tab',{name:'Live Alerts',exact:true}).click();const enabled=page.getByRole('checkbox',{name:'Enable alert Custom signal',exact:true}),row=page.getByRole('table',{name:'Configured alerts',exact:true}).getByRole('row').filter({has:enabled});await expect(enabled).toBeChecked();await expect(row.locator('td').nth(1)).toHaveText('Before rename');await page.evaluate(()=>(window as any).__releaseLiveRename());await expect(row.locator('td').nth(1)).toHaveText('After rename');await expect(row.getByRole('rowheader')).toHaveText('Custom signal');await expect(enabled).toBeChecked();await expect(page.getByRole('checkbox',{name:'Enable alert After rename',exact:true})).toHaveCount(0);await row.getByRole('button',{name:'View assets for Custom signal',exact:true}).click();await expect(page.getByRole('region',{name:'Monitored assets for Custom signal',exact:true})).toContainText('After rename');await page.getByRole('button',{name:'Close monitored assets',exact:true}).click();await expect(page.getByRole('table',{name:'Alert check history',exact:true}).getByRole('rowheader')).toHaveText(['Custom signal']);
});

test('Analysis rename refreshes Live Alerts immediately while metadata reads are delayed',async({page})=>{
 await open(page);await create(page,'Before immediate rename');
 const id=await page.evaluate(()=>(window as any).__testSavedAnalysisConditions()[0].id);
 await page.evaluate(async id=>{await window.go!.main!.App!.ConfigureScreenerLiveAlerts({enabled:false,alerts:[{id,conditionId:id,name:'Custom watcher',period:'h1',dataSource:'dukascopy',enabled:true}]});},id);
 await page.getByRole('tab',{name:'Live Alerts',exact:true}).click();
 const row=page.getByRole('table',{name:'Configured alerts',exact:true}).getByRole('row').filter({has:page.getByRole('checkbox',{name:'Enable alert Custom watcher',exact:true})});
 await expect(row.locator('td').nth(1)).toHaveText('Before immediate rename');
 const writes=await page.evaluate(()=>(window as any).__testLiveAlertsWrites());
 await page.getByRole('tab',{name:'Analysis',exact:true}).click();
 await page.getByRole('button',{name:'Load condition Before immediate rename',exact:true}).click({button:'right'});
 await page.getByRole('menuitem',{name:'Rename',exact:true}).click();
 const dialog=page.getByRole('dialog',{name:'Rename condition',exact:true});
 await dialog.getByRole('textbox',{name:'Condition name',exact:true}).fill('After immediate rename');
 await page.evaluate(()=>{const api=window.go!.main!.App!,read=api.GetScreenerSavedConditions;let release!:()=>void;const gate=new Promise<void>(resolve=>{release=resolve;});(window as any).__releaseRenameMetadata=()=>{api.GetScreenerSavedConditions=read;release();};api.GetScreenerSavedConditions=async()=>{await gate;return read();};});
 await dialog.getByRole('button',{name:'Rename',exact:true}).click();await expect(dialog).toHaveCount(0);
 await page.getByRole('tab',{name:'Live Alerts',exact:true}).click();
 await expect(row.locator('td').nth(1)).toHaveText('After immediate rename');
 await expect(row.getByRole('rowheader')).toHaveText('Custom watcher');
 await row.getByRole('button',{name:'View assets for Custom watcher',exact:true}).click();
 await expect(page.getByRole('dialog',{name:'Monitored assets for Custom watcher',exact:true})).toContainText('After immediate rename');
 expect(await page.evaluate(()=>(window as any).__testLiveAlertsWrites())).toEqual(writes);
 expect(await page.evaluate(()=>(window as any).__testLiveAlertsInfo().config.enabled)).toBe(false);
 await page.getByRole('button',{name:'Close monitored assets',exact:true}).click();
 await page.evaluate(()=>(window as any).__releaseRenameMetadata());
 await expect(row.locator('td').nth(1)).toHaveText('After immediate rename');
});

test('Analysis rename follows copied alert names before Live Alerts is opened and preserves custom names and history',async({page})=>{
 await open(page);await create(page,'Copied condition');
 const before=await page.evaluate(async()=>{
  const api=window.go!.main!.App!,condition=(window as any).__testSavedAnalysisConditions()[0];
  const entry={id:condition.id,conditionId:condition.id,name:condition.name,period:'h1' as const,dataSource:'hyperliquid' as const,enabled:true};
  const config={enabled:true,alerts:[entry,{...entry,id:crypto.randomUUID().replaceAll('-',''),dataSource:'bybit' as const,enabled:false},{...entry,id:crypto.randomUUID().replaceAll('-',''),name:'Custom signal',dataSource:'binance' as const}]};
  await api.ConfigureScreenerLiveAlerts(config);
  (window as any).__testSetLiveAlertsInfo({sent:9,checks:config.alerts.map((entry,index)=>({alertId:entry.id,savedConditionId:entry.conditionId,conditionId:entry.id,name:entry.name,status:'completed',manual:false,error:'',assetsScanned:index+5,lastChecked:1704067200000,sentAlerts:index+2,nextCheck:0}))});
  return{config,writes:(window as any).__testLiveAlertsWrites().length};
 });
 await page.getByRole('button',{name:'Load condition Copied condition',exact:true}).click({button:'right'});await page.getByRole('menuitem',{name:'Rename',exact:true}).click();
 const dialog=page.getByRole('dialog',{name:'Rename condition',exact:true});await dialog.getByRole('textbox',{name:'Condition name',exact:true}).fill('Renamed condition');
 await page.evaluate(()=>(window as any).__testAnalysisErrors({savedRename:'Rename failed explicitly'}));await dialog.getByRole('button',{name:'Rename',exact:true}).click();await expect(dialog).toContainText('Rename failed explicitly');
 expect(await page.evaluate(()=>(window as any).__testLiveAlertsWrites().length)).toBe(before.writes);
 await page.evaluate(()=>{(window as any).__testAnalysisErrors({});(window as any).__testLiveAlertsErrors({save:'Alert name save failed explicitly'});});await dialog.getByRole('button',{name:'Rename',exact:true}).click();await expect(dialog).toContainText('Condition was renamed, but Live Alerts could not be updated');
 expect(await page.evaluate(()=>(window as any).__testSavedAnalysisConditions()[0].name)).toBe('Renamed condition');expect(await page.evaluate(()=>(window as any).__testLiveAlertsInfo().config)).toEqual(before.config);
 await page.evaluate(()=>(window as any).__testLiveAlertsErrors({}));await dialog.getByRole('button',{name:'Rename',exact:true}).click();await expect(dialog).toHaveCount(0);
 const expected={...before.config,alerts:before.config.alerts.map(entry=>entry.name==='Copied condition'?{...entry,name:'Renamed condition'}:entry)};
 expect(await page.evaluate(()=>(window as any).__testLiveAlertsInfo().config)).toEqual(expected);
 expect(await page.evaluate(()=>(window as any).__testLiveAlertsWrites().length)).toBe(before.writes+2);
 await page.getByRole('tab',{name:'Live Alerts',exact:true}).click();
 const table=page.getByRole('table',{name:'Configured alerts',exact:true});await expect(table.locator('tbody th')).toHaveText(['Renamed condition','Renamed condition','Custom signal']);await expect(table.locator('tbody tr td:nth-child(3)')).toHaveText(['Renamed condition','Renamed condition','Renamed condition']);
 await expect(page.getByRole('button',{name:'Stop alerts',exact:true})).toBeVisible();
 await expect(page.getByRole('table',{name:'Alert check history',exact:true}).locator('tbody tr:has(th[scope="row"]) td:nth-child(6)')).toHaveText(['2','3','4']);
 await reloadTestApp(page);await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Live Alerts',exact:true}).click();await expect(table.locator('tbody th')).toHaveText(['Renamed condition','Renamed condition','Custom signal']);
 expect(await page.evaluate(()=>(window as any).__testLiveAlertsInfo().config)).toEqual(expected);
});

test('normal close waits for copied alert names to finish saving after an Analysis rename',async({page})=>{
 await open(page);await create(page,'Await copied name');
 await page.evaluate(async()=>{const id=(window as any).__testSavedAnalysisConditions()[0].id;await window.go!.main!.App!.ConfigureScreenerLiveAlerts({enabled:false,alerts:[{id,conditionId:id,name:'Await copied name',period:'h1',dataSource:'hyperliquid',enabled:true}]});(window as any).__testHoldLiveAlertsSaves();});
 await page.getByRole('button',{name:'Load condition Await copied name',exact:true}).click({button:'right'});await page.getByRole('menuitem',{name:'Rename',exact:true}).click();
 const dialog=page.getByRole('dialog',{name:'Rename condition',exact:true});await dialog.getByRole('textbox',{name:'Condition name',exact:true}).fill('Copied name saved');await dialog.getByRole('button',{name:'Rename',exact:true}).click();
 await expect.poll(()=>page.evaluate(()=>(window as any).__testLiveAlertsWrites().at(-1)?.alerts?.[0]?.name)).toBe('Copied name saved');
 await page.evaluate(()=>(window as any).__testEmit('prepare-close',{token:'copied-condition-rename'}));expect(await page.evaluate(()=>(window as any).__testCloseConfirmation())).toBeNull();
 await page.evaluate(()=>(window as any).__testReleaseLiveAlertsSaves());await expect.poll(()=>page.evaluate(()=>(window as any).__testCloseConfirmation())).toEqual({token:'copied-condition-rename',discard:false});
 expect(await page.evaluate(()=>(window as any).__testLiveAlertsInfo().config)).toMatchObject({enabled:false,alerts:[{name:'Copied name saved',enabled:true}]});
});

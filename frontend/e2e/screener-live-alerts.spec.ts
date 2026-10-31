import {test,expect,type Page} from '@playwright/test';
import {openTestApp,reloadTestApp} from './helpers/testApp';

const fixtures=[
 {id:'btc-d1',name:'btcusd-d1.csv',symbol:'btcusd',timeframe:'d1'},
 {id:'btc-h1',name:'btcusd-h1.csv',symbol:'btcusd',timeframe:'h1'},
 {id:'eur-h1',name:'eurusd-h1.csv',symbol:'eurusd',timeframe:'h1'},
 {id:'eth-h4',name:'ethusd-h4.csv',symbol:'ethusd',timeframe:'h4'},
].map(file=>({...file,fields:['timestamp','open','high','low','close'],bytes:1024}));

async function open(page:Page){
 await openTestApp(page);
 await page.evaluate(async files=>{
  (window as any).__testSetEnrichmentFiles(files);
  const api=window.go!.main!.App!,view={...await api.GetScreenerAnalysisView(),profitabilityFilter:0};
  await api.SaveScreenerAnalysisCondition('Trend entry',view);
  await api.SaveScreenerAnalysisCondition('Momentum entry',{...view,tradeSimulation:{stopLoss:'low',takeProfit:'high',duration:20}});
 },fixtures);
 expect(await page.evaluate(()=>(window as any).__testLiveAlertsReads())).toBe(0);
 await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Live Alerts',exact:true}).click();
 await expect(page.getByRole('heading',{name:'Live Alerts',exact:true})).toBeVisible();
 await expect(page.getByRole('checkbox',{name:'Alert condition Trend entry',exact:true})).toBeEnabled();
}

async function choose(page:Page){
 await page.getByRole('checkbox',{name:'Alert condition Trend entry',exact:true}).check();
 await page.getByRole('checkbox',{name:'Alert condition Momentum entry',exact:true}).check();
 await page.getByRole('combobox',{name:'Alert period for Momentum entry',exact:true}).selectOption('h1');
 await page.getByRole('button',{name:'Select all assets',exact:true}).click();
 await expect(page.getByRole('button',{name:'Start alerts',exact:true})).toBeEnabled();
}

test('Live Alerts selects saved conditions and downloaded daily/hourly assets, starts explicitly and displays outcomes',async({page})=>{
 await open(page);
 await expect(page.getByRole('checkbox',{name:/Alert asset ETHUSD/})).toHaveCount(0);
 const table=page.getByRole('table',{name:'Saved conditions and periods',exact:true});await expect(table.getByRole('columnheader')).toHaveText(['Saved condition','Period']);
 await expect(page.getByRole('combobox',{name:'Alert period for Trend entry',exact:true})).toBeDisabled();
 await expect(page.getByRole('button',{name:'Start alerts',exact:true})).toBeDisabled();
 await choose(page);
 await page.getByRole('combobox',{name:'Alert period for Momentum entry',exact:true}).selectOption('');
 await expect(page.getByRole('checkbox',{name:'Alert condition Momentum entry',exact:true})).toBeChecked();await expect(page.getByRole('button',{name:'Start alerts',exact:true})).toBeDisabled();
 await page.getByRole('combobox',{name:'Alert period for Momentum entry',exact:true}).selectOption('h1');
 await page.getByRole('button',{name:'Select all assets',exact:true}).click();
 await page.getByRole('button',{name:'Save config',exact:true}).click();await expect(page.getByRole('status')).toContainText('Alert configuration saved.');
 const config=await page.evaluate(()=>(window as any).__testLiveAlertsInfo().config);
 expect(config.enabled).toBe(false);expect(config.conditionPeriods).toHaveLength(2);expect(config.conditionPeriods.map((condition:any)=>condition.period)).toEqual(['d1','h1']);expect(config.symbols).toEqual(['btcusd','eurusd']);
 expect(await page.evaluate(()=>(window as any).__testLiveAlertsWrites().every((config:any)=>!config.enabled))).toBe(true);
 await page.getByRole('button',{name:'Start alerts',exact:true}).click();
 await expect(page.getByRole('button',{name:'Stop alerts',exact:true})).toBeEnabled();
 await expect(page.getByRole('checkbox',{name:'Alert condition Trend entry',exact:true})).toBeDisabled();
 await expect(page.getByRole('combobox',{name:'Alert period for Trend entry',exact:true})).toBeDisabled();await expect(page.getByRole('combobox',{name:'Alert period for Momentum entry',exact:true})).toBeDisabled();
 await page.getByRole('button',{name:'Check now',exact:true}).click();
 await expect(page.getByRole('progressbar',{name:'Live alerts progress',exact:true})).toBeVisible();
 expect(await page.evaluate(()=>(window as any).__testLiveAlertsChecks())).toBe(1);
 await page.evaluate(()=>(window as any).__testSetLiveAlertsInfo({running:false,status:'Completed',lastRun:Date.UTC(2026,9,28,12),sent:1,progress:100,recent:[{assetName:'Bitcoin vs US Dollar',symbol:'btcusd',timeframe:'h1',time:Date.UTC(2026,9,28,11),conditionName:'Trend entry',status:'sent',message:'Indicative entry 100, SL 90, TP 120'},{assetName:'Euro vs US Dollar',symbol:'eurusd',timeframe:'h1',time:Date.UTC(2026,9,28,11),conditionName:'Momentum entry',status:'failed',message:'Telegram delivery rejected'}]}));
 await expect(page.getByRole('region',{name:'Live alert status',exact:true})).toContainText('1 alerts sent');
 await expect(page.getByText('Telegram delivery rejected',{exact:true})).toBeVisible();
 await page.evaluate(()=>(window as any).__testSetLiveAlertsInfo({recent:[{assetName:'Bitcoin vs US Dollar',symbol:'btcusd',timeframe:'h1',time:Date.UTC(2026,9,28,11),conditionName:'Trend entry',status:'uncertain',message:'Delivery was interrupted; check Telegram.'}]}));
 await expect(page.getByRole('region',{name:'Live alert status',exact:true})).toContainText('Delivery uncertain — it may have arrived; no automatic retry');
 await expect(page.getByRole('region',{name:'Live alert status',exact:true})).toContainText('Closed 2026-10-28 12:00:00 UTC');
 await page.getByRole('button',{name:'Stop alerts',exact:true}).click();await expect(page.getByRole('button',{name:'Start alerts',exact:true})).toBeEnabled();
 await expect(page.getByRole('button',{name:'Check now',exact:true})).toBeDisabled();
});

test('Live Alerts persists choices, stops polling hidden panels and searches downloaded categories',async({page})=>{
 await open(page);await choose(page);
 await page.getByRole('searchbox',{name:'Search live alert assets',exact:true}).fill('Forex');
 await expect(page.getByRole('checkbox',{name:/Alert asset BTCUSD/})).toHaveCount(0);await expect(page.getByRole('checkbox',{name:/Alert asset EURUSD/})).toBeVisible();
 await page.getByRole('button',{name:'Save config',exact:true}).click();await expect(page.getByRole('status')).toContainText('Alert configuration saved.');
 await page.getByRole('tab',{name:'Analysis',exact:true}).click();const reads=await page.evaluate(()=>(window as any).__testLiveAlertsReads());await page.waitForTimeout(1700);expect(await page.evaluate(()=>(window as any).__testLiveAlertsReads())).toBe(reads);
 await reloadTestApp(page);await page.evaluate(files=>(window as any).__testSetEnrichmentFiles(files),fixtures);
 await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Live Alerts',exact:true}).click();
 await expect(page.getByRole('checkbox',{name:'Alert condition Trend entry',exact:true})).toBeChecked();await expect(page.getByRole('checkbox',{name:'Alert condition Momentum entry',exact:true})).toBeChecked();
 await expect(page.getByRole('combobox',{name:'Alert period for Trend entry',exact:true})).toHaveValue('d1');await expect(page.getByRole('combobox',{name:'Alert period for Momentum entry',exact:true})).toHaveValue('h1');await expect(page.getByRole('checkbox',{name:/Alert asset BTCUSD/})).toBeChecked();await expect(page.getByRole('checkbox',{name:/Alert asset EURUSD/})).toBeChecked();
 await page.getByRole('button',{name:'Deselect all assets',exact:true}).click();await expect(page.getByRole('button',{name:'Start alerts',exact:true})).toBeDisabled();
});

test('normal close waits for queued live alert choices without starting Telegram delivery',async({page})=>{
 await open(page);await page.evaluate(()=>(window as any).__testHoldLiveAlertsSaves());await choose(page);
 await page.evaluate(()=>(window as any).__testEmit('prepare-close',{token:'live-alerts-settings'}));
 expect(await page.evaluate(()=>(window as any).__testCloseConfirmation())).toBeNull();
 await page.evaluate(()=>(window as any).__testReleaseLiveAlertsSaves());
 await expect.poll(()=>page.evaluate(()=>(window as any).__testCloseConfirmation())).toEqual({token:'live-alerts-settings',discard:false});
 const config=await page.evaluate(()=>(window as any).__testLiveAlertsInfo().config);expect(config.enabled).toBe(false);expect(config.conditionPeriods).toHaveLength(2);expect(config.symbols).toEqual(['btcusd','eurusd']);
});

test('failed settings saves remain visible and retry before enabling Live Alerts',async({page})=>{
 await open(page);await page.evaluate(()=>(window as any).__testLiveAlertsErrors({save:'Alert settings could not be saved'}));
 await page.getByRole('checkbox',{name:'Alert condition Trend entry',exact:true}).check();
 await expect(page.getByRole('alert')).toContainText('Alert settings could not be saved');
 expect(await page.evaluate(()=>(window as any).__testLiveAlertsInfo().config.enabled)).toBe(false);
 await page.evaluate(()=>(window as any).__testLiveAlertsErrors({}));await page.getByRole('button',{name:'Retry',exact:true}).click();
 await expect(page.getByRole('alert')).toHaveCount(0);await choose(page);await page.getByRole('button',{name:'Start alerts',exact:true}).click();await expect(page.getByRole('button',{name:'Stop alerts',exact:true})).toBeVisible();
});

test('a stale status response cannot undo starting alerts and saved conditions refresh on tab return',async({page})=>{
 await open(page);await choose(page);await page.getByRole('button',{name:'Save config',exact:true}).click();await expect(page.getByRole('status')).toContainText('Alert configuration saved.');
 const before=await page.evaluate(()=>(window as any).__testLiveAlertsReads());await page.evaluate(()=>(window as any).__testLiveAlertsReadDelay(1400));
 await expect.poll(()=>page.evaluate(()=>(window as any).__testLiveAlertsReads())).toBeGreaterThan(before);
 await page.getByRole('button',{name:'Start alerts',exact:true}).click();await expect(page.getByRole('button',{name:'Stop alerts',exact:true})).toBeVisible();
 await page.evaluate(()=>(window as any).__testLiveAlertsReadDelay(0));await page.waitForTimeout(1550);await expect(page.getByRole('button',{name:'Stop alerts',exact:true})).toBeVisible();
 await page.getByRole('button',{name:'Stop alerts',exact:true}).click();
 await page.getByRole('tab',{name:'Analysis',exact:true}).click();
 await page.evaluate(async()=>{const api=window.go!.main!.App!,saved=await api.GetScreenerSavedConditions();await api.RenameScreenerAnalysisCondition(saved[0].id,'Renamed alert signal');});
 await page.getByRole('tab',{name:'Live Alerts',exact:true}).click();await expect(page.getByRole('checkbox',{name:'Alert condition Renamed alert signal',exact:true})).toBeChecked();
});

test('deleted conditions and unavailable selected periods remain visible and cannot start alerts',async({page})=>{
 await open(page);await choose(page);await page.getByRole('button',{name:'Save config',exact:true}).click();await expect(page.getByRole('status')).toContainText('Alert configuration saved.');
 await page.getByRole('combobox',{name:'Alert period for Momentum entry',exact:true}).selectOption('d1');
 await expect(page.getByRole('checkbox',{name:/Alert asset EURUSD/})).toHaveCount(0);await expect(page.getByRole('button',{name:'Start alerts',exact:true})).toBeEnabled();
 expect((await page.evaluate(()=>(window as any).__testLiveAlertsInfo().config)).symbols).toEqual(['btcusd']);
 await page.getByRole('tab',{name:'Analysis',exact:true}).click();
 const removed=await page.evaluate(async()=>{const api=window.go!.main!.App!,saved=await api.GetScreenerSavedConditions();await api.DeleteScreenerAnalysisCondition(saved[0].id);return saved[0].id;});
 await page.getByRole('tab',{name:'Live Alerts',exact:true}).click();await expect(page.getByText('Remove saved conditions that are no longer available.',{exact:true})).toBeVisible();await expect(page.getByRole('button',{name:'Start alerts',exact:true})).toBeDisabled();
 await page.getByRole('checkbox',{name:`Remove unavailable condition ${removed}`,exact:true}).click();await expect(page.getByRole('button',{name:'Start alerts',exact:true})).toBeEnabled();
});


test('migrated conditions require an explicit period before Live Alerts can restart',async({page})=>{
 await open(page);await choose(page);await page.getByRole('button',{name:'Save config',exact:true}).click();await expect(page.getByRole('status')).toContainText('Alert configuration saved.');
 await page.evaluate(()=>{const info=(window as any).__testLiveAlertsInfo();info.config.conditionPeriods=info.config.conditionPeriods.map((condition:any)=>({...condition,period:''}));(window as any).__testSetLiveAlertsInfo({config:info.config,error:'Saved Live Alerts used both periods. Choose one period for each saved condition before starting alerts.'});});
 await expect(page.getByRole('combobox',{name:'Alert period for Trend entry',exact:true})).toHaveValue('');
 await expect(page.getByRole('combobox',{name:'Alert period for Momentum entry',exact:true})).toHaveValue('');
 await expect(page.getByText('Choose Daily or Hourly for each saved condition.',{exact:true})).toBeVisible();
 await expect(page.getByRole('button',{name:'Start alerts',exact:true})).toBeDisabled();
 await page.getByRole('combobox',{name:'Alert period for Trend entry',exact:true}).selectOption('d1');
 await expect(page.getByRole('button',{name:'Start alerts',exact:true})).toBeDisabled();
 await page.getByRole('combobox',{name:'Alert period for Momentum entry',exact:true}).selectOption('h1');
 await expect(page.getByRole('button',{name:'Start alerts',exact:true})).toBeEnabled();
});


test('each assigned period requires a selected downloaded asset before alerts can start',async({page})=>{
 await open(page);await choose(page);
 await page.getByRole('checkbox',{name:/Alert asset BTCUSD/}).uncheck();
 await expect(page.getByText('Select at least one downloaded asset for every assigned period.',{exact:true})).toBeVisible();
 await expect(page.getByRole('button',{name:'Start alerts',exact:true})).toBeDisabled();
 await page.getByRole('combobox',{name:'Alert period for Trend entry',exact:true}).selectOption('h1');
 await expect(page.getByRole('button',{name:'Start alerts',exact:true})).toBeEnabled();
 await page.getByRole('combobox',{name:'Alert period for Trend entry',exact:true}).selectOption('d1');
 await expect(page.getByRole('button',{name:'Start alerts',exact:true})).toBeDisabled();
 await page.getByRole('checkbox',{name:/Alert asset BTCUSD/}).check();
 await expect(page.getByRole('button',{name:'Start alerts',exact:true})).toBeEnabled();
});

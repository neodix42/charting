import {test,expect,type Page} from '@playwright/test';
import type {AnalysisFile,AnalysisResult} from '../src/workspace/screenerAnalysis';
import {openTestApp,reloadTestApp} from './helpers/testApp';

const file=(index:number,symbol='btcusd',timeframe='h1'):AnalysisFile=>({id:`profit-file-${index}`,name:`${symbol}-${timeframe}-${index}.csv`,symbol,timeframe,fields:['timestamp','open','high','low','close'],bytes:1024});
const result=(index:number,hits:number,matches=20,symbol='btcusd',timeframe='h1'):AnalysisResult=>({id:`profit-result-${index}`,fileId:`profit-file-${index}`,name:file(index,symbol,timeframe).name,symbol,timeframe,status:'completed',rows:100,matches,firstTime:Date.UTC(2026,9,20),lastTime:Date.UTC(2026,9,21),simulation:{takeProfitHits:hits,stopLossHits:matches-hits,expired:0,insufficientData:0,invalidLevels:0,unavailable:0,profitablePercent:100*hits/matches}});
async function openAnalysis(page:Page,files:AnalysisFile[]){
 await openTestApp(page);await page.evaluate(files=>(window as any).__testSetEnrichmentFiles(files),files);
 await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Analysis',exact:true}).click();
 await expect(page.getByRole('heading',{name:'Visual condition rows',exact:true})).toBeVisible();
}
async function complete(page:Page,results:AnalysisResult[]){
 await page.evaluate(results=>(window as any).__testSetAnalysisInfo({active:false,phase:'completed',total:results.length,completed:results.length,failed:0,currentFile:'',progress:100,rows:results.reduce((n:number,r:any)=>n+r.rows,0),totalMatches:results.reduce((n:number,r:any)=>n+r.matches,0),matchedFiles:results.filter((r:any)=>r.matches).length,results}),results);
 await expect(page.getByRole('button',{name:'Apply conditions',exact:true})).toBeEnabled();
}
async function saveCondition(page:Page,name:string){
 await page.getByRole('button',{name:'Safe Condition',exact:true}).click();await page.getByLabel('Condition name',{exact:true}).fill(name);
 const dialog=page.getByRole('dialog',{name:'Save condition',exact:true});await dialog.getByRole('button',{name:'Save condition',exact:true}).click();await expect(dialog).toHaveCount(0);
}

test('fresh profitability defaults to 85 and filters the sorted full result set before pagination',async({page})=>{
 const percentages=[80,85,90,100,95,85,90,95,100,85,90,95,100,75];
 await openAnalysis(page,percentages.map((_,index)=>file(index)));
 const filter=page.getByRole('combobox',{name:'Analysis profitability filter',exact:true});
 await expect(filter).toHaveValue('85');await expect(filter.locator('option')).toHaveCount(21);
 await expect(page.getByText('Profitability filtering needs Simulate trade to calculate TP hits. Choose 0 to show matching files without a profitability filter.',{exact:true})).toBeVisible();
 await page.getByRole('button',{name:'Simulate trade',exact:true}).click();await page.getByLabel('Stop Loss field',{exact:true}).selectOption('low');await page.getByLabel('Take profit field',{exact:true}).selectOption('field:high');
 await page.getByRole('button',{name:'Apply conditions',exact:true}).click();
 const results=percentages.map((percent,index)=>result(index,percent/5));
 await complete(page,results);
 const names=page.locator('.analysis-results tr.analysis-file-match td:first-child>strong');
 await expect(names).toHaveText([3,8,12,4,7,11,2,6,10,1].map(index=>results[index].name));
 await expect(page.getByRole('navigation',{name:'Analysis file pages',exact:true})).toContainText('Files 1–10 of 12');
 await page.getByRole('button',{name:'Next files',exact:true}).click();await expect(names).toHaveText([5,9].map(index=>results[index].name));
 await filter.selectOption('100');await expect(names).toHaveText([3,8,12].map(index=>results[index].name));
 await expect(page.getByRole('navigation',{name:'Analysis file pages',exact:true})).toContainText('Files 1–3 of 3');
 await filter.selectOption('0');await expect(page.getByRole('navigation',{name:'Analysis file pages',exact:true})).toContainText('Files 1–10 of 14');
 await filter.selectOption('85');await saveCondition(page,'Profitable entries');
 const saved=await page.evaluate(()=>(window as any).__testSavedAnalysisConditions()[0]);
 expect(saved.request.profitabilityFilter).toBe(85);expect(saved.analysis.assets).toHaveLength(1);expect(saved.analysis.assets[0]).toMatchObject({symbol:'btcusd',timeframe:'h1',profitablePercent:100,fileName:results[12].name});
 await expect(page.getByRole('button',{name:'Load condition Profitable entries',exact:true})).toHaveAttribute('aria-pressed','true');
 await filter.selectOption('90');await expect(page.locator('.analysis-loaded-condition')).toContainText('Modified draft');
 await page.getByRole('button',{name:'Load condition Profitable entries',exact:true}).click();await expect(filter).toHaveValue('85');await expect(page.locator('.analysis-loaded-condition')).not.toContainText('Modified draft');
 await reloadTestApp(page);await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Analysis',exact:true}).click();
 await expect(filter).toHaveValue('85');await expect(page.getByRole('button',{name:'Load condition Profitable entries',exact:true})).toHaveAttribute('aria-pressed','true');
});

test('saved selection survives normalized Go fields and rename, while ambiguous restored drafts stay unselected',async({page})=>{
 await openAnalysis(page,[file(0)]);
 await page.evaluate(()=>{
  const api=window.go!.main!.App!,save=api.SaveScreenerAnalysisCondition;
  api.SaveScreenerAnalysisCondition=async(name,value)=>{
   const normalized=structuredClone(value);
   const operand=(value:any)=>({kind:'',value:0,field:'',timeframe:'',offset:0,aggregate:'',window:0,...value});
   normalized.conditions=normalized.conditions.map(row=>({...row,comparison:row.comparison??'',left:operand(row.left),right:operand(row.right),upper:operand(row.upper),distance:operand(row.distance),multiplier:row.multiplier??0,bars:row.bars??0}));
   return save(name,normalized);
  };
 });
 await saveCondition(page,'Normalized shape');
 const saved=page.getByRole('button',{name:'Load condition Normalized shape',exact:true});
 await expect(saved).toHaveAttribute('aria-pressed','true');await expect(page.locator('.analysis-loaded-condition')).not.toContainText('Modified draft');
 await page.getByLabel('Condition 1 right constant',{exact:true}).fill('1');await expect(page.locator('.analysis-loaded-condition')).toContainText('Modified draft');
 await saved.click();await expect(page.locator('.analysis-loaded-condition')).not.toContainText('Modified draft');
 await saved.click({button:'right'});await page.getByRole('menuitem',{name:'Rename',exact:true}).click();
 await page.getByLabel('Condition name',{exact:true}).fill('Renamed selection');await page.getByRole('dialog',{name:'Rename condition',exact:true}).getByRole('button',{name:'Rename',exact:true}).click();
 await expect(page.getByRole('button',{name:'Load condition Renamed selection',exact:true})).toHaveAttribute('aria-pressed','true');await expect(page.locator('.analysis-loaded-condition')).toHaveText('Loaded condition: Renamed selection');
 await saveCondition(page,'Identical second rule');
 await reloadTestApp(page);await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Analysis',exact:true}).click();
 await expect(page.getByRole('button',{name:'Load condition Renamed selection',exact:true})).toHaveAttribute('aria-pressed','false');await expect(page.getByRole('button',{name:'Load condition Identical second rule',exact:true})).toHaveAttribute('aria-pressed','false');
 await page.getByRole('button',{name:'Load condition Renamed selection',exact:true}).click();await expect(page.locator('.analysis-loaded-condition')).toHaveText('Loaded condition: Renamed selection');
});

test('saved highlight waits for the restored Analysis draft instead of selecting the loading placeholder',async({page})=>{
 await openTestApp(page);
 await page.evaluate(async files=>{
  (window as any).__testSetEnrichmentFiles(files);
  const api=window.go!.main!.App!,initial=await api.GetScreenerAnalysisView(),restored={...initial,conditions:initial.conditions.map(row=>({...row,right:{kind:'number',value:2}}))};
  await api.SaveScreenerAnalysisCondition('Initial placeholder',initial);await api.SaveScreenerAnalysisCondition('Restored draft',restored);await api.SaveScreenerAnalysisView(restored);
  const read=api.GetScreenerAnalysisView;
  api.GetScreenerAnalysisView=async()=>{const view=await read();await new Promise<void>(resolve=>{(window as any).__releaseRestoredAnalysisView=resolve;});return view;};
 },[file(0)]);
 await page.getByRole('tab',{name:'Screener',exact:true}).click();
 const initial=page.getByRole('button',{name:'Load condition Initial placeholder',exact:true}),restored=page.getByRole('button',{name:'Load condition Restored draft',exact:true});
 await expect(initial).toBeDisabled();await expect(initial).toHaveAttribute('aria-pressed','false');await expect(restored).toHaveAttribute('aria-pressed','false');
 await page.evaluate(()=>(window as any).__releaseRestoredAnalysisView());
 await expect(page.getByLabel('Condition 1 right constant',{exact:true})).toHaveValue('2');await expect(restored).toHaveAttribute('aria-pressed','true');await expect(initial).toHaveAttribute('aria-pressed','false');
});

test('Live Alerts displays only assigned-period assets with recorded profitability and keeps scopes separate',async({page})=>{
 await openTestApp(page);
 const files=[file(0,'btcusd','h1'),file(1,'btcusd','d1'),file(2,'eurusd','h1'),file(3,'ethusd','d1'),file(4,'ethusd','h1')];
 const results=[result(0,10,11,'btcusd','h1'),result(1,16,20,'btcusd','d1'),result(2,17,20,'eurusd','h1'),result(3,20,20,'ethusd','d1')];
 await page.evaluate(async({files,results})=>{
  (window as any).__testSetEnrichmentFiles(files);
  const api=window.go!.main!.App!,view={...await api.GetScreenerAnalysisView(),tradeSimulation:{stopLoss:'low',takeProfit:'high',duration:20}};
  await api.StartScreenerAnalysis(view);(window as any).__testSetAnalysisInfo({active:false,phase:'completed',failed:0,results});
  await api.SaveScreenerAnalysisCondition('Recorded 85',view);
  await api.SaveScreenerAnalysisCondition('Unfiltered hourly',{...view,profitabilityFilter:0});
  await api.SaveScreenerAnalysisCondition('Needs analysis',{...view,conditions:view.conditions.map(row=>({...row,right:{kind:'number',value:2}}))});
 },{files,results});
 await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Live Alerts',exact:true}).click();
 const assets=page.getByRole('region',{name:'Downloaded assets for alerts',exact:true});
 await page.getByRole('checkbox',{name:'Alert condition Recorded 85',exact:true}).check();
 await expect(assets.getByRole('checkbox',{name:/Alert asset ETHUSD/})).toBeVisible();await expect(assets.getByRole('checkbox',{name:/Alert asset BTCUSD/})).toHaveCount(0);await expect(assets.getByRole('checkbox',{name:/Alert asset EURUSD/})).toHaveCount(0);
 await expect(assets).toContainText('D1 downloaded');await expect(assets).not.toContainText('H1 downloaded');
 await page.getByRole('button',{name:'Select all assets',exact:true}).click();
 await page.getByRole('combobox',{name:'Alert period for Recorded 85',exact:true}).selectOption('h1');
 await expect(assets.getByRole('checkbox',{name:/Alert asset ETHUSD/})).toHaveCount(0);await expect(assets.getByRole('checkbox',{name:/Alert asset BTCUSD/})).not.toBeChecked();await expect(assets.getByRole('checkbox',{name:/Alert asset EURUSD/})).toBeVisible();
 await expect(assets).toContainText('Recorded 85 · H1 · Profitability: 90.91%, 10 / 11 hit TP');await expect(assets).toContainText('Profitability: 85.00%, 17 / 20 hit TP');
 await page.getByRole('button',{name:'Select all assets',exact:true}).click();await expect(page.getByRole('button',{name:'Start alerts',exact:true})).toBeEnabled();
 await page.getByRole('checkbox',{name:'Alert condition Unfiltered hourly',exact:true}).check();await page.getByRole('combobox',{name:'Alert period for Unfiltered hourly',exact:true}).selectOption('h1');
 await expect(assets.getByRole('checkbox',{name:/Alert asset ETHUSD/})).toBeVisible();await expect(assets.getByRole('checkbox',{name:/Alert asset ETHUSD/})).not.toBeChecked();
 await page.getByRole('checkbox',{name:'Alert condition Recorded 85',exact:true}).uncheck();await page.getByRole('checkbox',{name:'Alert condition Unfiltered hourly',exact:true}).uncheck();await page.getByRole('checkbox',{name:'Alert condition Needs analysis',exact:true}).check();
 await expect(assets.getByRole('checkbox',{name:/Alert asset/})).toHaveCount(0);await expect(page.getByText('Run Analysis for “Needs analysis” to record profitable assets before starting alerts.',{exact:true})).toBeVisible();await expect(page.getByRole('button',{name:'Start alerts',exact:true})).toBeDisabled();
 await page.evaluate(()=>(window as any).__testSetLiveAlertsInfo({recent:[{assetName:'Bitcoin vs US Dollar',symbol:'btcusd',timeframe:'h1',time:Date.UTC(2026,9,28,11),conditionName:'Recorded 85',status:'sent',profitability:{symbol:'btcusd',timeframe:'h1',fileName:'recorded.csv',matches:11,takeProfitHits:10,profitablePercent:1000/11}}]}));
 await expect(page.getByRole('region',{name:'Live alert status',exact:true})).toContainText('Profitability: 90.91%, 10 / 11 hit TP');
});

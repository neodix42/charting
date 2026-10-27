import {test,expect,type Page} from '@playwright/test';
import {openTestApp} from './helpers/testApp';
import type {AnalysisChart,AnalysisFile,AnalysisInfo,AnalysisRequest,AnalysisResult} from '../src/workspace/screenerAnalysis';

const fields=['timestamp','open','high','low','close','volume','ema_200','atr_14','macd_12_26','macd_histogram_12_26_9'];
const file=(index:number,symbol='btcusd',timeframe='d1'):AnalysisFile=>({id:`file-${index}`,name:`${symbol}-${timeframe}-${index}.csv`,symbol,timeframe,fields:[...fields],bytes:4096});
const result=(index:number,symbol='btcusd',matches=2,timeframe='d1'):AnalysisResult=>({id:`result-${index}`,fileId:`file-${index}`,name:file(index,symbol,timeframe).name,symbol,timeframe,status:'completed',rows:200,matches,firstTime:Date.UTC(2024,0,5),lastTime:Date.UTC(2024,0,5+matches-1)});
async function open(page:Page,files:AnalysisFile[]=[file(0),file(1,'ethusd','h4')]){
 await openTestApp(page);await page.evaluate(files=>(window as any).__testSetEnrichmentFiles(files),files);
 await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Analysis',exact:true}).click();
 await expect(page.getByRole('heading',{name:'Visual condition rows',exact:true})).toBeVisible();
 await expect(page.getByRole('button',{name:'Apply conditions',exact:true})).toBeEnabled();
}
async function complete(page:Page,results:AnalysisResult[]){
 await page.evaluate(results=>(window as any).__testSetAnalysisInfo({active:false,phase:'completed',total:results.length,completed:results.length,failed:results.filter((r:any)=>r.error).length,currentFile:'',progress:100,rows:results.reduce((n:number,r:any)=>n+r.rows,0),totalMatches:results.reduce((n:number,r:any)=>n+r.matches,0),matchedFiles:results.filter((r:any)=>r.matches).length,results}),results);
 await expect(page.getByRole('heading',{name:'Matching assets',exact:true})).toBeVisible();
}
const control=(page:Page,name:string)=>page.getByLabel(name,{exact:true});
const resources=(page:Page)=>page.evaluate(()=>(window as any).__testAnalysisChartResources());
async function saveCondition(page:Page,name:string){
 await page.getByRole('button',{name:'Safe Condition',exact:true}).click();await control(page,'Condition name').fill(name);
 const dialog=page.getByRole('dialog',{name:'Save condition',exact:true});
 await dialog.getByRole('button',{name:'Save condition',exact:true}).click();await expect(dialog).toHaveCount(0);
}
async function openRename(page:Page,name:string){
 await page.getByRole('button',{name:`Load condition ${name}`,exact:true}).click({button:'right'});
 await page.getByRole('menuitem',{name:'Rename',exact:true}).click();
 return page.getByRole('dialog',{name:'Rename condition',exact:true});
}

test('Analysis loads only when visible and exposes grouped enriched fields plus every comparison',async({page})=>{
 await openTestApp(page);expect(await page.evaluate(()=>(window as any).__testAnalysisReads())).toBe(0);
 await page.evaluate(files=>(window as any).__testSetEnrichmentFiles(files),[file(0),file(1,'ethusd','h4')]);
 await page.getByRole('tab',{name:'Screener',exact:true}).click();
 await expect(page.getByRole('heading',{name:'Visual condition rows'})).toBeVisible();
 await expect(control(page,'Condition 1 left field').locator('optgroup[label="MACD"] option')).toContainText(['MACD (12, 26)','Histogram (12, 26, 9)']);
 await expect(control(page,'Condition 1 operator').locator('option')).toHaveCount(11);
 await expect(control(page,'Condition 1 left timeframe').locator('option')).toHaveText(['Same as file','d1','h4']);
 await expect(page.getByText('All rows must match (AND). Every closed bar in the selected downloaded CSV files is checked.',{exact:true})).toBeVisible();
 await expect.poll(()=>page.evaluate(()=>(window as any).__testLeases())).toBe(0);
});

test('Is equal saves, reloads and submits calendar and temporal comparisons',async({page})=>{
 await open(page,[{...file(0),fields:[...fields,'weekday','hour']}]);
 await control(page,'Condition 1 left field').selectOption('weekday');await control(page,'Condition 1 operator').selectOption('equal');
 await control(page,'Condition 1 right constant').fill('1');
 await page.getByRole('button',{name:'Add condition',exact:false}).click();
 await control(page,'Condition 2 operator').selectOption('occurredWithin');await control(page,'Condition 2 comparison').selectOption('equal');
 await control(page,'Condition 2 left field').selectOption('hour');await control(page,'Condition 2 right constant').fill('0');
 await page.getByRole('button',{name:'Add condition',exact:false}).click();
 await control(page,'Condition 3 operator').selectOption('remainedFor');await control(page,'Condition 3 comparison').selectOption('equal');
 await control(page,'Condition 3 right type').selectOption('field');await control(page,'Condition 3 right field').selectOption('close');
 await control(page,'Condition 3 right bars ago').fill('1');
 await page.getByRole('button',{name:'Safe Condition',exact:true}).click();
 await control(page,'Condition name').fill('Monday equality');
 await page.getByRole('dialog',{name:'Save condition',exact:true}).getByRole('button',{name:'Save condition',exact:true}).click();
 await expect(page.getByRole('dialog',{name:'Save condition',exact:true})).toHaveCount(0);
 await control(page,'Condition 1 operator').selectOption('above');
 await page.getByRole('button',{name:'Load condition Monday equality',exact:true}).click();
 await expect.poll(()=>page.evaluate(()=>(window as any).__testAnalysisView().conditions[0].operator)).toBe('equal');
 await page.reload();await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Analysis',exact:true}).click();
 await expect(control(page,'Condition 1 operator')).toHaveValue('equal');
 await expect(control(page,'Condition 2 comparison')).toHaveValue('equal');await expect(control(page,'Condition 3 comparison')).toHaveValue('equal');
 expect(await page.evaluate(()=>(window as any).__testAnalysisRequest())).toBeNull();
 await page.getByRole('button',{name:'Apply conditions',exact:true}).click();
 const request=await page.evaluate(()=>(window as any).__testAnalysisRequest()) as AnalysisRequest;
 expect(request.conditions).toHaveLength(3);
 expect(request.conditions[0]).toMatchObject({operator:'equal',left:{field:'weekday'},right:{kind:'number',value:1}});
 expect(request.conditions[1]).toMatchObject({operator:'occurredWithin',comparison:'equal',left:{field:'hour'},right:{kind:'number',value:0}});
 expect(request.conditions[2]).toMatchObject({operator:'remainedFor',comparison:'equal',right:{kind:'field',field:'close',offset:1}});
});

test('visual AND rows submit MACD, cross-timeframe candle and ATR distance operands',async({page})=>{
 await open(page);
 await control(page,'Condition 1 left field').selectOption('macd_histogram_12_26_9');
 await page.getByRole('button',{name:'Add condition',exact:false}).click();
 await control(page,'Condition 2 operator').selectOption('crossesAbove');
 await control(page,'Condition 2 right type').selectOption('field');await control(page,'Condition 2 right field').selectOption('ema_200');
 await control(page,'Condition 2 right timeframe').selectOption('d1');await control(page,'Condition 2 right bars ago').fill('1');
 await page.getByRole('button',{name:'Add condition',exact:false}).click();
 await control(page,'Condition 3 left field').selectOption('low');await control(page,'Condition 3 operator').selectOption('withinDistance');
 await control(page,'Condition 3 right type').selectOption('field');await control(page,'Condition 3 right field').selectOption('ema_200');
 await control(page,'Condition 3 distance field').selectOption('atr_14');await control(page,'Condition 3 distance multiplier').fill('0.2');
 await page.getByRole('button',{name:'Apply conditions',exact:true}).click();
 const request=await page.evaluate(()=>(window as any).__testAnalysisRequest()) as AnalysisRequest;
 expect(request.conditions).toHaveLength(3);
 expect(request.conditions[0]).toMatchObject({operator:'above',left:{field:'macd_histogram_12_26_9'},right:{kind:'number',value:0}});
 expect(request.conditions[1]).toMatchObject({operator:'crossesAbove',right:{field:'ema_200',timeframe:'d1',offset:1}});
 expect(request.conditions[2]).toMatchObject({operator:'withinDistance',left:{field:'low'},right:{field:'ema_200'},distance:{field:'atr_14'},multiplier:0.2});
 await expect(control(page,'Condition 1 left field')).toBeDisabled();await page.getByRole('button',{name:'Cancel analysis',exact:true}).click();
 await expect(control(page,'Condition 1 left field')).toBeEnabled();
});

test('temporal/window operators retain both operand sides, allow between bounds and remove rows',async({page})=>{
 await open(page);await control(page,'Condition 1 operator').selectOption('highest');
 await expect(control(page,'Condition 1 right type')).toBeVisible();await control(page,'Condition 1 right type').selectOption('field');await control(page,'Condition 1 right field').selectOption('high');
 await control(page,'Condition 1 bar window').fill('20');
 await page.getByRole('button',{name:'Add condition',exact:false}).click();await control(page,'Condition 2 operator').selectOption('remainedFor');await control(page,'Condition 2 comparison').selectOption('between');
 await control(page,'Condition 2 upper constant').fill('70');await control(page,'Condition 2 right constant').fill('30');await control(page,'Condition 2 bar window').fill('5');
 await expect(page.getByText(/Highest \/ lowest compares the left value/)).toBeVisible();
 await page.getByRole('button',{name:'Apply conditions',exact:true}).click();
 const request=await page.evaluate(()=>(window as any).__testAnalysisRequest()) as AnalysisRequest;
 expect(request.conditions[0]).toMatchObject({operator:'highest',right:{kind:'field',field:'high'},bars:20});
 expect(request.conditions[1]).toMatchObject({operator:'remainedFor',comparison:'between',bars:5,right:{value:30},upper:{value:70}});
 await page.getByRole('button',{name:'Cancel analysis',exact:true}).click();await page.getByRole('button',{name:'Remove condition 2',exact:true}).click();
 await expect(control(page,'Condition 2 operator')).toHaveCount(0);await page.getByRole('button',{name:'Remove condition 1',exact:true}).click();
 await expect(page.getByRole('button',{name:'Apply conditions',exact:true})).toBeDisabled();await expect(page.getByText('Add at least one condition before scanning.',{exact:true})).toBeVisible();
 await page.getByRole('button',{name:'Add condition',exact:false}).click();await expect(control(page,'Condition 1 operator')).toBeVisible();
});

test('advanced operators initialize unused Go zero-value operands and comparisons',async({page})=>{
 for(const operator of ['between','withinDistance','occurredWithin','remainedFor']){
  await openTestApp(page);
  await page.evaluate(files=>{
   (window as any).__testSetEnrichmentFiles(files);
   (window as any).__testSetAnalysisView({conditions:[{id:'condition-1',operator:'above',comparison:'',left:{kind:'field',field:'close',timeframe:'same',offset:0,aggregate:'value',window:1},right:{kind:'number',value:0},upper:{kind:'',field:'',value:0},distance:{kind:'',field:'',value:0},multiplier:1,bars:3}]});
  },[file(0)]);
  await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Analysis',exact:true}).click();
  await expect(control(page,'Condition 1 operator')).toBeEnabled();await control(page,'Condition 1 operator').selectOption(operator);
  if(operator==='between')await expect(control(page,'Condition 1 upper constant')).toHaveValue('100');
  if(operator==='withinDistance')await expect(control(page,'Condition 1 distance field')).toHaveValue('atr_14');
  if(operator==='occurredWithin'||operator==='remainedFor')await expect(control(page,'Condition 1 comparison')).toHaveValue('above');
  await page.getByRole('button',{name:'Apply conditions',exact:true}).click();
  const request=await page.evaluate(()=>(window as any).__testAnalysisRequest()) as AnalysisRequest;
  expect(request.conditions[0]).toMatchObject({operator,comparison:'above',upper:{kind:'number',value:100},distance:{kind:'field',field:'atr_14'}});
 }
});

test('draft edits coalesce durably and restore after reload without being applied',async({page})=>{
 await open(page);await page.evaluate(()=>(window as any).__testHoldAnalysisSaves());
 await control(page,'Condition 1 right constant').fill('10');await control(page,'Condition 1 left field').selectOption('ema_200');
 await control(page,'Condition 1 left timeframe').selectOption('h4');await control(page,'Condition 1 left bars ago').fill('2');
 await control(page,'Condition 1 left aggregation').selectOption('highest');await control(page,'Condition 1 left aggregation window').fill('20');
 expect(await page.evaluate(()=>(window as any).__testAnalysisSaves().length)).toBe(1);
 await page.evaluate(()=>(window as any).__testReleaseAnalysisSaves());
 await expect.poll(()=>page.evaluate(()=>(window as any).__testAnalysisView().conditions[0].left)).toMatchObject({field:'ema_200',timeframe:'h4',offset:2,aggregate:'highest',window:20});
 expect(await page.evaluate(()=>(window as any).__testAnalysisRequest())).toBeNull();
 await page.reload();await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Analysis',exact:true}).click();
 await expect(control(page,'Condition 1 left field')).toHaveValue('ema_200');await expect(control(page,'Condition 1 left timeframe')).toHaveValue('h4');await expect(control(page,'Condition 1 left aggregation window')).toHaveValue('20');
});

test('matching assets show exact counts/errors and chart navigation traverses every occurrence with cleanup',async({page})=>{
 await open(page,[file(0),file(1,'btcusd','h4'),file(2,'ethusd')]);
 await complete(page,[result(0),result(1,'btcusd',3,'h4'),{...result(2,'ethusd',0),status:'failed',error:'Missing field ema_200 in this file'}]);
 await expect(page.locator('.analysis-asset').getByText('Bitcoin vs US Dollar',{exact:true})).toBeVisible();await expect(page.getByRole('alert')).toContainText('Missing field ema_200');
 await page.getByRole('button',{name:`Inspect ${file(0).name} matches`,exact:true}).click();
 const dialog=page.getByRole('dialog',{name:'Analysis match chart',exact:true});await expect(dialog).toBeVisible();
 await expect(dialog.getByText('Occurrence 1 of 2',{exact:true})).toBeVisible();await expect(dialog.getByTestId('analysis-chart').locator('canvas').first()).toBeVisible();
 await expect(dialog.getByText('MACD · Histogram (12, 26, 9)',{exact:false})).toBeVisible();
 await expect.poll(()=>resources(page)).toEqual({charts:1,observers:2,listeners:2});
 await dialog.getByRole('button',{name:'Next match',exact:false}).click();await expect(dialog.getByText('Occurrence 2 of 2',{exact:true})).toBeVisible();
 await dialog.getByRole('button',{name:'Next match',exact:false}).click();await expect(dialog.getByText('Occurrence 1 of 3',{exact:true})).toBeVisible();await expect(dialog.locator('header')).toContainText('h4');
 await dialog.getByRole('button',{name:'Previous match',exact:false}).click();await expect(dialog.getByText('Occurrence 2 of 2',{exact:true})).toBeVisible();
 expect(await page.evaluate(()=>(window as any).__testAnalysisChartRequests())).toEqual(expect.arrayContaining([{resultID:'result-0',occurrence:0,before:120,after:80},{resultID:'result-0',occurrence:1,before:120,after:80},{resultID:'result-1',occurrence:0,before:120,after:80}]));
 await page.keyboard.press('Escape');await expect(dialog).toHaveCount(0);await expect.poll(()=>resources(page)).toEqual({charts:0,observers:0,listeners:0});
 await expect.poll(()=>page.evaluate(()=>(window as any).__testLeases())).toBe(0);
});

test('match navigation crosses paginated zero-match files rather than truncating the occurrence list',async({page})=>{
 await open(page);const results=Array.from({length:51},(_,i)=>result(i,i===50?'ethusd':'btcusd',i===0||i===50?1:0));await complete(page,results);
 await page.getByRole('button',{name:`Inspect ${file(0).name} matches`,exact:true}).click();
 const dialog=page.getByRole('dialog',{name:'Analysis match chart',exact:true});await expect(dialog.getByTestId('analysis-chart')).toBeVisible();
 await dialog.getByRole('button',{name:'Next match',exact:false}).click();await expect(dialog.locator('header h2')).toContainText('ETHUSD');
 await expect.poll(()=>page.evaluate(()=>(window as any).__testAnalysisChartRequests().at(-1))).toMatchObject({resultID:'result-50',occurrence:0});
 await dialog.getByRole('button',{name:'Previous match',exact:false}).click();await expect(dialog.locator('header h2')).toContainText('BTCUSD');
 await dialog.getByRole('button',{name:'Close analysis chart',exact:true}).click();await expect.poll(()=>resources(page)).toEqual({charts:0,observers:0,listeners:0});
});

test('save errors block Apply, retries preserve the draft and hidden Analysis stops polling and chart listeners',async({page})=>{
 await open(page);await page.evaluate(()=>(window as any).__testAnalysisErrors({save:'Cannot save analysis: disk full'}));
 await control(page,'Condition 1 right constant').fill('42');await expect(page.getByRole('alert')).toContainText('disk full');
 await page.getByRole('button',{name:'Apply conditions',exact:true}).click();expect(await page.evaluate(()=>(window as any).__testAnalysisRequest())).toBeNull();
 await page.evaluate(()=>(window as any).__testAnalysisErrors({}));await page.getByRole('button',{name:'Retry saving rules',exact:true}).click();
 await expect(page.getByRole('alert')).toHaveCount(0);await complete(page,[result(0)]);
 await page.getByRole('button',{name:`Inspect ${file(0).name} matches`,exact:true}).click();await expect.poll(()=>resources(page)).toMatchObject({charts:1});
 await page.keyboard.press('Escape');await page.getByRole('tab',{name:'Downloader',exact:true}).click();
 await expect.poll(()=>resources(page)).toEqual({charts:0,observers:0,listeners:0});
 const reads=await page.evaluate(()=>(window as any).__testAnalysisReads());await page.waitForTimeout(1000);expect(await page.evaluate(()=>(window as any).__testAnalysisReads())).toBe(reads);
});

test('closing an in-flight chart ignores its stale response and tick timestamps are displayed honestly',async({page})=>{
 await open(page);await complete(page,[result(0),result(1,'ethusd',1,'tick')]);
 const time=Date.UTC(2024,0,5),tick:AnalysisChart={resultId:'result-1',name:file(1,'ethusd','tick').name,symbol:'ethusd',timeframe:'tick',occurrence:0,totalMatches:1,matchTime:time,matchIndex:1,startIndex:0,fields:['bidPrice','askPrice','macd_histogram_12_26_9','d1:ema_200[offset 1]'],bars:[{index:0,time,values:{bidPrice:10,askPrice:11,macd_histogram_12_26_9:0.1,'d1:ema_200[offset 1]':9}},{index:1,time,values:{bidPrice:11,askPrice:12,macd_histogram_12_26_9:0.2,'d1:ema_200[offset 1]':9}},{index:2,time:time+1000,values:{bidPrice:12,askPrice:13,macd_histogram_12_26_9:0.3,'d1:ema_200[offset 1]':9}}]};
 await page.evaluate(value=>{(window as any).__testSetAnalysisCharts({'result-1:0':value});(window as any).__testAnalysisChartDelay(1200);},tick);
 await page.getByRole('button',{name:`Inspect ${file(0).name} matches`,exact:true}).click();await page.getByRole('button',{name:'Close analysis chart',exact:true}).click();
 await page.evaluate(()=>(window as any).__testAnalysisChartDelay(0));await page.getByRole('button',{name:`Inspect ${file(1,'ethusd','tick').name} matches`,exact:true}).click();
 const dialog=page.getByRole('dialog',{name:'Analysis match chart',exact:true});await expect(dialog.locator('header h2')).toContainText('ETHUSD');
 await expect(dialog.getByText(/Ticks sharing an identical timestamp/)).toBeVisible();await expect(dialog.getByText('EMA (200) · d1 [offset 1]',{exact:false})).toBeVisible();
 await page.waitForTimeout(1300);await expect(dialog.locator('header h2')).toContainText('ETHUSD');await expect.poll(()=>resources(page)).toEqual({charts:1,observers:2,listeners:2});
 await dialog.getByRole('button',{name:'Close analysis chart',exact:true}).click();await expect.poll(()=>resources(page)).toEqual({charts:0,observers:0,listeners:0});
});

test('Safe Condition stores every row, loads from the right sidebar without applying, and survives reload',async({page})=>{
 await open(page);
 await control(page,'Condition 1 left field').selectOption('ema_200');
 await control(page,'Condition 1 left timeframe').selectOption('h4');await control(page,'Condition 1 left bars ago').fill('2');
 await control(page,'Condition 1 left aggregation').selectOption('highest');await control(page,'Condition 1 left aggregation window').fill('20');
 await control(page,'Condition 1 operator').selectOption('remainedFor');await control(page,'Condition 1 comparison').selectOption('between');
 await control(page,'Condition 1 right constant').fill('30');await control(page,'Condition 1 upper constant').fill('70');await control(page,'Condition 1 bar window').fill('5');
 await page.getByRole('button',{name:'Add condition',exact:false}).click();await control(page,'Condition 2 left field').selectOption('macd_histogram_12_26_9');await control(page,'Condition 2 operator').selectOption('crossesAbove');
 await page.getByRole('button',{name:'Safe Condition',exact:true}).click();
 const dialog=page.getByRole('dialog',{name:'Save condition',exact:true});await expect(dialog).toBeVisible();
 await expect(control(page,'Condition name')).toBeFocused();await control(page,'Condition name').fill('  Momentum setup  ');
 await dialog.getByRole('button',{name:'Save condition',exact:true}).click();await expect(dialog).toHaveCount(0);
 const sidebar=page.getByRole('complementary',{name:'Saved conditions',exact:true});
 const saved=await page.evaluate(()=>(window as any).__testSavedAnalysisConditions());
 expect(saved).toHaveLength(1);expect(saved[0].name).toBe('Momentum setup');
 expect(saved[0].request.conditions).toEqual([
  expect.objectContaining({operator:'remainedFor',comparison:'between',bars:5,left:expect.objectContaining({field:'ema_200',timeframe:'h4',offset:2,aggregate:'highest',window:20}),right:expect.objectContaining({value:30}),upper:expect.objectContaining({value:70})}),
  expect.objectContaining({operator:'crossesAbove',left:expect.objectContaining({field:'macd_histogram_12_26_9'})}),
 ]);
 await expect(sidebar.getByRole('button',{name:'Load condition Momentum setup',exact:true})).toContainText('2 rows');
 const layout=await sidebar.evaluate(element=>{const main=document.querySelector('.analysis-builder')!.getBoundingClientRect(),aside=element.getBoundingClientRect();return {mainRight:main.right,asideLeft:aside.left,asideRight:aside.right,viewport:innerWidth};});
 expect(layout.asideLeft).toBeGreaterThan(layout.mainRight);expect(layout.asideRight).toBeLessThanOrEqual(layout.viewport);
 await page.getByRole('button',{name:'Remove condition 2',exact:true}).click();await control(page,'Condition 1 operator').selectOption('below');
 await sidebar.getByRole('button',{name:'Load condition Momentum setup',exact:true}).click();
 await expect(control(page,'Condition 2 operator')).toHaveValue('crossesAbove');await expect(control(page,'Condition 1 comparison')).toHaveValue('between');
 await expect.poll(()=>page.evaluate(()=>(window as any).__testAnalysisView())).toEqual(saved[0].request);
 expect(await page.evaluate(()=>(window as any).__testAnalysisRequest())).toBeNull();
 await page.getByRole('button',{name:'Remove condition 2',exact:true}).click();await control(page,'Condition 1 operator').selectOption('above');
 await expect.poll(()=>page.evaluate(()=>(window as any).__testAnalysisView().conditions.length)).toBe(1);
 await page.reload();await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Analysis',exact:true}).click();
 await page.getByRole('button',{name:'Load condition Momentum setup',exact:true}).click();
 await expect(control(page,'Condition 1 left bars ago')).toHaveValue('2');await expect(control(page,'Condition 2 operator')).toHaveValue('crossesAbove');
 expect(await page.evaluate(()=>(window as any).__testAnalysisRequest())).toBeNull();
 await page.getByRole('button',{name:'Apply conditions',exact:true}).click();
 expect(await page.evaluate(()=>(window as any).__testAnalysisRequest())).toEqual(saved[0].request);
});

test('saved-condition names and failures preserve previous entries and the current draft',async({page})=>{
 await open(page);const sidebar=page.getByRole('complementary',{name:'Saved conditions',exact:true});
 await page.getByRole('button',{name:'Safe Condition',exact:true}).click();const dialog=page.getByRole('dialog',{name:'Save condition',exact:true});
 await control(page,'Condition name').fill('   ');await expect(dialog.getByRole('button',{name:'Save condition',exact:true})).toBeDisabled();
 await control(page,'Condition name').fill('First');await dialog.getByRole('button',{name:'Save condition',exact:true}).click();
 await page.getByRole('button',{name:'Safe Condition',exact:true}).click();await control(page,'Condition name').fill('first');await dialog.getByRole('button',{name:'Save condition',exact:true}).click();
 await expect(dialog.getByRole('alert')).toContainText('already exists');expect(await page.evaluate(()=>(window as any).__testSavedAnalysisConditions().length)).toBe(1);
 await page.evaluate(()=>(window as any).__testAnalysisErrors({savedWrite:'Cannot save named condition: disk full'}));
 await control(page,'Condition name').fill('Second');await dialog.getByRole('button',{name:'Save condition',exact:true}).click();await expect(dialog.getByRole('alert')).toContainText('disk full');
 await page.evaluate(()=>(window as any).__testAnalysisErrors({}));await dialog.getByRole('button',{name:'Save condition',exact:true}).click();await expect(dialog).toHaveCount(0);
 await expect(sidebar.getByRole('button',{name:'Load condition First',exact:true})).toBeVisible();await expect(sidebar.getByRole('button',{name:'Load condition Second',exact:true})).toBeVisible();
 await page.getByRole('button',{name:'Safe Condition',exact:true}).click();await control(page,'Condition name').fill('Cancelled');await page.keyboard.press('Escape');await expect(dialog).toHaveCount(0);
 await expect(page.getByRole('button',{name:'Safe Condition',exact:true})).toBeFocused();expect(await page.evaluate(()=>(window as any).__testSavedAnalysisConditions().length)).toBe(2);
 expect(await page.evaluate(()=>(window as any).__testAnalysisRequest())).toBeNull();
});

test('saved-condition list errors can retry while ordinary Analysis remains available at 1100px',async({page})=>{
 await page.setViewportSize({width:1100,height:900});await openTestApp(page);
 await page.evaluate(files=>{(window as any).__testSetEnrichmentFiles(files);(window as any).__testAnalysisErrors({savedList:'Saved conditions use an unsupported version; original state preserved'});},[file(0)]);
 await page.getByRole('tab',{name:'Screener',exact:true}).click();
 const sidebar=page.getByRole('complementary',{name:'Saved conditions',exact:true});
 await expect(sidebar.getByRole('alert')).toContainText('original state preserved');await expect(page.getByRole('button',{name:'Safe Condition',exact:true})).toBeDisabled();
 await expect(page.getByRole('button',{name:'Apply conditions',exact:true})).toBeEnabled();
 await page.evaluate(()=>(window as any).__testAnalysisErrors({}));await sidebar.getByRole('button',{name:'Retry saved conditions',exact:true}).click();
 await expect(page.getByRole('button',{name:'Safe Condition',exact:true})).toBeEnabled();
 const geometry=await sidebar.evaluate(element=>({left:element.getBoundingClientRect().left,right:element.getBoundingClientRect().right,mainRight:document.querySelector('.analysis-builder')!.getBoundingClientRect().right,width:innerWidth}));
 expect(geometry.left).toBeGreaterThan(geometry.mainRight);expect(geometry.right).toBeLessThanOrEqual(geometry.width);
 await page.getByRole('button',{name:'Apply conditions',exact:true}).click();await expect(sidebar.getByRole('button',{name:'Safe Condition',exact:true})).toBeDisabled();
});

test('normal close waits for an in-flight named condition save',async({page})=>{
 await open(page);
 await page.evaluate(()=>{
  const api=window.go!.main!.App!,original=api.SaveScreenerAnalysisCondition;
  api.SaveScreenerAnalysisCondition=(name,request)=>new Promise(resolve=>{(window as any).__releaseNamedSave=async()=>resolve(await original(name,request));});
 });
 await page.getByRole('button',{name:'Safe Condition',exact:true}).click();await control(page,'Condition name').fill('Pending close');
 await page.getByRole('dialog',{name:'Save condition',exact:true}).getByRole('button',{name:'Save condition',exact:true}).click();
 await expect.poll(()=>page.evaluate(()=>typeof(window as any).__releaseNamedSave)).toBe('function');
 await page.evaluate(()=>(window as any).__testEmit('prepare-close',{token:'named-condition-save'}));
 expect(await page.evaluate(()=>(window as any).__testCloseConfirmation())).toBeNull();
 await expect(page.getByRole('button',{name:'Saving…',exact:true})).toBeDisabled();
 await page.evaluate(()=>(window as any).__releaseNamedSave());
 await expect.poll(()=>page.evaluate(()=>(window as any).__testCloseConfirmation())).toEqual({token:'named-condition-save',discard:false});
 expect((await page.evaluate(()=>(window as any).__testSavedAnalysisConditions())).map((item:any)=>item.name)).toEqual(['Pending close']);
});

test('a delayed saved list cannot erase a newly saved condition',async({page})=>{
 await open(page);
 await page.evaluate(()=>{
  const api=window.go!.main!.App!,original=api.GetScreenerSavedConditions;
  api.GetScreenerSavedConditions=async()=>{const before=await original();return new Promise(resolve=>(window as any).__releaseSavedList=()=>resolve(before));};
 });
 await page.getByRole('tab',{name:'Downloader',exact:true}).click();await page.getByRole('tab',{name:'Analysis',exact:true}).click();
 await expect.poll(()=>page.evaluate(()=>typeof(window as any).__releaseSavedList)).toBe('function');
 await page.getByRole('button',{name:'Safe Condition',exact:true}).click();await control(page,'Condition name').fill('Keep this');
 await page.getByRole('dialog',{name:'Save condition',exact:true}).getByRole('button',{name:'Save condition',exact:true}).click();
 await expect(page.getByRole('button',{name:'Load condition Keep this',exact:true})).toBeVisible();
 await page.evaluate(()=>(window as any).__releaseSavedList());await page.waitForTimeout(100);
 await expect(page.getByRole('button',{name:'Load condition Keep this',exact:true})).toBeVisible();
});

test('right-click Rename persists only the name, retaining identity, order, every saved row and category, and the current draft',async({page})=>{
 await open(page,[file(0),file(1,'eurusd')]);
 const scope=page.getByRole('group',{name:'Asset categories to scan',exact:true});
 await scope.getByRole('button',{name:'Deselect all scan categories',exact:true}).click();
 await scope.getByRole('checkbox',{name:'Scan Forex',exact:true}).check();
 await control(page,'Condition 1 left field').selectOption('macd_histogram_12_26_9');
 await page.getByRole('button',{name:'Add condition',exact:false}).click();
 await control(page,'Condition 2 operator').selectOption('crossesAbove');
 await control(page,'Condition 2 right type').selectOption('field');await control(page,'Condition 2 right field').selectOption('ema_200');
 await control(page,'Condition 2 right timeframe').selectOption('d1');await control(page,'Condition 2 right bars ago').fill('1');
 await saveCondition(page,'Original');await saveCondition(page,'Keep second');
 const before=await page.evaluate(()=>(window as any).__testSavedAnalysisConditions());
 await control(page,'Condition 1 right constant').fill('99');await scope.getByRole('button',{name:'Select all scan categories',exact:true}).click();
 await expect.poll(()=>page.evaluate(()=>(window as any).__testAnalysisView().conditions[0].right.value)).toBe(99);
 await expect.poll(()=>page.evaluate(()=>(window as any).__testAnalysisView().categories)).toBeNull();
 const draft=await page.evaluate(()=>(window as any).__testAnalysisView());
 const dialog=await openRename(page,'Original');
 await expect(control(page,'Condition name')).toHaveValue('Original');await expect(control(page,'Condition name')).toBeFocused();
 expect(await control(page,'Condition name').evaluate((input:HTMLInputElement)=>[input.selectionStart,input.selectionEnd])).toEqual([0,'Original'.length]);
 await control(page,'Condition name').fill('  Renamed setup  ');await dialog.getByRole('button',{name:'Rename',exact:true}).click();
 await expect(dialog).toHaveCount(0);
 const renamed=page.getByRole('button',{name:'Load condition Renamed setup',exact:true});await expect(renamed).toBeFocused();
 await expect(page.locator('.analysis-saved-list strong')).toHaveText(['Renamed setup','Keep second']);
 expect(await page.evaluate(()=>(window as any).__testSavedAnalysisConditions())).toEqual([{...before[0],name:'Renamed setup'},before[1]]);
 expect(await page.evaluate(()=>(window as any).__testAnalysisView())).toEqual(draft);
 expect(await page.evaluate(()=>(window as any).__testAnalysisRequest())).toBeNull();
 await page.reload();await page.getByRole('tab',{name:'Screener',exact:true}).click();
 await expect(page.locator('.analysis-saved-list strong')).toHaveText(['Renamed setup','Keep second']);
 await expect(control(page,'Condition 1 right constant')).toHaveValue('99');
 await renamed.click();await expect.poll(()=>page.evaluate(()=>(window as any).__testAnalysisView())).toEqual(before[0].request);
 await page.getByRole('button',{name:'Apply conditions',exact:true}).click();
 expect(await page.evaluate(()=>(window as any).__testAnalysisRequest())).toEqual(before[0].request);
});

test('saved-condition Rename supports menu navigation, selected text, a trapped dialog and cancellation without mutations',async({page})=>{
 await open(page);await saveCondition(page,'Original');
 const before=await page.evaluate(()=>(window as any).__testSavedAnalysisConditions());
 const saved=page.getByRole('button',{name:'Load condition Original',exact:true}),menu=page.getByRole('menu',{name:'Saved condition Original',exact:true});
 await saved.focus();await page.keyboard.press('Shift+F10');
 const rename=menu.getByRole('menuitem',{name:'Rename',exact:true}),remove=menu.getByRole('menuitem',{name:'Delete',exact:true});
 await expect(rename).toBeFocused();await page.keyboard.press('ArrowDown');await expect(remove).toBeFocused();
 await page.keyboard.press('ArrowDown');await expect(rename).toBeFocused();await page.keyboard.press('ArrowUp');await expect(remove).toBeFocused();
 await page.keyboard.press('Home');await expect(rename).toBeFocused();await page.keyboard.press('End');await expect(remove).toBeFocused();
 await page.keyboard.press('ArrowUp');await page.keyboard.press('Enter');
 const dialog=page.getByRole('dialog',{name:'Rename condition',exact:true});await expect(dialog).toBeVisible();await expect(menu).toHaveCount(0);
 await expect(control(page,'Condition name')).toBeFocused();await page.keyboard.type('Cancelled');await expect(control(page,'Condition name')).toHaveValue('Cancelled');
 await page.keyboard.press('Tab');await expect(dialog.getByRole('button',{name:'Cancel',exact:true})).toBeFocused();
 await page.keyboard.press('Tab');await expect(dialog.getByRole('button',{name:'Rename',exact:true})).toBeFocused();
 await page.keyboard.press('Tab');await expect(dialog.getByRole('button',{name:'Close rename condition',exact:true})).toBeFocused();
 await page.keyboard.press('Shift+Tab');await expect(dialog.getByRole('button',{name:'Rename',exact:true})).toBeFocused();
 await page.keyboard.press('Escape');await expect(dialog).toHaveCount(0);await expect(saved).toBeFocused();
 await openRename(page,'Original');await control(page,'Condition name').fill('Also cancelled');await dialog.getByRole('button',{name:'Cancel',exact:true}).click();
 await expect(saved).toBeFocused();expect(await page.evaluate(()=>(window as any).__testSavedAnalysisConditions())).toEqual(before);
 expect(await page.evaluate(()=>(window as any).__testAnalysisRequest())).toBeNull();
});

test('Rename duplicate, invalid-name and storage errors stay inline, preserve entries and draft, and allow retry',async({page})=>{
 await open(page);await saveCondition(page,'Original');await saveCondition(page,'Other');await control(page,'Condition 1 right constant').fill('42');
 await expect.poll(()=>page.evaluate(()=>(window as any).__testAnalysisView().conditions[0].right.value)).toBe(42);
 const before=await page.evaluate(()=>(window as any).__testSavedAnalysisConditions()),draft=await page.evaluate(()=>(window as any).__testAnalysisView());
 const dialog=await openRename(page,'Original'),submit=dialog.getByRole('button',{name:'Rename',exact:true});
 await control(page,'Condition name').fill('   ');await expect(submit).toBeDisabled();
 await control(page,'Condition name').fill('  other  ');await submit.click();await expect(dialog.getByRole('alert')).toContainText('already exists');
 expect(await page.evaluate(()=>(window as any).__testSavedAnalysisConditions())).toEqual(before);
 await control(page,'Condition name').fill('Invalid\u007f');await submit.click();await expect(dialog.getByRole('alert')).toContainText('control characters');
 await expect(control(page,'Condition name')).toHaveAttribute('aria-invalid','true');
 expect(await page.evaluate(()=>(window as any).__testSavedAnalysisConditions())).toEqual(before);
 await page.evaluate(()=>(window as any).__testAnalysisErrors({savedRename:'Cannot rename named condition: disk full'}));
 await control(page,'Condition name').fill('Retry');await submit.click();await expect(dialog.getByRole('alert')).toContainText('disk full');
 expect(await page.evaluate(()=>(window as any).__testSavedAnalysisConditions())).toEqual(before);
 expect(await page.evaluate(()=>(window as any).__testAnalysisView())).toEqual(draft);
 await page.evaluate(()=>(window as any).__testAnalysisErrors({}));await control(page,'Condition name').fill('ORIGINAL');await submit.click();
 await expect(dialog).toHaveCount(0);await expect(page.getByRole('button',{name:'Load condition ORIGINAL',exact:true})).toBeVisible();
 expect(await page.evaluate(()=>(window as any).__testSavedAnalysisConditions())).toEqual([{...before[0],name:'ORIGINAL'},before[1]]);
 expect(await page.evaluate(()=>(window as any).__testAnalysisView())).toEqual(draft);
});

test('a delayed saved list cannot undo a renamed condition or reorder the list',async({page})=>{
 await open(page);await saveCondition(page,'Original');await saveCondition(page,'Keep second');
 await page.evaluate(()=>{
  const api=window.go!.main!.App!,original=api.GetScreenerSavedConditions;
  api.GetScreenerSavedConditions=async()=>{const before=await original();return new Promise(resolve=>(window as any).__releaseSavedList=()=>resolve(before));};
 });
 await page.getByRole('tab',{name:'Downloader',exact:true}).click();await page.getByRole('tab',{name:'Analysis',exact:true}).click();
 await expect.poll(()=>page.evaluate(()=>typeof(window as any).__releaseSavedList)).toBe('function');
 const dialog=await openRename(page,'Original');await control(page,'Condition name').fill('Renamed');await dialog.getByRole('button',{name:'Rename',exact:true}).click();
 await expect(page.locator('.analysis-saved-list strong')).toHaveText(['Renamed','Keep second']);
 await page.evaluate(()=>(window as any).__releaseSavedList());await page.waitForTimeout(100);
 await expect(page.locator('.analysis-saved-list strong')).toHaveText(['Renamed','Keep second']);
});

test('normal close waits for an in-flight saved-condition rename and retains its identity',async({page})=>{
 await open(page);await saveCondition(page,'Original');
 const before=await page.evaluate(()=>(window as any).__testSavedAnalysisConditions());
 await page.evaluate(()=>{
  const api=window.go!.main!.App!,original=api.RenameScreenerAnalysisCondition;
  api.RenameScreenerAnalysisCondition=(id,name)=>new Promise(resolve=>{(window as any).__releaseNamedRename=async()=>resolve(await original(id,name));});
 });
 const dialog=await openRename(page,'Original');await control(page,'Condition name').fill('Pending rename');await dialog.getByRole('button',{name:'Rename',exact:true}).click();
 await expect.poll(()=>page.evaluate(()=>typeof(window as any).__releaseNamedRename)).toBe('function');
 await expect(dialog.getByRole('button',{name:'Renaming…',exact:true})).toBeDisabled();await expect(dialog.getByRole('button',{name:'Cancel',exact:true})).toBeDisabled();
 await page.keyboard.press('Tab');await expect(dialog).toBeFocused();await page.keyboard.press('Shift+Tab');await expect(dialog).toBeFocused();
 await page.keyboard.press('Escape');await expect(dialog).toBeVisible();
 await page.evaluate(()=>(window as any).__testEmit('prepare-close',{token:'named-condition-rename'}));
 expect(await page.evaluate(()=>(window as any).__testCloseConfirmation())).toBeNull();
 await page.evaluate(()=>(window as any).__releaseNamedRename());
 await expect.poll(()=>page.evaluate(()=>(window as any).__testCloseConfirmation())).toEqual({token:'named-condition-rename',discard:false});
 expect(await page.evaluate(()=>(window as any).__testSavedAnalysisConditions())).toEqual([{...before[0],name:'Pending rename'}]);
});

test('a failed pending rename pauses normal close and leaves the original condition intact',async({page})=>{
 await open(page);await saveCondition(page,'Original');
 const before=await page.evaluate(()=>(window as any).__testSavedAnalysisConditions());
 await page.evaluate(()=>{
  window.go!.main!.App!.RenameScreenerAnalysisCondition=()=>new Promise((_resolve,reject)=>{(window as any).__rejectNamedRename=()=>reject(Error('Cannot rename named condition: disk full'));});
 });
 const dialog=await openRename(page,'Original');await control(page,'Condition name').fill('Unsaved rename');await dialog.getByRole('button',{name:'Rename',exact:true}).click();
 await expect.poll(()=>page.evaluate(()=>typeof(window as any).__rejectNamedRename)).toBe('function');
 await page.evaluate(()=>(window as any).__testEmit('prepare-close',{token:'failed-condition-rename'}));
 await expect(page.getByRole('button',{name:'Close without saving',exact:true})).toHaveCount(0);
 await page.evaluate(()=>(window as any).__rejectNamedRename());
 await expect(dialog.getByRole('alert')).toContainText('disk full');
 await expect(page.getByText('Close paused: Cannot rename named condition: disk full',{exact:true})).toBeVisible();
 expect(await page.evaluate(()=>(window as any).__testCloseConfirmation())).toBeNull();
 expect(await page.evaluate(()=>(window as any).__testSavedAnalysisConditions())).toEqual(before);
 await expect(dialog.getByRole('button',{name:'Rename',exact:true})).toBeEnabled();
});

test('right-click Delete removes only the selected saved condition durably and preserves the draft',async({page})=>{
 await open(page);await saveCondition(page,'First');
 await control(page,'Condition 1 right constant').fill('42');await saveCondition(page,'Second');
 await control(page,'Condition 1 right constant').fill('99');
 await expect.poll(()=>page.evaluate(()=>(window as any).__testAnalysisView().conditions[0].right.value)).toBe(99);
 const draft=await page.evaluate(()=>(window as any).__testAnalysisView());
 const first=page.getByRole('button',{name:'Load condition First',exact:true}),second=page.getByRole('button',{name:'Load condition Second',exact:true});
 await first.click({button:'right'});
 const menu=page.getByRole('menu',{name:'Saved condition First',exact:true});await expect(menu).toBeVisible();
 await expect(control(page,'Condition 1 right constant')).toHaveValue('99');
 expect(await page.evaluate(()=>(window as any).__testAnalysisView())).toEqual(draft);
 await menu.getByRole('menuitem',{name:'Delete',exact:true}).click();
 await expect(first).toHaveCount(0);await expect(second).toBeVisible();
 expect((await page.evaluate(()=>(window as any).__testSavedAnalysisConditions())).map((item:any)=>item.name)).toEqual(['Second']);
 expect(await page.evaluate(()=>(window as any).__testAnalysisView())).toEqual(draft);
 expect(await page.evaluate(()=>(window as any).__testAnalysisRequest())).toBeNull();
 await page.reload();await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Analysis',exact:true}).click();
 await expect(first).toHaveCount(0);await expect(second).toBeVisible();await expect(control(page,'Condition 1 right constant')).toHaveValue('99');
 await second.click({button:'right'});await page.getByRole('menuitem',{name:'Delete',exact:true}).click();
 await expect(second).toHaveCount(0);await expect(page.getByText('No saved conditions yet.',{exact:true})).toBeVisible();
 await page.reload();await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Analysis',exact:true}).click();
 await expect(page.getByText('No saved conditions yet.',{exact:true})).toBeVisible();await expect(control(page,'Condition 1 right constant')).toHaveValue('99');
 expect(await page.evaluate(()=>(window as any).__testSavedAnalysisConditions())).toEqual([]);
});

test('saved-condition Delete menu supports keyboard and dismissal, and a failed delete preserves the condition',async({page})=>{
 await open(page);await saveCondition(page,'Protected');await control(page,'Condition 1 right constant').fill('42');
 await expect.poll(()=>page.evaluate(()=>(window as any).__testAnalysisView().conditions[0].right.value)).toBe(42);
 const draft=await page.evaluate(()=>(window as any).__testAnalysisView());
 const saved=page.getByRole('button',{name:'Load condition Protected',exact:true}),menu=page.getByRole('menu',{name:'Saved condition Protected',exact:true});
 await saved.focus();await page.keyboard.press('Shift+F10');await expect(menu).toBeVisible();
 await expect(menu.getByRole('menuitem',{name:'Rename',exact:true})).toBeFocused();
 await page.keyboard.press('ArrowDown');await expect(menu.getByRole('menuitem',{name:'Delete',exact:true})).toBeFocused();
 await page.keyboard.press('Escape');await expect(menu).toHaveCount(0);await expect(saved).toBeFocused();
 await saved.click({button:'right'});await page.getByRole('heading',{name:'Visual condition rows',exact:true}).click();await expect(menu).toHaveCount(0);
 await saved.click({button:'right'});await page.getByRole('tab',{name:'Downloader',exact:true}).click();await expect(menu).toHaveCount(0);
 await page.getByRole('tab',{name:'Analysis',exact:true}).click();await expect(menu).toHaveCount(0);
 await page.evaluate(()=>(window as any).__testAnalysisErrors({savedDelete:'Cannot delete named condition: disk full'}));
 await saved.click({button:'right'});await menu.getByRole('menuitem',{name:'Delete',exact:true}).click();
 await expect(page.getByRole('complementary',{name:'Saved conditions',exact:true}).getByRole('alert')).toContainText('disk full');
 await expect(saved).toBeVisible();await expect(saved).toBeEnabled();await expect(control(page,'Condition 1 right constant')).toHaveValue('42');
 expect((await page.evaluate(()=>(window as any).__testSavedAnalysisConditions())).map((item:any)=>item.name)).toEqual(['Protected']);
 expect(await page.evaluate(()=>(window as any).__testAnalysisView())).toEqual(draft);
 expect(await page.evaluate(()=>(window as any).__testAnalysisRequest())).toBeNull();
});

test('a delayed saved list cannot resurrect a deleted condition',async({page})=>{
 await open(page);await saveCondition(page,'Delete me');await saveCondition(page,'Keep me');
 await page.evaluate(()=>{
  const api=window.go!.main!.App!,original=api.GetScreenerSavedConditions;
  api.GetScreenerSavedConditions=async()=>{const before=await original();return new Promise(resolve=>(window as any).__releaseSavedList=()=>resolve(before));};
 });
 await page.getByRole('tab',{name:'Downloader',exact:true}).click();await page.getByRole('tab',{name:'Analysis',exact:true}).click();
 await expect.poll(()=>page.evaluate(()=>typeof(window as any).__releaseSavedList)).toBe('function');
 const removed=page.getByRole('button',{name:'Load condition Delete me',exact:true}),kept=page.getByRole('button',{name:'Load condition Keep me',exact:true});
 await removed.click({button:'right'});await page.getByRole('menuitem',{name:'Delete',exact:true}).click();
 await expect(removed).toHaveCount(0);await expect(kept).toBeVisible();
 await page.evaluate(()=>(window as any).__releaseSavedList());await page.waitForTimeout(100);
 await expect(removed).toHaveCount(0);await expect(kept).toBeVisible();
 expect((await page.evaluate(()=>(window as any).__testSavedAnalysisConditions())).map((item:any)=>item.name)).toEqual(['Keep me']);
});

test('normal close waits for an in-flight saved-condition delete',async({page})=>{
 await open(page);await saveCondition(page,'Pending deletion');
 await page.evaluate(()=>{
  const api=window.go!.main!.App!,original=api.DeleteScreenerAnalysisCondition;
  api.DeleteScreenerAnalysisCondition=id=>new Promise(resolve=>{(window as any).__releaseNamedDelete=async()=>resolve(await original(id));});
 });
 const saved=page.getByRole('button',{name:'Load condition Pending deletion',exact:true});
 await saved.click({button:'right'});await page.getByRole('menuitem',{name:'Delete',exact:true}).click();
 await expect.poll(()=>page.evaluate(()=>typeof(window as any).__releaseNamedDelete)).toBe('function');await expect(saved).toBeDisabled();
 await page.evaluate(()=>(window as any).__testEmit('prepare-close',{token:'named-condition-delete'}));
 expect(await page.evaluate(()=>(window as any).__testCloseConfirmation())).toBeNull();
 await page.evaluate(()=>(window as any).__releaseNamedDelete());
 await expect.poll(()=>page.evaluate(()=>(window as any).__testCloseConfirmation())).toEqual({token:'named-condition-delete',discard:false});
 expect(await page.evaluate(()=>(window as any).__testSavedAnalysisConditions())).toEqual([]);
});

test('scan categories default to all, survive row edits and reload, and are included in saved conditions',async({page})=>{
 await open(page,[file(0),file(1,'eurusd'),file(2,'xauusd')]);
 const scope=page.getByRole('group',{name:'Asset categories to scan',exact:true});
 await expect(scope.getByRole('checkbox')).toHaveCount(3);
 for(const category of ['Forex','Cryptocurrencies','Commodities'])await expect(scope.getByRole('checkbox',{name:`Scan ${category}`,exact:true})).toBeChecked();
 await scope.getByRole('button',{name:'Deselect all scan categories',exact:true}).click();
 await expect(page.getByRole('button',{name:'Apply conditions',exact:true})).toBeDisabled();
 await expect(page.getByText('Select at least one asset category before scanning.',{exact:true})).toBeVisible();
 await scope.getByRole('checkbox',{name:'Scan Forex',exact:true}).check();
 await control(page,'Condition 1 right constant').fill('12');
 await page.getByRole('button',{name:'Add condition',exact:false}).click();
 await expect.poll(()=>page.evaluate(()=>(window as any).__testAnalysisView().categories)).toEqual(['Forex']);
 await page.getByRole('button',{name:'Safe Condition',exact:true}).click();
 await control(page,'Condition name').fill('Forex only');
 await page.getByRole('dialog',{name:'Save condition',exact:true}).getByRole('button',{name:'Save condition',exact:true}).click();
 await expect.poll(()=>page.evaluate(()=>(window as any).__testSavedAnalysisConditions()[0]?.request.categories)).toEqual(['Forex']);
 await page.reload();await page.getByRole('tab',{name:'Screener',exact:true}).click();
 await expect(scope.getByRole('checkbox',{name:'Scan Forex',exact:true})).toBeChecked();
 await expect(scope.getByRole('checkbox',{name:'Scan Cryptocurrencies',exact:true})).not.toBeChecked();
 await scope.getByRole('button',{name:'Select all scan categories',exact:true}).click();
 await expect.poll(()=>page.evaluate(()=>(window as any).__testAnalysisView().categories)).toBeNull();
 await page.getByRole('button',{name:'Load condition Forex only',exact:true}).click();
 await expect(scope.getByRole('checkbox',{name:'Scan Cryptocurrencies',exact:true})).not.toBeChecked();
 await page.getByRole('button',{name:'Apply conditions',exact:true}).click();
 await expect.poll(()=>page.evaluate(()=>(window as any).__testAnalysisRequest().categories)).toEqual(['Forex']);
 await expect(page.locator('.analysis-scan-status')).toContainText('0 / 1 files');
});

test('Matching assets filters before ten-record pagination and match navigation keeps the category scope',async({page})=>{
 await open(page);const results=Array.from({length:25},(_,index)=>result(index,index%2?'xauusd':'btcusd',index?1:0));
 await complete(page,results);
 const section=page.getByRole('region',{name:'Analysis results',exact:true}),nav=page.getByRole('navigation',{name:'Analysis file pages',exact:true});
 await expect(section.locator('.analysis-file-match')).toHaveCount(10);await expect(nav).toContainText('Files 1–10 of 24');
 await nav.getByRole('button',{name:'Next files',exact:true}).click();await expect(nav).toContainText('Files 11–20 of 24');
 await control(page,'Result asset category').selectOption('Commodities');
 await expect(nav).toContainText('Files 1–10 of 12');await expect(section.locator('.analysis-file-match')).toHaveCount(10);
 await expect(section.locator('.analysis-asset')).toHaveCount(1);await expect(section.locator('.analysis-asset')).toContainText('Gold vs US Dollar');
 await nav.getByRole('button',{name:'Next files',exact:true}).click();await expect(nav).toContainText('Files 11–12 of 12');
 await expect(section.locator('.analysis-file-match')).toHaveCount(2);
 await page.getByRole('button',{name:`Inspect ${file(21,'xauusd').name} matches`,exact:true}).click();
 const dialog=page.getByRole('dialog',{name:'Analysis match chart',exact:true});
 await expect(dialog.getByTestId('analysis-chart')).toBeVisible();await dialog.getByRole('button',{name:'Previous match',exact:false}).click();
 await expect.poll(()=>page.evaluate(()=>(window as any).__testAnalysisChartRequests().at(-1))).toMatchObject({resultID:'result-19',occurrence:0});
 await page.keyboard.press('Escape');
 await control(page,'Result asset category').selectOption('Cryptocurrencies');await expect(nav).toContainText('Files 1–10 of 12');
 await expect(page.getByRole('checkbox',{name:'Show files without matches',exact:true})).toHaveCount(0);
 await expect(section.getByRole('button',{name:`Inspect ${file(0).name} matches`,exact:true})).toHaveCount(0);
 await expect(nav).toContainText('Files 1–10 of 12');
 await control(page,'Result asset category').selectOption('Forex');await expect(nav).toHaveCount(0);
 await expect(section.getByText('No matching files in this asset category.',{exact:true})).toBeVisible();
});

test('asset headings use full catalog descriptions instead of aggregate-match boilerplate',async({page})=>{
 const description='ARK Artificial Intelligence & Robotics UCITS ETF';
 await open(page,[{...file(0,'arkiususd'),description,category:'ETFs'}]);
 await complete(page,[{...result(0,'arkiususd',224),description,category:'ETFs'}]);
 const heading=page.locator('.analysis-asset');await expect(heading).toContainText('ARKIUSUSD');await expect(heading).toContainText(description);
 await expect(heading).not.toContainText('total matches');await expect(page.getByRole('button',{name:/Inspect arkiususd/})).toContainText('224');
 await expect(control(page,'Result asset category').locator('option[value="ETFs"]')).toHaveCount(1);
});

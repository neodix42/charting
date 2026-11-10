import {test,expect,type Page} from '@playwright/test';
import {openTestApp,reloadTestApp} from './helpers/testApp';
import type {AnalysisFile,AnalysisRequest} from '../src/workspace/screenerAnalysis';

const fields=['timestamp','open','high','low','close','volume','weekday'];
const file=(id:string,timeframe='d1',extra:string[]=[],dataSource:'dukascopy'|'hyperliquid'='dukascopy'):AnalysisFile=>({id,name:`${id}-${timeframe}.csv`,symbol:id,timeframe,dataSource,fields:[...fields,...extra],bytes:1000,category:dataSource==='dukascopy'?'Forex':'Hyperliquid perpetual'});
const control=(page:Page,name:string)=>page.getByLabel(name,{exact:true});
async function analysis(page:Page){
 await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Analysis',exact:true}).click();
 await expect(page.getByRole('button',{name:'Apply conditions',exact:true})).toBeEnabled();
}
async function open(page:Page,files:AnalysisFile[]=[file('eurusd'),file('gbpusd')]){
 await openTestApp(page);await page.evaluate(files=>{
  (window as any).__testSetEnrichmentFiles(files);(window as any).__testSetAnalysisView({...((window as any).__testAnalysisView()),profitabilityFilter:0});
 },files);await analysis(page);
}
const warning=(page:Page,side='left')=>page.getByRole('group',{name:`Condition 1 ${side}`,exact:true}).getByRole('status');

test('rolling Low / High are available before enrichment and retain periods, offsets and timeframes when saved and loaded',async({page})=>{
 await open(page);const left=control(page,'Condition 1 left field');
 await expect(left.locator('option[value="low"]')).toHaveText('Low');await expect(left.locator('option[value="high"]')).toHaveText('High');
 await left.selectOption({label:'Low (last 10 bars)'});await expect(control(page,'Condition 1 left lookback bars')).toHaveValue('10');
 await expect(warning(page)).toContainText('missing from 2 of 2 selected Dukascopy files');
 await control(page,'Condition 1 left lookback bars').fill('25');await control(page,'Condition 1 left bars ago').fill('1');await control(page,'Condition 1 left timeframe').selectOption('d1');
 await expect(left).toHaveValue('low_25');await expect(warning(page)).toContainText('add Low with Period 25');
 await control(page,'Condition 1 right type').selectOption('field');await control(page,'Condition 1 right field').selectOption({label:'High (last 10 bars)'});await control(page,'Condition 1 right lookback bars').fill('30');
 await expect(warning(page,'right')).toContainText('add High with Period 30');
 await page.getByRole('button',{name:'Save as new',exact:true}).click();const dialog=page.getByRole('dialog',{name:'Save condition',exact:true});
 await dialog.getByLabel('Condition name',{exact:true}).fill('Rolling price range');await dialog.getByRole('button',{name:'Save condition',exact:true}).click();await expect(dialog).toHaveCount(0);
 await left.selectOption('low');await expect(control(page,'Condition 1 left lookback bars')).toHaveCount(0);await expect(warning(page)).toHaveCount(0);
 await page.getByRole('button',{name:'Load condition Rolling price range',exact:true}).click();await expect(left).toHaveValue('low_25');await expect(control(page,'Condition 1 left lookback bars')).toHaveValue('25');
 await reloadTestApp(page);await analysis(page);await expect(left).toHaveValue('low_25');await expect(control(page,'Condition 1 right field')).toHaveValue('high_30');
 await expect(control(page,'Condition 1 left bars ago')).toHaveValue('1');await expect(control(page,'Condition 1 left timeframe')).toHaveValue('d1');await expect(warning(page)).toContainText('Period 25');
 await page.getByRole('button',{name:'Apply conditions',exact:true}).click();
 const request=await page.evaluate(()=>(window as any).__testAnalysisRequest()) as AnalysisRequest;
 expect(request.conditions[0]).toMatchObject({left:{field:'low_25',offset:1,timeframe:'d1'},right:{field:'high_30'}});
});

test('enrichment notices follow exact columns, source, scan period, companion timeframe and refreshed files',async({page})=>{
 const files=[file('eurusd','d1',['low_10']),file('gbpusd'),file('eurusd','w1',['high_20']),file('btc','d1',['low_20'],'hyperliquid')];
 await open(page,files);await control(page,'Analysis period of data').selectOption('d1');
 const left=control(page,'Condition 1 left field');await left.selectOption('low_10');await expect(warning(page)).toContainText('missing from 1 of 2 selected Dukascopy d1 files');
 await left.selectOption('high_20');await expect(warning(page)).toContainText('missing from 2 of 2 selected Dukascopy d1 files');
 await control(page,'Condition 1 left timeframe').selectOption('w1');await expect(warning(page)).toHaveCount(0);
 await control(page,'Condition 1 left timeframe').selectOption('same');await left.selectOption('low_10');
 await page.evaluate(files=>(window as any).__testSetEnrichmentFiles(files.map((file:AnalysisFile)=>({...file,fields:[...file.fields,'low_10']}))),files);
 await page.getByRole('button',{name:'Refresh fields',exact:true}).click();await expect(warning(page)).toHaveCount(0);
 await control(page,'Condition 1 left lookback bars').fill('20');await expect(warning(page)).toContainText('missing from 2 of 2 selected Dukascopy d1 files');
 await control(page,'Analysis data source').selectOption('hyperliquid');await expect(page.getByLabel('Scan Hyperliquid perpetual',{exact:true})).toBeChecked();await expect(warning(page)).toHaveCount(0);
 await control(page,'Condition 1 left lookback bars').fill('10');await expect(warning(page)).toHaveCount(0);
 await control(page,'Condition 1 left lookback bars').fill('15');await expect(warning(page)).toContainText('missing from 1 of 1 selected Hyperliquid d1 files');await expect(warning(page)).toContainText('Period 15');
});

test('Low / High lookbacks use Enrichment limits and prevent invalid scan or named saves',async({page})=>{
 await open(page);await control(page,'Condition 1 left field').selectOption('low_10');
 const lookback=control(page,'Condition 1 left lookback bars'),apply=page.getByRole('button',{name:'Apply conditions',exact:true}),save=page.getByRole('button',{name:'Save as new',exact:true});
 for(const value of ['0','-1','1.5','2001']){
  await lookback.fill(value);await expect(apply).toBeDisabled();await expect(save).toBeDisabled();await expect(page.locator('.analysis-apply-row')).toContainText('whole number from 1 to 2,000');
 }
 for(const value of ['1','2000']){await lookback.fill(value);await expect(apply).toBeEnabled();await expect(save).toBeEnabled();}
});

test('Condition and operand dropdowns align with constants, rolling controls, warnings and temporal comparisons',async({page})=>{
 await open(page);await control(page,'Condition 1 left field').selectOption('weekday');await control(page,'Condition 1 operator').selectOption('equal');await control(page,'Condition 1 right constant').fill('2');
 const aligned=async()=>{
  const left=await control(page,'Condition 1 left field').boundingBox(),condition=await control(page,'Condition 1 operator').boundingBox();
  expect(left&&condition).toBeTruthy();expect(Math.abs(left!.y-condition!.y)).toBeLessThanOrEqual(1);expect(Math.abs(left!.height-condition!.height)).toBeLessThanOrEqual(1);
  const groups=page.locator('.analysis-condition .analysis-operand');
  for(const group of await groups.all()){
   const main=await group.locator('.analysis-operand-main').boundingBox();
   for(const box of await group.locator('.analysis-operand-main>select,.analysis-operand-main>input').all()){
    const bounds=await box.boundingBox();expect(Math.abs(bounds!.y-main!.y)).toBeLessThanOrEqual(1);expect(Math.abs(bounds!.height-main!.height)).toBeLessThanOrEqual(1);
   }
  }
 };
 for(const width of [1600,1100]){
  await page.setViewportSize({width,height:1000});await aligned();
  await control(page,'Condition 1 left field').selectOption('low_10');await control(page,'Condition 1 left lookback bars').fill('25');await aligned();
  await control(page,'Condition 1 right type').selectOption('field');await control(page,'Condition 1 right field').selectOption('high_10');await aligned();
  await control(page,'Condition 1 operator').selectOption('between');await aligned();
  await control(page,'Condition 1 upper type').selectOption('field');await control(page,'Condition 1 upper field').selectOption('high_10');await aligned();
  await control(page,'Condition 1 operator').selectOption('withinDistance');await aligned();
  const multiplier=await control(page,'Condition 1 distance multiplier').boundingBox(),distance=await control(page,'Condition 1 distance field').boundingBox();
  expect(Math.abs(multiplier!.y-distance!.y)).toBeLessThanOrEqual(1);expect(Math.abs(multiplier!.height-distance!.height)).toBeLessThanOrEqual(1);
  await control(page,'Condition 1 operator').selectOption('occurredWithin');await aligned();await expect(control(page,'Condition 1 comparison')).toBeVisible();
  await page.screenshot({path:`test-results/analysis-rolling-alignment-${width}.png`,fullPage:true});
  await control(page,'Condition 1 left field').selectOption('weekday');await control(page,'Condition 1 right type').selectOption('number');await control(page,'Condition 1 operator').selectOption('equal');
 }
});

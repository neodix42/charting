import {test,expect,type Locator,type Page} from '@playwright/test';
import starter from '../../examples/starter-one-tab-one-chart.json' with {type:'json'};
import intermediate from '../../examples/intermediate-two-tabs.json' with {type:'json'};
import type {IndicatorConfig,Workspace} from '../src/types';
import {openTestApp,reloadTestApp} from './helpers/testApp';

const ichimokuComponents=[
 ['conversion','Show conversion line'],['base','Show base line'],['spanA','Show leading span A'],
 ['spanB','Show leading span B'],['lagging','Show lagging line'],['cloud','Show cloud fill'],
] as const;
const macdComponents=[['macd','Show MACD line'],['signal','Show signal line'],['histogram','Show histogram'],['zero','Show zero line']] as const;
const savedWorkspace=(page:Page):Promise<Workspace>=>page.evaluate(()=>(window as any).__testWorkspace());
const savedIndicator=async(page:Page,type:IndicatorConfig['type'],index=0)=>(await savedWorkspace(page)).tabs[0].charts[index].indicators!.find(indicator=>indicator.type===type)!;
const saved=async(page:Page)=>expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();
// Read the actual composited plot canvases rather than persisted flags or chart
// internals. The primary magenta and fixed base red are distinct from fixture
// candles, grid and text; allow a few RGB units for stroke antialiasing.
const ichimokuPixels=(page:Page)=>page.getByTestId('chart-cell').first().getByTestId('chart-canvas').locator('canvas').evaluateAll(canvases=>{
 const counts={conversion:0,base:0},colours={conversion:[204,0,255],base:[216,95,104]};
 for(const canvas of canvases){
  const context=(canvas as HTMLCanvasElement).getContext('2d');if(!context)continue;
  const {data}=context.getImageData(0,0,(canvas as HTMLCanvasElement).width,(canvas as HTMLCanvasElement).height);
  for(let index=0;index<data.length;index+=4){
   if(data[index+3]<200)continue;
   for(const key of ['conversion','base'] as const){
    const colour=colours[key];if(colour.every((value,channel)=>Math.abs(data[index+channel]-value)<=3))counts[key]++;
   }
  }
 }
 return counts;
});
async function expectIchimokuPixels(page:Page,keys:readonly string[]){
 await page.mouse.move(0,0);
 await expect.poll(async()=>{
  const pixels=await ichimokuPixels(page);
  return (keys.includes('conversion')?pixels.conversion>8:pixels.conversion===0)&&(keys.includes('base')?pixels.base>8:pixels.base===0);
 },{message:`Only selected Ichimoku strokes should be painted: ${keys.join(', ')||'none'}`}).toBe(true);
}
async function seed(page:Page,workspace:Workspace){
 await page.addInitScript(workspace=>{
  if(!sessionStorage.getItem('cryptodesk-explicit-test-workspace'))sessionStorage.setItem('cryptodesk-explicit-test-workspace',JSON.stringify(workspace));
 },workspace);
 await openTestApp(page);
 await expect(page.getByTestId('chart-cell')).toHaveCount(workspace.tabs[0].charts.length);
 await expect.poll(()=>page.evaluate(()=>(window as any).__testLeases())).toBe(workspace.tabs[0].charts.length);
}
async function showOnly(dialog:Locator,keys:readonly string[]){
 for(const [key,label] of ichimokuComponents)await dialog.getByRole('checkbox',{name:label,exact:true}).setChecked(keys.includes(key));
}
async function expectIchimokuSelection(dialog:Locator,keys:readonly string[]){
 for(const [key,label] of ichimokuComponents)await expect(dialog.getByRole('checkbox',{name:label,exact:true})).toBeChecked({checked:keys.includes(key)});
}
const editIchimoku=async(page:Page,index=0)=>{
 await page.getByTestId('chart-cell').nth(index).getByRole('button',{name:'Edit Ichimoku Cloud (9, 26, 52, 26)',exact:true}).click();
 return page.getByRole('dialog',{name:'Ichimoku Cloud parameters',exact:true});
};

test('legacy multi-output indicators default to visible and cancelled selections leave saved JSON unchanged',async({page})=>{
 const workspace=structuredClone(starter) as Workspace;
 workspace.tabs[0].charts[0].indicators=[
  {id:'10000000-0000-4000-8000-000000000001',type:'ichimoku',parameters:{conversion:9,base:26,spanB:52,displacement:26}},
  {id:'10000000-0000-4000-8000-000000000002',type:'macd',parameters:{fast:12,slow:26,signal:9}},
 ];
 await seed(page,workspace);
 let dialog=await editIchimoku(page);
 await expect(dialog.getByRole('group',{name:'Visible components',exact:true})).toBeVisible();
 await expectIchimokuSelection(dialog,ichimokuComponents.map(([key])=>key));
 await dialog.screenshot({path:'test-results/indicator-visibility-dialog.png'});
 await showOnly(dialog,['base']);await dialog.getByRole('button',{name:'Cancel',exact:true}).click();
 await page.getByTestId('chart-cell').getByRole('button',{name:'Edit MACD (12, 26, 9)',exact:true}).click();
 dialog=page.getByRole('dialog',{name:'MACD parameters',exact:true});
 for(const [,label] of macdComponents)await expect(dialog.getByRole('checkbox',{name:label,exact:true})).toBeChecked();
 await dialog.getByRole('checkbox',{name:'Show signal line',exact:true}).uncheck();await dialog.press('Escape');
 await saved(page);expect((await savedWorkspace(page)).tabs[0].charts[0].indicators).toEqual(workspace.tabs[0].charts[0].indicators);
 await reloadTestApp(page);await expect(page.locator('.indicator-chip')).toHaveCount(2);
 expect((await savedWorkspace(page)).tabs[0].charts[0].indicators).toEqual(workspace.tabs[0].charts[0].indicators);
});

test('Ichimoku can retain only base, only conversion or no components and restore all after reload without new feeds',async({page})=>{
 const errors:string[]=[];page.on('pageerror',error=>errors.push(error.message));
 await openTestApp(page);await expect(page.getByLabel('OHLCV')).toContainText('C');
 await page.getByTestId('chart-cell').getByLabel('Indicators',{exact:true}).selectOption('ichimoku');
 let dialog=page.getByRole('dialog',{name:'Ichimoku Cloud parameters',exact:true});
 await dialog.getByLabel('Indicator color',{exact:true}).fill('#cc00ff');
 await showOnly(dialog,['base']);await dialog.getByRole('button',{name:'OK',exact:true}).click();await saved(page);
 await expectIchimokuPixels(page,['base']);
 const original=await savedIndicator(page,'ichimoku');
 expect(original.visibility).toEqual({conversion:false,spanA:false,spanB:false,lagging:false,cloud:false});
 const parameters={...original.parameters};
 for(const keys of [['base'],['conversion'],[]] as string[][]){
  await reloadTestApp(page);await expect(page.locator('.indicator-chip')).toHaveCount(1);
  const attachments=await page.evaluate(()=>(window as any).__testAttachCount());
  dialog=await editIchimoku(page);await showOnly(dialog,keys);
  await dialog.getByRole('button',{name:'OK',exact:true}).click();await saved(page);
  const indicator=await savedIndicator(page,'ichimoku');
  expect(indicator.id).toBe(original.id);expect(indicator.parameters).toEqual(parameters);
  for(const [key] of ichimokuComponents)expect(indicator.visibility?.[key]!==false).toBe(keys.includes(key));
  await expectIchimokuPixels(page,keys);
  expect(await page.evaluate(()=>(window as any).__testAttachCount())).toBe(attachments);
  dialog=await editIchimoku(page);await expectIchimokuSelection(dialog,keys);await dialog.getByRole('button',{name:'Cancel',exact:true}).click();
 }
 await reloadTestApp(page);dialog=await editIchimoku(page);await expectIchimokuSelection(dialog,[]);
 await showOnly(dialog,ichimokuComponents.map(([key])=>key));await dialog.getByRole('button',{name:'OK',exact:true}).click();await saved(page);
 await expectIchimokuPixels(page,['conversion','base']);
 expect(await savedIndicator(page,'ichimoku')).not.toHaveProperty('visibility');
 await reloadTestApp(page);dialog=await editIchimoku(page);await expectIchimokuSelection(dialog,ichimokuComponents.map(([key])=>key));
 expect(errors).toEqual([]);
});

test('MACD plot and zero-line choices persist independently without changing its calculation periods or feed',async({page})=>{
 const errors:string[]=[];page.on('pageerror',error=>errors.push(error.message));
 await openTestApp(page);await expect(page.getByLabel('OHLCV')).toContainText('C');
 const attachments=await page.evaluate(()=>(window as any).__testAttachCount());
 await page.getByTestId('chart-cell').getByLabel('Indicators',{exact:true}).selectOption('macd');
 let dialog=page.getByRole('dialog',{name:'MACD parameters',exact:true});
 await dialog.getByRole('checkbox',{name:'Show MACD line',exact:true}).uncheck();
 await dialog.getByRole('checkbox',{name:'Show histogram',exact:true}).uncheck();
 await dialog.getByRole('button',{name:'OK',exact:true}).click();await saved(page);
 const original=await savedIndicator(page,'macd');expect(original.visibility).toEqual({macd:false,histogram:false});
 expect(original.parameters).toEqual({fast:12,slow:26,signal:9});
 expect(await page.evaluate(()=>(window as any).__testAttachCount())).toBe(attachments);
 await reloadTestApp(page);await page.getByTestId('chart-cell').getByRole('button',{name:'Edit MACD (12, 26, 9)',exact:true}).click();
 dialog=page.getByRole('dialog',{name:'MACD parameters',exact:true});
 await expect(dialog.getByRole('checkbox',{name:'Show MACD line',exact:true})).not.toBeChecked();
 await expect(dialog.getByRole('checkbox',{name:'Show histogram',exact:true})).not.toBeChecked();
 await expect(dialog.getByRole('checkbox',{name:'Show signal line',exact:true})).toBeChecked();
 await expect(dialog.getByRole('checkbox',{name:'Show zero line',exact:true})).toBeChecked();
 await dialog.getByRole('checkbox',{name:'Show signal line',exact:true}).uncheck();await dialog.getByRole('checkbox',{name:'Show zero line',exact:true}).uncheck();
 await dialog.getByRole('button',{name:'OK',exact:true}).click();await saved(page);
 expect(await savedIndicator(page,'macd')).toEqual({...original,visibility:{macd:false,signal:false,histogram:false,zero:false}});
 await reloadTestApp(page);await page.getByTestId('chart-cell').getByRole('button',{name:'Edit MACD (12, 26, 9)',exact:true}).click();
 dialog=page.getByRole('dialog',{name:'MACD parameters',exact:true});
 for(const [,label] of macdComponents)await expect(dialog.getByRole('checkbox',{name:label,exact:true})).not.toBeChecked();
 expect(errors).toEqual([]);
});

test('shared visibility settings reach maximized siblings and remain independent from other charts and tabs',async({page})=>{
 const workspace=structuredClone(intermediate) as Workspace;await seed(page,workspace);
 const attachments=await page.evaluate(()=>(window as any).__testAttachCount());
 await page.getByLabel('Maximize chart',{exact:true}).first().click();
 await expect(page.locator('.cell-wrapper.maximized')).toHaveCount(1);
 await page.getByTestId('tab-chart-controls').getByLabel('Indicators for current tab',{exact:true}).selectOption('ichimoku');
 let dialog=page.getByRole('dialog',{name:'Ichimoku Cloud parameters',exact:true});
 await showOnly(dialog,['base']);await dialog.getByRole('button',{name:'OK',exact:true}).click();await saved(page);
 const before=await savedWorkspace(page),visibility={conversion:false,spanA:false,spanB:false,lagging:false,cloud:false};
 expect(before.tabs[0].charts.map(chart=>chart.indicators?.[0].visibility)).toEqual([visibility,visibility,visibility]);
 const ids=before.tabs[0].charts.map(chart=>chart.indicators![0].id);expect(new Set(ids).size).toBe(3);
 expect(before.tabs[1]).toEqual(workspace.tabs[1]);
 dialog=await editIchimoku(page);await showOnly(dialog,['conversion']);await dialog.getByRole('button',{name:'OK',exact:true}).click();await saved(page);
 const after=await savedWorkspace(page);
 expect(after.tabs[0].charts[0].indicators![0].visibility).toEqual({base:false,spanA:false,spanB:false,lagging:false,cloud:false});
 expect(after.tabs[0].charts.slice(1)).toEqual(before.tabs[0].charts.slice(1));expect(after.tabs[1]).toEqual(workspace.tabs[1]);
 expect(after.tabs[0].charts.map(chart=>chart.indicators![0].id)).toEqual(ids);
 expect(await page.evaluate(()=>(window as any).__testAttachCount())).toBe(attachments);
 await reloadTestApp(page);await expect(page.locator('.indicator-chip')).toHaveCount(3);
 expect((await savedWorkspace(page)).tabs[0].charts.map(chart=>chart.indicators)).toEqual(after.tabs[0].charts.map(chart=>chart.indicators));
});

test('visibility controls remain keyboard accessible in a short window and Escape discards staged flags',async({page})=>{
 await page.setViewportSize({width:1100,height:600});await openTestApp(page);
 const picker=page.getByTestId('chart-cell').getByLabel('Indicators',{exact:true});await picker.focus();await picker.selectOption('ichimoku');
 const dialog=page.getByRole('dialog',{name:'Ichimoku Cloud parameters',exact:true});
 await expect(dialog.getByRole('group',{name:'Visible components',exact:true})).toBeVisible();
 const checkbox=dialog.getByRole('checkbox',{name:'Show conversion line',exact:true});await checkbox.focus();await page.keyboard.press('Space');await expect(checkbox).not.toBeChecked();
 const confirm=dialog.getByRole('button',{name:'OK',exact:true}),close=dialog.getByRole('button',{name:'Close indicator parameters',exact:true});
 await confirm.focus();await page.keyboard.press('Tab');await expect(close).toBeFocused();await page.keyboard.press('Shift+Tab');await expect(confirm).toBeFocused();
 expect(await page.evaluate(()=>document.documentElement.scrollWidth)).toBeLessThanOrEqual(1100);
 await page.keyboard.press('Escape');await expect(dialog).toHaveCount(0);await expect(picker).toBeFocused();
 expect((await savedWorkspace(page)).tabs[0].charts[0].indicators??[]).toEqual([]);
 await picker.selectOption('ichimoku');await expect(page.getByRole('dialog').getByRole('checkbox',{name:'Show conversion line',exact:true})).toBeChecked();
});

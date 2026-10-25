import {test,expect,type Page} from '@playwright/test';
import intermediate from '../../examples/intermediate-two-tabs.json' with {type:'json'};
import type {Workspace} from '../src/types';

const storageKey='cryptodesk-explicit-test-workspace';
const savedWorkspace=(page:Page):Promise<Workspace>=>page.evaluate(()=>(window as any).__testWorkspace());
async function seed(page:Page,workspace:Workspace=structuredClone(intermediate) as Workspace){
 await page.addInitScript(({key,workspace})=>{if(!sessionStorage.getItem(key))sessionStorage.setItem(key,JSON.stringify(workspace));},{key:storageKey,workspace});
 await page.goto('/');await expect(page.getByTestId('chart-cell')).toHaveCount(workspace.tabs[0].charts.length);await expect.poll(()=>page.evaluate(()=>(window as any).__testLeases())).toBe(workspace.tabs[0].charts.length);
}
const toolbar=(page:Page)=>page.getByRole('toolbar',{name:'Current tab chart controls',exact:true});

async function expectTopHeader(page:Page){
 const header=page.locator('.app-header'),controls=toolbar(page);
 await expect(header.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();
 await expect(header.getByRole('toolbar',{name:'Current tab chart controls',exact:true})).toBeVisible();
 for(const name of ['Add chart','Duplicate tab','Open settings'])await expect(header.getByRole('button',{name,exact:true})).toBeVisible();
 await expect(header.getByRole('combobox',{name:'Grid layout',exact:true})).toBeVisible();
 await expect(page.locator('.workspace-title')).toHaveCount(0);
 await expect(page.getByText('Independent public spot charts',{exact:true})).toHaveCount(0);
 const geometry=await header.evaluate(element=>{
  const bounds=(node:Element)=>{const rect=node.getBoundingClientRect();return{left:rect.left,right:rect.right,top:rect.top,bottom:rect.bottom,centerY:(rect.top+rect.bottom)/2};};
  const find=(selector:string)=>bounds(element.querySelector(selector)!);
  return{header:bounds(element),brand:find('.brand'),controls:find('[data-testid="tab-chart-controls"]'),periods:find('[role="group"][aria-label="Period for current tab"]'),indicators:find('[aria-label="Indicators for current tab"]'),grid:find('[aria-label="Grid layout"]'),add:find('[aria-label="Add chart"]'),duplicate:find('[aria-label="Duplicate tab"]'),save:find('.save-indicator'),settings:find('[aria-label="Open settings"]'),tabs:bounds(document.querySelector('.tab-row')!),chartTop:document.querySelector('.workspace-content')!.getBoundingClientRect().top,documentWidth:document.documentElement.scrollWidth,windowWidth:window.innerWidth};
 });
 for(const rect of [geometry.brand,geometry.controls,geometry.periods,geometry.indicators,geometry.grid,geometry.add,geometry.duplicate,geometry.save,geometry.settings]){
  expect(rect.top).toBeGreaterThanOrEqual(geometry.header.top);
  expect(rect.bottom).toBeLessThanOrEqual(geometry.header.bottom);
  expect(Math.abs(rect.centerY-geometry.header.centerY)).toBeLessThanOrEqual(2);
  expect(rect.left).toBeGreaterThanOrEqual(geometry.header.left);
  expect(rect.right).toBeLessThanOrEqual(geometry.header.right);
 }
 const ordered=[geometry.brand,geometry.controls,geometry.grid,geometry.add,geometry.duplicate,geometry.save,geometry.settings];
 for(let index=1;index<ordered.length;index++)expect(ordered[index].left).toBeGreaterThanOrEqual(ordered[index-1].right);
 expect(geometry.indicators.left).toBeGreaterThanOrEqual(geometry.periods.right);
 expect(Math.abs((geometry.controls.left+geometry.controls.right)/2-geometry.windowWidth/2)).toBeLessThanOrEqual(2);
 expect(geometry.header.bottom).toBeLessThanOrEqual(geometry.tabs.top);
 expect(geometry.chartTop-geometry.tabs.bottom).toBeLessThanOrEqual(20);
 expect(geometry.documentWidth).toBe(geometry.windowWidth);
 const controlsGeometry=await controls.evaluate(element=>{const periods=element.querySelector('[role="group"]')!.getBoundingClientRect(),indicators=element.querySelector('select')!.getBoundingClientRect(),row=element.getBoundingClientRect();return{center:(periods.left+indicators.right)/2,rowCenter:(row.left+row.right)/2};});
 expect(Math.abs(controlsGeometry.center-controlsGeometry.rowCenter)).toBeLessThanOrEqual(2);
}

for(const viewport of [{width:1600,height:1000},{width:1100,height:720}])test(`chart controls and tab actions share the top logo row at ${viewport.width}px`,async({page})=>{
 await page.setViewportSize(viewport);await seed(page);await expectTopHeader(page);
 await expect(page.getByRole('tab',{name:/Main/})).toBeVisible();
 await page.screenshot({path:`test-results/top-header-${viewport.width}.png`});
});

test('tab double-click and keyboard close preserve rename, grid and header duplication behavior',async({page})=>{
 await seed(page);const header=page.locator('.app-header');
 await page.getByRole('tab',{selected:true}).dblclick();let dialog=page.getByRole('dialog',{name:'Rename tab',exact:true});
 await dialog.getByRole('textbox',{name:'Tab name',exact:true}).fill('Header research');await dialog.getByRole('button',{name:'Rename',exact:true}).click();
 await expect(page.getByRole('tab',{name:/Header research/})).toBeVisible();
 await header.getByRole('combobox',{name:'Grid layout',exact:true}).selectOption('2x2');await expect(page.getByTestId('chart-placeholder')).toHaveCount(1);
 await header.getByRole('button',{name:'Add chart',exact:true}).click();await expect(page.getByTestId('chart-cell')).toHaveCount(4);await expect(page.getByTestId('chart-placeholder')).toHaveCount(0);
 await header.getByRole('button',{name:'Duplicate tab',exact:true}).click();await expect(page.locator('[role="tab"][data-tab-id]')).toHaveCount(3);await expect(page.getByTestId('chart-cell')).toHaveCount(4);
 await page.keyboard.press('Control+w');dialog=page.getByRole('dialog',{name:/Close Header research/});await dialog.getByRole('button',{name:'Close tab',exact:true}).click();
 await expect(page.locator('[role="tab"][data-tab-id]')).toHaveCount(2);await expect(page.getByRole('tab',{name:/Header research/})).toBeVisible();
 await expect(header.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();
 const saved=await savedWorkspace(page);expect(saved.tabs).toHaveLength(2);expect(saved.tabs[0].name).toBe('Header research');expect(saved.tabs[0].charts).toHaveLength(4);expect(saved.tabs[0].grid).toMatchObject({rows:2,columns:2});
 await page.reload();await expect(page.getByRole('tab',{name:/Header research/})).toBeVisible();await expectTopHeader(page);
});

test('top-center tab controls display mixed periods and apply weekly to every current chart',async({page})=>{
 const workspace=structuredClone(intermediate) as Workspace;for(const chart of workspace.tabs[0].charts)chart.view={mode:'historical',fromSec:1791763200,toSec:1791849600,barSpacing:9,rightOffset:2};await seed(page,workspace);
 const controls=toolbar(page),periods=controls.getByRole('group',{name:'Period for current tab',exact:true});await expect(controls).toHaveAttribute('data-testid','tab-chart-controls');await expect(periods.getByRole('button')).toHaveCount(7);await expect(periods.locator('[aria-pressed="true"]')).toHaveCount(0);
 await expectTopHeader(page);
 await page.evaluate(()=>{const api=window.go!.main!.App!,original=api.AttachChart.bind(api);(window as any).__tabControlAttachments=[];api.AttachChart=async(...args)=>{(window as any).__tabControlAttachments.push({key:args[2],from:args[3],to:args[4]});return original(...args);};});
 await controls.getByRole('button',{name:'Apply 1w to current tab',exact:true}).click();await expect(periods.getByRole('button',{name:'Apply 1w to current tab',exact:true})).toHaveAttribute('aria-pressed','true');await expect(page.getByRole('button',{name:'Timeframe 1w',exact:true})).toHaveCount(3);for(const chart of await page.getByTestId('chart-cell').all())await expect(chart.getByRole('button',{name:'Timeframe 1w',exact:true})).toHaveAttribute('aria-pressed','true');
 await expect.poll(()=>page.evaluate(()=>(window as any).__tabControlAttachments.length)).toBe(3);const attachments=await page.evaluate(()=>(window as any).__tabControlAttachments);expect(attachments.every((attachment:any)=>attachment.key.timeframe==='1w'&&attachment.to-attachment.from===500*604800&&attachment.to>Date.now()/1000-30)).toBe(true);
 await expect(page.getByRole('button',{name:'All changes saved'})).toBeVisible();const saved=await savedWorkspace(page);expect(saved.tabs[0].charts.map(chart=>chart.timeframe)).toEqual(['1w','1w','1w']);expect(saved.tabs[0].charts.every(chart=>chart.view.mode==='live')).toBe(true);expect(saved.tabs[1]).toEqual(workspace.tabs[1]);
 await page.reload();await expect(toolbar(page).getByRole('button',{name:'Apply 1w to current tab',exact:true})).toHaveAttribute('aria-pressed','true');await expect(page.getByTestId('chart-cell')).toHaveCount(3);await page.getByRole('tab',{name:/Longer term/}).click();await expect(toolbar(page).getByRole('button',{name:'Apply 1d to current tab',exact:true})).toHaveAttribute('aria-pressed','true');
});

test('one tab indicator dialog applies custom settings independently to visible and maximized sibling charts',async({page})=>{
 const errors:string[]=[];page.on('pageerror',error=>errors.push(error.message));await seed(page);const attachments=await page.evaluate(()=>(window as any).__testAttachCount());await page.getByLabel('Maximize chart',{exact:true}).first().click();await expect(page.locator('.cell-wrapper.maximized')).toHaveCount(1);
 const dropdown=toolbar(page).getByRole('combobox',{name:'Indicators for current tab',exact:true});await dropdown.selectOption('sma');let dialog=page.getByRole('dialog',{name:'SMA parameters',exact:true});await expect(page.getByRole('dialog')).toHaveCount(1);await expect(page.locator('.indicator-chip')).toHaveCount(0);await dialog.getByLabel('Period',{exact:true}).fill('13');await page.screenshot({path:'test-results/current-tab-indicator-parameters.png'});await dialog.getByRole('button',{name:'Cancel',exact:true}).click();await expect(page.locator('.indicator-chip')).toHaveCount(0);
 const configurations=[{type:'sma',name:'SMA',fields:{Period:'13'}},{type:'ema',name:'EMA',fields:{Period:'8'}},{type:'ichimoku',name:'Ichimoku Cloud',fields:{'Conversion period':'7','Base period':'22','Leading span B period':'44',Displacement:'22'}},{type:'macd',name:'MACD',fields:{'Fast period':'8','Slow period':'17','Signal period':'5'}}];
 for(const configuration of configurations){await dropdown.selectOption(configuration.type);dialog=page.getByRole('dialog',{name:`${configuration.name} parameters`,exact:true});await expect(page.getByRole('dialog')).toHaveCount(1);for(const [label,value] of Object.entries(configuration.fields))await dialog.getByLabel(label,{exact:true}).fill(value);await dialog.getByRole('button',{name:'OK',exact:true}).click();await expect(page.getByRole('dialog')).toHaveCount(0);}
 await expect(page.locator('.indicator-chip')).toHaveCount(12);await expect(page.getByRole('button',{name:'All changes saved'})).toBeVisible();let saved=await savedWorkspace(page);const ids=saved.tabs[0].charts.flatMap(chart=>(chart.indicators??[]).map(indicator=>indicator.id));expect(new Set(ids).size).toBe(12);for(const chart of saved.tabs[0].charts){expect(chart.indicators?.map(indicator=>indicator.type)).toEqual(['sma','ema','ichimoku','macd']);expect(chart.indicators?.map(indicator=>indicator.parameters)).toEqual([{period:13},{period:8},{conversion:7,base:22,spanB:44,displacement:22},{fast:8,slow:17,signal:5}]);}expect(saved.tabs[1]).toEqual((intermediate as Workspace).tabs[1]);expect(await page.evaluate(()=>(window as any).__testAttachCount())).toBe(attachments);
 await page.getByTestId('chart-cell').first().getByRole('button',{name:'Edit SMA (13)',exact:true}).click();dialog=page.getByRole('dialog',{name:'SMA parameters',exact:true});await dialog.getByLabel('Period',{exact:true}).fill('21');await dialog.getByRole('button',{name:'OK',exact:true}).click();await expect(page.getByRole('button',{name:'All changes saved'})).toBeVisible();saved=await savedWorkspace(page);expect(saved.tabs[0].charts.map(chart=>chart.indicators?.[0].parameters.period)).toEqual([21,13,13]);expect(saved.tabs[0].charts.flatMap(chart=>(chart.indicators??[]).map(indicator=>indicator.id))).toEqual(ids);
 await page.reload();await expect(page.locator('.indicator-chip')).toHaveCount(12);await expect(page.getByTestId('chart-cell').first().getByRole('button',{name:'Edit SMA (21)',exact:true})).toBeVisible();expect((await savedWorkspace(page)).tabs[0].charts.map(chart=>chart.indicators?.[0].parameters.period)).toEqual([21,13,13]);await page.screenshot({path:'test-results/current-tab-controls.png'});expect(errors).toEqual([]);
});

test('tab period selection tracks per-chart edits and affects hidden maximized siblings',async({page})=>{
 await seed(page);const controls=toolbar(page),group=controls.getByRole('group',{name:'Period for current tab',exact:true});await controls.getByRole('button',{name:'Apply 4h to current tab',exact:true}).click();await expect(group.locator('[aria-pressed="true"]')).toHaveCount(1);await page.getByTestId('chart-cell').first().getByRole('button',{name:'Timeframe 15m',exact:true}).click();await expect(group.locator('[aria-pressed="true"]')).toHaveCount(0);
 await page.getByLabel('Maximize chart',{exact:true}).first().click();await controls.getByRole('button',{name:'Apply 1m to current tab',exact:true}).click();await expect(group.getByRole('button',{name:'Apply 1m to current tab',exact:true})).toHaveAttribute('aria-pressed','true');await page.getByLabel('Restore chart',{exact:true}).click();for(const chart of await page.getByTestId('chart-cell').all())await expect(chart.getByRole('button',{name:'Timeframe 1m',exact:true})).toHaveAttribute('aria-pressed','true');
 await expect(page.getByRole('button',{name:'All changes saved'})).toBeVisible();const saved=await savedWorkspace(page);expect(saved.tabs[0].charts.map(chart=>chart.timeframe)).toEqual(['1m','1m','1m']);expect(saved.tabs[1]).toEqual((intermediate as Workspace).tabs[1]);
});

test('a chart at indicator capacity disables the tab dropdown without partially applying settings',async({page})=>{
 const workspace=structuredClone(intermediate) as Workspace;workspace.tabs[0].charts[0].indicators=Array.from({length:11},(_,index)=>({id:`40000000-0000-4000-8000-${String(index+1).padStart(12,'0')}`,type:'sma',parameters:{period:index+1},color:'#e7ad51'}));await seed(page,workspace);
 const dropdown=toolbar(page).getByRole('combobox',{name:'Indicators for current tab',exact:true});await expect(dropdown).toBeEnabled();await dropdown.selectOption('ema');const dialog=page.getByRole('dialog',{name:'EMA parameters',exact:true});await dialog.getByRole('button',{name:'OK',exact:true}).click();await expect(dropdown).toBeDisabled();await expect(page.getByRole('button',{name:'All changes saved'})).toBeVisible();let saved=await savedWorkspace(page);expect(saved.tabs[0].charts.map(chart=>chart.indicators?.length)).toEqual([12,1,1]);expect(saved.tabs[1]).toEqual(workspace.tabs[1]);
 await page.reload();await expect(toolbar(page).getByRole('combobox',{name:'Indicators for current tab',exact:true})).toBeDisabled();saved=await savedWorkspace(page);expect(saved.tabs[0].charts.map(chart=>chart.indicators?.length)).toEqual([12,1,1]);await page.getByTestId('chart-cell').first().getByRole('button',{name:'Remove EMA (20)',exact:true}).click();await expect(toolbar(page).getByRole('combobox',{name:'Indicators for current tab',exact:true})).toBeEnabled();await expect(page.getByRole('button',{name:'All changes saved'})).toBeVisible();expect((await savedWorkspace(page)).tabs[0].charts.map(chart=>chart.indicators?.length)).toEqual([11,1,1]);
});

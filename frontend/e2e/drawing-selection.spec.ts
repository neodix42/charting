import {test,expect,type Locator,type Page} from '@playwright/test';
import type {DrawingConfig,DrawingPoint,DrawingType} from '../src/types';

test.use({deviceScaleFactor:2,launchOptions:{args:['--force-device-scale-factor=2']}});

interface Position {x:number;y:number}
interface ProjectedDrawing {id:string;type:DrawingType;points:DrawingPoint[];coordinates:Position[]}
interface LayerDiagnostic {
 id:string;tool:DrawingType|null;movingId:string|null;selectedId:string|null;editingId:string|null;
 width:number;height:number;drawings:ProjectedDrawing[];preview:ProjectedDrawing[];selection:ProjectedDrawing|null;
}
const tools=(chart:Locator)=>chart.getByRole('group',{name:'Drawing tools',exact:true});
const deleteButton=(chart:Locator)=>tools(chart).getByRole('button',{name:'Delete selected drawing',exact:true});
const savedDrawings=(page:Page,chartIndex=0,tabIndex=0):Promise<DrawingConfig[]>=>page.evaluate(({chartIndex,tabIndex})=>(window as any).__testWorkspace().tabs[tabIndex].charts[chartIndex].drawings??[],{chartIndex,tabIndex});
const layers=(page:Page):Promise<LayerDiagnostic[]>=>page.evaluate(async()=>{
 const modulePath='/src/charts/drawings/DrawingLayer.ts';
 const {drawingDiagnostics}=await import(modulePath);
 return [...drawingDiagnostics].map(([id,layer])=>({id,...layer.read()}));
});
async function layer(page:Page,chart:Locator){
 const id=await chart.getAttribute('data-chart-id'),result=(await layers(page)).find(item=>item.id===id);
 if(!result)throw Error(`Drawing layer ${id} is unavailable`);return result;
}
async function flush(page:Page){
 // A programmatic click can flush while the real mouse still holds a handle.
 await page.locator('.save-indicator').evaluate(element=>(element as HTMLElement).click());
 await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();
}
async function canvas(chart:Locator){
 const bounds=await chart.getByTestId('chart-canvas').locator('canvas').first().boundingBox();
 if(!bounds)throw Error('Price pane canvas is unavailable');return bounds;
}
async function clientPoint(page:Page,chart:Locator,point:Position){
 const bounds=await canvas(chart),current=await layer(page,chart);
 return {x:bounds.x+point.x*bounds.width/current.width,y:bounds.y+point.y*bounds.height/current.height};
}
async function place(page:Page,chart:Locator,type:DrawingType,count=1){
 const labels:Record<DrawingType,string>={arrowUp:'Arrow up',arrowDown:'Arrow down',horizontal:'Horizontal line',trendline:'Trendline'};
 await tools(chart).getByRole('button',{name:labels[type],exact:true}).click();
 const bounds=await canvas(chart);
 await page.mouse.click(bounds.x+bounds.width*.62,bounds.y+bounds.height*.58);
 if(type==='trendline')await page.mouse.click(bounds.x+bounds.width*.8,bounds.y+bounds.height*.33);
 await expect.poll(async()=>(await layer(page,chart)).drawings.length).toBe(count);
 await flush(page);return (await layer(page,chart)).drawings[count-1];
}
async function select(page:Page,chart:Locator,drawing:ProjectedDrawing){
 // Scope and grid changes restore their viewport after the first price-data render.
 await chart.evaluate(()=>new Promise<void>(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>resolve()))));
 let pointer:Position|undefined;
 await expect.poll(async()=>{
  const current=(await layer(page,chart)).drawings.find(item=>item.id===drawing.id);if(!current)return false;
  const a=current.coordinates[0],b=current.coordinates[1];
  const point=current.type==='trendline'||current.type==='horizontal'&&b?{x:(a.x+b.x)/2,y:(a.y+b.y)/2}
   :current.type==='arrowUp'||current.type==='arrowDown'?{x:a.x,y:a.y+(current.type==='arrowUp'?16:-16)}:a;
  pointer=await clientPoint(page,chart,point);const bounds=await canvas(chart);
  return pointer.x>=bounds.x&&pointer.x<=bounds.x+bounds.width&&pointer.y>=bounds.y&&pointer.y<=bounds.y+bounds.height;
 },{message:'The requested drawing must be visible in the settled price pane before clicking it'}).toBe(true);
 await page.mouse.click(pointer!.x,pointer!.y);
 await expect.poll(async()=>(await layer(page,chart)).selectedId).toBe(drawing.id);
 await expect(deleteButton(chart)).toBeEnabled();return pointer!;
}
async function endpoint(page:Page,chart:Locator,index:number){
 const selection=(await layer(page,chart)).selection;
 if(!selection||selection.coordinates.length!==2)throw Error('A selected line must display two endpoint handles');
 return clientPoint(page,chart,selection.coordinates[index]);
}
async function beginEdit(page:Page,chart:Locator,index:number,delta:Position){
 const pointer=await endpoint(page,chart,index),id=(await layer(page,chart)).selectedId;
 await page.mouse.move(pointer.x,pointer.y);await page.mouse.down();
 await page.mouse.move(pointer.x+delta.x,pointer.y+delta.y,{steps:6});
 await expect.poll(async()=>(await layer(page,chart)).editingId).toBe(id);
 return {x:pointer.x+delta.x,y:pointer.y+delta.y};
}
async function expectPreviewAt(page:Page,chart:Locator,index:number,pointer:Position){
 await expect.poll(async()=>{
  const point=(await layer(page,chart)).preview[0]?.coordinates[index];if(!point)return Infinity;
  const client=await clientPoint(page,chart,point);
  return Math.max(Math.abs(client.x-pointer.x),Math.abs(client.y-pointer.y));
 },{message:'The edited endpoint must remain under the actual browser pointer'}).toBeLessThanOrEqual(1.1);
}

for(const type of ['arrowUp','arrowDown','horizontal','trendline'] as const){
 test(`a single click selects ${type} without moving it and Delete removes only that item permanently`,async({page})=>{
  const errors:string[]=[];page.on('pageerror',error=>errors.push(error.message));
  await page.goto('/');const chart=page.getByTestId('chart-cell');await expect(chart.getByLabel('OHLCV')).toContainText('61,000.00');
  await expect(deleteButton(chart)).toBeDisabled();await expect(deleteButton(chart)).toHaveText('Delete');
  const original=await place(page,chart,type),saved=await savedDrawings(page);
  await select(page,chart,original);
  expect((await layer(page,chart)).movingId).toBeNull();expect((await layer(page,chart)).editingId).toBeNull();
  expect((await layer(page,chart)).drawings[0].coordinates).toEqual(original.coordinates);
  await flush(page);expect(await savedDrawings(page)).toEqual(saved);
  // The browser sends the standard Delete key for both physical Delete and Del keys.
  await page.keyboard.press('Delete');await expect.poll(async()=>(await layer(page,chart)).drawings).toEqual([]);
  expect((await layer(page,chart)).selectedId).toBeNull();await expect(deleteButton(chart)).toBeDisabled();
  await flush(page);expect(await savedDrawings(page)).toEqual([]);
  await page.reload();await expect(chart.getByLabel('OHLCV')).toContainText('61,000.00');
  expect((await layer(page,chart)).drawings).toEqual([]);expect(errors).toEqual([]);
 });
}

test('the toolbar deletes the selected arrow, while Delete in a text input or open dialog preserves it',async({page})=>{
 await page.goto('/');const chart=page.getByTestId('chart-cell');await expect(chart.getByLabel('OHLCV')).toContainText('61,000.00');
 const original=await place(page,chart,'arrowUp'),saved=await savedDrawings(page);await select(page,chart,original);
 await chart.getByRole('button',{name:'Select instrument',exact:true}).evaluate(element=>(element as HTMLElement).click());
 const search=page.getByRole('textbox',{name:'Search instruments',exact:true});await search.fill('BTC');
 expect((await layer(page,chart)).selectedId).toBe(original.id);
 await page.keyboard.press('Home');await page.keyboard.press('Delete');await expect(search).toHaveValue('TC');
 await flush(page);expect(await savedDrawings(page)).toEqual(saved);
 await page.getByRole('button',{name:'Close instrument selector',exact:true}).click();
 await select(page,chart,(await layer(page,chart)).drawings[0]);
 await page.getByRole('button',{name:'Open settings',exact:true}).evaluate(element=>(element as HTMLElement).click());
 const dialog=page.getByRole('dialog',{name:'Workspace settings',exact:true});await expect(dialog).toBeVisible();
 expect((await layer(page,chart)).selectedId).toBe(original.id);
 await dialog.getByRole('button',{name:'Close settings',exact:true}).focus();await page.keyboard.press('Delete');
 await flush(page);expect(await savedDrawings(page)).toEqual(saved);
 await dialog.getByRole('button',{name:'Close settings',exact:true}).click();
 await select(page,chart,(await layer(page,chart)).drawings[0]);await deleteButton(chart).click();
 await flush(page);expect(await savedDrawings(page)).toEqual([]);await expect(deleteButton(chart)).toBeDisabled();
});

test('toolbar and legacy Del key deletion preserve other drawings on the same chart',async({page})=>{
 await page.goto('/');const chart=page.getByTestId('chart-cell');await expect(chart.getByLabel('OHLCV')).toContainText('61,000.00');
 const up=await place(page,chart,'arrowUp'),down=await place(page,chart,'arrowDown',2),trend=await place(page,chart,'trendline',3);
 const saved=await savedDrawings(page);await select(page,chart,trend);await deleteButton(chart).click();await flush(page);
 expect(await savedDrawings(page)).toEqual(saved.filter(drawing=>drawing.id!==trend.id));
 await select(page,chart,(await layer(page,chart)).drawings.find(drawing=>drawing.id===up.id)!);
 // Older host webviews can expose the physical key using its legacy Del name.
 await page.evaluate(()=>document.body.dispatchEvent(new KeyboardEvent('keydown',{key:'Del',bubbles:true,cancelable:true})));
 await flush(page);expect(await savedDrawings(page)).toEqual(saved.filter(drawing=>drawing.id===down.id));
 await page.reload();await expect.poll(async()=>(await layer(page,chart)).drawings.map(drawing=>drawing.id)).toEqual([down.id]);
});

for(const index of [0,1])test(`dragging trendline endpoint ${index+1} changes only that anchor and restores its new position`,async({page})=>{
 await page.goto('/');const chart=page.getByTestId('chart-cell');await expect(chart.getByLabel('OHLCV')).toContainText('61,000.00');
 const original=await place(page,chart,'trendline'),saved=await savedDrawings(page);await select(page,chart,original);
 expect((await layer(page,chart)).selection?.coordinates).toEqual(original.coordinates);
 const pointer=await beginEdit(page,chart,index,{x:index?-48:36,y:index?35:-29});
 await expectPreviewAt(page,chart,index,pointer);
 const preview=(await layer(page,chart)).preview[0];expect(preview.points[1-index]).toEqual(saved[0].points[1-index]);
 expect((await layer(page,chart)).drawings).toEqual([]);
 await flush(page);expect(await savedDrawings(page)).toEqual(saved);
 await page.mouse.up();await expect.poll(async()=>(await layer(page,chart)).editingId).toBeNull();await flush(page);
 const resized=await savedDrawings(page);expect(resized).toHaveLength(1);
 expect(resized[0]).toMatchObject({id:original.id,type:'trendline',points:preview.points});
 expect(resized[0].points[index]).not.toEqual(saved[0].points[index]);expect(resized[0].points[1-index]).toEqual(saved[0].points[1-index]);
 const committed=(await layer(page,chart)).drawings[0];
 await page.reload();await expect(chart.getByLabel('OHLCV')).toContainText('61,000.00');
 await expect.poll(async()=>(await layer(page,chart)).drawings[0]?.points).toEqual(resized[0].points);
 await expect.poll(async()=>{
  const restored=(await layer(page,chart)).drawings[0];if(!restored||restored.id!==original.id)return Infinity;
  return Math.max(...restored.coordinates.flatMap((coordinate,i)=>[
   Math.abs(coordinate.x-committed.coordinates[i].x),Math.abs(coordinate.y-committed.coordinates[i].y),
  ]));
 },{message:'Restart must restore the saved anchors to their original displayed coordinates after viewport restoration'}).toBeLessThan(1);
 expect((await layer(page,chart)).selectedId).toBeNull();await expect(deleteButton(chart)).toBeDisabled();
});

test('horizontal line endpoint handles shorten the full-width line to a saved finite horizontal segment',async({page})=>{
 await page.goto('/');const chart=page.getByTestId('chart-cell');await expect(chart.getByLabel('OHLCV')).toContainText('61,000.00');
 const original=await place(page,chart,'horizontal'),saved=await savedDrawings(page);expect(saved[0].points).toHaveLength(1);
 await select(page,chart,original);const current=await layer(page,chart),handles=current.selection!.coordinates;
 expect(handles).toHaveLength(2);expect(handles[0].x).toBeLessThan(current.width*.05);expect(handles[1].x).toBeGreaterThan(current.width*.95);
 expect(handles[0].y).toBe(handles[1].y);
 const pointer=await beginEdit(page,chart,0,{x:105,y:-32});await expectPreviewAt(page,chart,0,pointer);
 const preview=(await layer(page,chart)).preview[0];expect(preview.points).toHaveLength(2);
 expect(preview.points[0].time).not.toBe(preview.points[1].time);expect(preview.points[0].price).toBe(preview.points[1].price);
 expect(preview.coordinates[1].x).toBeCloseTo(handles[1].x,0);
 await flush(page);expect(await savedDrawings(page)).toEqual(saved);
 await page.mouse.up();await flush(page);const shortened=await savedDrawings(page);
 expect(shortened[0]).toMatchObject({id:original.id,type:'horizontal',points:preview.points});
 await page.reload();await expect(chart.getByLabel('OHLCV')).toContainText('61,000.00');
 await expect.poll(async()=>(await layer(page,chart)).drawings[0]?.points).toEqual(shortened[0].points);
 const restored=(await layer(page,chart)).drawings[0];expect(restored.coordinates[0].x).toBeGreaterThan(90);
 await select(page,chart,restored);const before=(await layer(page,chart)).selection!.coordinates;
 const nextPointer=await beginEdit(page,chart,1,{x:-75,y:27});await expectPreviewAt(page,chart,1,nextPointer);
 const nextPreview=(await layer(page,chart)).preview[0];expect(nextPreview.points[0].time).toBe(shortened[0].points[0].time);
 expect(nextPreview.points[0].price).toBe(nextPreview.points[1].price);expect(nextPreview.coordinates[0].x).toBeCloseTo(before[0].x,0);
 await page.mouse.up();await flush(page);const resized=await savedDrawings(page);
 expect(resized[0]).toMatchObject({id:original.id,type:'horizontal',points:nextPreview.points});
 await page.reload();await expect.poll(async()=>(await layer(page,chart)).drawings[0]?.points).toEqual(resized[0].points);
 // Resized segments retain the existing double-click movement interaction.
 const finite=(await layer(page,chart)).drawings[0],grab=await clientPoint(page,chart,{
  x:(finite.coordinates[0].x+finite.coordinates[1].x)/2,y:finite.coordinates[0].y,
 });
 await page.mouse.dblclick(grab.x,grab.y);await expect.poll(async()=>(await layer(page,chart)).movingId).toBe(original.id);
 await page.mouse.move(grab.x+24,grab.y-14);const movedPreview=(await layer(page,chart)).preview[0];
 expect(movedPreview.points).toHaveLength(2);expect(movedPreview.points[0].price).toBe(movedPreview.points[1].price);
 for(const [index,coordinate] of movedPreview.coordinates.entries()){
  expect(Math.abs(coordinate.x-finite.coordinates[index].x-24)).toBeLessThan(1.1);
  expect(Math.abs(coordinate.y-finite.coordinates[index].y+14)).toBeLessThan(1.1);
 }
 await page.mouse.click(grab.x+24,grab.y-14);await flush(page);
 expect((await savedDrawings(page))[0]).toMatchObject({id:original.id,type:'horizontal',points:movedPreview.points});
 await page.reload();await expect.poll(async()=>(await layer(page,chart)).drawings[0]?.points).toEqual(movedPreview.points);
});

test('Escape and pointer cancellation discard endpoint previews without saving or leaving chart interactions blocked',async({page})=>{
 await page.goto('/');const chart=page.getByTestId('chart-cell');await expect(chart.getByLabel('OHLCV')).toContainText('61,000.00');
 await place(page,chart,'trendline');const saved=await savedDrawings(page);
 for(const cancellation of ['Escape','pointercancel']){
  await select(page,chart,(await layer(page,chart)).drawings[0]);await beginEdit(page,chart,0,{x:45,y:-20});
  if(cancellation==='Escape')await page.keyboard.press('Escape');
  else await chart.getByTestId('drawing-overlay').dispatchEvent('pointercancel',{pointerId:1});
  await page.mouse.up();await expect.poll(async()=>(await layer(page,chart)).editingId).toBeNull();
  await expect.poll(async()=>(await layer(page,chart)).drawings[0]?.points).toEqual(saved[0].points);
  expect((await layer(page,chart)).preview).toEqual([]);await flush(page);expect(await savedDrawings(page)).toEqual(saved);
 }
 await page.reload();await expect.poll(async()=>(await layer(page,chart)).drawings[0]?.points).toEqual(saved[0].points);
 const before=(await layer(page,chart)).drawings[0],bounds=await canvas(chart);
 await page.mouse.move(bounds.x+bounds.width*.22,bounds.y+bounds.height*.22);await page.mouse.down();
 await page.mouse.move(bounds.x+bounds.width*.28,bounds.y+bounds.height*.22,{steps:8});await page.mouse.up();
 await expect.poll(async()=>Math.abs((await layer(page,chart)).drawings[0].coordinates[0].x-before.coordinates[0].x)).toBeGreaterThan(20);
 await flush(page);expect(await savedDrawings(page)).toEqual(saved);
});

test('dragging a selected line body pans normally and clicking empty space clears selection',async({page})=>{
 await page.goto('/');const chart=page.getByTestId('chart-cell');await expect(chart.getByLabel('OHLCV')).toContainText('61,000.00');
 const original=await place(page,chart,'trendline'),saved=await savedDrawings(page),pointer=await select(page,chart,original);
 await page.mouse.move(pointer.x,pointer.y);await page.mouse.down();await page.mouse.move(pointer.x+60,pointer.y,{steps:8});await page.mouse.up();
 await expect.poll(async()=>Math.abs((await layer(page,chart)).drawings[0].coordinates[0].x-original.coordinates[0].x)).toBeGreaterThan(20);
 expect((await layer(page,chart)).editingId).toBeNull();expect((await layer(page,chart)).movingId).toBeNull();
 await flush(page);expect(await savedDrawings(page)).toEqual(saved);
 const bounds=await canvas(chart);await page.mouse.click(bounds.x+bounds.width*.25,bounds.y+bounds.height*.18);
 expect((await layer(page,chart)).selectedId).toBeNull();expect((await layer(page,chart)).selection).toBeNull();await expect(deleteButton(chart)).toBeDisabled();
 await page.keyboard.press('Delete');await flush(page);expect(await savedDrawings(page)).toEqual(saved);
});

test('only one chart owns selection and keyboard deletion, and tab switches dispose endpoint edits',async({page})=>{
 await page.goto('/');await page.getByRole('button',{name:'Add chart',exact:true}).click();
 const charts=page.getByTestId('chart-cell'),first=charts.first(),second=charts.nth(1);
 await expect(first.getByLabel('OHLCV')).toContainText('61,000.00');await expect(second.getByLabel('OHLCV')).toContainText('61,000.00');
 const firstDrawing=await place(page,first,'trendline'),secondDrawing=await place(page,second,'arrowDown');
 const firstSaved=await savedDrawings(page);await select(page,first,firstDrawing);await select(page,second,secondDrawing);
 expect((await layer(page,first)).selectedId).toBeNull();await expect(deleteButton(first)).toBeDisabled();
 await page.keyboard.press('Delete');await flush(page);expect(await savedDrawings(page,1)).toEqual([]);expect(await savedDrawings(page)).toEqual(firstSaved);
 await select(page,first,(await layer(page,first)).drawings[0]);await beginEdit(page,first,0,{x:35,y:-19});
 const oldIds=(await layers(page)).map(item=>item.id);
 await page.getByRole('button',{name:'Add tab',exact:true}).evaluate(element=>(element as HTMLElement).click());await page.mouse.up();await expect(charts).toHaveCount(1);
 await expect.poll(async()=>(await layers(page)).length).toBe(1);
 expect((await layers(page)).some(item=>oldIds.includes(item.id))).toBe(false);
 expect((await layers(page))[0]).toMatchObject({selectedId:null,editingId:null});
 await page.keyboard.press('Delete');await flush(page);expect(await savedDrawings(page,0,0)).toEqual(firstSaved);
 await page.locator('[role="tab"][data-tab-id]').first().click();await expect(charts).toHaveCount(2);
 await expect.poll(async()=>(await layer(page,first)).drawings[0]?.points).toEqual(firstSaved[0].points);
 expect((await layer(page,first)).selectedId).toBeNull();await expect(deleteButton(first)).toBeDisabled();
 expect((await layer(page,second)).drawings).toEqual([]);
});

test('changing market or timeframe clears line selection and removing a selected chart leaves no keyboard owner',async({page})=>{
 await page.goto('/');const chart=page.getByTestId('chart-cell');await expect(chart.getByLabel('OHLCV')).toContainText('61,000.00');
 const drawing=await place(page,chart,'trendline'),saved=await savedDrawings(page);await select(page,chart,drawing);
 await beginEdit(page,chart,0,{x:24,y:-14});
 // Change scope without a real outside pointerdown, so scope cleanup must cancel the active edit itself.
 await chart.getByRole('button',{name:'Select instrument',exact:true}).evaluate(element=>(element as HTMLElement).click());
 await page.getByLabel('Instruments',{exact:true}).getByRole('button',{name:'BTCUSDT Perpetual',exact:true}).evaluate(element=>(element as HTMLElement).click());
 await page.mouse.up();await page.mouse.move(20,20);
 await expect(chart.getByLabel('OHLCV')).toContainText('62,000.00');
 expect((await layer(page,chart)).selectedId).toBeNull();expect((await layer(page,chart)).editingId).toBeNull();
 await page.keyboard.press('Delete');await flush(page);expect(await savedDrawings(page)).toEqual(saved);
 await chart.getByRole('button',{name:'Select instrument',exact:true}).click();
 await page.getByLabel('Instruments',{exact:true}).getByRole('button',{name:'BTCUSDT Spot',exact:true}).click();
 await expect.poll(async()=>(await layer(page,chart)).drawings[0]?.points).toEqual(saved[0].points);
 await select(page,chart,(await layer(page,chart)).drawings[0]);await beginEdit(page,chart,1,{x:-28,y:17});
 await chart.getByLabel('Timeframe 15m',{exact:true}).evaluate(element=>(element as HTMLElement).click());await page.mouse.up();
 await expect.poll(async()=>(await layer(page,chart)).selectedId).toBeNull();expect((await layer(page,chart)).editingId).toBeNull();
 await page.keyboard.press('Delete');await flush(page);expect(await savedDrawings(page)).toEqual(saved);
 await chart.getByLabel('Timeframe 1h',{exact:true}).click();
 await page.getByRole('button',{name:'Add chart',exact:true}).click();const charts=page.getByTestId('chart-cell'),first=charts.first();
 await select(page,first,(await layer(page,first)).drawings[0]);const removedId=await first.getAttribute('data-chart-id');
 await first.getByRole('button',{name:'Remove chart',exact:true}).click();await expect(charts).toHaveCount(1);
 expect((await layers(page)).some(item=>item.id===removedId)).toBe(false);
 await page.keyboard.press('Delete');await flush(page);expect(await savedDrawings(page)).toEqual([]);await expect(deleteButton(charts)).toBeDisabled();
});

test.describe('endpoint interaction at physical display density 2',()=>{
 test('a trendline handle in a lower grid row with a MACD pane stays under the pointer',async({page})=>{
  await page.goto('/');await page.getByLabel('Grid layout').selectOption('2x1');await page.getByRole('button',{name:'Add chart',exact:true}).click();
  const chart=page.getByTestId('chart-cell').nth(1);await expect(chart.getByLabel('OHLCV')).toContainText('61,000.00');
  await chart.getByLabel('Indicators',{exact:true}).selectOption('macd');
  await page.getByRole('dialog',{name:'MACD parameters',exact:true}).getByRole('button',{name:'OK',exact:true}).click();
  await expect(chart.locator('.indicator-chip')).toHaveCount(1);
  const original=await place(page,chart,'trendline');await select(page,chart,original);
  const pointer=await beginEdit(page,chart,1,{x:-35,y:18});await expectPreviewAt(page,chart,1,pointer);
  const preview=(await layer(page,chart)).preview[0];expect(preview.points[0]).toEqual(original.points[0]);
  await page.mouse.up();await flush(page);expect((await savedDrawings(page,1))[0].points).toEqual(preview.points);
  expect(await savedDrawings(page)).toEqual([]);await page.reload();await expect(chart.locator('.indicator-chip')).toHaveCount(1);
  await expect.poll(async()=>(await layer(page,chart)).drawings[0]?.points).toEqual(preview.points);
 });
});

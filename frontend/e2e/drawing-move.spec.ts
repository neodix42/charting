import {test,expect,type Locator,type Page} from '@playwright/test';
import type {DrawingConfig,DrawingPoint,DrawingType} from '../src/types';

test.use({deviceScaleFactor:2,launchOptions:{args:['--force-device-scale-factor=2']}});

interface Position {x:number;y:number}
interface ProjectedDrawing {id:string;type:DrawingType;points:DrawingPoint[];coordinates:Position[]}
interface LayerDiagnostic {
 id:string;tool:DrawingType|null;movingId:string|null;draft:DrawingPoint[];width:number;height:number;
 drawings:ProjectedDrawing[];preview:ProjectedDrawing[];
}
const moveHint='Move drawing · click to place · Esc cancels';
const tools=(chart:Locator)=>chart.getByRole('group',{name:'Drawing tools',exact:true});
const savedDrawings=(page:Page,chartIndex=0,tabIndex=0):Promise<DrawingConfig[]>=>page.evaluate(({chartIndex,tabIndex})=>(window as any).__testWorkspace().tabs[tabIndex].charts[chartIndex].drawings??[],{chartIndex,tabIndex});
const layers=(page:Page):Promise<LayerDiagnostic[]>=>page.evaluate(async()=>{
 const modulePath='/src/charts/drawings/DrawingLayer.ts';
 const {drawingDiagnostics}=await import(modulePath);
 return [...drawingDiagnostics].map(([id,layer])=>({id,...layer.read()}));
});
async function layer(page:Page,chart:Locator){
 const id=await chart.getAttribute('data-chart-id'),value=(await layers(page)).find(layer=>layer.id===id);
 if(!value)throw Error(`Drawing layer ${id} is unavailable`);return value;
}
async function flush(page:Page){
 await page.locator('.save-indicator').click();
 await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();
}
async function canvas(chart:Locator){
 const bounds=await chart.getByTestId('chart-canvas').locator('canvas').first().boundingBox();
 if(!bounds)throw Error('Price pane canvas is unavailable');return bounds;
}
async function clientPoint(page:Page,chart:Locator,coordinate:Position){
 const bounds=await canvas(chart),current=await layer(page,chart);
 return {x:bounds.x+coordinate.x*bounds.width/current.width,y:bounds.y+coordinate.y*bounds.height/current.height};
}
async function place(page:Page,chart:Locator,type:DrawingType){
 const labels:Record<DrawingType,string>={arrowUp:'Arrow up',arrowDown:'Arrow down',horizontal:'Horizontal line',trendline:'Trendline'};
 await tools(chart).getByRole('button',{name:labels[type],exact:true}).click();
 const bounds=await canvas(chart);
 await page.mouse.click(bounds.x+bounds.width*.62,bounds.y+bounds.height*.56);
 if(type==='trendline')await page.mouse.click(bounds.x+bounds.width*.79,bounds.y+bounds.height*.34);
 await expect.poll(async()=>(await layer(page,chart)).drawings.length).toBe(1);
 await flush(page);return (await layer(page,chart)).drawings[0];
}
async function startMove(page:Page,chart:Locator,drawing:ProjectedDrawing){
 const coordinate=drawing.type==='trendline'?{
  x:(drawing.coordinates[0].x+drawing.coordinates[1].x)/2,
  y:(drawing.coordinates[0].y+drawing.coordinates[1].y)/2,
 }:drawing.type==='arrowUp'||drawing.type==='arrowDown'?{
  x:drawing.coordinates[0].x,y:drawing.coordinates[0].y+(drawing.type==='arrowUp'?16:-16),
 }:drawing.coordinates[0];
 const pointer=await clientPoint(page,chart,coordinate);
 await page.mouse.dblclick(pointer.x,pointer.y);
 await expect.poll(async()=>(await layer(page,chart)).movingId).toBe(drawing.id);
 await expect(chart.getByText(moveHint,{exact:true})).toBeVisible();
 return pointer;
}
async function expectTranslation(page:Page,chart:Locator,original:ProjectedDrawing,delta:Position){
 await expect.poll(async()=>{
  const current=await layer(page,chart),preview=current.preview[0];
  if(!preview||preview.coordinates.length!==original.coordinates.length)return Infinity;
  return Math.max(...preview.coordinates.flatMap((point,index)=>[
   Math.abs(point.y-original.coordinates[index].y-delta.y),
   ...(original.type==='horizontal'?[]:[Math.abs(point.x-original.coordinates[index].x-delta.x)]),
  ]));
 },{message:'The entire drawing must move by the pointer delta without snapping an endpoint to the grab point'}).toBeLessThan(1.1);
}

for(const type of ['arrowUp','arrowDown','horizontal','trendline'] as const){
 test(`double-click moves a ${type} without jumping and saves its existing identity and anchors across restart`,async({page})=>{
  const errors:string[]=[];page.on('pageerror',error=>errors.push(error.message));
  await page.goto('/');const chart=page.getByTestId('chart-cell');await expect(chart.getByLabel('OHLCV')).toContainText('61,000.00');
  const original=await place(page,chart,type),saved=await savedDrawings(page),pointer=await startMove(page,chart,original);
  await expectTranslation(page,chart,original,{x:0,y:0});
  expect((await layer(page,chart)).drawings).toEqual([]);
  const delta={x:64,y:-36};await page.mouse.move(pointer.x+delta.x,pointer.y+delta.y);
  await expectTranslation(page,chart,original,delta);
  // Explicit save during the preview must retain the committed drawing, including after autosave work.
  await flush(page);expect(await savedDrawings(page)).toEqual(saved);
  expect((await layer(page,chart)).movingId).toBe(original.id);
  await page.mouse.click(pointer.x+delta.x,pointer.y+delta.y);
  await expect.poll(async()=>(await layer(page,chart)).movingId).toBeNull();
  await expect(chart.getByText(moveHint,{exact:true})).toHaveCount(0);
  await flush(page);const moved=await savedDrawings(page);
  expect(moved).toHaveLength(1);expect(moved[0]).toMatchObject({id:saved[0].id,type,exchange:saved[0].exchange,market:saved[0].market,instrument:saved[0].instrument});
  expect(moved[0].points).not.toEqual(saved[0].points);
  expect(moved[0].points).toHaveLength(type==='trendline'?2:1);
  for(const [index,point] of moved[0].points.entries()){
   expect(point.price).not.toBe(saved[0].points[index].price);
   if(type!=='horizontal')expect(point.time).not.toBe(saved[0].points[index].time);
  }
  const committed=(await layer(page,chart)).drawings[0];
  await page.reload();await expect(chart.getByLabel('OHLCV')).toContainText('61,000.00');
  await expect.poll(async()=>(await layer(page,chart)).drawings.map(({id,type,points})=>({id,type,points}))).toEqual([{id:moved[0].id,type,points:moved[0].points}]);
  // Anchors and OHLC arrive before the chart has measured its price axis and
  // restored the live right gap. Verify the rendered geometry once that layout
  // settles, retaining the same subpixel tolerance on every saved anchor.
  await expect.poll(async()=>{
   const restored=(await layer(page,chart)).drawings[0];
   if(!restored||restored.coordinates.length!==committed.coordinates.length)return Infinity;
   return Math.max(...restored.coordinates.flatMap((coordinate,index)=>[
    Math.abs(coordinate.x-committed.coordinates[index].x),
    Math.abs(coordinate.y-committed.coordinates[index].y),
   ]));
  },{message:'Restart must restore every drawing anchor within one pixel after chart layout'}).toBeLessThan(1);
  expect(await savedDrawings(page)).toEqual(moved);expect(errors).toEqual([]);
 });
}

test('Escape, Cursor, and choosing another drawing tool cancel movement without saving a preview',async({page})=>{
 await page.goto('/');const chart=page.getByTestId('chart-cell');await expect(chart.getByLabel('OHLCV')).toContainText('61,000.00');
 await place(page,chart,'trendline');const saved=await savedDrawings(page);
 for(const cancel of ['Escape','Cursor','Arrow down']){
  const original=(await layer(page,chart)).drawings[0],pointer=await startMove(page,chart,original);
  await page.mouse.move(pointer.x+48,pointer.y-25);await expectTranslation(page,chart,original,{x:48,y:-25});
  if(cancel==='Escape')await page.keyboard.press('Escape');else await tools(chart).getByRole('button',{name:cancel,exact:true}).click();
  await expect.poll(async()=>(await layer(page,chart)).movingId).toBeNull();
  await expect.poll(async()=>(await layer(page,chart)).drawings[0]?.points).toEqual(saved[0].points);
  await expect(chart.getByText(moveHint,{exact:true})).toHaveCount(0);
  await flush(page);expect(await savedDrawings(page)).toEqual(saved);
  await page.keyboard.press('Escape');
 }
 await page.reload();await expect.poll(async()=>(await layer(page,chart)).drawings[0]?.points).toEqual(saved[0].points);
});

test('ordinary dragging pans the chart and a double-click away from a drawing never selects it',async({page})=>{
 await page.goto('/');const chart=page.getByTestId('chart-cell');await expect(chart.getByLabel('OHLCV')).toContainText('61,000.00');
 const original=await place(page,chart,'arrowUp'),saved=await savedDrawings(page),pointer=await clientPoint(page,chart,original.coordinates[0]);
 await page.mouse.move(pointer.x,pointer.y+12);await page.mouse.down();await page.mouse.move(pointer.x+62,pointer.y+12,{steps:8});await page.mouse.up();
 await expect.poll(async()=>Math.abs((await layer(page,chart)).drawings[0].coordinates[0].x-original.coordinates[0].x)).toBeGreaterThan(20);
 expect((await layer(page,chart)).movingId).toBeNull();
 const bounds=await canvas(chart);await page.mouse.dblclick(bounds.x+bounds.width*.23,bounds.y+bounds.height*.2);
 expect((await layer(page,chart)).movingId).toBeNull();await flush(page);expect(await savedDrawings(page)).toEqual(saved);
 await expect(chart.getByText(moveHint,{exact:true})).toHaveCount(0);
});

test('a new live candle preserves an active move and only placing commits its final anchors',async({page})=>{
 await page.goto('/');const chart=page.getByTestId('chart-cell');await expect(chart.getByLabel('OHLCV')).toContainText('61,000.00');
 const original=await place(page,chart,'arrowUp'),saved=await savedDrawings(page),pointer=await startMove(page,chart,original);
 const latest=await page.evaluate(()=>(window as any).__testChartGaps()[0].latestTime);
 await page.evaluate(()=>(window as any).__testAdvanceBars());
 await expect.poll(()=>page.evaluate(()=>(window as any).__testChartGaps()[0].latestTime)).toBeGreaterThan(latest);
 expect((await layer(page,chart)).movingId).toBe(original.id);await flush(page);expect(await savedDrawings(page)).toEqual(saved);
 await page.mouse.move(pointer.x+46,pointer.y-22);
 await expect.poll(async()=>{
  const preview=(await layer(page,chart)).preview[0];if(!preview)return Infinity;
  const tip=await clientPoint(page,chart,preview.coordinates[0]);
  return Math.max(Math.abs(tip.x-(pointer.x+46)),Math.abs(tip.y-(pointer.y-22-16)));
 }).toBeLessThan(1.1);
 const preview=(await layer(page,chart)).preview[0];
 await page.mouse.click(pointer.x+46,pointer.y-22);await flush(page);
 expect((await savedDrawings(page))[0]).toMatchObject({id:original.id,points:preview.points});
 expect((await layer(page,chart)).movingId).toBeNull();
});

test('a move affects only its chart and leaving a tab discards its unplaced preview and drawing layer',async({page})=>{
 await page.goto('/');await page.getByRole('button',{name:'Add chart',exact:true}).click();
 const charts=page.getByTestId('chart-cell'),first=charts.first(),second=charts.nth(1);
 await expect(first.getByLabel('OHLCV')).toContainText('61,000.00');await expect(second.getByLabel('OHLCV')).toContainText('61,000.00');
 await place(page,first,'arrowUp');await place(page,second,'horizontal');
 const untouched=await savedDrawings(page,1),original=(await layer(page,first)).drawings[0],pointer=await startMove(page,first,original);
 await page.mouse.click(pointer.x+28,pointer.y-18);await flush(page);
 const moved=await savedDrawings(page);expect(moved[0].points).not.toEqual(original.points);expect(await savedDrawings(page,1)).toEqual(untouched);
 const selected=(await layer(page,first)).drawings[0],nextPointer=await startMove(page,first,selected);
 await page.mouse.move(nextPointer.x+25,nextPointer.y-16);await expectTranslation(page,first,selected,{x:25,y:-16});
 const oldIds=(await layers(page)).map(layer=>layer.id);
 await page.getByRole('button',{name:'Add tab',exact:true}).click();await expect(charts).toHaveCount(1);
 await expect.poll(async()=>(await layers(page)).length).toBe(1);
 expect((await layers(page)).some(layer=>oldIds.includes(layer.id))).toBe(false);
 expect((await layers(page))[0].movingId).toBeNull();await flush(page);
 expect(await savedDrawings(page,0,0)).toEqual(moved);expect(await savedDrawings(page,1,0)).toEqual(untouched);expect(await savedDrawings(page,0,1)).toEqual([]);
 await page.locator('[role="tab"][data-tab-id]').first().click();await expect(charts).toHaveCount(2);
 await expect.poll(async()=>(await layer(page,first)).drawings[0]?.points).toEqual(moved[0].points);
 expect((await layer(page,first)).movingId).toBeNull();expect((await layer(page,second)).drawings[0].points).toEqual(untouched[0].points);
});

test('instrument and timeframe changes cancel movement and preserve the drawing in its original market scope',async({page})=>{
 await page.goto('/');const chart=page.getByTestId('chart-cell');await expect(chart.getByLabel('OHLCV')).toContainText('61,000.00');
 await place(page,chart,'arrowDown');const saved=await savedDrawings(page),original=(await layer(page,chart)).drawings[0],pointer=await startMove(page,chart,original);
 await page.mouse.move(pointer.x+42,pointer.y-28);
 await chart.getByRole('button',{name:'Select instrument',exact:true}).click();
 await page.getByLabel('Instruments',{exact:true}).getByRole('button',{name:'BTCUSDT Perpetual',exact:true}).click();
 await expect(chart.getByLabel('OHLCV')).toContainText('62,000.00');
 await expect.poll(async()=>(await layer(page,chart)).movingId).toBeNull();expect((await layer(page,chart)).drawings).toEqual([]);
 await flush(page);expect(await savedDrawings(page)).toEqual(saved);
 await chart.getByRole('button',{name:'Select instrument',exact:true}).click();
 await page.getByLabel('Instruments',{exact:true}).getByRole('button',{name:'BTCUSDT Spot',exact:true}).click();
 await expect.poll(async()=>(await layer(page,chart)).drawings[0]?.points).toEqual(saved[0].points);
 const restored=(await layer(page,chart)).drawings[0],nextPointer=await startMove(page,chart,restored);
 await page.mouse.move(nextPointer.x+36,nextPointer.y+18);await chart.getByLabel('Timeframe 15m',{exact:true}).click();
 await expect.poll(async()=>(await layer(page,chart)).movingId).toBeNull();await flush(page);expect(await savedDrawings(page)).toEqual(saved);
 await expect(chart.getByText(moveHint,{exact:true})).toHaveCount(0);
});

test.describe('drawing movement through transformed price panes',()=>{
 test('a midpoint grab translates a trendline in a logarithmic line chart with a MACD pane',async({page})=>{
  await page.goto('/');const chart=page.getByTestId('chart-cell');await expect(chart.getByLabel('OHLCV')).toContainText('61,000.00');
  await chart.getByRole('button',{name:'Chart style',exact:true}).click();await chart.getByRole('button',{name:'Toggle logarithmic scale',exact:true}).click();
  await chart.getByLabel('Indicators',{exact:true}).selectOption('macd');
  await page.getByRole('dialog',{name:'MACD parameters',exact:true}).getByRole('button',{name:'OK',exact:true}).click();
  await expect(chart.locator('.indicator-chip')).toHaveCount(1);
  const original=await place(page,chart,'trendline'),pointer=await startMove(page,chart,original);
  await expectTranslation(page,chart,original,{x:0,y:0});await page.mouse.move(pointer.x+45,pointer.y-24);
  await expectTranslation(page,chart,original,{x:45,y:-24});await page.mouse.click(pointer.x+45,pointer.y-24);await flush(page);
  const moved=await savedDrawings(page);expect(moved).toHaveLength(1);expect(moved[0].id).toBe(original.id);
  await page.reload();await expect(chart.locator('.indicator-chip')).toHaveCount(1);
  await expect.poll(async()=>(await layer(page,chart)).drawings[0]?.points).toEqual(moved[0].points);
 });
});

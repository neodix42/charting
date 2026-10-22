import {test,expect,type Locator,type Page} from '@playwright/test';

type DrawingType='arrowUp'|'arrowDown'|'horizontal'|'trendline';
interface DrawingPoint {time:number;price:number}
interface Drawing {id:string;type:DrawingType;exchange:string;market:string;instrument:string;points:DrawingPoint[]}
type ProjectedDrawing=Pick<Drawing,'id'|'type'|'points'>&{coordinates:Array<{x:number;y:number}>};
interface LayerDiagnostic {
 id:string;tool:DrawingType|null;draft:DrawingPoint[];width:number;height:number;
 drawings:ProjectedDrawing[];preview:ProjectedDrawing[];
}

const savedDrawings=(page:Page,chartIndex=0,tabIndex=0):Promise<Drawing[]>=>page.evaluate(({chartIndex,tabIndex})=>(window as any).__testWorkspace().tabs[tabIndex].charts[chartIndex].drawings??[],{chartIndex,tabIndex});
const layers=(page:Page):Promise<LayerDiagnostic[]>=>page.evaluate(async()=>{
 const modulePath='/src/charts/drawings/DrawingLayer.ts';
 const {drawingDiagnostics}=await import(modulePath);
 return [...drawingDiagnostics].map(([id,layer])=>({id,...layer.read()}));
});
const attachments=(page:Page):Promise<number>=>page.evaluate(()=>(window as any).__testAttachCount());
const tools=(chart:Locator)=>chart.getByRole('group',{name:'Drawing tools',exact:true});
async function clickPlot(chart:Locator,x=.82,y=.4){
 const canvas=chart.getByTestId('chart-canvas'),bounds=await canvas.boundingBox();
 if(!bounds)throw Error('Chart plot is unavailable');
 await canvas.click({position:{x:Math.floor(bounds.width*x),y:Math.floor(bounds.height*y)}});
}
async function place(chart:Locator,label:string,x=.82,y=.4){
 await tools(chart).getByRole('button',{name:label,exact:true}).click();
 await expect(tools(chart).getByRole('button',{name:label,exact:true})).toHaveAttribute('aria-pressed','true');
 await clickPlot(chart,x,y);
}
async function flush(page:Page){
 await page.locator('.save-indicator').click();
 await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();
}

/** Compare media coordinates with the actual price-pane canvas, independently of the drawing overlay. */
async function expectPointerPosition(page:Page,chart:Locator,kind:'preview'|'drawings',drawingIndex:number,pointIndex:number,client:{x:number;y:number}){
 const id=await chart.getAttribute('data-chart-id');
 await expect.poll(async()=>{
  const layer=(await layers(page)).find(layer=>layer.id===id),coordinate=layer?.[kind]?.[drawingIndex]?.coordinates[pointIndex];
  const canvas=await chart.getByTestId('chart-canvas').locator('canvas').first().boundingBox();
  return layer&&coordinate&&canvas?Math.max(Math.abs(canvas.x+coordinate.x*canvas.width/layer.width-client.x),Math.abs(canvas.y+coordinate.y*canvas.height/layer.height-client.y)):Infinity;
 },{message:`${kind} anchor ${pointIndex+1} must be under the pointer at viewport (${client.x}, ${client.y})`}).toBeLessThanOrEqual(1);
}
async function drawAtPointer(page:Page,chart:Locator){
 const id=await chart.getAttribute('data-chart-id'),expected:Array<{type:DrawingType;positions:Array<{x:number;y:number}>}>=[];
 const cases:Array<{type:DrawingType;label:string;ys:number[]}>= [
  {type:'arrowUp',label:'Arrow up',ys:[.64]},{type:'arrowDown',label:'Arrow down',ys:[.32]},
  {type:'horizontal',label:'Horizontal line',ys:[.47]},{type:'trendline',label:'Trendline',ys:[.71,.26]},
 ];
 for(const [drawingIndex,{type,label,ys}] of cases.entries()){
  await tools(chart).getByRole('button',{name:label,exact:true}).click();
  await expect(tools(chart).getByRole('button',{name:label,exact:true})).toHaveAttribute('aria-pressed','true');
  const positions:Array<{x:number;y:number}>=[];
  for(const [pointIndex,fraction] of ys.entries()){
   const canvas=await chart.getByTestId('chart-canvas').locator('canvas').first().boundingBox();
   if(!canvas)throw Error('Price pane is unavailable');
   // Fractional native pointer coordinates also catch integer rounding in the event-to-price conversion.
   const x=canvas.x+Math.floor(canvas.width*(pointIndex?.88:.78))+.375,y=canvas.y+Math.floor(canvas.height*fraction)+.625;
   positions.push({x,y});await page.mouse.move(x,y);
   await expectPointerPosition(page,chart,'preview',0,pointIndex,{x,y});
   if(pointIndex)await expectPointerPosition(page,chart,'preview',0,0,positions[0]);
   await page.mouse.click(x,y);
   if(type==='trendline'&&!pointIndex)await expect.poll(async()=>(await layers(page)).find(layer=>layer.id===id)?.draft.length).toBe(1);
  }
  await expect.poll(async()=>(await layers(page)).find(layer=>layer.id===id)?.drawings.length).toBe(drawingIndex+1);
  for(const [pointIndex,position] of positions.entries())await expectPointerPosition(page,chart,'drawings',drawingIndex,pointIndex,position);
  expected.push({type,positions});
 }
 return expected;
}

test('drawing previews and committed arrow tips and line anchors stay at the pointer position',async({page})=>{
 await page.goto('/');const chart=page.getByTestId('chart-cell');await expect(chart.getByLabel('OHLCV')).toContainText('61,000.00');
 const expected=await drawAtPointer(page,chart);await flush(page);const saved=await savedDrawings(page);
 expect(saved.map(drawing=>drawing.type)).toEqual(expected.map(drawing=>drawing.type));
 await page.reload();await expect(chart.getByLabel('OHLCV')).toContainText('61,000.00');
 for(const [drawingIndex,drawing] of expected.entries())for(const [pointIndex,position] of drawing.positions.entries())await expectPointerPosition(page,chart,'drawings',drawingIndex,pointIndex,position);
 expect(await savedDrawings(page)).toEqual(saved);
});

test.describe('high-density chart drawing pointer alignment',()=>{
 test.use({deviceScaleFactor:2});
 test('a lower grid row with multiple MACD panes keeps previews and saved drawings under the pointer',async({page})=>{
  await page.goto('/');await page.getByLabel('Grid layout').selectOption('2x1');await page.getByRole('button',{name:'Add chart',exact:true}).click();
  const charts=page.getByTestId('chart-cell'),chart=charts.nth(1);await expect(charts).toHaveCount(2);await expect(chart.getByLabel('OHLCV')).toContainText('61,000.00');
  for(const fast of ['12','8']){
   await chart.getByLabel('Indicators',{exact:true}).selectOption('macd');const dialog=page.getByRole('dialog',{name:'MACD parameters',exact:true});
   await dialog.getByLabel('Fast period',{exact:true}).fill(fast);await dialog.getByRole('button',{name:'OK',exact:true}).click();
  }
  await expect(chart.locator('.indicator-chip')).toHaveCount(2);
  const geometry=await chart.getByTestId('chart-canvas').locator('canvas').first().evaluate(canvas=>({devicePixelRatio:window.devicePixelRatio,bitmapHeight:(canvas as HTMLCanvasElement).height,cssHeight:canvas.getBoundingClientRect().height,top:canvas.getBoundingClientRect().top}));
  expect(geometry.devicePixelRatio).toBe(2);expect(geometry.bitmapHeight).toBe(geometry.cssHeight*2);expect(geometry.top).toBeGreaterThan(500);
  const expected=await drawAtPointer(page,chart);await flush(page);const saved=await savedDrawings(page,1);
  expect(await savedDrawings(page,0)).toEqual([]);await page.reload();await expect(chart.locator('.indicator-chip')).toHaveCount(2);
  for(const [drawingIndex,drawing] of expected.entries())for(const [pointIndex,position] of drawing.positions.entries())await expectPointerPosition(page,chart,'drawings',drawingIndex,pointIndex,position);
  expect(await savedDrawings(page,1)).toEqual(saved);
 });
});

test('each chart draws both arrows, a horizontal line and a two-point trendline, and restores their price/time anchors',async({page})=>{
 const errors:string[]=[];page.on('pageerror',error=>errors.push(error.message));
 await page.goto('/');const chart=page.getByTestId('chart-cell');
 await expect(chart.getByLabel('OHLCV')).toContainText('61,000.00');
 await expect(tools(chart)).toBeVisible();
 await expect(tools(chart).getByRole('button',{name:'Cursor',exact:true})).toHaveAttribute('aria-pressed','true');
 const count=await attachments(page);
 await place(chart,'Arrow up',.8,.55);
 await place(chart,'Arrow down',.88,.3);
 await place(chart,'Horizontal line',.85,.45);
 await place(chart,'Trendline',.77,.55);
 await expect.poll(async()=>(await layers(page))[0]?.draft.length).toBe(1);
 await flush(page);expect(await savedDrawings(page)).toHaveLength(3);
 await clickPlot(chart,.9,.3);
 await expect.poll(async()=>(await layers(page))[0]?.drawings.length).toBe(4);
 await flush(page);const saved=await savedDrawings(page);
 expect(saved.map(drawing=>drawing.type)).toEqual(['arrowUp','arrowDown','horizontal','trendline']);
 expect(new Set(saved.map(drawing=>drawing.id)).size).toBe(4);
 for(const drawing of saved){
  expect(drawing).toMatchObject({exchange:'binance',market:'spot',instrument:'BTCUSDT'});
  expect(drawing.points).toHaveLength(drawing.type==='trendline'?2:1);
  for(const point of drawing.points){expect(Number.isInteger(point.time)).toBe(true);expect(point.time).toBeGreaterThan(0);expect(Number.isFinite(point.price)).toBe(true);expect(point.price).toBeGreaterThan(0);}
 }
 expect(saved[3].points[0].time).not.toBe(saved[3].points[1].time);
 expect(saved[3].points[0].price).not.toBe(saved[3].points[1].price);
 expect(await attachments(page)).toBe(count);
 await page.reload();await expect(page.getByLabel('OHLCV')).toContainText('61,000.00');
 await expect.poll(async()=>(await layers(page))[0]?.drawings.map(({id,type,points})=>({id,type,points}))).toEqual(saved.map(({id,type,points})=>({id,type,points})));
 expect(await savedDrawings(page)).toEqual(saved);
 await page.screenshot({path:'test-results/chart-drawings-restored.png'});
 expect(errors).toEqual([]);
});

test('Escape cancels an unfinished trendline and returns the chart to ordinary cursor interaction',async({page})=>{
 await page.goto('/');const chart=page.getByTestId('chart-cell');await expect(chart.getByLabel('OHLCV')).toContainText('61,000.00');
 const count=await attachments(page);
 await place(chart,'Trendline',.8,.5);
 await expect.poll(async()=>(await layers(page))[0]?.draft.length).toBe(1);
 await page.keyboard.press('Escape');
 await expect(tools(chart).getByRole('button',{name:'Cursor',exact:true})).toHaveAttribute('aria-pressed','true');
 await expect.poll(async()=>(await layers(page))[0]?.draft).toEqual([]);
 await clickPlot(chart,.9,.3);await flush(page);
 expect(await savedDrawings(page)).toEqual([]);
 await expect.poll(async()=>(await layers(page))[0]?.drawings).toEqual([]);
 expect(await attachments(page)).toBe(count);
 await place(chart,'Arrow up');await flush(page);expect(await savedDrawings(page)).toHaveLength(1);
 await tools(chart).getByRole('button',{name:'Undo drawing',exact:true}).click();await flush(page);
 expect(await savedDrawings(page)).toEqual([]);
});

test('drawings remain independent per chart, and tab switches dispose hidden drawing layers and market consumers',async({page})=>{
 const errors:string[]=[];page.on('pageerror',error=>errors.push(error.message));
 await page.goto('/');await page.getByRole('button',{name:'Add chart',exact:true}).click();
 const charts=page.getByTestId('chart-cell'),first=charts.first(),second=charts.nth(1);
 await expect(first.getByLabel('OHLCV')).toContainText('61,000.00');await expect(second.getByLabel('OHLCV')).toContainText('61,000.00');
 const originalIds=await charts.evaluateAll(elements=>elements.map(element=>element.getAttribute('data-chart-id')));
 const count=await attachments(page);
 await place(first,'Arrow up');await place(second,'Arrow down');await flush(page);
 const firstSaved=await savedDrawings(page),secondSaved=await savedDrawings(page,1);
 expect(firstSaved.map(drawing=>drawing.type)).toEqual(['arrowUp']);expect(secondSaved.map(drawing=>drawing.type)).toEqual(['arrowDown']);
 expect(await attachments(page)).toBe(count);
 await page.getByRole('button',{name:'Add tab',exact:true}).click();
 await expect(charts).toHaveCount(1);await expect.poll(()=>page.evaluate(()=>window.__cryptodeskDiagnostics?.())).toMatchObject({chartInstances:1,consumers:1,savedTabs:2,savedCharts:3});
 await expect.poll(()=>page.evaluate(()=>(window as any).__testLeases())).toBe(1);
 await expect.poll(async()=>(await layers(page)).length).toBe(1);
 expect((await layers(page)).some(layer=>originalIds.includes(layer.id))).toBe(false);
 await expect(charts.getByLabel('OHLCV')).toContainText('61,000.00');
 await expect.poll(async()=>(await layers(page))[0]?.drawings).toEqual([]);
 await place(charts,'Horizontal line');await flush(page);const newTabDrawings=await savedDrawings(page,0,1);
 await page.getByRole('tab').first().click();await expect(charts).toHaveCount(2);
 await expect.poll(async()=>(await layers(page)).map(layer=>layer.id).sort()).toEqual(originalIds.toSorted());
 await expect.poll(()=>page.evaluate(()=>(window as any).__testLeases())).toBe(2);
 expect(await savedDrawings(page)).toEqual(firstSaved);expect(await savedDrawings(page,1)).toEqual(secondSaved);
 await tools(first).getByRole('button',{name:'Clear drawings',exact:true}).click();await flush(page);
 expect(await savedDrawings(page)).toEqual([]);expect(await savedDrawings(page,1)).toEqual(secondSaved);
 await page.reload();await expect(charts).toHaveCount(2);
 await expect.poll(async()=>(await layers(page)).map(layer=>layer.drawings.length)).toEqual([0,1]);
 await page.getByRole('tab').nth(1).click();
 await expect.poll(async()=>(await layers(page))[0]?.drawings.map(drawing=>drawing.id)).toEqual(newTabDrawings.map(drawing=>drawing.id));
 expect(errors).toEqual([]);
});

test('drawings follow time and price through zoom and resize while market changes preserve their original scope',async({page})=>{
 const errors:string[]=[];page.on('pageerror',error=>errors.push(error.message));
 await page.goto('/');const chart=page.getByTestId('chart-cell');await expect(chart.getByLabel('OHLCV')).toContainText('61,000.00');
 await place(chart,'Arrow up',.88,.4);await place(chart,'Trendline',.84,.5);await clickPlot(chart,.9,.3);await flush(page);
 const saved=await savedDrawings(page),before=(await layers(page))[0],count=await attachments(page);
 expect(before.drawings).toHaveLength(2);
 expect(before.drawings.every(drawing=>drawing.coordinates.length===drawing.points.length)).toBe(true);
 await page.evaluate(id=>(window as any).__testChartZoom(id,12),before.id);
 await expect.poll(async()=>(await layers(page))[0]?.drawings[0]?.coordinates[0].x).not.toBe(before.drawings[0].coordinates[0].x);
 const zoomed=(await layers(page))[0];
 expect(zoomed.drawings.map(drawing=>drawing.points)).toEqual(saved.map(drawing=>drawing.points));
 await page.setViewportSize({width:1200,height:800});
 await expect.poll(async()=>(await layers(page))[0]?.width).toBeLessThan(zoomed.width);
 await expect.poll(async()=>(await layers(page))[0]?.drawings[0]?.coordinates[0].x).not.toBe(zoomed.drawings[0].coordinates[0].x);
 const resized=(await layers(page))[0];
 expect(resized.drawings.map(drawing=>drawing.points)).toEqual(saved.map(drawing=>drawing.points));
 for(const drawing of resized.drawings)for(const coordinate of drawing.coordinates){expect(Number.isFinite(coordinate.x)).toBe(true);expect(Number.isFinite(coordinate.y)).toBe(true);}
 await chart.getByRole('button',{name:'Chart style',exact:true}).click();
 await expect.poll(async()=>(await layers(page))[0]?.drawings.map(drawing=>drawing.id)).toEqual(saved.map(drawing=>drawing.id));
 await flush(page);expect(await savedDrawings(page)).toEqual(saved);expect(await attachments(page)).toBe(count);
 await chart.getByRole('button',{name:'Select instrument',exact:true}).click();
 await page.getByLabel('Instruments',{exact:true}).getByRole('button',{name:'BTCUSDT Perpetual',exact:true}).click();
 await expect(chart.getByLabel('OHLCV')).toContainText('62,000.00');
 await expect.poll(async()=>(await layers(page))[0]?.drawings).toEqual([]);
 await flush(page);expect(await savedDrawings(page)).toEqual(saved);
 await place(chart,'Arrow down',.87,.35);await flush(page);const combined=await savedDrawings(page);
 expect(combined).toHaveLength(3);expect(combined[2]).toMatchObject({type:'arrowDown',market:'perpetual',instrument:'BTCUSDT'});
 await chart.getByRole('button',{name:'Select instrument',exact:true}).click();
 await page.getByLabel('Instruments',{exact:true}).getByRole('button',{name:'BTCUSDT Spot',exact:true}).click();
 await expect(chart.getByLabel('OHLCV')).toContainText('61,000.00');
 await expect.poll(async()=>(await layers(page))[0]?.drawings.map(drawing=>drawing.id)).toEqual(saved.map(drawing=>drawing.id));
 await flush(page);expect(await savedDrawings(page)).toEqual(combined);
 await page.reload();await expect.poll(async()=>(await layers(page))[0]?.drawings.map(drawing=>drawing.id)).toEqual(saved.map(drawing=>drawing.id));
 expect(errors).toEqual([]);
});

test('a line chart accepts drawings in future whitespace and keeps their anchors when a new candle arrives',async({page})=>{
 const errors:string[]=[];page.on('pageerror',error=>errors.push(error.message));
 await page.goto('/');const chart=page.getByTestId('chart-cell');await expect(chart.getByLabel('OHLCV')).toContainText('61,000.00');
 await chart.getByRole('button',{name:'Chart style',exact:true}).click();
 const layer=(await layers(page))[0],geometry=await page.evaluate(()=>(window as any).__testChartGaps()[0]);
 const canvas=chart.getByTestId('chart-canvas'),futureX=Math.min(layer.width-5,geometry.lastX+30),y=Math.floor(layer.height*.4);
 expect(futureX).toBeGreaterThan(geometry.lastX+5);
 const count=await attachments(page);
 await tools(chart).getByRole('button',{name:'Arrow down',exact:true}).click();
 await canvas.click({position:{x:Math.floor(futureX),y}});await flush(page);
 const saved=await savedDrawings(page);expect(saved).toHaveLength(1);expect(saved[0].type).toBe('arrowDown');
 expect(saved[0].points[0].time).toBeGreaterThan(geometry.latestTime);
 const before=(await layers(page))[0].drawings[0].coordinates[0];expect(Math.abs(before.x-futureX)).toBeLessThan(4);
 await page.evaluate(()=>(window as any).__testAdvanceBars());
 await expect.poll(()=>page.evaluate(()=>(window as any).__testChartGaps()[0].latestTime)).toBeGreaterThan(geometry.latestTime);
 await expect.poll(async()=>(await layers(page))[0]?.drawings[0]?.coordinates[0].x).toBeLessThan(before.x);
 await flush(page);expect(await savedDrawings(page)).toEqual(saved);expect(await attachments(page)).toBe(count);
 await page.reload();await expect(page.getByLabel('OHLCV')).toContainText('61,000.00');
 await expect.poll(async()=>(await layers(page))[0]?.drawings[0]?.points).toEqual(saved[0].points);
 expect(errors).toEqual([]);
});

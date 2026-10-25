import {test,expect,type Locator,type Page} from '@playwright/test';
import type {ChartConfig} from '../src/types';

const chartAt=(page:Page,index=0)=>page.getByTestId('chart-cell').nth(index);
const savedChart=(page:Page,index=0):Promise<ChartConfig>=>page.evaluate(index=>(window as any).__testWorkspace().tabs[0].charts[index],index);
const separator=(chart:Locator,index=0)=>chart.locator('.tv-lightweight-charts > table > tr > td[colspan="3"] > div[style*="position: absolute"][style*="row-resize"]').nth(index);
const paneHeights=(chart:Locator):Promise<number[]>=>chart.getByTestId('chart-canvas').evaluate(element=>{
 const table=element.querySelector('.tv-lightweight-charts > table') as HTMLTableElement|null;
 return table?Array.from(table.rows).filter(row=>row.querySelector('canvas')).slice(0,-1).map(row=>row.getBoundingClientRect().height):[];
});
async function addMACD(page:Page,chart:Locator,fast?:string){
 await chart.getByLabel('Indicators',{exact:true}).selectOption('macd');
 const dialog=page.getByRole('dialog',{name:'MACD parameters',exact:true});
 if(fast)await dialog.getByLabel('Fast period',{exact:true}).fill(fast);
 await dialog.getByRole('button',{name:'OK',exact:true}).click();
}
async function dragSeparator(page:Page,chart:Locator,index:number,delta:number){
 const bounds=await separator(chart,index).boundingBox();expect(bounds).not.toBeNull();
 await page.mouse.move(bounds!.x+bounds!.width/2,bounds!.y+bounds!.height/2);
 await page.mouse.down();
 await page.mouse.move(bounds!.x+bounds!.width/2,bounds!.y+bounds!.height/2+delta,{steps:12});
 await page.mouse.up();
}
async function expectPaneRatio(chart:Locator,index:number,ratio:number){
 await expect.poll(async()=>{
  const heights=await paneHeights(chart);
  return heights.length>index+1&&Math.abs(heights[index+1]/heights[0]-ratio)<.025;
 }).toBe(true);
}

test('healthy public-data details stay in the footer while the crosshair is over future cloud space',async({page})=>{
 await page.goto('/');const chart=chartAt(page);
 await expect(chart.getByLabel('OHLCV')).toContainText('C');
 const reason='Public spot data; current bucket can change until closed';
 await page.evaluate(reason=>(window as any).__testSetHealth({state:'live',reason}),reason);
 await expect(chart.locator('.chart-status')).toContainText(reason);
 await chart.getByLabel('Indicators',{exact:true}).selectOption('ichimoku');
 await page.getByRole('dialog',{name:'Ichimoku Cloud parameters',exact:true}).getByRole('button',{name:'OK',exact:true}).click();
 await expect(chart.locator('.chart-message')).toHaveCount(0);
 const position=await page.evaluate(()=>{
  const geometry=(window as any).__testChartGaps()[0],host=document.querySelector('[data-testid="chart-canvas"]')!.getBoundingClientRect();
  return{x:host.x+geometry.lastX+geometry.barSpacing*4,y:host.y+host.height*.4};
 });
 await page.mouse.move(position.x,position.y);
 await expect(chart.getByLabel('OHLCV')).toContainText('awaiting public market data');
 await expect(chart.locator('.chart-message')).toHaveCount(0);
 await expect(chart.locator('.chart-status')).toContainText('live');
 await expect(chart.locator('.chart-status')).toContainText(reason);
 await page.evaluate(()=>(window as any).__testSetHealth({state:'provisional',reason:'Current candle is provisional'}));
 await expect(chart.locator('.chart-status')).toContainText('Current candle is provisional');
 await expect(chart.locator('.chart-message')).toHaveCount(0);
});

test('loading and failed feeds keep their central connection and retry messages',async({page})=>{
 await page.goto('/');const chart=chartAt(page);await expect(chart.getByLabel('OHLCV')).toContainText('C');
 await page.evaluate(()=>{window.go!.main!.App!.AttachChart=async()=>new Promise((_resolve,reject)=>{
  (window as any).__rejectPaneStatusAttach=()=>reject(Error('Explicit public feed connection failure'));
 });});
 await chart.getByRole('button',{name:'Timeframe 1m',exact:true}).click();
 await expect(chart.locator('.chart-message')).toContainText('Connecting to public spot feed');
 await page.evaluate(()=>(window as any).__rejectPaneStatusAttach());
 await expect(chart.locator('.chart-message')).toContainText('Explicit public feed connection failure');
 await expect(chart.locator('.chart-message').getByRole('button',{name:'Retry feed',exact:true})).toBeVisible();
 await expect(chart.locator('.chart-status')).toContainText('failed');
});

test('a dragged MACD height survives edits, tab switches, viewport resize and restart without new feed leases',async({page})=>{
 const errors:string[]=[];page.on('pageerror',error=>errors.push(error.message));
 await page.goto('/');let chart=chartAt(page);await expect(chart.getByLabel('OHLCV')).toContainText('C');
 await addMACD(page,chart);await expect.poll(async()=>(await paneHeights(chart)).length).toBe(2);
 const attachments=await page.evaluate(()=>(window as any).__testAttachCount()),before=await paneHeights(chart);
 await dragSeparator(page,chart,0,-110);
 await expect.poll(async()=>(await savedChart(page)).indicators?.[0]?.paneStretchFactor).toBeGreaterThan(before[1]/before[0]+.1);
 await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();
 const preferred=(await savedChart(page)).indicators![0].paneStretchFactor!;
 await expectPaneRatio(chart,0,preferred);
 await chart.getByRole('button',{name:'Edit MACD (12, 26, 9)',exact:true}).click();
 const dialog=page.getByRole('dialog',{name:'MACD parameters',exact:true});await dialog.getByLabel('Fast period',{exact:true}).fill('8');await dialog.getByRole('button',{name:'OK',exact:true}).click();
 await expectPaneRatio(chart,0,preferred);await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();
 expect((await savedChart(page)).indicators![0].paneStretchFactor).toBe(preferred);
 await page.setViewportSize({width:1250,height:650});await expectPaneRatio(chart,0,preferred);
 await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();
 expect((await savedChart(page)).indicators![0].paneStretchFactor).toBe(preferred);
 expect(await page.evaluate(()=>(window as any).__testAttachCount())).toBe(attachments);
 await page.getByRole('button',{name:'Add tab',exact:true}).click();await expect(page.locator('[role="tab"][data-tab-id]')).toHaveCount(2);
 await page.locator('[role="tab"][data-tab-id]').first().click();chart=chartAt(page);await expectPaneRatio(chart,0,preferred);
 await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();
 await page.reload();chart=chartAt(page);await expect(chart.getByRole('button',{name:'Edit MACD (8, 26, 9)',exact:true})).toBeVisible();await expectPaneRatio(chart,0,preferred);
 expect((await savedChart(page)).indicators![0].paneStretchFactor).toBe(preferred);
 await page.screenshot({path:'test-results/macd-height-restored.png'});expect(errors).toEqual([]);
});

test('multiple MACD panes retain their own sizes when an earlier pane is removed',async({page})=>{
 await page.goto('/');let chart=chartAt(page);await expect(chart.getByLabel('OHLCV')).toContainText('C');
 await addMACD(page,chart);await addMACD(page,chart,'8');await expect.poll(async()=>(await paneHeights(chart)).length).toBe(3);
 const attachments=await page.evaluate(()=>(window as any).__testAttachCount());
 await dragSeparator(page,chart,0,-70);await dragSeparator(page,chart,1,-50);
 await expect.poll(async()=>(await savedChart(page)).indicators?.every(indicator=>typeof indicator.paneStretchFactor==='number')).toBe(true);
 await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();
 const original=(await savedChart(page)).indicators!,second=original[1];
 await expectPaneRatio(chart,0,original[0].paneStretchFactor!);await expectPaneRatio(chart,1,second.paneStretchFactor!);
 await chart.getByRole('button',{name:'Remove MACD (12, 26, 9)',exact:true}).click();await expect.poll(async()=>(await paneHeights(chart)).length).toBe(2);
 await expectPaneRatio(chart,0,second.paneStretchFactor!);
 await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();
 expect((await savedChart(page)).indicators).toEqual([second]);expect(await page.evaluate(()=>(window as any).__testAttachCount())).toBe(attachments);
 await page.reload();chart=chartAt(page);await expectPaneRatio(chart,0,second.paneStretchFactor!);
 expect((await savedChart(page)).indicators).toEqual([second]);
});

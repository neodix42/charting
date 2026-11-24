import {test,expect,type Page} from '@playwright/test';
import type {ChartConfig} from '../src/types';

const savedScale=(page:Page):Promise<ChartConfig['priceScale']>=>page.evaluate(()=>(window as any).__testWorkspace().tabs[0].charts[0].priceScale);
const actualScale=(page:Page)=>page.evaluate(async()=>{
 const modulePath='/src/charts/priceScale.ts';
 const {chartPriceScaleDiagnostics}=await import(modulePath);
 const scale=chartPriceScaleDiagnostics.values().next().value;
 return scale?{autoScale:scale.options().autoScale,range:scale.getVisibleRange()}:null;
});
async function setRange(page:Page,range:{from:number;to:number}){
 await page.evaluate(async range=>{
  const modulePath='/src/charts/priceScale.ts';
  const {chartPriceScaleDiagnostics}=await import(modulePath);
  chartPriceScaleDiagnostics.values().next().value.setVisibleRange(range);
 },range);
 await expect.poll(async()=>(await actualScale(page))?.range).toEqual(range);
 // ChartCell captures price-scale interaction on the chart's pointer release.
 await page.getByTestId('chart-canvas').dispatchEvent('pointerup');
 await page.locator('.save-indicator').click();
 await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();
}

test('manual price ranges save and restore while a range crossing zero preserves the last valid setting',async({page})=>{
 const errors:string[]=[];page.on('pageerror',error=>errors.push(error.message));
 await page.goto('/');await expect(page.getByLabel('OHLCV')).toContainText('C');
 const automatic={mode:'linear',autoScale:true,manualRange:null};
 await setRange(page,{from:-1000,to:100000});
 expect(await savedScale(page)).toEqual(automatic);
 await expect(page.locator('.save-indicator.error')).toHaveCount(0);

 const range={from:59000,to:63000};
 const manual={mode:'linear',autoScale:false,manualRange:{min:range.from,max:range.to}};
 await setRange(page,range);expect(await savedScale(page)).toEqual(manual);
 await setRange(page,{from:0,to:100000});expect(await savedScale(page)).toEqual(manual);
 await setRange(page,{from:-1000,to:100000});expect(await savedScale(page)).toEqual(manual);

 await page.reload();await expect(page.getByLabel('OHLCV')).toContainText('C');
 await expect.poll(()=>actualScale(page)).toEqual({autoScale:false,range});
 expect(await savedScale(page)).toEqual(manual);
 await page.getByRole('button',{name:'Autoscale price',exact:true}).click();
 await expect.poll(()=>savedScale(page)).toEqual(automatic);
 await expect.poll(async()=>(await actualScale(page))?.autoScale).toBe(true);
 expect(errors).toEqual([]);
});

test('a native price-axis drag preserves its manual scale through a price refresh',async({page},testInfo)=>{
 await page.goto('/');await expect(page.getByLabel('OHLCV')).toContainText('C');
 const canvas=page.getByTestId('chart-canvas'),box=(await canvas.boundingBox())!;
 const before=await actualScale(page);
 expect(before?.autoScale).toBe(true);
 await page.mouse.move(box.x+box.width-25,box.y+box.height*.3);
 await page.mouse.down();await page.mouse.move(box.x+box.width-25,box.y+box.height*.55,{steps:8});await page.mouse.up();
 await expect.poll(async()=>(await actualScale(page))?.autoScale).toBe(false);
 const manual=await actualScale(page);
 expect(manual?.range).not.toEqual(before?.range);
 await page.mouse.move(0,0);
 await page.evaluate(()=>{
  const subscription=(window as any).__testSubscriptions()[0],geometry=(window as any).__testChartGaps()[0];
  (window as any).__testPublish({...subscription,kind:'bars',revision:subscription.revision+1,bars:[{time:geometry.latestTime,open:60990,high:61020,low:60970,close:61001,volume:3,closed:false,quality:'provisional',sourceRevision:2}]});
 });
 await expect(page.getByLabel('OHLCV')).toContainText('61,001');
 const afterPrice=await actualScale(page);
 await testInfo.attach('native-price-axis-evidence',{body:JSON.stringify({before,manual,afterPrice},null,2),contentType:'application/json'});
 expect(afterPrice?.autoScale).toBe(false);
 expect(afterPrice!.range!.from).toBeCloseTo(manual!.range!.from,6);
 expect(afterPrice!.range!.to).toBeCloseTo(manual!.range!.to,6);
});

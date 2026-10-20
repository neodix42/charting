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

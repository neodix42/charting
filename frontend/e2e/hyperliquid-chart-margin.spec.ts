import {test,expect,type Page} from '@playwright/test';
import {openTestApp} from './helpers/testApp';

const overlay=(page:Page)=>page.getByRole('complementary',{name:'Hyperliquid leverage and margin',exact:true});
async function catalog(page:Page){
 await page.evaluate(()=>{
  const api=window.go!.main!.App!,list=api.ListInstruments;
  api.ListInstruments=async exchange=>{
   const items=await list(exchange);
   if(exchange!=='hyperliquid')return items;
   const result=items.map(item=>({...item,providerMetadata:{...item.providerMetadata,maxLeverage:item.nativeId==='BTC'?'40':item.nativeId==='xyz:XYZ100'?'5':'999'}}));
   const base=result.find(item=>item.nativeId==='BTC')!;
   return [...result,{...base,nativeId:'NO-LIMIT',baseAsset:'NO-LIMIT',providerMetadata:{}},{...base,nativeId:'DELISTED',baseAsset:'DELISTED',availability:'delisted',providerMetadata:{maxLeverage:'40'}}];
  };
 });
}
async function choose(page:Page,name:string){await page.getByRole('button',{name:'Select instrument',exact:true}).click();await page.getByRole('button',{name,exact:true}).click();}

test('public Hyperliquid asset margin stays at bottom right and follows native, builder and spot choices without account reads',async({page},testInfo)=>{
 await openTestApp(page);await catalog(page);await page.getByRole('combobox',{name:'Exchange',exact:true}).selectOption('hyperliquid');await choose(page,'BTC Perpetual');
 await expect(overlay(page)).toContainText('Asset max leverage 1:40');await expect(overlay(page)).toContainText('Minimum initial margin 2.5% of position value');await expect(overlay(page)).toContainText('position-size tiers can require more');
 const plot=(await page.getByTestId('chart-canvas').boundingBox())!,bounds=(await overlay(page).boundingBox())!;
 expect(bounds.x).toBeGreaterThan(plot.x+plot.width/2);expect(bounds.x+bounds.width).toBeLessThanOrEqual(plot.x+plot.width);
 expect(bounds.y+bounds.height).toBeLessThanOrEqual(plot.y+plot.height);expect(plot.y+plot.height-bounds.y-bounds.height).toBeLessThan(100);
 expect(await overlay(page).evaluate(element=>getComputedStyle(element).pointerEvents)).toBe('none');
 await choose(page,'xyz:XYZ100 Perpetual');await expect(overlay(page)).toContainText('Asset max leverage 1:5');await expect(overlay(page)).toContainText('Minimum initial margin 20% of position value');await expect(overlay(page)).not.toContainText('1:40');
 await choose(page,'HYPE/USDC Spot');await expect(overlay(page)).toContainText('Spot leverage 1:1');await expect(overlay(page)).toContainText('Funding requirement 100% of trade value');await expect(overlay(page)).toContainText('Fully funded spot; no borrowing');
 expect(await page.evaluate(()=>(window as any).__testTradeValuationReads())).toBe(0);expect(await page.evaluate(()=>(window as any).__testTradeMetricRequests())).toEqual([]);expect(await page.evaluate(()=>(window as any).__testTradeSubmissions())).toEqual([]);
 await page.getByTestId('chart-cell').screenshot({path:testInfo.outputPath('hyperliquid-spot-margin-corner.png')});
});

test('missing and delisted public margin terms do not retain a previous asset maximum',async({page})=>{
 await openTestApp(page);await catalog(page);await page.getByRole('combobox',{name:'Exchange',exact:true}).selectOption('hyperliquid');await choose(page,'BTC Perpetual');await expect(overlay(page)).toContainText('1:40');
 for(const name of ['NO-LIMIT Perpetual','DELISTED Perpetual']){
  await choose(page,name);await expect(overlay(page)).toContainText('Asset max leverage —');await expect(overlay(page)).toContainText('Minimum initial margin —');await expect(overlay(page)).toContainText('Asset margin terms unavailable');await expect(overlay(page)).not.toContainText('1:40');
 }
 await page.getByRole('combobox',{name:'Exchange',exact:true}).selectOption('binance');await expect(overlay(page)).toHaveCount(0);
 expect(await page.evaluate(()=>(window as any).__testTradeSubmissions())).toEqual([]);
});

import {test,expect} from '@playwright/test';
import {openTestApp} from './helpers/testApp';

test('Dukascopy margin shows provider account and raw instrument settings without inventing a cap',async({page})=>{
 await openTestApp(page);await page.clock.install();
 await page.evaluate(()=>(window as any).__testSetTradeMetrics({accountLeverage:180,weekendLeverage:5,instrumentMaxLeverage:10,effectiveLeverage:10}));
 await page.getByRole('combobox',{name:'Exchange',exact:true}).selectOption('dukascopy');
 const overlay=page.getByRole('complementary',{name:'Dukascopy leverage and margin'});
 const instrument=overlay.locator('span').filter({hasText:'Dukascopy instrument leverage'});
 await expect(overlay).toContainText('Account 1:180 · Weekend setting 1:5');
 await expect(instrument).toHaveText('Dukascopy instrument leverage 1:10 · Effective margin leverage 1:10');
 await page.evaluate(()=>(window as any).__testSetTradeMetrics({instrumentMaxLeverage:250,effectiveLeverage:250}));
 await page.clock.fastForward(10000);
 await expect(instrument).toHaveText('Dukascopy instrument leverage 1:250 · Effective margin leverage 1:250');
 await page.evaluate(()=>(window as any).__testSetTradeMetrics({instrumentMaxLeverage:0,effectiveLeverage:180}));
 await page.clock.fastForward(10000);
 await expect(instrument).toHaveText('Dukascopy instrument leverage Account setting · Effective margin leverage 1:180');
 await page.evaluate(()=>(window as any).__testSetTradeMetrics({instrumentMaxLeverage:undefined}));
 await page.clock.fastForward(10000);
 await expect(instrument).toHaveText('Dukascopy instrument leverage — · Effective margin leverage 1:180');
 expect(await page.evaluate(()=>(window as any).__testTradeSubmissions())).toEqual([]);
});

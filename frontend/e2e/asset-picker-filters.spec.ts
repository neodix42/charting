import {test,expect,type Page} from '@playwright/test';
import {openTestApp} from './helpers/testApp';

const picker=(page:Page)=>page.getByLabel('Instruments',{exact:true});
const trigger=(page:Page)=>page.getByRole('button',{name:'Select instrument',exact:true});
const exchange=(page:Page)=>page.getByRole('combobox',{name:'Exchange',exact:true});

test('chart asset filters separate Dukascopy CFD/Forex/metals and Hyperliquid perpetual/spot without changing feeds until selection',async({page})=>{
 await openTestApp(page);
 await page.evaluate(()=>window.go!.main!.App!.ConfigureDucascopy({username:'filter-fixture',password:'explicit-offline-fixture',environment:'demo'}));
 await exchange(page).selectOption('dukascopy');await trigger(page).click();
 await picker(page).getByRole('button',{name:'Forex',exact:true}).click();
 await expect(picker(page).getByRole('button',{name:'EUR/USD Forex',exact:true})).toBeVisible();
 await expect(picker(page).locator('.product-market.cfd')).toHaveCount(0);await expect(picker(page).locator('.product-market.metal')).toHaveCount(0);
 await picker(page).getByRole('button',{name:'CFD',exact:true}).click();
 await expect(picker(page).getByRole('button',{name:'EUR/USD Forex',exact:true})).toHaveCount(0);
 await picker(page).getByRole('textbox',{name:'Search instruments',exact:true}).fill('Apple');
 await picker(page).getByRole('button',{name:'AAPL.US/USD CFD',exact:true}).click();
 await expect(trigger(page)).toContainText('AAPL.US/USD');await expect(page.getByLabel('Selected market',{exact:true})).toHaveText('CFD');
 await trigger(page).click();await picker(page).getByRole('button',{name:'Metal',exact:true}).click();
 await picker(page).getByRole('button',{name:'XAU/USD Metal',exact:true}).click();
 await expect(page.getByLabel('Selected market',{exact:true})).toHaveText('Metal');
 await exchange(page).selectOption('hyperliquid');await trigger(page).click();
 await expect(picker(page).getByRole('button',{name:'Forex',exact:true})).toHaveCount(0);
 await picker(page).getByRole('button',{name:'Spot',exact:true}).click();
 await expect(picker(page).locator('.product-market.perpetual')).toHaveCount(0);
 await expect(page.getByLabel('Selected market',{exact:true})).toHaveText('Perpetual');
 await picker(page).getByRole('button',{name:'HYPE/USDC Spot',exact:true}).click();
 await expect(page.getByLabel('Selected market',{exact:true})).toHaveText('Spot');
 await trigger(page).click();await picker(page).getByRole('button',{name:'Perpetual',exact:true}).click();
 await expect(picker(page).locator('.product-market.spot')).toHaveCount(0);
 await picker(page).getByRole('button',{name:'BTC Perpetual',exact:true}).click();
 await expect(page.getByLabel('Selected market',{exact:true})).toHaveText('Perpetual');
 await expect.poll(()=>page.evaluate(()=>(window as any).__testSubscriptions().map((value:any)=>value.feedKey))).toMatchObject([{exchange:'hyperliquid',market:'perpetual',instrument:'BTC'}]);
});

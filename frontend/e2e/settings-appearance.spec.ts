import {test,expect,type Page} from '@playwright/test';

const settings=(page:Page)=>page.getByRole('dialog',{name:'Workspace settings',exact:true});
const saved=(page:Page)=>page.evaluate(()=>(window as any).__testWorkspace());
async function textSizes(page:Page){return page.evaluate(()=>Object.fromEntries(['.chart-header [aria-label="Timeframe 1h"]','.chart-status','.app-footer','.app-header .settings-button'].map(selector=>[selector,parseFloat(getComputedStyle(document.querySelector(selector)!).fontSize)])));}

test('settings pages separate appearance, local storage, Screener, export and debug controls',async({page})=>{
 await page.goto('/');await page.getByRole('button',{name:'Open settings',exact:true}).click();const dialog=settings(page);
 await expect(dialog.getByRole('tab',{selected:true})).toHaveText('Appearance');await expect(dialog.getByLabel('Font size',{exact:true})).toHaveValue('13');
 await expect(dialog.getByRole('form',{name:'Local candle storage',exact:true})).toHaveCount(0);await expect(dialog.getByRole('button',{name:'Export JSON…',exact:true})).toHaveCount(0);
 await dialog.getByRole('tab',{name:'Storage',exact:true}).click();await expect(dialog.getByRole('checkbox',{name:'Store candle data locally',exact:true})).toBeEnabled();await expect(dialog.getByLabel('Theme',{exact:true})).toHaveCount(0);
 await dialog.getByRole('tab',{name:'Screener',exact:true}).click();await expect(dialog.getByRole('form',{name:'Screener data storage',exact:true})).toBeVisible();
 await dialog.getByRole('tab',{name:'Export',exact:true}).click();await expect(dialog.getByRole('button',{name:'Export JSON…',exact:true})).toBeVisible();await expect(dialog.getByRole('button',{name:'Import JSON…',exact:true})).toBeVisible();
 await dialog.getByRole('tab',{name:'Debug',exact:true}).click();await dialog.getByRole('button',{name:'Refresh diagnostics',exact:true}).click();await expect(dialog.locator('.diagnostics')).toContainText('frontend');
 await dialog.getByRole('tab',{name:'Debug',exact:true}).press('Home');await expect(dialog.getByRole('tab',{selected:true})).toHaveText('Appearance');
 await dialog.getByRole('tab',{name:'Appearance',exact:true}).press('ArrowRight');await expect(dialog.getByRole('tab',{selected:true})).toHaveText('Storage');
});

test('font size reaches chart controls, both status bars and actual canvas labels, then restores after restart',async({page})=>{
 await page.addInitScript(()=>{
  const original=CanvasRenderingContext2D.prototype.fillText;(window as any).__paintedFonts=[];
  CanvasRenderingContext2D.prototype.fillText=function(...args:Parameters<CanvasRenderingContext2D['fillText']>){(window as any).__paintedFonts.push(this.font);return original.apply(this,args);};
 });
 await page.goto('/');await expect(page.getByTestId('chart-cell')).toHaveCount(1);await expect(page.getByLabel('OHLCV')).toContainText('61,000');
 const before=await textSizes(page),attachments=await page.evaluate(()=>(window as any).__testAttachCount());
 await page.getByRole('button',{name:'Open settings',exact:true}).click();await settings(page).getByLabel('Font size',{exact:true}).selectOption('20');
 await expect.poll(()=>saved(page)).toMatchObject({fontSize:20});await settings(page).getByRole('button',{name:'Close settings',exact:true}).click();
 const after=await textSizes(page);for(const selector of Object.keys(before))expect(after[selector]/before[selector]).toBeCloseTo(20/13,3);
 await expect.poll(()=>page.evaluate(()=>(window as any).__paintedFonts.some((font:string)=>/\b17px\b/.test(font)))).toBe(true);
 expect(await page.evaluate(()=>(window as any).__testAttachCount())).toBe(attachments);
 await expect.poll(()=>page.evaluate(()=>window.__cryptodeskDiagnostics?.())).toMatchObject({chartInstances:1,consumers:1});
 await page.reload();await expect(page.getByTestId('chart-cell')).toHaveAttribute('data-chart-font-size','17');await expect.poll(()=>page.evaluate(()=>getComputedStyle(document.documentElement).fontSize)).toBe('20px');
 await page.getByRole('button',{name:'Open settings',exact:true}).click();await expect(settings(page).getByLabel('Font size',{exact:true})).toHaveValue('20');
 await settings(page).getByLabel('Font size',{exact:true}).selectOption('13');await expect.poll(()=>saved(page)).toMatchObject({fontSize:13});
});


test('the largest font keeps settings and toolbar controls usable on a compact desktop',async({page})=>{
 await page.setViewportSize({width:1000,height:800});await page.goto('/');await page.getByRole('button',{name:'Open settings',exact:true}).click();const dialog=settings(page);
 await dialog.getByLabel('Font size',{exact:true}).selectOption('24');await expect.poll(()=>saved(page)).toMatchObject({fontSize:24});
 for(const name of ['Appearance','Storage','Screener','Export','Debug'])await expect(dialog.getByRole('tab',{name,exact:true})).toBeVisible();
 await expect(dialog.getByLabel('Font size',{exact:true})).toBeInViewport();await dialog.getByRole('button',{name:'Close settings',exact:true}).click();
 const geometry=await page.locator('.app-header').evaluate(header=>{const rect=(selector:string)=>{const r=header.querySelector(selector)!.getBoundingClientRect();return{left:r.left,right:r.right,top:r.top,bottom:r.bottom};};return{brand:rect('.brand'),periods:rect('.tab-chart-controls'),actions:rect('.header-actions'),windowWidth:innerWidth};});
 expect(geometry.brand.right).toBeLessThanOrEqual(geometry.actions.left);expect(geometry.periods.top).toBeGreaterThanOrEqual(geometry.brand.bottom);expect(geometry.actions.right).toBeLessThanOrEqual(geometry.windowWidth);
 await expect(page.getByRole('button',{name:'Open settings',exact:true})).toBeInViewport();await expect(page.locator('.app-footer')).toBeInViewport();await page.screenshot({path:'test-results/font-size-24.png'});
});

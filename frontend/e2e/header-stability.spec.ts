import {test,expect,type Page} from '@playwright/test';

async function headerGeometry(page:Page){return page.locator('.app-header').evaluate(header=>{
 const rect=(selector:string)=>{const r=header.querySelector(selector)!.getBoundingClientRect();return{left:r.left,width:r.width,top:r.top};};
 return{save:rect('.save-indicator'),grid:rect('[aria-label="Grid layout"]'),add:rect('[aria-label="Add chart"]'),duplicate:rect('[aria-label="Duplicate tab"]'),periods:rect('[data-testid="tab-chart-controls"]'),brand:rect('.brand'),settings:rect('[aria-label="Open settings"]')};
});}
async function minimalControls(page:Page){
 for(const name of ['Rename tab','Close tab'])await expect(page.locator('.app-header').getByRole('button',{name,exact:true})).toHaveCount(0);
 await expect(page.getByRole('combobox',{name:'All tabs',exact:true})).toHaveCount(0);await expect(page.getByText(/^Tabs \d+\/\d+$/)).toHaveCount(0);
}
async function adjacentTabPlus(page:Page,count:number){
 await expect(page.locator('[role="tab"][data-tab-id]')).toHaveCount(count);
 await expect.poll(()=>page.locator('.tab-row').evaluate(row=>{
  const list=row.querySelector('[role="tablist"]')!,last=list.querySelector('[role="tab"]:last-child')!,plus=row.querySelector('[aria-label="Add tab"]')!,scroller=list.parentElement!;
  const tab=last.getBoundingClientRect(),add=plus.getBoundingClientRect(),scroll=scroller.getBoundingClientRect();
  return{siblings:plus.previousElementSibling===list,gap:add.left-tab.right,sameRow:Math.abs((tab.top+tab.bottom-add.top-add.bottom)/2)<1,inside:add.left>=scroll.left-1&&add.right<=scroll.right+1};
 })).toMatchObject({siblings:true,sameRow:true,inside:true});
 const geometry=await page.locator('.tab-row').evaluate(row=>{
  const list=row.querySelector('[role="tablist"]')!,last=list.querySelector('[role="tab"]:last-child')!.getBoundingClientRect(),plus=row.querySelector('[aria-label="Add tab"]')!.getBoundingClientRect(),scroller=list.parentElement!;
  return{gap:plus.left-last.right,overflow:scroller.scrollWidth>scroller.clientWidth+1,documentWidth:document.documentElement.scrollWidth,windowWidth:innerWidth};
 });
 expect(geometry.gap).toBeGreaterThanOrEqual(0);expect(geometry.gap).toBeLessThanOrEqual(14);expect(geometry.documentWidth).toBe(geometry.windowWidth);return geometry;
}

for(const viewport of [{width:1600,height:1000},{width:1100,height:720}]){
 test(`save state changes keep header actions stationary at ${viewport.width}px`,async({page})=>{
  await page.setViewportSize(viewport);await page.clock.install();await page.goto('/');await expect(page.locator('.save-indicator.saved')).toBeVisible();
  await page.clock.pauseAt(await page.evaluate(()=>Date.now()+1000));await page.locator('.save-indicator').click();await expect(page.locator('.save-indicator.saved')).toBeVisible();await minimalControls(page);
  const baseline=await headerGeometry(page);
  const stationary=async()=>{const next=await headerGeometry(page);for(const key of Object.keys(baseline) as Array<keyof typeof baseline>){expect(next[key].left,`${key} left edge`).toBeCloseTo(baseline[key].left,3);expect(next[key].width,`${key} width`).toBeCloseTo(baseline[key].width,3);expect(next[key].top,`${key} top edge`).toBeCloseTo(baseline[key].top,3);}};
  await page.evaluate(()=>{const api=window.go!.main!.App!,original=api.SaveWorkspace.bind(api);api.SaveWorkspace=async(...args)=>{api.SaveWorkspace=original;await new Promise<void>((_resolve,reject)=>{(window as any).__rejectHeaderSave=()=>reject(Error('Explicit save-state geometry failure'));});return original(...args);};});
  await page.getByRole('button',{name:'Toggle volume',exact:true}).click();await expect(page.locator('.save-indicator.pending')).toBeVisible();await expect(page.getByRole('button',{name:'Unsaved changes',exact:true})).toBeVisible();await stationary();
  await page.locator('.save-indicator').click();await expect(page.locator('.save-indicator.saving')).toBeVisible();await expect(page.getByRole('button',{name:'Saving…',exact:true})).toBeVisible();await stationary();
  await page.evaluate(()=>(window as any).__rejectHeaderSave());await expect(page.locator('.save-indicator.error')).toBeVisible();await expect(page.getByRole('button',{name:'Save failed · Retry',exact:true})).toBeVisible();await stationary();
  await page.locator('.save-indicator').click();await expect(page.locator('.save-indicator.saved')).toBeVisible();await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();await stationary();await page.screenshot({path:`test-results/header-save-stability-${viewport.width}.png`});
 });
 test(`add-tab plus stays adjacent through editing and reaches twenty tabs at ${viewport.width}px`,async({page})=>{
  await page.setViewportSize(viewport);await page.goto('/');await minimalControls(page);await adjacentTabPlus(page,1);
  for(let i=1;i<3;i++){await page.getByRole('button',{name:'Add tab',exact:true}).click();await expect(page.locator('[role="tab"][data-tab-id]')).toHaveCount(i+1);}await adjacentTabPlus(page,3);
  const longName='A long research workspace '.repeat(3).slice(0,80);await page.locator('[role="tab"][data-tab-id]').last().dblclick();await page.getByRole('textbox',{name:'Tab name',exact:true}).fill(longName);await page.getByRole('button',{name:'Rename',exact:true}).click();const longTab=page.locator('[role="tab"][data-tab-id]').last();await expect(longTab).toContainText(longName);await expect(page.getByRole('tab',{name:longName,exact:false})).toHaveCount(1);await expect.poll(()=>longTab.evaluate(element=>{const text=element.querySelector('.tab-name')!;return{bounded:element.getBoundingClientRect().width<=260,ellipsis:getComputedStyle(text).textOverflow==='ellipsis',clipped:text.scrollWidth>text.clientWidth};})).toEqual({bounded:true,ellipsis:true,clipped:true});await adjacentTabPlus(page,3);
  await longTab.dragTo(page.locator('[role="tab"][data-tab-id]').first());await expect(page.locator('[role="tab"][data-tab-id]').first()).toContainText(longName);await adjacentTabPlus(page,3);await page.locator('[role="tab"][data-tab-id]').last().click({button:'middle'});await expect(page.getByRole('dialog')).toHaveCount(0);await adjacentTabPlus(page,2);
  for(let i=2;i<20;i++){await page.getByRole('button',{name:'Add tab',exact:true}).click();await expect(page.locator('[role="tab"][data-tab-id]')).toHaveCount(i+1);}const full=await adjacentTabPlus(page,20);expect(full.overflow).toBe(true);await expect(page.getByRole('button',{name:'Add tab',exact:true})).toBeDisabled();await expect(page.getByRole('button',{name:'Duplicate tab',exact:true})).toBeDisabled();
  await page.locator('[role="tab"][data-tab-id]').first().click();await expect(page.locator('[role="tab"][data-tab-id]').first()).toHaveAttribute('aria-selected','true');await expect.poll(()=>page.locator('.tabs').evaluate(element=>element.scrollLeft)).toBe(0);
  await page.locator('[role="tab"][data-tab-id]').first().focus();await page.keyboard.press('Control+Shift+Tab');await expect(page.locator('[role="tab"][data-tab-id]').last()).toHaveAttribute('aria-selected','true');await adjacentTabPlus(page,20);await page.keyboard.press('Control+Tab');await expect(page.locator('[role="tab"][data-tab-id]').first()).toHaveAttribute('aria-selected','true');await page.locator('[role="tab"][data-tab-id]').last().click();await adjacentTabPlus(page,20);await minimalControls(page);
  await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();expect(await page.evaluate(()=>(window as any).__testWorkspace().tabs.length)).toBe(20);await page.screenshot({path:`test-results/tabstrip-${viewport.width}-20.png`});
 });
}

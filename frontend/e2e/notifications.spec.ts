import {test,expect,type Page} from '@playwright/test';
import intermediate from '../../examples/intermediate-two-tabs.json' with {type:'json'};

async function ready(page:Page){
 await page.clock.install();
 await page.addInitScript(value=>{value.activeTabId=value.tabs[0].id;sessionStorage.setItem('cryptodesk-explicit-test-workspace',JSON.stringify(value));},intermediate);
 await page.goto('/');
 await expect(page.getByTestId('chart-cell')).toHaveCount(3);
 await expect(page.locator('.save-indicator.saved')).toBeVisible();
 await page.clock.pauseAt(await page.evaluate(()=>Date.now()+1000));
 await page.locator('.save-indicator').click();
 await expect(page.locator('.save-indicator.saved')).toBeVisible();
}
async function chartGeometry(page:Page){return page.locator('.chart-grid,.chart-cell,.chart-canvas,.chart-canvas canvas').evaluateAll(elements=>elements.map(element=>{
 const rect=element.getBoundingClientRect();return{left:rect.left,top:rect.top,width:rect.width,height:rect.height};
}));}
async function notify(page:Page,message:string){await page.evaluate(value=>(window as any).__testEmit('candle-storage-error',value),message);}

for(const viewport of [{width:1600,height:1000},{width:1100,height:720}]){
 test(`notifications float for seven seconds without moving any charts at ${viewport.width}px`,async({page})=>{
  await page.setViewportSize(viewport);await ready(page);const baseline=await chartGeometry(page);
  await notify(page,'Explicit candle storage write failure: '+ '/long-folder-name'.repeat(20));
  const notice=page.getByRole('status');await expect(notice).toContainText('Explicit candle storage write failure');
  expect(await page.locator('.notification-stack').evaluate(element=>getComputedStyle(element).position)).toBe('fixed');
  expect(await chartGeometry(page)).toEqual(baseline);
  await page.screenshot({path:`test-results/floating-notification-${viewport.width}.png`});
  await page.clock.runFor(6999);await expect(notice).toBeVisible();expect(await chartGeometry(page)).toEqual(baseline);
  await page.clock.runFor(1);await expect(notice).toHaveCount(0);expect(await chartGeometry(page)).toEqual(baseline);
 });
}

test('repeated and replacement notifications each get seven seconds and can be dismissed',async({page})=>{
 await ready(page);const baseline=await chartGeometry(page),notice=page.getByRole('status');
 await notify(page,'Repeated storage error');await expect(notice).toHaveText('Repeated storage error×');
 await page.clock.runFor(4000);await notify(page,'Repeated storage error');
 await page.clock.runFor(3000);await expect(notice).toBeVisible();
 await notify(page,'New storage error');await expect(notice).toContainText('New storage error');
 await page.clock.runFor(4000);await expect(notice).toBeVisible();
 await page.clock.runFor(2999);await expect(notice).toBeVisible();
 await page.clock.runFor(1);await expect(notice).toHaveCount(0);
 await notify(page,'Repeated storage error');await expect(notice).toBeVisible();
 await page.getByRole('button',{name:'Dismiss notification',exact:true}).click();await expect(notice).toHaveCount(0);
 await notify(page,'Repeated storage error');await expect(notice).toBeVisible();
 await page.clock.runFor(7000);await expect(notice).toHaveCount(0);expect(await chartGeometry(page)).toEqual(baseline);
});

test('save failure toast expires or dismisses while unsaved state and retry remain available',async({page})=>{
 await ready(page);const baseline=await chartGeometry(page),notice=page.getByRole('status');
 await page.evaluate(()=>(window as any).__testFailSave(true));
 await page.getByRole('button',{name:'Toggle volume',exact:true}).first().click();await page.locator('.save-indicator').click();
 await expect(notice).toContainText('Workspace save failed: Explicit simulated database write failure');expect(await chartGeometry(page)).toEqual(baseline);
 await page.clock.runFor(6999);await expect(notice).toBeVisible();await page.clock.runFor(1);await expect(notice).toHaveCount(0);
 await expect(page.getByRole('button',{name:'Save failed · Retry',exact:true})).toBeVisible();expect(await chartGeometry(page)).toEqual(baseline);
 await page.locator('.save-indicator').click();await expect(notice).toContainText('Workspace save failed');
 await page.getByRole('button',{name:'Dismiss notification',exact:true}).click();await expect(notice).toHaveCount(0);
 await expect(page.locator('.save-indicator.error')).toBeVisible();await page.clock.runFor(7000);await expect(notice).toHaveCount(0);
 await page.locator('.save-indicator').click();await expect(notice).toBeVisible();await page.evaluate(()=>(window as any).__testFailSave(false));
 await page.getByRole('button',{name:'Retry save',exact:true}).click();await expect(page.locator('.save-indicator.saved')).toBeVisible();await expect(notice).toHaveCount(0);
 expect(await chartGeometry(page)).toEqual(baseline);
});

test('shutdown save choices float without moving charts and remain until resolved',async({page})=>{
 await ready(page);const baseline=await chartGeometry(page);
 await page.evaluate(()=>(window as any).__testFailSave(true));await page.getByRole('button',{name:'Toggle volume',exact:true}).first().click();
 await page.evaluate(()=>(window as any).__testEmit('prepare-close',{token:'floating-close'}));
 await expect(page.getByRole('button',{name:'Retry save and close',exact:true})).toBeVisible();expect(await chartGeometry(page)).toEqual(baseline);
 await page.clock.runFor(7000);await expect(page.getByRole('status')).toHaveCount(0);await expect(page.getByRole('alert')).toBeVisible();expect(await chartGeometry(page)).toEqual(baseline);
 await page.evaluate(()=>(window as any).__testFailSave(false));await page.getByRole('button',{name:'Retry save and close',exact:true}).click();
 await expect.poll(()=>page.evaluate(()=>(window as any).__testCloseConfirmation())).toEqual({token:'floating-close',discard:false});
 await expect(page.getByRole('alert')).toHaveCount(0);expect(await chartGeometry(page)).toEqual(baseline);
});

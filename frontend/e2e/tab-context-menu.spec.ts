import {test,expect,type Page} from '@playwright/test';
import intermediate from '../../examples/intermediate-two-tabs.json' with {type:'json'};
import type {Workspace} from '../src/types';

const saved=(page:Page):Promise<Workspace>=>page.evaluate(()=>(window as any).__testWorkspace());
const menu=(page:Page)=>page.getByRole('menu');
const tab=(page:Page,id:string)=>page.locator(`[role="tab"][data-tab-id="${id}"]`);
async function seed(page:Page,activeIndex=0){
 const workspace=structuredClone(intermediate) as Workspace;workspace.activeTabId=workspace.tabs[activeIndex].id;
 await page.addInitScript(value=>{const key='cryptodesk-explicit-test-workspace';if(!sessionStorage.getItem(key))sessionStorage.setItem(key,JSON.stringify(value));},workspace);
 await page.goto('/');await expect(page.getByTestId('chart-cell')).toHaveCount(workspace.tabs[activeIndex].charts.length);
 await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();
 return workspace;
}

test('inactive tab context menu closes its entire tab without selecting or attaching its charts',async({page})=>{
 const workspace=await seed(page,1),target=workspace.tabs[0],active=workspace.tabs[1],trigger=tab(page,target.id);
 const mounted=await page.getByTestId('chart-cell').evaluateAll(cells=>cells.map(cell=>cell.getAttribute('data-chart-id')));
 const attachments=await page.evaluate(()=>(window as any).__testAttachCount());
 await trigger.click({button:'right'});await expect(menu(page)).toHaveAccessibleName(`Actions for ${target.name}`);
 await expect(trigger).toHaveAttribute('aria-expanded','true');await expect(tab(page,active.id)).toHaveAttribute('aria-selected','true');
 await expect.poll(()=>page.evaluate(()=>(window as any).__testLeases())).toBe(1);
 expect(await page.evaluate(()=>(window as any).__testAttachCount())).toBe(attachments);
 await menu(page).getByRole('menuitem',{name:'Close tab',exact:true}).click();
 let dialog=page.getByRole('dialog',{name:`Close ${target.name}?`,exact:true});await expect(dialog).toContainText('all 3 charts');
 await expect(dialog.getByRole('button',{name:'Cancel',exact:true})).toBeFocused();
 await dialog.getByRole('button',{name:'Cancel',exact:true}).click();await expect(trigger).toBeFocused();
 await expect(page.getByRole('tab')).toHaveCount(2);expect((await saved(page)).tabs).toHaveLength(2);
 await trigger.click({button:'right'});await menu(page).getByRole('menuitem',{name:'Close tab',exact:true}).click();
 dialog=page.getByRole('dialog',{name:`Close ${target.name}?`,exact:true});await dialog.getByRole('button',{name:'Close tab',exact:true}).click();
 await expect(page.getByRole('tab')).toHaveCount(1);await expect(tab(page,active.id)).toHaveAttribute('aria-selected','true');
 await expect(tab(page,active.id)).toBeFocused();
 expect(await page.getByTestId('chart-cell').evaluateAll(cells=>cells.map(cell=>cell.getAttribute('data-chart-id')))).toEqual(mounted);
 expect(await page.evaluate(()=>(window as any).__testAttachCount())).toBe(attachments);
 await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();
 const result=await saved(page);expect(result.activeTabId).toBe(active.id);expect(result.tabs.map(value=>value.id)).toEqual([active.id]);
 expect(result.tabs[0].charts.map(chart=>chart.id)).toEqual(mounted);
 await page.reload();await expect(page.getByRole('tab')).toHaveCount(1);await expect(page.getByTestId('chart-cell')).toHaveCount(1);
 expect((await saved(page)).tabs.map(value=>value.id)).toEqual([active.id]);
});

test('closing the active tab removes every chart and releases its market leases',async({page})=>{
 const workspace=await seed(page),target=workspace.tabs[0],remaining=workspace.tabs[1];
 await tab(page,target.id).click({button:'right'});await menu(page).getByRole('menuitem',{name:'Close tab',exact:true}).click();
 await page.getByRole('dialog',{name:`Close ${target.name}?`,exact:true}).getByRole('button',{name:'Close tab',exact:true}).click();
 await expect(page.getByTestId('chart-cell')).toHaveCount(1);await expect(tab(page,remaining.id)).toHaveAttribute('aria-selected','true');
 await expect.poll(()=>page.evaluate(()=>(window as any).__testLeases())).toBe(1);
 await expect.poll(()=>page.evaluate(()=>window.__cryptodeskDiagnostics?.())).toMatchObject({chartInstances:1,consumers:1,savedTabs:1,savedCharts:1});
 await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();expect((await saved(page)).tabs.map(value=>value.id)).toEqual([remaining.id]);
});

test('context menu keyboard access, cancellation and final-tab guard preserve selection',async({page})=>{
 const workspace=await seed(page,1),inactive=tab(page,workspace.tabs[0].id),selected=tab(page,workspace.tabs[1].id);
 await inactive.focus();await page.keyboard.press('Shift+F10');await expect(menu(page).getByRole('menuitem',{name:'Close tab',exact:true})).toBeFocused();
 await expect(selected).toHaveAttribute('aria-selected','true');await page.keyboard.press('Escape');await expect(menu(page)).toHaveCount(0);await expect(inactive).toBeFocused();
 await inactive.press('ContextMenu');await menu(page).getByRole('menuitem',{name:'Close tab',exact:true}).press('Enter');
 await expect(page.getByRole('dialog')).toBeVisible();await page.keyboard.press('Escape');await expect(page.getByRole('dialog')).toHaveCount(0);await expect(inactive).toBeFocused();
 await inactive.press('Shift+F10');await page.locator('.brand').click();await expect(menu(page)).toHaveCount(0);await expect(page.getByRole('tab')).toHaveCount(2);
 await inactive.click({button:'right'});await menu(page).getByRole('menuitem',{name:'Close tab',exact:true}).click();
 await page.getByRole('dialog').getByRole('button',{name:'Close tab',exact:true}).click();await expect(page.getByRole('tab')).toHaveCount(1);
 await selected.click({button:'right'});await expect(menu(page).getByRole('menuitem',{name:'Close tab',exact:true})).toBeDisabled();
 await page.keyboard.press('Enter');await expect(page.getByRole('dialog')).toHaveCount(0);await page.keyboard.press('Escape');await expect(selected).toBeFocused();
});

test('context menu stays within the viewport and dismisses when scrolling or resizing',async({page})=>{
 const workspace=await seed(page);await page.setViewportSize({width:1100,height:720});const trigger=tab(page,workspace.tabs[0].id);
 await trigger.dispatchEvent('contextmenu',{clientX:1098,clientY:718,button:2,bubbles:true});await expect(menu(page)).toBeVisible();
 const bounds=await menu(page).boundingBox();expect(bounds).not.toBeNull();expect(bounds!.x).toBeGreaterThanOrEqual(8);expect(bounds!.y).toBeGreaterThanOrEqual(8);expect(bounds!.x+bounds!.width).toBeLessThanOrEqual(1092);expect(bounds!.y+bounds!.height).toBeLessThanOrEqual(712);
 await page.screenshot({path:'test-results/tab-context-menu-clamped.png'});
 await page.setViewportSize({width:1200,height:800});await expect(menu(page)).toHaveCount(0);
 await trigger.click({button:'right'});await page.locator('.tabs').dispatchEvent('scroll');await expect(menu(page)).toHaveCount(0);
});

test('failed save before tab closure preserves the tab and all its chart configurations',async({page})=>{
 const workspace=await seed(page,1),target=workspace.tabs[0];
 await page.evaluate(()=>(window as any).__testFailSave(true));await page.getByRole('button',{name:'Timeframe 4h',exact:true}).click();
 await tab(page,target.id).click({button:'right'});await menu(page).getByRole('menuitem',{name:'Close tab',exact:true}).click();
 await page.getByRole('dialog').getByRole('button',{name:'Close tab',exact:true}).click();
 await expect(page.getByRole('status')).toContainText('Workspace save failed');await expect(page.getByRole('tab')).toHaveCount(2);
 expect((await saved(page)).tabs[0]).toEqual(target);await expect(tab(page,workspace.tabs[1].id)).toHaveAttribute('aria-selected','true');
 await page.evaluate(()=>(window as any).__testFailSave(false));await page.getByRole('button',{name:'Retry save',exact:true}).click();
 await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();await expect(page.getByRole('tab')).toHaveCount(2);
});

test('repeated confirmations waiting on one save cannot close an unrelated tab',async({page})=>{
 const workspace=await seed(page,1),target=workspace.tabs[0],remaining=workspace.tabs[1];
 await page.evaluate(()=>{const api=window.go!.main!.App!,save=api.SaveWorkspace.bind(api);(window as any).__contextSaveRelease=null;api.SaveWorkspace=async(...args)=>{await new Promise<void>(resolve=>(window as any).__contextSaveRelease=resolve);return save(...args);};});
 await page.getByRole('button',{name:'Timeframe 4h',exact:true}).click();
 for(let count=0;count<2;count++){
  await tab(page,target.id).click({button:'right'});await menu(page).getByRole('menuitem',{name:'Close tab',exact:true}).click();
  await page.getByRole('dialog').getByRole('button',{name:'Close tab',exact:true}).click();
 }
 await expect.poll(()=>page.evaluate(()=>typeof (window as any).__contextSaveRelease)).toBe('function');
 await page.evaluate(()=>{const api=window.go!.main!.App!,blocked=api.SaveWorkspace.bind(api);api.SaveWorkspace=async(...args)=>{const operation=blocked(...args);(window as any).__contextSaveRelease();return operation;};(window as any).__contextSaveRelease();});
 await expect(page.getByRole('tab')).toHaveCount(1);await expect(tab(page,remaining.id)).toHaveAttribute('aria-selected','true');
 await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();expect((await saved(page)).tabs.map(value=>value.id)).toEqual([remaining.id]);
});

import {test,expect,type Page} from '@playwright/test';
import intermediate from '../../examples/intermediate-two-tabs.json' with {type:'json'};
import type {Workspace} from '../src/types';

const saved=(page:Page):Promise<Workspace>=>page.evaluate(()=>(window as any).__testWorkspace());
const tab=(page:Page,id:string)=>page.locator(`[role="tab"][data-tab-id="${id}"]`);
const chartIds=(page:Page)=>page.getByTestId('chart-cell').evaluateAll(cells=>cells.map(cell=>cell.getAttribute('data-chart-id')));
async function seed(page:Page,activeIndex=0){
 const workspace=structuredClone(intermediate) as Workspace;workspace.activeTabId=workspace.tabs[activeIndex].id;
 await page.addInitScript(value=>{const key='cryptodesk-explicit-test-workspace';if(!sessionStorage.getItem(key))sessionStorage.setItem(key,JSON.stringify(value));},workspace);
 await page.goto('/');await expect(page.getByTestId('chart-cell')).toHaveCount(workspace.tabs[activeIndex].charts.length);
 await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();
 return workspace;
}
async function noCloseDialog(page:Page){await expect(page.getByRole('menu')).toHaveCount(0);await expect(page.getByRole('dialog')).toHaveCount(0);}
async function holdFirstSave(page:Page){
 await page.evaluate(()=>{const api=window.go!.main!.App!,save=api.SaveWorkspace.bind(api);(window as any).__middleSaveRelease=null;(window as any).__middleSaveCalls=0;api.SaveWorkspace=async(...args)=>{api.SaveWorkspace=save;(window as any).__middleSaveCalls++;await new Promise<void>(resolve=>(window as any).__middleSaveRelease=resolve);return save(...args);};});
}

test('middle-click closes an inactive whole tab without selecting it or opening a menu or confirmation',async({page})=>{
 const workspace=await seed(page,1),target=workspace.tabs[0],active=workspace.tabs[1],mounted=await chartIds(page);
 const attachments=await page.evaluate(()=>(window as any).__testAttachCount());
 await tab(page,active.id).focus();
 await tab(page,target.id).click({button:'middle'});await noCloseDialog(page);
 await expect(page.locator('[role="tab"][data-tab-id]')).toHaveCount(1);await expect(tab(page,active.id)).toHaveAttribute('aria-selected','true');
 await expect(tab(page,active.id)).toBeFocused();expect(await chartIds(page)).toEqual(mounted);
 expect(await page.evaluate(()=>(window as any).__testAttachCount())).toBe(attachments);
 await expect.poll(()=>page.evaluate(()=>(window as any).__testLeases())).toBe(1);
 await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();
 const result=await saved(page);expect(result.activeTabId).toBe(active.id);expect(result.tabs.map(value=>value.id)).toEqual([active.id]);
 expect(result.tabs[0].charts.map(chart=>chart.id)).toEqual(mounted);
 await page.reload();await expect(page.locator('[role="tab"][data-tab-id]')).toHaveCount(1);await expect(page.getByTestId('chart-cell')).toHaveCount(1);
 expect((await saved(page)).tabs.map(value=>value.id)).toEqual([active.id]);
});

test('middle-clicking the active tab removes all its charts and releases their leases',async({page})=>{
 const workspace=await seed(page),target=workspace.tabs[0],remaining=workspace.tabs[1];
 await expect.poll(()=>page.evaluate(()=>(window as any).__testLeases())).toBe(3);
 await tab(page,target.id).click({button:'middle'});await noCloseDialog(page);
 await expect(page.getByTestId('chart-cell')).toHaveCount(1);await expect(tab(page,remaining.id)).toHaveAttribute('aria-selected','true');
 await expect(tab(page,remaining.id)).toBeFocused();
 await expect.poll(()=>page.evaluate(()=>(window as any).__testLeases())).toBe(1);
 await expect.poll(()=>page.evaluate(()=>window.__cryptodeskDiagnostics?.())).toMatchObject({chartInstances:1,consumers:1,savedTabs:1,savedCharts:1});
 await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();
 expect((await saved(page)).tabs.map(value=>value.id)).toEqual([remaining.id]);
});

test('middle-clicking an inactive tab preserves the active maximized chart and its siblings',async({page})=>{
 const workspace=await seed(page),active=workspace.tabs[0],inactive=workspace.tabs[1];
 await page.getByRole('button',{name:'Maximize chart',exact:true}).nth(1).click();
 const mounted=await chartIds(page),attachments=await page.evaluate(()=>(window as any).__testAttachCount());
 const maximized=page.locator('.cell-wrapper.maximized');await expect(maximized).toHaveCount(1);
 const maximizedId=await maximized.getByTestId('chart-cell').getAttribute('data-chart-id');
 await tab(page,inactive.id).click({button:'middle'});await noCloseDialog(page);
 await expect(page.locator('[role="tab"][data-tab-id]')).toHaveCount(1);await expect(tab(page,active.id)).toHaveAttribute('aria-selected','true');
 expect(await chartIds(page)).toEqual(mounted);await expect(maximized.getByTestId('chart-cell')).toHaveAttribute('data-chart-id',maximizedId!);
 expect(await page.evaluate(()=>(window as any).__testAttachCount())).toBe(attachments);
 await expect.poll(()=>page.evaluate(()=>(window as any).__testLeases())).toBe(3);
 await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();expect((await saved(page)).tabs[0].charts).toHaveLength(3);
});

test('middle-clicking an inactive tab preserves the focused chart search input',async({page})=>{
 const workspace=await seed(page,1),active=workspace.tabs[1];
 await page.getByRole('button',{name:'Select instrument',exact:true}).click();
 const search=page.getByRole('textbox',{name:'Search instruments',exact:true});
 // Trusted keyboard selection primes Linux primary selection deterministically.
 // Tab closure must not paste it into the still-focused search input.
 await search.fill('Selected instrument text');await search.press('Control+a');await search.fill('ETH');
 await tab(page,workspace.tabs[0].id).click({button:'middle'});
 await expect(page.locator('[role="tab"][data-tab-id]')).toHaveCount(1);await expect(tab(page,active.id)).toHaveAttribute('aria-selected','true');
 await expect(search).toBeFocused();await expect(search).toHaveValue('ETH');await noCloseDialog(page);
});

test('left and right mouse buttons do not close tabs and the final tab cannot be middle-closed',async({page})=>{
 const workspace=await seed(page),first=tab(page,workspace.tabs[0].id),second=tab(page,workspace.tabs[1].id);
 await second.click({button:'right'});await noCloseDialog(page);await expect(first).toHaveAttribute('aria-selected','true');
 await expect(page.locator('[role="tab"][data-tab-id]')).toHaveCount(2);
 for(const attribute of ['aria-haspopup','aria-expanded','aria-controls'])await expect(second).not.toHaveAttribute(attribute);
 await second.click();await expect(second).toHaveAttribute('aria-selected','true');await expect(page.locator('[role="tab"][data-tab-id]')).toHaveCount(2);
 await first.click({button:'middle'});await expect(page.locator('[role="tab"][data-tab-id]')).toHaveCount(1);
 await second.click({button:'middle'});await noCloseDialog(page);await expect(page.locator('[role="tab"][data-tab-id]')).toHaveCount(1);
 await expect(second).toHaveAttribute('aria-selected','true');await expect(page.locator('.app-header').getByRole('button',{name:'Close tab',exact:true})).toHaveCount(0);await second.focus();await page.keyboard.press('Control+w');await noCloseDialog(page);await expect(page.locator('[role="tab"][data-tab-id]')).toHaveCount(1);
});

test('Ctrl+W continues to require confirmation after header Close is removed',async({page})=>{
 const workspace=await seed(page),target=workspace.tabs[0];
 await expect(page.locator('.app-header').getByRole('button',{name:'Close tab',exact:true})).toHaveCount(0);await tab(page,target.id).focus();await page.keyboard.press('Control+w');
 let dialog=page.getByRole('dialog',{name:`Close ${target.name}?`,exact:true});await expect(dialog).toContainText('all 3 charts');
 await dialog.getByRole('button',{name:'Cancel',exact:true}).click();await expect(page.locator('[role="tab"][data-tab-id]')).toHaveCount(2);
 await tab(page,target.id).focus();await page.keyboard.press('Control+w');
 dialog=page.getByRole('dialog',{name:`Close ${target.name}?`,exact:true});await expect(dialog).toBeVisible();
 await dialog.getByRole('button',{name:'Close tab',exact:true}).click();await expect(page.locator('[role="tab"][data-tab-id]')).toHaveCount(1);
 await noCloseDialog(page);
});

test('failed save before middle-click closure preserves its target and all chart configurations',async({page})=>{
 const workspace=await seed(page,1),target=workspace.tabs[0];
 await page.evaluate(()=>(window as any).__testFailSave(true));await page.getByRole('button',{name:'Timeframe 4h',exact:true}).click();
 await tab(page,target.id).click({button:'middle'});await noCloseDialog(page);
 await expect(page.getByRole('status')).toContainText('Workspace save failed');await expect(page.locator('[role="tab"][data-tab-id]')).toHaveCount(2);
 expect((await saved(page)).tabs[0]).toEqual(target);await expect(tab(page,workspace.tabs[1].id)).toHaveAttribute('aria-selected','true');
 await page.evaluate(()=>(window as any).__testFailSave(false));await page.getByRole('button',{name:'Retry save',exact:true}).click();
 await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();await expect(page.locator('[role="tab"][data-tab-id]')).toHaveCount(2);
 await tab(page,target.id).click({button:'middle'});await expect(page.locator('[role="tab"][data-tab-id]')).toHaveCount(1);await noCloseDialog(page);
});

test('repeated middle-clicks waiting on one save close only the captured target',async({page})=>{
 const workspace=await seed(page,1),target=workspace.tabs[0],remaining=workspace.tabs[1];
 await holdFirstSave(page);await page.getByRole('button',{name:'Timeframe 4h',exact:true}).click();
 for(let count=0;count<3;count++)await tab(page,target.id).click({button:'middle'});
 await expect.poll(()=>page.evaluate(()=>typeof (window as any).__middleSaveRelease)).toBe('function');
 await noCloseDialog(page);await expect(page.locator('[role="tab"][data-tab-id]')).toHaveCount(2);
 expect(await page.evaluate(()=>(window as any).__middleSaveCalls)).toBe(1);
 await page.evaluate(()=>(window as any).__middleSaveRelease());
 await expect(page.locator('[role="tab"][data-tab-id]')).toHaveCount(1);await expect(tab(page,remaining.id)).toHaveAttribute('aria-selected','true');
 await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();expect((await saved(page)).tabs.map(value=>value.id)).toEqual([remaining.id]);
});

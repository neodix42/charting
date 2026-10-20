import {test,expect,type Page} from '@playwright/test';
import intermediate from '../../examples/intermediate-two-tabs.json' with {type:'json'};
import type {Workspace} from '../src/types';

const saved=(page:Page):Promise<Workspace>=>page.evaluate(()=>(window as any).__testWorkspace());
const chart=(page:Page,id:string)=>page.locator(`[data-testid="chart-cell"][data-chart-id="${id}"]`);
async function seed(page:Page){
 const workspace=structuredClone(intermediate) as Workspace;
 await page.addInitScript(value=>{const key='cryptodesk-explicit-test-workspace';if(!sessionStorage.getItem(key))sessionStorage.setItem(key,JSON.stringify(value));},workspace);
 await page.goto('/');await expect(page.getByTestId('chart-cell')).toHaveCount(3);
 await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();return workspace;
}

test('chart X has a larger hit target and removes one chart immediately with persistence and lease cleanup',async({page})=>{
 const workspace=await seed(page),target=workspace.tabs[0].charts[1],remaining=workspace.tabs[0].charts.filter(value=>value.id!==target.id);
 const button=chart(page,target.id).getByRole('button',{name:'Remove chart',exact:true});
 const size=await button.evaluate(element=>{const rect=element.getBoundingClientRect();return{width:rect.width,height:rect.height,fontSize:parseFloat(getComputedStyle(element).fontSize),text:element.textContent};});
 expect(size.width).toBeGreaterThanOrEqual(30);expect(size.height).toBeGreaterThanOrEqual(30);expect(size.fontSize).toBeGreaterThanOrEqual(22);expect(size.text).toBe('×');
 await expect.poll(()=>page.evaluate(()=>(window as any).__testLeases())).toBe(3);
 const attachments=await page.evaluate(()=>(window as any).__testAttachCount());
 await button.click();await expect(page.getByRole('dialog')).toHaveCount(0);await expect(page.getByTestId('chart-cell')).toHaveCount(2);
 await expect(chart(page,target.id)).toHaveCount(0);await expect.poll(()=>page.evaluate(()=>(window as any).__testLeases())).toBe(2);
 expect(await page.evaluate(()=>(window as any).__testAttachCount())).toBe(attachments);
 await expect.poll(()=>page.evaluate(()=>window.__cryptodeskDiagnostics?.())).toMatchObject({chartInstances:2,consumers:2,savedTabs:2,savedCharts:3});
 await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();
 const result=await saved(page);expect(result.tabs[0].charts.map(value=>value.id)).toEqual(remaining.map(value=>value.id));expect(result.tabs[1]).toEqual(workspace.tabs[1]);
 await page.reload();await expect(page.getByTestId('chart-cell')).toHaveCount(2);await expect(chart(page,target.id)).toHaveCount(0);
});

test('closing the maximized chart immediately restores its remaining siblings and protects the final chart',async({page})=>{
 const workspace=await seed(page),target=workspace.tabs[0].charts[1];
 await chart(page,target.id).getByRole('button',{name:'Maximize chart',exact:true}).click();await expect(page.locator('.cell-wrapper.maximized')).toHaveCount(1);
 await chart(page,target.id).getByRole('button',{name:'Remove chart',exact:true}).click();
 await expect(page.getByRole('dialog')).toHaveCount(0);await expect(page.getByTestId('chart-cell')).toHaveCount(2);
 await expect(page.locator('.cell-wrapper.maximized,.cell-wrapper.maximized-hidden')).toHaveCount(0);
 for(const cell of await page.getByTestId('chart-cell').all())await expect(cell).toBeVisible();
 await page.getByRole('button',{name:'Remove chart',exact:true}).first().click();
 await expect(page.getByRole('dialog')).toHaveCount(0);await expect(page.getByTestId('chart-cell')).toHaveCount(1);
 const last=page.getByRole('button',{name:'Remove chart',exact:true});await expect(last).toBeDisabled();
 await expect.poll(()=>page.evaluate(()=>(window as any).__testLeases())).toBe(1);
 await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();expect((await saved(page)).tabs[0].charts).toHaveLength(1);
 await page.reload();await expect(page.getByTestId('chart-cell')).toHaveCount(1);await expect(page.getByRole('button',{name:'Remove chart',exact:true})).toBeDisabled();
});

test('rapid chart X clicks stay immediate during a pending save and retain the final chart',async({page})=>{
 const workspace=await seed(page),first=workspace.tabs[0].charts[0],second=workspace.tabs[0].charts[1],last=workspace.tabs[0].charts[2];
 await page.evaluate(()=>{const api=window.go!.main!.App!,save=api.SaveWorkspace.bind(api);(window as any).__chartCloseRelease=null;api.SaveWorkspace=async(...args)=>{api.SaveWorkspace=save;await new Promise<void>(resolve=>(window as any).__chartCloseRelease=resolve);return save(...args);};});
 await chart(page,first.id).getByRole('button',{name:'Timeframe 4h',exact:true}).click();
 await expect.poll(()=>page.evaluate(()=>typeof (window as any).__chartCloseRelease)).toBe('function');
 await chart(page,first.id).getByRole('button',{name:'Remove chart',exact:true}).click();
 await expect(page.getByTestId('chart-cell')).toHaveCount(2);
 await chart(page,second.id).getByRole('button',{name:'Remove chart',exact:true}).click();
 await expect(page.getByTestId('chart-cell')).toHaveCount(1);await expect(chart(page,last.id)).toBeVisible();
 await expect(page.getByRole('dialog')).toHaveCount(0);
 await expect(chart(page,last.id).getByRole('button',{name:'Remove chart',exact:true})).toBeDisabled();
 await page.evaluate(()=>(window as any).__chartCloseRelease());
 await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();
 expect((await saved(page)).tabs[0].charts.map(value=>value.id)).toEqual([last.id]);
 await expect.poll(()=>page.evaluate(()=>(window as any).__testLeases())).toBe(1);
});

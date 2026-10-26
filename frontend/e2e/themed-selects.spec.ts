import {test,expect,type Page} from '@playwright/test';
import {THEME_OPTIONS,getThemePalette} from '../src/workspace/themes';

const menu=(page:Page)=>page.getByRole('listbox');
const rgb=(hex:string)=>`rgb(${[1,3,5].map(offset=>parseInt(hex.slice(offset,offset+2),16)).join(', ')})`;

test('pointer-opened menus use every saved palette and restore normal focus on dismissal',async({page})=>{
 const errors:string[]=[];page.on('pageerror',error=>errors.push(error.message));
 await page.goto('/');await page.getByRole('button',{name:'Open settings',exact:true}).click();
 const theme=page.getByLabel('Theme',{exact:true});
 for(const {id,label} of THEME_OPTIONS){
  await theme.click();await expect(menu(page)).toBeVisible();
  await menu(page).getByRole('option',{name:label,exact:true}).click();
  await expect(menu(page)).toHaveCount(0);await expect(theme).toHaveValue(id);await expect(theme).toBeFocused();
  await theme.click();await expect(menu(page)).toHaveCSS('background-color',rgb(getThemePalette(id).elevated));
  await expect(menu(page).getByRole('option',{selected:true})).toContainText(label);
  await expect(theme).toHaveAttribute('aria-expanded','true');
  expect(await theme.getAttribute('aria-controls')).toBe(await menu(page).getAttribute('id'));
  const active=await theme.getAttribute('aria-activedescendant');expect(active).toBeTruthy();
  await expect(page.locator(`[id="${active}"]`)).toHaveAttribute('role','option');
  await theme.press('Escape');await expect(menu(page)).toHaveCount(0);await expect(theme).toBeFocused();
  await expect(theme).not.toHaveAttribute('aria-expanded');await expect(theme).not.toHaveAttribute('aria-controls');await expect(theme).not.toHaveAttribute('aria-activedescendant');
 }
 await theme.press('F4');await expect(menu(page)).toBeVisible();await theme.press('F4');await expect(menu(page)).toHaveCount(0);
 await theme.press('Alt+ArrowDown');await expect(menu(page)).toBeVisible();await theme.press('Alt+ArrowUp');await expect(menu(page)).toHaveCount(0);
 await theme.press('ArrowDown');await theme.press('Home');await theme.press('ArrowDown');await theme.press('ArrowDown');await theme.press('Enter');
 await expect(theme).toHaveValue('matrix');await expect(menu(page)).toHaveCount(0);
 await theme.click();await theme.press('Tab');await expect(menu(page)).toHaveCount(0);await expect(theme).not.toBeFocused();
 await page.getByRole('button',{name:'Close settings',exact:true}).click();
 await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();
 await page.reload();await expect.poll(()=>page.evaluate(()=>document.documentElement.dataset.theme)).toBe('matrix');
 expect(errors).toEqual([]);
});

test('grouped Screener indicator menus keep options readable, scrollable and actionable',async({page})=>{
 await page.setViewportSize({width:1000,height:800});await page.goto('/');
 await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Enrichment',exact:true}).click();
 const indicator=page.getByLabel('Enrichment indicator',{exact:true});await expect(indicator.locator('option')).toHaveCount(42);
 await indicator.click();await expect(menu(page).getByRole('option')).toHaveCount(42);expect(await menu(page).getByRole('group').count()).toBeGreaterThan(1);
 await expect(menu(page)).toHaveCSS('background-color',rgb(getThemePalette('dark').elevated));
 const bounds=await menu(page).boundingBox();expect(bounds!.x).toBeGreaterThanOrEqual(8);expect(bounds!.y+bounds!.height).toBeLessThanOrEqual(792);
 await indicator.press('End');await expect(menu(page).locator('[data-active=true]')).toBeInViewport();await indicator.press('Enter');
 await expect(menu(page)).toHaveCount(0);await expect(indicator).toBeFocused();
 expect(await indicator.inputValue()).toBe(await indicator.locator('option').last().getAttribute('value'));
 await indicator.click();await page.getByRole('tab',{name:'Analysis',exact:true}).click();await expect(menu(page)).toHaveCount(0);
});

test('async options, disabled groups and removed controls never select stale entries or retain menus',async({page})=>{
 await page.goto('/');
 await page.evaluate(()=>{
  const select=document.createElement('select');select.setAttribute('aria-label','Async choices');select.style.cssText='position:fixed;left:25px;top:150px;z-index:90';
  select.innerHTML='<option value="alpha">Alpha</option><optgroup label="Unavailable" disabled><option value="beta">Beta</option></optgroup><option value="gamma">Gamma</option>';
  document.body.append(select);(window as any).__choiceEvents=[];
  for(const type of ['input','change'])select.addEventListener(type,()=>{(window as any).__choiceEvents.push([type,select.value]);});
 });
 const select=page.getByLabel('Async choices',{exact:true});await select.click();
 await expect(menu(page).getByRole('option',{name:'Beta',exact:true})).toHaveAttribute('aria-disabled','true');
 await select.press('ArrowDown');await select.press('Enter');await expect(select).toHaveValue('gamma');
 expect(await page.evaluate(()=>(window as any).__choiceEvents)).toEqual([['input','gamma'],['change','gamma']]);
 await select.click();
 await select.evaluate(element=>{(element as HTMLSelectElement).insertBefore(new Option('New entry','new'),element.firstChild);});
 await expect(menu(page).getByRole('option')).toHaveCount(4);await expect(menu(page).getByRole('option',{selected:true})).toContainText('Gamma');
 await menu(page).getByRole('option',{name:'Alpha',exact:true}).click();await expect(select).toHaveValue('alpha');
 await select.click();await select.press('g');await select.press('Enter');await expect(select).toHaveValue('gamma');
 await select.click();await select.evaluate(element=>element.remove());await expect(menu(page)).toHaveCount(0);
 await page.getByRole('button',{name:'Open settings',exact:true}).click();await page.getByLabel('Theme',{exact:true}).click();
 await page.getByRole('button',{name:'Close settings',exact:true}).click();await expect(menu(page)).toHaveCount(0);
});

import {test,expect,type Page} from '@playwright/test';
import {openTestApp,reloadTestApp} from './helpers/testApp';

const dialog=(page:Page)=>page.getByRole('dialog',{name:'Workspace settings',exact:true});
const form=(page:Page)=>page.getByRole('form',{name:'Screener worker threads',exact:true});
const input=(page:Page)=>form(page).getByRole('spinbutton',{name:'Screener worker threads',exact:true});
const apply=(page:Page)=>form(page).getByRole('button',{name:'Apply Screener worker threads',exact:true});
const applied=(page:Page)=>form(page).getByRole('status',{name:'Applied Screener worker threads',exact:true});
async function settings(page:Page){await page.getByRole('button',{name:'Open settings',exact:true}).click();await dialog(page).getByRole('tab',{name:'Screener',exact:true}).click();await expect(input(page)).toBeEnabled();}
async function downloader(page:Page){await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Downloader',exact:true}).click();await expect(page.getByRole('checkbox',{name:'Select all symbols',exact:true})).toBeEnabled();}

test('worker threads default to five and Apply enforces whole values from one through twenty',async({page})=>{
 await openTestApp(page);await settings(page);await expect(input(page)).toHaveValue('5');await expect(input(page)).toHaveAttribute('min','1');await expect(input(page)).toHaveAttribute('max','20');await expect(apply(page)).toBeDisabled();
 await expect(form(page)).toContainText('Downloader, Enrichment and Refresh');await expect(form(page)).toContainText('Changes apply to subsequent runs');
 for(const value of ['0','21','1.5','']){await input(page).fill(value);await expect(apply(page)).toBeDisabled();await expect(form(page).getByText('Enter a whole number from 1 to 20.',{exact:true})).toBeVisible();await form(page).dispatchEvent('submit');expect(await page.evaluate(()=>(window as any).__testScreener().concurrency)).toBe(5);}
 for(const value of ['1','20']){await input(page).fill(value);await apply(page).click();await expect(applied(page)).toHaveText(`Applied: ${value} ${value==='1'?'worker':'workers'}`);expect(await page.evaluate(()=>(window as any).__testScreener().concurrency)).toBe(Number(value));}
 expect(JSON.stringify(await page.evaluate(()=>(window as any).__testWorkspace()))).not.toContain('concurrency');
});

test('worker and folder preferences coexist across restart and every download uses the latest applied setting',async({page})=>{
 await openTestApp(page);await settings(page);await input(page).fill('17');await apply(page).click();await expect(applied(page)).toHaveText('Applied: 17 workers');
 const storage=page.getByRole('form',{name:'Screener data storage',exact:true});await storage.getByLabel('Screener download folder',{exact:true}).fill('/chosen parallel data');await storage.getByRole('button',{name:'Apply Screener download folder',exact:true}).click();await expect(storage.getByRole('status',{name:'Applied Screener folder',exact:true})).toContainText('/chosen parallel data');
 await reloadTestApp(page);await settings(page);await expect(input(page)).toHaveValue('17');await expect(storage.getByLabel('Screener download folder',{exact:true})).toHaveValue('/chosen parallel data');
 await dialog(page).getByRole('button',{name:'Close settings',exact:true}).click();await downloader(page);await expect(page.getByLabel('Download summary',{exact:true})).toContainText('17 worker threads');await page.getByRole('checkbox',{name:'Select all Cryptocurrencies',exact:true}).check();
 // Change the authoritative preference after the Downloader has loaded. Start
 // must obtain it again, rather than submitting its previous polling snapshot.
 await page.evaluate(()=>window.go!.main!.App!.ConfigureScreenerConcurrency(4));await page.getByRole('button',{name:'Download 2 symbols',exact:true}).click();
 expect(await page.evaluate(()=>(window as any).__testScreenerRequest().concurrency)).toBe(4);await expect(page.getByLabel('Download summary',{exact:true})).toContainText('4 worker threads');
});

test('active downloads reject worker changes and retain the applied value until cancelled',async({page})=>{
 await openTestApp(page);await downloader(page);await page.getByRole('checkbox',{name:'Select all symbols',exact:true}).check();await page.getByRole('button',{name:'Download 4 symbols',exact:true}).click();await settings(page);await input(page).fill('8');await apply(page).click();
 await expect(form(page).getByRole('alert')).toContainText('Finish or cancel active Screener work');await expect(applied(page)).toContainText('Applied: 5 workers');await expect(input(page)).toHaveValue('8');expect(await page.evaluate(()=>(window as any).__testScreener().concurrency)).toBe(5);
 await dialog(page).getByRole('button',{name:'Close settings',exact:true}).click();await page.getByRole('button',{name:'Cancel downloads',exact:true}).click();await settings(page);await expect(input(page)).toHaveValue('5');await input(page).fill('8');await apply(page).click();await expect(applied(page)).toHaveText('Applied: 8 workers');
});

for(const kind of ['Enrichment','Refresh'] as const){
 test(`active ${kind.toLowerCase()} rejects worker changes without changing the saved preference`,async({page})=>{
  await openTestApp(page);await settings(page);await page.evaluate(kind=>(window as any)[`__testSet${kind}`]({active:true}),kind);await input(page).fill('10');await apply(page).click();
  await expect(form(page).getByRole('alert')).toContainText('Finish or cancel active Screener work');await expect(applied(page)).toContainText('Applied: 5 workers');expect(await page.evaluate(()=>(window as any).__testScreener().concurrency)).toBe(5);
  await page.evaluate(kind=>(window as any)[`__testSet${kind}`]({active:false}),kind);await apply(page).click();await expect(applied(page)).toHaveText('Applied: 10 workers');
 });
}

test('worker Apply writes once and normal close waits even after moving to another settings page',async({page})=>{
 await openTestApp(page);await settings(page);
 await page.evaluate(()=>{const api=window.go!.main!.App!,save=api.ConfigureScreenerConcurrency;(window as any).__workerApplyCount=0;api.ConfigureScreenerConcurrency=value=>{(window as any).__workerApplyCount++;return new Promise(resolve=>(window as any).__releaseWorkerApply=async()=>resolve(await save(value)));};});
 await input(page).fill('12');await apply(page).click();await form(page).dispatchEvent('submit');expect(await page.evaluate(()=>(window as any).__workerApplyCount)).toBe(1);
 await dialog(page).getByRole('tab',{name:'Appearance',exact:true}).click();await page.evaluate(()=>(window as any).__testEmit('prepare-close',{token:'worker-settings'}));expect(await page.evaluate(()=>(window as any).__testCloseConfirmation())).toBeNull();
 await page.evaluate(()=>(window as any).__releaseWorkerApply());await expect.poll(()=>page.evaluate(()=>(window as any).__testCloseConfirmation())).toEqual({token:'worker-settings',discard:false});expect(await page.evaluate(()=>(window as any).__testScreener().concurrency)).toBe(12);
});

test('failed worker preference writes preserve the old count and pause an already pending close',async({page})=>{
 await openTestApp(page);await settings(page);
 await page.evaluate(()=>{const api=window.go!.main!.App!;(window as any).__originalWorkerConfigure=api.ConfigureScreenerConcurrency;api.ConfigureScreenerConcurrency=()=>new Promise((_resolve,reject)=>(window as any).__rejectWorkerApply=()=>reject(Error('Could not save worker setting: disk full')));});
 await input(page).fill('11');await apply(page).click();await page.evaluate(()=>(window as any).__testEmit('prepare-close',{token:'worker-settings-failure'}));await page.evaluate(()=>(window as any).__rejectWorkerApply());
 await expect(form(page).getByRole('alert')).toContainText('disk full');await expect(applied(page)).toContainText('Applied: 5 workers');await expect(page.getByText('Save failed during shutdown. Your changes remain open.',{exact:true})).toBeVisible();expect(await page.evaluate(()=>(window as any).__testCloseConfirmation())).toBeNull();
 await page.evaluate(()=>{window.go!.main!.App!.ConfigureScreenerConcurrency=(window as any).__originalWorkerConfigure;});await apply(page).click();await expect(applied(page)).toHaveText('Applied: 11 workers');
});

test('a load failure can retry and an abandoned hidden-page read cannot replace restored worker settings',async({page})=>{
 await openTestApp(page);await page.evaluate(()=>{const api=window.go!.main!.App!;(window as any).__originalWorkerGet=api.GetScreener;api.GetScreener=async()=>{throw Error('Worker settings unavailable');};});await page.getByRole('button',{name:'Open settings',exact:true}).click();await dialog(page).getByRole('tab',{name:'Screener',exact:true}).click();
 await expect(form(page).getByRole('alert')).toContainText('Worker settings unavailable');await expect(input(page)).toBeDisabled();
 await page.evaluate(()=>{const api=window.go!.main!.App!;api.GetScreener=(window as any).__originalWorkerGet;const original=api.GetScreener;api.GetScreener=async()=>{api.GetScreener=original;const value=await original();return new Promise(resolve=>(window as any).__releaseOldWorkers=()=>resolve(value));};});
 await form(page).getByRole('button',{name:'Retry worker settings',exact:true}).click();await expect.poll(()=>page.evaluate(()=>typeof(window as any).__releaseOldWorkers)).toBe('function');await dialog(page).getByRole('tab',{name:'Appearance',exact:true}).click();await page.evaluate(()=>window.go!.main!.App!.ConfigureScreenerConcurrency(9));await dialog(page).getByRole('tab',{name:'Screener',exact:true}).click();await expect(input(page)).toHaveValue('9');await page.evaluate(()=>(window as any).__releaseOldWorkers());await expect(input(page)).toHaveValue('9');
});

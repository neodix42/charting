import {test,expect,type Page} from '@playwright/test';
import {openTestApp} from './helpers/testApp';

const dialog=(page:Page)=>page.getByRole('dialog',{name:'Workspace settings',exact:true});
const token=(page:Page)=>dialog(page).getByLabel('Telegram Bot API Token',{exact:true});
const save=(page:Page)=>dialog(page).getByRole('button',{name:'Save Telegram Bot settings',exact:true});
async function openSettings(page:Page){
 await page.getByRole('button',{name:'Open settings',exact:true}).click();
 await dialog(page).getByRole('tab',{name:'Telegram Bot',exact:true}).click();
}

test('Telegram Bot has one masked field, saves locally across restart, clears and stays out of diagnostics/workspace',async({page})=>{
 const requests:string[]=[];page.on('request',request=>{if(request.url().includes('telegram.org'))requests.push(request.url());});
 await openTestApp(page);
 await page.evaluate(()=>{const api=window.go!.main!.App!,read=api.GetTelegramBotSettings;(window as any).__telegramReads=0;api.GetTelegramBotSettings=()=>{(window as any).__telegramReads++;return read();};});
 await page.getByRole('button',{name:'Open settings',exact:true}).click();
 expect(await page.evaluate(()=>(window as any).__telegramReads)).toBe(0);
 await dialog(page).getByRole('tab',{name:'Telegram Bot',exact:true}).click();
 const form=dialog(page).getByRole('form',{name:'Telegram Bot settings',exact:true});
 await expect(form.locator('input')).toHaveCount(1);await expect(token(page)).toHaveAttribute('type','password');
 await expect(token(page)).toHaveAttribute('autocomplete','off');await expect(token(page)).toBeEnabled();await expect(save(page)).toBeDisabled();
 await token(page).fill('  123456:explicit_test_token  ');await save(page).click();
 await expect(form.getByRole('status')).toHaveText('Telegram Bot settings saved.');await expect(token(page)).toHaveValue('123456:explicit_test_token');
 expect(JSON.stringify(await page.evaluate(()=>(window as any).__testWorkspace()))).not.toContain('explicit_test_token');
 await dialog(page).getByRole('tab',{name:'Debug',exact:true}).click();await dialog(page).getByRole('button',{name:'Refresh diagnostics',exact:true}).click();
 await expect(dialog(page).locator('.diagnostics')).not.toContainText('explicit_test_token');
 await page.reload();await expect(page.getByRole('button',{name:'Open settings',exact:true})).toBeVisible();await openSettings(page);
 await expect(token(page)).toHaveValue('123456:explicit_test_token');await token(page).fill('');await save(page).click();
 await expect(save(page)).toBeDisabled();await expect(token(page)).toHaveValue('');
 await dialog(page).getByRole('button',{name:'Close settings',exact:true}).click();await openSettings(page);await expect(token(page)).toHaveValue('');
 expect(requests).toEqual([]);
});

test('Telegram save failure preserves the applied token and draft without exposing backend errors, then retries',async({page})=>{
 await openTestApp(page);
 await page.evaluate(async()=>{
  const api=window.go!.main!.App!;await api.ConfigureTelegramBot('123:original');const configure=api.ConfigureTelegramBot;let failed=false;
  api.ConfigureTelegramBot=async token=>{if(!failed){failed=true;throw Error(`backend error containing ${token}`);}return configure(token);};
 });
 await openSettings(page);await expect(token(page)).toHaveValue('123:original');await token(page).fill('456:private_new_token');await save(page).click();
 await expect(dialog(page).getByRole('alert')).toHaveText('Could not save Telegram Bot settings. Please retry.');
 await expect(dialog(page)).not.toContainText('private_new_token');await expect(token(page)).toHaveValue('456:private_new_token');
 expect((await page.evaluate(()=>window.go!.main!.App!.GetTelegramBotSettings())).apiToken).toBe('123:original');
 await save(page).click();await expect(dialog(page).getByRole('status')).toHaveText('Telegram Bot settings saved.');
 expect((await page.evaluate(()=>window.go!.main!.App!.GetTelegramBotSettings())).apiToken).toBe('456:private_new_token');
});

test('Telegram load errors retry safely and a late hidden-page read cannot replace an edited token',async({page})=>{
 await openTestApp(page);
 await page.evaluate(()=>{
  const api=window.go!.main!.App!,read=api.GetTelegramBotSettings;let count=0;
  api.GetTelegramBotSettings=async()=>{count++;if(count===1)throw Error('private_secret_in_load_error');if(count===2)return new Promise(resolve=>{(window as any).__releaseTelegramRead=()=>resolve({apiToken:'stale-token'});});return read();};
 });
 await openSettings(page);await expect(dialog(page).getByRole('alert')).toContainText('Could not load Telegram Bot settings');
 await expect(dialog(page)).not.toContainText('private_secret_in_load_error');await expect(token(page)).toBeDisabled();
 await dialog(page).getByRole('button',{name:'Retry Telegram settings',exact:true}).click();
 await expect.poll(()=>page.evaluate(()=>typeof (window as any).__releaseTelegramRead)).toBe('function');
 await dialog(page).getByRole('tab',{name:'Appearance',exact:true}).click();await dialog(page).getByRole('tab',{name:'Telegram Bot',exact:true}).click();
 await expect(token(page)).toBeEnabled();await token(page).fill('123:current_draft');await page.evaluate(()=>(window as any).__releaseTelegramRead());
 await expect(token(page)).toHaveValue('123:current_draft');
});

test('closing settings and normal application shutdown wait for a pending Telegram save even on another page',async({page})=>{
 await openTestApp(page);
 await page.evaluate(()=>{
  const api=window.go!.main!.App!,configure=api.ConfigureTelegramBot;
  api.ConfigureTelegramBot=value=>new Promise(resolve=>{(window as any).__releaseTelegramSave=async()=>resolve(await configure(value));});
 });
 await openSettings(page);await expect(token(page)).toBeEnabled();await token(page).fill('123:pending_token');await save(page).click();
 await expect.poll(()=>page.evaluate(()=>typeof (window as any).__releaseTelegramSave)).toBe('function');
 await dialog(page).getByRole('tab',{name:'Appearance',exact:true}).click();await dialog(page).getByRole('button',{name:'Close settings',exact:true}).click();
 await page.evaluate(()=>(window as any).__testEmit('prepare-close',{token:'telegram-save'}));
 await page.waitForTimeout(100);await expect(dialog(page)).toBeVisible();expect(await page.evaluate(()=>(window as any).__testCloseConfirmation())).toBeNull();
 await page.evaluate(()=>(window as any).__releaseTelegramSave());await expect(dialog(page)).toHaveCount(0);
 await expect.poll(()=>page.evaluate(()=>(window as any).__testCloseConfirmation())).toEqual({token:'telegram-save',discard:false});
 expect((await page.evaluate(()=>window.go!.main!.App!.GetTelegramBotSettings())).apiToken).toBe('123:pending_token');
});

test('a failed pending token save pauses shutdown without exposing the token and can be retried',async({page})=>{
 await openTestApp(page);
 await page.evaluate(()=>{
  const api=window.go!.main!.App!,configure=api.ConfigureTelegramBot;let first=true;
  api.ConfigureTelegramBot=value=>{if(!first)return configure(value);first=false;return new Promise((_,reject)=>{(window as any).__rejectTelegramSave=()=>reject(Error(`backend rejected ${value}`));});};
 });
 await openSettings(page);await expect(token(page)).toBeEnabled();await token(page).fill('123:private_shutdown_token');await save(page).click();
 await expect.poll(()=>page.evaluate(()=>typeof (window as any).__rejectTelegramSave)).toBe('function');
 await page.evaluate(()=>(window as any).__testEmit('prepare-close',{token:'telegram-failure'}));
 await page.waitForTimeout(100);await page.evaluate(()=>(window as any).__rejectTelegramSave());
 await expect(page.getByText('Save failed during shutdown. Your changes remain open.',{exact:true})).toBeVisible();
 expect(await page.evaluate(()=>(window as any).__testCloseConfirmation())).toBeNull();await expect(page.locator('body')).not.toContainText('private_shutdown_token');
 await expect(token(page)).toHaveValue('123:private_shutdown_token');await save(page).click();await expect(dialog(page).getByRole('status')).toHaveText('Telegram Bot settings saved.');
 await page.getByRole('button',{name:'Retry save and close',exact:true}).click();
 await expect.poll(()=>page.evaluate(()=>(window as any).__testCloseConfirmation())).toEqual({token:'telegram-failure',discard:false});
});

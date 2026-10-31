import {test,expect,type Page} from '@playwright/test';
import {openTestApp} from './helpers/testApp';

const dialog=(page:Page)=>page.getByRole('dialog',{name:'Workspace settings',exact:true});
const token=(page:Page)=>dialog(page).getByLabel('Telegram Bot API Token',{exact:true});
const save=(page:Page)=>dialog(page).getByRole('button',{name:'Save Telegram Bot settings',exact:true});
async function openSettings(page:Page){
 await page.getByRole('button',{name:'Open settings',exact:true}).click();
 await dialog(page).getByRole('tab',{name:'Telegram Bot',exact:true}).click();
}

test('Telegram Bot has a masked token and Recipient, saves locally across restart, clears and stays out of diagnostics/workspace',async({page})=>{
 const requests:string[]=[];page.on('request',request=>{if(request.url().includes('telegram.org'))requests.push(request.url());});
 await openTestApp(page);
 await page.evaluate(()=>{const api=window.go!.main!.App!,read=api.GetTelegramBotSettings;(window as any).__telegramReads=0;api.GetTelegramBotSettings=()=>{(window as any).__telegramReads++;return read();};});
 await page.getByRole('button',{name:'Open settings',exact:true}).click();
 expect(await page.evaluate(()=>(window as any).__telegramReads)).toBe(0);
 await dialog(page).getByRole('tab',{name:'Telegram Bot',exact:true}).click();
 const form=dialog(page).getByRole('form',{name:'Telegram Bot settings',exact:true});
 await expect(form.locator('input')).toHaveCount(2);await expect(token(page)).toHaveAttribute('type','password');
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
  const api=window.go!.main!.App!;await api.ConfigureTelegramBot('123:original');const configure=api.ConfigureTelegramBotSettings;let failed=false;
  api.ConfigureTelegramBotSettings=async(token,recipient)=>{if(!failed){failed=true;throw Error(`backend error containing ${token}`);}return configure(token,recipient);};
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
  api.GetTelegramBotSettings=async()=>{count++;if(count===1)throw Error('private_secret_in_load_error');if(count===2)return new Promise(resolve=>{(window as any).__releaseTelegramRead=()=>resolve({apiToken:'stale-token',recipient:''});});return read();};
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
  const api=window.go!.main!.App!,configure=api.ConfigureTelegramBotSettings;
  api.ConfigureTelegramBotSettings=(value,recipient)=>new Promise(resolve=>{(window as any).__releaseTelegramSave=async()=>resolve(await configure(value,recipient));});
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
  const api=window.go!.main!.App!,configure=api.ConfigureTelegramBotSettings;let first=true;
  api.ConfigureTelegramBotSettings=(value,recipient)=>{if(!first)return configure(value,recipient);first=false;return new Promise((_,reject)=>{(window as any).__rejectTelegramSave=()=>reject(Error(`backend rejected ${value}`));});};
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

test('Recipient shows the actual bot Start instructions, saves before Test, and persists across restart',async({page})=>{
 await openTestApp(page);
 await page.evaluate(()=>{
  const api=window.go!.main!.App!;(window as any).__telegramTestCalls=0;
  api.TestTelegramBot=async()=>{(window as any).__telegramTestCalls++;const settings=await api.GetTelegramBotSettings();(window as any).__testedTelegramSettings=settings;return{sent:true,botUsername:'ExplicitCryptoDeskBot',messageId:42};};
 });
 await openSettings(page);await expect(token(page)).toBeEnabled();await token(page).fill('123:explicit');
 const recipient=dialog(page).getByLabel('Recipient',{exact:true}),testButton=dialog(page).getByRole('button',{name:'Test',exact:true});
 await expect(testButton).toBeDisabled();await recipient.fill('@neodix');
 await expect(dialog(page).getByRole('note')).toContainText('@neodix must open bot @ExplicitCryptoDeskBot in Telegram and press Start. Then click Test.');
 await testButton.click();await expect(dialog(page).getByRole('status')).toHaveText('Test notification sent.');
 expect(await page.evaluate(()=>(window as any).__telegramTestCalls)).toBe(1);
 expect(await page.evaluate(()=>(window as any).__testedTelegramSettings)).toEqual({apiToken:'123:explicit',recipient:'@neodix'});
 await page.reload();await expect(page.getByRole('button',{name:'Open settings',exact:true})).toBeVisible();await openSettings(page);
 await expect(recipient).toHaveValue('@neodix');await expect(token(page)).toHaveValue('123:explicit');
 await recipient.fill('42');await expect(dialog(page).getByRole('note')).toHaveCount(0);await expect(testButton).toBeEnabled();
 await recipient.fill('@bad name');await expect(testButton).toBeDisabled();await expect(save(page)).toBeDisabled();
});

test('Test failures explain Start and delivery uncertainty without leaking provider errors or automatically retrying',async({page})=>{
 await openTestApp(page);await openSettings(page);await expect(token(page)).toBeEnabled();await token(page).fill('123:private_token');await dialog(page).getByLabel('Recipient',{exact:true}).fill('@neodix');
 await page.evaluate(()=>{(window as any).__telegramTestCalls=0;window.go!.main!.App!.TestTelegramBot=async()=>{(window as any).__telegramTestCalls++;return{sent:false,code:'recipientNotFound',botUsername:'ExplicitCryptoDeskBot'};};});
 const testButton=dialog(page).getByRole('button',{name:'Test',exact:true});await testButton.click();
 await expect(dialog(page).getByRole('alert')).toContainText('press Start (or send /start again), then click Test');
 expect(await page.evaluate(()=>(window as any).__telegramTestCalls)).toBe(1);
 await page.evaluate(()=>{window.go!.main!.App!.TestTelegramBot=async()=>({sent:false,code:'webhook'});});await testButton.click();
 await expect(dialog(page).getByRole('alert')).toHaveText('This bot uses a webhook. Enter the recipient’s numeric private chat ID for testing. The webhook was not changed.');
 await page.evaluate(()=>{window.go!.main!.App!.TestTelegramBot=async()=>{(window as any).__telegramTestCalls++;throw Error('private_token transport payload');};});
 await testButton.click();await expect(dialog(page).getByRole('alert')).toContainText('Delivery was not confirmed. Check Telegram before retrying');
 await expect(page.locator('body')).not.toContainText('private_token');expect(await page.evaluate(()=>(window as any).__telegramTestCalls)).toBe(2);
});

test('bot identity responses cannot replace a newer token and repeated Test clicks send once',async({page})=>{
 await openTestApp(page);
 await page.evaluate(()=>{
  const api=window.go!.main!.App!;
  api.GetTelegramBotIdentity=async token=>token==='123:first'?new Promise(resolve=>{(window as any).__releaseOldBot=()=>resolve({id:1,username:'OldBot'});}):{id:2,username:'NewBot'};
  (window as any).__telegramTestCalls=0;api.TestTelegramBot=()=>{(window as any).__telegramTestCalls++;return new Promise(resolve=>{(window as any).__releaseTestNotification=()=>resolve({sent:true,botUsername:'NewBot',messageId:7});});};
 });
 await openSettings(page);await expect(token(page)).toBeEnabled();await token(page).fill('123:first');const recipient=dialog(page).getByLabel('Recipient',{exact:true});await recipient.fill('@neodix');
 await expect.poll(()=>page.evaluate(()=>typeof (window as any).__releaseOldBot)).toBe('function');
 await token(page).fill('456:second');await expect(dialog(page).getByRole('note')).toContainText('@NewBot');
 await page.evaluate(()=>(window as any).__releaseOldBot());await expect(dialog(page).getByRole('note')).not.toContainText('@OldBot');
 await dialog(page).getByRole('button',{name:'Test',exact:true}).dblclick();await expect(dialog(page).getByRole('button',{name:'Testing…',exact:true})).toBeDisabled();await expect(recipient).toBeDisabled();
 expect(await page.evaluate(()=>(window as any).__telegramTestCalls)).toBe(1);
 await page.evaluate(()=>(window as any).__releaseTestNotification());await expect(dialog(page).getByRole('status')).toHaveText('Test notification sent.');
});

test('a late bot-name failure cannot replace the identity verified by a successful Test',async({page})=>{
 await openTestApp(page);
 await page.evaluate(()=>{
  const api=window.go!.main!.App!;api.GetTelegramBotIdentity=()=>new Promise((_,reject)=>{(window as any).__rejectOldBotName=()=>reject(Error('private provider error'));});
  api.TestTelegramBot=async()=>({sent:true,botUsername:'VerifiedBot',messageId:8});
 });
 await openSettings(page);await expect(token(page)).toBeEnabled();await token(page).fill('123:token');await dialog(page).getByLabel('Recipient',{exact:true}).fill('@neodix');
 await expect.poll(()=>page.evaluate(()=>typeof (window as any).__rejectOldBotName)).toBe('function');
 await dialog(page).getByRole('button',{name:'Test',exact:true}).click();await expect(dialog(page).getByRole('status')).toHaveText('Test notification sent.');
 await expect(dialog(page).getByRole('note')).toContainText('@VerifiedBot');await page.evaluate(()=>(window as any).__rejectOldBotName());
 await expect(dialog(page).getByRole('note')).toContainText('@VerifiedBot');await expect(dialog(page).getByRole('button',{name:'Retry bot name',exact:true})).toHaveCount(0);
});

test('comma-separated recipients normalize and persist, with Start help for usernames after numeric IDs',async({page})=>{
 const requests:string[]=[];page.on('request',request=>{if(request.url().includes('telegram.org'))requests.push(request.url());});
 await openTestApp(page);await openSettings(page);await expect(token(page)).toBeEnabled();await token(page).fill('123:multi_recipient_fixture');
 const recipient=dialog(page).getByLabel('Recipient',{exact:true}),testButton=dialog(page).getByRole('button',{name:'Test',exact:true});
 await expect(recipient).toHaveAttribute('maxlength','4096');
 await recipient.fill(' 00042 , @Alice, @ALICE, +0042, @Bob ');
 await expect(dialog(page).getByRole('note')).toContainText('@Alice, @Bob must open bot @ExplicitCryptoDeskBot');
 await expect(testButton).toBeEnabled();await save(page).click();
 await expect(recipient).toHaveValue('42, @Alice, @Bob');
 expect((await page.evaluate(()=>window.go!.main!.App!.GetTelegramBotSettings())).recipient).toBe('42, @Alice, @Bob');
 await testButton.click();await expect(dialog(page).getByRole('status')).toHaveText('Test notifications sent to 3 recipients.');
 await page.reload();await expect(page.getByRole('button',{name:'Open settings',exact:true})).toBeVisible();await openSettings(page);
 await expect(recipient).toHaveValue('42, @Alice, @Bob');
 expect(requests).toEqual([]);
});

test('comma-separated recipient validation rejects incomplete lists and enforces the distinct-recipient limit',async({page})=>{
 await openTestApp(page);await openSettings(page);await expect(token(page)).toBeEnabled();await token(page).fill('123:multi_validation_fixture');
 const recipient=dialog(page).getByLabel('Recipient',{exact:true}),testButton=dialog(page).getByRole('button',{name:'Test',exact:true});
 for(const value of ['42,','42,,@alice','@alice,-42','@alice,0','42,@bad name',Array.from({length:21},(_,index)=>`@person_${index}`).join(',')]){
  await recipient.fill(value);await expect(testButton).toBeDisabled();await expect(save(page)).toBeDisabled();
  await expect(dialog(page).getByText('Use up to 20 @usernames or positive private chat IDs separated by commas, without empty entries.',{exact:true})).toBeVisible();
 }
 await recipient.fill(Array.from({length:20},(_,index)=>`@person_${index}`).concat('@PERSON_0').join(','));
 await expect(testButton).toBeEnabled();await expect(save(page)).toBeEnabled();
 await recipient.fill('');await expect(testButton).toBeDisabled();await expect(save(page)).toBeEnabled();
});

test('partial multi-recipient tests disclose delivered counts and never retry automatically',async({page})=>{
 await openTestApp(page);await openSettings(page);await expect(token(page)).toBeEnabled();await token(page).fill('123:multi_partial_fixture');
 await dialog(page).getByLabel('Recipient',{exact:true}).fill('42, @not_started');
 await page.evaluate(()=>{(window as any).__multiTelegramTests=0;window.go!.main!.App!.TestTelegramBot=async()=>{(window as any).__multiTelegramTests++;return{sent:false,code:'recipientNotFound',botUsername:'ExplicitCryptoDeskBot',sentCount:1,totalRecipients:2};};});
 const testButton=dialog(page).getByRole('button',{name:'Test',exact:true});await testButton.click();
 await expect(dialog(page).getByRole('alert')).toContainText('Test notifications sent to 1 of 2 recipients.');
 await expect(dialog(page).getByRole('alert')).toContainText('testing again also notifies recipients who already received this test.');
 expect(await page.evaluate(()=>(window as any).__multiTelegramTests)).toBe(1);
 await page.evaluate(()=>{window.go!.main!.App!.TestTelegramBot=async()=>({sent:false,code:'rateLimit',sentCount:0,totalRecipients:2});});
 await testButton.click();await expect(dialog(page).getByRole('alert')).toContainText('No test notifications were confirmed for 2 recipients.');
 await expect(dialog(page).getByRole('alert')).toContainText('Telegram is limiting requests.');
});

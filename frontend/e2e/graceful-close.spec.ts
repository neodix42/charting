import {test,expect} from '@playwright/test';
import {openTestApp} from './helpers/testApp';

test('native close shows progress while saving and during backend cleanup, and failures restore the open workspace',async({page})=>{
 await openTestApp(page);await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();
 const closing=page.getByRole('status').filter({hasText:'Closing gracefully...'});await expect(closing).toHaveCount(0);
 await page.evaluate(()=>{const api=window.go!.main!.App!,save=api.SaveWorkspace;(window as any).__closingOriginalSave=save;api.SaveWorkspace=async(...args)=>{await new Promise<void>(resolve=>{(window as any).__releaseClosingSave=resolve;});return save(...args);};});
 await page.getByLabel('Timeframe 4h',{exact:true}).click();await page.evaluate(()=>(window as any).__testEmit('prepare-close',{token:'slow-native-close'}));
 await expect(closing).toBeVisible();expect(await page.evaluate(()=>(window as any).__testCloseConfirmation())).toBeNull();
 await expect.poll(()=>page.evaluate(()=>typeof(window as any).__releaseClosingSave)).toBe('function');await page.evaluate(()=>(window as any).__releaseClosingSave());
 await expect.poll(()=>page.evaluate(()=>(window as any).__testCloseConfirmation())).toEqual({token:'slow-native-close',discard:false});await expect(closing).toBeVisible();
 await page.evaluate(()=>{window.go!.main!.App!.SaveWorkspace=(window as any).__closingOriginalSave;(window as any).__testFailSave(true);});await page.getByLabel('Timeframe 15m',{exact:true}).click();await page.evaluate(()=>(window as any).__testEmit('prepare-close',{token:'failed-native-close'}));
 await expect(page.getByRole('button',{name:'Retry save and close',exact:true})).toBeVisible();await expect(closing).toHaveCount(0);await page.getByRole('button',{name:'Keep open',exact:true}).click();await expect(page.getByRole('button',{name:'Retry save and close',exact:true})).toHaveCount(0);await expect(closing).toHaveCount(0);
});

test('close timeout clears progress and cancels the stale confirmation while a save is still pending',async({page})=>{
 await openTestApp(page);await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();
 await page.evaluate(()=>{const api=window.go!.main!.App!,save=api.SaveWorkspace;api.SaveWorkspace=async(...args)=>{await new Promise<void>(resolve=>{(window as any).__releaseTimedOutSave=resolve;});return save(...args);};});
 await page.getByLabel('Timeframe 4h',{exact:true}).click();await page.evaluate(()=>(window as any).__testEmit('prepare-close',{token:'timed-out-close'}));await expect(page.getByText('Closing gracefully...',{exact:true})).toBeVisible();
 await expect.poll(()=>page.evaluate(()=>typeof(window as any).__releaseTimedOutSave)).toBe('function');await page.evaluate(()=>(window as any).__testEmit('close-timeout',{reason:'The final save was not acknowledged. Retry closing.'}));
 await expect(page.getByText('Closing gracefully...',{exact:true})).toHaveCount(0);await page.evaluate(()=>(window as any).__releaseTimedOutSave());await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();expect(await page.evaluate(()=>(window as any).__testCloseConfirmation())).toBeNull();
});

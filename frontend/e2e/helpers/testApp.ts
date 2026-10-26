import {expect,type Page} from '@playwright/test';

/** Navigation can finish while main.tsx is still awaiting the explicit test bridge. */
export async function waitForTestBridge(page:Page){
 await expect.poll(()=>page.evaluate(()=>(window as Window&{__testBridgeReady?:boolean}).__testBridgeReady===true),{
  message:'The explicitly enabled browser test bridge must finish installing before fixture access',
  timeout:10000,
 }).toBe(true);
}

export async function openTestApp(page:Page){
 await page.goto('/');
 await waitForTestBridge(page);
}

export async function reloadTestApp(page:Page){
 await page.reload();
 await waitForTestBridge(page);
}

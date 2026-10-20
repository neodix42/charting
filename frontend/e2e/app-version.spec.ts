import {test,expect,type Page} from '@playwright/test';
import {appVersion} from '../build-version.mjs';
import {firstWorkspace} from '../src/workspace/model';
import type {Backend} from '../src/types';

async function expectVersion(page:Page){
 const badge=page.getByLabel('Application version',{exact:true});
 await expect(badge).toHaveText(`v${appVersion}`);await expect(badge).toBeVisible();
 await expect(badge).toHaveAttribute('title',`CryptoDesk release v${appVersion}`);
 const geometry=await badge.evaluate(element=>{
  const bounds=(node:Element)=>{const r=node.getBoundingClientRect();return{left:r.left,right:r.right,top:r.top,bottom:r.bottom};};
  const label=element.previousElementSibling!;
  return{labelText:label.textContent,labelVisible:getComputedStyle(label).display!=='none',label:bounds(label),badge:bounds(element),brand:bounds(element.parentElement!),controls:bounds(document.querySelector('[data-testid="tab-chart-controls"]')!),documentWidth:document.documentElement.scrollWidth,windowWidth:innerWidth};
 });
 expect(geometry.labelText).toBe('CryptoDesk');await expect(page.getByText('SPOT TERMINAL',{exact:true})).toHaveCount(0);await expect(page.locator('.brand-tag')).toHaveCount(0);
 if(geometry.labelVisible)expect(geometry.badge.left).toBeGreaterThanOrEqual(geometry.label.right);
 expect(geometry.brand.right).toBeLessThanOrEqual(geometry.controls.left);
 expect(geometry.badge.top).toBeGreaterThanOrEqual(geometry.brand.top);expect(geometry.badge.bottom).toBeLessThanOrEqual(geometry.brand.bottom);
 expect(geometry.documentWidth).toBe(geometry.windowWidth);
}

for(const viewport of [{width:1600,height:1000},{width:1100,height:720}])test(`release version sits beside the brand without overlapping header controls at ${viewport.width}px`,async({page})=>{
 await page.setViewportSize(viewport);await page.goto('/');await expectVersion(page);
 await page.screenshot({path:`test-results/app-version-${viewport.width}.png`});
});

test('production header and build metadata contain the same resolved release tag',async({page})=>{
 // Explicit bridge stub lets the production bundle render its header without a desktop.
 await page.addInitScript(workspace=>{
  let revision=1;
  window.runtime={EventsOn:()=>()=>{}};
  window.go={main:{App:{
   LoadWorkspace:async()=>({workspace,revision}),SaveWorkspace:async(_expected:number,_session:string,sequence:number)=>({revision:++revision,acceptedEditSequence:sequence}),
   ListInstruments:async()=>[],AttachChart:async()=>{throw Error('Explicit header-only bridge stub');},FrontendReady:async()=>{}
  } as unknown as Backend}};
 },firstWorkspace());
 await page.goto('http://127.0.0.1:4174');await expectVersion(page);
 const metadata=await page.request.get('http://127.0.0.1:4174/app-version.json');
 expect(metadata.ok()).toBe(true);expect(await metadata.json()).toEqual({version:appVersion,tag:`v${appVersion}`});
 expect(await page.evaluate(()=>window.__CRYPTO_TEST__)).toBeUndefined();
});

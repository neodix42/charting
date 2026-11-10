import {test,expect} from '@playwright/test';
import {waitForTestBridge} from './helpers/testApp';

test('workspace and active charts open while optional services report background progress',async({page})=>{
 await page.addInitScript(()=>{
  let release!:()=>void,bridge:any;
  const gate=new Promise<void>(resolve=>{release=resolve;});
  (window as any).__startupStatus={workspaceReady:false,ready:false,phase:'Reading saved layout…',elapsedMs:25,tasks:[]};
  (window as any).__releaseWorkspace=()=>{(window as any).__startupStatus={workspaceReady:true,ready:false,phase:'Workspace ready',elapsedMs:40,tasks:[{id:'predictions',message:'Restoring saved forecasts…',state:'loading',elapsedMs:20}]};release();};
  Object.defineProperty(window,'go',{configurable:true,get:()=>bridge,set:value=>{
   bridge=value;const api=bridge.main.App,load=api.LoadWorkspace;
   api.LoadWorkspace=async()=>{await gate;return load();};
   api.GetStartupStatus=async()=>structuredClone((window as any).__startupStatus);
  }});
 });
 await page.goto('/');await waitForTestBridge(page);
 await expect(page.getByRole('status')).toHaveText('Reading saved layout…');
 await expect(page.getByTestId('chart-cell')).toHaveCount(0);
 expect(await page.evaluate(()=>(window as any).__testLeases())).toBe(0);
 await page.evaluate(()=>(window as any).__releaseWorkspace());
 await expect(page.getByTestId('chart-cell')).toHaveCount(1);
 await expect(page.getByText('Loading background services',{exact:true})).toBeVisible();
 await expect(page.getByText('Restoring saved forecasts…',{exact:true})).toBeVisible();
 await expect.poll(()=>page.evaluate(()=>(window as any).__testLeases())).toBe(1);
 await page.screenshot({path:'../.tools/verification/latency-startup-progress.png'});
 await page.getByRole('button',{name:'Open settings',exact:true}).click();
 await expect(page.getByRole('dialog',{name:'Workspace settings',exact:true})).toBeVisible();
 await page.evaluate(()=>(window as any).__startupStatus={workspaceReady:true,ready:true,phase:'Workspace ready',elapsedMs:75,tasks:[{id:'predictions',message:'Restoring saved forecasts…',state:'ready',elapsedMs:35}]});
 await expect(page.getByText('Loading background services',{exact:true})).toHaveCount(0);
 await expect(page.getByTestId('chart-cell')).toHaveCount(1);
});

test('completed background restoration keeps service errors visible without replacing the workspace',async({page})=>{
 await page.addInitScript(()=>{
  let bridge:any;
  Object.defineProperty(window,'go',{configurable:true,get:()=>bridge,set:value=>{
   bridge=value;bridge.main.App.GetStartupStatus=async()=>({workspaceReady:true,ready:true,phase:'Workspace ready',elapsedMs:25,tasks:[{id:'candles',message:'Checking saved candle cache…',state:'error',elapsedMs:20,error:'Saved candle cache could not be validated; original data preserved'}]});
  }});
 });
 await page.goto('/');await waitForTestBridge(page);
 await expect(page.getByTestId('chart-cell')).toHaveCount(1);
 await expect(page.getByRole('alert')).toContainText('Saved candle cache could not be validated');
 await page.getByRole('button',{name:'Dismiss background startup notice',exact:true}).click();
 await expect(page.getByText('Background service unavailable',{exact:true})).toHaveCount(0);
 await expect.poll(()=>page.evaluate(()=>(window as any).__testLeases())).toBe(1);
});

test('pending cache validation is shown separately from a confirmed empty folder',async({page})=>{
 await page.addInitScript(()=>{
  let bridge:any;
  Object.defineProperty(window,'go',{configurable:true,get:()=>bridge,set:value=>{
   bridge=value;bridge.main.App.GetCandleStorage=async()=>({enabled:true,directory:'/explicit-test/pending-cache',sizeBytes:0,pending:true});
  }});
 });
 await page.goto('/');await waitForTestBridge(page);
 await expect(page.getByTestId('chart-cell')).toHaveCount(1);
 await page.getByRole('button',{name:'Open settings',exact:true}).click();
 await page.getByRole('dialog',{name:'Workspace settings',exact:true}).getByRole('tab',{name:'Storage',exact:true}).click();
 const form=page.getByRole('form',{name:'Local candle storage',exact:true});
 await expect(form.getByLabel('Candle storage folder size',{exact:true})).toHaveText('Checking cache…');
 await expect(form).toContainText('Checking saved candle cache…');
 await page.evaluate(()=>{window.go!.main!.App!.GetCandleStorage=async()=>({enabled:true,directory:'/explicit-test/pending-cache',sizeBytes:2048,pending:false});});
 await form.getByRole('button',{name:'Refresh candle storage size',exact:true}).click();
 await expect(form.getByLabel('Candle storage folder size',{exact:true})).toHaveText('2 KiB');
});

import {test,expect,type Page,type Route} from '@playwright/test';
import {openTestApp,reloadTestApp} from './helpers/testApp';
import intermediate from '../../examples/intermediate-two-tabs.json' with {type:'json'};
import type {Workspace} from '../src/types';
import {intervalSeconds} from '../src/types';

interface GapMetrics {
 id:string; unit:'bars'|'pixels'; value:number; effectiveGapBars:number;
 gapPixels:number; gapBars:number; barSpacing:number; timeScaleWidth:number;
 lastX:number; latestLogical:number; visibleTo:number; followingLive:boolean; latestTime:number;
}
const storageKey='cryptodesk-explicit-test-workspace';
const gaps=(page:Page):Promise<GapMetrics[]>=>page.evaluate(()=>(window as any).__testChartGaps());
const saved=(page:Page):Promise<Workspace>=>page.evaluate(()=>(window as any).__testWorkspace());
async function seed(page:Page,workspace:Workspace){
 await page.addInitScript(({key,workspace})=>{if(!sessionStorage.getItem(key))sessionStorage.setItem(key,JSON.stringify(workspace));},{key:storageKey,workspace});
 await openTestApp(page);
 await expect(page.getByTestId('chart-cell')).toHaveCount(workspace.tabs.find(tab=>tab.id===workspace.activeTabId)!.charts.length);
 await expect.poll(async()=>(await gaps(page)).length).toBe(workspace.tabs.find(tab=>tab.id===workspace.activeTabId)!.charts.length);
}
async function applyGap(page:Page,unit:'bars'|'pixels',value:string){
 await page.getByRole('button',{name:'Open settings',exact:true}).click();
 const dialog=page.getByRole('dialog',{name:'Workspace settings',exact:true});
 await dialog.getByLabel('Chart right gap unit',{exact:true}).selectOption(unit);
 await dialog.getByLabel('Chart right gap',{exact:true}).fill(value);
 await dialog.getByRole('button',{name:'Apply chart gap',exact:true}).click();
 await dialog.getByRole('button',{name:'Close settings',exact:true}).click();
}
async function expectGap(page:Page,count:number,unit:'bars'|'pixels',value:number){
 await expect.poll(async()=>{
  const metrics=await gaps(page);
  return metrics.length===count&&metrics.every(metric=>metric.unit===unit&&metric.value===value&&metric.followingLive&&Math.abs((unit==='bars'?metric.gapBars:metric.gapPixels)-value)<0.2);
 }).toBe(true);
}

test('fresh and legacy workspaces display a ten-bar gap without expanding their layouts',async({page})=>{
 await openTestApp(page);await expect(page.getByTestId('chart-cell')).toHaveCount(1);await expectGap(page,1,'bars',10);
 await page.getByRole('button',{name:'Open settings',exact:true}).click();
 const dialog=page.getByRole('dialog',{name:'Workspace settings',exact:true});
 await expect(dialog.getByLabel('Chart right gap',{exact:true})).toHaveValue('10');
 await expect(dialog.getByLabel('Chart right gap unit',{exact:true})).toHaveValue('bars');
 await dialog.getByRole('button',{name:'Close settings',exact:true}).click();
 const workspace=structuredClone(intermediate) as Workspace;
 for(const tab of workspace.tabs)for(const chart of tab.charts)chart.view={...chart.view,mode:'live',fromSec:null,toSec:null};
 await page.evaluate(({key,workspace})=>sessionStorage.setItem(key,JSON.stringify(workspace)),{key:storageKey,workspace});
 await reloadTestApp(page);await expect(page.locator('[role="tab"][data-tab-id]')).toHaveCount(2);await expect(page.getByTestId('chart-cell')).toHaveCount(3);await expectGap(page,3,'bars',10);
 expect((await saved(page)).chartRightGap).toBeUndefined();
});

test('a gap change repaints every active chart and maximized sibling and persists for hidden tabs',async({page})=>{
 const workspace=structuredClone(intermediate) as Workspace;
 for(const [tabIndex,tab] of workspace.tabs.entries())for(const [index,chart] of tab.charts.entries()){
  const step=intervalSeconds[chart.timeframe],last=Math.floor(Date.now()/1000/step)*step;
  chart.view={mode:'historical',fromSec:last-60*step,toSec:last-20*step,barSpacing:6+index*3,rightOffset:5};
  if(tabIndex===0&&index===0)chart.indicators=[{id:'30000000-0000-4000-8000-000000000001',type:'ichimoku',parameters:{conversion:9,base:26,spanB:52,displacement:26}}];
 }
 await seed(page,workspace);
 await expect.poll(async()=>(await gaps(page)).every(metric=>!metric.followingLive&&metric.gapBars<-5)).toBe(true);
 const historical=await gaps(page);
 await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();
 await reloadTestApp(page);await expect.poll(async()=>(await gaps(page)).length).toBe(3);
 await expect.poll(async()=>{
  const restored=await gaps(page);
  return restored.length===3&&restored.every((metric,index)=>!metric.followingLive&&Math.abs(metric.gapBars-historical[index].gapBars)<0.2);
 }).toBe(true);
 const attachments=await page.evaluate(()=>(window as any).__testAttachCount());
 await page.getByRole('button',{name:'Maximize chart',exact:true}).first().click();
 await applyGap(page,'bars','23');
 await expectGap(page,3,'bars',23);
 expect(await page.evaluate(()=>(window as any).__testAttachCount())).toBe(attachments);
 await page.getByRole('button',{name:'Restore chart',exact:true}).click();await expectGap(page,3,'bars',23);
 await expect.poll(async()=>(await saved(page)).chartRightGap).toEqual({unit:'bars',value:23});
 const current=await saved(page);
 expect(current.tabs.map(tab=>tab.charts.length)).toEqual([3,1]);
 expect(current.tabs.every(tab=>tab.charts.every(chart=>chart.view.mode==='live'))).toBe(true);
 expect(current.tabs[0].charts[0].indicators).toEqual(workspace.tabs[0].charts[0].indicators);
 await page.locator('[role="tab"][data-tab-id]').nth(1).click();await expectGap(page,1,'bars',23);
 await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();
 await reloadTestApp(page);await expect(page.getByTestId('chart-cell')).toHaveCount(1);await expectGap(page,1,'bars',23);
 await page.locator('[role="tab"][data-tab-id]').first().click();await expectGap(page,3,'bars',23);
 await page.screenshot({path:'test-results/right-gap-all-charts.png'});
});

test('pixel gaps stay fixed through zoom, resize, future Ichimoku spans and a new live candle',async({page})=>{
 const errors:string[]=[];page.on('pageerror',error=>errors.push(error.message));
 await openTestApp(page);await expectGap(page,1,'bars',10);
 const chart=page.getByTestId('chart-cell');await chart.getByLabel('Indicators',{exact:true}).selectOption('ichimoku');
 let dialog=page.getByRole('dialog',{name:'Ichimoku Cloud parameters',exact:true});await dialog.getByRole('button',{name:'OK',exact:true}).click();
 await expectGap(page,1,'bars',10);
 const attachments=await page.evaluate(()=>(window as any).__testAttachCount());
 await applyGap(page,'pixels','79');await expectGap(page,1,'pixels',79);
 let metric=(await gaps(page))[0];const previousTime=metric.latestTime;
 await page.evaluate(({id,spacing})=>(window as any).__testChartZoom(id,spacing),{id:metric.id,spacing:18});
 await expect.poll(async()=>(await gaps(page))[0]?.barSpacing).toBe(18);await expectGap(page,1,'pixels',79);
 await page.setViewportSize({width:1100,height:800});await expectGap(page,1,'pixels',79);
 await chart.getByRole('button',{name:'Edit Ichimoku Cloud (9, 26, 52, 26)',exact:true}).click();
 dialog=page.getByRole('dialog',{name:'Ichimoku Cloud parameters',exact:true});await dialog.getByLabel('Displacement',{exact:true}).fill('40');await dialog.getByRole('button',{name:'OK',exact:true}).click();
 await expect.poll(async()=>(await gaps(page))[0]?.barSpacing).toBe(18);await expectGap(page,1,'pixels',79);
 await page.evaluate(()=>(window as any).__testAdvanceBars());
 await expect.poll(async()=>(await gaps(page))[0]?.latestTime).toBeGreaterThan(previousTime);await expectGap(page,1,'pixels',79);
 metric=(await gaps(page))[0];expect(metric.lastX).toBeGreaterThan(0);expect(metric.lastX).toBeLessThan(metric.timeScaleWidth);
 expect(await page.evaluate(()=>(window as any).__testAttachCount())).toBe(attachments);
 await expect.poll(async()=>(await saved(page)).chartRightGap).toEqual({unit:'pixels',value:79});
 // Browser load can precede the async fixture import. Hold that import to
 // reproduce the CI reload race while checking the saved pixel-gap restore.
 let releaseBridge!:()=>void,moduleRequested=false,reloadReady=false;
 const released=new Promise<void>(resolve=>{releaseBridge=resolve;});
 const holdBridge=async(route:Route)=>{moduleRequested=true;await released;await route.continue();};
 await page.route('**/src/test/browserBridge.ts*',holdBridge);
 const reloaded=reloadTestApp(page).then(()=>{reloadReady=true;});
 try{
  await expect.poll(()=>moduleRequested).toBe(true);await page.waitForLoadState('load');
  expect(await page.evaluate(()=>typeof(window as any).__testChartGaps)).toBe('undefined');
  expect(reloadReady).toBe(false);releaseBridge();await reloaded;
  await expectGap(page,1,'pixels',79);
 }finally{
  releaseBridge();await reloaded;await page.unroute('**/src/test/browserBridge.ts*',holdBridge);
 }
 await page.getByRole('button',{name:'Open settings',exact:true}).click();dialog=page.getByRole('dialog',{name:'Workspace settings',exact:true});
 await expect(dialog.getByLabel('Chart right gap',{exact:true})).toHaveValue('79');await expect(dialog.getByLabel('Chart right gap unit',{exact:true})).toHaveValue('pixels');
 await page.screenshot({path:'test-results/right-gap-settings.png'});expect(errors).toEqual([]);
});

for(const updateTiming of ['before capture','after capture'] as const)test(`real wheel zoom survives a historical price update ${updateTiming}`,async({page},testInfo)=>{
 await openTestApp(page);await expectGap(page,1,'bars',10);
 const canvas=page.getByTestId('chart-canvas'),box=(await canvas.boundingBox())!;
 // Use the actual browser wheel path after panning the newest candle offscreen.
 const initial=(await gaps(page))[0];
 await page.evaluate(id=>(window as any).__testChartPan(id,-35),initial.id);
 await expect.poll(async()=>(await gaps(page))[0]?.followingLive).toBe(false);
 await page.waitForTimeout(120);
 const before=(await gaps(page))[0];
 await page.mouse.move(box.x+box.width*.5,box.y+box.height*.4);
 await page.evaluate(()=>{
  document.querySelector('[data-testid="chart-canvas"]')!.addEventListener('wheel',()=>{(window as any).__zoomWheelAt=performance.now();},{capture:true,once:true});
 });
 await page.mouse.wheel(0,240);
 const afterWheel=await page.evaluate(async()=>{
  await new Promise<void>(resolve=>requestAnimationFrame(()=>resolve()));
  return {geometry:(window as any).__testChartGaps()[0],elapsedMs:performance.now()-(window as any).__zoomWheelAt};
 });
 expect(afterWheel.geometry.barSpacing).toBeLessThan(before.barSpacing);
 const publish=()=>page.evaluate(()=>{
  const subscription=(window as any).__testSubscriptions()[0],geometry=(window as any).__testChartGaps()[0];
  (window as any).__testPublish({...subscription,kind:'bars',revision:subscription.revision+1,bars:[{time:geometry.latestTime,open:60990,high:61020,low:60970,close:61001,volume:3,closed:false,quality:'provisional',sourceRevision:2}]});
 });
 if(updateTiming==='before capture')await publish();
 await page.waitForTimeout(120);
 const afterCapture=(await gaps(page))[0];
 if(updateTiming==='after capture')await publish();
 await page.waitForTimeout(160);
 const afterPrice=(await gaps(page))[0];
 const evidence={updateTiming,before,afterWheel,afterCapture,afterPrice};
 await testInfo.attach('wheel-zoom-evidence',{body:JSON.stringify(evidence,null,2),contentType:'application/json'});
 expect(afterPrice.followingLive).toBe(false);
 expect(afterPrice.barSpacing).toBeCloseTo(afterWheel.geometry.barSpacing,6);
 expect(afterPrice.visibleTo).toBeCloseTo(afterWheel.geometry.visibleTo,6);
 await page.getByRole('button',{name:'Return to live',exact:true}).click();
 await expectGap(page,1,'bars',10);
});

test('real wheel zoom keeps its fractional historical window when older candles are prepended',async({page},testInfo)=>{
 await openTestApp(page);await expectGap(page,1,'bars',10);
 await page.evaluate(()=>{
  window.go!.main!.App!.GetHistory=async(requestId,feedKey,_from,to)=>new Promise(resolve=>{
   (window as any).__releaseZoomPrepend=()=>resolve({requestId,feedKey,moreHistory:false,bars:Array.from({length:100},(_,index)=>({time:to-(100-index)*60,open:60000,high:60030,low:59980,close:60010,volume:2,closed:true,quality:'provisional',sourceRevision:1}))});
  });
 });
 await page.getByRole('button',{name:'Timeframe 1m',exact:true}).click();
 await expectGap(page,1,'bars',10);
 const initial=(await gaps(page))[0];
 await page.evaluate(id=>(window as any).__testChartPan(id,-35),initial.id);
 await expect.poll(async()=>(await gaps(page))[0]?.followingLive).toBe(false);
 await expect.poll(()=>page.evaluate(()=>typeof(window as any).__releaseZoomPrepend)).toBe('function');
 const box=(await page.getByTestId('chart-canvas').boundingBox())!;
 await page.mouse.move(box.x+box.width*.5,box.y+box.height*.4);await page.mouse.wheel(0,240);
 await expect.poll(async()=>(await gaps(page))[0]?.barSpacing).toBeLessThan(initial.barSpacing);
 await page.waitForTimeout(120);
 const beforePrepend=(await gaps(page))[0];
 await page.evaluate(()=>(window as any).__releaseZoomPrepend());
 await expect.poll(async()=>(await gaps(page))[0]?.latestLogical).toBe(initial.latestLogical+100);
 const afterPrepend=(await gaps(page))[0];
 await testInfo.attach('wheel-prepend-evidence',{body:JSON.stringify({beforePrepend,afterPrepend},null,2),contentType:'application/json'});
 expect(afterPrepend.barSpacing).toBeCloseTo(beforePrepend.barSpacing,6);
 expect(afterPrepend.visibleTo-afterPrepend.latestLogical).toBeCloseTo(beforePrepend.visibleTo-beforePrepend.latestLogical,6);
 expect(afterPrepend.followingLive).toBe(false);
 await page.getByRole('button',{name:'Return to live',exact:true}).click();await expectGap(page,1,'bars',10);
});

test('invalid and unapplied gap drafts preserve the saved rule and zero is accepted',async({page})=>{
 await openTestApp(page);await expectGap(page,1,'bars',10);await page.getByRole('button',{name:'Open settings',exact:true}).click();
 const dialog=page.getByRole('dialog',{name:'Workspace settings',exact:true}),value=dialog.getByLabel('Chart right gap',{exact:true}),apply=dialog.getByRole('button',{name:'Apply chart gap',exact:true});
 for(const draft of ['','-1','1.5','501']){
  await value.fill(draft);await apply.click();await expect(dialog.getByRole('alert')).toBeVisible();await expectGap(page,1,'bars',10);
  expect((await saved(page)).chartRightGap).toEqual({unit:'bars',value:10});
 }
 await dialog.getByLabel('Chart right gap unit',{exact:true}).selectOption('pixels');await value.fill('5001');await apply.click();await expect(dialog.getByRole('alert')).toBeVisible();await expectGap(page,1,'bars',10);
 await value.fill('45');await dialog.getByRole('button',{name:'Close settings',exact:true}).click();await expectGap(page,1,'bars',10);
 await page.getByRole('button',{name:'Open settings',exact:true}).click();await expect(value).toHaveValue('10');await expect(dialog.getByLabel('Chart right gap unit',{exact:true})).toHaveValue('bars');
 await value.fill('0');await apply.click();await expectGap(page,1,'bars',0);
 await expect.poll(async()=>(await saved(page)).chartRightGap).toEqual({unit:'bars',value:0});
});

test('Apply refreshes a full older window and a late older page cannot evict the latest candle',async({page})=>{
 const errors:string[]=[];page.on('pageerror',error=>errors.push(error.message));
 await openTestApp(page);await expectGap(page,1,'bars',10);
 const oldEnd=await page.evaluate(()=>{
  const api=window.go!.main!.App!,attach=api.AttachChart.bind(api),oldEnd=Math.floor((Date.now()/1000-30*86400)/60)*60;
  const makeBars=(end:number,count:number)=>Array.from({length:count},(_,index)=>({time:end-(count-index-1)*60,open:60000,high:60030,low:59980,close:60010,volume:2,closed:true,quality:'provisional',sourceRevision:1}));
  api.AttachChart=async(...args)=>({...await attach(...args),bars:makeBars(oldEnd,10000)});
  api.GetHistory=async(requestId,feedKey,_from,to)=>{
   const latest=to>oldEnd+86400,key=latest?'__releaseGapLatest':'__releaseGapOlder';
   return new Promise(resolve=>{
    (window as any)[key]=async()=>{
     const end=latest?Math.floor(Date.now()/1000/60)*60:oldEnd-10000*60;
     if(latest)(window as any).__expectedGapLatest=end;
     resolve({requestId,feedKey,bars:makeBars(end,500),moreHistory:false});
     await new Promise<void>(done=>requestAnimationFrame(()=>requestAnimationFrame(()=>done())));
    };
   });
  };
  return oldEnd;
 });
 await page.getByRole('button',{name:'Timeframe 1m',exact:true}).click();
 await expect.poll(async()=>(await gaps(page))[0]?.latestTime).toBe(oldEnd);
 const metric=(await gaps(page))[0],attachments=await page.evaluate(()=>(window as any).__testAttachCount());
 await page.evaluate(id=>(window as any).__testChartPan(id,-9970),metric.id);
 await expect.poll(()=>page.evaluate(()=>typeof(window as any).__releaseGapOlder)).toBe('function');
 await expect.poll(async()=>(await gaps(page))[0]?.followingLive).toBe(false);
 await applyGap(page,'bars','23');
 await expect.poll(()=>page.evaluate(()=>typeof(window as any).__releaseGapLatest)).toBe('function');
 await page.evaluate(()=>(window as any).__releaseGapLatest());
 const latest=await page.evaluate(()=>(window as any).__expectedGapLatest);
 await expect.poll(async()=>(await gaps(page))[0]?.latestTime).toBe(latest);await expectGap(page,1,'bars',23);
 await page.evaluate(()=>(window as any).__releaseGapOlder());
 await expect.poll(async()=>(await gaps(page))[0]?.latestTime).toBe(latest);await expectGap(page,1,'bars',23);
 expect(await page.evaluate(()=>(window as any).__testAttachCount())).toBe(attachments);
 await expect.poll(async()=>(await saved(page)).chartRightGap).toEqual({unit:'bars',value:23});expect(errors).toEqual([]);
});

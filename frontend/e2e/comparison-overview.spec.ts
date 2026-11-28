import {test,expect,type Page} from '@playwright/test';
import {openTestApp,reloadTestApp} from './helpers/testApp';
import {comparisonDate} from '../src/workspace/comparison';

const periods=[2,4,8,12],day=86400;
const panel=(page:Page)=>page.locator('#comparison-panel');
const region=(page:Page,weeks:number)=>page.getByRole('region',{name:`${weeks}-week comparison`,exact:true});
const plot=(page:Page,weeks:number)=>region(page,weeks).getByTestId('comparison-chart');
const layout=(page:Page,name:'Four charts'|'Single chart')=>page.getByRole('group',{name:'Comparison layout',exact:true}).getByRole('button',{name,exact:true});
const resources=(page:Page)=>page.evaluate(()=>(window as any).__testComparisonChartResources());
const overview=(page:Page)=>page.evaluate(()=>(window as any).__testComparisonLastOverview());
const groupCalls=(page:Page)=>page.evaluate(()=>(window as any).__testComparisonOverviewCalls());
const calls=(page:Page)=>page.evaluate(()=>(window as any).__testComparisonCalls());
const charts=(page:Page,time?:number)=>page.evaluate(time=>(window as any).__testComparisonCharts(time),time);
const pending=(page:Page)=>page.evaluate(()=>(window as any).__testComparisonPendingOverview());
const legend=(page:Page,weeks:number,symbol:string)=>region(page,weeks).getByLabel('Comparison legend',{exact:true}).getByRole('button',{name:symbol,exact:true});
const navigate=(page:Page,name:'Prev'|'Today'|'Next')=>page.getByRole('group',{name:'Comparison date navigation',exact:true}).getByRole('button',{name,exact:true});
const groupRequests=(page:Page)=>page.evaluate(()=>(window as any).__testComparisonOverviewRequests());
async function through(page:Page,endTime:number,checkLatestDelivery=true){
 if(checkLatestDelivery)await expect.poll(async()=>(await overview(page))?.panels.map((item:any)=>item.lastClosedTime)).toEqual(periods.map(()=>endTime));
 for(const weeks of periods)await expect(plot(page,weeks)).toHaveAttribute('data-baseline-time',String(endTime-weeks*7*day));
 await expect(panel(page).locator('.comparison-end-date')).toContainText(`${comparisonDate(endTime)} UTC`);
}
async function frames(page:Page){await page.evaluate(()=>new Promise<void>(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>resolve()))));}
async function enter(page:Page){await page.getByRole('tab',{name:'Comparison',exact:true}).click();await expect(panel(page)).toBeVisible();}
async function loaded(page:Page,counts=[7,7,7,7]){
 await expect(page.getByTestId('comparison-chart')).toHaveCount(4);
 for(const [index,weeks] of periods.entries())await expect(plot(page,weeks)).toHaveAttribute('data-series-count',String(counts[index]));
 await expect.poll(()=>resources(page)).toEqual({charts:4,observers:8,listeners:4});
}
async function saved(page:Page,weeks:number,assets:string[]){await page.addInitScript(view=>{const key='cryptodesk-explicit-test-comparison-view';if(sessionStorage.getItem(key)===null)sessionStorage.setItem(key,JSON.stringify(view));},{weeks,assets});}
async function add(page:Page,symbol:string,exchange='Hyperliquid',market='Perpetual',quote='USDC'){
 await page.getByRole('button',{name:'Add comparison asset',exact:true}).click();const picker=page.getByLabel('Comparison assets',{exact:true});
 await picker.getByRole('button',{name:'All',exact:true}).click();await picker.getByRole('button',{name:'All sources',exact:true}).click();await picker.getByRole('textbox',{name:'Search instruments',exact:true}).fill(symbol);
 await picker.getByRole('button',{name:`${symbol} ${exchange} ${market} ${quote}`,exact:true}).click();
}

test('default overview uses one shared close history for ordered 2/4/8/12-week charts in a responsive 2x2 grid',async({page},testInfo)=>{
 await page.setViewportSize({width:1566,height:1080});await openTestApp(page);expect(await groupCalls(page)).toBe(0);await enter(page);await loaded(page);
 await expect(layout(page,'Four charts')).toHaveAttribute('aria-pressed','true');await expect(page.getByRole('spinbutton',{name:'Comparison weeks',exact:true})).toHaveCount(0);await expect(page.getByLabel('Common comparison periods',{exact:true})).toHaveCount(0);
 expect(await page.getByTestId('comparison-chart').evaluateAll(elements=>elements.map(element=>element.getAttribute('data-comparison-chart-id')))).toEqual(periods.map(weeks=>`weeks-${weeks}`));
 const result=await overview(page);expect(result.panels.map((item:any)=>item.weeks)).toEqual(periods);expect(await groupCalls(page)).toBe(1);expect((await calls(page)).data).toBe(0);expect((await calls(page)).save).toBe(0);
 expect(new Set(result.panels.map((item:any)=>item.asOf)).size).toBe(1);expect(new Set(result.panels.map((item:any)=>item.lastClosedTime)).size).toBe(1);
 const closes=new Map<string,number>();
 for(const item of result.panels){
  expect(item.baselineTime).toBe(item.lastClosedTime-item.weeks*7*day);expect(item.baselineTime%day).toBe(0);expect(item.lastClosedTime+day).toBeLessThanOrEqual(item.asOf);
  for(const series of item.series){
   expect(series.points).toHaveLength(item.weeks*7+1);const baseline=series.points[0];expect(baseline).toMatchObject({time:item.baselineTime,pct:0});
   for(const point of series.points){const key=`${series.asset.id}:${point.time}`;if(closes.has(key))expect(point.close).toBe(closes.get(key));else closes.set(key,point.close);expect(point.pct).toBeCloseTo((point.close/baseline.close-1)*100,10);}
  }
  await expect(legend(page,item.weeks,'BTC').locator('.comparison-legend-price')).toHaveText('Close 1,840 USDC');
 }
 expect(new Set((await charts(page)).map((item:any)=>item.chartId))).toEqual(new Set(periods.map(weeks=>`weeks-${weeks}`)));expect(await page.evaluate(()=>(window as any).__testComparisonChart())).toBeNull();
 const boxes=await Promise.all(periods.map(weeks=>region(page,weeks).boundingBox()));for(const box of boxes)expect(box).not.toBeNull();expect(boxes[0]!.x).toBeCloseTo(boxes[2]!.x,0);expect(boxes[1]!.x).toBeCloseTo(boxes[3]!.x,0);expect(boxes[0]!.y).toBeCloseTo(boxes[1]!.y,0);expect(boxes[2]!.y).toBeGreaterThan(boxes[0]!.y);expect(boxes[1]!.x).toBeGreaterThan(boxes[0]!.x);
 // Font/line-height rounding can extend the bottom host a fraction of a pixel.
 for(const weeks of periods){const bounds=(await plot(page,weeks).boundingBox())!;await expect(plot(page,weeks)).toBeInViewport({ratio:1-1/bounds.height});}await page.screenshot({path:testInfo.outputPath('comparison-overview-default-2x2.png')});
 // CI falls back to wider font metrics than local Ubuntu installations.
 // Preserve full-size plots without relying on that particular system font.
 await page.addStyleTag({content:':root{font-family:"DejaVu Sans",sans-serif}'});await frames(page);expect(await page.evaluate(()=>getComputedStyle(document.documentElement).fontFamily)).toContain('DejaVu Sans');
 for(const weeks of periods){const bounds=(await plot(page,weeks).boundingBox())!;await expect(plot(page,weeks)).toBeInViewport({ratio:1-1/bounds.height});}await page.screenshot({path:testInfo.outputPath('comparison-overview-dejavu.png')});
 for(const weeks of periods){const item=region(page,weeks).getByLabel('Comparison legend',{exact:true});await item.scrollIntoViewIfNeeded();await expect(item).toBeInViewport({ratio:1});}expect(await page.evaluate(()=>document.documentElement.scrollWidth)).toBeLessThanOrEqual(1566);expect(await groupCalls(page)).toBe(1);await page.screenshot({path:testInfo.outputPath('comparison-overview-lower-legends.png')});
 await page.setViewportSize({width:900,height:1200});await reloadTestApp(page);await enter(page);await loaded(page);await frames(page);expect(await page.evaluate(()=>document.documentElement.scrollWidth)).toBeLessThanOrEqual(900);
 const fresh=await overview(page);for(const item of fresh.panels){const first=(await charts(page,item.baselineTime)).find((value:any)=>value.chartId===`weeks-${item.weeks}`),last=(await charts(page,item.lastClosedTime)).find((value:any)=>value.chartId===`weeks-${item.weeks}`),bounds=(await plot(page,item.weeks).boundingBox())!;expect(first.x).toBeGreaterThanOrEqual(0);expect(last.x+last.leftWidth).toBeLessThanOrEqual(bounds.width);}await panel(page).screenshot({path:testInfo.outputPath('comparison-overview-900.png')});
 await page.setViewportSize({width:700,height:1200});await frames(page);const narrow=await Promise.all(periods.map(weeks=>region(page,weeks).boundingBox()));for(let i=1;i<narrow.length;i++){expect(narrow[i]!.x).toBeCloseTo(narrow[0]!.x,0);expect(narrow[i]!.y).toBeGreaterThan(narrow[i-1]!.y);}
 // Below the existing 900px desktop shell minimum, test the overview's
 // one-column fallback without asserting a new minimum width for the app.
 const sizing=await panel(page).locator('.comparison-grid').evaluate(element=>({width:element.clientWidth,scroll:element.scrollWidth,appMin:parseFloat(getComputedStyle(document.querySelector('.app-shell')!).minWidth),document:document.documentElement.scrollWidth}));expect(sizing.scroll).toBeLessThanOrEqual(sizing.width);expect(sizing.document).toBeLessThanOrEqual(Math.max(700,sizing.appMin));for(const box of narrow)expect(box!.width).toBeLessThanOrEqual(sizing.width);expect(await groupCalls(page)).toBe(1);expect(await resources(page)).toEqual({charts:4,observers:8,listeners:4});
});

test('each overview chart owns its hover and viewport while visibility toggles are shared without refetching',async({page},testInfo)=>{
 await saved(page,26,['BTC','ETH']);await openTestApp(page);await enter(page);await loaded(page,[2,2,2,2]);await plot(page,2).scrollIntoViewIfNeeded();await frames(page);
 const result=await overview(page),time=result.panels[0].lastClosedTime-7*day,before=await charts(page,time),canvases=await Promise.all(periods.map(weeks=>plot(page,weeks).locator('canvas').first().elementHandle())),beforeCalls=await calls(page),groups=await groupCalls(page);
 const bounds=(await plot(page,2).boundingBox())!;await page.mouse.move(bounds.x+bounds.width/2,bounds.y+130);await page.mouse.wheel(0,-180);
 await expect.poll(async()=>{const item=(await charts(page,time)).find((value:any)=>value.chartId==='weeks-2');return item.logicalRange.to-item.logicalRange.from;}).toBeLessThan(before[0].logicalRange.to-before[0].logicalRange.from);
 await page.mouse.move(0,0);await frames(page);const zoomed=await charts(page,time);for(const item of zoomed.filter((value:any)=>value.chartId!=='weeks-2'))expect(item.logicalRange).toEqual(before.find((value:any)=>value.chartId===item.chartId).logicalRange);
 const rendered=zoomed.find((item:any)=>item.chartId==='weeks-2'),line=rendered.series.find((item:any)=>item.symbol==='BTC'),hoverBounds=(await plot(page,2).boundingBox())!;expect(Number.isFinite(line.y)).toBe(true);
 await page.mouse.move(hoverBounds.x+rendered.leftWidth+rendered.x,hoverBounds.y+line.y);await expect(region(page,2).getByRole('tooltip')).toHaveCount(0);await expect(legend(page,2,'BTC')).toHaveAttribute('data-highlighted','true');await expect(region(page,2).locator('.comparison-readout')).toContainText(`${comparisonDate(time)} UTC`);await expect(legend(page,2,'BTC').locator('.comparison-legend-price')).toHaveText('Close 1,770 USDC');
 for(const weeks of [4,8,12]){await expect(region(page,weeks).getByRole('tooltip')).toHaveCount(0);await expect(region(page,weeks).locator('.comparison-readout')).toContainText('Latest completed daily close');await expect(legend(page,weeks,'BTC').locator('.comparison-legend-price')).toHaveText('Close 1,840 USDC');}
 await region(page,2).screenshot({path:testInfo.outputPath('comparison-overview-independent-hover.png')});
 await legend(page,4,'BTC').click();for(const weeks of periods)await expect(legend(page,weeks,'BTC')).toHaveAttribute('aria-pressed','false');
 await expect.poll(async()=>(await charts(page)).every((item:any)=>item.series.find((series:any)=>series.symbol==='BTC').visible===false&&item.series.find((series:any)=>series.symbol==='BTC').dotsVisible===false)).toBe(true);await expect(panel(page).getByRole('tooltip')).toHaveCount(0);
 const after=await charts(page);for(const item of after)expect(item.logicalRange).toEqual(zoomed.find((value:any)=>value.chartId===item.chartId).logicalRange);for(const canvas of canvases)expect(await canvas!.evaluate(element=>element.isConnected)).toBe(true);
 await legend(page,2,'ETH').click();await expect.poll(async()=>(await charts(page)).every((item:any)=>item.series.every((series:any)=>!series.visible&&!series.dotsVisible))).toBe(true);await expect(panel(page).getByRole('alert')).toHaveCount(0);
 expect(await calls(page)).toEqual(beforeCalls);expect(await groupCalls(page)).toBe(groups);expect(await resources(page)).toEqual({charts:4,observers:8,listeners:4});
 await legend(page,12,'BTC').click();await expect.poll(async()=>(await charts(page)).every((item:any)=>item.series.find((series:any)=>series.symbol==='BTC').visible)).toBe(true);
});

test('missing long baselines leave short windows usable and shared missing closes remain gaps in every panel',async({page})=>{
 await openTestApp(page);await page.evaluate(()=>(window as any).__testComparisonSeries({TON:{missingBaselineWeeks:[8,12]},BTC:{gaps:[80]}}));await enter(page);await loaded(page,[7,7,6,6]);const result=await overview(page);
 for(const weeks of [2,4]){await expect(region(page,weeks).getByLabel('Unavailable comparison assets',{exact:true})).toHaveCount(0);expect(result.panels.find((item:any)=>item.weeks===weeks).series.find((series:any)=>series.asset.id==='TON').points[0].pct).toBe(0);}
 for(const weeks of [8,12]){const item=result.panels.find((item:any)=>item.weeks===weeks);await expect(region(page,weeks).getByLabel('Unavailable comparison assets',{exact:true})).toContainText('TON');await expect(region(page,weeks)).toContainText(comparisonDate(item.baselineTime));expect(item.series.find((series:any)=>series.asset.id==='TON').points).toEqual([]);}
 const time=result.panels[0].lastClosedTime-4*day;for(const item of result.panels)expect(item.series.find((series:any)=>series.asset.id==='BTC').points.find((point:any)=>point.time===time)).toMatchObject({close:null,pct:null});
 const rendered=(await charts(page,time)).find((item:any)=>item.chartId==='weeks-2');await plot(page,2).scrollIntoViewIfNeeded();await frames(page);const bounds=(await plot(page,2).boundingBox())!;await page.mouse.move(bounds.x+rendered.leftWidth+rendered.x,bounds.y+100);await expect(legend(page,2,'BTC').locator('.comparison-legend-price')).toHaveText('Close —');await expect(legend(page,4,'BTC').locator('.comparison-legend-price')).toHaveText('Close 1,840 USDC');expect(await groupCalls(page)).toBe(1);
});

test('shared asset edits refresh one overview group and preserve the single-chart period across layout changes and reload',async({page})=>{
 await saved(page,26,['BTC','ETH']);await openTestApp(page);await enter(page);await loaded(page,[2,2,2,2]);expect((await page.evaluate(()=>(window as any).__testComparisonView())).weeks).toBe(26);
 await page.getByRole('button',{name:'Remove ETH from comparison',exact:true}).click();await loaded(page,[1,1,1,1]);expect(await groupCalls(page)).toBe(2);
 await add(page,'DCR','Binance','Spot','USDT');await loaded(page,[2,2,2,2]);expect(await groupCalls(page)).toBe(3);for(const weeks of periods)await expect(legend(page,weeks,'DCR')).toContainText('USDT');
 expect(await page.evaluate(()=>(window as any).__testComparisonView())).toEqual({weeks:26,assets:['BTC','binance|spot|DCRUSDT']});
 const writes=(await calls(page)).save;await layout(page,'Single chart').click();await expect(page.getByTestId('comparison-chart')).toHaveCount(1);await expect(page.getByRole('spinbutton',{name:'Comparison weeks',exact:true})).toHaveValue('26');await expect.poll(()=>resources(page)).toEqual({charts:1,observers:2,listeners:1});expect(await groupCalls(page)).toBe(3);expect((await calls(page)).save).toBe(writes);
 await panel(page).getByRole('button',{name:'8w',exact:true}).click();await expect.poll(()=>page.evaluate(()=>(window as any).__testComparisonView().weeks)).toBe(8);await layout(page,'Four charts').click();await loaded(page,[2,2,2,2]);expect(await groupCalls(page)).toBe(4);
 await reloadTestApp(page);await enter(page);await loaded(page,[2,2,2,2]);await expect(layout(page,'Four charts')).toHaveAttribute('aria-pressed','true');expect(await page.evaluate(()=>(window as any).__testComparisonView())).toEqual({weeks:8,assets:['BTC','binance|spot|DCRUSDT']});expect((await calls(page)).data).toBe(0);
});

test('late overview responses cannot replace a newer layout or shared asset selection',async({page})=>{
 await openTestApp(page);await page.evaluate(()=>(window as any).__testComparisonHoldNextOverview());await enter(page);await expect.poll(async()=>(await pending(page)).length).toBe(1);const old=(await pending(page))[0];
 await layout(page,'Single chart').click();await expect(page.getByTestId('comparison-chart')).toHaveCount(1);await expect.poll(()=>resources(page)).toEqual({charts:1,observers:2,listeners:1});await expect.poll(()=>page.evaluate(()=>(window as any).__testComparisonCancelled())).toContain(old);
 await page.evaluate(id=>(window as any).__testComparisonReleaseOverview(id),old);await frames(page);await expect(page.getByTestId('comparison-chart')).toHaveCount(1);await expect(layout(page,'Single chart')).toHaveAttribute('aria-pressed','true');
 await layout(page,'Four charts').click();await loaded(page);await page.evaluate(()=>(window as any).__testComparisonHoldNextOverview());await panel(page).getByRole('button',{name:'Refresh',exact:true}).click();await expect.poll(async()=>(await pending(page)).length).toBe(1);const obsolete=(await pending(page))[0];
 await page.getByRole('button',{name:'Remove ETH from comparison',exact:true}).click();await loaded(page,[6,6,6,6]);await expect.poll(()=>page.evaluate(()=>(window as any).__testComparisonCancelled())).toContain(obsolete);const groups=await groupCalls(page);
 await page.evaluate(id=>(window as any).__testComparisonReleaseOverview(id),obsolete);await frames(page);await loaded(page,[6,6,6,6]);for(const weeks of periods)await expect(region(page,weeks).getByLabel('Comparison legend',{exact:true})).not.toContainText('ETH');expect(await groupCalls(page)).toBe(groups);
});

test('settings and navigation cancel a grouped refresh, release all four charts and restore shared visibility',async({page})=>{
 await openTestApp(page);await enter(page);await loaded(page);await legend(page,2,'BTC').click();await page.evaluate(()=>(window as any).__testComparisonHoldNextOverview());await panel(page).getByRole('button',{name:'Refresh',exact:true}).click();await expect.poll(async()=>(await pending(page)).length).toBe(1);const old=(await pending(page))[0];
 await page.getByRole('button',{name:'Open settings',exact:true}).click();const settings=page.getByRole('dialog',{name:'Workspace settings',exact:true});await expect.poll(()=>resources(page)).toEqual({charts:0,observers:0,listeners:0});await expect.poll(()=>page.evaluate(()=>(window as any).__testComparisonCancelled())).toContain(old);const groups=await groupCalls(page);
 await page.evaluate(id=>(window as any).__testComparisonReleaseOverview(id),old);await frames(page);expect(await charts(page)).toEqual([]);expect(await groupCalls(page)).toBe(groups);await settings.getByRole('button',{name:'Close settings',exact:true}).click();await loaded(page);for(const weeks of periods)await expect(legend(page,weeks,'BTC')).toHaveAttribute('aria-pressed','false');
 await page.getByRole('tab',{name:'Screener',exact:true}).click();await expect.poll(()=>resources(page)).toEqual({charts:0,observers:0,listeners:0});expect(await charts(page)).toEqual([]);const hiddenGroups=await groupCalls(page);await page.mouse.move(500,500);await frames(page);expect(await groupCalls(page)).toBe(hiddenGroups);
 await enter(page);await loaded(page);for(const weeks of periods)await expect(legend(page,weeks,'BTC')).toHaveAttribute('aria-pressed','false');await expect.poll(()=>resources(page)).toEqual({charts:4,observers:8,listeners:4});
});

test('an intentionally empty shared selection stays empty without provider work in either layout or after reload',async({page})=>{
 await saved(page,13,[]);await openTestApp(page);await enter(page);await expect(panel(page)).toContainText('Add assets to compare');expect(await groupCalls(page)).toBe(0);expect((await calls(page)).data).toBe(0);expect(await resources(page)).toEqual({charts:0,observers:0,listeners:0});
 for(const name of ['Prev','Today','Next'] as const)await expect(navigate(page,name)).toBeDisabled();
 await layout(page,'Single chart').click();await expect(page.getByRole('spinbutton',{name:'Comparison weeks',exact:true})).toHaveValue('13');await layout(page,'Four charts').click();await expect(page.getByTestId('comparison-chart')).toHaveCount(0);expect(await groupCalls(page)).toBe(0);expect((await calls(page)).data).toBe(0);
 await reloadTestApp(page);await enter(page);await expect(panel(page)).toContainText('Add assets to compare');expect(await groupCalls(page)).toBe(0);await add(page,'BTC');await loaded(page,[1,1,1,1]);expect(await groupCalls(page)).toBe(1);
 const remove=page.getByLabel('Selected comparison assets',{exact:true}).getByRole('button',{name:/^Remove .* from comparison$/});await remove.click();await expect(panel(page)).toContainText('Add assets to compare');await expect.poll(()=>resources(page)).toEqual({charts:0,observers:0,listeners:0});expect(await groupCalls(page)).toBe(1);
});

test('grouped data failures remain retryable without rendering partial invented panels',async({page})=>{
 await openTestApp(page);await page.evaluate(()=>(window as any).__testComparisonErrors({data:'Daily overview source unavailable'}));await enter(page);await expect(panel(page).getByRole('alert')).toContainText('Daily overview source unavailable');await expect(page.getByTestId('comparison-chart')).toHaveCount(0);expect(await resources(page)).toEqual({charts:0,observers:0,listeners:0});expect(await groupCalls(page)).toBe(1);expect((await calls(page)).data).toBe(0);
 await page.evaluate(()=>(window as any).__testComparisonErrors({}));await panel(page).getByRole('button',{name:'Retry daily data',exact:true}).click();await loaded(page);expect(await groupCalls(page)).toBe(2);await expect(panel(page).getByRole('alert')).toHaveCount(0);
});

test('one UTC rollover refresh advances all four cutoffs together and stops scheduling while hidden',async({page})=>{
 await page.clock.install({time:Date.UTC(2026,10,28,23,59,50)});await openTestApp(page);await enter(page);await loaded(page);const before=await overview(page);expect(await groupCalls(page)).toBe(1);
 await page.clock.fastForward(16000);await expect.poll(()=>groupCalls(page)).toBe(2);await loaded(page);const after=await overview(page);
 for(const [index,item] of after.panels.entries()){expect(item.lastClosedTime).toBe(before.panels[index].lastClosedTime+day);expect(item.baselineTime).toBe(before.panels[index].baselineTime+day);}expect(new Set(after.panels.map((item:any)=>item.asOf)).size).toBe(1);expect((await calls(page)).data).toBe(0);expect((await calls(page)).save).toBe(0);
 await page.getByRole('tab',{name:'Screener',exact:true}).click();await expect.poll(()=>resources(page)).toEqual({charts:0,observers:0,listeners:0});await page.clock.fastForward(86410000);await frames(page);expect(await groupCalls(page)).toBe(2);expect(await charts(page)).toEqual([]);
});

test('overview fills wide and tall windows while resize preserves chart instances, zoom and provider work',async({page},testInfo)=>{
 await page.setViewportSize({width:1920,height:1440});await openTestApp(page);await enter(page);await loaded(page);await frames(page);
 const grid=panel(page).locator('.comparison-grid'),firstBounds=(await grid.boundingBox())!;expect(firstBounds.width).toBeGreaterThan(1800);expect(firstBounds.y+firstBounds.height).toBeGreaterThan(1320);expect(firstBounds.x+firstBounds.width).toBeLessThanOrEqual(1920);
 const result=await overview(page),time=result.panels[0].lastClosedTime-7*day,initial=await charts(page,time),canvases=await Promise.all(periods.map(weeks=>plot(page,weeks).locator('canvas').first().elementHandle()));
 for(const item of initial){expect(item.fillAvailable).toBe(true);expect(item.height).toBeGreaterThan(260);}for(const weeks of periods)await expect(region(page,weeks).getByLabel('Comparison legend',{exact:true})).toBeInViewport({ratio:1});
 const bounds=(await plot(page,2).boundingBox())!;await page.mouse.move(bounds.x+bounds.width/2,bounds.y+bounds.height/2);await page.mouse.wheel(0,-180);await expect.poll(async()=>{const item=(await charts(page,time)).find((value:any)=>value.chartId==='weeks-2');return item.logicalRange.to-item.logicalRange.from;}).toBeLessThan(initial[0].logicalRange.to-initial[0].logicalRange.from);await page.mouse.move(0,0);await frames(page);
 const zoomed=await charts(page,time),before=await calls(page),groups=await groupCalls(page);await page.screenshot({path:testInfo.outputPath('comparison-overview-fill-1920x1440.png')});
 await page.setViewportSize({width:1920,height:1800});await expect.poll(async()=>(await charts(page,time)).every((item:any)=>item.height>initial.find((old:any)=>old.chartId===item.chartId).height+100)).toBe(true);
 await page.setViewportSize({width:2200,height:1800});await frames(page);expect((await grid.boundingBox())!.width).toBeGreaterThan(firstBounds.width+200);
 const resized=await charts(page,time);for(const item of resized){const old=zoomed.find((value:any)=>value.chartId===item.chartId);expect(item.logicalRange.from).toBeCloseTo(old.logicalRange.from,5);expect(item.logicalRange.to).toBeCloseTo(old.logicalRange.to,5);}for(const canvas of canvases)expect(await canvas!.evaluate(element=>element.isConnected)).toBe(true);expect(await calls(page)).toEqual(before);expect(await groupCalls(page)).toBe(groups);expect(await resources(page)).toEqual({charts:4,observers:8,listeners:4});
 await page.screenshot({path:testInfo.outputPath('comparison-overview-fill-2200x1800.png')});
 await page.setViewportSize({width:900,height:700});await frames(page);for(const weeks of periods){expect((await plot(page,weeks).boundingBox())!.height).toBeGreaterThanOrEqual(179);const item=region(page,weeks).getByLabel('Comparison legend',{exact:true});await item.scrollIntoViewIfNeeded();await expect(item).toBeInViewport({ratio:1});}expect(await page.evaluate(()=>document.documentElement.scrollWidth)).toBeLessThanOrEqual(900);expect(await groupCalls(page)).toBe(groups);for(const canvas of canvases)expect(await canvas!.evaluate(element=>element.isConnected)).toBe(true);await page.screenshot({path:testInfo.outputPath('comparison-overview-minimum-scroll.png')});
});

test('Prev, Today and Next shift every fixed window by one real UTC day without changing saved choices',async({page})=>{
 await page.clock.install({time:Date.UTC(2026,10,28,10)});await openTestApp(page);await enter(page);await loaded(page);const initial=await overview(page),latest=initial.panels[0].lastClosedTime,view=await page.evaluate(()=>(window as any).__testComparisonView());
 await expect(navigate(page,'Next')).toBeDisabled();const prevBounds=(await navigate(page,'Prev').boundingBox())!,layoutBounds=(await layout(page,'Four charts').boundingBox())!,refreshBounds=(await panel(page).getByRole('button',{name:'Refresh',exact:true}).boundingBox())!;expect(prevBounds.x).toBeLessThan(layoutBounds.x);expect(prevBounds.x).toBeLessThan(refreshBounds.x);
 await navigate(page,'Prev').click();await through(page,latest-day);await expect(navigate(page,'Next')).toBeEnabled();expect((await groupRequests(page)).at(-1).endTime).toBe(latest-day);
 const previous=await overview(page);for(const item of previous.panels){const old=initial.panels.find((value:any)=>value.weeks===item.weeks);expect(item.baselineTime).toBe(old.baselineTime-day);for(const series of item.series){const oldSeries=old.series.find((value:any)=>value.asset.id===series.asset.id);const baseline=series.points[0];expect(baseline.pct).toBe(0);for(const point of series.points){const oldPoint=oldSeries.points.find((value:any)=>value.time===point.time);if(oldPoint)expect(point.close).toBe(oldPoint.close);expect(point.pct).toBeCloseTo((point.close/baseline.close-1)*100,10);}}await expect(legend(page,item.weeks,'BTC').locator('.comparison-legend-price')).toHaveText('Close 1,830 USDC');await expect(region(page,item.weeks).locator('.comparison-readout')).toContainText('Final daily close in this window');}
 await layout(page,'Single chart').click();await expect(page.getByTestId('comparison-chart')).toHaveCount(1);await expect(page.getByTestId('comparison-chart')).toHaveAttribute('data-baseline-time',String(latest-day-view.weeks*7*day));await expect(panel(page).locator('.comparison-end-date')).toContainText(`${comparisonDate(latest-day)} UTC`);expect(await page.evaluate(()=>(window as any).__testComparisonRequests().at(-1).endTime)).toBe(latest-day);expect(await page.evaluate(()=>(window as any).__testComparisonLastResult().lastClosedTime)).toBe(latest-day);await expect(panel(page).locator('.comparison-readout')).toContainText('Final daily close in this window');
 await layout(page,'Four charts').click();await loaded(page);await through(page,latest-day);expect(await page.evaluate(()=>(window as any).__testComparisonView())).toEqual(view);
 await navigate(page,'Prev').click();await through(page,latest-2*day);await navigate(page,'Next').click();await through(page,latest-day);await navigate(page,'Next').click();await through(page,latest);await expect(navigate(page,'Next')).toBeDisabled();expect((await groupRequests(page)).at(-1).endTime).toBe(0);for(const weeks of periods)await expect(region(page,weeks).locator('.comparison-readout')).toContainText('Latest completed daily close');
 await navigate(page,'Prev').click();await through(page,latest-day);const groups=await groupCalls(page);await navigate(page,'Today').click();await through(page,latest);expect(await groupCalls(page)).toBe(groups+1);await expect(navigate(page,'Next')).toBeDisabled();expect((await groupRequests(page)).at(-1).endTime).toBe(0);
 await navigate(page,'Today').click();await expect.poll(()=>groupCalls(page)).toBe(groups+2);expect(await page.evaluate(()=>(window as any).__testComparisonView())).toEqual(view);expect((await calls(page)).save).toBe(0);
});

test('historical end days stay pinned over UTC rollover until Today resumes following new closed days',async({page})=>{
 await page.clock.install({time:Date.UTC(2026,10,28,23,59,50)});await openTestApp(page);await enter(page);await loaded(page);const initial=await overview(page),latest=initial.panels[0].lastClosedTime;
 await navigate(page,'Prev').click();await through(page,latest-day);const groups=await groupCalls(page);await page.clock.fastForward(11000);await frames(page);expect(await groupCalls(page)).toBe(groups);await through(page,latest-day);
 // Click before the 00:00:05 refresh: yesterday is now historical, so Next
 // must select that exact day rather than jumping directly to the new latest.
 await navigate(page,'Next').click();await through(page,latest);expect((await groupRequests(page)).at(-1).endTime).toBe(latest);await expect(navigate(page,'Next')).toBeEnabled();const pinned=await groupCalls(page);await page.clock.fastForward(5000);await frames(page);expect(await groupCalls(page)).toBe(pinned);await through(page,latest);
 await panel(page).getByRole('button',{name:'Refresh',exact:true}).click();await expect.poll(()=>groupCalls(page)).toBe(pinned+1);await through(page,latest);expect((await groupRequests(page)).at(-1).endTime).toBe(latest);await expect(legend(page,2,'BTC').locator('.comparison-legend-price')).toHaveText('Close 1,840 USDC');
 await navigate(page,'Today').click();await through(page,latest+day);await expect(navigate(page,'Next')).toBeDisabled();await expect(legend(page,2,'BTC').locator('.comparison-legend-price')).toHaveText('Close 1,850 USDC');const following=await groupCalls(page);
 await page.clock.fastForward(86400000);await expect.poll(()=>groupCalls(page)).toBe(following+1);await through(page,latest+2*day);expect((await groupRequests(page)).at(-1).endTime).toBe(0);await expect(legend(page,2,'BTC').locator('.comparison-legend-price')).toHaveText('Close 1,860 USDC');expect((await calls(page)).save).toBe(0);
});

test('out-of-order day navigation responses are cancelled and cannot replace the current endpoint',async({page})=>{
 await page.clock.install({time:Date.UTC(2026,10,28,10)});await openTestApp(page);await enter(page);await loaded(page);const initial=await overview(page),latest=initial.panels[0].lastClosedTime;
 await page.evaluate(()=>(window as any).__testComparisonHoldNextOverview());await navigate(page,'Prev').click();await expect.poll(async()=>(await pending(page)).length).toBe(1);const first=(await pending(page))[0];expect((await groupRequests(page)).at(-1).endTime).toBe(latest-day);
 await page.evaluate(()=>(window as any).__testComparisonHoldNextOverview());await navigate(page,'Prev').click();await expect.poll(async()=>(await pending(page)).length).toBe(2);const second=(await pending(page)).find((id:string)=>id!==first)!;expect((await groupRequests(page)).at(-1).endTime).toBe(latest-2*day);
 await navigate(page,'Next').click();await through(page,latest-day);const groups=await groupCalls(page);await expect.poll(()=>page.evaluate(()=>(window as any).__testComparisonCancelled())).toEqual(expect.arrayContaining([first,second]));
 await page.evaluate(({first,second,panels})=>{const fixture=window as any;fixture.__testComparisonReleaseOverview(second);fixture.__testComparisonReleaseOverview(first,{panels});},{first,second,panels:initial.panels});await frames(page);
 // The transport deliberately records the last stale delivery; only the
 // rendered endpoint/baselines prove that production rejected that response.
 await through(page,latest-day,false);expect(await groupCalls(page)).toBe(groups);await expect(navigate(page,'Next')).toBeEnabled();
 await navigate(page,'Today').click();await through(page,latest);await expect(navigate(page,'Next')).toBeDisabled();expect((await groupRequests(page)).at(-1).endTime).toBe(0);expect((await calls(page)).save).toBe(0);
});

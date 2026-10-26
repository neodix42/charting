import {test,expect,type Page} from '@playwright/test';

const savedChart=(page:Page,index=0)=>page.evaluate(index=>(window as any).__testWorkspace().tabs[0].charts[index],index);

test('horizontal timeframe buttons include weekly and restore the saved selection',async({page})=>{
 await page.goto('/');const chart=page.getByTestId('chart-cell');
 await expect(chart.getByRole('group',{name:'Timeframe'}).getByRole('button')).toHaveCount(7);
 await expect(chart.getByRole('combobox',{name:'Timeframe',exact:true})).toHaveCount(0);
 await expect(chart.getByRole('button',{name:'Timeframe 1h',exact:true})).toHaveAttribute('aria-pressed','true');
 const buttons=await chart.getByRole('group',{name:'Timeframe'}).getByRole('button').evaluateAll(elements=>elements.map(element=>element.getBoundingClientRect().top));
 expect(new Set(buttons).size).toBe(1);
 for(const period of ['1m','5m','15m','1h','4h','1d','1w']){await chart.getByRole('button',{name:`Timeframe ${period}`,exact:true}).click();await expect(chart.getByRole('button',{name:`Timeframe ${period}`,exact:true})).toHaveAttribute('aria-pressed','true');}
 await expect(page.getByRole('button',{name:'All changes saved'})).toBeVisible();expect((await savedChart(page)).timeframe).toBe('1w');
 await page.reload();await expect(page.getByRole('button',{name:'Timeframe 1w',exact:true})).toHaveAttribute('aria-pressed','true');await expect(page.getByTestId('chart-cell')).toHaveCount(1);
});

test('all four indicators configure, render, edit, remove and persist independently',async({page})=>{
 const errors:string[]=[];page.on('pageerror',error=>errors.push(error.message));await page.goto('/');await page.getByRole('button',{name:'Add chart',exact:true}).click();
 const chart=page.getByTestId('chart-cell').first(),other=page.getByTestId('chart-cell').nth(1),indicators=chart.getByLabel('Indicators',{exact:true});await expect(chart.getByTestId('chart-canvas').locator('canvas').first()).toBeVisible();const baseCanvases=await chart.getByTestId('chart-canvas').locator('canvas').count();await expect.poll(()=>page.evaluate(()=>(window as any).__testLeases())).toBe(2);const attachCount=await page.evaluate(()=>(window as any).__testAttachCount());
 await indicators.selectOption('sma');let dialog=page.getByRole('dialog',{name:'SMA parameters'});await expect(dialog).toBeVisible();await expect(chart.getByLabel('Applied indicators')).toHaveCount(0);await dialog.getByRole('button',{name:'Cancel',exact:true}).click();await expect(chart.getByLabel('Applied indicators')).toHaveCount(0);
 await indicators.selectOption('ema');await page.getByRole('dialog',{name:'EMA parameters'}).press('Escape');await expect(page.getByRole('dialog')).toHaveCount(0);await expect(chart.getByLabel('Applied indicators')).toHaveCount(0);
 for(const [type,name] of [['sma','SMA'],['ema','EMA'],['ichimoku','Ichimoku Cloud'],['macd','MACD']]){
  await indicators.selectOption(type);dialog=page.getByRole('dialog',{name:`${name} parameters`});await expect(dialog).toBeVisible();if(type==='sma')await dialog.getByLabel('Period',{exact:true}).fill('5');if(type==='ema')await dialog.getByLabel('Period',{exact:true}).fill('8');await dialog.getByRole('button',{name:'OK',exact:true}).click();await expect(page.getByRole('dialog')).toHaveCount(0);
 }
 await expect(chart.locator('.indicator-chip')).toHaveCount(4);await expect(other.locator('.indicator-chip')).toHaveCount(0);await expect.poll(()=>chart.getByTestId('chart-canvas').locator('canvas').count()).toBeGreaterThan(baseCanvases);await page.screenshot({path:'test-results/indicators-all.png'});
 await chart.getByRole('button',{name:'Edit SMA (5)',exact:true}).click();dialog=page.getByRole('dialog',{name:'SMA parameters'});await dialog.getByLabel('Period',{exact:true}).fill('13');await dialog.getByRole('button',{name:'OK',exact:true}).click();await expect(chart.getByRole('button',{name:'Edit SMA (13)',exact:true})).toBeVisible();
 await chart.getByRole('button',{name:'Edit MACD (12, 26, 9)',exact:true}).click();dialog=page.getByRole('dialog',{name:'MACD parameters'});await dialog.getByLabel('Fast period',{exact:true}).fill('30');await dialog.getByRole('button',{name:'OK',exact:true}).click();await expect(dialog.getByRole('alert')).toContainText('Fast period must be smaller');await dialog.getByRole('button',{name:'Cancel',exact:true}).click();
 await expect(page.getByRole('button',{name:'All changes saved'})).toBeVisible();const before=await savedChart(page);expect(before.indicators.map((indicator:any)=>indicator.type)).toEqual(['sma','ema','ichimoku','macd']);expect(before.indicators[0].parameters.period).toBe(13);expect(await page.evaluate(()=>(window as any).__testAttachCount())).toBe(attachCount);
 await page.reload();const restored=page.getByTestId('chart-cell').first();await expect(restored.locator('.indicator-chip')).toHaveCount(4);await expect(restored.getByRole('button',{name:'Edit SMA (13)',exact:true})).toBeVisible();await expect(page.getByTestId('chart-cell').nth(1).locator('.indicator-chip')).toHaveCount(0);
 await restored.getByRole('button',{name:'Remove EMA (8)',exact:true}).click();await expect(restored.locator('.indicator-chip')).toHaveCount(3);await restored.getByRole('button',{name:'Remove MACD (12, 26, 9)',exact:true}).click();await expect(restored.getByTestId('chart-canvas').locator('canvas')).toHaveCount(baseCanvases);await expect(page.getByRole('button',{name:'All changes saved'})).toBeVisible();await page.screenshot({path:'test-results/indicators.png'});expect(errors).toEqual([]);
});

test('indicator controls stay in the header row in a three by three grid',async({page})=>{
 await page.goto('/');await page.getByLabel('Grid layout').selectOption('3x3');for(let i=0;i<8;i++)await page.getByRole('button',{name:'Add chart',exact:true}).click();
 const chart=page.getByTestId('chart-cell').last();await chart.getByRole('button',{name:'Timeframe 1w',exact:true}).click();await chart.getByLabel('Indicators',{exact:true}).selectOption('macd');const dialog=page.getByRole('dialog',{name:'MACD parameters'});await dialog.getByRole('button',{name:'OK',exact:true}).click();await expect(chart.locator('.indicator-chip')).toHaveCount(1);
 const geometry=await chart.locator('.chart-header').evaluate(header=>{
  const rect=header.getBoundingClientRect(),controls=[...header.querySelectorAll('button,select')].map(control=>control.getBoundingClientRect()).filter(control=>control.height>0),centers=controls.map(control=>control.top+control.height/2);
  return{height:rect.height,maxControlHeight:Math.max(...controls.map(control=>control.height)),fontSize:parseFloat(getComputedStyle(document.documentElement).fontSize),centerSpread:Math.max(...centers)-Math.min(...centers),contained:controls.every(control=>control.top>=rect.top-1&&control.bottom<=rect.bottom+1),scroll:header.scrollWidth>header.clientWidth};
 });
 expect(geometry.centerSpread).toBeLessThan(2);expect(geometry.contained).toBe(true);expect(geometry.height).toBeLessThanOrEqual(geometry.maxControlHeight+2*geometry.fontSize);expect(geometry.scroll).toBe(true);
 await expect.poll(()=>page.evaluate(()=>window.__cryptodeskDiagnostics?.())).toMatchObject({chartInstances:9,consumers:9,savedTabs:1,savedCharts:9});
});

test('long indicator periods load earlier history in bounded pages without a new feed lease',async({page})=>{
 const errors:string[]=[];page.on('pageerror',error=>errors.push(error.message));await page.goto('/');await expect(page.getByLabel('OHLCV')).toContainText('C');
 const chart=page.getByTestId('chart-cell');const initialAttachments=await page.evaluate(()=>(window as any).__testAttachCount());
 await page.evaluate(()=>{const api=window.go!.main!.App!;(window as any).__warmupRequests=[];api.GetHistory=async(requestId,feedKey,from,to,maxBars)=>{
  (window as any).__warmupRequests.push({from,to,maxBars});const bars=[];for(let time=from;time<to;time+=3600)bars.push({time,open:60000,high:60030,low:59980,close:60010,volume:2,closed:true,quality:'provisional',sourceRevision:1});return{requestId,feedKey,bars,moreHistory:true};
 };});
 await chart.getByLabel('Indicators',{exact:true}).selectOption('sma');const dialog=page.getByRole('dialog',{name:'SMA parameters'});await dialog.getByLabel('Period',{exact:true}).fill('2000');await dialog.getByRole('button',{name:'OK',exact:true}).click();
 await expect(chart.getByRole('button',{name:'Edit SMA (2000)',exact:true})).toBeVisible();await expect.poll(()=>page.evaluate(()=>(window as any).__warmupRequests.length)).toBe(2);await expect(chart.locator('.indicator-warmup')).toHaveCount(0);
 const requests=await page.evaluate(()=>(window as any).__warmupRequests);expect(requests.map((request:any)=>(request.to-request.from)/3600)).toEqual([1000,900]);expect(requests.every((request:any)=>request.maxBars===1000)).toBe(true);expect(requests[1].to).toBe(requests[0].from);expect(await page.evaluate(()=>(window as any).__testAttachCount())).toBe(initialAttachments);expect(errors).toEqual([]);
});

test('insufficient indicator history explains the required bars and can retry',async({page})=>{
 await page.goto('/');const chart=page.getByTestId('chart-cell');await expect(page.getByLabel('OHLCV')).toContainText('C');await chart.getByLabel('Indicators',{exact:true}).selectOption('ema');const dialog=page.getByRole('dialog',{name:'EMA parameters'});await dialog.getByLabel('Period',{exact:true}).fill('200');await dialog.getByRole('button',{name:'OK',exact:true}).click();
 await expect(chart.locator('.indicator-warmup')).toHaveText(' · needs 200 bars');await expect(chart.getByLabel('Retry indicator history')).toBeVisible();await expect(chart.locator('.chart-status')).toContainText('More history needed for indicators');await chart.getByLabel('Retry indicator history').click();await expect(chart.getByLabel('Retry indicator history')).toBeVisible();await expect(chart.getByRole('button',{name:'Edit EMA (200)',exact:true})).toHaveAttribute('title',/100 bars loaded; 200 required/);
});

test('a delayed indicator history response cannot enter a different timeframe',async({page})=>{
 await page.goto('/');await expect(page.getByLabel('OHLCV')).toContainText('C');const chart=page.getByTestId('chart-cell'),initialAttachments=await page.evaluate(()=>(window as any).__testAttachCount());
 await page.evaluate(()=>{const api=window.go!.main!.App!;let held=false;api.GetHistory=async(requestId,feedKey,from,to,maxBars)=>{
  if(!held&&feedKey.timeframe==='1h'&&maxBars===1000){held=true;return await new Promise(resolve=>{(window as any).__releaseIndicatorHistory=async()=>{const bars=[];for(let time=from;time<to;time+=3600)bars.push({time,open:60000,high:60030,low:59980,close:60010,volume:2,closed:true,quality:'provisional',sourceRevision:1});resolve({requestId,feedKey,bars,moreHistory:true});await new Promise<void>(done=>requestAnimationFrame(()=>requestAnimationFrame(()=>done())));};});}
  return{requestId,feedKey,bars:[],moreHistory:false};
 };});
 await chart.getByLabel('Indicators',{exact:true}).selectOption('sma');const dialog=page.getByRole('dialog',{name:'SMA parameters'});await dialog.getByLabel('Period',{exact:true}).fill('2000');await dialog.getByRole('button',{name:'OK',exact:true}).click();await expect.poll(()=>page.evaluate(()=>typeof(window as any).__releaseIndicatorHistory)).toBe('function');
 await chart.getByRole('button',{name:'Timeframe 1w',exact:true}).click();await expect(chart.getByLabel('Retry indicator history')).toBeVisible();await page.evaluate(()=>(window as any).__releaseIndicatorHistory());
 await expect(chart.getByRole('button',{name:'Edit SMA (2000)',exact:true})).toHaveAttribute('title',/100 bars loaded; 2000 required/);await expect(chart.getByRole('button',{name:'Timeframe 1w',exact:true})).toHaveAttribute('aria-pressed','true');expect(await page.evaluate(()=>(window as any).__testAttachCount())).toBe(initialAttachments+1);
});

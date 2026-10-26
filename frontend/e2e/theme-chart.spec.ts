import {test,expect,type Locator,type Page} from '@playwright/test';
import {openTestApp} from './helpers/testApp';
import {firstWorkspace} from '../src/workspace/model';
import {THEME_OPTIONS,getThemePalette} from '../src/workspace/themes';
import type {AnalysisFile,AnalysisResult} from '../src/workspace/screenerAnalysis';

/** Inspect pixels painted by Lightweight Charts, rather than the surrounding CSS. */
async function paintedColors(canvas:Locator){
 return canvas.evaluate(element=>{
  const surface=element as HTMLCanvasElement,pixels=surface.getContext('2d')!.getImageData(0,0,surface.width,surface.height).data,counts=new Map<string,number>();
  for(let index=0;index<pixels.length;index+=4){
   if(pixels[index+3]!==255)continue;
   const color=`#${[pixels[index],pixels[index+1],pixels[index+2]].map(value=>value.toString(16).padStart(2,'0')).join('')}`;
   counts.set(color,(counts.get(color)??0)+1);
  }
  return{background:[...counts].sort((a,b)=>b[1]-a[1])[0]?.[0],colors:[...counts.keys()]};
 });
}
const resources=(page:Page)=>page.evaluate(()=>(window as any).__testAnalysisChartResources());
const settings=(page:Page)=>page.getByRole('dialog',{name:'Workspace settings',exact:true});

test('all five themes repaint actual chart canvases without replacing feeds, viewports or custom indicators',async({page})=>{
 const errors:string[]=[];page.on('pageerror',error=>errors.push(error.message));
 const workspace=firstWorkspace(),chart=workspace.tabs[0].charts[0];chart.indicators=[{id:crypto.randomUUID(),type:'sma',parameters:{period:20},color:'#457bd4'}];
 await page.addInitScript(value=>{if(!sessionStorage.getItem('cryptodesk-explicit-test-workspace'))sessionStorage.setItem('cryptodesk-explicit-test-workspace',JSON.stringify(value));},workspace);
 await openTestApp(page);await expect(page.getByLabel('OHLCV')).toContainText('61,000');
 // Supply alternating real series inputs so both bullish and bearish candle paint is verified.
 await page.evaluate(()=>{
  const step=3600,end=Math.floor(Date.now()/1000/step)*step;
  for(const subscription of (window as any).__testSubscriptions())(window as any).__testPublish({...subscription,kind:'bars',revision:2,bars:Array.from({length:100},(_,index)=>({time:end-(99-index)*step,open:60000+index*10,high:60030+index*10,low:59980+index*10,close:60000+index*10+(index%2?-10:10),volume:2,closed:index!==99,quality:'provisional',sourceRevision:2}))});
 });
 await expect(page.getByLabel('OHLCV')).toContainText('60,980');
 const canvas=page.getByTestId('chart-canvas').locator('canvas').first(),originalCanvas=await canvas.elementHandle();
 await expect.poll(()=>page.evaluate(()=>(window as any).__testChartGaps().length)).toBe(1);
 await page.evaluate(id=>{(window as any).__testChartZoom(id,9);(window as any).__testChartPan(id,-30);},chart.id);
 await expect.poll(()=>page.evaluate(()=>(window as any).__testChartGaps()[0]?.followingLive)).toBe(false);
 const before=await page.evaluate(()=>(window as any).__testChartGaps()[0]),attachments=await page.evaluate(()=>(window as any).__testAttachCount());
 await page.getByRole('button',{name:'Open settings',exact:true}).click();
 // Repeatedly leave Red Sands, including Red Sands → Light, to catch stale series colors.
 for(const theme of THEME_OPTIONS.flatMap(theme=>[THEME_OPTIONS[4],theme])){
  await settings(page).getByLabel('Theme',{exact:true}).selectOption(theme.id);
  const palette=getThemePalette(theme.id);
  await expect.poll(async()=>(await paintedColors(canvas)).background).toBe(palette.chartBackground);
  await expect.poll(async()=>(await paintedColors(canvas)).colors.includes(palette.up)).toBe(true);
  await expect.poll(async()=>(await paintedColors(canvas)).colors.includes(palette.danger)).toBe(true);
  if(theme.id!=='red-sands'){
   expect((await paintedColors(canvas)).colors).not.toContain(getThemePalette('red-sands').up);
   expect((await paintedColors(canvas)).colors).not.toContain(getThemePalette('red-sands').danger);
  }
  expect(await originalCanvas!.evaluate(element=>element.isConnected)).toBe(true);
  expect(await page.evaluate(()=>(window as any).__testAttachCount())).toBe(attachments);
  const after=await page.evaluate(()=>(window as any).__testChartGaps()[0]);
  expect(after.followingLive).toBe(false);expect(after.barSpacing).toBeCloseTo(before.barSpacing,5);expect(after.visibleTo).toBeCloseTo(before.visibleTo,5);
 }
 await settings(page).getByRole('button',{name:'Close settings',exact:true}).click();
 await expect(page.getByRole('button',{name:'All changes saved',exact:true})).toBeVisible();
 expect((await page.evaluate(()=>(window as any).__testWorkspace())).tabs[0].charts[0].indicators).toEqual(chart.indicators);
 await page.reload();await expect.poll(()=>page.evaluate(()=>document.documentElement.dataset.theme)).toBe('red-sands');
 await expect.poll(async()=>(await paintedColors(page.getByTestId('chart-canvas').locator('canvas').first())).background).toBe(getThemePalette('red-sands').chartBackground);
 expect(errors).toEqual([]);
});

test('an open Analysis chart follows theme changes in place and releases both observers when closed',async({page})=>{
 const errors:string[]=[];page.on('pageerror',error=>errors.push(error.message));
 const file:AnalysisFile={id:'theme-file',name:'btcusd-d1-theme.csv',symbol:'btcusd',timeframe:'d1',fields:['timestamp','open','high','low','close','volume','sma_20','macd_histogram_12_26_9'],bytes:4096};
 const result:AnalysisResult={id:'theme-result',fileId:file.id,name:file.name,symbol:file.symbol,timeframe:file.timeframe,status:'completed',rows:200,matches:1,firstTime:Date.UTC(2024,0,5),lastTime:Date.UTC(2024,0,5)};
 await openTestApp(page);await page.evaluate(file=>(window as any).__testSetEnrichmentFiles([file]),file);
 await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Analysis',exact:true}).click();
 await expect(page.getByRole('button',{name:'Apply conditions',exact:true})).toBeEnabled();
 await page.evaluate(result=>(window as any).__testSetAnalysisInfo({active:false,phase:'completed',total:1,completed:1,failed:0,currentFile:'',progress:100,rows:200,totalMatches:1,matchedFiles:1,results:[result]}),result);
 await page.getByRole('button',{name:`Inspect ${file.name} matches`,exact:true}).click();
 const dialog=page.getByRole('dialog',{name:'Analysis match chart',exact:true}),canvas=dialog.getByTestId('analysis-chart').locator('canvas').first();
 await expect(canvas).toBeVisible();const originalCanvas=await canvas.elementHandle(),requests=await page.evaluate(()=>(window as any).__testAnalysisChartRequests().length);
 for(const theme of THEME_OPTIONS){
  await page.evaluate(theme=>{document.documentElement.dataset.theme=theme;},theme.id);
  const palette=getThemePalette(theme.id);
  await expect.poll(async()=>(await paintedColors(canvas)).background).toBe(palette.chartBackground);
  await expect.poll(async()=>(await paintedColors(canvas)).colors.includes(palette.up)).toBe(true);
  expect(await originalCanvas!.evaluate(element=>element.isConnected)).toBe(true);
  await expect.poll(()=>resources(page)).toEqual({charts:1,observers:2,listeners:2});
 }
 expect(await page.evaluate(()=>(window as any).__testAnalysisChartRequests().length)).toBe(requests);
 await dialog.getByRole('button',{name:'Close analysis chart',exact:true}).click();
 await expect.poll(()=>resources(page)).toEqual({charts:0,observers:0,listeners:0});
 await page.evaluate(()=>{document.documentElement.dataset.theme='matrix';});
 expect(errors).toEqual([]);
});

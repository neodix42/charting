import {test,expect,type Page} from '@playwright/test';
import {openTestApp,reloadTestApp} from './helpers/testApp';

const panel=(page:Page)=>page.getByRole('tabpanel',{name:'Predictions',exact:true});
const tab=(page:Page)=>page.locator('#predictions-tab');
const settings=(page:Page)=>page.getByRole('dialog',{name:'Workspace settings',exact:true});
const modelSettings=(page:Page)=>page.getByRole('form',{name:'Prediction model settings',exact:true});
const calls=(page:Page)=>page.evaluate(()=>(window as any).__testPredictionCalls());
async function enter(page:Page,ready=true){if(ready)await page.evaluate(()=>(window as any).__testPredictionReady());await tab(page).click();await expect(panel(page).getByLabel('Prediction asset',{exact:true})).toBeVisible();if(ready)await expect(panel(page).getByLabel('Prediction asset',{exact:true})).toBeEnabled();}
async function run(page:Page,asset='btcusd',horizon='20'){await panel(page).getByLabel('Prediction asset',{exact:true}).selectOption(asset);await panel(page).getByLabel('Prediction future bars',{exact:true}).fill(horizon);await panel(page).getByRole('button',{name:'Start prediction',exact:true}).click();await expect(panel(page).getByRole('progressbar',{name:'Prediction progress',exact:true})).toBeVisible();await page.evaluate(()=>(window as any).__testFinishPrediction());await expect(panel(page).getByRole('progressbar',{name:'Prediction progress',exact:true})).toHaveCount(0);await expect(panel(page).getByTestId('prediction-chart')).toBeVisible();await expect(panel(page).getByTestId('prediction-chart')).toHaveAttribute('data-forecast-bars',horizon);await expect(panel(page).getByLabel('Selected forecast',{exact:true})).toContainText(`${asset}-d1.csv`);}
async function openSettings(page:Page){await page.getByRole('button',{name:'Open settings',exact:true}).click();await settings(page).getByRole('tab',{name:'Predictions',exact:true}).click();await expect(modelSettings(page).getByLabel('Prediction model',{exact:true})).toBeEnabled();}

test('Predictions is fixed after Screener, pauses chart leases and prepares only after opening',async({page})=>{
 await openTestApp(page);await expect(page.getByTestId('chart-cell')).toHaveCount(1);expect((await calls(page)).get).toBe(0);expect((await calls(page)).prepare).toBe(0);
 const fixed=await page.locator('.tab-row > button[role=tab]').allTextContents();expect(fixed).toEqual(['Screener','Predictions']);
 await enter(page,false);await expect(panel(page).getByRole('progressbar',{name:'Prediction progress'})).toHaveAttribute('value','25');await expect(panel(page)).toContainText('100 MiB / 400 MiB');expect((await calls(page)).prepare).toBe(1);
 await expect.poll(()=>page.evaluate(()=>(window as any).__testLeases())).toBe(0);await expect(page.getByTestId('chart-cell')).toHaveCount(0);await expect(page.getByRole('button',{name:'Add tab',exact:true})).toBeDisabled();await expect(panel(page).getByRole('button',{name:'Start prediction',exact:true})).toBeDisabled();
 await panel(page).getByRole('button',{name:'Cancel prediction',exact:true}).click();await expect(panel(page)).toContainText('Prediction cancelled');
 await page.getByRole('tab',{name:'Screener',exact:true}).click();const before=await calls(page);await expect(page.getByRole('tab',{name:'Analysis',exact:true})).toBeVisible();await tab(page).click();await expect(panel(page)).toContainText('Prediction cancelled');expect((await calls(page)).prepare).toBe(before.prepare);
 await panel(page).getByRole('button',{name:'Retry model preparation',exact:true}).click();expect((await calls(page)).prepare).toBe(2);await page.evaluate(()=>(window as any).__testPredictionReady());await expect(panel(page).getByLabel('Prediction asset',{exact:true})).toBeEnabled();await panel(page).getByLabel('Prediction asset',{exact:true}).selectOption('btcusd');await expect(panel(page).getByRole('button',{name:'Start prediction',exact:true})).toBeEnabled();
 await page.locator('[data-tab-id]').first().click();await expect.poll(()=>page.evaluate(()=>(window as any).__testLeases())).toBe(1);await expect.poll(()=>page.evaluate(()=>(window as any).__testPredictionResources())).toEqual({charts:0,observers:0,listeners:0});
});

test('model settings are atomic, preparation-free and persisted independently from the workspace',async({page})=>{
 await openTestApp(page);await openSettings(page);await expect(modelSettings(page).getByLabel('Prediction model',{exact:true})).toHaveValue('Datadog/Toto-2.0-313m');await expect(modelSettings(page).getByRole('status',{name:'Applied prediction model'})).toContainText('/explicit-test/models');expect((await calls(page)).prepare).toBe(0);
 await modelSettings(page).getByRole('button',{name:'Choose folder…',exact:true}).click();await modelSettings(page).getByRole('button',{name:'Apply prediction settings',exact:true}).click();await expect(modelSettings(page).getByRole('status',{name:'Applied prediction model'})).toContainText('/explicit-test/chosen-models');expect((await calls(page)).prepare).toBe(0);
 expect(JSON.stringify(await page.evaluate(()=>(window as any).__testWorkspace()))).not.toContain('modelDirectory');await reloadTestApp(page);await openSettings(page);await expect(modelSettings(page).getByLabel('Prediction model folder',{exact:true})).toHaveValue('/explicit-test/chosen-models');
 await page.evaluate(()=>{const api=window.go!.main!.App!,save=api.ConfigurePredictions;(window as any).__configureCalls=0;api.ConfigurePredictions=value=>{(window as any).__configureCalls++;return new Promise(resolve=>(window as any).__releaseModelSettings=async()=>resolve(await save(value)));};});
 await modelSettings(page).getByLabel('Prediction model folder',{exact:true}).fill('/changed/models');await modelSettings(page).getByRole('button',{name:'Apply prediction settings',exact:true}).click();await modelSettings(page).dispatchEvent('submit');expect(await page.evaluate(()=>(window as any).__configureCalls)).toBe(1);
 await settings(page).getByRole('tab',{name:'Appearance',exact:true}).click();await page.evaluate(()=>(window as any).__testEmit('prepare-close',{token:'prediction-model-settings'}));expect(await page.evaluate(()=>(window as any).__testCloseConfirmation())).toBeNull();await page.evaluate(()=>(window as any).__releaseModelSettings());await expect.poll(()=>page.evaluate(()=>(window as any).__testCloseConfirmation())).toEqual({token:'prediction-model-settings',discard:false});expect((await calls(page)).prepare).toBe(0);
});

test('selection validates horizon, restricts periods to downloaded files and survives restart',async({page})=>{
 await openTestApp(page);await enter(page);const input=panel(page).getByLabel('Prediction future bars',{exact:true}),start=panel(page).getByRole('button',{name:'Start prediction',exact:true});await expect(panel(page).getByLabel('Prediction historical bars',{exact:true})).toHaveValue('512');await expect(input).toHaveValue('20');
 await panel(page).getByLabel('Prediction asset',{exact:true}).selectOption('btcusd');await panel(page).getByLabel('Prediction period',{exact:true}).selectOption('h1');await panel(page).getByLabel('Prediction historical bars',{exact:true}).selectOption('1024');
 for(const value of ['0','257','1.5','']){await input.fill(value);await expect(start).toBeDisabled();await expect(panel(page)).toContainText('Enter a whole number from 1 to 256');}
 await input.fill('256');await page.getByRole('tab',{name:'Screener',exact:true}).click();await reloadTestApp(page);await enter(page);await expect(panel(page).getByLabel('Prediction asset',{exact:true})).toHaveValue('btcusd');await expect(panel(page).getByLabel('Prediction period',{exact:true})).toHaveValue('h1');await expect(panel(page).getByLabel('Prediction historical bars',{exact:true})).toHaveValue('1024');await expect(input).toHaveValue('256');
 await panel(page).getByLabel('Prediction asset',{exact:true}).selectOption('eurusd');await expect(panel(page).getByLabel('Prediction period',{exact:true})).toHaveValue('d1');expect(await panel(page).getByLabel('Prediction period',{exact:true}).locator('option').allTextContents()).toEqual(['d1']);
});

test('forecasts chart real history and quantiles, automatically open new results and remember an older selection',async({page})=>{
 await page.addInitScript(()=>{const original=CanvasRenderingContext2D.prototype.fillText;(window as any).__predictionPaintedText=[];CanvasRenderingContext2D.prototype.fillText=function(...args:Parameters<CanvasRenderingContext2D['fillText']>){(window as any).__predictionPaintedText.push(args[0]);return original.apply(this,args);};});
 await openTestApp(page);await enter(page);await run(page);await expect(panel(page)).toContainText('Estimated 10th–90th percentiles');await expect(panel(page)).toContainText('Future bars count from the last closed bar');await expect(panel(page).getByTestId('prediction-chart')).toHaveAttribute('data-history-bars','512');await expect(panel(page).getByTestId('prediction-chart')).toHaveAttribute('data-forecast-bars','20');await expect(panel(page).getByLabel('Selected forecast',{exact:true})).toContainText('btcusd-d1.csv');
 await expect.poll(()=>page.evaluate(()=>(window as any).__predictionPaintedText.some((text:string)=>text.startsWith('+')))).toBe(true);expect(await page.evaluate(()=>(window as any).__predictionPaintedText.some((text:string)=>text.includes('1970')))).toBe(false);
 await run(page,'eurusd','10');await expect(panel(page).getByLabel('Selected forecast',{exact:true})).toContainText('eurusd-d1.csv');const stored=panel(page).getByLabel('Saved forecasts',{exact:true});await expect(stored.getByRole('button')).toHaveCount(2);await stored.getByRole('button').filter({hasText:'BTCUSD'}).click();await expect(panel(page).getByLabel('Selected forecast',{exact:true})).toContainText('btcusd-d1.csv');
 await page.getByRole('tab',{name:'Screener',exact:true}).click();await reloadTestApp(page);await enter(page);await expect(panel(page).getByLabel('Selected forecast',{exact:true})).toContainText('btcusd-d1.csv');await expect(stored.getByRole('button').filter({hasText:'BTCUSD'})).toHaveAttribute('aria-pressed','true');
 await run(page,'eurusd','5');await expect(panel(page).getByTestId('prediction-chart')).toHaveAttribute('data-forecast-bars','5');await expect(stored.getByRole('button')).toHaveCount(3);
});

test('prediction chart paints a real shaded band in every theme and releases resources on tool navigation',async({page})=>{
 await openTestApp(page);await enter(page);await run(page,'btcusd','256');
 for(const theme of ['matrix','cyberpunk','red-sands','dark','light']){
  await page.getByRole('button',{name:'Open settings',exact:true}).click();await settings(page).getByLabel('Theme',{exact:true}).selectOption(theme);await settings(page).getByLabel('Font size',{exact:true}).selectOption('20');await settings(page).getByRole('button',{name:'Close settings',exact:true}).click();await expect(panel(page).getByTestId('prediction-chart')).toHaveAttribute('data-chart-font-size','17');
  await expect.poll(()=>panel(page).getByTestId('prediction-chart').evaluate(host=>{const canvas=host.querySelector('canvas')!,ctx=canvas.getContext('2d')!,pixels=ctx.getImageData(0,0,canvas.width,canvas.height).data;const hex=getComputedStyle(document.documentElement).getPropertyValue('--accent').trim(),bg=getComputedStyle(document.documentElement).getPropertyValue('--chart-background').trim();const rgb=(value:string)=>value.startsWith('#')&&value.length===7?[1,3,5].map(start=>parseInt(value.slice(start,start+2),16)):[];const accent=rgb(hex),background=rgb(bg);if(accent.length!==3||background.length!==3)return -1;const expected=accent.map((value,index)=>Math.round(value*.2+background[index]*.8));let count=0;for(let index=0;index<pixels.length;index+=4)if(expected.every((value,channel)=>Math.abs(pixels[index+channel]-value)<=2))count++;return count;})).toBeGreaterThan(100);
  await expect.poll(()=>page.evaluate(()=>(window as any).__testPredictionResources())).toEqual({charts:1,observers:2,listeners:1});
 }
 for(let count=0;count<3;count++){await page.getByRole('tab',{name:'Screener',exact:true}).click();await expect.poll(()=>page.evaluate(()=>(window as any).__testPredictionResources())).toEqual({charts:0,observers:0,listeners:0});await tab(page).click();await expect(panel(page).getByTestId('prediction-chart')).toBeVisible();}
 await page.locator('[data-tab-id]').first().click();await expect.poll(()=>page.evaluate(()=>(window as any).__testPredictionResources())).toEqual({charts:0,observers:0,listeners:0});await expect.poll(()=>page.evaluate(()=>(window as any).__testLeases())).toBe(1);
});

test('preparation and inference failures remain retryable without automatic retry loops',async({page})=>{
 await openTestApp(page);await page.evaluate(()=>{const api=window.go!.main!.App!;(window as any).__realPrepare=api.PreparePredictionModel;api.PreparePredictionModel=async()=>{throw Error('Python 3.11 is required. Install Python and retry.');};});await enter(page,false);await expect(panel(page).getByRole('alert')).toContainText('Python 3.11 is required');await page.getByRole('tab',{name:'Screener',exact:true}).click();await tab(page).click();await expect(panel(page).getByRole('button',{name:'Retry model preparation',exact:true})).toBeVisible();expect((await calls(page)).prepare).toBe(0);
 await page.evaluate(()=>{window.go!.main!.App!.PreparePredictionModel=(window as any).__realPrepare;});await panel(page).getByRole('button',{name:'Retry model preparation',exact:true}).click();await page.evaluate(()=>(window as any).__testPredictionReady());await expect(panel(page).getByLabel('Prediction asset',{exact:true})).toBeEnabled();await panel(page).getByLabel('Prediction asset',{exact:true}).selectOption('btcusd');await page.evaluate(()=>{window.go!.main!.App!.StartPrediction=async()=>{throw Error('The selected file has only 200 closed bars; 512 are required');};});await panel(page).getByRole('button',{name:'Start prediction',exact:true}).click();await expect(panel(page).getByRole('alert')).toContainText('only 200 closed bars');await expect(panel(page).getByRole('button',{name:'Start prediction',exact:true})).toBeEnabled();
});

test('pending selection saves flush before navigation and close; a newer tab request wins',async({page})=>{
 await openTestApp(page);await enter(page);await page.evaluate(()=>{const api=window.go!.main!.App!,save=api.SavePredictionSelection;api.SavePredictionSelection=async value=>{api.SavePredictionSelection=save;await new Promise<void>(resolve=>(window as any).__releasePredictionSelection=resolve);return save(value);};});await panel(page).getByLabel('Prediction asset',{exact:true}).selectOption('btcusd');
 await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.locator('[data-tab-id]').first().click();await expect(tab(page)).toHaveAttribute('aria-selected','true');await page.evaluate(()=>(window as any).__testEmit('prepare-close',{token:'selection-close'}));expect(await page.evaluate(()=>(window as any).__testCloseConfirmation())).toBeNull();await page.evaluate(()=>(window as any).__releasePredictionSelection());await expect(page.locator('[data-tab-id]').first()).toHaveAttribute('aria-selected','true');await expect.poll(()=>page.evaluate(()=>(window as any).__testCloseConfirmation())).toEqual({token:'selection-close',discard:false});expect(await page.evaluate(()=>(window as any).__testPredictionInfo().selection.asset)).toBe('btcusd');
});

test('a stale model status read cannot replace new settings or initiate hidden preparation',async({page})=>{
 await openTestApp(page);await page.evaluate(()=>{const api=window.go!.main!.App!,get=api.GetPredictions;api.GetPredictions=async()=>{api.GetPredictions=get;const value=await get();return new Promise(resolve=>(window as any).__releasePredictionRead=()=>resolve(value));};});await tab(page).click();await expect.poll(()=>page.evaluate(()=>typeof(window as any).__releasePredictionRead)).toBe('function');await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.evaluate(()=>(window as any).__releasePredictionRead());expect((await calls(page)).prepare).toBe(0);
 await openSettings(page);await modelSettings(page).getByLabel('Prediction model folder',{exact:true}).fill('/new/config');await modelSettings(page).getByRole('button',{name:'Apply prediction settings',exact:true}).click();await expect(modelSettings(page).getByRole('status',{name:'Applied prediction model'})).toContainText('/new/config');await settings(page).getByRole('button',{name:'Close settings',exact:true}).click();await enter(page,false);await expect(panel(page).getByRole('progressbar',{name:'Prediction progress'})).toBeVisible();expect((await calls(page)).prepare).toBe(1);
});

test('a failed selection save keeps navigation and shutdown pending until the latest selection is saved',async({page})=>{
 await openTestApp(page);await enter(page);await page.evaluate(()=>{const api=window.go!.main!.App!;(window as any).__savePredictionSelection=api.SavePredictionSelection;api.SavePredictionSelection=async()=>{throw Error('Cannot save selection: disk full');};});await panel(page).getByLabel('Prediction asset',{exact:true}).selectOption('btcusd');await expect(panel(page).getByRole('alert')).toContainText('disk full');
 await page.locator('[data-tab-id]').first().click();await expect(tab(page)).toHaveAttribute('aria-selected','true');await page.evaluate(()=>(window as any).__testEmit('prepare-close',{token:'prediction-selection-error'}));await expect(page.getByText('Save failed during shutdown. Your changes remain open.',{exact:true})).toBeVisible();expect(await page.evaluate(()=>(window as any).__testCloseConfirmation())).toBeNull();
 await page.evaluate(()=>{window.go!.main!.App!.SavePredictionSelection=(window as any).__savePredictionSelection;});await panel(page).getByRole('button',{name:'Retry selection save',exact:true}).click();await page.getByRole('button',{name:'Retry save and close',exact:true}).click();await expect.poll(()=>page.evaluate(()=>(window as any).__testCloseConfirmation())).toEqual({token:'prediction-selection-error',discard:false});expect(await page.evaluate(()=>(window as any).__testPredictionInfo().selection.asset)).toBe('btcusd');
});

test('active predictions reject model configuration, retain the applied folder and can be cancelled after navigation',async({page})=>{
 await openTestApp(page);await enter(page);await panel(page).getByLabel('Prediction asset',{exact:true}).selectOption('btcusd');await panel(page).getByRole('button',{name:'Start prediction',exact:true}).click();await expect(panel(page).getByRole('progressbar',{name:'Prediction progress'})).toBeVisible();await openSettings(page);await modelSettings(page).getByLabel('Prediction model folder',{exact:true}).fill('/not-applied');await modelSettings(page).getByRole('button',{name:'Apply prediction settings',exact:true}).click();await expect(modelSettings(page).getByRole('alert')).toContainText('Finish or cancel');await expect(modelSettings(page).getByRole('status',{name:'Applied prediction model'})).toContainText('/explicit-test/models');
 await settings(page).getByRole('button',{name:'Close settings',exact:true}).click();await page.getByRole('tab',{name:'Screener',exact:true}).click();await tab(page).click();await panel(page).getByRole('button',{name:'Cancel prediction',exact:true}).click();await expect(panel(page)).toContainText('Prediction cancelled');expect((await calls(page)).start).toBe(1);expect((await calls(page)).cancel).toBe(1);
});

test('Start rechecks readiness after a model settings change and never invokes the old model',async({page})=>{
 await openTestApp(page);await enter(page);await panel(page).getByLabel('Prediction asset',{exact:true}).selectOption('btcusd');
 await page.evaluate(()=>{const api=window.go!.main!.App!,get=api.GetPredictions;api.GetPredictions=async()=>{await api.ConfigurePredictions({model:'Datadog/Toto-2.0-313m',modelDirectory:'/changed-before-start'});api.GetPredictions=get;return get();};});await panel(page).getByRole('button',{name:'Start prediction',exact:true}).click();await expect.poll(()=>page.evaluate(()=>(window as any).__testPredictionInfo().settings.modelDirectory)).toBe('/changed-before-start');expect((await calls(page)).start).toBe(0);await expect(panel(page).getByRole('button',{name:'Start prediction',exact:true})).toBeDisabled();
});

test('a new model folder starts one preparation after the previous preparation was cancelled',async({page})=>{
 await openTestApp(page);await enter(page,false);await expect(panel(page).getByRole('progressbar',{name:'Prediction progress',exact:true})).toBeVisible();await panel(page).getByRole('button',{name:'Cancel prediction',exact:true}).click();await expect(panel(page)).toContainText('Prediction cancelled');expect((await calls(page)).prepare).toBe(1);
 await openSettings(page);await modelSettings(page).getByLabel('Prediction model folder',{exact:true}).fill('/second/model-folder');await modelSettings(page).getByRole('button',{name:'Apply prediction settings',exact:true}).click();await expect(modelSettings(page).getByRole('status',{name:'Applied prediction model'})).toContainText('/second/model-folder');expect((await calls(page)).prepare).toBe(1);
 await settings(page).getByRole('button',{name:'Close settings',exact:true}).click();await expect(panel(page).getByRole('progressbar',{name:'Prediction progress',exact:true})).toBeVisible();expect((await calls(page)).prepare).toBe(2);
 await page.getByRole('tab',{name:'Screener',exact:true}).click();await tab(page).click();await expect(panel(page).getByRole('progressbar',{name:'Prediction progress',exact:true})).toBeVisible();expect((await calls(page)).prepare).toBe(2);
});

const stored=(page:Page)=>panel(page).getByLabel('Saved forecasts',{exact:true});
async function removeForecast(page:Page,id:string){
 await stored(page).locator(`button[data-forecast-id="${id}"]`).click({button:'right'});
 await page.getByRole('menuitem',{name:'Remove',exact:true}).click();
 await expect(stored(page).locator(`button[data-forecast-id="${id}"]`)).toHaveCount(0);
}

test('Saved forecasts Remove deletes only the unselected forecast durably and preserves the selected chart and inputs',async({page})=>{
 await openTestApp(page);await enter(page);await run(page);await run(page,'eurusd','10');
 const before=await page.evaluate(()=>(window as any).__testPredictionInfo()),[selected,other]=before.forecasts;
 await panel(page).getByLabel('Prediction historical bars',{exact:true}).selectOption('1024');
 await removeForecast(page,other.id);
 await expect(stored(page).getByRole('button')).toHaveCount(1);await expect(stored(page).locator('button')).toHaveAttribute('aria-pressed','true');
 await expect(panel(page).getByLabel('Selected forecast',{exact:true})).toContainText('eurusd-d1.csv');
 await expect(panel(page).getByLabel('Prediction historical bars',{exact:true})).toHaveValue('1024');
 expect((await calls(page)).remove).toBe(1);expect((await page.evaluate(()=>(window as any).__testPredictionInfo())).forecasts.map((value:any)=>value.id)).toEqual([selected.id]);
 await reloadTestApp(page);await enter(page);await expect(stored(page).getByRole('button')).toHaveCount(1);await expect(panel(page).getByLabel('Selected forecast',{exact:true})).toContainText('eurusd-d1.csv');
 await expect(panel(page).getByLabel('Prediction historical bars',{exact:true})).toHaveValue('1024');
});

test('removing the selected forecast opens the remaining forecast and removing the last clears the chart',async({page})=>{
 await openTestApp(page);await enter(page);await run(page);await run(page,'eurusd','10');
 const [newest,older]=await page.evaluate(()=>(window as any).__testPredictionInfo().forecasts);
 await stored(page).locator(`button[data-forecast-id="${older.id}"]`).click();await expect(panel(page).getByLabel('Selected forecast',{exact:true})).toContainText('btcusd-d1.csv');
 await removeForecast(page,older.id);await expect(panel(page).getByLabel('Selected forecast',{exact:true})).toContainText('eurusd-d1.csv');
 await expect(stored(page).locator('button')).toBeFocused();
 expect(await page.evaluate(()=>(window as any).__testPredictionInfo().selection.forecastId)).toBe(newest.id);
 await removeForecast(page,newest.id);await expect(stored(page)).toContainText('Completed forecasts appear here.');await expect(stored(page).getByRole('heading')).toBeFocused();
 await expect(panel(page).getByTestId('prediction-chart')).toHaveCount(0);await expect(panel(page).getByLabel('Selected forecast',{exact:true})).toContainText('Select an asset and start a prediction');
 await expect.poll(()=>page.evaluate(()=>(window as any).__testPredictionResources())).toEqual({charts:0,observers:0,listeners:0});
 expect(await page.evaluate(()=>(window as any).__testPredictionInfo().selection.forecastId)).toBe('');
 await reloadTestApp(page);await enter(page);await expect(stored(page).getByRole('button')).toHaveCount(0);await expect(panel(page).getByTestId('prediction-chart')).toHaveCount(0);
});

test('Saved forecast menu supports keyboard, dismissal and focus without removing or changing selection',async({page})=>{
 await openTestApp(page);await enter(page);await run(page);
 const forecast=stored(page).getByRole('button'),menu=page.getByRole('menu',{name:'Saved forecast BTCUSD d1',exact:true}),item=page.getByRole('menuitem',{name:'Remove',exact:true});
 await forecast.focus();await page.keyboard.press('Shift+F10');await expect(menu).toBeVisible();await expect(item).toBeFocused();
 await page.keyboard.press('End');await expect(item).toBeFocused();await page.keyboard.press('ArrowUp');await expect(item).toBeFocused();
 await page.keyboard.press('Escape');await expect(menu).toHaveCount(0);await expect(forecast).toBeFocused();
 await forecast.click({button:'right'});await page.evaluate(()=>window.dispatchEvent(new Event('resize')));await expect(menu).toHaveCount(0);await expect(forecast).toBeFocused();
 await forecast.click({button:'right'});await page.evaluate(()=>document.dispatchEvent(new Event('scroll')));await expect(menu).toHaveCount(0);await expect(forecast).toBeFocused();
 await forecast.dispatchEvent('keydown',{key:'ContextMenu'});await expect(menu).toBeVisible();await panel(page).getByRole('heading',{name:'Predictions',exact:true}).click();await expect(menu).toHaveCount(0);
 await forecast.click({button:'right'});await expect(menu).toBeVisible();await page.getByRole('button',{name:'Open settings',exact:true}).click();await expect(menu).toHaveCount(0);await settings(page).getByRole('button',{name:'Close settings',exact:true}).click();
 await forecast.click({button:'right'});await page.getByRole('tab',{name:'Screener',exact:true}).click();await expect(menu).toHaveCount(0);expect((await calls(page)).remove).toBe(0);
});

test('Remove flushes pending selection, blocks duplicate requests and normal close awaits deletion',async({page})=>{
 await openTestApp(page);await enter(page);await run(page);await run(page,'eurusd','10');
 const older=await page.evaluate(()=>(window as any).__testPredictionInfo().forecasts[1].id);
 await page.evaluate(()=>{
  const api=window.go!.main!.App!,save=api.SavePredictionSelection,remove=api.DeletePredictionForecast;
  api.SavePredictionSelection=async value=>{api.SavePredictionSelection=save;await new Promise<void>(resolve=>(window as any).__releaseRemoveSelection=resolve);return save(value);};
  api.DeletePredictionForecast=async id=>{(window as any).__forecastRemoveId=id;await new Promise<void>(resolve=>(window as any).__releaseForecastRemove=resolve);return remove(id);};
 });
 const origin=stored(page).locator(`button[data-forecast-id="${older}"]`);await origin.click();await origin.click({button:'right'});
 await page.getByRole('menuitem',{name:'Remove',exact:true}).evaluate(element=>{(element as HTMLButtonElement).click();(element as HTMLButtonElement).click();});
 await expect(origin).toBeDisabled();expect(await page.evaluate(()=>(window as any).__forecastRemoveId)).toBeUndefined();
 await page.evaluate(()=>(window as any).__testEmit('prepare-close',{token:'forecast-remove-close'}));expect(await page.evaluate(()=>(window as any).__testCloseConfirmation())).toBeNull();
 await page.evaluate(()=>(window as any).__releaseRemoveSelection());await expect.poll(()=>page.evaluate(()=>(window as any).__forecastRemoveId)).toBe(older);
 expect(await page.evaluate(()=>(window as any).__testCloseConfirmation())).toBeNull();await page.evaluate(()=>(window as any).__releaseForecastRemove());
 await expect.poll(()=>page.evaluate(()=>(window as any).__testCloseConfirmation())).toEqual({token:'forecast-remove-close',discard:false});
 await expect(origin).toHaveCount(0);expect((await calls(page)).remove).toBe(1);
});

test('failed pending Remove preserves forecasts and chart and pauses normal close until retry',async({page})=>{
 await openTestApp(page);await enter(page);await run(page);
 await page.evaluate(()=>{const api=window.go!.main!.App!;(window as any).__originalForecastRemove=api.DeletePredictionForecast;api.DeletePredictionForecast=async()=>{await new Promise<void>(resolve=>(window as any).__rejectForecastRemove=resolve);throw Error('Cannot remove forecast: read-only folder');};});
 const origin=stored(page).getByRole('button');await origin.click({button:'right'});await page.getByRole('menuitem',{name:'Remove',exact:true}).click();
 await page.evaluate(()=>(window as any).__testEmit('prepare-close',{token:'failed-forecast-remove'}));expect(await page.evaluate(()=>(window as any).__testCloseConfirmation())).toBeNull();await page.evaluate(()=>(window as any).__rejectForecastRemove());
 await expect(stored(page).getByRole('alert')).toContainText('read-only folder');await expect(origin).toBeEnabled();await expect(origin).toBeFocused();await expect(panel(page).getByTestId('prediction-chart')).toBeVisible();await expect(page.getByText('Save failed during shutdown. Your changes remain open.',{exact:true})).toBeVisible();
 expect(await page.evaluate(()=>(window as any).__testCloseConfirmation())).toBeNull();
 await page.evaluate(()=>{window.go!.main!.App!.DeletePredictionForecast=(window as any).__originalForecastRemove;});
 await origin.click({button:'right'});await page.getByRole('menuitem',{name:'Remove',exact:true}).click();await expect(origin).toHaveCount(0);await page.getByRole('button',{name:'Retry save and close',exact:true}).click();
 await expect.poll(()=>page.evaluate(()=>(window as any).__testCloseConfirmation())).toEqual({token:'failed-forecast-remove',discard:false});
});

test('stale status and forecast reads cannot restore a removed selected forecast',async({page})=>{
 await openTestApp(page);await enter(page);await run(page);await run(page,'eurusd','10');
 const [newest,older]=await page.evaluate(()=>(window as any).__testPredictionInfo().forecasts);
 await page.evaluate(id=>{
  const api=window.go!.main!.App!,get=api.GetPredictions,forecast=api.GetPredictionForecast;
  api.GetPredictions=async()=>{api.GetPredictions=get;const value=await get();return new Promise(resolve=>(window as any).__releaseStaleForecastList=()=>resolve(value));};
  api.GetPredictionForecast=async value=>{if(value!==id)return forecast(value);const snapshot=await forecast(value);return new Promise(resolve=>(window as any).__releaseRemovedForecast=()=>resolve(snapshot));};
 },older.id);
 await stored(page).locator(`button[data-forecast-id="${older.id}"]`).click();await expect.poll(()=>page.evaluate(()=>typeof(window as any).__releaseRemovedForecast)).toBe('function');
 await expect.poll(()=>page.evaluate(()=>typeof(window as any).__releaseStaleForecastList)).toBe('function');await removeForecast(page,older.id);
 await expect(panel(page).getByLabel('Selected forecast',{exact:true})).toContainText('eurusd-d1.csv');
 await page.evaluate(()=>{(window as any).__releaseRemovedForecast();(window as any).__releaseStaleForecastList();});
 await expect(stored(page).getByRole('button')).toHaveCount(1);await expect(stored(page).locator('button')).toHaveAttribute('data-forecast-id',newest.id);await expect(panel(page).getByLabel('Selected forecast',{exact:true})).toContainText('eurusd-d1.csv');
});

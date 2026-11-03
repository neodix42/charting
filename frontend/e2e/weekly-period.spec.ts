import {test,expect} from '@playwright/test';
import {openTestApp,reloadTestApp} from './helpers/testApp';
import type {ScreenerDataFile} from '../src/types';

const files:ScreenerDataFile[]=['d1','w1'].map(timeframe=>({id:`btc-${timeframe}`,name:`btcusd-${timeframe}.csv`,symbol:'btcusd',timeframe,fields:['timestamp','open','high','low','close','volume'],bytes:4096}));

test('Downloader selects and restores w1 and explains retained daily history',async({page})=>{
 await openTestApp(page);await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Downloader',exact:true}).click();
 const period=page.getByLabel('Download timeframe',{exact:true});await period.selectOption('w1');
 await expect(page.locator('#screener-downloader')).toContainText('Existing daily bars are reused; missing daily data is downloaded and kept alongside the weekly CSV.');
 await page.getByRole('checkbox',{name:'BTCUSD — Bitcoin vs US Dollar',exact:true}).check();
 await page.getByRole('button',{name:'Download 1 symbol',exact:true}).click();
 await expect.poll(()=>page.evaluate(()=>(window as any).__testScreenerRequest()?.timeframe)).toBe('w1');
 await expect(page.locator('.screener-job')).toHaveCount(2);await expect(page.locator('.screener-job-title > span:first-of-type')).toHaveText(['d1','w1']);await expect(page.getByLabel('Download summary',{exact:true})).toContainText('2 total');
 await page.getByRole('button',{name:'Cancel downloads',exact:true}).click();await reloadTestApp(page);
 await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Downloader',exact:true}).click();await expect(period).toHaveValue('w1');
});

test('Enrichment scopes w1 to weekly files while retaining daily files in the inventory',async({page})=>{
 await openTestApp(page);await page.evaluate(value=>(window as any).__testSetEnrichmentFiles(value),files);
 await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Enrichment',exact:true}).click();
 await page.getByLabel('Enrichment period',{exact:true}).selectOption('w1');await expect(page.getByLabel('Enrichment file scope',{exact:true})).toHaveText('1 of 2 files selected · w1');
 await page.getByRole('button',{name:'Enrich 1 file',exact:true}).click();
 await expect.poll(()=>page.evaluate(()=>(window as any).__testEnrichmentRequest()?.period)).toBe('w1');
 expect(await page.evaluate(()=>(window as any).__testEnrichment().jobs.map((job:any)=>job.fileId))).toEqual(['btc-w1']);
 await expect(page.locator('#screener-enrichment tbody tr')).toHaveCount(1);await expect(page.locator('#screener-enrichment tbody tr')).toContainText('btcusd-w1.csv');
 expect(await page.evaluate(()=>(window as any).__testEnrichmentFiles().map((file:ScreenerDataFile)=>file.timeframe))).toEqual(['d1','w1']);
});

test('Analysis saves and restores w1 and scans only weekly files',async({page})=>{
 await openTestApp(page);await page.evaluate(value=>{(window as any).__testSetEnrichmentFiles(value);const view=(window as any).__testAnalysisView();(window as any).__testSetAnalysisView({...view,profitabilityFilter:0});},files);
 await page.getByRole('tab',{name:'Screener',exact:true}).click();
 const period=page.getByLabel('Analysis period of data',{exact:true});await period.selectOption('w1');await expect(page.locator('.analysis-apply-row')).toContainText('1 of 2 files');
 await page.getByRole('button',{name:'Save as new',exact:true}).click();await page.getByLabel('Condition name',{exact:true}).fill('Weekly setup');await page.getByRole('dialog',{name:'Save condition',exact:true}).getByRole('button',{name:'Save condition',exact:true}).click();
 await expect.poll(()=>page.evaluate(()=>(window as any).__testSavedAnalysisConditions()[0]?.request.period)).toBe('w1');
 await reloadTestApp(page);await page.getByRole('tab',{name:'Screener',exact:true}).click();await expect(period).toHaveValue('w1');
 await page.getByRole('button',{name:'Apply conditions',exact:true}).click();await expect.poll(()=>page.evaluate(()=>(window as any).__testAnalysisRequest()?.period)).toBe('w1');await expect(page.locator('.analysis-scan-status')).toContainText('0 / 1 files');
});

test('Predictions selects downloaded w1 history, forecasts it and restores the weekly selection',async({page})=>{
 await openTestApp(page);await page.evaluate(()=>(window as any).__testPredictionReady());await page.locator('#predictions-tab').click();
 const panel=page.getByRole('tabpanel',{name:'Predictions',exact:true}),period=panel.getByLabel('Prediction period',{exact:true});
 await panel.getByLabel('Prediction asset',{exact:true}).selectOption('btcusd');await period.selectOption('w1');await panel.getByRole('button',{name:'Start prediction',exact:true}).click();
 await expect.poll(()=>page.evaluate(()=>(window as any).__testPredictionInfo().selection.period)).toBe('w1');await page.evaluate(()=>(window as any).__testFinishPrediction());
 await expect(panel.getByLabel('Selected forecast',{exact:true})).toContainText('btcusd-w1.csv');await expect(panel.getByTestId('prediction-chart')).toHaveAttribute('data-history-bars','512');
 await page.getByRole('tab',{name:'Screener',exact:true}).click();await reloadTestApp(page);await page.locator('#predictions-tab').click();await expect(period).toHaveValue('w1');await expect(panel.getByLabel('Selected forecast',{exact:true})).toContainText('btcusd-w1.csv');
});

import {test,expect,type Page} from '@playwright/test';
import {openTestApp,reloadTestApp} from './helpers/testApp';
import type {ScreenerDataFile} from '../src/types';
import {calendarRangeNames} from '../../screener-runtime/calendar-ranges.mjs';

const control=(page:Page,name:string)=>page.getByLabel(name,{exact:true});
const fields=['timestamp','open','high','low','close','volume'];
const file=(id:string,timeframe:string,dataSource:'hyperliquid'|'dukascopy'='hyperliquid'):ScreenerDataFile=>({id,name:`${dataSource}_${id}-${timeframe}-2024-01-01-2024-02-01.csv`,symbol:id,timeframe,dataSource,fields:[...fields],bytes:1000});
async function screen(page:Page,tab:string){await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:tab,exact:true}).click();}

test('Hyperliquid H1 downloads retain the selected dates/timeframe and queue only native hourly jobs',async({page})=>{
 await openTestApp(page);await screen(page,'Downloader');await control(page,'Download data source').selectOption('hyperliquid');
 await expect(control(page,'Download timeframe').locator('option')).toHaveText(['h1','d1','w1']);await control(page,'Download timeframe').selectOption('h1');
 await control(page,'Download from date').fill('2000-01-01');await control(page,'Download to date').fill('2026-11-09');
 await expect(page.locator('#screener-downloader')).toContainText('208 days at H1');
 await expect.poll(()=>page.evaluate(()=>JSON.parse(sessionStorage.getItem('cryptodesk-explicit-test-screener-view')||'null').timeframe)).toBe('h1');
 await reloadTestApp(page);await screen(page,'Downloader');await expect(control(page,'Download data source')).toHaveValue('hyperliquid');await expect(control(page,'Download timeframe')).toHaveValue('h1');
 await page.getByRole('button',{name:'Download 3 symbols',exact:true}).click();
 expect(await page.evaluate(()=>(window as any).__testScreenerRequest())).toMatchObject({dataSource:'hyperliquid',timeframe:'h1',from:'2000-01-01',to:'2026-11-09'});
 await expect(page.locator('.screener-job')).toHaveCount(3);await expect(page.locator('.screener-job-title>span:first-of-type')).toHaveText(['h1','h1','h1']);
 await page.getByRole('button',{name:'Cancel downloads',exact:true}).click();
});

test('a Hyperliquid H1 range group submits one operation for all twelve indicators and exposes the resulting Analysis fields',async({page})=>{
 await openTestApp(page);await page.evaluate(files=>{(window as any).__testSetEnrichmentFiles(files);(window as any).__testSetAnalysisView({...((window as any).__testAnalysisView()),profitabilityFilter:0});},[file('btc','h1'),file('hype','h1'),file('btc-daily','d1'),file('eurusd','h1','dukascopy')]);
 await screen(page,'Enrichment');await control(page,'Enrichment data source').selectOption('hyperliquid');await expect(control(page,'Enrichment period').locator('option')).toHaveText(['All periods','h1','d1','w1']);
 await control(page,'Enrichment period').selectOption('h1');await control(page,'Enrichment indicator').selectOption('group:Day / Week ranges');
 await expect(control(page,'Period')).toHaveCount(0);await expect(control(page,'Enrichment price source')).toBeDisabled();await expect(page.locator('.enrichment-help')).toContainText('Adds all 12 indicators');
 for(const name of Object.values(calendarRangeNames))await expect(page.locator('.enrichment-help')).toContainText(name);
 await page.getByRole('button',{name:'Enrich 2 files',exact:true}).click();
 expect(await page.evaluate(()=>(window as any).__testEnrichmentRequest())).toEqual({dataSource:'hyperliquid',indicator:'',group:'Day / Week ranges',parameters:{},source:'auto',period:'h1'});
 expect(await page.evaluate(()=>(window as any).__testEnrichment().total)).toBe(2);
 await page.evaluate(ids=>{
  const info=(window as any).__testEnrichment();(window as any).__testSetEnrichmentFiles((window as any).__testEnrichmentFiles().map((file:ScreenerDataFile)=>file.dataSource==='hyperliquid'&&file.timeframe==='h1'?{...file,fields:[...file.fields,...ids]}:file));
  (window as any).__testSetEnrichment({...info,active:false,completed:2,jobs:info.jobs.map((job:any)=>({...job,status:'completed',progress:100,columns:ids}))});
 },Object.keys(calendarRangeNames));
 await expect(page.getByRole('button',{name:'Enrich 2 files',exact:true})).toBeEnabled();await expect(page.locator('#screener-enrichment tbody tr').first()).toContainText('previous_week_high');
 await screen(page,'Analysis');await control(page,'Analysis data source').selectOption('hyperliquid');await control(page,'Analysis period of data').selectOption('h1');
 await control(page,'Condition 1 left field').selectOption('today_low');await control(page,'Condition 1 right type').selectOption('field');await control(page,'Condition 1 right field').selectOption('previous_day_low');
 await expect(page.locator('.analysis-enrichment-message')).toHaveCount(0);
});

test('all-group choices show defaults and respect intraday restrictions while retaining individual controls',async({page})=>{
 await openTestApp(page);await page.evaluate(files=>(window as any).__testSetEnrichmentFiles(files),[file('eurusd','h1','dukascopy'),file('daily','d1','dukascopy')]);await screen(page,'Enrichment');
 const indicator=control(page,'Enrichment indicator');await indicator.selectOption('group:Moving averages');await expect(control(page,'Period')).toHaveCount(0);await expect(control(page,'Enrichment price source')).toBeEnabled();
 await expect(page.locator('.enrichment-help')).toContainText('default parameters');await expect(page.locator('.enrichment-help')).toContainText('Period: 200');
 await indicator.selectOption('group:Day / Week ranges');await expect(page.getByRole('button',{name:'Enrich 1 file',exact:true})).toBeEnabled();
 await control(page,'Enrichment period').selectOption('d1');await expect(indicator).toHaveValue('sma');await expect(indicator.locator('option[value="group:Day / Week ranges"]')).toHaveCount(0);
 await indicator.selectOption('group:Time');await page.getByRole('button',{name:'Enrich 1 file',exact:true}).click();expect(await page.evaluate(()=>(window as any).__testEnrichmentRequest())).toMatchObject({indicator:'',group:'Time',parameters:{},source:'auto',period:'d1'});
 await page.getByRole('button',{name:'Cancel enrichment',exact:true}).click();await indicator.selectOption('sma');await expect(control(page,'Period')).toHaveValue('200');
});

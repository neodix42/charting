import {test,expect,type Page} from '@playwright/test';
import {openTestApp,reloadTestApp} from './helpers/testApp';
import type {AnalysisFile} from '../src/workspace/screenerAnalysis';

const control=(page:Page,name:string)=>page.getByLabel(name,{exact:true});
const file=(timeframe:string,dataSource:'dukascopy'|'hyperliquid'='dukascopy'):AnalysisFile=>({id:`${dataSource}-${timeframe}`,name:`${dataSource}_btc-${timeframe}-2024-01-01-2024-02-01.csv`,symbol:'btc',timeframe,dataSource,category:'Crypto assets',fields:['timestamp','open','high','low','close','volume'],bytes:1000});
async function analysis(page:Page){await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Analysis',exact:true}).click();}

test('daily signal limit appears beside profitability for intraday scans, persists with saved conditions and is submitted unchanged',async({page})=>{
 await openTestApp(page);await page.evaluate(files=>{(window as any).__testSetEnrichmentFiles(files);(window as any).__testSetAnalysisView({...((window as any).__testAnalysisView()),period:'h1',profitabilityFilter:0});},[file('h1'),file('d1'),file('w1'),file('h1','hyperliquid')]);
 await analysis(page);const toggle=page.getByRole('checkbox',{name:'Only one signal a day',exact:true});await expect(toggle).not.toBeChecked();
 const row=page.locator('.analysis-period');await expect(row.getByLabel('Analysis profitability filter',{exact:true})).toBeVisible();await expect(row.getByRole('checkbox')).toBeVisible();
 await toggle.check();await page.getByRole('button',{name:'Save as new',exact:true}).click();const dialog=page.getByRole('dialog',{name:'Save condition',exact:true});await dialog.getByLabel('Condition name',{exact:true}).fill('First intraday signal');await dialog.getByRole('button',{name:'Save condition',exact:true}).click();await expect(dialog).toHaveCount(0);
 await expect.poll(()=>page.evaluate(()=>JSON.parse(sessionStorage.getItem('cryptodesk-explicit-test-analysis-view')||'null')?.onlyOneSignalPerDay)).toBe(true);
 await reloadTestApp(page);await analysis(page);await expect(toggle).toBeChecked();
 await toggle.uncheck();await page.getByRole('button',{name:'Load condition First intraday signal',exact:true}).click();await expect(toggle).toBeChecked();
 for(const period of ['d1','w1']){await control(page,'Analysis period of data').selectOption(period);await expect(toggle).toHaveCount(0);}
 await control(page,'Analysis period of data').selectOption('all');await expect(toggle).toBeChecked();
 await control(page,'Analysis period of data').selectOption('h1');await page.getByRole('button',{name:'Apply conditions',exact:true}).click();
 expect(await page.evaluate(()=>(window as any).__testAnalysisRequest())).toMatchObject({period:'h1',onlyOneSignalPerDay:true});
 await page.getByRole('button',{name:'Cancel analysis',exact:true}).click();
 await control(page,'Analysis data source').selectOption('hyperliquid');await expect(toggle).toBeChecked();await expect(page.getByRole('button',{name:'Apply conditions',exact:true})).toBeEnabled();await page.getByRole('button',{name:'Apply conditions',exact:true}).click();
 expect(await page.evaluate(()=>(window as any).__testAnalysisRequest())).toMatchObject({dataSource:'hyperliquid',period:'h1',onlyOneSignalPerDay:true});
});

test('day-offset fields require enrichment and remain parameterless single-day conditions',async({page})=>{
 await openTestApp(page);await page.evaluate(files=>{(window as any).__testSetEnrichmentFiles(files);(window as any).__testSetAnalysisView({...((window as any).__testAnalysisView()),period:'h1',profitabilityFilter:0});},[file('h1')]);await analysis(page);
 const ids=['two_days_ago_low','two_days_ago_high','three_days_ago_low','three_days_ago_high'];
 for(const id of ids){await control(page,'Condition 1 left field').selectOption(id);await expect(control(page,'Condition 1 left lookback bars')).toHaveCount(0);await expect(page.getByRole('group',{name:'Condition 1 left',exact:true}).getByRole('status')).toContainText('Enrichment');}
 await page.evaluate(ids=>(window as any).__testSetEnrichmentFiles((window as any).__testEnrichmentFiles().map((file:AnalysisFile)=>({...file,fields:[...file.fields,...ids]}))),ids);
 await page.getByRole('button',{name:'Refresh fields',exact:true}).click();await expect(page.locator('.analysis-enrichment-message')).toHaveCount(0);await page.getByRole('button',{name:'Apply conditions',exact:true}).click();
 expect(await page.evaluate(()=>(window as any).__testAnalysisRequest())).toMatchObject({conditions:[{left:{field:'three_days_ago_high'}}]});
});

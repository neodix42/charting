import {describe,expect,it} from 'vitest';
import {intervals,type ScreenerInstrument} from '../types';
import {chartInstrumentIntervals,isCalculatedInstrument,isSyntheticFamily,SYNTHETIC_CATEGORY,supportedDownloadPeriods} from '../workspace/syntheticInstruments';
import {firstWorkspace,reduceWorkspace} from '../workspace/model';
import {analysisInputError,initialAnalysisView,type SavedAnalysisCondition} from '../workspace/screenerAnalysis';
import {defaultLiveAlertPeriod} from '../workspace/screenerLiveAlerts';

describe('Synthetic Instruments identity and period policy',()=>{
 it('recognizes calculated Dukascopy IDs before metadata without reclassifying native or other-source instruments',()=>{
  for(const instrument of ['BTCXAU','XAUSPX','ETHBTC'])expect(isCalculatedInstrument({exchange:'dukascopy',instrument})).toBe(true);
  for(const instrument of ['BTCUSD','XAUXAG','EURUSD'])expect(isCalculatedInstrument({exchange:'dukascopy',instrument})).toBe(false);
  expect(isCalculatedInstrument({exchange:'hyperliquid',instrument:'BTCXAU'})).toBe(false);
  expect(isCalculatedInstrument({exchange:'dukascopy',instrument:'XAUXAG'},{synthetic:'true'})).toBe(true);
 });
 it('distinguishes a native family category from a calculated trading restriction',()=>{
  const key={exchange:'dukascopy' as const,instrument:'BTCUSD'},metadata={syntheticCategory:'true'};
  expect(isSyntheticFamily(key,metadata)).toBe(true);
  expect(isCalculatedInstrument(key,metadata)).toBe(false);
  expect(chartInstrumentIntervals(key,metadata)).toEqual(['1d','1w']);
  expect(chartInstrumentIntervals({exchange:'dukascopy',instrument:'EURUSD'})).toEqual(intervals);
  expect(chartInstrumentIntervals({exchange:'binance',instrument:'BTCUSD'},metadata)).toEqual(intervals);
 });
 it('limits selected family downloads while preserving legacy native and ordinary period choices',()=>{
  const periods=['m1','h1','d1','w1'];
  const native:ScreenerInstrument={id:'btcusd',name:'Native legacy BTC',earliestDate:''};
  expect(supportedDownloadPeriods(periods,[native])).toEqual(periods);
  expect(supportedDownloadPeriods(periods,[{...native,category:SYNTHETIC_CATEGORY}])).toEqual(['d1','w1']);
  expect(supportedDownloadPeriods(periods,[native,{id:'btcxau',name:'Ratio',earliestDate:''}])).toEqual(['d1','w1']);
  expect(supportedDownloadPeriods(periods,[{...native,supportedTimeframes:['w1']}])).toEqual(['w1']);
  expect(supportedDownloadPeriods(periods,[])).toEqual(periods);
 });
 it('rejects unsupported tab actions without rewriting restored native intraday state',()=>{
  const workspace=firstWorkspace();Object.assign(workspace.tabs[0].charts[0],{exchange:'dukascopy',market:'cfd',instrument:'BTCUSD',timeframe:'1h'});
  const original=structuredClone(workspace);
  expect(()=>reduceWorkspace(workspace,{type:'tabTimeframe',tabId:workspace.activeTabId,timeframe:'1m'})).toThrow('D1 and W1');
  expect(workspace).toEqual(original);
  expect(reduceWorkspace(workspace,{type:'settings',displayTimezone:'Asia/Dubai'}).tabs[0].charts[0].timeframe).toBe('1h');
  expect(reduceWorkspace(workspace,{type:'tabTimeframe',tabId:workspace.activeTabId,timeframe:'1w'}).tabs[0].charts[0].timeframe).toBe('1w');
 });
});

describe('Synthetic Instruments Analysis and Live Alerts',()=>{
 const request=()=>({...initialAnalysisView(),period:'d1' as const,profitabilityFilter:0,categories:[SYNTHETIC_CATEGORY]});
 it('validates supported scan and operand periods only for the dedicated Dukascopy category',()=>{
  const daily=request();expect(analysisInputError(daily)).toBe('');
  expect(analysisInputError({...daily,period:'h1'})).toContain('D1 and W1');
  const hourlyOperand={...daily,conditions:daily.conditions.map(condition=>({...condition,left:{...condition.left,timeframe:'h1'}}))};
  expect(analysisInputError(hourlyOperand)).toContain('operand timeframes');
  expect(analysisInputError({...hourlyOperand,dataSource:'hyperliquid'})).toBe('');
  expect(analysisInputError({...hourlyOperand,categories:['Forex']})).toBe('');
  const inactiveHourly={...daily,conditions:daily.conditions.map(condition=>({...condition,distance:{...condition.distance!,timeframe:'h1'}}))};
  expect(analysisInputError(inactiveHourly)).toBe('');
 });
 it('defaults synthetic-only alerts to daily while retaining native and public-source defaults',()=>{
  const saved:SavedAnalysisCondition={id:'synthetic-condition',name:'Ratios',request:{...request(),period:'all'}};
  expect(defaultLiveAlertPeriod(saved)).toBe('d1');
  expect(defaultLiveAlertPeriod({...saved,request:{...saved.request,period:'w1'}})).toBe('w1');
  expect(defaultLiveAlertPeriod({...saved,request:{...saved.request,categories:['Forex']}})).toBe('h1');
  expect(defaultLiveAlertPeriod({...saved,request:{...saved.request,dataSource:'hyperliquid'}})).toBe('h1');
 });
});

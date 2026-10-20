import {describe,it,expect} from 'vitest';
import {firstWorkspace,grid,liveView,MAX_INDICATORS,reduceWorkspace} from '../workspace/model';
import {intervals,type IndicatorConfig,type Workspace} from '../types';
import {tickPrecision} from '../charts/format';
import intermediate from '../../../examples/intermediate-two-tabs.json';
import maximum from '../../../examples/capacity-20-tabs-180-charts.json';
describe('workspace production reducer',()=>{
 it('first launch and new tabs create exactly one actual chart',()=>{let w=firstWorkspace();expect(w.tabs).toHaveLength(1);expect(w.tabs[0].charts).toHaveLength(1);expect(w.tabs[0].grid).toEqual(grid());w=reduceWorkspace(w,{type:'grid',rows:3,columns:3});expect(w.tabs[0].charts).toHaveLength(1);for(let i=0;i<8;i++)w=reduceWorkspace(w,{type:'addChart'});expect(w.tabs[0].charts).toHaveLength(9);expect(()=>reduceWorkspace(w,{type:'addChart'})).toThrow('Maximum 9');w=reduceWorkspace(w,{type:'addTab'});expect(w.tabs[1].charts).toHaveLength(1);expect(w.tabs[1].grid).toEqual(grid());});
 it('larger layouts only change geometry; shrinking cannot drop charts',()=>{let w=firstWorkspace();const id=w.tabs[0].charts[0].id;w=reduceWorkspace(w,{type:'grid',rows:3,columns:3});expect(w.tabs[0].charts.map(c=>c.id)).toEqual([id]);w=reduceWorkspace(w,{type:'addChart'});expect(()=>reduceWorkspace(w,{type:'grid',rows:1,columns:1})).toThrow('Remove charts');expect(w.tabs[0].charts).toHaveLength(2);});
 it('enforces twenty tabs and keeps at least one tab/chart',()=>{let w=firstWorkspace();expect(()=>reduceWorkspace(w,{type:'removeTab',id:w.activeTabId})).toThrow('at least one tab');expect(()=>reduceWorkspace(w,{type:'removeChart',id:w.tabs[0].charts[0].id})).toThrow('at least one chart');for(let i=1;i<20;i++)w=reduceWorkspace(w,{type:'addTab'});expect(w.tabs).toHaveLength(20);expect(()=>reduceWorkspace(w,{type:'addTab'})).toThrow('Maximum 20');expect(()=>reduceWorkspace(w,{type:'duplicateTab',id:w.activeTabId})).toThrow('Maximum 20');});
 it('duplicate is deeply independent and all identifiers are unique',()=>{let w=firstWorkspace();const original=w.tabs[0];w=reduceWorkspace(w,{type:'addChart'});w=reduceWorkspace(w,{type:'duplicateTab',id:original.id});const duplicate=w.tabs[1];expect(duplicate.charts).toHaveLength(2);w=reduceWorkspace(w,{type:'chart',id:duplicate.charts[0].id,patch:{instrument:'ETHUSDT',view:{mode:'historical',fromSec:1,toSec:100,barSpacing:8,rightOffset:0}}});expect(w.tabs[0].charts[0].instrument).toBe('BTCUSDT');expect(w.tabs[0].charts[0].view.mode).toBe('live');const ids=[w.id,...w.tabs.flatMap(t=>[t.id,...t.charts.map(c=>c.id)])];expect(new Set(ids).size).toBe(ids.length);});
 it('retains actual restored counts and settings across reorder and select',()=>{let w=structuredClone(intermediate) as Workspace;const original=structuredClone(w);expect(w.tabs.map(t=>t.charts.length)).toEqual([3,1]);w=reduceWorkspace(w,{type:'reorderTab',from:w.tabs[0].id,to:w.tabs[1].id});w=reduceWorkspace(w,{type:'selectTab',id:w.tabs[1].id});expect(w.tabs[1]).toEqual(original.tabs[0]);expect(w.tabs.map(t=>t.charts.length)).toEqual([1,3]);expect((maximum as Workspace).tabs.flatMap(t=>t.charts)).toHaveLength(180);});
 it('copies only focused chart preferences for Add Chart and resets viewport',()=>{let w=firstWorkspace();const c=w.tabs[0].charts[0];w=reduceWorkspace(w,{type:'chart',id:c.id,patch:{instrument:'ETHUSDT',timeframe:'4h',showVolume:false,view:{mode:'historical',fromSec:10,toSec:20,rightOffset:0,barSpacing:7}}});w=reduceWorkspace(w,{type:'addChart',sourceId:c.id});const next=w.tabs[0].charts[1];expect(next.id).not.toBe(c.id);expect(next.instrument).toBe('ETHUSDT');expect(next.timeframe).toBe('4h');expect(next.showVolume).toBe(false);expect(next.view.mode).toBe('live');expect(w.tabs[0].charts[0].view.mode).toBe('historical');});
 it('duplicates indicators with independent parameters and globally unique identifiers',()=>{let w=firstWorkspace();const original=w.tabs[0].charts[0];const indicator={id:crypto.randomUUID(),type:'ema' as const,parameters:{period:34},color:'#61b6ef'};w=reduceWorkspace(w,{type:'chart',id:original.id,patch:{indicators:[indicator]}});w=reduceWorkspace(w,{type:'addChart',sourceId:original.id});const copy=w.tabs[0].charts[1];expect(copy.indicators?.[0]).toMatchObject({type:'ema',parameters:{period:34}});expect(copy.indicators?.[0].id).not.toBe(indicator.id);copy.indicators![0].parameters.period=8;expect(w.tabs[0].charts[0].indicators![0].parameters.period).toBe(34);w=reduceWorkspace(w,{type:'duplicateTab',id:w.activeTabId});const ids=[w.id,...w.tabs.flatMap(tab=>[tab.id,...tab.charts.flatMap(chart=>[chart.id,...(chart.indicators??[]).map(value=>value.id)])])];expect(new Set(ids).size).toBe(ids.length);expect(w.tabs[1].charts[0].indicators![0].parameters).not.toBe(w.tabs[0].charts[0].indicators![0].parameters);});
 it('preserves grid fractions until preset dimensions change and normalizes resize',()=>{let w=reduceWorkspace(firstWorkspace(),{type:'grid',rows:2,columns:2});w=reduceWorkspace(w,{type:'tracks',axis:'rowFractions',values:[3,2]});expect(w.tabs[0].grid.rowFractions).toEqual([.6,.4]);w=reduceWorkspace(w,{type:'grid',rows:2,columns:2});expect(w.tabs[0].grid.rowFractions).toEqual([.6,.4]);expect(()=>reduceWorkspace(w,{type:'tracks',axis:'rowFractions',values:[0,1]})).toThrow('Invalid');});
});

describe('active tab controls',()=>{
 function workspace(){let w=firstWorkspace();for(let i=1;i<9;i++)w=reduceWorkspace(w,{type:'addChart'});const active=w.activeTabId;w=reduceWorkspace(w,{type:'addTab'});return reduceWorkspace(w,{type:'selectTab',id:active});}
 const indicator:IndicatorConfig={id:'dialog-template',type:'ichimoku',parameters:{conversion:9,base:26,spanB:52,displacement:26},color:'#61b6ef'};
 it('normalizes mixed periods on all nine actual charts and resets views while preserving independent preferences and hidden tabs',()=>{
  const w=workspace();w.tabs[0].charts=w.tabs[0].charts.map((chart,i)=>({...chart,timeframe:intervals[i%intervals.length],instrument:i%2?'ETHUSDT':'BTCUSDT',showVolume:i%2===0,chartType:i%2?'line':'candlestick',priceScale:{mode:i%2?'log':'linear',autoScale:false,manualRange:{min:1+i,max:50+i}},view:{mode:'historical',fromSec:100+i,toSec:200+i,barSpacing:7+i,rightOffset:-10-i},indicators:[{id:crypto.randomUUID(),type:'ema',parameters:{period:20+i}}]}));
  const original=structuredClone(w),hidden=w.tabs[1],next=reduceWorkspace(w,{type:'tabTimeframe',tabId:w.activeTabId,timeframe:'1w'});
  expect(next.tabs[0].charts).toHaveLength(9);expect(next.tabs[0].grid).toEqual(w.tabs[0].grid);expect(next.tabs[1]).toBe(hidden);expect(next.activeTabId).toBe(w.activeTabId);
  next.tabs[0].charts.forEach((chart,i)=>{expect(chart.timeframe).toBe('1w');expect(chart.view).toEqual(liveView());expect({...chart,timeframe:original.tabs[0].charts[i].timeframe,view:original.tabs[0].charts[i].view}).toEqual(original.tabs[0].charts[i]);expect(chart.priceScale).toBe(w.tabs[0].charts[i].priceScale);});
  expect(w).toEqual(original);expect(new Set(next.tabs[0].charts.map(chart=>chart.view)).size).toBe(9);
 });
 it('adds independent indicators to every actual chart with globally unique IDs and leaves existing indicators and other tabs intact',()=>{
  let w=workspace();w.tabs[0].charts=w.tabs[0].charts.map((chart,i)=>({...chart,indicators:i%2?[{id:crypto.randomUUID(),type:'sma',parameters:{period:5+i}}]:undefined}));
  const original=structuredClone(w),hidden=w.tabs[1],template=structuredClone(indicator),next=reduceWorkspace(w,{type:'tabIndicator',tabId:w.activeTabId,indicator});
  expect(next.tabs[0].charts).toHaveLength(9);expect(next.tabs[1]).toBe(hidden);
  next.tabs[0].charts.forEach((chart,i)=>{const previous=w.tabs[0].charts[i],added=chart.indicators!.at(-1)!;expect(chart.indicators!.slice(0,-1)).toEqual(previous.indicators??[]);expect(added).toEqual({...indicator,id:added.id});expect(added.id).not.toBe(indicator.id);expect(added.parameters).not.toBe(indicator.parameters);expect({...chart,indicators:previous.indicators}).toEqual(previous);});
  const ids=[next.id,...next.tabs.flatMap(tab=>[tab.id,...tab.charts.flatMap(chart=>[chart.id,...(chart.indicators??[]).map(item=>item.id)])])];expect(new Set(ids).size).toBe(ids.length);
  next.tabs[0].charts[0].indicators!.at(-1)!.parameters.base=10;expect(next.tabs[0].charts[1].indicators!.at(-1)!.parameters.base).toBe(26);expect(indicator).toEqual(template);expect(w).toEqual(original);
 });
 it('rejects a full chart atomically before changing any of the other eight charts',()=>{
  const w=workspace();w.tabs[0].charts[8].indicators=Array.from({length:MAX_INDICATORS},()=>({...structuredClone(indicator),id:crypto.randomUUID()}));const before=structuredClone(w);
  expect(()=>reduceWorkspace(w,{type:'tabIndicator',tabId:w.activeTabId,indicator})).toThrow('Maximum 12 indicators');expect(w).toEqual(before);expect(w.tabs[0].charts.slice(0,8).every(chart=>!chart.indicators?.length)).toBe(true);
  w.tabs[0].charts[8].indicators!.pop();const next=reduceWorkspace(w,{type:'tabIndicator',tabId:w.activeTabId,indicator});expect(next.tabs[0].charts[8].indicators).toHaveLength(MAX_INDICATORS);expect(next.tabs[0].charts.slice(0,8).every(chart=>chart.indicators?.length===1)).toBe(true);
 });
 it('rejects stale, hidden and missing tab targets without editing the workspace',()=>{
  const w=workspace(),before=structuredClone(w),stale=w.activeTabId,switched=reduceWorkspace(w,{type:'selectTab',id:w.tabs[1].id});
  for(const tabId of [w.tabs[1].id,'missing-tab']){expect(()=>reduceWorkspace(w,{type:'tabTimeframe',tabId,timeframe:'1w'})).toThrow('no longer active');expect(()=>reduceWorkspace(w,{type:'tabIndicator',tabId,indicator})).toThrow('no longer active');}
  expect(()=>reduceWorkspace(switched,{type:'tabTimeframe',tabId:stale,timeframe:'1d'})).toThrow('no longer active');expect(()=>reduceWorkspace(switched,{type:'tabIndicator',tabId:stale,indicator})).toThrow('no longer active');expect(w).toEqual(before);
 });
 it('never creates charts for unused layout cells when applying tab controls',()=>{
  let w=reduceWorkspace(firstWorkspace(),{type:'grid',rows:3,columns:3});w=reduceWorkspace(w,{type:'tabTimeframe',tabId:w.activeTabId,timeframe:'1w'});w=reduceWorkspace(w,{type:'tabIndicator',tabId:w.activeTabId,indicator});expect(w.tabs[0].charts).toHaveLength(1);expect(w.tabs[0].charts[0].indicators).toHaveLength(1);expect(w.tabs[0].grid).toEqual(grid(3,3));
 });
});

describe('price tick metadata',()=>{it('preserves decimal increment precision without forcing two places',()=>{expect(tickPrecision('0.25')).toBe(2);expect(tickPrecision('0.00000001')).toBe(8);expect(tickPrecision('1e-8')).toBe(8);expect(tickPrecision('0.01000')).toBe(2);expect(tickPrecision('1')).toBe(0);});});


describe('moving average line width persistence',()=>{
 it('copies a selected width independently through tab controls, chart and tab duplication without populating absent legacy widths',()=>{
  let w=firstWorkspace();const first=w.tabs[0].charts[0];
  const legacy:IndicatorConfig={id:crypto.randomUUID(),type:'sma',parameters:{period:20}};
  w=reduceWorkspace(w,{type:'chart',id:first.id,patch:{indicators:[legacy]}});w=reduceWorkspace(w,{type:'addChart',sourceId:first.id});
  const template:IndicatorConfig={id:crypto.randomUUID(),type:'ema',parameters:{period:8},lineWidth:4,color:'#61b6ef'};
  w=reduceWorkspace(w,{type:'tabIndicator',tabId:w.activeTabId,indicator:template});
  expect(w.tabs[0].charts.map(chart=>chart.indicators?.[1].lineWidth)).toEqual([4,4]);expect(w.tabs[0].charts.every(chart=>!Object.hasOwn(chart.indicators![0],'lineWidth'))).toBe(true);
  const original=structuredClone(w.tabs[0]);w=reduceWorkspace(w,{type:'duplicateTab',id:w.activeTabId});const duplicate=w.tabs[1];expect(duplicate.charts.map(chart=>chart.indicators?.[1].lineWidth)).toEqual([4,4]);
  const indicators=structuredClone(duplicate.charts[0].indicators!);indicators[1].lineWidth=1;w=reduceWorkspace(w,{type:'chart',id:duplicate.charts[0].id,patch:{indicators}});expect(w.tabs[0]).toEqual(original);expect(w.tabs[1].charts[1].indicators![1].lineWidth).toBe(4);expect(template.lineWidth).toBe(4);
  const restored=JSON.parse(JSON.stringify(w)) as Workspace;expect(restored).toEqual(w);expect(restored.tabs[0].charts[0].indicators![0]).not.toHaveProperty('lineWidth');
 });
});

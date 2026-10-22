import {describe,expect,it} from 'vitest';
import {firstWorkspace,reduceWorkspace} from '../workspace/model';
import type {DrawingConfig} from '../types';

describe('drawing ownership in the production workspace reducer',()=>{
 function workspace(){const w=firstWorkspace(),chart=w.tabs[0].charts[0];const drawing:DrawingConfig={id:crypto.randomUUID(),type:'trendline',exchange:chart.exchange,market:chart.market,instrument:chart.instrument,points:[{time:1700000000,price:100},{time:1700003600,price:120}]};chart.drawings=[drawing];return w;}
 it('starts new charts and tabs without copying another chart’s drawings',()=>{
  let w=workspace();const original=structuredClone(w.tabs[0].charts[0]);
  w=reduceWorkspace(w,{type:'addChart',sourceId:original.id});
  expect(w.tabs[0].charts[0]).toEqual(original);
  expect(w.tabs[0].charts[1].drawings).toEqual([]);
  w=reduceWorkspace(w,{type:'addTab'});
  expect(w.tabs[1].charts[0].drawings??[]).toEqual([]);
 });
 it('duplicates drawings with new globally unique identifiers and independent anchors',()=>{
  const original=workspace();const w=reduceWorkspace(original,{type:'duplicateTab',id:original.activeTabId});
  const source=w.tabs[0].charts[0],copy=w.tabs[1].charts[0];
  expect(copy.drawings![0]).toEqual({...source.drawings![0],id:copy.drawings![0].id});
  const ids=[w.id,...w.tabs.flatMap(t=>[t.id,...t.charts.flatMap(c=>[c.id,...(c.drawings??[]).map(d=>d.id)])])];
  expect(new Set(ids).size).toBe(ids.length);
  copy.drawings![0].points[0].price=200;
  expect(source.drawings![0].points[0].price).toBe(100);
  expect(original.tabs[0].charts[0].drawings![0].points[0].price).toBe(100);
 });
 it('retains instrument-scoped anchors through feed changes, reorder, tab switch and save snapshots',()=>{
  let w=workspace();const chart=w.tabs[0].charts[0],drawings=structuredClone(chart.drawings);
  w=reduceWorkspace(w,{type:'chart',id:chart.id,patch:{instrument:'ETHUSDT',market:'perpetual',timeframe:'4h'}});
  w=reduceWorkspace(w,{type:'addChart'});
  w=reduceWorkspace(w,{type:'reorderChart',from:chart.id,to:w.tabs[0].charts[1].id});
  w=reduceWorkspace(w,{type:'addTab'});
  w=reduceWorkspace(w,{type:'selectTab',id:w.tabs[0].id});
  const restored=JSON.parse(JSON.stringify(w));
  expect(restored.tabs[0].charts[1].drawings).toEqual(drawings);
  expect(restored.tabs[0].charts[1].instrument).toBe('ETHUSDT');
 });
});

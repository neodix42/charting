import {describe,expect,it} from 'vitest';
import type {ChartRightGap,Workspace} from '../types';
import {DEFAULT_CHART_RIGHT_GAP,firstWorkspace,reduceWorkspace} from '../workspace/model';
import maximum from '../../../examples/capacity-20-tabs-180-charts.json';

describe('workspace chart right gap',()=>{
 it('starts with ten bars and exactly one chart, without sharing mutable default settings',()=>{
  const first=firstWorkspace(),second=firstWorkspace();
  expect(first.chartRightGap).toEqual({unit:'bars',value:10});expect(first.tabs).toHaveLength(1);expect(first.tabs[0].charts).toHaveLength(1);expect(first.tabs[0].charts[0].view.rightOffset).toBe(10);
  first.chartRightGap!.value=20;expect(second.chartRightGap).toEqual(DEFAULT_CHART_RIGHT_GAP);expect(DEFAULT_CHART_RIGHT_GAP.value).toBe(10);
 });
 it('applies one preference to all saved charts and returns them to live while retaining independent zoom and prices',()=>{
  const original=structuredClone(maximum) as Workspace;
  original.tabs.forEach(tab=>tab.charts.forEach((chart,i)=>{chart.view={mode:'historical',fromSec:100,toSec:200,barSpacing:6+i,rightOffset:-i};}));
  const before=structuredClone(original),gap:ChartRightGap={unit:'pixels',value:120};
  const next=reduceWorkspace(original,{type:'settings',chartRightGap:gap});
  expect(next.tabs).toHaveLength(20);expect(next.tabs.flatMap(tab=>tab.charts)).toHaveLength(180);expect(next.chartRightGap).toEqual(gap);expect(next.chartRightGap).not.toBe(gap);
  next.tabs.forEach((tab,t)=>{expect(tab.grid).toBe(original.tabs[t].grid);tab.charts.forEach((chart,c)=>{const old=original.tabs[t].charts[c];expect(chart.view).toEqual({...old.view,mode:'live',fromSec:null,toSec:null});expect({...chart,view:old.view}).toEqual(old);expect(chart.priceScale).toBe(old.priceScale);});});
  expect(original).toEqual(before);expect(next.activeTabId).toBe(original.activeTabId);
 });
 it('keeps historical views when unrelated settings change or the effective gap stays the same',()=>{
  const w=firstWorkspace();delete w.chartRightGap;w.tabs[0].charts[0].view={mode:'historical',fromSec:100,toSec:200,barSpacing:11,rightOffset:-15};
  const appearance=reduceWorkspace(w,{type:'settings',theme:'light'});expect(appearance.chartRightGap).toBeUndefined();expect(appearance.tabs).toBe(w.tabs);
  const same=reduceWorkspace(w,{type:'settings',chartRightGap:{unit:'bars',value:10}});expect(same.tabs).toBe(w.tabs);expect(same.chartRightGap).toEqual({unit:'bars',value:10});
  const changed=reduceWorkspace(w,{type:'settings',chartRightGap:{unit:'bars',value:11}});expect(changed.tabs[0].charts[0].view.mode).toBe('live');
 });
 it.each([{unit:'bars',value:0},{unit:'bars',value:500},{unit:'pixels',value:0},{unit:'pixels',value:5000}] as ChartRightGap[])('accepts boundary preference %j',gap=>{
  expect(reduceWorkspace(firstWorkspace(),{type:'settings',chartRightGap:gap}).chartRightGap).toEqual(gap);
 });
 it.each([{unit:'bars',value:-1},{unit:'bars',value:501},{unit:'pixels',value:5001},{unit:'bars',value:1.5},{unit:'pixels',value:NaN},{unit:'bars',value:Infinity},{unit:'inches',value:10}])('rejects invalid preference atomically %j',gap=>{
  const w=firstWorkspace(),before=structuredClone(w);expect(()=>reduceWorkspace(w,{type:'settings',chartRightGap:gap as ChartRightGap})).toThrow('Chart right gap');expect(w).toEqual(before);
 });
});

import {describe,expect,it,vi} from 'vitest';
import {comparisonColor,comparisonDate,comparisonLineData,comparisonPercent,ComparisonViewWriter,copyComparisonView,defaultComparisonView,type ComparisonView} from '../workspace/comparison';

describe('Comparison view and chart values',()=>{
 it('defaults to four weeks and exactly the requested seven assets with independent copies',()=>{
  const value=defaultComparisonView(),copy=copyComparisonView(value);copy.assets.pop();
  expect(value).toEqual({weeks:4,assets:['BTC','ETH','TON','HYPE','ZEN','APT','NEAR']});
  expect(defaultComparisonView().assets).toHaveLength(7);
  expect(new Set(value.assets.map(comparisonColor)).size).toBe(7);
  expect(comparisonColor('BTC')).toBe(comparisonColor('BTC'));
 });
 it('formats UTC baseline dates and genuine missing/zero/signed percentage values',()=>{
  expect(comparisonDate(Date.UTC(2026,10,24,23,59)/1000)).toBe('24 Nov 2026');
  expect(comparisonPercent(null)).toBe('—');expect(comparisonPercent(undefined)).toBe('—');
  expect(comparisonPercent(0)).toBe('0.00%');expect(comparisonPercent(-.001)).toBe('0.00%');
  expect(comparisonPercent(12.345)).toBe('+12.35%');expect(comparisonPercent(-7.5)).toBe('-7.50%');
 });
 it('breaks outgoing lines across missing closes without inventing any intermediate values',()=>{
  const points=[{time:0,close:100,pct:0},{time:86400,close:null,pct:null},{time:172800,close:110,pct:10},{time:259200,close:90,pct:-10}];
  expect(comparisonLineData(points,'#123456')).toEqual([{time:0,value:0,color:'rgba(0, 0, 0, 0)'},{time:86400},{time:172800,value:10,color:'#123456'},{time:259200,value:-10,color:'#123456'}]);
  expect(points[1]).toEqual({time:86400,close:null,pct:null});
  expect(comparisonLineData([points[0],points[2]],'#123456')[0]).toMatchObject({color:'rgba(0, 0, 0, 0)'});
 });
});

describe('Comparison ordered view persistence',()=>{
 it('coalesces edits while a save is pending and preserves an explicitly empty selection',async()=>{
  let release!:()=>void;const first=new Promise<void>(resolve=>{release=resolve;}),saved:ComparisonView[]=[];
  const persist=vi.fn(async(view:ComparisonView)=>{saved.push(copyComparisonView(view));if(saved.length===1)await first;return view;});
  const writer=new ComparisonViewWriter(persist,()=>{});
  writer.edit(defaultComparisonView());writer.edit({weeks:8,assets:['BTC']});writer.edit({weeks:12,assets:[]});
  release();await writer.flush();
  expect(saved).toEqual([defaultComparisonView(),{weeks:12,assets:[]}]);
 });
 it('retains the newest edit after a failed save and makes a later flush await its retry',async()=>{
  let reject!:(error:Error)=>void;const first=new Promise<ComparisonView>((_resolve,fail)=>{reject=fail;}),error=vi.fn();
  const persist=vi.fn<(view:ComparisonView)=>Promise<ComparisonView>>().mockImplementationOnce(()=>first).mockImplementation(async view=>view);
  const writer=new ComparisonViewWriter(persist,error);
  writer.edit(defaultComparisonView());writer.edit({weeks:2,assets:['TON','HYPE']});
  reject(new Error('Disk is full'));await Promise.resolve();await Promise.resolve();
  await writer.flush();
  expect(persist).toHaveBeenLastCalledWith({weeks:2,assets:['TON','HYPE']});
 });
});

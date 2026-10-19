import {describe,it,expect} from 'vitest';
import type {Candle} from '../../types';
import {ema,ichimoku,indicatorWarmupBars,macd,sma} from './math';
const bars=(values:number[]):Candle[]=>values.map((close,i)=>({time:1000+i*60,open:String(close),high:String(close+3),low:String(close-1),close:String(close),volume:'10',closed:i<values.length-1,quality:'authoritative',sourceRevision:1}));

describe('indicator history requirements',()=>{
 it('covers complete SMA/EMA windows, all cloud spans and the MACD signal seed',()=>{
  expect(indicatorWarmupBars({id:'sma',type:'sma',parameters:{}})).toBe(20);
  expect(indicatorWarmupBars({id:'ema',type:'ema',parameters:{period:2000}})).toBe(2000);
  expect(indicatorWarmupBars({id:'cloud',type:'ichimoku',parameters:{}})).toBe(52);
  expect(indicatorWarmupBars({id:'cloud',type:'ichimoku',parameters:{conversion:75,base:20,spanB:30,displacement:500}})).toBe(75);
  expect(indicatorWarmupBars({id:'macd',type:'macd',parameters:{}})).toBe(34);
  expect(indicatorWarmupBars({id:'macd',type:'macd',parameters:{fast:1,slow:2000,signal:2000}})).toBe(3999);
 });
});

describe('moving averages',()=>{
 it('waits for a complete close-price window and uses an SMA-seeded EMA',()=>{
  const input=bars([1,2,3,8,5,9]);
  expect(sma(input,3)).toEqual([{time:1120,value:2},{time:1180,value:13/3},{time:1240,value:16/3},{time:1300,value:22/3}]);
  expect(ema(input,3)).toEqual([{time:1120,value:2},{time:1180,value:5},{time:1240,value:5},{time:1300,value:7}]);
  expect(sma(input,20)).toEqual([]);expect(ema(input,20)).toEqual([]);
  expect(ema(input,1).map(point=>point.value)).toEqual([1,2,3,8,5,9]);
 });
 it('recomputes a corrected partial candle without double counting or changing earlier values',()=>{
  const input=bars([2,4,6,8,10]),before=sma(input,3),beforeEMA=ema(input,3);
  input[4]={...input[4],close:'25',closed:false,sourceRevision:2};
  expect(sma(input,3).slice(0,-1)).toEqual(before.slice(0,-1));expect(sma(input,3).at(-1)!.value).toBe(13);
  expect(ema(input,3).slice(0,-1)).toEqual(beforeEMA.slice(0,-1));expect(ema(input,3).at(-1)!.value).toBe(15.5);
  expect(ema([...input,...bars([99]).map(bar=>({...bar,time:1300}))],3).slice(0,-1)).toEqual(ema(input,3));
 });
 it('rejects invalid periods and leaves the source data untouched',()=>{
  const input=bars([1,2,3]),copy=structuredClone(input);
  for(const period of [0,-1,1.5,2001,NaN]){expect(sma(input,period)).toEqual([]);expect(ema(input,period)).toEqual([]);}
  expect(input).toEqual(copy);
 });
});

describe('Ichimoku',()=>{
 it('uses rolling high-low midpoints, independent warmups and signed bar displacement',()=>{
  const input=bars([1,2,3,4,5,6,7,8,9,10]),cloud=ichimoku(input,2,3,4,2,60);
  expect(cloud.conversion[0]).toEqual({time:1060,value:2.5});expect(cloud.base[0]).toEqual({time:1120,value:3});
  expect(cloud.leadingA[0]).toEqual({time:1240,value:3.25});expect(cloud.leadingB[0]).toEqual({time:1300,value:3.5});
  expect(cloud.cloud[0]).toEqual({time:1300,a:4.25,b:3.5});
  expect(cloud.lagging[0]).toEqual({time:1000,value:3});expect(cloud.lagging.at(-1)).toEqual({time:1420,value:10});
  expect(cloud.leadingA.at(-1)!.time).toBe(input.at(-1)!.time+120);
 });
 it('shifts through actual loaded bars across gaps and extrapolates weekly future positions in UTC',()=>{
  const input=bars([1,2,3,4]);input[2].time+=60;input[3].time+=60;
  const values=ichimoku(input,1,1,1,1,604800);
  expect(values.leadingA[0].time).toBe(input[1].time);expect(values.leadingA[1].time).toBe(input[2].time);
  expect(values.leadingA.at(-1)!.time).toBe(input.at(-1)!.time+604800);
  const unshifted=ichimoku(input,1,1,1,0,60);expect(unshifted.lagging).toEqual(input.map(bar=>({time:bar.time,value:Number(bar.close)})));
 });
 it('matches a direct rolling-window oracle, including repeated extrema and large windows',()=>{
  const input=bars(Array.from({length:250},(_,i)=>20+(i%17)*3-(i%5)));
  for(const period of [1,2,9,26,52,200]){
   const actual=ichimoku(input,period,period,period,0,60).base;
   const expected=input.slice(period-1).map((bar,index)=>{const window=input.slice(index,index+period);return {time:bar.time,value:(Math.max(...window.map(value=>Number(value.high)))+Math.min(...window.map(value=>Number(value.low))))/2};});
   expect(actual).toEqual(expected);
  }
 });
 it('never uses later high-low values in an earlier conversion or leading span',()=>{
  const input=bars([3,2,9,4,5,8]),prefix=ichimoku(input.slice(0,4),2,3,4,2,60),full=ichimoku(input,2,3,4,2,60);
  expect(full.conversion.slice(0,prefix.conversion.length)).toEqual(prefix.conversion);
  expect(full.leadingA.slice(0,prefix.leadingA.length)).toEqual(prefix.leadingA);
  input[5]={...input[5],high:'100'};expect(ichimoku(input,2,3,4,2,60).cloud.at(-1)!.b).toBe(51.5);
 });
});

describe('MACD',()=>{
 it('seeds both price EMAs and then seeds the signal with complete MACD observations',()=>{
  const values=macd(bars([1,2,3,6,4,7]),2,3,2);
  expect(values.macd.map(point=>point.time)).toEqual([1120,1180,1240,1300]);expect(values.signal.map(point=>point.time)).toEqual([1180,1240,1300]);
  [1/2,5/6,5/18,16/27].forEach((expected,i)=>expect(values.macd[i].value).toBeCloseTo(expected,12));
  [2/3,11/27,43/81].forEach((expected,i)=>expect(values.signal[i].value).toBeCloseTo(expected,12));
  [1/6,-7/54,5/81].forEach((expected,i)=>expect(values.histogram[i].value).toBeCloseTo(expected,12));
 });
 it('centers a constant market at zero and recalculates corrected candles',()=>{
  const input=bars(Array(40).fill(5)),values=macd(input);
  expect(values.macd).toHaveLength(15);expect(values.signal).toHaveLength(7);expect(values.histogram.every(point=>point.value===0)).toBe(true);
  input[39]={...input[39],close:'10'};const correction=macd(input);
  expect(correction.histogram.slice(0,-1)).toEqual(values.histogram.slice(0,-1));expect(correction.histogram.at(-1)!.value).toBeGreaterThan(0);
  expect(macd(input,26,12,9)).toEqual({macd:[],signal:[],histogram:[]});
 });
});

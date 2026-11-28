import {describe,expect,it} from 'vitest';
import {comparisonThirdRiseTimes,type ComparisonCloseObservation} from './comparisonSignals';

const day=86400,start=Date.UTC(2026,9,29)/1000;
const observations=(closes:Array<number|null>,from=start):ComparisonCloseObservation[]=>closes.map((close,index)=>({time:from+index*day,close}));

describe('comparisonThirdRiseTimes',()=>{
 it('marks the third rise from four closes, once per streak and without guessing earlier history',()=>{
  const points=observations([10,11,12,13,14,15,16]);
  expect(comparisonThirdRiseTimes([])).toEqual([]);
  expect(comparisonThirdRiseTimes(points.slice(0,3))).toEqual([]);
  expect(comparisonThirdRiseTimes(points.slice(0,4))).toEqual([start+3*day]);
  expect(comparisonThirdRiseTimes(points)).toEqual([start+3*day]);
 });

 it('uses rising native closes even while every rebased percentage is negative',()=>{
  const points=observations([60,62,65,69,73]).map(point=>({...point,pct:(point.close!/100-1)*100}));
  expect(points.every(point=>point.pct<0)).toBe(true);
  expect(comparisonThirdRiseTimes(points)).toEqual([start+3*day]);
 });

 it('restarts after a down or flat close and marks only each new third rise',()=>{
  expect(comparisonThirdRiseTimes(observations([10,11,12,13,12,13,14,15,15,16,17,18,19])))
   .toEqual([start+3*day,start+7*day,start+11*day]);
 });

 it('cannot bridge missing daily observations, including weekends or holidays',()=>{
  const points=[...observations([10,11,12]),...observations([13,14,15,16,17],start+5*day)];
  expect(comparisonThirdRiseTimes(points)).toEqual([start+8*day]);
  expect(comparisonThirdRiseTimes(points.slice(0,6))).toEqual([]);
 });

 it.each([null,0,-1,NaN,Infinity,-Infinity])('resets on invalid or missing close %s',close=>{
  const points=observations([10,11,12,close,13,14,15,16,17]);
  expect(comparisonThirdRiseTimes(points)).toEqual([start+7*day]);
 });

 it('rejects duplicate-day observations as seeds and preserves prior confirmed signals',()=>{
  const points=[...observations([10,11,12,13]),{time:start+3*day,close:14},...observations([15,16,17,18,19],start+4*day)];
  expect(comparisonThirdRiseTimes(points)).toEqual([start+3*day,start+7*day]);
  const beforeThird=[...observations([10,11,12]),{time:start+2*day,close:14},...observations([15,16,17,18],start+3*day)];
  expect(comparisonThirdRiseTimes(beforeThird)).toEqual([start+6*day]);
 });

 it('does not sort out-of-order data or reuse a previously observed day after the reset',()=>{
  const points=[...observations([10,11,12]),{time:start+day,close:13},{time:start+2*day,close:14},...observations([15,16,17,18],start+3*day)];
  expect(comparisonThirdRiseTimes(points)).toEqual([start+6*day]);
  expect(comparisonThirdRiseTimes([...observations([10,11,12,13])].reverse())).toEqual([]);
 });

 it.each([NaN,Infinity,-Infinity,-day,start+1,start+.5,Number.MAX_SAFE_INTEGER+1])('requires valid UTC day timestamps; invalid time %s resets the streak',time=>{
  const points=[...observations([10,11,12]),{time,close:13},...observations([14,15,16,17],start+4*day)];
  expect(comparisonThirdRiseTimes(points)).toEqual([start+7*day]);
 });

 it('uses exact UTC days across daylight-saving boundaries and supports the epoch',()=>{
  for(const from of [0,Date.UTC(2026,2,27)/1000,Date.UTC(2026,9,30)/1000]){
   expect(comparisonThirdRiseTimes(observations([1,2,3,4],from))).toEqual([from+3*day]);
  }
 });

 it('is prefix stable and cannot change an earlier signal using later observations',()=>{
  const points=[...observations([10,11,12,13,14,14,15,16,17]),{time:start+8*day,close:99},...observations([18,19,null,20,21,22,23],start+9*day)];
  const full=comparisonThirdRiseTimes(points);
  expect(full).toEqual([start+3*day,start+8*day,start+15*day]);
  for(let length=0;length<=points.length;length++){
   const prefix=points.slice(0,length),cutoff=prefix.length?Math.max(...prefix.map(point=>point.time)):-1;
   expect(comparisonThirdRiseTimes(prefix)).toEqual(full.filter(time=>time<=cutoff));
  }
 });

 it('keeps input order and values unchanged and supports bounded one-pass processing of long series',()=>{
  const points=Object.freeze(observations(Array.from({length:10000},(_,index)=>index+1)).map(point=>Object.freeze(point)));
  expect(comparisonThirdRiseTimes(points)).toEqual([start+3*day]);
  expect(points[0]).toEqual({time:start,close:1});
  expect(points.at(-1)).toEqual({time:start+9999*day,close:10000});
 });
});

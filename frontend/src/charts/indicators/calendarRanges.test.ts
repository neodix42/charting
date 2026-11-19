import {describe,it,expect} from 'vitest';
import type {Candle,IndicatorConfig} from '../../types';
import {calendarRangeNames,type CalendarRangeType} from '../../../../screener-runtime/calendar-ranges.mjs';
import {calendarRangeValues,indicatorHistoryLimit,indicatorWarmupBars} from './math';
import {indicatorLabel} from '../IndicatorDialog';

const bar=(date:string,low:number,high:number):Candle=>({time:Date.parse(date)/1000,open:(low+high)/2,high,low,close:(low+high)/2,volume:1,closed:true,quality:'authoritative',sourceRevision:1});
const ids=Object.keys(calendarRangeNames) as CalendarRangeType[];
const config=(type:CalendarRangeType):IndicatorConfig=>({id:type,type,parameters:{}});
describe('calendar range chart calculations',()=>{
 it('uses candle-open UTC dates and wick extrema, including the current provisional candle',()=>{
  const bars=[bar('2024-01-01T00:00Z',8,12),bar('2024-01-01T23:00Z',6,15),bar('2024-01-02T00:00Z',9,11),bar('2024-01-02T12:00Z',5,20),bar('2024-01-08T00:00Z',10,18)];
  bars[4].closed=false;
  const copy=structuredClone(bars);
  expect(calendarRangeValues(bars,'today_low','1h').map(point=>point.value)).toEqual([8,6,9,5,10]);
  expect(calendarRangeValues(bars,'previous_day_low','1h').map(point=>point.value)).toEqual([6,6,5]);
  expect(calendarRangeValues(bars,'week_high','1h').map(point=>point.value)).toEqual([12,15,15,20,18]);
  expect(calendarRangeValues(bars,'previous_week_high','1h')).toEqual([{time:bars[4].time,value:20}]);
  const revised=[...bars.slice(0,-1),{...bars[4],low:1,high:30}];
  expect(calendarRangeValues(revised,'today_low','1h').at(-1)!.value).toBe(1);
  expect(calendarRangeValues(revised,'previous_week_high','1h').at(-1)!.value).toBe(20);
  expect(bars).toEqual(copy);
 });
 it('uses the first candle open and latest observed close for day/week ranges and completed available periods',()=>{
  const times=['2024-01-01T00:00Z','2024-01-01T23:00Z','2024-01-02T00:00Z','2024-01-03T00:00Z','2024-01-05T16:00Z','2024-01-08T00:00Z'];
  const opens=[10,12,20,30,40,50],closes=[11,14,21,31,41,51];
  const bars=times.map((time,index)=>({...bar(time,opens[index]-2,closes[index]+3),open:opens[index],close:closes[index]}));
  bars[5].closed=false;
  const values=(type:CalendarRangeType)=>calendarRangeValues(bars,type,'1h').map(point=>point.value);
  expect(values('today_open')).toEqual([10,10,20,30,40,50]);expect(values('today_close')).toEqual(closes);
  expect(values('previous_day_open')).toEqual([10,20,30,40]);expect(values('previous_day_close')).toEqual([14,21,31,41]);
  expect(values('two_days_ago_open')).toEqual([10,20,30]);expect(values('two_days_ago_close')).toEqual([14,21,31]);
  expect(values('three_days_ago_open')).toEqual([10,20]);expect(values('three_days_ago_close')).toEqual([14,21]);
  expect(values('week_open')).toEqual([10,10,10,10,10,50]);expect(values('week_close')).toEqual(closes);
  expect(values('previous_week_open')).toEqual([10]);expect(values('previous_week_close')).toEqual([41]);
  const before=structuredClone(bars),revised=[...bars.slice(0,-1),{...bars[5],close:52,high:55}];
  expect(calendarRangeValues(revised,'today_open','1h').at(-1)!.value).toBe(50);
  expect(calendarRangeValues(revised,'today_close','1h').at(-1)!.value).toBe(52);
  expect(calendarRangeValues(revised,'week_close','1h').at(-1)!.value).toBe(52);
  expect(calendarRangeValues(revised,'previous_week_close','1h').at(-1)!.value).toBe(41);expect(bars).toEqual(before);
 });
 it('keeps incomplete first periods blank and never uses later rows in earlier ranges',()=>{
  const bars=[bar('2024-01-01T12:00Z',6,10),bar('2024-01-02T00:00Z',5,12),bar('2024-01-03T00:00Z',4,13),bar('2024-01-08T00:00Z',3,14),bar('2024-01-15T00:00Z',2,15)];
  expect(calendarRangeValues(bars,'today_low','1h')[0]).toEqual({time:bars[1].time,value:5});
  expect(calendarRangeValues(bars,'previous_day_low','1h')[0]).toEqual({time:bars[2].time,value:5});
  expect(calendarRangeValues(bars,'previous_week_low','1h')).toEqual([{time:bars[4].time,value:3}]);
  for(const id of ids){
   const prefix=calendarRangeValues(bars.slice(0,3),id,'1h'),full=calendarRangeValues(bars,id,'1h');
   expect(full.filter(point=>point.time<=bars[2].time)).toEqual(prefix);
  }
 });
 it('retains parameterless labels and disables calculations on daily/weekly periods',()=>{
  for(const id of ids){expect(indicatorLabel(config(id))).toBe(calendarRangeNames[id]);for(const timeframe of ['1d','1w'] as const){expect(calendarRangeValues([bar('2024-01-01T00:00Z',8,12)],id,timeframe)).toEqual([]);expect(indicatorWarmupBars(config(id),timeframe)).toBe(0);}}
 });
 it('selects one completed day for two/three days ago rather than combining a rolling window',()=>{
  const bars=[bar('2024-01-01T00:00Z',8,12),bar('2024-01-01T23:00Z',6,15),bar('2024-01-02T00:00Z',2,30),bar('2024-01-03T00:00Z',10,11),bar('2024-01-03T12:00Z',1,40),bar('2024-01-04T00:00Z',7,9)];
  expect(calendarRangeValues(bars,'two_days_ago_low','1h')).toEqual([{time:bars[3].time,value:6},{time:bars[4].time,value:6},{time:bars[5].time,value:2}]);
  expect(calendarRangeValues(bars,'two_days_ago_high','1h')).toEqual([{time:bars[3].time,value:15},{time:bars[4].time,value:15},{time:bars[5].time,value:30}]);
  expect(calendarRangeValues(bars,'three_days_ago_low','1h')).toEqual([{time:bars[5].time,value:6}]);
  expect(calendarRangeValues(bars,'three_days_ago_high','1h')).toEqual([{time:bars[5].time,value:15}]);
 });
 it('bounds ordinary chart history at 10,000 and loads enough minute bars for complete day/week boundaries',()=>{
  expect(indicatorHistoryLimit([],'1m')).toBe(10000);
  expect(indicatorWarmupBars(config('today_low'),'1m')).toBe(1441);
  expect(indicatorWarmupBars(config('today_open'),'1m')).toBe(1441);
  expect(indicatorHistoryLimit([config('previous_week_close')],'1m')).toBe(20161);
  expect(indicatorWarmupBars(config('previous_day_low'),'1m')).toBe(2881);
  expect(indicatorWarmupBars(config('two_days_ago_low'),'1m')).toBe(4321);
  expect(indicatorWarmupBars(config('three_days_ago_high'),'1m')).toBe(5761);
  expect(indicatorWarmupBars(config('week_high'),'1m')).toBe(10081);
  expect(indicatorHistoryLimit([config('previous_week_low')],'1m')).toBe(20161);
  expect(indicatorHistoryLimit([config('previous_week_low')],'1h')).toBe(10000);
  expect(indicatorHistoryLimit([config('previous_week_low')],'1d')).toBe(10000);
 });
});

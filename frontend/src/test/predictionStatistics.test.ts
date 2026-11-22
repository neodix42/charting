import {describe,expect,it} from 'vitest';
import {initialStatisticsRequest,statisticsKey,statisticsResultKey,type PredictionStatisticsForecast} from '../workspace/predictionStatistics';

describe('Statistics pattern selection identity',()=>{
 it('starts with a selectable five-bar query without sharing mutable directions',()=>{
  const first=initialStatisticsRequest(),second=initialStatisticsRequest();
  expect(first.pattern).toEqual(['up','up','up','up','up']);
  first.pattern![0]='down';expect(second.pattern![0]).toBe('up');
 });
 it('rejects a result for another direction ordering or bar count',()=>{
  const request={...initialStatisticsRequest(),asset:'eurusd',pattern:['up','down','up'] as const};
  const selection={...request,pattern:[...request.pattern]};
  const result={dataSource:selection.dataSource,symbol:selection.asset,period:selection.period,lookbackWeeks:selection.lookbackWeeks,pattern:[...selection.pattern]} as PredictionStatisticsForecast;
  expect(statisticsResultKey(result)).toBe(statisticsKey(selection));
  result.pattern=['down','up','up'];expect(statisticsResultKey(result)).not.toBe(statisticsKey(selection));
  result.pattern=['up','down','up','up'];expect(statisticsResultKey(result)).not.toBe(statisticsKey(selection));
 });
 it('does not accept a legacy unpatterned result as the selected manual query',()=>{
  const request={...initialStatisticsRequest(),asset:'eurusd'};
  const legacy={dataSource:request.dataSource,symbol:request.asset,period:request.period,lookbackWeeks:request.lookbackWeeks} as PredictionStatisticsForecast;
  expect(statisticsResultKey(legacy)).not.toBe(statisticsKey(request));
 });
});

import {describe,it,expect} from 'vitest';
import {historyFrom} from './historyRange';
import {intervals,intervalSeconds} from '../types';

describe('chart history ranges across closed forex sessions',()=>{
 it('reaches Friday from a Sunday reopening even for one-minute initial/older history',()=>{
  const sunday=Date.parse('2026-11-01T22:00:00Z')/1000,friday=Date.parse('2026-10-30T21:59:00Z')/1000;
  expect(sunday-500*60).toBeGreaterThan(friday);
  expect(historyFrom('dukascopy','1m',sunday,500)).toBeLessThan(friday);
 });
 it('leaves room for holidays when a short remaining indicator warmup crosses a closure',()=>{
  const sunday=Date.parse('2026-11-01T22:00:00Z')/1000,thursday=Date.parse('2026-10-29T20:00:00Z')/1000;
  expect(historyFrom('dukascopy','1m',sunday,5)).toBeLessThan(thursday);
 });
 it('keeps longer-period requests proportional and bounded, with nonnegative dates',()=>{
  const end=Date.parse('2026-11-01T22:00:00Z')/1000;
  expect(end-historyFrom('dukascopy','1h',end,1000)).toBe(2000*3600);
  expect(end-historyFrom('dukascopy','1w',end,500)).toBe(1000*7*86400);
  expect(historyFrom('dukascopy','1w',1000,500)).toBe(0);
 });
 it('preserves the original range for every crypto source, period and request size',()=>{
  const end=Date.parse('2026-11-01T22:00:00Z')/1000;
  for(const source of ['binance','bybit','coinbase'] as const)for(const period of intervals)for(const count of [5,30,500,1000])expect(historyFrom(source,period,end,count)).toBe(Math.max(0,end-count*intervalSeconds[period]));
 });
});

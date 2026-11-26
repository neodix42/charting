import {describe,expect,it} from 'vitest';
import {chartQuoteDisplay,chartQuotePrice,quoteSpreadText} from '../charts/ChartQuoteLegend';

const now=Date.UTC(2026,10,26,12);
describe('chart top-of-book display',()=>{
 it('uses the midpoint basis for spread and retains exact quote sides',()=>{
  const quote=chartQuoteDisplay({state:'live',quote:{bid:99,ask:101,time:now}},now);
  expect(quote).toMatchObject({bid:99,ask:101,spread:200,time:now,stale:false,notice:'Top of book'});
  expect(quoteSpreadText(quote.spread)).toBe('200.00');
 });
 it('keeps tiny nonzero spreads visible without overflowing a large midpoint',()=>{
  expect(quoteSpreadText(.000001)).toBe('<0.0001');
  expect(quoteSpreadText(.001)).toBe('0.001');
  const value=chartQuoteDisplay({state:'live',quote:{bid:1e308,ask:1.1e308,time:now}},now);
  expect(value.spread).toBeCloseTo(952.380952,5);
  expect(chartQuoteDisplay({state:'live',quote:{bid:1,ask:1,time:now}},now).spread).toBe(0);
 });
 it('does not manufacture a missing side or derive spread from candles',()=>{
  expect(chartQuoteDisplay({state:'live',quote:{bid:null,ask:101,time:now}},now)).toMatchObject({bid:null,ask:101,spread:null,notice:'One-sided book'});
  expect(chartQuoteDisplay({state:'live'},now)).toMatchObject({bid:null,ask:null,spread:null,notice:'Awaiting quotes'});
  expect(quoteSpreadText(null)).toBe('—');
 });
 it('retains dated reference quotes but explicitly marks their age and connection state',()=>{
  const health={state:'live',quote:{bid:99,ask:101,time:now-30001}};
  expect(chartQuoteDisplay(health,now)).toMatchObject({bid:99,ask:101,time:now-30001,stale:true,notice:'Stale quote'});
  for(const state of ['disconnected','offline','marketClosed','paused','stale'])expect(chartQuoteDisplay({...health,state,quote:{...health.quote,time:now}},now).stale).toBe(true);
  expect(chartQuoteDisplay({...health,quote:{...health.quote,time:now-30000}},now).stale).toBe(false);
 });
 it('rejects invalid, crossed and future snapshots without fabricating prices',()=>{
  for(const quote of [
   {bid:101,ask:99,time:now},{bid:99,ask:101,time:0},{bid:99,ask:101,time:now+5001},
   {bid:99,ask:101,time:Infinity},{bid:NaN,ask:Infinity,time:now},
  ]){const value=chartQuoteDisplay({state:'live',quote},now);expect(value.bid).toBeNull();expect(value.ask).toBeNull();expect(value.spread).toBeNull();}
  expect(chartQuoteDisplay({state:'live',quote:{bid:0,ask:101,time:now}},now)).toMatchObject({bid:null,ask:101,spread:null});
 });
 it('never treats calculated ratio candles as an executable book',()=>{
  expect(chartQuoteDisplay({state:'live',quote:{bid:99,ask:101,time:now}},now,true)).toMatchObject({bid:null,ask:null,spread:null,notice:'Calculated instrument · no executable quote'});
 });
 it('uses instrument formatting without rounding away finer observed quotes',()=>{
  const format=(value:number)=>value.toLocaleString('en-US',{minimumFractionDigits:2,maximumFractionDigits:2});
  expect(chartQuotePrice(61000,format)).toBe('61,000.00');
  expect(chartQuotePrice(1.10001,format)).toBe('1.10001');
  expect(chartQuotePrice(.000000031,format)).toBe('0.000000031');
 });
});

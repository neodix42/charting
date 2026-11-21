import {describe,it,expect} from 'vitest';
import {maximumOrderQuantity} from '../workspace/tradeQuantity';

describe('maximum order quantity display',()=>{
 const now=100000,metrics={metricsTime:now,maximumQuantity:'0.123456789123456789',maximumQuantityBasis:'Rounded down to the provider step'};
 it('preserves all provider decimals and confirmed zero',()=>{
  expect(maximumOrderQuantity(metrics,false,'',now)).toEqual({text:metrics.maximumQuantity,reason:metrics.maximumQuantityBasis});
  expect(maximumOrderQuantity({...metrics,maximumQuantity:'0'},false,'',now).text).toBe('0');
 });
 it('hides unverified, malformed, failed and expired limits',()=>{
  for(const maximumQuantity of ['',undefined,'NaN','Infinity','-1','1e3','1,000','<1','1.2.3'])expect(maximumOrderQuantity({...metrics,maximumQuantity},false,'',now).text).toBe('Unavailable');
  expect(maximumOrderQuantity(metrics,false,'Transport failed',now)).toEqual({text:'Unavailable',reason:'Transport failed'});
  expect(maximumOrderQuantity({...metrics,maximumQuantityError:'No book depth'},false,'',now).reason).toBe('No book depth');
  for(const metricsTime of [undefined,NaN,now+5001,now-30001])expect(maximumOrderQuantity({...metrics,metricsTime},true,'',now).text).toBe('Unavailable');
 });
 it('shows pending reads and keeps immutable reviews and fresh polling snapshots',()=>{
  expect(maximumOrderQuantity(null,true,'',now).text).toBe('Loading…');
  expect(maximumOrderQuantity(null,false,'',now).text).toBe('Unavailable');
  expect(maximumOrderQuantity(metrics,true,'',now+10000).text).toBe(metrics.maximumQuantity);
  expect(maximumOrderQuantity(metrics,false,'',now+60000,true).text).toBe(metrics.maximumQuantity);
 });
});

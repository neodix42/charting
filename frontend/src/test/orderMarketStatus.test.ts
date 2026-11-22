import {describe,expect,it} from 'vitest';
import {orderMarketStatus} from '../charts/orderMarketStatus';

const now=Date.parse('2026-11-23T12:00:00Z');
const regular={marketHoursStatus:'available',marketHoursFirstDay:'1',marketHoursLastDay:'5',marketHoursOpen:'09:00:00',marketHoursClose:'17:00:00',marketHoursTimeZone:'UTC'};
const calendar={marketHoursStatus:'available',marketHoursSource:'JForex instrument calendar',marketHoursFrom:String(now-86400000),marketHoursTo:String(now+86400000),marketHoursClosedIntervals:JSON.stringify([[now-1000,now+1000]])};

describe('Dukascopy order market status',()=>{
 it('prioritizes the broker closure report over an apparently open regular session',()=>{
  expect(orderMarketStatus({state:'marketClosed',reason:'Actual broker closure'},regular,now)).toEqual({text:'Market closed',basis:'Actual broker closure'});
 });
 it('uses the actual dated calendar rather than treating a live feed as an open market',()=>{
  expect(orderMarketStatus({state:'live'},calendar,now).text).toBe('Market closed');
  expect(orderMarketStatus({state:'live'},calendar,now+1000).text).toBe('Market opened');
 });
 it('can show a qualified regular schedule when no broker closure report exists',()=>{
  expect(orderMarketStatus({state:'provisional'},regular,now).text).toBe('Market opened');
  expect(orderMarketStatus({state:'live'},regular,Date.parse('2026-11-22T12:00:00Z')).text).toBe('Market closed');
 });
 it('keeps missing or expired opening evidence unavailable even with a live connection',()=>{
  expect(orderMarketStatus({state:'live'},undefined,now).text).toBe('Market status unavailable');
  expect(orderMarketStatus({state:'live'},calendar,now+86400000).text).toBe('Market status unavailable');
 });
});

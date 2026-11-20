import {describe,expect,it} from 'vitest';
import {regularMarketHours} from '../charts/DukascopyTradingHours';

const session={marketHoursStatus:'available',marketHoursFirstDay:'1',marketHoursLastDay:'5',marketHoursOpen:'09:30:00',marketHoursClose:'16:00:00',marketHoursTimeZone:'Asia/Hong_Kong'};

describe('provider regular market hours',()=>{
 it('keeps the market timezone and weekday range without inventing intraday breaks',()=>{
  expect(regularMarketHours(session)).toBe('Mon–Fri · 09:30–16:00 · Asia/Hong_Kong');
 });
 it('preserves overnight and wrapped-week windows and nonzero seconds',()=>{
  expect(regularMarketHours({...session,marketHoursFirstDay:'7',marketHoursOpen:'22:00:01',marketHoursClose:'21:59:59',marketHoursTimeZone:'America/New_York'})).toBe('Sun–Fri · 22:00:01–21:59:59 · America/New_York');
  expect(regularMarketHours({...session,marketHoursLastDay:'1'})).toBe('Mon · 09:30–16:00 · Asia/Hong_Kong');
 });
 it('retains an equal clock window as reported without assuming 24/7',()=>{
  expect(regularMarketHours({...session,marketHoursOpen:'00:00:00',marketHoursClose:'00:00:00'})).toBe('Mon–Fri · 00:00–00:00 · Asia/Hong_Kong');
 });
 it('does not turn absent, unavailable or malformed provider data into a schedule',()=>{
  for(const metadata of [undefined,{}, {...session,marketHoursStatus:'unavailable'},...['0','8','1.5','NaN',''].map(marketHoursFirstDay=>({...session,marketHoursFirstDay})),...['24:00:00','09:60:00','09:30:99','09:30:00.123',''].map(marketHoursOpen=>({...session,marketHoursOpen})),{...session,marketHoursTimeZone:' '}])expect(regularMarketHours(metadata)).toBeNull();
 });
});

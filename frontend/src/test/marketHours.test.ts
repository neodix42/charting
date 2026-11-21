import {describe,expect,it} from 'vitest';
import {regularMarketHours,regularMarketStatus} from '../charts/DukascopyTradingHours';

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
  for(const metadata of [undefined,{}, {...session,marketHoursStatus:'unavailable'},...['0','8','1.5','NaN',''].map(marketHoursFirstDay=>({...session,marketHoursFirstDay})),...['24:00:00','09:60:00','09:30:99','09:30:00.123',''].map(marketHoursOpen=>({...session,marketHoursOpen})),{...session,marketHoursTimeZone:' '},{...session,marketHoursTimeZone:'Unknown/Market'}])expect(regularMarketHours(metadata)).toBeNull();
 });
 it('matches SDK regular-session boundaries and excludes the local weekend',()=>{
  const status=(time:string)=>regularMarketStatus(session,Date.parse(time));
  expect(status('2026-11-23T01:29:59Z')).toBe('Closed');
  expect(status('2026-11-23T01:30:00Z')).toBe('Closed');
  expect(status('2026-11-23T01:30:01Z')).toBe('Opened');
  expect(status('2026-11-23T07:59:59Z')).toBe('Opened');
  expect(status('2026-11-23T08:00:00Z')).toBe('Closed');
  expect(status('2026-11-21T02:00:00Z')).toBe('Closed');
  expect(status('2026-11-22T23:59:59Z')).toBe('Closed'); // Monday in Hong Kong, before open.
 });
 it('uses the market timezone and its daylight-saving rules instead of the machine clock',()=>{
  const ny={...session,marketHoursTimeZone:'America/New_York'};
  expect(regularMarketStatus(ny,Date.parse('2026-03-02T14:30:01Z'))).toBe('Opened');
  expect(regularMarketStatus(ny,Date.parse('2026-03-09T13:30:01Z'))).toBe('Opened');
  expect(regularMarketStatus(ny,Date.parse('2026-03-09T13:29:59Z'))).toBe('Closed');
  expect(regularMarketStatus(ny,Date.parse('2026-03-09T20:00:00Z'))).toBe('Closed');
 });
 it('keeps ambiguous overnight and continuous clocks as hours without making up Opened/Closed',()=>{
  for(const metadata of [{...session,marketHoursOpen:'22:00:00',marketHoursClose:'06:00:00'},{...session,marketHoursOpen:'00:00:00',marketHoursClose:'00:00:00'},{...session,marketHoursFirstDay:'7'}]){
   expect(regularMarketHours(metadata)).not.toBeNull();
   expect(regularMarketStatus(metadata,Date.parse('2026-11-23T02:00:00Z'))).toBeNull();
  }
  expect(regularMarketStatus(session,NaN)).toBeNull();
  expect(regularMarketStatus({...session,marketHoursTimeZone:'Unknown/Market'},Date.now())).toBeNull();
 });
});

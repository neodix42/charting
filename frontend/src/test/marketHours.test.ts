import {describe,expect,it} from 'vitest';
import {calendarMarketSession,regularMarketHours,regularMarketStatus} from '../charts/DukascopyTradingHours';

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

const instant=(value:string)=>Date.parse(value);
const calendar={marketHoursStatus:'available',marketHoursSource:'JForex instrument calendar',marketHoursTimeZone:'UTC',marketHoursFrom:String(instant('2026-11-13T00:00:00Z')),marketHoursTo:String(instant('2026-11-29T00:00:00Z')),marketHoursClosedIntervals:JSON.stringify([
 [instant('2026-11-13T22:00:00Z'),instant('2026-11-15T22:00:00Z')],
 [instant('2026-11-20T22:00:00Z'),instant('2026-11-22T22:00:00Z')],
 [instant('2026-11-27T22:00:00Z'),instant('2026-11-29T22:00:00Z')],
])};

describe('dated provider instrument calendar',()=>{
 it('shows the next weekly forex session during a supplied weekend closure',()=>{
  expect(calendarMarketSession(calendar,instant('2026-11-21T12:00:00Z'))).toEqual({status:'Closed',hours:'Next opens Sun 2026-11-22 22:00 UTC · closes Fri 2026-11-27 22:00 UTC'});
 });
 it('uses exact calendar boundaries and actual current session, without daily assumptions',()=>{
  expect(calendarMarketSession(calendar,instant('2026-11-20T21:59:59Z'))).toEqual({status:'Opened',hours:'Opened Sun 2026-11-15 22:00 UTC · Closes Fri 2026-11-20 22:00 UTC'});
  expect(calendarMarketSession(calendar,instant('2026-11-20T22:00:00Z'))?.status).toBe('Closed');
  expect(calendarMarketSession(calendar,instant('2026-11-22T21:59:59Z'))?.status).toBe('Closed');
  expect(calendarMarketSession(calendar,instant('2026-11-22T22:00:00Z'))?.status).toBe('Opened');
 });
 it('never presents a coverage boundary as a known session opening or closing',()=>{
  expect(calendarMarketSession(calendar,instant('2026-11-13T12:00:00Z'))?.hours).toBe('Closes Fri 2026-11-13 22:00 UTC');
  expect(calendarMarketSession(calendar,instant('2026-11-28T12:00:00Z'))?.hours).toBe('Closed through Sun 2026-11-29 00:00 UTC');
 });
 it('rejects expired/missing coverage and empty calendars instead of inventing 24/7',()=>{
  for(const now of [NaN,instant('2026-11-12T23:59:59Z'),instant('2026-11-29T00:00:00Z')])expect(calendarMarketSession(calendar,now)).toBeNull();
  for(const value of [undefined,{}, {...calendar,marketHoursClosedIntervals:'[]'}, {...calendar,marketHoursClosedIntervals:'oops'}, {...calendar,marketHoursClosedIntervals:'[[1,2]]'}, {...calendar,marketHoursClosedIntervals:'[[0,2]]'}, {...calendar,marketHoursClosedIntervals:JSON.stringify([[instant('2026-11-20T22:00:00Z'),instant('2026-11-19T22:00:00Z')]])}, {...calendar,marketHoursTo:'NaN'}, {...calendar,marketHoursStatus:'unavailable'}])expect(calendarMarketSession(value,instant('2026-11-21T12:00:00Z'))).toBeNull();
 });
 it('merges touching and overlapping broker closures before finding the next opening',()=>{
  const value={...calendar,marketHoursClosedIntervals:JSON.stringify([
   [instant('2026-11-21T00:00:00Z'),instant('2026-11-22T22:00:00Z')],
   [instant('2026-11-20T22:00:00Z'),instant('2026-11-21T00:00:00Z')],
   [instant('2026-11-21T01:00:00Z'),instant('2026-11-22T21:00:00Z')],
  ])};
  expect(calendarMarketSession(value,instant('2026-11-21T12:00:00Z'))).toEqual({status:'Closed',hours:'Next opens Sun 2026-11-22 22:00 UTC'});
  expect(calendarMarketSession(value,instant('2026-11-22T22:00:00Z'))).toEqual({status:'Opened',hours:'Opened Sun 2026-11-22 22:00 UTC'});
 });
});

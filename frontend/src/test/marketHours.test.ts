import {describe,expect,it} from 'vitest';
import {calendarMarketSession,marketSessionDisplay,marketSessionTimes,regularMarketHours,regularMarketStatus} from '../charts/DukascopyTradingHours';

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

describe('UTC session opening and closing times',()=>{
 const times=(metadata:typeof session,time:string)=>marketSessionTimes(metadata,instant(time));
 it('converts the actual current regular session from market clocks to UTC',()=>{
  expect(times(session,'2026-11-23T02:00:00Z')).toEqual({opensAt:instant('2026-11-23T01:30:00Z'),closesAt:instant('2026-11-23T08:00:00Z')});
  expect(times(session,'2026-11-22T23:59:59Z')).toEqual({opensAt:instant('2026-11-23T01:30:00Z'),closesAt:instant('2026-11-23T08:00:00Z')});
 });
 it('advances to the next supported weekday at closing rather than reusing an old session',()=>{
  expect(times(session,'2026-11-27T08:00:00Z')).toEqual({opensAt:instant('2026-11-30T01:30:00Z'),closesAt:instant('2026-11-30T08:00:00Z')});
  const friday={...session,marketHoursFirstDay:'5',marketHoursLastDay:'5'};
  expect(times(friday,'2026-11-27T08:00:00Z')).toEqual({opensAt:instant('2026-12-04T01:30:00Z'),closesAt:instant('2026-12-04T08:00:00Z')});
 });
 it('uses the upcoming session offset across both daylight-saving weekends',()=>{
  const ny={...session,marketHoursTimeZone:'America/New_York'};
  expect(times(ny,'2026-03-06T22:00:00Z')).toEqual({opensAt:instant('2026-03-09T13:30:00Z'),closesAt:instant('2026-03-09T20:00:00Z')});
  expect(times(ny,'2026-10-30T22:00:00Z')).toEqual({opensAt:instant('2026-11-02T14:30:00Z'),closesAt:instant('2026-11-02T21:00:00Z')});
 });
 it('handles fractional-hour offsets, UTC date changes and reported nonzero seconds',()=>{
  const nepal={...session,marketHoursTimeZone:'Asia/Kathmandu',marketHoursOpen:'00:15:03',marketHoursClose:'01:15:07'};
  expect(times(nepal,'2026-11-22T18:00:00Z')).toEqual({opensAt:instant('2026-11-22T18:30:03Z'),closesAt:instant('2026-11-22T19:30:07Z')});
 });
 it('does not choose between ambiguous DST clocks or invent a skipped opening time',()=>{
  const sunday={...session,marketHoursFirstDay:'7',marketHoursLastDay:'7',marketHoursTimeZone:'America/New_York',marketHoursOpen:'01:30:00',marketHoursClose:'03:00:00'};
  expect(times(sunday,'2026-11-01T04:00:00Z')).toBeNull();
  expect(times({...sunday,marketHoursOpen:'02:30:00'},'2026-03-08T05:00:00Z')).toBeNull();
 });
 it('retains unsupported or invalid regular envelopes as unavailable',()=>{
  for(const metadata of [undefined,{}, {...session,marketHoursTimeZone:'Unknown/Market'}, {...session,marketHoursOpen:'22:00:00',marketHoursClose:'06:00:00'}, {...session,marketHoursOpen:'00:00:00',marketHoursClose:'00:00:00'}, {...session,marketHoursFirstDay:'7'}])expect(marketSessionTimes(metadata,instant('2026-11-23T02:00:00Z'))).toBeNull();
  expect(marketSessionTimes(session,NaN)).toBeNull();
  expect(marketSessionTimes(session,Infinity)).toBeNull();
 });
 it('uses actual calendar session boundaries in preference to a conflicting daily schedule',()=>{
  expect(marketSessionTimes({...session,...calendar},instant('2026-11-21T12:00:00Z'))).toEqual({opensAt:instant('2026-11-22T22:00:00Z'),closesAt:instant('2026-11-27T22:00:00Z')});
  expect(marketSessionTimes(calendar,instant('2026-11-20T21:59:59Z'))).toEqual({opensAt:instant('2026-11-15T22:00:00Z'),closesAt:instant('2026-11-20T22:00:00Z')});
  expect(marketSessionTimes(calendar,instant('2026-11-20T22:00:00Z'))).toEqual({opensAt:instant('2026-11-22T22:00:00Z'),closesAt:instant('2026-11-27T22:00:00Z')});
 });
 it('omits unknown calendar boundaries and never converts the coverage horizon into a session',()=>{
  expect(marketSessionTimes(calendar,instant('2026-11-13T12:00:00Z'))).toEqual({closesAt:instant('2026-11-13T22:00:00Z')});
  expect(marketSessionTimes(calendar,instant('2026-11-28T12:00:00Z'))).toEqual({});
  expect(marketSessionTimes(calendar,instant('2026-11-29T00:00:00Z'))).toBeNull();
  const oneClosure={...calendar,marketHoursClosedIntervals:JSON.stringify([[instant('2026-11-20T22:00:00Z'),instant('2026-11-22T22:00:00Z')]])};
  expect(marketSessionTimes(oneClosure,instant('2026-11-21T12:00:00Z'))).toEqual({opensAt:instant('2026-11-22T22:00:00Z')});
 });
});

describe('visible market-hours availability',()=>{
 it('reports loading without replacing known provider timestamps',()=>{
  expect(marketSessionDisplay(undefined,instant('2026-11-24T02:00:00Z'),true)).toEqual({notice:'Hours loading…'});
  expect(marketSessionDisplay(session,instant('2026-11-24T02:00:00Z'),true)).toEqual({opensAt:instant('2026-11-24T01:30:00Z'),closesAt:instant('2026-11-24T08:00:00Z')});
 });
 it('makes unavailable and malformed provider schedules explicit instead of disappearing',()=>{
  expect(marketSessionDisplay({marketHoursStatus:'unavailable'},instant('2026-11-24T02:00:00Z'))).toEqual({notice:'Hours unavailable: provider schedule not supplied.'});
  expect(marketSessionDisplay({...session,marketHoursOpen:'09:60:00'},instant('2026-11-24T02:00:00Z'))).toEqual({notice:'Hours unavailable: provider schedule cannot be resolved.'});
  expect(marketSessionDisplay({...session,marketHoursOpen:'22:00:00',marketHoursClose:'06:00:00'},instant('2026-11-24T02:00:00Z'))).toEqual({notice:'Hours unavailable: provider schedule cannot be resolved.'});
 });
 it('distinguishes expired calendar coverage from missing session boundaries',()=>{
  expect(marketSessionDisplay(calendar,instant('2026-11-29T00:00:00Z'))).toEqual({notice:'Hours expired: refresh needed.'});
  expect(marketSessionDisplay(calendar,instant('2026-11-12T23:59:59Z'))).toEqual({notice:'Hours unavailable: outside calendar coverage.'});
  expect(marketSessionDisplay(calendar,instant('2026-11-28T12:00:00Z'))).toEqual({notice:'Hours unavailable: no session boundaries in calendar coverage.'});
 });
 it('preserves a known boundary while explaining the missing one without inventing dates',()=>{
  expect(marketSessionDisplay(calendar,instant('2026-11-13T12:00:00Z'))).toEqual({closesAt:instant('2026-11-13T22:00:00Z'),notice:'Opening time unavailable in the supplied calendar.'});
  const oneClosure={...calendar,marketHoursClosedIntervals:JSON.stringify([[instant('2026-11-20T22:00:00Z'),instant('2026-11-22T22:00:00Z')]])};
  expect(marketSessionDisplay(oneClosure,instant('2026-11-21T12:00:00Z'))).toEqual({opensAt:instant('2026-11-22T22:00:00Z'),notice:'Closing time unavailable in the supplied calendar.'});
 });
 it('labels retained provider schedules with their original capture time without replacing valid hours',()=>{
  const retained={...session,marketHoursRetained:'true',marketHoursFetchedAt:String(instant('2026-11-23T06:01:49.519Z'))};
  expect(marketSessionDisplay(retained,instant('2026-11-24T02:00:00Z'))).toEqual({opensAt:instant('2026-11-24T01:30:00Z'),closesAt:instant('2026-11-24T08:00:00Z'),notice:'Last-known provider schedule. Saved 2026-11-23 06:01:49 UTC.'});
  expect(marketSessionDisplay({...retained,marketHoursFetchedAt:'unknown'},instant('2026-11-24T02:00:00Z')).notice).toBe('Last-known provider schedule.');
  expect(marketSessionDisplay({...calendar,marketHoursRetained:'true'},instant('2026-11-29T00:00:00Z')).notice).toBe('Hours expired: refresh needed.');
 });
});

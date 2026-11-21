import {useEffect,useState} from 'react';
import type {Instrument} from '../types';
import './DukascopyTradingHours.css';

const days=['Mon','Tue','Wed','Thu','Fri','Sat','Sun'];
const clock=/^(?:[01]\d|2[0-3]):[0-5]\d:[0-5]\d$/;
const displayTime=(value:string)=>value.endsWith(':00')?value.slice(0,5):value;
const seconds=(value:string)=>value.split(':').reduce((total,part)=>total*60+Number(part),0);

function schedule(metadata:Instrument['providerMetadata']) {
 if(metadata?.marketHoursStatus!=='available')return null;
 const first=metadata.marketHoursFirstDay,last=metadata.marketHoursLastDay,open=metadata.marketHoursOpen,close=metadata.marketHoursClose,zone=metadata.marketHoursTimeZone?.trim();
 if(!/^[1-7]$/.test(first??'')||!/^[1-7]$/.test(last??'')||!clock.test(open??'')||!clock.test(close??'')||!zone)return null;
 try {
  const formatter=new Intl.DateTimeFormat('en-US',{timeZone:zone,weekday:'short',hour:'2-digit',minute:'2-digit',second:'2-digit',hourCycle:'h23'});
  return {first:Number(first),last:Number(last),open:open!,close:close!,zone,formatter};
 }catch{return null;}
}

export function regularMarketHours(metadata:Instrument['providerMetadata']):string|null {
 const value=schedule(metadata);if(!value)return null;
 const {first,last,open,close,zone}=value;
 const workingDays=first===last?days[first-1]:`${days[first-1]}–${days[last-1]}`;
 return `${workingDays} · ${displayTime(open)}–${displayTime(close)} · ${zone}`;
}

/** The SDK session envelope describes regular hours, not holidays or live tradability. */
export function regularMarketStatus(metadata:Instrument['providerMetadata'],now:number):'Opened'|'Closed'|null {
 const value=schedule(metadata);if(!value||!Number.isFinite(now))return null;
 const {first,last,formatter}=value,open=seconds(value.open),close=seconds(value.close);
 // The pinned SDK's isOpenAt supports a weekday range with a daytime window.
 // These fields cannot prove overnight, wrapped-week or continuous sessions;
 // keep their supplied hours visible without inventing a tradability state.
 if(open>=close||first>last)return null;
 try {
  const parts=formatter.formatToParts(now),part=(name:Intl.DateTimeFormatPartTypes)=>parts.find(item=>item.type===name)?.value;
  const day=days.indexOf(part('weekday')??'')+1;
  if(!day)return null;
  const local=Number(part('hour'))*3600+Number(part('minute'))*60+Number(part('second'));
  if(day<first||day>last)return 'Closed';
  // IMarketInfo.isOpenAt uses LocalTime.isAfter(open) && isBefore(close).
  return local>open&&local<close?'Opened':'Closed';
 }catch{return null;}
}

const timestamp=(value:unknown):value is number=>typeof value==='number'&&Number.isSafeInteger(value)&&value>0&&value<=8640000000000000;
const calendarTime=(value:number)=>`${days[(new Date(value).getUTCDay()+6)%7]} ${new Date(value).toISOString().slice(0,19).replace('T',' ').replace(/:00$/,'')} UTC`;

/** Only a nonempty, bounded instrument calendar can establish an open session. */
export function calendarMarketSession(metadata:Instrument['providerMetadata'],now:number):{status:'Opened'|'Closed';hours:string}|null {
 if(metadata?.marketHoursStatus!=='available'||metadata.marketHoursSource!=='JForex instrument calendar'||!Number.isFinite(now))return null;
 const from=Number(metadata.marketHoursFrom),to=Number(metadata.marketHoursTo),encoded=metadata.marketHoursClosedIntervals;
 if(!timestamp(from)||!timestamp(to)||from>=to||now<from||now>=to||!encoded||encoded.length>32768)return null;
 try {
  const supplied:unknown=JSON.parse(encoded);
  if(!Array.isArray(supplied)||!supplied.length||supplied.length>512)return null;
  const intervals:Array<[number,number]>=[];
  for(const interval of supplied){
   if(!Array.isArray(interval)||interval.length!==2||!timestamp(interval[0])||!timestamp(interval[1])||interval[0]>=interval[1])return null;
   if(interval[1]>from&&interval[0]<to)intervals.push([Math.max(from,interval[0]),Math.min(to,interval[1])]);
  }
  if(!intervals.length)return null;
  intervals.sort((a,b)=>a[0]-b[0]);
  const merged:Array<[number,number]>=[];
  for(const interval of intervals){const last=merged.at(-1);if(last&&interval[0]<=last[1])last[1]=Math.max(last[1],interval[1]);else merged.push(interval);}
  const closed=merged.findIndex(([start,end])=>now>=start&&now<end);
  if(closed>=0){
   const end=merged[closed][1],next=merged[closed+1];
   return {status:'Closed',hours:end>=to?`Closed through ${calendarTime(to)}`:`Next opens ${calendarTime(end)}${next?` · closes ${calendarTime(next[0])}`:''}`};
  }
  const previous=merged.filter(([,end])=>end<=now).at(-1),next=merged.find(([start])=>start>now);
  const opening=previous?`Opened ${calendarTime(previous[1])}`:'',closing=next?`Closes ${calendarTime(next[0])}`:'';
  // A calendar horizon is not an opening or closing boundary.
  return {status:'Opened',hours:[opening,closing].filter(Boolean).join(' · ')};
 }catch{return null;}
}

export function DukascopyTradingHours({instrument}:{instrument?:Instrument}) {
 const [now,setNow]=useState(Date.now);
 const hours=regularMarketHours(instrument?.providerMetadata);
 const calendar=calendarMarketSession(instrument?.providerMetadata,now);
 const metadata=instrument?.providerMetadata;
 useEffect(()=>{
  if(metadata?.marketHoursStatus!=='available')return;
  setNow(Date.now());const timer=setInterval(()=>setNow(Date.now()),1000);
  return()=>clearInterval(timer);
 },[metadata]);
 if(!hours&&!calendar)return null;
 const status=calendar?.status??regularMarketStatus(metadata,now);
 const text=calendar?`Opening hours · ${calendar.hours}`:`Regular hours ${hours}`;
 const hint=calendar?'Opened/Closed follows the supplied JForex instrument calendar within its dated coverage, not a live broker tradability check. Calendar updates and trading halts may change actual tradability.':'Opened/Closed follows the regular session window in the market timezone, not a live broker tradability check. Intraday breaks, holidays and trading halts may change actual tradability.';
 return <aside className="chart-market-hours chart-market-schedule" aria-label="Dukascopy trading hours" title={`${text}. ${hint}`}>
  <span><strong className={status==='Opened'?'market-open':status==='Closed'?'market-closed':undefined} aria-label="Asset regular-session status">{status??'Status unavailable'}</strong> · {calendar?'broker calendar':'regular schedule'}</span>
  <span>{text}</span>
 </aside>;
}

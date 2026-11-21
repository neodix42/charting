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

export function DukascopyTradingHours({instrument}:{instrument?:Instrument}) {
 const [now,setNow]=useState(Date.now);
 const hours=regularMarketHours(instrument?.providerMetadata);
 useEffect(()=>{
  if(!hours)return;
  setNow(Date.now());const timer=setInterval(()=>setNow(Date.now()),1000);
  return()=>clearInterval(timer);
 },[hours]);
 if(!hours)return null;
 const status=regularMarketStatus(instrument?.providerMetadata,now);
 const text=`Regular hours ${hours}`;
 const hint='Opened/Closed follows the regular session window in the market timezone, not a live broker tradability check. Intraday breaks, holidays and trading halts may change actual tradability.';
 return <aside className="chart-market-hours chart-market-schedule" aria-label="Dukascopy trading hours" title={`${text}. ${hint}`}>
  <span><strong className={status==='Opened'?'market-open':status==='Closed'?'market-closed':undefined} aria-label="Asset regular-session status">{status??'Status unavailable'}</strong> · regular schedule</span>
  <span>{text}</span>
 </aside>;
}

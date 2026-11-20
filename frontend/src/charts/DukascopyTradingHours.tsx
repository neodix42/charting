import type {Instrument} from '../types';

const days=['Mon','Tue','Wed','Thu','Fri','Sat','Sun'];
const clock=/^(?:[01]\d|2[0-3]):[0-5]\d:[0-5]\d$/;
const displayTime=(value:string)=>value.endsWith(':00')?value.slice(0,5):value;

export function regularMarketHours(metadata:Instrument['providerMetadata']):string|null {
 if(metadata?.marketHoursStatus!=='available')return null;
 const first=metadata.marketHoursFirstDay,last=metadata.marketHoursLastDay,open=metadata.marketHoursOpen,close=metadata.marketHoursClose,zone=metadata.marketHoursTimeZone?.trim();
 if(!/^[1-7]$/.test(first??'')||!/^[1-7]$/.test(last??'')||!clock.test(open??'')||!clock.test(close??'')||!zone)return null;
 const workingDays=first===last?days[Number(first)-1]:`${days[Number(first)-1]}–${days[Number(last)-1]}`;
 return `${workingDays} · ${displayTime(open)}–${displayTime(close)} · ${zone}`;
}

export function DukascopyTradingHours({instrument}:{instrument?:Instrument}) {
 const hours=regularMarketHours(instrument?.providerMetadata);
 if(!hours)return null;
 const text=`Regular hours ${hours}`;
 const hint='Regular daily session window in the market timezone. Intraday breaks, holidays and trading halts may change actual tradability.';
 return <aside className="chart-market-hours" aria-label="Dukascopy trading hours" title={`${text}. ${hint}`}>{text}</aside>;
}

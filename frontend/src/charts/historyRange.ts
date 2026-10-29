import {intervalSeconds,type Exchange,type Interval} from '../types';

/** A requested number of forex bars can span a weekend or a session holiday. */
export function historyFrom(exchange:Exchange,timeframe:Interval,to:number,count:number){
 const duration=count*intervalSeconds[timeframe];
 const lookback=exchange==='dukascopy'?Math.max(7*86400,2*duration):duration;
 return Math.max(0,to-lookback);
}

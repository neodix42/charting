import {useEffect,useRef,useState} from 'react';
import type {Backend,FeedKey,TradeMetrics,TradeRequest} from '../types';
import {errorText} from '../types';

interface Wanted {request:TradeRequest;api:Backend;ready:boolean}

/** Coalesce edits while one account read is in flight; never queue every keystroke. */
export function useTradeMetrics(api:Backend,request:TradeRequest|null,refresh:number){
 const [metrics,setMetrics]=useState<TradeMetrics|null>(null),[error,setError]=useState(''),[loading,setLoading]=useState(false);
 const alive=useRef(false),wanted=useRef<Wanted|null>(null),inFlight=useRef(false),pump=useRef<()=>void>(()=>{});
 const requestKey=JSON.stringify(request);
 useEffect(()=>{alive.current=true;return()=>{alive.current=false;wanted.current=null;};},[]);
 pump.current=()=>{
  const current=wanted.current;if(!alive.current||!current?.ready||inFlight.current)return;
  current.ready=false;inFlight.current=true;setLoading(true);
  void current.api.GetTradeMetrics(current.request).then(next=>{
   if(!alive.current||wanted.current!==current)return;
   const expected=current.request;
   if(['exchange','market','instrument','timeframe'].some(field=>next.feedKey[field as keyof FeedKey]!==expected.feedKey[field as keyof FeedKey])||next.side!==expected.side||next.quantity.trim()!==expected.quantity.trim()||next.leverage!==expected.leverage)throw Error('Account metrics do not match the current order. Refresh quote to retry.');
   setMetrics(next);setError('');
  }).catch(reason=>{if(alive.current&&wanted.current===current){setMetrics(null);setError(errorText(reason));}}).finally(()=>{
   inFlight.current=false;if(alive.current&&wanted.current===current)setLoading(false);
   if(wanted.current?.ready)pump.current();
  });
 };
 useEffect(()=>{
  const next:TradeRequest|null=JSON.parse(requestKey);setMetrics(null);setError('');setLoading(!!next);
  if(!next){wanted.current=null;return;}
  const current:Wanted={request:next,api,ready:false};wanted.current=current;
  const read=()=>{if(wanted.current!==current)return;current.ready=true;pump.current();};
  const debounce=setTimeout(read,300),poll=setInterval(read,10000);
  return()=>{clearTimeout(debounce);clearInterval(poll);if(wanted.current===current)wanted.current=null;};
 },[api,requestKey,refresh]);
 return {metrics,error,loading};
}

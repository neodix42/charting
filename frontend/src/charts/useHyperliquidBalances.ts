import {useEffect,useRef,useState} from 'react';
import type {Backend,HyperliquidBalances} from '../types';
import {errorText} from '../types';
import {TRADING_CONFIGURED} from '../workspace/trading';

interface Wanted {api:Backend;network:string;ready:boolean}

/** One bounded overview request at a time, including explicit refreshes. */
export function useHyperliquidBalances(api:Backend,network:string,paused:boolean,refresh:number){
 const [value,setValue]=useState<HyperliquidBalances|null>(null),[error,setError]=useState(''),[loading,setLoading]=useState(false);
 const alive=useRef(false),wanted=useRef<Wanted|null>(null),inFlight=useRef(false),account=useRef(''),invalidated=useRef(false),pump=useRef<()=>void>(()=>{});
 useEffect(()=>{alive.current=true;return()=>{alive.current=false;wanted.current=null;};},[]);
 useEffect(()=>{account.current='';invalidated.current=false;setValue(null);setError('');},[api,network]);
 useEffect(()=>{const changed=()=>{wanted.current=null;invalidated.current=true;setValue(null);setLoading(false);setError('Hyperliquid settings changed. Reopen the order ticket to read balances.');};window.addEventListener(TRADING_CONFIGURED,changed);return()=>window.removeEventListener(TRADING_CONFIGURED,changed);},[]);
 pump.current=()=>{
  const current=wanted.current;if(!alive.current||invalidated.current||!current?.ready||inFlight.current)return;
  current.ready=false;inFlight.current=true;setLoading(true);
  void Promise.resolve().then(()=>current.api.GetHyperliquidBalances()).then(next=>{
   if(!alive.current||wanted.current!==current)return;
   if(next.network!==current.network||!/^0x[0-9a-f]{40}$/i.test(next.accountAddress)||!Array.isArray(next.venues)||!Array.isArray(next.tokens)||account.current&&account.current.toLowerCase()!==next.accountAddress.toLowerCase())throw Error('Balances do not match this account and network. Reopen the order ticket.');
   account.current=next.accountAddress;setValue(next);setError('');
  }).catch(reason=>{if(alive.current&&wanted.current===current){setValue(null);setError(errorText(reason));}}).finally(()=>{
   inFlight.current=false;if(alive.current&&wanted.current===current)setLoading(false);
   if(wanted.current?.ready)pump.current();
  });
 };
 useEffect(()=>{
  if(paused||invalidated.current){wanted.current=null;setLoading(false);return;}
  const current:Wanted={api,network,ready:true};wanted.current=current;pump.current();
  const poll=setInterval(()=>{if(wanted.current!==current)return;current.ready=true;pump.current();},30000);
  return()=>{clearInterval(poll);if(wanted.current===current)wanted.current=null;};
 },[api,network,paused,refresh]);
 return {value,error,loading};
}

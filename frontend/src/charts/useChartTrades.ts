import {useEffect,useMemo,useRef,useState} from 'react';
import type {Backend,FeedKey} from '../types';
import {errorText} from '../types';
import type {TradeRecord} from '../workspace/tradeHistory';
import {ChartTradeCache,chartTradeKeys,TRADES_CHANGED} from '../workspace/chartTrades';
import {TRADING_CONFIGURED} from '../workspace/trading';

/** One active-grid refresh loop; retained last-trade prices never wait for it. */
export function useChartTrades(api:Backend,enabled:boolean,keys:FeedKey[],retainedKeys:FeedKey[]=keys){
 const accountGeneration=useRef(0),cache=useRef(new ChartTradeCache()),[revision,setRevision]=useState(0),[accountEpoch,setAccountEpoch]=useState(0);
 const [problem,setProblem]=useState({scope:'',error:''});
 const scope=JSON.stringify(chartTradeKeys(keys)),retainedScope=JSON.stringify(chartTradeKeys(retainedKeys));
 useEffect(()=>{cache.current.retain(JSON.parse(retainedScope) as FeedKey[]);},[retainedScope]);
 // Settings can change while charts are hidden. Discard the old account's
 // prices and its pending replies even when no chart refresh loop is active.
 useEffect(()=>{
  const reset=()=>{accountGeneration.current++;cache.current.clear();setRevision(value=>value+1);setAccountEpoch(value=>value+1);setProblem({scope:'',error:''});};
  window.addEventListener(TRADING_CONFIGURED,reset);
  return()=>window.removeEventListener(TRADING_CONFIGURED,reset);
 },[api]);
 useEffect(()=>{
  const requested=JSON.parse(scope) as FeedKey[];
  if(!enabled||!requested.length)return;
  let active=true,running=false,pending=false,version=0,timer:ReturnType<typeof setTimeout>|undefined;
  const publish=(rows:TradeRecord[],error:string)=>{cache.current.merge(rows,requested);setRevision(value=>value+1);setProblem({scope,error});};
  const load=async()=>{
   if(!active)return;if(running){pending=true;return;}running=true;pending=false;const current=version,generation=accountGeneration.current;
   if(timer)clearTimeout(timer);
   try{
    const snapshot=await api.GetChartTrades(requested);if(!active||current!==version||generation!==accountGeneration.current)return;
    // Local fills are already authoritative for these historical price levels.
    // Paint them before any provider request, including after a process restart.
    publish(snapshot.rows,snapshot.errors.join(' · '));
    const live=snapshot.rows.filter(row=>!['closed','rejected','cancelled','filled'].includes(row.status));
    const updates=live.length?await api.RefreshTradeHistory(live.map(row=>row.id)):[];if(!active||current!==version||generation!==accountGeneration.current)return;
    const byId=new Map(updates.map(row=>[row.id,row]));
    const rows=snapshot.rows.map(row=>{const update=byId.get(row.id);return update?.instrument?update:live.includes(row)?{...row,error:update?.error||'Exchange status for this trade is unavailable'}:row;});
    publish(rows,snapshot.errors.join(' · '));
   }catch(reason){if(active&&current===version&&generation===accountGeneration.current)setProblem({scope,error:errorText(reason)});}
   finally{running=false;if(active){if(pending)void load();else timer=setTimeout(()=>void load(),20000);}}
  };
  const refresh=()=>{
   version++;
   if(!running){void load();return;}
   pending=true;
   const current=version,generation=accountGeneration.current;
   // A newly confirmed fill can be painted from its journal while an older
   // provider refresh drains. Do not start a second provider request for it.
   void api.GetChartTrades(requested).then(snapshot=>{
    if(active&&current===version&&generation===accountGeneration.current)publish(snapshot.rows,snapshot.errors.join(' · '));
   }).catch(reason=>{if(active&&current===version&&generation===accountGeneration.current)setProblem({scope,error:errorText(reason)});});
  };
  window.addEventListener(TRADES_CHANGED,refresh);void load();
  return()=>{active=false;version++;if(timer)clearTimeout(timer);window.removeEventListener(TRADES_CHANGED,refresh);};
 },[api,enabled,scope,accountEpoch]);
 const rows=useMemo(()=>cache.current.rows(JSON.parse(scope) as FeedKey[]),[scope,revision,accountEpoch]);
 return {rows,error:enabled&&problem.scope===scope?problem.error:''};
}

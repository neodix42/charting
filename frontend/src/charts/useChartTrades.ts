import {useEffect,useState} from 'react';
import type {Backend,FeedKey} from '../types';
import {errorText} from '../types';
import type {TradeRecord} from '../workspace/tradeHistory';
import {chartTradeKeys,TRADES_CHANGED} from '../workspace/chartTrades';
import {TRADING_CONFIGURED} from '../workspace/trading';

/** One read/refresh loop for the active chart grid, with no signed operations. */
export function useChartTrades(api:Backend,enabled:boolean,keys:FeedKey[]){
 const [value,setValue]=useState<{rows:TradeRecord[];error:string}>({rows:[],error:''});
 const scope=JSON.stringify(chartTradeKeys(keys));
 useEffect(()=>{
  const requested=JSON.parse(scope) as FeedKey[];setValue({rows:[],error:''});
  if(!enabled||!requested.length)return;
  let active=true,running=false,pending=false,version=0,timer:ReturnType<typeof setTimeout>|undefined,expiry:ReturnType<typeof setTimeout>|undefined;
  const expire=()=>{if(expiry)clearTimeout(expiry);expiry=setTimeout(()=>{if(active){version++;setValue({rows:[],error:'Trade levels expired while waiting for fresh exchange status.'});}},35000);};
  const load=async()=>{
   if(!active)return;if(running){pending=true;return;}running=true;pending=false;const current=version;
   if(timer)clearTimeout(timer);
   try{
    const snapshot=await api.GetChartTrades(requested);if(!active||current!==version)return;
    const updates=snapshot.rows.length?await api.RefreshTradeHistory(snapshot.rows.map(row=>row.id)):[];if(!active||current!==version)return;
    const byId=new Map(updates.map(row=>[row.id,row]));
    const rows=snapshot.rows.map(row=>{const update=byId.get(row.id);return update?.instrument?update:{...row,error:update?.error||'Exchange status for this trade is unavailable'};});
    setValue({rows,error:snapshot.errors.join(' · ')});expire();
   }catch(reason){if(active&&current===version)setValue({rows:[],error:errorText(reason)});}
   finally{running=false;if(active){if(pending)void load();else timer=setTimeout(()=>void load(),20000);}}
  };
  const refresh=()=>{version++;setValue({rows:[],error:''});expire();void load();};
  window.addEventListener(TRADES_CHANGED,refresh);window.addEventListener(TRADING_CONFIGURED,refresh);expire();void load();
  return()=>{active=false;version++;if(timer)clearTimeout(timer);if(expiry)clearTimeout(expiry);window.removeEventListener(TRADES_CHANGED,refresh);window.removeEventListener(TRADING_CONFIGURED,refresh);};
 },[api,enabled,scope]);
 return value;
}

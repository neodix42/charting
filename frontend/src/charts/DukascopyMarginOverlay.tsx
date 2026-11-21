import {useEffect,useState} from 'react';
import type {Backend,FeedKey,TradeMetrics} from '../types';
import type {TradeRecord} from '../workspace/tradeHistory';
import {tradeProfitLoss} from '../workspace/tradeHistory';
import {TRADES_CHANGED,matchesChartTrade} from '../workspace/chartTrades';
import {formatCurrencyAmount} from '../workspace/currencyFormat';

const ratio=(value:number|undefined)=>value&&Number.isFinite(value)&&value>0?`1:${Number(value.toFixed(2))}`:'—';
const percentage=(value:number|null)=>value!==null&&Number.isFinite(value)?`${value.toFixed(1)}%`:'—';
export function DukascopyMarginOverlay({api,feedKey,trades}:{api:Backend;feedKey:FeedKey;trades:TradeRecord[]}){
 const [metrics,setMetrics]=useState<TradeMetrics|null>(null);
 const [error,setError]=useState('');
 useEffect(()=>{
  let active=true,pending=false;
  const load=async()=>{
   if(pending)return;
   pending=true;
   try{
    const result=await api.GetTradeMetrics({feedKey,side:'buy',quantity:'',stopLoss:'',takeProfit:'',leverage:0});
    if(active){setMetrics(result);setError('');}
   }catch(reason){if(active){setMetrics(null);setError(reason instanceof Error?reason.message:String(reason));}}
   finally{pending=false;}
  };
  setMetrics(null);setError('');
  void load();
  const timer=setInterval(()=>void load(),10000);
  const onTrade=()=>void load();
  window.addEventListener(TRADES_CHANGED,onTrade);
  return()=>{active=false;clearInterval(timer);window.removeEventListener(TRADES_CHANGED,onTrade);};
 },[api,feedKey.exchange,feedKey.market,feedKey.instrument,feedKey.timeframe]);
 const activeTrades=trades.filter(row=>row.provider==='dukascopy'&&matchesChartTrade(row,feedKey)&&['open','closing'].includes(row.status)&&Number(row.remainingSize)>0);
 const cutoff=metrics?.marginCutUsePercent&&metrics.marginCutUsePercent>0?10000/metrics.marginCutUsePercent:null;
 const level=metrics?.equity!==undefined&&metrics?.usedMargin!==undefined&&Number(metrics.usedMargin)>0?100*Number(metrics.equity)/Number(metrics.usedMargin):null;
 const stale=!!metrics&&(!metrics.metricsTime||Date.now()-metrics.metricsTime>30000);
 return <aside className="dukascopy-margin-overlay" aria-label="Dukascopy leverage and margin">
  <strong>JForex margin {stale?'(stale)':''}</strong>
  {metrics?<><span>Account <b>{ratio(metrics.accountLeverage)}</b> · Weekend <b>{ratio(metrics.weekendLeverage)}</b></span>
   <span>Instrument cap <b>{ratio(metrics.instrumentMaxLeverage)}</b> · Current effective <b>{ratio(metrics.effectiveLeverage)}</b></span>
   <span>Margin cut <b>{percentage(metrics.marginCutUsePercent??null)} leverage use</b> / <b>{percentage(cutoff)} margin level</b></span>
   <span>Margin level <b>{percentage(level)}</b> · Equity stop <b>{formatCurrencyAmount(metrics.accountEquityStopLoss,metrics.availableBalanceCurrency)??'—'}</b> · Free <b>{formatCurrencyAmount(metrics.availableBalance,metrics.availableBalanceCurrency)??'—'}</b></span>
   <small>Account-wide margin; broker rules can change.</small>
  </>:<small>{error?'Margin data unavailable: '+error:'Loading account margin…'}</small>}
  {activeTrades.length>0&&<div className="dukascopy-open-positions"><strong>Open positions ({activeTrades.length})</strong>
   {activeTrades.map(row=><span key={row.id}>{row.direction} {row.remainingSize} @ {row.entryPrice} · SL {row.stopLoss||'—'} · TP {row.takeProfit||'—'} · P/L {tradeProfitLoss(row).text}</span>)}
  </div>}
 </aside>;
}

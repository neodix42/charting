import {useEffect,useState} from 'react';
import type {Health} from '../types';
import './ChartQuoteLegend.css';

const priceValue=(value:unknown):value is number=>typeof value==='number'&&Number.isFinite(value)&&value>0;
const unavailableStates=new Set(['loading','offline','disconnected','failed','error','restricted','unavailable','unknownInstrument','paused','rateLimited','rate-limited','stale','marketClosed']);

/** Top-of-book spread uses the midpoint, independently of chart candle prices. */
export function chartQuoteDisplay(health:Health,now:number,calculated=false){
 const empty={bid:null as number|null,ask:null as number|null,spread:null as number|null,time:null as number|null,stale:false};
 if(calculated)return{...empty,notice:'Calculated instrument · no executable quote'};
 const quote=health.quote;
 if(!quote)return{...empty,notice:'Awaiting quotes'};
 if(!Number.isSafeInteger(quote.time)||quote.time<=0||quote.time>8640000000000000||!Number.isFinite(now)||quote.time>now+5000)return{...empty,notice:'Quote unavailable'};
 const bid=priceValue(quote.bid)?quote.bid:null,ask=priceValue(quote.ask)?quote.ask:null;
 if(bid===null&&ask===null)return{...empty,notice:'Awaiting quotes'};
 if(bid!==null&&ask!==null&&ask<bid)return{...empty,notice:'Invalid quote'};
 const stale=now-quote.time>30000||unavailableStates.has(health.state);
 // Avoid overflowing the midpoint when both prices are large finite values.
 const midpoint=bid!==null&&ask!==null?bid+(ask-bid)/2:null;
 const spread=midpoint!==null?((ask!-bid!)/midpoint)*10000:null;
 return{bid,ask,spread:spread!==null&&Number.isFinite(spread)?spread:null,time:quote.time,stale,notice:stale?'Stale quote':bid===null||ask===null?'One-sided book':'Top of book'};
}

export function quoteSpreadText(value:number|null){
 if(value===null)return'—';
 if(value>0&&value<0.0001)return'<0.0001';
 return new Intl.NumberFormat('en-US',{minimumFractionDigits:2,maximumFractionDigits:4}).format(value);
}

export function chartQuotePrice(value:number,format:(value:number)=>string){
 const displayed=format(value);
 // Metadata may still be loading, or the provider may quote a finer tick.
 // Keep real book precision when the candle formatter would erase a side.
 return Number(displayed.replaceAll(',',''))===value?displayed:new Intl.NumberFormat('en-US',{maximumSignificantDigits:21}).format(value);
}

export function ChartQuoteLegend({health,price,calculated=false}:{health:Health;price:(value:number)=>string;calculated?:boolean}){
 const [now,setNow]=useState(Date.now);
 useEffect(()=>{
  setNow(Date.now());
  if(!health.quote||calculated)return;
  const timer=setInterval(()=>setNow(Date.now()),1000);
  return()=>clearInterval(timer);
 },[health.quote?.time,calculated]);
 const quote=chartQuoteDisplay(health,now,calculated);
 const timestamp=quote.time===null?'':`${new Date(quote.time).toISOString().slice(0,19).replace('T',' ')} UTC`;
 return <aside className="chart-bid-ask" aria-label="Bid and Ask" title={timestamp?`${quote.notice} · ${timestamp}`:quote.notice}>
  <div className="chart-quote-prices"><span>Bid <b>{quote.bid===null?'—':chartQuotePrice(quote.bid,price)}</b></span><span>Ask <b>{quote.ask===null?'—':chartQuotePrice(quote.ask,price)}</b></span></div>
  <span>Spread <b>{quoteSpreadText(quote.spread)} bps</b></span>
  <small data-quote-stale={quote.stale}>{quote.notice}{quote.stale&&timestamp?` · ${timestamp}`:''}</small>
 </aside>;
}

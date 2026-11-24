'use strict';

// Aggregate synchronized BID quote events, not independent OHLC extrema.
// Inputs must be sorted [timestampMilliseconds, bidPrice] iterables.
// The caller supplies a common trading-session predicate and maximum quote age.
// No forward fill across a closed session or stale quote is permitted.
const DAY=86400000, WEEK=7*DAY, MONDAY=4*DAY;
const instruments=Object.freeze({
  btcusd: {numerator:'btcusd', denominator:null, native:'btcusd'},
  btcxau: {numerator:'btcusd', denominator:'xauusd'},
  xauspx: {numerator:'xauusd', denominator:'usa500idxusd'},
  ethbtc: {numerator:'ethusd', denominator:'btcusd'},
  xauxag: {numerator:'xauusd', denominator:'xagusd',native:'xauxag'}
});
function bucket(t,period){
  if(period==='d1')return Math.floor(t/DAY)*DAY;
  if(period==='w1')return Math.floor((t-MONDAY)/WEEK)*WEEK+MONDAY;
  throw Error('Synthetic instruments support only d1 and w1');
}
async function* aggregateRatio(numerator,denominator,{period='d1',maxAgeMs,isSessionOpen,from=0,to=Infinity}={}){
  bucket(0,period);
  if(!Number.isSafeInteger(maxAgeMs)||maxAgeMs<0||typeof isSessionOpen!=='function'||!(from<to))throw Error('Explicit quote freshness and common session policy required');
  const iterator=x=>x[Symbol.asyncIterator]?.()??x[Symbol.iterator]();
  const a=iterator(numerator),b=iterator(denominator);
  let ah,bh,previousA=-Infinity,previousB=-Infinity,aq,bq,bar;
  async function read(it,prev){
    const item=await it.next();
    if(!item.done){
      const [t,p]=item.value;
      if(!Number.isSafeInteger(t)||t<0||t<prev||!Number.isFinite(p)||p<=0)throw Error('Invalid or unordered source quote');
    }
    return item;
  }
  try{
    ah=await read(a,previousA);bh=await read(b,previousB);
    while(!ah.done||!bh.done){
      const t=Math.min(ah.done?Infinity:ah.value[0],bh.done?Infinity:bh.value[0]);
      if(t>=to)break;
      // Consume both sides of simultaneous events before observing the ratio.
      while(!ah.done&&ah.value[0]===t){aq=ah.value;previousA=t;ah=await read(a,previousA);}
      while(!bh.done&&bh.value[0]===t){bq=bh.value;previousB=t;bh=await read(b,previousB);}
      if(!isSessionOpen(t)){aq=bq=undefined;continue;}
      if(t<from||!aq||!bq||t-aq[0]>maxAgeMs||t-bq[0]>maxAgeMs||
        !isSessionOpen(aq[0])||!isSessionOpen(bq[0]))continue;
      const value=aq[1]/bq[1],start=bucket(t,period);
      if(!Number.isFinite(value)||value<=0)throw Error('Invalid synthetic ratio');
      if(!bar||bar.timestamp!==start){
        if(bar)yield bar;
        bar={timestamp:start,open:value,high:value,low:value,close:value,volume:0};
      }else{
        bar.high=Math.max(bar.high,value);bar.low=Math.min(bar.low,value);bar.close=value;
      }
    }
    if(bar)yield bar;
  }finally{try{await a.return?.();}finally{await b.return?.();}}
}
module.exports={instruments,bucket,aggregateRatio};

import type {TradeValuation} from '../types';
import {formatCurrencyAmount} from './currencyFormat';

export interface TradeLevelEstimate {basisPoints:number|null;amount:number|null;currency:string|null}
const positive=(value:string|undefined)=>{if(!value?.trim())return null;const number=Number(value);return Number.isFinite(number)&&number>0?number:null;};

/** Old quotes can value an explicitly identified planning reference, never a live order. */
export function tradeValuationIsStale(valuation:TradeValuation|null,now:number,allowReference=false):boolean {
 if(!valuation)return false;
 const time=valuation.valuationTime;
 const maxAge=allowReference&&valuation.referenceOnly===true?14*86400000:30000;
 return !time||!Number.isFinite(time)||time<=0||time>now+5000||now-time>maxAge;
}

/** Providers supply the denomination and conversion; never infer it from a symbol name. */
export function tradeLevelEstimate(side:'buy'|'sell',entryPrice:string,quantity:string,exitPrice:string,valuation:TradeValuation={}):TradeLevelEstimate {
 const entry=positive(entryPrice),exit=positive(exitPrice),units=positive(quantity),conversion=valuation.valuationError?null:positive(valuation.profitPerPriceUnit),currency=valuation.accountCurrency?.trim()||null;
 if(entry===null||exit===null)return {basisPoints:null,amount:null,currency};
 const distance=(exit-entry)*(side==='buy'?1:-1),basisPoints=distance/entry*10000;
 const amount=units!==null&&conversion!==null&&currency?distance*units*conversion:null;
 return {basisPoints:Number.isFinite(basisPoints)?basisPoints:null,amount:amount!==null&&Number.isFinite(amount)?amount:null,currency};
}

const signed=new Intl.NumberFormat('en-US',{minimumFractionDigits:2,maximumFractionDigits:2,signDisplay:'always'});
export function formatTradeLevelEstimate(value:TradeLevelEstimate,unavailable='Account amount unavailable'):string {
 const bps=value.basisPoints===null?'bps unavailable':`${signed.format(value.basisPoints)} bps`;
 const amount=value.amount===null||!value.currency?unavailable:formatCurrencyAmount(value.amount,value.currency,{signed:true})??unavailable;
 return `${bps} · ${amount}`;
}

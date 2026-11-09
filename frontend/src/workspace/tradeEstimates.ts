import type {TradeValuation} from '../types';

export interface TradeLevelEstimate {basisPoints:number|null;amount:number|null;currency:string|null}
const positive=(value:string|undefined)=>{if(!value?.trim())return null;const number=Number(value);return Number.isFinite(number)&&number>0?number:null;};

/** Account conversion is supplied by JForex, never inferred from a symbol name. */
export function tradeLevelEstimate(side:'buy'|'sell',entryPrice:string,quantity:string,exitPrice:string,valuation:TradeValuation={}):TradeLevelEstimate {
 const entry=positive(entryPrice),exit=positive(exitPrice),units=positive(quantity),conversion=valuation.valuationError?null:positive(valuation.profitPerPriceUnit),currency=valuation.accountCurrency?.trim()||null;
 if(entry===null||exit===null)return {basisPoints:null,amount:null,currency};
 const distance=(exit-entry)*(side==='buy'?1:-1),basisPoints=distance/entry*10000;
 const amount=units!==null&&conversion!==null&&currency?distance*units*conversion:null;
 return {basisPoints:Number.isFinite(basisPoints)?basisPoints:null,amount:amount!==null&&Number.isFinite(amount)?amount:null,currency};
}

const signed=new Intl.NumberFormat('en-US',{minimumFractionDigits:2,maximumFractionDigits:2,signDisplay:'always'});
export function formatTradeLevelEstimate(value:TradeLevelEstimate):string {
 const bps=value.basisPoints===null?'bps unavailable':`${signed.format(value.basisPoints)} bps`;
 const amount=value.amount===null||!value.currency?'Account amount unavailable':`${signed.format(value.amount)} ${value.currency}`;
 return `${bps} · ${amount}`;
}

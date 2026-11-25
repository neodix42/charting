import type {FeedKey} from '../types';
import type {LineData,UTCTimestamp,WhitespaceData} from 'lightweight-charts';

export interface ComparisonView {weeks:number;assets:string[]}
export interface ComparisonAsset {symbol:string;displaySymbol?:string;name:string;exchange:string;market:string;nativeId:string;quoteAsset:string;feedKey?:FeedKey}
export interface ComparisonCatalog {assets:ComparisonAsset[];warnings:string[]}
export interface ComparisonPoint {time:number;close:number|null;pct:number|null}
export interface ComparisonSeries {asset:ComparisonAsset;points:ComparisonPoint[];error:string}
export interface ComparisonResult {requestId:string;weeks:number;baselineTime:number;lastClosedTime:number;asOf:number;series:ComparisonSeries[];warnings:string[]}
export interface ComparisonBackend {
 GetComparisonView():Promise<ComparisonView>;
 SaveComparisonView(view:ComparisonView):Promise<ComparisonView>;
 GetComparisonCatalog():Promise<ComparisonCatalog>;
 GetComparisonDailyChanges(requestId:string,view:ComparisonView):Promise<ComparisonResult>;
 CancelComparisonRequest(requestId:string):Promise<void>;
}
export const defaultComparisonView=():ComparisonView=>({weeks:4,assets:['BTC','ETH','TON','HYPE','ZEN','APT','NEAR']});
export const copyComparisonView=(value:ComparisonView):ComparisonView=>({weeks:value.weeks,assets:[...value.assets]});
export const comparisonDate=(time:number)=>new Intl.DateTimeFormat('en-GB',{timeZone:'UTC',day:'2-digit',month:'short',year:'numeric'}).format(new Date(time*1000));
export const comparisonPercent=(value:number|null|undefined)=>value===null||value===undefined||!Number.isFinite(value)?'—':new Intl.NumberFormat('en-US',{minimumFractionDigits:2,maximumFractionDigits:2,signDisplay:'exceptZero'}).format(Math.abs(value)<.005?0:value)+'%';
export const comparisonSource=(asset:ComparisonAsset)=>asset.exchange?`${asset.exchange==='hyperliquid'?'Hyperliquid':'Binance'} · ${asset.market==='perpetual'?'Perpetual':'Spot'} · ${asset.quoteAsset}`:'Unavailable';
export const comparisonLabel=(asset:ComparisonAsset)=>asset.displaySymbol||asset.symbol;
const colors=['#4c91ff','#50b878','#b884e9','#e99b38','#df6680','#29aeb4','#8f91ed','#bb8d50','#5eaf81','#c579b5'];
export const comparisonColor=(symbol:string)=>{
 const preferred=defaultComparisonView().assets.indexOf(symbol);
 if(preferred>=0)return colors[preferred];
 let hash=0;for(const char of symbol)hash=(hash*31+char.charCodeAt(0))>>>0;
 return colors[hash%colors.length];
};
/** LWC connects over whitespace; hide the outgoing segment before each gap. */
export const comparisonLineData=(points:ComparisonPoint[],color:string):Array<LineData<UTCTimestamp>|WhitespaceData<UTCTimestamp>>=>points.map((point,index)=>{
 if(point.pct===null)return{time:point.time as UTCTimestamp};
 const next=points[index+1],gap=next&&(next.pct===null||next.time-point.time>86400);
 return{time:point.time as UTCTimestamp,value:point.pct,color:gap?'rgba(0, 0, 0, 0)':color};
});

/** One ordered/coalescing writer; a failed edit stays pending for Retry/close. */
export class ComparisonViewWriter {
 private pending?:ComparisonView;private flight?:Promise<void>;
 constructor(private persist:(view:ComparisonView)=>Promise<ComparisonView>,private onError:(error:unknown)=>void){}
 edit(view:ComparisonView){this.pending=copyComparisonView(view);void this.flush().catch(this.onError);}
 async flush():Promise<void>{
  if(this.flight){await this.flight;if(this.pending)return this.flush();return;}
  const next=this.pending;if(!next)return;this.pending=undefined;
  this.flight=(async()=>{try{await this.persist(next);}catch(error){if(!this.pending)this.pending=next;throw error;}finally{this.flight=undefined;}})();
  await this.flight;if(this.pending)return this.flush();
 }
}

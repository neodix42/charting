import type {FeedKey} from '../types';
import type {LineData,UTCTimestamp,WhitespaceData} from 'lightweight-charts';

export interface ComparisonView {weeks:number;assets:string[]}
export interface ComparisonAsset {id?:string;symbol:string;displaySymbol?:string;name:string;exchange:string;market:string;nativeId:string;quoteAsset:string;feedKey?:FeedKey}
export interface ComparisonCatalog {assets:ComparisonAsset[];markets?:ComparisonAsset[];autoError?:string;warnings:string[]}
export interface ComparisonPoint {time:number;close:number|null;pct:number|null}
export interface ComparisonSeries {asset:ComparisonAsset;points:ComparisonPoint[];error:string}
export interface ComparisonResult {requestId:string;weeks:number;baselineTime:number;lastClosedTime:number;asOf:number;series:ComparisonSeries[];warnings:string[]}
export interface ComparisonOverview {requestId:string;panels:ComparisonResult[]}
export const COMPARISON_OVERVIEW_WEEKS=[2,4,8,12] as const;
export interface ComparisonBackend {
 GetComparisonView():Promise<ComparisonView>;
 SaveComparisonView(view:ComparisonView):Promise<ComparisonView>;
 GetComparisonCatalog():Promise<ComparisonCatalog>;
 GetComparisonDailyChanges(requestId:string,view:ComparisonView):Promise<ComparisonResult>;
 GetComparisonDailyChangesAt(requestId:string,view:ComparisonView,endTime:number):Promise<ComparisonResult>;
 GetComparisonOverview(requestId:string,view:ComparisonView):Promise<ComparisonOverview>;
 GetComparisonOverviewAt(requestId:string,view:ComparisonView,endTime:number):Promise<ComparisonOverview>;
 GetComparisonSnapshotDirectory():Promise<string>;
 ConfigureComparisonSnapshotDirectory(directory:string):Promise<string>;
 ChooseComparisonSnapshotDirectory():Promise<string>;
 ExportComparisonSnapshots(panels:ComparisonResult[]):Promise<string[]>;
 CancelComparisonRequest(requestId:string):Promise<void>;
}
export const defaultComparisonView=():ComparisonView=>({weeks:4,assets:['BTC','ETH','TON','HYPE','ZEN','APT','NEAR']});
export const copyComparisonView=(value:ComparisonView):ComparisonView=>({weeks:value.weeks,assets:[...value.assets]});
export const comparisonDate=(time:number)=>new Intl.DateTimeFormat('en-GB',{timeZone:'UTC',day:'2-digit',month:'short',year:'numeric'}).format(new Date(time*1000));
export const comparisonPercent=(value:number|null|undefined)=>value===null||value===undefined||!Number.isFinite(value)?'—':new Intl.NumberFormat('en-US',{minimumFractionDigits:2,maximumFractionDigits:2,signDisplay:'exceptZero'}).format(Math.abs(value)<.005?0:value)+'%';
/** Prices retain the selected market's quote; no rebasing or conversion. */
const priceFormat=new Intl.NumberFormat('en-US',{maximumSignificantDigits:21});
export const comparisonPrice=(value:number|null|undefined,quoteAsset:string)=>typeof value!=='number'||!Number.isFinite(value)||value<=0?'—':priceFormat.format(value)+(quoteAsset?` ${quoteAsset}`:'');
export const comparisonExchange=(exchange:string)=>({hyperliquid:'Hyperliquid',binance:'Binance',bybit:'Bybit',coinbase:'Coinbase'}[exchange]??exchange);
export const comparisonIdentity=(asset:ComparisonAsset)=>asset.id||asset.symbol;
export const comparisonFeedIdentity=(asset:ComparisonAsset)=>{
 const exchange=asset.feedKey?.exchange||asset.exchange,market=asset.feedKey?.market||asset.market,nativeId=asset.feedKey?.instrument||asset.nativeId;
 return exchange&&market&&nativeId?JSON.stringify([exchange,market,nativeId]):undefined;
};

/** Display saved selections while discovery is pending; never infer a quote/feed. */
export const comparisonSelectionAsset=(id:string):ComparisonAsset|undefined=>{
 const parts=id.split('|');
 if(parts.length!==3||id.length>512||!['hyperliquid','binance','bybit','coinbase'].includes(parts[0])||!['spot','perpetual'].includes(parts[1]))return;
 try{
  const nativeId=decodeURIComponent(parts[2].replace(/\+/g,' '));
  if(!nativeId||/[\u0000-\u001f\u007f]/.test(nativeId))return;
  return{id,symbol:nativeId,name:nativeId,exchange:parts[0],market:parts[1],nativeId,quoteAsset:''};
 }catch{return;}
};
export const comparisonSelectionLabel=(id:string)=>comparisonSelectionAsset(id)?.symbol??(id.includes('|')?'Selected market':id);

export const comparisonSource=(asset:ComparisonAsset)=>asset.exchange?[comparisonExchange(asset.exchange),asset.market==='perpetual'?'Perpetual':'Spot',asset.quoteAsset].filter(Boolean).join(' · '):'Unavailable';
export const comparisonLabel=(asset:ComparisonAsset)=>asset.displaySymbol||(asset.symbol.includes('|')?comparisonSelectionLabel(asset.symbol):asset.symbol);
export const comparisonOptionName=(asset:ComparisonAsset)=>[comparisonLabel(asset),comparisonExchange(asset.exchange),asset.market==='perpetual'?'Perpetual':'Spot',asset.quoteAsset].filter(Boolean).join(' ');
export const comparisonSeriesLabel=(asset:ComparisonAsset,peers:ComparisonAsset[])=>{
 const same=peers.filter(peer=>peer.symbol===asset.symbol);
 if(same.length<2)return comparisonLabel(asset);
 const source=comparisonSource(asset),duplicate=same.filter(peer=>comparisonSource(peer)===source).length>1;
 return `${comparisonLabel(asset)} · ${source}${duplicate?` · ${asset.nativeId}`:''}`;
};
const colors=['#4c91ff','#50b878','#b884e9','#e99b38','#df6680','#29aeb4','#8f91ed','#bb8d50','#5eaf81','#c579b5'];
export const comparisonColor=(symbol:string)=>{
 const preferred=defaultComparisonView().assets.indexOf(symbol);
 if(preferred>=0)return colors[preferred];
 let hash=0;for(const char of symbol)hash=(hash*31+char.charCodeAt(0))>>>0;
 return symbol.includes('|')?`hsl(${hash%360}, 62%, 52%)`:colors[hash%colors.length];
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

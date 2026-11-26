import type {FeedKey} from '../types';
import type {ComparisonAsset,ComparisonBackend,ComparisonCatalog,ComparisonResult,ComparisonView} from '../workspace/comparison';
import {comparisonDate,comparisonIdentity,copyComparisonView,defaultComparisonView} from '../workspace/comparison';
import {comparisonChartDiagnostics,comparisonChartResources} from '../components/ComparisonChart';

/** Explicit offline browser data only; never installed by the production bridge. */
export function comparisonBridge():ComparisonBackend {
 const key='cryptodesk-explicit-test-comparison-view';
 const names:Record<string,string>={BTC:'Bitcoin',ETH:'Ethereum',TON:'Gram (formerly Toncoin)',HYPE:'Hyperliquid',ZEN:'Horizen',APT:'Aptos',NEAR:'NEAR Protocol',SOL:'Solana',LTC:'Litecoin',DCR:'Decred',XAUT:'Tether Gold'};
 const assets:ComparisonAsset[]=Object.entries(names).map(([symbol,name])=>{
  const fallback=symbol==='ZEN'||symbol==='DCR'||symbol==='XAUT',exchange=fallback?'binance':'hyperliquid',market=fallback?'spot':'perpetual',nativeId=fallback?`${symbol}USDT`:symbol==='TON'?'GRAM':symbol,quoteAsset=fallback?'USDT':'USDC';
  return {id:symbol,symbol,...(symbol==='TON'?{displaySymbol:'TON (GRAM)'}:{}),name,exchange,market,nativeId,quoteAsset,feedKey:{exchange,market,instrument:nativeId,timeframe:'1d'}};
 });
 const marketID=(asset:ComparisonAsset)=>`${asset.exchange}|${asset.market}|${encodeURIComponent(asset.nativeId)}`;
 const markets:ComparisonAsset[]=[...assets.map(asset=>({...asset,id:marketID(asset)})),
  {symbol:'TON',displaySymbol:'TON (GRAM)',name:'Gram (formerly Toncoin)',exchange:'bybit',market:'spot',nativeId:'GRAMUSDT',quoteAsset:'USDT'},
  {symbol:'TON',displaySymbol:'TON (GRAM)',name:'Gram (formerly Toncoin)',exchange:'bybit',market:'perpetual',nativeId:'GRAMUSDT',quoteAsset:'USDT'},
  {symbol:'TON',name:'Toncoin',exchange:'coinbase',market:'spot',nativeId:'TON-USD',quoteAsset:'USD'},
 ].map(asset=>({...asset,id:marketID(asset),feedKey:{exchange:asset.exchange,market:asset.market,instrument:asset.nativeId,timeframe:'1d'} as FeedKey}));
 let view:ComparisonView=JSON.parse(sessionStorage.getItem(key)||'null')??defaultComparisonView();
 let errors:Partial<Record<'view'|'save'|'catalog'|'data',string>>={},catalog:ComparisonCatalog={assets,markets,warnings:[]};
 let overrides:Record<string,{error?:string;missingBaseline?:boolean;gaps?:number[]}>={};
 let saveGate:Promise<void>|undefined,releaseSave:(()=>void)|undefined,viewGate:Promise<void>|undefined,releaseView:(()=>void)|undefined,holdNext=0;
 const calls={view:0,save:0,catalog:0,data:0,cancel:0},writes:ComparisonView[]=[],requests:Array<{requestId:string;view:ComparisonView}>=[],cancelled:string[]=[];
 const pending=new Map<string,{result:ComparisonResult;resolve:(value:ComparisonResult)=>void}>();
 let lastResult:ComparisonResult|undefined;
 const validate=(value:ComparisonView)=>{
  if(!Number.isInteger(value.weeks)||value.weeks<1||value.weeks>104||!Array.isArray(value.assets)||value.assets.length>20||new Set(value.assets).size!==value.assets.length||value.assets.some(id=>!/^[A-Z0-9][A-Z0-9._-]{0,31}$/.test(id)&&!(/^(binance|bybit|coinbase|hyperliquid)\|(spot|perpetual)\|[^|]+$/.test(id)&&id.length<=512)))throw Error('Invalid Comparison choices');
 };
 const makeResult=(requestId:string,value:ComparisonView):ComparisonResult=>{
  const asOf=Math.floor(Date.now()/1000),day=86400,lastClosedTime=Math.floor(asOf/day)*day-day,baselineTime=lastClosedTime-value.weeks*7*day;
  const slopes=[1,-.5,.25,.75,-.3,.1,.4,.6,-.2,.15,.05];
  return {requestId,weeks:value.weeks,asOf,baselineTime,lastClosedTime,warnings:[],series:value.assets.map(id=>{
   const found=[...(catalog.markets??[]),...catalog.assets].find(asset=>comparisonIdentity(asset)===id),asset=found?{...found,id}:undefined,symbol=asset?.symbol??id,override=overrides[id]??{};
   if(!asset)return{asset:{id,symbol,name:symbol,exchange:'',market:'',nativeId:'',quoteAsset:''},error:'No matching asset is available in the verified provider catalog',points:[]};
   const missing=override.missingBaseline;
   const error=override.error||(missing?`The common UTC baseline candle is unavailable on ${comparisonDate(baselineTime)} UTC; first available daily close: 02 Jul 2026 UTC. This asset was not rebased; choose another source for longer history.`:'');
   const index=Math.max(0,assets.findIndex(item=>item.symbol===symbol)),base=1000*(index+1),slope=slopes[index]+(id.includes('|')&&symbol==='TON'?(asset.exchange==='coinbase'?.03:asset.exchange==='bybit'?.06:0):0);
   return {asset,error,points:error?[]:Array.from({length:value.weeks*7+1},(_,offset)=>{
    const time=baselineTime+offset*day,pct=offset===0?0:offset*slope;
    return override.gaps?.includes(offset)?{time,close:null,pct:null}:{time,close:base*(1+pct/100),pct};
   })};
  })};
 };
 Object.assign(window,{
  __testComparisonView:()=>copyComparisonView(view),__testComparisonCalls:()=>({...calls}),__testComparisonWrites:()=>structuredClone(writes),__testComparisonRequests:()=>structuredClone(requests),__testComparisonCancelled:()=>[...cancelled],__testComparisonLastResult:()=>structuredClone(lastResult),__testComparisonChartResources:()=>({...comparisonChartResources}),__testComparisonChart:(time?:number)=>comparisonChartDiagnostics.read(time),
  __testComparisonErrors:(value:typeof errors)=>{errors={...value};},
  __testComparisonSeries:(value:typeof overrides)=>{overrides=structuredClone(value);},
  __testComparisonCatalog:(value:ComparisonCatalog)=>{catalog=structuredClone(value);},
  __testComparisonHoldSaves:()=>{saveGate=new Promise(resolve=>{releaseSave=resolve;});},
  __testComparisonReleaseSaves:()=>{saveGate=undefined;releaseSave?.();releaseSave=undefined;},
  __testComparisonHoldView:()=>{viewGate=new Promise(resolve=>{releaseView=resolve;});},
  __testComparisonReleaseView:()=>{viewGate=undefined;releaseView?.();releaseView=undefined;},
  __testComparisonHoldNextData:()=>{holdNext++;},__testComparisonPending:()=>[...pending.keys()],
  // Resolve even after Cancel to model an already in-flight bridge response.
  __testComparisonReleaseData:(id:string,patch:Partial<ComparisonResult>={})=>{const item=pending.get(id);if(!item)throw Error('No held Comparison request');pending.delete(id);lastResult={...item.result,...structuredClone(patch)};item.resolve(structuredClone(lastResult));},
 });
 return {
  async GetComparisonView(){calls.view++;const value=copyComparisonView(view);if(viewGate)await viewGate;if(errors.view)throw Error(errors.view);return value;},
  async SaveComparisonView(value){calls.save++;validate(value);const next=copyComparisonView(value);if(saveGate)await saveGate;if(errors.save)throw Error(errors.save);view=next;writes.push(copyComparisonView(next));sessionStorage.setItem(key,JSON.stringify(view));return copyComparisonView(view);},
  async GetComparisonCatalog(){calls.catalog++;if(errors.catalog)throw Error(errors.catalog);return structuredClone(catalog);},
  async GetComparisonDailyChanges(requestId,value){
   calls.data++;validate(value);requests.push({requestId,view:copyComparisonView(value)});if(errors.data)throw Error(errors.data);
   const result=makeResult(requestId,value);
   if(holdNext>0){holdNext--;return new Promise(resolve=>{pending.set(requestId,{result,resolve});});}
   lastResult=result;return structuredClone(result);
  },
  async CancelComparisonRequest(requestId){calls.cancel++;cancelled.push(requestId);},
 };
}

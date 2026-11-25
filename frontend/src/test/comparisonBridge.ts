import type {ComparisonAsset,ComparisonBackend,ComparisonCatalog,ComparisonResult,ComparisonView} from '../workspace/comparison';
import {copyComparisonView,defaultComparisonView} from '../workspace/comparison';
import {comparisonChartDiagnostics,comparisonChartResources} from '../components/ComparisonChart';

/** Explicit offline browser data only; never installed by the production bridge. */
export function comparisonBridge():ComparisonBackend {
 const key='cryptodesk-explicit-test-comparison-view';
 const names:Record<string,string>={BTC:'Bitcoin',ETH:'Ethereum',TON:'Gram (formerly Toncoin)',HYPE:'Hyperliquid',ZEN:'Horizen',APT:'Aptos',NEAR:'NEAR Protocol',SOL:'Solana',LTC:'Litecoin'};
 const assets:ComparisonAsset[]=Object.entries(names).map(([symbol,name])=>{
  const fallback=symbol==='ZEN',exchange=fallback?'binance':'hyperliquid',market=fallback?'spot':'perpetual',nativeId=fallback?'ZENUSDT':symbol==='TON'?'GRAM':symbol,quoteAsset=fallback?'USDT':'USDC';
  return {symbol,...(symbol==='TON'?{displaySymbol:'TON (GRAM)'}:{}),name,exchange,market,nativeId,quoteAsset,feedKey:{exchange,market,instrument:nativeId,timeframe:'1d'}};
 });
 let view:ComparisonView=JSON.parse(sessionStorage.getItem(key)||'null')??defaultComparisonView();
 let errors:Partial<Record<'view'|'save'|'catalog'|'data',string>>={},catalog:ComparisonCatalog={assets,warnings:[]};
 let overrides:Record<string,{error?:string;missingBaseline?:boolean;gaps?:number[]}>={};
 let saveGate:Promise<void>|undefined,releaseSave:(()=>void)|undefined,viewGate:Promise<void>|undefined,releaseView:(()=>void)|undefined,holdNext=0;
 const calls={view:0,save:0,catalog:0,data:0,cancel:0},writes:ComparisonView[]=[],requests:Array<{requestId:string;view:ComparisonView}>=[],cancelled:string[]=[];
 const pending=new Map<string,{result:ComparisonResult;resolve:(value:ComparisonResult)=>void}>();
 let lastResult:ComparisonResult|undefined;
 const validate=(value:ComparisonView)=>{
  if(!Number.isInteger(value.weeks)||value.weeks<1||value.weeks>104||!Array.isArray(value.assets)||value.assets.length>20||new Set(value.assets).size!==value.assets.length||value.assets.some(symbol=>!/^[A-Z0-9][A-Z0-9._-]{0,31}$/.test(symbol)))throw Error('Invalid Comparison choices');
 };
 const makeResult=(requestId:string,value:ComparisonView):ComparisonResult=>{
  const asOf=Math.floor(Date.now()/1000),day=86400,lastClosedTime=Math.floor(asOf/day)*day-day,baselineTime=lastClosedTime-value.weeks*7*day;
  const slopes=[1,-.5,.25,.75,-.3,.1,.4,.6,-.2];
  return {requestId,weeks:value.weeks,asOf,baselineTime,lastClosedTime,warnings:[],series:value.assets.map(symbol=>{
   const asset=catalog.assets.find(asset=>asset.symbol===symbol),override=overrides[symbol]??{};
   if(!asset)return{asset:{symbol,name:symbol,exchange:'',market:'',nativeId:'',quoteAsset:''},error:'No matching asset is available in the verified provider catalog',points:[]};
   const error=override.error||(override.missingBaseline?'The common UTC baseline candle is unavailable; this asset was not rebased':'');
   const index=Math.max(0,assets.findIndex(item=>item.symbol===symbol)),base=1000*(index+1);
   return {asset,error,points:error?[]:Array.from({length:value.weeks*7+1},(_,offset)=>{
    const time=baselineTime+offset*day,pct=offset===0?0:offset*slopes[index];
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

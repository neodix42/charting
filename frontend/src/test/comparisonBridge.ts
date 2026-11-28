import type {FeedKey} from '../types';
import type {ComparisonAsset,ComparisonBackend,ComparisonCatalog,ComparisonResult,ComparisonOverview,ComparisonView} from '../workspace/comparison';
import {comparisonDate,comparisonIdentity,copyComparisonView,defaultComparisonView} from '../workspace/comparison';
import {comparisonChartDiagnostics,comparisonChartResources} from '../components/ComparisonChart';

/** Explicit offline browser data only; never installed by the production bridge. */
export function comparisonBridge():ComparisonBackend {
 const key='cryptodesk-explicit-test-comparison-view';
 const initialLastClosedTime=Math.floor(Date.now()/86400000)*86400-86400,masterAnchor=initialLastClosedTime-84*86400;
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
 let errors:Partial<Record<'view'|'save'|'catalog'|'data'|'snapshot',string>>={},catalog:ComparisonCatalog={assets,markets,warnings:[]};
 let overrides:Record<string,{error?:string;missingBaseline?:boolean;missingBaselineWeeks?:number[];gaps?:number[];closes?:Record<number,number|null>}>={};
 let saveGate:Promise<void>|undefined,releaseSave:(()=>void)|undefined,viewGate:Promise<void>|undefined,releaseView:(()=>void)|undefined,holdNext=0,holdNextOverview=0;
 const calls={view:0,save:0,catalog:0,data:0,cancel:0},writes:ComparisonView[]=[],requests:Array<{requestId:string;view:ComparisonView;endTime:number}>=[],overviewRequests:Array<{requestId:string;view:ComparisonView;endTime:number}>=[],cancelled:string[]=[];
 let overviewCalls=0;
 const pending=new Map<string,{result:ComparisonResult;resolve:(value:ComparisonResult)=>void}>();
 const pendingOverview=new Map<string,{result:ComparisonOverview;resolve:(value:ComparisonOverview)=>void}>();
 let lastResult:ComparisonResult|undefined,lastOverview:ComparisonOverview|undefined;
 const validate=(value:ComparisonView)=>{
  if(!Number.isInteger(value.weeks)||value.weeks<1||value.weeks>104||!Array.isArray(value.assets)||value.assets.length>20||new Set(value.assets).size!==value.assets.length||value.assets.some(id=>!/^[A-Z0-9][A-Z0-9._-]{0,31}$/.test(id)&&!(/^(binance|bybit|coinbase|hyperliquid)\|(spot|perpetual)\|[^|]+$/.test(id)&&id.length<=512)))throw Error('Invalid Comparison choices');
 };
 const makeResult=(requestId:string,value:ComparisonView,master=false,endTime=0):ComparisonResult=>{
  const asOf=Math.floor(Date.now()/1000),day=86400,lastClosedTime=endTime||Math.floor(asOf/day)*day-day,baselineTime=lastClosedTime-value.weeks*7*day;
  const slopes=[1,-.5,.25,.75,-.3,.1,.4,.6,-.2,.15,.05];
  return {requestId,weeks:value.weeks,asOf,baselineTime,lastClosedTime,warnings:[],series:value.assets.map(id=>{
   const found=[...(catalog.markets??[]),...catalog.assets].find(asset=>comparisonIdentity(asset)===id),asset=found?{...found,id}:undefined,symbol=asset?.symbol??id,override=overrides[id]??{};
   if(!asset)return{asset:{id,symbol,name:symbol,exchange:'',market:'',nativeId:'',quoteAsset:''},error:'No matching asset is available in the verified provider catalog',points:[]};
   const missing=!master&&(override.missingBaseline||override.missingBaselineWeeks?.includes(value.weeks));
   const error=override.error||(missing?`The common UTC baseline candle is unavailable on ${comparisonDate(baselineTime)} UTC; first available daily close: 02 Jul 2026 UTC. This asset was not rebased; choose another source for longer history.`:'');
   const index=Math.max(0,assets.findIndex(item=>item.symbol===symbol)),base=1000*(index+1),slope=slopes[index]+(id.includes('|')&&symbol==='TON'?(asset.exchange==='coinbase'?.03:asset.exchange==='bybit'?.06:0):0);
   const sourceAnchor=master?masterAnchor:initialLastClosedTime-value.weeks*7*day,baselineOffset=(baselineTime-sourceAnchor)/day;
   const closeAt=(time:number)=>{const sharedOffset=(time-masterAnchor)/day;if(override.closes&&Object.hasOwn(override.closes,sharedOffset))return override.closes[sharedOffset];const offset=(time-sourceAnchor)/day;return master?base+base*offset*slope/100:base*(1+offset*slope/100);};
   return {asset,error,points:error?[]:Array.from({length:value.weeks*7+1},(_,offset)=>{
    const time=baselineTime+offset*day,observedOffset=(time-sourceAnchor)/day,close=closeAt(time),baselineClose=closeAt(baselineTime),pct=close===null||baselineClose===null||!Number.isFinite(baselineClose)||baselineClose<=0?null:offset===0?0:baselineOffset===0&&!override.closes?offset*slope:(close/baselineClose-1)*100;
    const gapOffset=master?observedOffset:offset;
    return override.gaps?.includes(gapOffset)||master&&override.missingBaselineWeeks?.some(weeks=>gapOffset===(12-weeks)*7)?{time,close:null,pct:null}:{time,close,pct};
   })};
  })};
 };
 // One master history supplies every panel's identical real daily closes.
 // Only the strict UTC baseline and resulting percentages change per period.
 const makeOverview=(requestId:string,value:ComparisonView,endTime=0):ComparisonOverview=>{
  const master=makeResult(requestId,{weeks:12,assets:value.assets},true,endTime);
  return{requestId,panels:[2,4,8,12].map(weeks=>{
   const baselineTime=master.lastClosedTime-weeks*7*86400;
   return{...master,weeks,baselineTime,series:master.series.map(series=>{
    const points=series.points.filter(point=>point.time>=baselineTime),baseline=points.find(point=>point.time===baselineTime)?.close,override=overrides[comparisonIdentity(series.asset)]??{};
    const missing=!series.error&&(override.missingBaseline||baseline===null||baseline===undefined||!Number.isFinite(baseline)||baseline<=0);
    const error=series.error||(missing?`The common UTC baseline candle is unavailable on ${comparisonDate(baselineTime)} UTC. This asset was not rebased; choose another source for longer history.`:'');
    return{...series,error,points:error?[]:points.map(point=>({...point,pct:point.close===null?null:point.time===baselineTime?0:(point.close/baseline!-1)*100}))};
   })};
  })};
 };
 const validateEnd=(endTime:number)=>{const latest=Math.floor(Date.now()/86400000)*86400-86400;if(!Number.isSafeInteger(endTime)||endTime<0||endTime!==0&&(endTime%86400!==0||endTime>latest))throw Error('Choose a fully closed UTC day');};
 const getDaily=async(requestId:string,value:ComparisonView,endTime=0)=>{
  calls.data++;validate(value);validateEnd(endTime);requests.push({requestId,view:copyComparisonView(value),endTime});if(errors.data)throw Error(errors.data);
  const result=makeResult(requestId,value,false,endTime);
  if(holdNext>0){holdNext--;return new Promise<ComparisonResult>(resolve=>{pending.set(requestId,{result,resolve});});}
  lastResult=result;return structuredClone(result);
 };
 const getOverview=async(requestId:string,value:ComparisonView,endTime=0)=>{
  overviewCalls++;validate(value);validateEnd(endTime);overviewRequests.push({requestId,view:copyComparisonView(value),endTime});if(errors.data)throw Error(errors.data);
  const result=makeOverview(requestId,value,endTime);
  if(holdNextOverview>0){holdNextOverview--;return new Promise<ComparisonOverview>(resolve=>{pendingOverview.set(requestId,{result,resolve});});}
  lastOverview=result;return structuredClone(result);
 };
 Object.assign(window,{
  __testComparisonView:()=>copyComparisonView(view),__testComparisonCalls:()=>({...calls}),__testComparisonWrites:()=>structuredClone(writes),__testComparisonRequests:()=>structuredClone(requests),__testComparisonCancelled:()=>[...cancelled],__testComparisonLastResult:()=>structuredClone(lastResult),__testComparisonLastOverview:()=>structuredClone(lastOverview),__testComparisonChartResources:()=>({...comparisonChartResources}),__testComparisonChart:(time?:number,id?:string)=>comparisonChartDiagnostics.read(time,id),__testComparisonCharts:(time?:number)=>comparisonChartDiagnostics.readAll(time),
  __testComparisonOverviewCalls:()=>overviewCalls,__testComparisonOverviewRequests:()=>structuredClone(overviewRequests),
  __testComparisonErrors:(value:typeof errors)=>{errors={...value};},
  __testComparisonSeries:(value:typeof overrides)=>{overrides=structuredClone(value);},
  __testComparisonCatalog:(value:ComparisonCatalog)=>{catalog=structuredClone(value);},
  __testComparisonHoldSaves:()=>{saveGate=new Promise(resolve=>{releaseSave=resolve;});},
  __testComparisonReleaseSaves:()=>{saveGate=undefined;releaseSave?.();releaseSave=undefined;},
  __testComparisonHoldView:()=>{viewGate=new Promise(resolve=>{releaseView=resolve;});},
  __testComparisonReleaseView:()=>{viewGate=undefined;releaseView?.();releaseView=undefined;},
  __testComparisonHoldNextData:()=>{holdNext++;},__testComparisonPending:()=>[...pending.keys()],
  __testComparisonHoldNextOverview:()=>{holdNextOverview++;},__testComparisonPendingOverview:()=>[...pendingOverview.keys()],
  // Resolve even after Cancel to model an already in-flight bridge response.
  __testComparisonReleaseData:(id:string,patch:Partial<ComparisonResult>={})=>{const item=pending.get(id);if(!item)throw Error('No held Comparison request');pending.delete(id);lastResult={...item.result,...structuredClone(patch)};item.resolve(structuredClone(lastResult));},
  __testComparisonReleaseOverview:(id:string,patch:Partial<ComparisonOverview>={})=>{const item=pendingOverview.get(id);if(!item)throw Error('No held Comparison overview request');pendingOverview.delete(id);lastOverview={...item.result,...structuredClone(patch)};item.resolve(structuredClone(lastOverview));},
 });
 let snapshotDirectory=sessionStorage.getItem('comparison-snapshot-directory')||'/explicit-test/snapshots';
 return {
  async GetComparisonSnapshotDirectory(){return snapshotDirectory;},
  async ConfigureComparisonSnapshotDirectory(directory){snapshotDirectory=directory.trim()||'/explicit-test/snapshots';sessionStorage.setItem('comparison-snapshot-directory',snapshotDirectory);return snapshotDirectory;},
  async ChooseComparisonSnapshotDirectory(){throw Error('Native folder dialog unavailable in explicit browser test mode');},
  async ExportComparisonSnapshots(panels){if(errors.snapshot)throw Error(errors.snapshot);(window as any).__testComparisonSnapshot=structuredClone(panels);return panels.map(panel=>`${snapshotDirectory}/${new Date(panel.baselineTime*1000).toISOString().slice(0,10)}_w${panel.weeks}.txt`);},
  async GetComparisonView(){calls.view++;const value=copyComparisonView(view);if(viewGate)await viewGate;if(errors.view)throw Error(errors.view);return value;},
  async SaveComparisonView(value){calls.save++;validate(value);const next=copyComparisonView(value);if(saveGate)await saveGate;if(errors.save)throw Error(errors.save);view=next;writes.push(copyComparisonView(next));sessionStorage.setItem(key,JSON.stringify(view));return copyComparisonView(view);},
  async GetComparisonCatalog(){calls.catalog++;if(errors.catalog)throw Error(errors.catalog);return structuredClone(catalog);},
  GetComparisonDailyChanges:getDaily,
  GetComparisonDailyChangesAt:getDaily,
  GetComparisonOverview:getOverview,
  GetComparisonOverviewAt:getOverview,
  async CancelComparisonRequest(requestId){calls.cancel++;cancelled.push(requestId);},
 };
}

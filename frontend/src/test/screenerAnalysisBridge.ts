import type {AnalysisCatalog,AnalysisChart,AnalysisFile,AnalysisInfo,AnalysisRequest,AnalysisResult,ScreenerAnalysisBackend} from '../workspace/screenerAnalysis';
import {initialAnalysisView} from '../workspace/screenerAnalysis';
import {analysisChartResources} from '../components/ScreenerAnalysisChart';

/** Installed only by VITE_EXPLICIT_TEST_BRIDGE. No production data fallback. */
export function screenerAnalysisBridge(directory:()=>string):ScreenerAnalysisBackend{
 const viewKey='cryptodesk-explicit-test-analysis-view';
 let view:AnalysisRequest=JSON.parse(sessionStorage.getItem(viewKey)||'null')??initialAnalysisView();
 let catalogOverride:Partial<AnalysisCatalog>={},request:AnalysisRequest|null=null,reads=0,chartDelay=0;
 let errors:Partial<Record<'catalog'|'view'|'save'|'start'|'status'|'chart',string>>={};
 const saves:AnalysisRequest[]=[],chartRequests:Array<{resultID:string;occurrence:number;before:number;after:number}>=[];
 let info:AnalysisInfo={active:false,phase:'idle',total:0,completed:0,failed:0,currentFile:'',progress:0,rows:0,totalMatches:0,matchedFiles:0,results:[],offset:0,limit:50,totalResults:0};
 let charts:Record<string,AnalysisChart>={},saveGate:Promise<void>|undefined,releaseSave:(()=>void)|undefined;
 const files=()=>((window as unknown as {__testEnrichmentFiles?:()=>AnalysisFile[]}).__testEnrichmentFiles?.()??[]);
 const catalog=():AnalysisCatalog=>({directory:directory(),files:files(),fields:[...new Set(['price','close',...files().flatMap(file=>file.fields.map(field=>field.toLowerCase()))])].filter(field=>field!=='timestamp'),timeframes:[...new Set(files().map(file=>file.timeframe).filter(Boolean))],...structuredClone(catalogOverride)});
 const page=(offset:number,limit:number):AnalysisInfo=>{
  const groups=new Map<string,{matches:number;files:number}>();
  info.results.forEach(result=>{const key=result.symbol||result.name,current=groups.get(key)??{matches:0,files:0};current.matches+=result.matches;if(result.matches)current.files++;groups.set(key,current);});
  return structuredClone({...info,offset,limit,totalResults:info.results.length,results:info.results.slice(offset,offset+limit).map(result=>({...result,assetMatches:groups.get(result.symbol||result.name)?.matches??0,assetFiles:groups.get(result.symbol||result.name)?.files??0}))});
 };
 const chart=(result:AnalysisResult,occurrence:number):AnalysisChart=>{
  const fixture=charts[`${result.id}:${occurrence}`];if(fixture)return structuredClone(fixture);
  const time=(result.firstTime??Date.UTC(2024,0,5))+occurrence*86400000,index=20+occurrence;
  const fields=['open','high','low','close','volume',...(catalog().fields.filter(field=>!['open','high','low','close','price','volume'].includes(field.toLowerCase())))];
  return{resultId:result.id,name:result.name,symbol:result.symbol,timeframe:result.timeframe,occurrence,totalMatches:result.matches,matchTime:time,matchIndex:index,startIndex:index-8,fields,bars:Array.from({length:17},(_,i)=>({index:index-8+i,time:time+(i-8)*86400000,values:Object.fromEntries(fields.map(field=>[field,field==='open'?100+i:field==='high'?105+i:field==='low'?96+i:field==='close'?103+i:field==='volume'?1000+i:/histogram/.test(field)?(i-8)/10:50+i/2]))}))};
 };
 const api:ScreenerAnalysisBackend={
  async GetScreenerAnalysisCatalog(){if(errors.catalog)throw Error(errors.catalog);return catalog();},
  async GetScreenerAnalysisView(){if(errors.view)throw Error(errors.view);return structuredClone(view);},
  async SaveScreenerAnalysisView(value){saves.push(structuredClone(value));if(saveGate)await saveGate;if(errors.save)throw Error(errors.save);view=structuredClone(value);sessionStorage.setItem(viewKey,JSON.stringify(view));return structuredClone(view);},
  async StartScreenerAnalysis(value){if(errors.start)throw Error(errors.start);request=structuredClone(value);info={...info,active:true,phase:'indexing',total:files().length,completed:0,failed:0,currentFile:'',progress:0,rows:0,totalMatches:0,matchedFiles:0,results:[],totalResults:0};return page(0,50);},
  async GetScreenerAnalysis(offset,limit){reads++;if(errors.status)throw Error(errors.status);return page(offset,limit);},
  async CancelScreenerAnalysis(){info={...info,active:false,phase:'cancelled',currentFile:''};return page(0,50);},
  async GetScreenerAnalysisChart(resultID,occurrence,before,after){
   chartRequests.push({resultID,occurrence,before,after});if(errors.chart)throw Error(errors.chart);
   const result=info.results.find(result=>result.id===resultID);if(!result||occurrence<0||occurrence>=result.matches)throw Error('Matching occurrence is unavailable.');
   const response=chart(result,occurrence),delay=chartDelay;if(delay)await new Promise(resolve=>setTimeout(resolve,delay));return response;
  },
 };
 Object.assign(window,{
  __testSetAnalysisCatalog:(value:Partial<AnalysisCatalog>)=>{catalogOverride=structuredClone(value);},
  __testSetAnalysisInfo:(value:Partial<AnalysisInfo>)=>{info={...info,...structuredClone(value)};},
  __testAnalysisInfo:()=>structuredClone(info),__testAnalysisRequest:()=>structuredClone(request),__testAnalysisReads:()=>reads,
  __testAnalysisView:()=>structuredClone(view),__testAnalysisSaves:()=>structuredClone(saves),
  __testSetAnalysisView:(value:AnalysisRequest)=>{view=structuredClone(value);sessionStorage.setItem(viewKey,JSON.stringify(view));},
  __testAnalysisErrors:(value:typeof errors)=>{errors=structuredClone(value);},
  __testSetAnalysisCharts:(value:Record<string,AnalysisChart>)=>{charts=structuredClone(value);},
  __testAnalysisChartDelay:(value:number)=>{chartDelay=value;},__testAnalysisChartRequests:()=>structuredClone(chartRequests),
  __testAnalysisChartResources:()=>({...analysisChartResources}),
  __testHoldAnalysisSaves:()=>{saveGate=new Promise(resolve=>{releaseSave=resolve;});},
  __testReleaseAnalysisSaves:()=>{releaseSave?.();saveGate=undefined;},
 });
 return api;
}

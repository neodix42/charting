import type {AnalysisCatalog,AnalysisChart,AnalysisFile,AnalysisInfo,AnalysisRequest,AnalysisResult,AnalysisResultFilter,SavedAnalysisCondition,ScreenerAnalysisBackend} from '../workspace/screenerAnalysis';
import type {ScreenerCatalog} from '../types';
import {initialAnalysisView} from '../workspace/screenerAnalysis';
import {analysisChartResources} from '../components/ScreenerAnalysisChart';

/** Installed only by VITE_EXPLICIT_TEST_BRIDGE. No production data fallback. */
export function screenerAnalysisBridge(directory:()=>string,instrumentCatalog:()=>ScreenerCatalog):ScreenerAnalysisBackend{
 const viewKey='cryptodesk-explicit-test-analysis-view';
 let view:AnalysisRequest=JSON.parse(sessionStorage.getItem(viewKey)||'null')??initialAnalysisView();
 const savedKey='cryptodesk-explicit-test-saved-conditions';
 let savedConditions:SavedAnalysisCondition[]=JSON.parse(sessionStorage.getItem(savedKey)||'[]');
 let catalogOverride:Partial<AnalysisCatalog>={},request:AnalysisRequest|null=null,reads=0,chartDelay=0;
 let errors:Partial<Record<'catalog'|'view'|'save'|'start'|'status'|'chart'|'savedList'|'savedWrite'|'savedRename'|'savedDelete',string>>={};
 const saves:AnalysisRequest[]=[],chartRequests:Array<{resultID:string;occurrence:number;before:number;after:number}>=[];
 let info:AnalysisInfo={active:false,phase:'idle',total:0,completed:0,failed:0,currentFile:'',progress:0,rows:0,totalMatches:0,matchedFiles:0,results:[],offset:0,limit:50,totalResults:0};
 let charts:Record<string,AnalysisChart>={},saveGate:Promise<void>|undefined,releaseSave:(()=>void)|undefined;
 const metadata=(symbol:string)=>{for(const group of instrumentCatalog().groups){const item=group.instruments.find(item=>item.id===symbol.toLowerCase());if(item)return{description:item.name,category:group.name};}return{description:'Description unavailable',category:'Other'};};
 const files=()=>((window as unknown as {__testEnrichmentFiles?:()=>AnalysisFile[]}).__testEnrichmentFiles?.()??[]).map(file=>({...metadata(file.symbol),...file}));
 const catalog=():AnalysisCatalog=>({directory:directory(),files:files(),fields:[...new Set(['price','close',...files().flatMap(file=>file.fields.map(field=>field.toLowerCase()))])].filter(field=>field!=='timestamp'),timeframes:[...new Set(files().map(file=>file.timeframe).filter(Boolean))],categories:[...new Set([...instrumentCatalog().groups.map(group=>group.name),...files().map(file=>file.category)])],...structuredClone(catalogOverride)});
 const page=(offset:number,limit:number,filter?:AnalysisResultFilter):AnalysisInfo=>{
  const groups=new Map<string,{matches:number;files:number}>();
  info.results.forEach(result=>{const key=result.symbol||result.name,current=groups.get(key)??{matches:0,files:0};current.matches+=result.matches;if(result.matches)current.files++;groups.set(key,current);});
  const results=info.results.map(result=>({...metadata(result.symbol),...result})).filter(result=>!filter||((filter.categories==null||filter.categories.includes(result.category))&&(filter.includeEmpty||result.matches>0||!!result.error||result.status==='failed'||result.status==='cancelled')));
  if(filter?.profitableSort){
   const percentage=(result:AnalysisResult)=>result.matches&&result.simulation&&result.simulation.unavailable!==result.matches&&Number.isFinite(result.simulation.profitablePercent)?result.simulation.profitablePercent:null;
   results.sort((left,right)=>{const a=percentage(left),b=percentage(right);return a===null?b===null?0:1:b===null?-1:filter.profitableSort==='desc'?b-a:a-b;});
  }
  return structuredClone({...info,offset,limit,totalResults:results.length,results:results.slice(offset,offset+limit).map(result=>({...result,assetMatches:groups.get(result.symbol||result.name)?.matches??0,assetFiles:groups.get(result.symbol||result.name)?.files??0}))});
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
  async GetScreenerSavedConditions(){if(errors.savedList)throw Error(errors.savedList);return structuredClone(savedConditions);},
  async SaveScreenerAnalysisCondition(name,value){
   if(errors.savedWrite)throw Error(errors.savedWrite);
   name=name.trim();if(!name)throw Error('Enter a condition name.');
   if(savedConditions.some(item=>item.name.toLowerCase()===name.toLowerCase()))throw Error('A condition with this name already exists; choose another name.');
   const saved={id:crypto.randomUUID().replaceAll('-',''),name,request:structuredClone(value)};
   savedConditions.push(saved);sessionStorage.setItem(savedKey,JSON.stringify(savedConditions));return structuredClone(saved);
  },
  async DeleteScreenerAnalysisCondition(id){
   if(errors.savedDelete)throw Error(errors.savedDelete);
   if(!savedConditions.some(item=>item.id===id))throw Error('Saved condition not found.');
   savedConditions=savedConditions.filter(item=>item.id!==id);sessionStorage.setItem(savedKey,JSON.stringify(savedConditions));
  },
  async RenameScreenerAnalysisCondition(id,name){
   if(errors.savedRename)throw Error(errors.savedRename);
   name=name.trim();if(!name||[...name].length>100||/\p{Cc}/u.test(name))throw Error('Condition name must contain 1–100 characters without control characters.');
   const index=savedConditions.findIndex(item=>item.id===id);if(index<0)throw Error('Saved condition not found.');
   if(savedConditions.some(item=>item.id!==id&&item.name.toLowerCase()===name.toLowerCase()))throw Error('A condition with this name already exists; choose another name.');
   const renamed={...savedConditions[index],name};savedConditions[index]=renamed;
   sessionStorage.setItem(savedKey,JSON.stringify(savedConditions));return structuredClone(renamed);
  },
  async StartScreenerAnalysis(value){if(errors.start)throw Error(errors.start);request=structuredClone(value);info={...info,tradeSimulation:structuredClone(value.tradeSimulation??null),active:true,phase:'indexing',total:files().filter(file=>(value.categories==null||value.categories.includes(file.category))&&(!value.period||value.period==='all'||file.timeframe===value.period)).length,completed:0,failed:0,currentFile:'',progress:0,rows:0,totalMatches:0,matchedFiles:0,results:[],totalResults:0};return page(0,10);},
  async GetScreenerAnalysis(offset,limit){reads++;if(errors.status)throw Error(errors.status);return page(offset,limit);},
  async GetScreenerAnalysisFiltered(offset,limit,filter){reads++;if(errors.status)throw Error(errors.status);return page(offset,limit,filter);},
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
  __testSavedAnalysisConditions:()=>structuredClone(savedConditions),
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

import {forwardRef,useEffect,useImperativeHandle,useRef,useState} from 'react';
import {errorText} from '../types';
import type {AnalysisCatalog,AnalysisInfo,AnalysisRequest,ScreenerAnalysisBackend} from '../workspace/screenerAnalysis';
import {AnalysisViewWriter,analysisDate,analysisInputError,initialAnalysisView,newAnalysisCondition} from '../workspace/screenerAnalysis';
import {ScreenerAnalysisCondition} from './ScreenerAnalysisCondition';
import {ScreenerAnalysisChart,type AnalysisSelection} from './ScreenerAnalysisChart';
import {ScreenerSavedConditions,type ScreenerSavedConditionsHandle} from './ScreenerSavedConditions';
import {AnalysisSimulationCell,ScreenerTradeSimulation} from './ScreenerTradeSimulation';
import './ScreenerAnalysis.css';

const pageSize=10;
export interface ScreenerAnalysisHandle {flush():Promise<void>}
export const ScreenerAnalysis=forwardRef<ScreenerAnalysisHandle,{api:ScreenerAnalysisBackend;visible:boolean}>(function ScreenerAnalysis({api,visible},ref){
 const [catalog,setCatalog]=useState<AnalysisCatalog|null>(null),[view,setView]=useState<AnalysisRequest>(initialAnalysisView),[ready,setReady]=useState(false),[info,setInfo]=useState<AnalysisInfo|null>(null);
 const [loadError,setLoadError]=useState(''),[catalogError,setCatalogError]=useState(''),[saveError,setSaveError]=useState(''),[scanError,setScanError]=useState(''),[actionError,setActionError]=useState('');
 const [page,setPage]=useState(0),[reload,setReload]=useState(0),[busy,setBusy]=useState(false),[resultCategory,setResultCategory]=useState(''),[selection,setSelection]=useState<AnalysisSelection|null>(null);
 const [simulationInputError,setSimulationInputError]=useState('');
 const [profitableSort,setProfitableSort]=useState<''|'asc'|'desc'>('');
 const draft=useRef(view),mounted=useRef(false),lifetime=useRef(0),epoch=useRef(0),actionBusy=useRef(false),selectionRef=useRef(selection),chartEpoch=useRef(0),writer=useRef<AnalysisViewWriter|null>(null);
 const panelVisible=useRef(visible);panelVisible.current=visible;
 const savedConditions=useRef<ScreenerSavedConditionsHandle>(null);
 selectionRef.current=selection;
 if(!writer.current)writer.current=new AnalysisViewWriter(async value=>{const saved=await api.SaveScreenerAnalysisView(value);if(mounted.current)setSaveError('');return saved;},error=>{if(mounted.current)setSaveError(errorText(error));});
 useImperativeHandle(ref,()=>({flush:async()=>{await writer.current!.flush();await savedConditions.current?.flush();}}),[]);
 useEffect(()=>{mounted.current=true;lifetime.current++;return ()=>{mounted.current=false;lifetime.current++;chartEpoch.current++;};},[]);
 useEffect(()=>{
  if(!visible||ready)return;
  let current=true;
  void api.GetScreenerAnalysisView().then(value=>{if(current){draft.current=value;setView(value);setReady(true);setLoadError('');}}).catch(error=>{if(current)setLoadError(errorText(error));});
  return ()=>{current=false;};
 },[api,visible,ready,reload]);
 useEffect(()=>{
  if(!visible)return;
  let current=true;
  void api.GetScreenerAnalysisCatalog().then(value=>{if(current){setCatalog(value);setCatalogError('');}}).catch(error=>{if(current)setCatalogError(errorText(error));});
  return ()=>{current=false;};
 },[api,visible,reload]);
 useEffect(()=>{
  if(!visible)return;
  let current=true,timer:ReturnType<typeof setTimeout>|undefined;
  const poll=async()=>{
   const revision=epoch.current;
   try{
    if(!actionBusy.current){const value=await api.GetScreenerAnalysisFiltered(page*pageSize,pageSize,{categories:resultCategory?[resultCategory]:null,includeEmpty:false,profitableSort});if(current&&revision===epoch.current){setInfo(value);setScanError('');if(page>0&&page*pageSize>=value.totalResults)setPage(Math.max(0,Math.ceil(value.totalResults/pageSize)-1));}}
   }catch(error){if(current&&revision===epoch.current)setScanError(errorText(error));}
   finally{if(current)timer=setTimeout(()=>void poll(),750);}
  };
  void poll();return ()=>{current=false;if(timer)clearTimeout(timer);};
 },[api,visible,page,reload,resultCategory,profitableSort]);
 const change=(next:AnalysisRequest)=>{draft.current=next;setView(next);setSaveError('');writer.current!.edit(next);};
 const closeChart=()=>{chartEpoch.current++;selectionRef.current=null;setSelection(null);};
 useEffect(()=>{if(!visible)closeChart();},[visible]);
 const run=async(operation:()=>Promise<AnalysisInfo>,apply=false)=>{
  if(actionBusy.current)return;
  const session=lifetime.current;actionBusy.current=true;epoch.current++;setBusy(true);setActionError('');
  try{
   if(apply){await writer.current!.flush();closeChart();}
   await operation();const value=await api.GetScreenerAnalysisFiltered(apply?0:page*pageSize,pageSize,{categories:resultCategory?[resultCategory]:null,includeEmpty:false,profitableSort});if(mounted.current&&session===lifetime.current){setInfo(value);setScanError('');if(apply)setPage(0);setReload(previous=>previous+1);}
  }catch(error){if(mounted.current&&session===lifetime.current)setActionError(errorText(error));}
  finally{actionBusy.current=false;if(mounted.current&&session===lifetime.current)setBusy(false);}
 };
 const navigate=async(direction:-1|1)=>{
  const selected=selectionRef.current;if(!selected||!panelVisible.current)return;
  const revision=chartEpoch.current,occurrence=selected.occurrence+direction;
  if(occurrence>=0&&occurrence<selected.result.matches){if(revision===chartEpoch.current)setSelection({...selected,occurrence});return;}
  let index=selected.offset+direction;
  while(index>=0){
   const offset=Math.floor(index/pageSize)*pageSize,chunk=await api.GetScreenerAnalysisFiltered(offset,pageSize,{categories:resultCategory?[resultCategory]:null,includeEmpty:false,profitableSort});
   if(revision!==chartEpoch.current||!mounted.current||!panelVisible.current)return;
   if(index>=chunk.totalResults)break;
   for(;index>=offset&&index<offset+chunk.results.length;index+=direction){
    const result=chunk.results[index-offset];
    if(result.matches>0&&!result.error){setSelection({result,offset:index,occurrence:direction===1?0:result.matches-1});return;}
   }
   if(direction===1&&offset+chunk.results.length>=chunk.totalResults)break;
   if(!chunk.results.length)break;
  }
  throw new Error(direction===1?'This is the last matching occurrence.':'This is the first matching occurrence.');
 };
 const categories=catalog?.categories??[],selectedCategories=new Set(view.categories??categories);
 const selectedFiles=catalog?.files.filter(file=>selectedCategories.has(file.category||'Other')).length??0;
 const changeCategories=(next:string[]|null)=>change({...draft.current,categories:next});
 const resetResults=()=>{epoch.current++;closeChart();setPage(0);setInfo(previous=>previous?{...previous,results:[],totalResults:0}:null);};
 const changeResultFilter=(category:string)=>{resetResults();setResultCategory(category);};
 const changeResultSort=()=>{resetResults();setProfitableSort(value=>value==='desc'?'asc':'desc');};
 const results=info?.results??[],groups:Array<{asset:string;entries:Array<{result:typeof results[number];offset:number}>}>=[];
 // Group only adjacent files so multiple timeframes never override percentage order.
 results.forEach((result,index)=>{const asset=result.symbol||result.name;let group=groups[groups.length-1];if(!group||group.asset!==asset){group={asset,entries:[]};groups.push(group);}group.entries.push({result,offset:(info?.offset??page*pageSize)+index});});
 const working=info?.active??false,invalid=(view.tradeSimulation&&simulationInputError)||analysisInputError(view),error=loadError||catalogError||saveError||actionError||scanError||info?.error;
 const resultSimulation=info?.tradeSimulation;
 return <div className="screener-analysis-layout"><div className="screener-analysis-content">
  <section className="analysis-builder" aria-label="Visual condition builder">
   <header><div><span className="analysis-eyebrow">HISTORICAL SCREENER</span><h2>Visual condition rows</h2><p>All rows must match (AND). Every closed bar in the selected downloaded CSV files is checked.</p></div><span className="analysis-and-badge">ALL CONDITIONS · AND</span></header>
   {error&&<div className="analysis-error" role="alert">{error}<button type="button" onClick={()=>{if(saveError)void writer.current!.flush().then(()=>{setSaveError('');setActionError('');}).catch(error=>setSaveError(errorText(error)));else{setActionError('');setReload(value=>value+1);}}}>{saveError?'Retry saving rules':'Retry analysis'}</button></div>}
   <form aria-label="Analysis conditions" onSubmit={event=>{event.preventDefault();if(ready&&selectedFiles&&!working&&!busy&&!invalid){const request=structuredClone(draft.current);void run(()=>api.StartScreenerAnalysis(request),true);}}}>
    <fieldset className="analysis-rules" disabled={!ready||working||busy}>
     {view.conditions.map((condition,index)=><ScreenerAnalysisCondition key={condition.id} condition={condition} index={index} catalog={catalog} onChange={value=>change({...draft.current,conditions:draft.current.conditions.map(item=>item.id===value.id?value:item)})} onRemove={()=>change({...draft.current,conditions:draft.current.conditions.filter(item=>item.id!==condition.id)})}/>)}
     <div className="analysis-add-actions"><button type="button" className="analysis-add" disabled={view.conditions.length>=32} title={view.conditions.length>=32?'Maximum 32 conditions':'Add another AND condition'} onClick={()=>change({...draft.current,conditions:[...draft.current.conditions,newAnalysisCondition()]})}>＋ Add condition</button><button type="button" className="analysis-add" aria-expanded={!!view.tradeSimulation} disabled={!!view.tradeSimulation} onClick={()=>{setSimulationInputError('');change({...draft.current,tradeSimulation:{stopLoss:'',takeProfit:'',duration:20}});}}>Simulate trade</button></div>
     {view.tradeSimulation&&<ScreenerTradeSimulation value={view.tradeSimulation} catalog={catalog} onInputError={setSimulationInputError} onChange={tradeSimulation=>change({...draft.current,tradeSimulation})} onRemove={()=>{setSimulationInputError('');change({...draft.current,tradeSimulation:null});}}/>}
    </fieldset>
    <div className="analysis-apply-row"><button type="submit" className="primary" disabled={!ready||!selectedFiles||working||busy||!!invalid}>{busy&&!working?'Starting scan…':'Apply conditions'}</button>{working&&<button type="button" className="danger" disabled={busy} onClick={()=>void run(()=>api.CancelScreenerAnalysis())}>Cancel analysis</button>}<button type="button" disabled={busy||working} onClick={()=>setReload(value=>value+1)}>Refresh fields</button><span>{invalid|| (catalog?`${selectedFiles.toLocaleString('en-US')} of ${catalog.files.length.toLocaleString('en-US')} files in selected categories`:'Loading downloaded fields…')}</span></div>
    <fieldset className="analysis-scan-categories" disabled={!ready||!catalog||working||busy}><legend>Asset categories to scan</legend><div className="analysis-category-actions"><button type="button" aria-label="Select all scan categories" onClick={()=>changeCategories(null)}>Select all</button><button type="button" aria-label="Deselect all scan categories" onClick={()=>changeCategories([])}>Deselect all</button><span>{selectedCategories.size} / {categories.length} categories selected</span></div><div className="analysis-category-options">{categories.map(category=><label key={category}><input type="checkbox" aria-label={`Scan ${category}`} checked={selectedCategories.has(category)} onChange={event=>{const next=new Set(draft.current.categories??categories);if(event.target.checked)next.add(category);else next.delete(category);changeCategories(categories.filter(item=>next.has(item)));}}/><span>{category}</span></label>)}</div></fieldset>
   </form>
   <div className="analysis-help"><p>Both sides can use a price, an enriched indicator, a constant or an earlier candle. “Price” / “Close” uses close, then bid or ask when the file contains ticks.</p><p>Timeframes use only companion bars that have fully closed. Temporal windows count the evaluated file’s closed bars. Highest / lowest compares the left value with the preceding N right-side values, excluding the aligned current bar.</p><p>Missing fields, missing companion files and invalid data are reported per file. Blank indicator warm-up values cannot create a match.</p>{catalog&&<small>{catalog.directory}</small>}</div>
  </section>
  <section className="analysis-results" aria-label="Analysis results">
   <header><div><h2>Matching assets</h2><p>{info?.totalMatches?`${info.totalMatches.toLocaleString('en-US')} occurrences in ${info.matchedFiles.toLocaleString('en-US')} files`:'Apply conditions to find historical matches.'}</p></div><div className="analysis-result-filters"><label>Asset category<select aria-label="Result asset category" disabled={busy} value={resultCategory} onChange={event=>changeResultFilter(event.target.value)}><option value="">All categories</option>{categories.map(category=><option key={category} value={category}>{category}</option>)}</select></label></div></header>
   {resultSimulation&&<p className="analysis-simulation-summary">Simulated long trades · SL: {resultSimulation.stopLoss} ({resultSimulation.stopLossBarsAgo??0} bars ago) · TP: {resultSimulation.takeProfitMultiple?`${resultSimulation.takeProfitMultiple}xSL`:resultSimulation.takeProfit} · {resultSimulation.duration.toLocaleString('en-US')} bars. Profitable, % = TP hits ÷ all matching bars × 100.</p>}
   {info&&(working||info.total>0)&&<div className="analysis-scan-status" role="status"><div><strong>{working?(info.phase==='indexing'?'Indexing downloaded files':'Scanning closed bars'):info.phase==='cancelled'?'Analysis cancelled':'Analysis complete'}</strong><span>{info.completed} / {info.total} files · {info.rows.toLocaleString('en-US')} rows · {info.failed} failed</span></div><progress aria-label="Analysis scan progress" max={100} value={Math.max(0,Math.min(100,info.progress))}/>{info.currentFile&&<p>{info.currentFile}</p>}</div>}
   {!catalog?.files.length&&catalog&&<p className="analysis-empty">No downloaded files. Download historical data, then enrich it with any indicators you want to compare.</p>}
   {!groups.length&&!!info?.total&&<p className="analysis-empty">{working?'Waiting for matching files…':resultCategory?'No matching files in this asset category.':info.totalMatches?'No files match the current result filters.':'No closed bars matched all conditions.'}</p>}
   {!!groups.length&&<div className="analysis-table-scroll"><table><thead><tr><th>Asset / downloaded file</th><th>Timeframe</th><th>Matches</th>{resultSimulation&&<th aria-label="Profitable, %" aria-sort={profitableSort==='asc'?'ascending':profitableSort==='desc'?'descending':'none'}><button type="button" className="analysis-sort" aria-label="Sort by Profitable, %" disabled={busy} onClick={changeResultSort}>Profitable, % <span aria-hidden="true">{profitableSort==='asc'?'▲':profitableSort==='desc'?'▼':'↕'}</span></button></th>}<th>First match (UTC)</th><th>Last match (UTC)</th></tr></thead><tbody>{groups.map(({asset,entries})=>[
    <tr className="analysis-asset" key={`asset:${asset}:${entries[0].result.id}`}><th colSpan={resultSimulation?6:5}><strong>{asset.toUpperCase()}</strong><span>{entries[0].result.description||catalog?.files.find(file=>file.id===entries[0].result.fileId)?.description||'Description unavailable'}</span></th></tr>,
    ...entries.map(({result,offset})=><tr key={result.id} className={result.error?'analysis-file-failed':result.matches?'analysis-file-match':''} tabIndex={result.matches&&!result.error?0:undefined} aria-label={`${result.symbol.toUpperCase()||result.name} ${result.timeframe} · ${result.matches} matches`} onClick={()=>{if(result.matches&&!result.error){chartEpoch.current++;setSelection({result,offset,occurrence:0});}}} onKeyDown={event=>{if((event.key==='Enter'||event.key===' ')&&event.target===event.currentTarget&&result.matches&&!result.error){event.preventDefault();chartEpoch.current++;setSelection({result,offset,occurrence:0});}}}><td><strong>{result.name}</strong><small>{result.rows.toLocaleString('en-US')} closed rows checked</small>{result.error&&<p role="alert">{result.error}</p>}</td><td>{result.timeframe||'Unknown'}</td><td>{result.matches&&!result.error?<button type="button" aria-label={`Inspect ${result.name} matches`}>{result.matches.toLocaleString('en-US')} ↗</button>:result.error?'Failed':'0'}</td>{resultSimulation&&<AnalysisSimulationCell result={result}/>}<td>{analysisDate(result.firstTime)}</td><td>{analysisDate(result.lastTime)}</td></tr>),
   ])}</tbody></table></div>}
   {!!info?.totalResults&&<nav className="analysis-pagination" aria-label="Analysis file pages"><button type="button" disabled={page===0} onClick={()=>setPage(value=>value-1)}>Previous files</button><span>Files {page*pageSize+1}–{Math.min((page+1)*pageSize,info.totalResults)} of {info.totalResults}</span><button type="button" disabled={(page+1)*pageSize>=info.totalResults} onClick={()=>setPage(value=>value+1)}>Next files</button></nav>}
  </section>
  </div>
  <ScreenerSavedConditions ref={savedConditions} api={api} visible={visible} disabled={!ready||working||busy} canSave={!invalid} request={view} onLoad={request=>{closeChart();change(request);setActionError('');}}/>
  {visible&&selection&&<ScreenerAnalysisChart api={api} selection={selection} onClose={closeChart} onNavigate={navigate}/>}
 </div>;
});

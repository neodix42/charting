import {forwardRef,useEffect,useImperativeHandle,useRef,useState} from 'react';
import type {Backend,ScreenerCatalog,ScreenerDataSource,ScreenerInfo,ScreenerView} from '../types';
import {errorText} from '../types';
import {initialScreenerView,normalizeScreenerView,ScreenerViewWriter} from '../workspace/screenerView';
import {ScreenerEnrichment} from './ScreenerEnrichment';
import {ScreenerAnalysis,type ScreenerAnalysisHandle} from './ScreenerAnalysis';
import {ScreenerLiveAlerts,type ScreenerLiveAlertsHandle} from './ScreenerLiveAlerts';
import type {SavedConditionRename} from '../workspace/screenerAnalysis';
import './ScreenerDownloads.css';

const downloadPageSize=10;
type DownloadFilter='all'|'queued'|'running'|'completed'|'failed'|'cancelled';

function Selection({label,checked,partial,disabled,onChange}:{label:string;checked:boolean;partial:boolean;disabled?:boolean;onChange(value:boolean):void}){
 const input=useRef<HTMLInputElement>(null);
 useEffect(()=>{if(input.current)input.current.indeterminate=partial;},[partial]);
 return <label className="screener-selection"><input ref={input} type="checkbox" aria-label={label} checked={checked} disabled={disabled} onChange={event=>onChange(event.target.checked)}/><span>{label}</span></label>;
}

export interface ScreenerHandle {flush():Promise<void>}
export const Screener=forwardRef<ScreenerHandle,{api:Backend;visible:boolean}>(function Screener({api,visible},ref){
 const [tab,setTab]=useState<'analysis'|'downloader'|'enrichment'|'live-alerts'>('analysis'),[catalog,setCatalog]=useState<ScreenerCatalog|null>(null),[info,setInfo]=useState<ScreenerInfo|null>(null);
 const [view,setView]=useState<ScreenerView>(initialScreenerView),[viewReady,setViewReady]=useState(false),[viewError,setViewError]=useState(''),[viewReload,setViewReload]=useState(0),[query,setQuery]=useState('');
 const draft=useRef(view),writer=useRef<ScreenerViewWriter|null>(null);
 const analysis=useRef<ScreenerAnalysisHandle>(null);
 const liveAlerts=useRef<ScreenerLiveAlertsHandle>(null);
 const [renamedCondition,setRenamedCondition]=useState<SavedConditionRename>();
 const conditionRenamed=async(rename:SavedConditionRename)=>{
  setRenamedCondition(rename);
  try{await liveAlerts.current?.conditionRenamed(rename);}catch(error){throw Error(`Condition was renamed, but Live Alerts could not be updated: ${errorText(error)}`);}
 };
 const dataSource=view.dataSource??'dukascopy',isHyperliquid=dataSource==='hyperliquid',sourceName=isHyperliquid?'Hyperliquid':'Dukascopy';
 const selected=new Set(isHyperliquid?view.hyperliquidSymbols??[]:view.symbols),collapsed=new Set(isHyperliquid?[]:view.collapsedCategories),{timeframe,from,to}=view;
 const [catalogError,setCatalogError]=useState(''),[infoError,setInfoError]=useState(''),[actionError,setActionError]=useState(''),[busy,setBusy]=useState(false),[retry,setRetry]=useState(0);
 const [downloadFilter,setDownloadFilter]=useState<DownloadFilter>('all'),[downloadPage,setDownloadPage]=useState(0);
 const alive=useRef(false),lifetime=useRef(0),epoch=useRef(0),actionBusy=useRef(false);
 if(!writer.current)writer.current=new ScreenerViewWriter(value=>api.SaveScreenerView(value),error=>{if(alive.current)setViewError(errorText(error));});
 useImperativeHandle(ref,()=>({flush:async()=>{await writer.current!.flush();await analysis.current?.flush();await liveAlerts.current?.flush();}}),[]);
 useEffect(()=>{alive.current=true;lifetime.current++;return ()=>{alive.current=false;lifetime.current++;};},[]);
 useEffect(()=>{if(!visible||viewReady)return;let active=true;void Promise.resolve().then(()=>api.GetScreenerView()).then(value=>{if(active){const next=normalizeScreenerView(value);draft.current=next;setView(next);setViewReady(true);setViewError('');}}).catch(error=>{if(active)setViewError(errorText(error));});return ()=>{active=false;};},[api,visible,viewReady,viewReload]);
 const changeView=(patch:Partial<ScreenerView>)=>{const next={...draft.current,...patch};draft.current=next;setView(next);setViewError('');writer.current!.edit(next);};
 useEffect(()=>{
  if(!visible||!viewReady)return;
  let active=true;setCatalog(null);setCatalogError('');
  void (isHyperliquid?api.GetScreenerCatalogForSource(dataSource):api.GetScreenerCatalog()).then(value=>{if(active){setCatalog(value);setCatalogError('');}}).catch(error=>{if(active)setCatalogError(errorText(error));});
  return ()=>{active=false;};
 },[api,visible,retry,viewReady,dataSource]);
 useEffect(()=>{
  if(!visible)return;
  let active=true,timer:ReturnType<typeof setTimeout>|undefined;
  const read=async()=>{
   const version=epoch.current;
   try{if(!actionBusy.current){const value=await api.GetScreener();if(active&&version===epoch.current){setInfo(value);setInfoError('');}}}
   catch(error){if(active&&version===epoch.current)setInfoError(errorText(error));}
   finally{if(active)timer=setTimeout(()=>void read(),750);}
  };
  void read();return ()=>{active=false;if(timer)clearTimeout(timer);};
 },[api,visible,retry]);
 const instruments=catalog?.groups.flatMap(group=>group.instruments)??[],working=info?.jobs.some(job=>job.status==='queued'||job.status==='running')??false;
 const downloaded=new Set(info?.downloadedSymbols??[]),downloadedIDs=instruments.filter(instrument=>downloaded.has(instrument.id)).map(instrument=>instrument.id);
 const selectedDownloaded=downloadedIDs.filter(id=>selected.has(id)).length,onlyDownloaded=downloadedIDs.length>0&&selectedDownloaded===downloadedIDs.length&&selected.size===downloadedIDs.length;
 const downloads=info?.jobs??[],filteredDownloads=downloads.filter(job=>downloadFilter==='all'||job.status===downloadFilter);
 const lastDownloadPage=Math.max(0,Math.ceil(filteredDownloads.length/downloadPageSize)-1),currentDownloadPage=Math.min(downloadPage,lastDownloadPage);
 const visibleDownloads=filteredDownloads.slice(currentDownloadPage*downloadPageSize,(currentDownloadPage+1)*downloadPageSize);
 useEffect(()=>{setDownloadPage(page=>Math.min(page,lastDownloadPage));},[lastDownloadPage]);
 const selectSymbols=(symbols:string[])=>changeView(isHyperliquid?{hyperliquidSymbols:symbols,hyperliquidSelectionInitialized:true}:{symbols});
 const changeSelection=(ids:string[],checked:boolean)=>{const next=new Set(isHyperliquid?draft.current.hyperliquidSymbols??[]:draft.current.symbols);ids.forEach(id=>checked?next.add(id):next.delete(id));selectSymbols([...next]);};
 useEffect(()=>{if(viewReady&&isHyperliquid&&catalog&&!view.hyperliquidSelectionInitialized)changeView({hyperliquidSymbols:catalog.groups.flatMap(group=>group.instruments.map(instrument=>instrument.id)),hyperliquidSelectionInitialized:true});},[viewReady,isHyperliquid,catalog,view.hyperliquidSelectionInitialized]);
 const run=async(operation:()=>Promise<ScreenerInfo>)=>{
  if(actionBusy.current)return;
  const session=lifetime.current;actionBusy.current=true;epoch.current++;setBusy(true);setActionError('');
  try{const value=await operation();if(alive.current&&session===lifetime.current){setInfo(value);setInfoError('');}}
  catch(error){if(alive.current&&session===lifetime.current)setActionError(errorText(error));}
  finally{if(alive.current&&session===lifetime.current){actionBusy.current=false;setBusy(false);}}
 };
 const search=query.trim().toLowerCase();
 const invalidDates=!from||!to||from>=to;
 return <section id="screener-panel" className="screener" role="tabpanel" aria-label="Screener" hidden={!visible}>
  <div className="screener-heading"><div><h1>Screener</h1><p>Historical market data from Dukascopy and Hyperliquid</p></div><div role="tablist" aria-label="Screener sections" className="screener-subtabs"><button id="screener-analysis-tab" role="tab" aria-controls="screener-analysis" aria-selected={tab==='analysis'} onClick={()=>setTab('analysis')}>Analysis</button><button id="screener-downloader-tab" role="tab" aria-controls="screener-downloader" aria-selected={tab==='downloader'} onClick={()=>setTab('downloader')}>Downloader</button><button id="screener-enrichment-tab" role="tab" aria-controls="screener-enrichment" aria-selected={tab==='enrichment'} onClick={()=>setTab('enrichment')}>Enrichment</button><button id="screener-live-alerts-tab" role="tab" aria-controls="screener-live-alerts" aria-selected={tab==='live-alerts'} onClick={()=>setTab('live-alerts')}>Live Alerts</button></div></div>
  <div id="screener-analysis" role="tabpanel" aria-labelledby="screener-analysis-tab" hidden={tab!=='analysis'} className="screener-analysis"><ScreenerAnalysis ref={analysis} api={api} visible={visible&&tab==='analysis'} onConditionRenamed={conditionRenamed}/></div>
  <div id="screener-enrichment" role="tabpanel" aria-labelledby="screener-enrichment-tab" hidden={tab!=='enrichment'}><ScreenerEnrichment api={api} visible={visible&&tab==='enrichment'} timeframes={['tick','s1','m1','m5','m15','m30','h1','h4','d1','w1','mn1']}/></div>
  <div id="screener-live-alerts" role="tabpanel" aria-labelledby="screener-live-alerts-tab" hidden={tab!=='live-alerts'}><ScreenerLiveAlerts ref={liveAlerts} api={api} visible={visible&&tab==='live-alerts'} renamedCondition={renamedCondition}/></div>
  <div id="screener-downloader" role="tabpanel" aria-labelledby="screener-downloader-tab" hidden={tab!=='downloader'}>
   {(catalogError||infoError||actionError||viewError||info?.error||info&&!info.runtimeAvailable&&!isHyperliquid)&&<div className="screener-error" role="alert">{actionError||catalogError||infoError||viewError||info?.error||(!isHyperliquid?info?.runtimeError:'')||'The Dukascopy downloader runtime is unavailable.'}{viewError&&<button type="button" onClick={()=>{if(viewReady)void writer.current!.flush().then(()=>setViewError('')).catch(error=>setViewError(errorText(error)));else setViewReload(value=>value+1);}}>Retry saving Screener choices</button>}{(catalogError||infoError||(!info?.runtimeAvailable&&!isHyperliquid))&&<button type="button" onClick={()=>setRetry(value=>value+1)}>Retry downloader</button>}</div>}
   <form className="screener-download-controls" aria-label="Download historical data" onSubmit={event=>{event.preventDefault();if(viewReady&&selected.size&&!invalidDates&&!working&&(isHyperliquid||info?.runtimeAvailable)&&!busy)void run(async()=>{await writer.current!.flush();const settings=await api.GetScreener();return api.StartScreenerDownload({...(isHyperliquid?{dataSource}:{}),symbols:instruments.filter(instrument=>selected.has(instrument.id)).map(instrument=>instrument.id),timeframe,from,to,concurrency:settings.concurrency});});}}>
    <label>Data source<select aria-label="Download data source" value={dataSource} disabled={!viewReady||working||busy} onChange={event=>{const next=event.target.value as ScreenerDataSource;setCatalog(null);setQuery('');changeView({dataSource:next,...(next==='hyperliquid'&&!['d1','w1'].includes(timeframe)?{timeframe:'d1'}:{})});}}><option value="dukascopy">Dukascopy</option><option value="hyperliquid">Hyperliquid</option></select></label>
    <label>Timeframe<select aria-label="Download timeframe" value={timeframe} disabled={!viewReady||!catalog||working||busy} onChange={event=>changeView({timeframe:event.target.value})}>{(catalog?.timeframes??['d1']).map(value=><option key={value} value={value}>{value}</option>)}</select></label>
    <label>From (UTC)<input type="date" aria-label="Download from date" value={from} max={to||undefined} required disabled={!viewReady||working||busy} onChange={event=>changeView({from:event.target.value})}/></label>
    <label>To (UTC, exclusive)<input type="date" aria-label="Download to date" value={to} min={from||undefined} required disabled={!viewReady||working||busy} onChange={event=>changeView({to:event.target.value})}/></label>
    <button type="submit" className="primary" disabled={!viewReady||!selected.size||invalidDates||working||busy||(!isHyperliquid&&!info?.runtimeAvailable)||!catalog}>{busy&&!working?'Starting…':`Download ${selected.size||''} ${selected.size===1?'symbol':'symbols'}`}</button>
    {working&&<button type="button" className="danger" disabled={busy} onClick={()=>void run(()=>api.CancelScreenerDownload())}>Cancel downloads</button>}
    {!info?.active&&(isHyperliquid||info?.runtimeAvailable)&&info?.jobs.some(job=>job.status==='failed'||job.status==='queued')&&<button type="button" disabled={busy} onClick={()=>void run(()=>api.ResumeScreenerDownload())}>Resume downloads</button>}
    <small className="screener-download-directory">CSV destination: <span>{info?.directory||'Loading…'}</span><span>Change the folder in Settings.</span></small>
    <small className="screener-download-note">Existing histories are reused by data source, symbol and timeframe. Download fetches only missing earlier or later ranges, preserves saved data, and recalculates applied indicators. Already covered ranges need no download.</small>
    {isHyperliquid&&<small className="screener-download-note">Hyperliquid retains the latest 5,000 daily candles. If From precedes an asset’s available history, its earliest available daily bar is used. Files start with hyperliquid; spot and perpetual markets stay separate.</small>}
    {timeframe==='w1'&&<small className="screener-download-note">Weekly bars start Monday at 00:00 UTC. Existing daily bars are reused; missing daily data is downloaded and kept alongside the weekly CSV.</small>}
    {invalidDates&&<small className="screener-date-error">Choose a start date before the end date.</small>}
   </form>
   <div className="screener-columns">
    <section className="screener-catalog" aria-label={`${sourceName} instruments`}><div className="screener-catalog-toolbar"><Selection label="Select all symbols" checked={instruments.length>0&&selected.size===instruments.length} partial={selected.size>0&&selected.size<instruments.length} disabled={!viewReady||!catalog||working||busy} onChange={checked=>changeSelection(instruments.map(instrument=>instrument.id),checked)}/><Selection label="Select all downloaded assets" checked={onlyDownloaded} partial={!onlyDownloaded&&selectedDownloaded>0} disabled={!viewReady||!catalog||!info||!downloadedIDs.length||working||busy} onChange={checked=>checked?selectSymbols(downloadedIDs):changeSelection(downloadedIDs,false)}/><span className="muted" aria-label="Selected symbol count">{selected.size} / {instruments.length} selected</span><input type="search" aria-label={`Search ${sourceName} instruments`} placeholder="Search symbols or names…" value={query} onChange={event=>setQuery(event.target.value)}/></div>
     {!catalog&&!catalogError&&<p className="screener-loading" role="status">Loading supported instruments…</p>}
     {catalog?.groups.map(group=>{
      const matches=group.instruments.filter(instrument=>!search||`${group.name} ${instrument.id} ${instrument.nativeId??''} ${instrument.market??''} ${instrument.name}`.toLowerCase().includes(search));
      if(!matches.length)return null;
      const count=group.instruments.filter(instrument=>selected.has(instrument.id)).length;
      return <details className="screener-instrument-group" key={group.name} open={!collapsed.has(group.name)} onToggle={event=>{const closed=!event.currentTarget.open;const current=new Set(draft.current.collapsedCategories);if(!isHyperliquid&&viewReady&&current.has(group.name)!==closed){if(closed)current.add(group.name);else current.delete(group.name);changeView({collapsedCategories:[...current]});}}}><summary><span>{group.name}</span><span className="muted">{count} / {group.instruments.length}</span></summary><div className="screener-group-selection"><Selection label={`Select all ${group.name}`} checked={count===group.instruments.length} partial={count>0&&count<group.instruments.length} disabled={!viewReady||working||busy} onChange={checked=>changeSelection(group.instruments.map(instrument=>instrument.id),checked)}/></div><div className="screener-instruments">{matches.map(instrument=><label key={instrument.id} className="screener-instrument"><input type="checkbox" aria-label={`${(instrument.nativeId??instrument.id).toUpperCase()} — ${instrument.name}`} checked={selected.has(instrument.id)} disabled={!viewReady||working||busy} onChange={event=>changeSelection([instrument.id],event.target.checked)}/><span><strong>{instrument.nativeId??instrument.id.toUpperCase()}</strong><small>{instrument.name}</small>{instrument.earliestDate&&<small className="screener-earliest">History from {instrument.earliestDate}</small>}</span></label>)}</div></details>;
     })}
     {catalog&&!catalog.groups.some(group=>group.instruments.some(instrument=>!search||`${group.name} ${instrument.id} ${instrument.nativeId??''} ${instrument.market??''} ${instrument.name}`.toLowerCase().includes(search)))&&<p className="screener-loading">No instruments match your search.</p>}
     {catalog&&<p className="screener-catalog-version muted">{isHyperliquid?'Hyperliquid public API':'dukascopy-node'} {catalog.version} · {instruments.length} supported instruments</p>}
    </section>
    <section className="screener-jobs" aria-label="Download progress"><div className="screener-jobs-heading"><h2>Downloads</h2><span className="muted" aria-label="Download summary">{downloads.filter(job=>job.status==='completed').length} completed · {downloads.filter(job=>job.status==='failed').length} failed · {downloads.length} total · <span>{info?.concurrency??5} {(info?.concurrency??5)===1?'worker thread':'worker threads'}</span></span></div>
     <div className="screener-jobs-filter"><label>Show downloads<select aria-label="Download status filter" value={downloadFilter} onChange={event=>{setDownloadFilter(event.target.value as DownloadFilter);setDownloadPage(0);}}><option value="all">All statuses</option><option value="queued">Queued</option><option value="running">Running</option><option value="completed">Completed</option><option value="failed">Failed</option><option value="cancelled">Cancelled</option></select></label><span>{filteredDownloads.length} {filteredDownloads.length===1?'download':'downloads'}</span></div>
     {!info?.jobs.length&&<p className="screener-loading">Select symbols and a common timeframe, then download their CSV files.</p>}
     {!!downloads.length&&!filteredDownloads.length&&<p className="screener-loading">No {downloadFilter} downloads.</p>}
     <div aria-live="polite" aria-relevant="additions text">{visibleDownloads.map(job=><article className={`screener-job ${job.status}`} key={job.id} data-symbol={job.symbol} aria-label={`${job.symbol.toUpperCase()} download`}><div className="screener-job-title"><strong>{job.nativeId??job.symbol.toUpperCase()}</strong><span>{job.timeframe}</span><span className="screener-job-status">{job.status}</span></div><progress aria-label={`${job.symbol.toUpperCase()} download progress`} max={100} value={Math.min(100,Math.max(0,job.progress||0))}/><div className="screener-job-details"><span>{job.from} → {job.to}</span><span>{Math.round(job.progress||0)}%</span></div>{job.message&&<p>{job.message}</p>}{job.outputPath&&<small className="screener-output">{job.outputPath}</small>}</article>)}</div>
     {!!downloads.length&&<nav className="screener-download-pagination" aria-label="Download pages"><button type="button" disabled={currentDownloadPage===0} onClick={()=>setDownloadPage(currentDownloadPage-1)}>Previous downloads</button><span>{filteredDownloads.length?`${currentDownloadPage*downloadPageSize+1}–${Math.min((currentDownloadPage+1)*downloadPageSize,filteredDownloads.length)} of ${filteredDownloads.length}`:'0'} downloads</span><button type="button" disabled={currentDownloadPage>=lastDownloadPage} onClick={()=>setDownloadPage(currentDownloadPage+1)}>Next downloads</button></nav>}
    </section>
   </div>
  </div>
 </section>;
});

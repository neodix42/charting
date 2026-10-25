import {useEffect,useRef,useState} from 'react';
import type {Backend,ScreenerCatalog,ScreenerInfo} from '../types';
import {errorText} from '../types';

function Selection({label,checked,partial,disabled,onChange}:{label:string;checked:boolean;partial:boolean;disabled?:boolean;onChange(value:boolean):void}){
 const input=useRef<HTMLInputElement>(null);
 useEffect(()=>{if(input.current)input.current.indeterminate=partial;},[partial]);
 return <label className="screener-selection"><input ref={input} type="checkbox" aria-label={label} checked={checked} disabled={disabled} onChange={event=>onChange(event.target.checked)}/><span>{label}</span></label>;
}

export function Screener({api,visible}:{api:Backend;visible:boolean}){
 const [tab,setTab]=useState<'analysis'|'downloader'>('analysis'),[catalog,setCatalog]=useState<ScreenerCatalog|null>(null),[info,setInfo]=useState<ScreenerInfo|null>(null);
 const [selected,setSelected]=useState<Set<string>>(()=>new Set()),[query,setQuery]=useState(''),[timeframe,setTimeframe]=useState('d1'),[from,setFrom]=useState('2010-01-01'),[to,setTo]=useState(()=>new Date().toISOString().slice(0,10));
 const [catalogError,setCatalogError]=useState(''),[infoError,setInfoError]=useState(''),[actionError,setActionError]=useState(''),[busy,setBusy]=useState(false),[retry,setRetry]=useState(0);
 const alive=useRef(false),lifetime=useRef(0),epoch=useRef(0),actionBusy=useRef(false);
 useEffect(()=>{alive.current=true;lifetime.current++;return ()=>{alive.current=false;lifetime.current++;};},[]);
 useEffect(()=>{
  if(!visible)return;
  let active=true;
  void api.GetScreenerCatalog().then(value=>{if(active){setCatalog(value);setCatalogError('');setTimeframe(previous=>value.timeframes.includes(previous)?previous:value.timeframes[0]||'d1');}}).catch(error=>{if(active)setCatalogError(errorText(error));});
  return ()=>{active=false;};
 },[api,visible,retry]);
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
 const changeSelection=(ids:string[],checked:boolean)=>setSelected(previous=>{const next=new Set(previous);ids.forEach(id=>checked?next.add(id):next.delete(id));return next;});
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
  <div className="screener-heading"><div><h1>Screener</h1><p>Historical market data from Dukascopy</p></div><div role="tablist" aria-label="Screener sections" className="screener-subtabs"><button id="screener-analysis-tab" role="tab" aria-controls="screener-analysis" aria-selected={tab==='analysis'} onClick={()=>setTab('analysis')}>Analysis</button><button id="screener-downloader-tab" role="tab" aria-controls="screener-downloader" aria-selected={tab==='downloader'} onClick={()=>setTab('downloader')}>Downloader</button></div></div>
  <div id="screener-analysis" role="tabpanel" aria-labelledby="screener-analysis-tab" hidden={tab!=='analysis'} className="screener-analysis"/>
  <div id="screener-downloader" role="tabpanel" aria-labelledby="screener-downloader-tab" hidden={tab!=='downloader'}>
   {(catalogError||infoError||actionError||info?.error||info&&!info.runtimeAvailable)&&<div className="screener-error" role="alert">{actionError||catalogError||infoError||info?.error||info?.runtimeError||'The Dukascopy downloader runtime is unavailable.'}{(catalogError||infoError||!info?.runtimeAvailable)&&<button type="button" onClick={()=>setRetry(value=>value+1)}>Retry downloader</button>}</div>}
   <form className="screener-download-controls" aria-label="Download historical data" onSubmit={event=>{event.preventDefault();if(selected.size&&!invalidDates&&!working&&info?.runtimeAvailable&&!busy)void run(()=>api.StartScreenerDownload({symbols:instruments.filter(instrument=>selected.has(instrument.id)).map(instrument=>instrument.id),timeframe,from,to}));}}>
    <label>Timeframe<select aria-label="Download timeframe" value={timeframe} disabled={!catalog||working||busy} onChange={event=>setTimeframe(event.target.value)}>{(catalog?.timeframes??['d1']).map(value=><option key={value} value={value}>{value}</option>)}</select></label>
    <label>From (UTC)<input type="date" aria-label="Download from date" value={from} max={to||undefined} required disabled={working||busy} onChange={event=>setFrom(event.target.value)}/></label>
    <label>To (UTC, exclusive)<input type="date" aria-label="Download to date" value={to} min={from||undefined} required disabled={working||busy} onChange={event=>setTo(event.target.value)}/></label>
    <button type="submit" className="primary" disabled={!selected.size||invalidDates||working||busy||!info?.runtimeAvailable||!catalog}>{busy&&!working?'Starting…':`Download ${selected.size||''} ${selected.size===1?'symbol':'symbols'}`}</button>
    {working&&<button type="button" className="danger" disabled={busy} onClick={()=>void run(()=>api.CancelScreenerDownload())}>Cancel downloads</button>}
    <small className="screener-download-directory">CSV destination: <span>{info?.directory||'Loading…'}</span><span>Change the folder in Settings.</span></small>
    {invalidDates&&<small className="screener-date-error">Choose a start date before the end date.</small>}
   </form>
   <div className="screener-columns">
    <section className="screener-catalog" aria-label="Dukascopy instruments"><div className="screener-catalog-toolbar"><Selection label="Select all symbols" checked={instruments.length>0&&selected.size===instruments.length} partial={selected.size>0&&selected.size<instruments.length} disabled={!catalog||working||busy} onChange={checked=>changeSelection(instruments.map(instrument=>instrument.id),checked)}/><span className="muted" aria-label="Selected symbol count">{selected.size} / {instruments.length} selected</span><input type="search" aria-label="Search Dukascopy instruments" placeholder="Search symbols or names…" value={query} onChange={event=>setQuery(event.target.value)}/></div>
     {!catalog&&!catalogError&&<p className="screener-loading" role="status">Loading supported instruments…</p>}
     {catalog?.groups.map(group=>{
      const matches=group.instruments.filter(instrument=>!search||`${group.name} ${instrument.id} ${instrument.name}`.toLowerCase().includes(search));
      if(!matches.length)return null;
      const count=group.instruments.filter(instrument=>selected.has(instrument.id)).length;
      return <details className="screener-instrument-group" key={group.name} open><summary><span>{group.name}</span><span className="muted">{count} / {group.instruments.length}</span></summary><div className="screener-group-selection"><Selection label={`Select all ${group.name}`} checked={count===group.instruments.length} partial={count>0&&count<group.instruments.length} disabled={working||busy} onChange={checked=>changeSelection(group.instruments.map(instrument=>instrument.id),checked)}/></div><div className="screener-instruments">{matches.map(instrument=><label key={instrument.id} className="screener-instrument"><input type="checkbox" aria-label={`${instrument.id.toUpperCase()} — ${instrument.name}`} checked={selected.has(instrument.id)} disabled={working||busy} onChange={event=>changeSelection([instrument.id],event.target.checked)}/><span><strong>{instrument.id.toUpperCase()}</strong><small>{instrument.name}</small>{instrument.earliestDate&&<small className="screener-earliest">History from {instrument.earliestDate}</small>}</span></label>)}</div></details>;
     })}
     {catalog&&!catalog.groups.some(group=>group.instruments.some(instrument=>!search||`${group.name} ${instrument.id} ${instrument.name}`.toLowerCase().includes(search)))&&<p className="screener-loading">No instruments match your search.</p>}
     {catalog&&<p className="screener-catalog-version muted">dukascopy-node {catalog.version} · {instruments.length} supported instruments</p>}
    </section>
    <section className="screener-jobs" aria-label="Download progress"><div className="screener-jobs-heading"><h2>Downloads</h2><span className="muted">{info?.jobs.filter(job=>job.status==='completed').length??0} / {info?.jobs.length??0} complete</span></div>
     {!info?.jobs.length&&<p className="screener-loading">Select symbols and a common timeframe, then download their CSV files.</p>}
     <div aria-live="polite" aria-relevant="additions text">{info?.jobs.map(job=><article className={`screener-job ${job.status}`} key={job.id} data-symbol={job.symbol} aria-label={`${job.symbol.toUpperCase()} download`}><div className="screener-job-title"><strong>{job.symbol.toUpperCase()}</strong><span>{job.timeframe}</span><span className="screener-job-status">{job.status}</span></div><progress aria-label={`${job.symbol.toUpperCase()} download progress`} max={100} value={Math.min(100,Math.max(0,job.progress||0))}/><div className="screener-job-details"><span>{job.from} → {job.to}</span><span>{Math.round(job.progress||0)}%</span></div>{job.message&&<p>{job.message}</p>}{job.outputPath&&<small className="screener-output">{job.outputPath}</small>}</article>)}</div>
    </section>
   </div>
  </div>
 </section>;
}

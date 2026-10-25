import {useEffect,useRef,useState} from 'react';
import type {Backend,ScreenerEnrichmentInfo,ScreenerFilePage,ScreenerIndicators} from '../types';
import {errorText} from '../types';

const pageSize=50;
const requirements={price:'close, bidPrice or askPrice',hl:'high, low',hlc:'high, low, close',hlcv:'high, low, close, volume',ohlcv:'open, high, low, close, volume'};
function size(bytes:number){return bytes<1024?`${bytes} B`:bytes<1024*1024?`${(bytes/1024).toFixed(1)} KiB`:`${(bytes/1024/1024).toFixed(1)} MiB`;}

export function ScreenerEnrichment({api,visible}:{api:Backend;visible:boolean}){
 const [catalog,setCatalog]=useState<ScreenerIndicators|null>(null),[files,setFiles]=useState<ScreenerFilePage|null>(null),[info,setInfo]=useState<ScreenerEnrichmentInfo|null>(null);
 const [indicator,setIndicator]=useState('sma'),[parameters,setParameters]=useState<Record<string,string>>({}),[source,setSource]=useState('auto');
 const [page,setPage]=useState(0),[reload,setReload]=useState(0),[busy,setBusy]=useState(false);
 const [catalogError,setCatalogError]=useState(''),[fileError,setFileError]=useState(''),[infoError,setInfoError]=useState(''),[actionError,setActionError]=useState('');
 const mounted=useRef(false),lifetime=useRef(0),actionBusy=useRef(false),epoch=useRef(0),completed=useRef(0);
 useEffect(()=>{mounted.current=true;lifetime.current++;return ()=>{mounted.current=false;lifetime.current++;epoch.current++;};},[]);
 useEffect(()=>{
  if(!visible)return;
  let current=true;
  void api.GetScreenerIndicators().then(value=>{if(current){setCatalog(value);setCatalogError('');}}).catch(error=>{if(current)setCatalogError(errorText(error));});
  return ()=>{current=false;};
 },[api,visible,reload]);
 useEffect(()=>{
  if(!visible)return;
  let current=true;
  void api.GetScreenerFiles(page*pageSize,pageSize).then(value=>{
   if(!current)return;
   if(page>0&&page*pageSize>=value.total){setPage(Math.max(0,Math.ceil(value.total/pageSize)-1));return;}
   setFiles(value);setFileError('');
  }).catch(error=>{if(current)setFileError(errorText(error));});
  return ()=>{current=false;};
 },[api,visible,page,reload]);
 const fileIDs=files?.files.map(file=>file.id).join(',')??'';
 useEffect(()=>{
  if(!visible)return;
  let current=true,timer:ReturnType<typeof setTimeout>|undefined;
  const poll=async()=>{
   const version=epoch.current;
   try{
    if(!actionBusy.current){
     const value=await api.GetScreenerEnrichment(fileIDs?fileIDs.split(','):[]);
     if(current&&version===epoch.current){
      setInfo(value);setInfoError('');
      if(value.completed!==completed.current){completed.current=value.completed;setReload(value=>value+1);}
     }
    }
   }catch(error){if(current&&version===epoch.current)setInfoError(errorText(error));}
   finally{if(current)timer=setTimeout(()=>void poll(),750);}
  };
  void poll();return ()=>{current=false;if(timer)clearTimeout(timer);};
 },[api,visible,fileIDs]);
 const definition=catalog?.indicators.find(item=>item.id===indicator);
 const values=Object.fromEntries((definition?.parameters??[]).map(param=>[param.key,parameters[param.key]??String(param.default)]));
 const invalid=definition?.parameters.some(param=>values[param.key]===''||!Number.isFinite(Number(values[param.key]))||Number(values[param.key])<param.min||Number(values[param.key])>param.max||(param.step===1&&!Number.isInteger(Number(values[param.key]))))??true;
 const invalidPeriods='fast' in values&&Number(values.fast)>=Number(values.slow);
 const run=async(operation:()=>Promise<ScreenerEnrichmentInfo>)=>{
  if(actionBusy.current)return;
  const session=lifetime.current;
  actionBusy.current=true;epoch.current++;setBusy(true);setActionError('');
  try{const value=await operation();if(mounted.current&&session===lifetime.current){completed.current=value.completed;setInfo(value);setInfoError('');setReload(value=>value+1);}}
  catch(error){if(mounted.current&&session===lifetime.current)setActionError(errorText(error));}
  finally{actionBusy.current=false;if(mounted.current&&session===lifetime.current)setBusy(false);}
 };
 const jobs=new Map(info?.jobs.map(job=>[job.fileId,job])??[]),working=info?.active??false;
 const categories=[...new Set(catalog?.indicators.map(item=>item.category)??[])];
 const error=actionError||catalogError||fileError||infoError||info?.runtimeError;
 const finished=(info?.completed??0)+(info?.failed??0)+(info?.cancelled??0);
 return <div className="screener-enrichment">
  {error&&<div className="screener-error" role="alert">{error}<button type="button" onClick={()=>{setActionError('');setReload(value=>value+1);}}>Refresh enrichment</button></div>}
  <form className="screener-download-controls enrichment-controls" aria-label="Enrich downloaded data" onSubmit={event=>{
   event.preventDefault();if(definition&&!invalid&&!invalidPeriods&&files?.total&&!working&&!busy&&info?.runtimeAvailable)void run(()=>api.StartScreenerEnrichment({indicator,parameters:Object.fromEntries(Object.entries(values).map(([key,value])=>[key,Number(value)])),source:definition.input==='price'?source:'auto'}));
  }}>
   <label>Indicator<select aria-label="Enrichment indicator" value={indicator} disabled={!catalog||working||busy} onChange={event=>{setIndicator(event.target.value);setParameters({});}}>{categories.map(category=><optgroup key={category} label={category}>{catalog?.indicators.filter(item=>item.category===category).map(item=><option key={item.id} value={item.id}>{item.name}</option>)}</optgroup>)}</select></label>
   {definition?.parameters.map(param=><label key={param.key}>{param.label}<input type="number" aria-label={param.label} min={param.min} max={param.max} step={param.step} required value={values[param.key]} disabled={working||busy} onChange={event=>setParameters(current=>({...current,[param.key]:event.target.value}))}/></label>)}
   <label>Price source<select aria-label="Enrichment price source" value={definition?.input==='price'?source:'auto'} disabled={working||busy||definition?.input!=='price'} onChange={event=>setSource(event.target.value)}><option value="auto">Auto (close / bid / ask)</option><option value="close">Close</option><option value="open">Open</option><option value="high">High</option><option value="low">Low</option><option value="bidprice">Bid price (ticks)</option><option value="askprice">Ask price (ticks)</option></select></label>
   <button type="submit" className="primary" disabled={!definition||invalid||invalidPeriods||!files?.total||working||busy||!info?.runtimeAvailable}>{busy&&!working?'Starting…':`Enrich ${files?.total??0} ${files?.total===1?'file':'files'}`}</button>
   {working&&<button type="button" className="danger" disabled={busy} onClick={()=>void run(()=>api.CancelScreenerEnrichment())}>Cancel enrichment</button>}
   <div className="enrichment-help"><span>Requires: {definition?requirements[definition.input]:'Loading indicators…'}. Applies to all CSV files currently in this folder.</span><span>Originals are backed up. Matching indicator columns are recalculated; warm-up and undefined values stay blank.</span>{definition?.note&&<span>{definition.note}</span>}<span className="muted">{catalog&&`trading-signals ${catalog.version} · ${catalog.indicators.length} indicators`}</span></div>
   {invalidPeriods&&<small className="screener-date-error">Fast period must be smaller than slow period.</small>}
  </form>
  {info&&info.total>0&&<section className="enrichment-progress" aria-label="Enrichment progress" aria-live="polite"><div><strong>{working?'Enriching files':'Enrichment finished'}</strong><span>{info.completed} / {info.total} complete · {info.failed} failed{info.cancelled?` · ${info.cancelled} cancelled`:''}</span></div><progress aria-label="Overall enrichment progress" max={100} value={(finished+(info.currentProgress||0)/100)/info.total*100}/>{info.currentFile&&<p>{info.currentFile} · {Math.round(info.currentProgress)}%</p>}</section>}
  <section className="enrichment-files" aria-label="Downloaded data files"><div className="enrichment-files-heading"><div><h2>Downloaded data files</h2><p>{files?.directory||'Loading Screener folder…'}</p></div><button type="button" onClick={()=>setReload(value=>value+1)}>Refresh files</button></div>
   {!files&&!fileError&&<p className="screener-loading" role="status">Reading downloaded CSV headers…</p>}
   {files?.total===0&&<p className="screener-loading">No downloaded CSV files. Download historical data in the Downloader tab first.</p>}
   {!!files?.files.length&&<div className="enrichment-table-scroll"><table><thead><tr><th>File</th><th>Requested UTC period<br/><small>End date is exclusive</small></th><th>Available fields</th><th>Enrichment</th></tr></thead><tbody>{files.files.map(file=>{
    const job=jobs.get(file.id);
    return <tr key={file.id} data-file={file.name}><td><strong>{file.name}</strong><small>{file.timeframe||'Custom CSV'} · {size(file.bytes)}</small></td><td>{file.from&&file.to?<><time>{file.from}</time><span> → </span><time>{file.to}</time></>:<span className="muted">Period unavailable</span>}</td><td><code>{file.fields.join(', ')}</code>{file.error&&<p className="enrichment-file-error">{file.error}</p>}</td><td className={`enrichment-file-status ${job?.status??''}`}>{job?<><span>{job.status} · {Math.round(job.progress)}%</span><progress aria-label={`${file.name} enrichment progress`} max={100} value={job.progress}/><small>{job.message}</small>{!!job.columns.length&&<code>{job.columns.join(', ')}</code>}{job.backupPath&&<small title={job.backupPath}>Original saved in .cryptodesk-enrichment-backups</small>}</>:<span className="muted">Ready</span>}</td></tr>;
   })}</tbody></table></div>}
   {!!files?.total&&<nav className="enrichment-pagination" aria-label="Data file pages"><button type="button" disabled={page===0} onClick={()=>setPage(value=>value-1)}>Previous files</button><span>{page*pageSize+1}–{Math.min((page+1)*pageSize,files.total)} of {files.total} files</span><button type="button" disabled={(page+1)*pageSize>=files.total} onClick={()=>setPage(value=>value+1)}>Next files</button></nav>}
  </section>
 </div>;
}

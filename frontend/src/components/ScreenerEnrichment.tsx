import {useEffect,useRef,useState} from 'react';
import type {Backend,ScreenerEnrichmentInfo,ScreenerFilePage,ScreenerIndicators,ScreenerRefreshInfo} from '../types';
import {errorText} from '../types';
import './ScreenerRefresh.css';

const pageSize=50;
const requirements={price:'close, bidPrice or askPrice',hl:'high, low',hlc:'high, low, close',hlcv:'high, low, close, volume',ohlcv:'open, high, low, close, volume'};
function size(bytes:number){return bytes<1024?`${bytes} B`:bytes<1024*1024?`${(bytes/1024).toFixed(1)} KiB`:`${(bytes/1024/1024).toFixed(1)} MiB`;}
function utcTime(value:number){return new Date(value).toISOString().replace('T',' ').replace('.000Z',' UTC');}

export function ScreenerEnrichment({api,visible}:{api:Backend;visible:boolean}){
 const [catalog,setCatalog]=useState<ScreenerIndicators|null>(null),[files,setFiles]=useState<ScreenerFilePage|null>(null),[info,setInfo]=useState<ScreenerEnrichmentInfo|null>(null);
 const [refreshInfo,setRefreshInfo]=useState<ScreenerRefreshInfo|null>(null),[refreshError,setRefreshError]=useState(''),[lastAction,setLastAction]=useState<'enrichment'|'refresh'>('enrichment');
 const [indicator,setIndicator]=useState('sma'),[parameters,setParameters]=useState<Record<string,string>>({}),[source,setSource]=useState('auto');
 const [page,setPage]=useState(0),[reload,setReload]=useState(0),[busy,setBusy]=useState(false);
 const [catalogError,setCatalogError]=useState(''),[fileError,setFileError]=useState(''),[infoError,setInfoError]=useState(''),[actionError,setActionError]=useState('');
 const mounted=useRef(false),lifetime=useRef(0),actionBusy=useRef(false),epoch=useRef(0),completed=useRef(0),refreshed=useRef(0);
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
     const ids=fileIDs?fileIDs.split(','):[];
     const [enrichment,refresh]=await Promise.allSettled([api.GetScreenerEnrichment(ids),api.GetScreenerRefresh(ids)]);
     if(current&&version===epoch.current){
      if(enrichment.status==='fulfilled'){
       const value=enrichment.value;setInfo(value);setInfoError('');
       if(value.completed!==completed.current){completed.current=value.completed;setReload(value=>value+1);}
      }else setInfoError(errorText(enrichment.reason));
      if(refresh.status==='fulfilled'){
       const value=refresh.value;setRefreshInfo(value);setRefreshError('');
       if(value.completed!==refreshed.current){refreshed.current=value.completed;setReload(value=>value+1);}
      }else setRefreshError(errorText(refresh.reason));
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
  try{const value=await operation();if(mounted.current&&session===lifetime.current){completed.current=value.completed;setLastAction('enrichment');setInfo(value);setInfoError('');setReload(value=>value+1);}}
  catch(error){if(mounted.current&&session===lifetime.current)setActionError(errorText(error));}
  finally{actionBusy.current=false;if(mounted.current&&session===lifetime.current)setBusy(false);}
 };
 const runRefresh=async(cancel=false)=>{
  if(actionBusy.current)return;
  const session=lifetime.current;actionBusy.current=true;epoch.current++;setBusy(true);setActionError('');
  try{const value=await(cancel?api.CancelScreenerRefresh():api.StartScreenerRefresh());if(mounted.current&&session===lifetime.current){refreshed.current=value.completed;setLastAction('refresh');setRefreshInfo(value);setRefreshError('');setReload(value=>value+1);}}
  catch(error){if(mounted.current&&session===lifetime.current)setActionError(errorText(error));}
  finally{actionBusy.current=false;if(mounted.current&&session===lifetime.current)setBusy(false);}
 };
 const showRefresh=refreshInfo?.active||(lastAction==='refresh'&&!!refreshInfo?.total);
 const jobs=new Map((showRefresh?refreshInfo?.jobs:info?.jobs)?.map(job=>[job.fileId,job])??[]),working=!!(info?.active||refreshInfo?.active);
 const categories=[...new Set(catalog?.indicators.map(item=>item.category)??[])];
 const error=actionError||catalogError||fileError||infoError||refreshError||info?.runtimeError||refreshInfo?.runtimeError;
 const finished=(info?.completed??0)+(info?.failed??0)+(info?.cancelled??0);
 const refreshFinished=(refreshInfo?.completed??0)+(refreshInfo?.failed??0)+(refreshInfo?.cancelled??0);
 return <div className="screener-enrichment">
  {error&&<div className="screener-error" role="alert">{error}<button type="button" onClick={()=>{setActionError('');setReload(value=>value+1);}}>Retry loading</button></div>}
  <form className="screener-download-controls enrichment-controls" aria-label="Enrich downloaded data" onSubmit={event=>{
   event.preventDefault();if(definition&&!invalid&&!invalidPeriods&&files?.total&&!working&&!busy&&info?.runtimeAvailable)void run(()=>api.StartScreenerEnrichment({indicator,parameters:Object.fromEntries(Object.entries(values).map(([key,value])=>[key,Number(value)])),source:definition.input==='price'?source:'auto'}));
  }}>
   <label>Indicator<select aria-label="Enrichment indicator" value={indicator} disabled={!catalog||working||busy} onChange={event=>{setIndicator(event.target.value);setParameters({});}}>{categories.map(category=><optgroup key={category} label={category}>{catalog?.indicators.filter(item=>item.category===category).map(item=><option key={item.id} value={item.id}>{item.name}</option>)}</optgroup>)}</select></label>
   {definition?.parameters.map(param=><label key={param.key}>{param.label}<input type="number" aria-label={param.label} min={param.min} max={param.max} step={param.step} required value={values[param.key]} disabled={working||busy} onChange={event=>setParameters(current=>({...current,[param.key]:event.target.value}))}/></label>)}
   <label>Price source<select aria-label="Enrichment price source" value={definition?.input==='price'?source:'auto'} disabled={working||busy||definition?.input!=='price'} onChange={event=>setSource(event.target.value)}><option value="auto">Auto (close / bid / ask)</option><option value="close">Close</option><option value="open">Open</option><option value="high">High</option><option value="low">Low</option><option value="bidprice">Bid price (ticks)</option><option value="askprice">Ask price (ticks)</option></select></label>
   <button type="submit" className="primary" disabled={!definition||invalid||invalidPeriods||!files?.total||working||busy||!info?.runtimeAvailable}>{busy&&!working?'Starting…':`Enrich ${files?.total??0} ${files?.total===1?'file':'files'}`}</button>
   {info?.active&&<button type="button" className="danger" disabled={busy} onClick={()=>void run(()=>api.CancelScreenerEnrichment())}>Cancel enrichment</button>}
   <div className="enrichment-help"><span>Requires: {definition?requirements[definition.input]:'Loading indicators…'}. Applies to all CSV files currently in this folder.</span><span>Originals are backed up. Matching indicator columns are recalculated; warm-up and undefined values stay blank.</span>{definition?.note&&<span>{definition.note}</span>}<span className="muted">{catalog&&`trading-signals ${catalog.version} · ${catalog.indicators.length} indicators`}</span></div>
   {invalidPeriods&&<small className="screener-date-error">Fast period must be smaller than slow period.</small>}
  </form>
  {info&&info.total>0&&!showRefresh&&<section className="enrichment-progress" aria-label="Enrichment progress" aria-live="polite"><div><strong>{info.active?'Enriching files':'Enrichment finished'}</strong><span>{info.completed} / {info.total} complete · {info.failed} failed{info.cancelled?` · ${info.cancelled} cancelled`:''}</span></div><progress aria-label="Overall enrichment progress" max={100} value={(finished+(info.currentProgress||0)/100)/info.total*100}/>{info.currentFile&&<p>{info.currentFile} · {Math.round(info.currentProgress)}%</p>}</section>}
  {refreshInfo&&refreshInfo.total>0&&showRefresh&&<section className="enrichment-progress" aria-label="Data refresh progress" aria-live="polite"><div><strong>{refreshInfo.active?'Refreshing data and indicators':'Data refresh finished'}</strong><span>{refreshInfo.completed} / {refreshInfo.total} complete · {refreshInfo.failed} failed{refreshInfo.cancelled?` · ${refreshInfo.cancelled} cancelled`:''}</span></div><progress aria-label="Overall data refresh progress" max={100} value={(refreshFinished+(refreshInfo.currentProgress||0)/100)/refreshInfo.total*100}/>{refreshInfo.currentFile&&<p>{refreshInfo.currentFile} · {Math.round(refreshInfo.currentProgress)}%</p>}{refreshInfo.active&&<button type="button" className="danger" disabled={busy} onClick={()=>void runRefresh(true)}>Cancel refresh</button>}</section>}
  <section className="enrichment-files" aria-label="Downloaded data files"><div className="enrichment-files-heading"><div><h2>Downloaded data files</h2><p>{files?.directory||'Loading Screener folder…'}</p></div><div className="enrichment-file-actions"><button type="button" disabled={working||busy} onClick={()=>setReload(value=>value+1)}>Reload file list</button><button type="button" className="primary" disabled={!files?.total||working||busy||!refreshInfo?.runtimeAvailable} onClick={()=>void runRefresh()}>Refresh data and indicators</button></div></div>
   <p className="enrichment-refresh-help muted">Refresh checks missing and new data, then recalculates each file’s applied indicators. The first refresh can verify its full saved history. Completed changes keep original backups.</p>
   {!files&&!fileError&&<p className="screener-loading" role="status">Reading downloaded CSV headers…</p>}
   {files?.total===0&&<p className="screener-loading">No downloaded CSV files. Download historical data in the Downloader tab first.</p>}
   {!!files?.files.length&&<div className="enrichment-table-scroll"><table><thead><tr><th>File</th><th>UTC period</th><th>Available fields</th><th>{showRefresh?'Data refresh':'Enrichment'}</th></tr></thead><tbody>{files.files.map(file=>{
    const job=jobs.get(file.id);
    return <tr key={file.id} data-file={file.name}><td><strong>{file.name}</strong><small>{file.timeframe||'Custom CSV'} · {size(file.bytes)}</small></td><td>{file.firstTimestamp!==undefined&&file.lastTimestamp!==undefined?<><time>{utcTime(file.firstTimestamp)}</time><span> → </span><time>{utcTime(file.lastTimestamp)}</time><small>Available bar open times</small></>:file.from&&file.to?<><time>{file.from}</time><span> → </span><time>{file.to}</time><small>Requested; end date exclusive</small></>:<span className="muted">Period unavailable</span>}</td><td><code>{file.fields.join(', ')}</code>{file.error&&<p className="enrichment-file-error">{file.error}</p>}</td><td className={`enrichment-file-status ${job?.status??''}`}>{job?<><span>{job.status} · {Math.round(job.progress)}%</span><progress aria-label={`${file.name} ${showRefresh?'refresh':'enrichment'} progress`} max={100} value={job.progress}/><small>{job.message}</small>{showRefresh&&'addedRows' in job&&'updatedRows' in job&&<small>{String(job.addedRows)} rows added · {String(job.updatedRows)} rows updated</small>}{!!job.columns.length&&<code>{job.columns.join(', ')}</code>}{job.backupPath&&<small title={job.backupPath}>Original backup retained</small>}</>:<span className="muted">Ready</span>}</td></tr>;
   })}</tbody></table></div>}
   {!!files?.total&&<nav className="enrichment-pagination" aria-label="Data file pages"><button type="button" disabled={page===0} onClick={()=>setPage(value=>value-1)}>Previous files</button><span>{page*pageSize+1}–{Math.min((page+1)*pageSize,files.total)} of {files.total} files</span><button type="button" disabled={(page+1)*pageSize>=files.total} onClick={()=>setPage(value=>value+1)}>Next files</button></nav>}
  </section>
 </div>;
}

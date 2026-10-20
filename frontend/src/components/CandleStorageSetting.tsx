import {useCallback,useEffect,useRef,useState} from 'react';
import type {Backend,CandleStorageInfo} from '../types';
import {errorText} from '../types';

export function folderSize(size:number){
 if(!Number.isFinite(size)||size<0)return 'Unavailable';
 const units=['B','KiB','MiB','GiB','TiB','PiB'];let unit=0;
 while(size>=1024&&unit<units.length-1){size/=1024;unit++;}
 return `${size.toLocaleString('en-US',{maximumFractionDigits:unit?1:0})} ${units[unit]}`;
}

/** Machine-local preferences have their own backend commit, separate from workspace autosave. */
export function CandleStorageSetting({api}:{api:Backend}){
 const [info,setInfo]=useState<CandleStorageInfo|null>(null),[enabled,setEnabled]=useState(false),[directory,setDirectory]=useState('');
 const [loading,setLoading]=useState(true),[refreshing,setRefreshing]=useState(false),[applying,setApplying]=useState(false),[choosing,setChoosing]=useState(false);
 const [readError,setReadError]=useState(''),[applyError,setApplyError]=useState(''),[chooseError,setChooseError]=useState('');
 const alive=useRef(false),lifetime=useRef(0),readSequence=useRef(0),readBusy=useRef(false),applyBusy=useRef(false),chooseBusy=useRef(false);
 const refresh=useCallback(async(initialize=false)=>{
  if(!alive.current||readBusy.current||applyBusy.current)return;
  const sequence=++readSequence.current,session=lifetime.current;
  readBusy.current=true;setRefreshing(true);if(initialize)setLoading(true);
  try{
   const value=await api.GetCandleStorage();
   if(!alive.current||session!==lifetime.current||sequence!==readSequence.current)return;
   setInfo(value);setReadError('');if(initialize){setEnabled(value.enabled);setDirectory(value.directory);}
  }catch(error){if(alive.current&&session===lifetime.current&&sequence===readSequence.current)setReadError(errorText(error));}
  finally{if(alive.current&&session===lifetime.current&&sequence===readSequence.current){readBusy.current=false;setRefreshing(false);setLoading(false);}}
 },[api]);
 useEffect(()=>{
  alive.current=true;lifetime.current++;void refresh(true);
  return ()=>{alive.current=false;lifetime.current++;readSequence.current++;readBusy.current=false;};
 },[refresh]);
 useEffect(()=>{
  if(!info?.enabled||!enabled)return;
  const timer=setInterval(()=>{void refresh();},5000);return ()=>clearInterval(timer);
 },[info?.enabled,enabled,refresh]);
 const busy=loading||applying||choosing,dirty=info!==null&&(enabled!==info.enabled||directory!==info.directory),differentFolder=info!==null&&directory!==info.directory;
 const apply=async()=>{
  if(!info||applyBusy.current||chooseBusy.current)return;
  const session=lifetime.current;
  applyBusy.current=true;readSequence.current++;readBusy.current=false;setRefreshing(false);setApplying(true);setApplyError('');
  try{
   const value=await api.ConfigureCandleStorage(enabled,directory);
   if(!alive.current||session!==lifetime.current)return;
   setInfo(value);setEnabled(value.enabled);setDirectory(value.directory);setReadError('');
  }catch(error){if(alive.current&&session===lifetime.current)setApplyError(errorText(error));}
  finally{if(alive.current&&session===lifetime.current){applyBusy.current=false;setApplying(false);}}
 };
 const choose=async()=>{
  if(chooseBusy.current||applyBusy.current)return;
  const session=lifetime.current;chooseBusy.current=true;setChoosing(true);setChooseError('');
  try{const chosen=await api.ChooseCandleStorageDirectory();if(alive.current&&session===lifetime.current&&chosen){setDirectory(chosen);setApplyError('');}}
  catch(error){if(alive.current&&session===lifetime.current)setChooseError(errorText(error));}
  finally{if(alive.current&&session===lifetime.current){chooseBusy.current=false;setChoosing(false);}}
 };
 return <form className="settings-section candle-storage-setting" aria-label="Local candle storage" onSubmit={event=>{event.preventDefault();void apply();}}>
  <label className="candle-storage-toggle"><input type="checkbox" checked={enabled} disabled={busy||!info} onChange={event=>{setEnabled(event.target.checked);setApplyError('');}}/><strong>Store candle data locally</strong></label>
  <p>Candle storage stays on this computer. Workspace imports and exports do not include these settings.</p>
  {loading&&<p role="status">Loading candle storage settings…</p>}
  {info&&<small className="muted" role="status" aria-label="Applied candle storage settings">Applied: {info.enabled?'Enabled':'Disabled'}{dirty?' · Unapplied changes':''}</small>}
  {enabled&&info&&<div className="candle-storage-details">
   <label className="candle-storage-path"><span>Storage folder</span><input aria-label="Candle storage folder" value={directory} disabled={busy} placeholder="Use the default folder" onChange={event=>{setDirectory(event.target.value);setApplyError('');setChooseError('');}}/></label>
   <button type="button" aria-label="Choose candle storage folder" disabled={busy} onClick={()=>void choose()}>{choosing?'Choosing…':'Choose folder…'}</button>
   <small className="candle-storage-current muted">Current folder: {info.directory}</small>{differentFolder&&<small className="candle-storage-current muted">Existing cache files stay in the previous folder.</small>}
   <div className="candle-storage-size"><span>Total folder size: <output aria-label="Candle storage folder size">{differentFolder?'Apply folder changes to measure':refreshing?'Measuring…':info.error?'Unavailable':folderSize(info.sizeBytes)}</output></span><button type="button" aria-label="Refresh candle storage size" disabled={busy||refreshing||differentFolder} onClick={()=>void refresh()}>{refreshing?'Refreshing…':'Refresh'}</button></div>
  </div>}
  {(readError||applyError||chooseError||info?.error)&&<div className="candle-storage-error" role="alert">{applyError?`Could not apply candle storage settings: ${applyError}`:chooseError?`Could not choose a folder: ${chooseError}`:readError?`Could not read candle storage settings: ${readError}`:info?.error}{!info&&<button type="button" disabled={refreshing} onClick={()=>void refresh(true)}>Retry storage settings</button>}</div>}
  <div className="candle-storage-actions"><button type="submit" aria-label="Apply candle storage settings" disabled={!info||busy||(!dirty&&!info.error)}>{applying?'Applying…':'Apply'}</button></div>
 </form>;
}

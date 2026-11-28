import {useEffect,useRef,useState} from 'react';
import type {Backend} from '../types';
import {errorText} from '../types';

export function ComparisonSnapshotSetting({api}:{api:Backend}){
 const [info,setInfo]=useState<string|null>(null),[directory,setDirectory]=useState(''),[error,setError]=useState(''),[busy,setBusy]=useState<'loading'|'applying'|'choosing'|null>('loading'),[retry,setRetry]=useState(0);
 const lifetime=useRef(0),alive=useRef(false),locked=useRef(false);
 useEffect(()=>{alive.current=true;lifetime.current++;const session=lifetime.current;setBusy('loading');
  void api.GetComparisonSnapshotDirectory().then(value=>{if(alive.current&&session===lifetime.current){setInfo(value);setDirectory(value);setError('');}}).catch(value=>{if(alive.current&&session===lifetime.current)setError(errorText(value));}).finally(()=>{if(alive.current&&session===lifetime.current)setBusy(null);});
  return ()=>{alive.current=false;lifetime.current++;};
 },[api,retry]);
 const perform=async(kind:'applying'|'choosing')=>{
  if(locked.current)return;const session=lifetime.current;locked.current=true;setBusy(kind);setError('');
  try{if(kind==='choosing'){const value=await api.ChooseComparisonSnapshotDirectory();if(alive.current&&session===lifetime.current&&value)setDirectory(value);}else{const value=await api.ConfigureComparisonSnapshotDirectory(directory);if(alive.current&&session===lifetime.current){setInfo(value);setDirectory(value);}}}
  catch(value){if(alive.current&&session===lifetime.current)setError(errorText(value));}
  finally{if(alive.current&&session===lifetime.current){locked.current=false;setBusy(null);}}
 };
 const dirty=!!info&&directory!==info;
 return <form className="settings-section comparison-snapshot-setting" aria-label="Comparison snapshots storage" onSubmit={event=>{event.preventDefault();if(info)void perform('applying');}}><h3>Comparison snapshots</h3><p>Save Comparison text snapshots in a local folder. Existing snapshots stay in their original folder.</p>
  {busy==='loading'&&<p role="status">Loading Comparison settings…</p>}
  <div className="candle-storage-details"><label className="candle-storage-path"><span>Snapshot folder</span><input aria-label="Comparison snapshot folder" value={directory} placeholder="Use the default folder" disabled={!!busy||!info} onChange={event=>{setDirectory(event.target.value);setError('');}}/></label><button type="button" aria-label="Choose Comparison snapshot folder" disabled={!!busy||!info} onClick={()=>void perform('choosing')}>{busy==='choosing'?'Choosing…':'Choose folder…'}</button>{info&&<small className="candle-storage-current muted" role="status" aria-label="Applied Comparison folder">Current folder: {info}{dirty?' · Unapplied changes':''}</small>}</div>
  {(error||'')&&<p className="candle-storage-error" role="alert">{error||''}{!info&&<button type="button" onClick={()=>setRetry(value=>value+1)} disabled={!!busy}>Retry Comparison settings</button>}</p>}
  <div className="candle-storage-actions"><button type="submit" aria-label="Apply Comparison snapshot folder" disabled={!!busy||!dirty}>{busy==='applying'?'Applying…':'Apply'}</button></div>
 </form>;
}

import {useEffect,useRef,useState} from 'react';
import type {Backend,ScreenerInfo} from '../types';
import {errorText} from '../types';

export function ScreenerDirectorySetting({api}:{api:Backend}){
 const [info,setInfo]=useState<ScreenerInfo|null>(null),[directory,setDirectory]=useState(''),[error,setError]=useState(''),[busy,setBusy]=useState<'loading'|'applying'|'choosing'|null>('loading'),[retry,setRetry]=useState(0);
 const lifetime=useRef(0),alive=useRef(false),locked=useRef(false);
 useEffect(()=>{alive.current=true;lifetime.current++;const session=lifetime.current;setBusy('loading');
  void api.GetScreener().then(value=>{if(alive.current&&session===lifetime.current){setInfo(value);setDirectory(value.directory);setError('');}}).catch(value=>{if(alive.current&&session===lifetime.current)setError(errorText(value));}).finally(()=>{if(alive.current&&session===lifetime.current)setBusy(null);});
  return ()=>{alive.current=false;lifetime.current++;};
 },[api,retry]);
 const perform=async(kind:'applying'|'choosing')=>{
  if(locked.current)return;const session=lifetime.current;locked.current=true;setBusy(kind);setError('');
  try{if(kind==='choosing'){const value=await api.ChooseScreenerDirectory();if(alive.current&&session===lifetime.current&&value)setDirectory(value);}else{const value=await api.ConfigureScreenerDirectory(directory);if(alive.current&&session===lifetime.current){setInfo(value);setDirectory(value.directory);}}}
  catch(value){if(alive.current&&session===lifetime.current)setError(errorText(value));}
  finally{if(alive.current&&session===lifetime.current){locked.current=false;setBusy(null);}}
 };
 const dirty=!!info&&directory!==info.directory;
 return <form className="settings-section screener-directory-setting" aria-label="Screener data storage" onSubmit={event=>{event.preventDefault();if(info)void perform('applying');}}><h3>Screener data</h3><p>Save Dukascopy CSV downloads in a local folder. Existing downloads stay in their original folder.</p>
  {busy==='loading'&&<p role="status">Loading Screener settings…</p>}
  <div className="candle-storage-details"><label className="candle-storage-path"><span>Download folder</span><input aria-label="Screener download folder" value={directory} placeholder="Use the default folder" disabled={!!busy||!info} onChange={event=>{setDirectory(event.target.value);setError('');}}/></label><button type="button" aria-label="Choose Screener download folder" disabled={!!busy||!info} onClick={()=>void perform('choosing')}>{busy==='choosing'?'Choosing…':'Choose folder…'}</button>{info&&<small className="candle-storage-current muted" role="status" aria-label="Applied Screener folder">Current folder: {info.directory}{dirty?' · Unapplied changes':''}</small>}</div>
  {(error||info?.error)&&<p className="candle-storage-error" role="alert">{error||info?.error}{!info&&<button type="button" onClick={()=>setRetry(value=>value+1)} disabled={!!busy}>Retry Screener settings</button>}</p>}
  <div className="candle-storage-actions"><button type="submit" aria-label="Apply Screener download folder" disabled={!!busy||!dirty}>{busy==='applying'?'Applying…':'Apply'}</button></div>
 </form>;
}

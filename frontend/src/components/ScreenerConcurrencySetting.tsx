import {forwardRef,useEffect,useImperativeHandle,useRef,useState} from 'react';
import type {Backend,ScreenerInfo} from '../types';
import {errorText} from '../types';

export interface ScreenerConcurrencySettingHandle {flush():Promise<void>}
export const ScreenerConcurrencySetting=forwardRef<ScreenerConcurrencySettingHandle,{api:Backend;visible:boolean}>(function ScreenerConcurrencySetting({api,visible},ref){
 const [info,setInfo]=useState<ScreenerInfo|null>(null),[value,setValue]=useState('5'),[loading,setLoading]=useState(false),[applying,setApplying]=useState(false),[error,setError]=useState(''),[retry,setRetry]=useState(0);
 const alive=useRef(false),lifetime=useRef(0),pending=useRef<Promise<void>|null>(null);
 useEffect(()=>{alive.current=true;lifetime.current++;return()=>{alive.current=false;lifetime.current++;};},[]);
 useImperativeHandle(ref,()=>({flush:async()=>{await pending.current;}}),[]);
 useEffect(()=>{
  if(!visible||info)return;
  let active=true;setLoading(true);setError('');
  void api.GetScreener().then(next=>{if(active){setInfo(next);setValue(String(next.concurrency));setLoading(false);}}).catch(reason=>{if(active){setError(errorText(reason));setLoading(false);}});
  return()=>{active=false;};
 },[api,visible,info,retry]);
 const concurrency=Number(value),valid=value.trim()!==''&&Number.isInteger(concurrency)&&concurrency>=1&&concurrency<=20,dirty=!!info&&concurrency!==info.concurrency;
 const apply=()=>{
  if(!info||pending.current||!valid||!dirty)return;
  const session=lifetime.current;setApplying(true);setError('');
  const operation=Promise.resolve().then(()=>api.ConfigureScreenerConcurrency(concurrency)).then(next=>{if(alive.current&&session===lifetime.current){setInfo(next);setValue(String(next.concurrency));}}).catch(reason=>{if(alive.current&&session===lifetime.current)setError(errorText(reason));throw reason;}).finally(()=>{pending.current=null;if(alive.current&&session===lifetime.current)setApplying(false);});
  pending.current=operation;void operation.catch(()=>{});
 };
 return <form hidden={!visible} className="settings-section screener-concurrency-setting" aria-label="Screener worker threads" onSubmit={event=>{event.preventDefault();apply();}}>
  <h3>Worker threads</h3><p>Run Downloader, Enrichment and Refresh with 1–20 workers. The default is 5. Changes apply to subsequent runs; finish or cancel active Screener work before applying.</p>
  {loading&&<p role="status">Loading worker settings…</p>}
  <label className="setting-row"><span><strong>Worker threads</strong><small>Saved on this computer, separately from workspace exports.</small></span><input type="number" aria-label="Screener worker threads" value={value} min={1} max={20} step={1} required disabled={!info||loading||applying} onChange={event=>{setValue(event.target.value);setError('');}}/></label>
  {info&&<small className="muted" role="status" aria-label="Applied Screener worker threads">Applied: {info.concurrency} {info.concurrency===1?'worker':'workers'}{dirty?' · Unapplied changes':''}</small>}
  {info&&!valid&&<p className="candle-storage-error">Enter a whole number from 1 to 20.</p>}
  {error&&<p className="candle-storage-error" role="alert">{error}{!info&&<button type="button" disabled={loading} onClick={()=>setRetry(count=>count+1)}>Retry worker settings</button>}</p>}
  <div className="candle-storage-actions"><button type="submit" aria-label="Apply Screener worker threads" disabled={!info||loading||applying||!valid||!dirty}>{applying?'Applying…':'Apply'}</button></div>
 </form>;
});

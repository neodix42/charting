import {forwardRef,useEffect,useImperativeHandle,useRef,useState} from 'react';
import type {AnalysisRequest,SavedAnalysisCondition,ScreenerAnalysisBackend} from '../workspace/screenerAnalysis';
import {errorText} from '../types';

export interface ScreenerSavedConditionsHandle {flush():Promise<void>}

export const ScreenerSavedConditions=forwardRef<ScreenerSavedConditionsHandle,{
 api:ScreenerAnalysisBackend;visible:boolean;disabled:boolean;canSave:boolean;request:AnalysisRequest;onLoad(request:AnalysisRequest):void;
}>(function ScreenerSavedConditions({api,visible,disabled,canSave,request,onLoad},ref){
 const [conditions,setConditions]=useState<SavedAnalysisCondition[]>([]),[ready,setReady]=useState(false),[listError,setListError]=useState(''),[retry,setRetry]=useState(0);
 const [open,setOpen]=useState(false),[name,setName]=useState(''),[saveError,setSaveError]=useState(''),[busy,setBusy]=useState(false),[notice,setNotice]=useState('');
 const input=useRef<HTMLInputElement>(null),dialog=useRef<HTMLElement>(null),trigger=useRef<HTMLButtonElement>(null),mounted=useRef(false),flight=useRef<Promise<void>|null>(null);
 const listEpoch=useRef(0);
 useImperativeHandle(ref,()=>({flush:async()=>{await flight.current;}}),[]);
 useEffect(()=>{mounted.current=true;return ()=>{mounted.current=false;};},[]);
 useEffect(()=>{
  if(!visible)return;
  let current=true;const revision=++listEpoch.current;
  void api.GetScreenerSavedConditions().then(value=>{if(current&&revision===listEpoch.current){setConditions(value);setReady(true);setListError('');}}).catch(error=>{if(current&&revision===listEpoch.current){setListError(errorText(error));setReady(false);}});
  return ()=>{current=false;};
 },[api,visible,retry]);
 useEffect(()=>{if(!visible)setOpen(false);},[visible]);
 useEffect(()=>{
  if(!open)return;
  input.current?.focus();
  const key=(event:KeyboardEvent)=>{
   if(event.key==='Escape'){event.preventDefault();event.stopPropagation();if(!flight.current)setOpen(false);}
   if(event.key==='Tab'){
    const items=[...(dialog.current?.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled)')??[])],first=items[0],last=items.at(-1);
    if(event.shiftKey&&document.activeElement===first){event.preventDefault();last?.focus();}
    else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();first?.focus();}
   }
  };
  document.addEventListener('keydown',key);
  return ()=>{document.removeEventListener('keydown',key);if(trigger.current?.isConnected)trigger.current.focus();};
 },[open]);
 const save=()=>{
  if(flight.current||disabled||!canSave)return;
  const title=name.trim(),snapshot=structuredClone(request);
  if(!title){setSaveError('Enter a condition name.');return;}
  listEpoch.current++;setBusy(true);setSaveError('');
  const operation=(async()=>{
   try{
    const saved=await api.SaveScreenerAnalysisCondition(title,snapshot);
    if(mounted.current){setConditions(current=>[...current.filter(item=>item.id!==saved.id),saved]);setOpen(false);setNotice(`Saved “${saved.name}”.`);}
   }catch(error){if(mounted.current)setSaveError(errorText(error));throw error;}
   finally{flight.current=null;if(mounted.current)setBusy(false);}
  })();
  flight.current=operation;void operation.catch(()=>{});
 };
 return <>
  <aside className="analysis-saved" aria-label="Saved conditions">
   <header><h2>Saved conditions</h2><span>{conditions.length}</span></header>
   <button ref={trigger} type="button" className="analysis-save-condition" disabled={!ready||disabled||!canSave||busy} onClick={()=>{setName('');setSaveError('');setNotice('');setOpen(true);}}>Safe Condition</button>
   <p>Select a condition to load all its rows. Apply conditions runs the scan.</p>
   {notice&&<p className="analysis-saved-notice" role="status">{notice}</p>}
   {listError&&<div className="analysis-saved-error" role="alert">{listError}<button type="button" onClick={()=>setRetry(value=>value+1)}>Retry saved conditions</button></div>}
   {!ready&&!listError&&<p role="status">Loading saved conditions…</p>}
   {ready&&conditions.length===0&&<p className="analysis-saved-empty">No saved conditions yet.</p>}
   <div className="analysis-saved-list">{conditions.map(condition=><button type="button" key={condition.id} disabled={!ready||disabled||busy} aria-label={`Load condition ${condition.name}`} onClick={()=>{setNotice('');onLoad(structuredClone(condition.request));}}><strong>{condition.name}</strong><small>{condition.request.conditions.length} {condition.request.conditions.length===1?'row':'rows'} · AND</small></button>)}</div>
  </aside>
  {visible&&open&&<div className="modal-backdrop analysis-save-backdrop"><section ref={dialog} className="modal analysis-save-dialog" role="dialog" aria-modal="true" aria-labelledby="analysis-save-title">
   <form aria-label="Save named condition" onSubmit={event=>{event.preventDefault();save();}}>
    <div className="modal-header"><h2 id="analysis-save-title">Save condition</h2><button type="button" disabled={busy} aria-label="Close save condition" onClick={()=>setOpen(false)}>×</button></div>
    <p>Save all {request.conditions.length} condition {request.conditions.length===1?'row':'rows'} together for later use.</p>
    <label>Condition name<input ref={input} aria-label="Condition name" required maxLength={100} value={name} disabled={busy} onChange={event=>{setName(event.target.value);setSaveError('');}}/></label>
    {saveError&&<p className="analysis-saved-error" role="alert">{saveError}</p>}
    <div className="modal-actions"><button type="button" disabled={busy} onClick={()=>setOpen(false)}>Cancel</button><button type="submit" className="primary" disabled={busy||!name.trim()}>{busy?'Saving…':'Save condition'}</button></div>
   </form>
  </section></div>}
 </>;
});

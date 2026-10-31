import {forwardRef,useEffect,useImperativeHandle,useLayoutEffect,useRef,useState} from 'react';
import {createPortal} from 'react-dom';
import type {AnalysisRequest,SavedAnalysisCondition,ScreenerAnalysisBackend} from '../workspace/screenerAnalysis';
import {analysisRequestIdentity} from '../workspace/screenerAnalysis';
import {errorText} from '../types';

export interface ScreenerSavedConditionsHandle {flush():Promise<void>}

export const ScreenerSavedConditions=forwardRef<ScreenerSavedConditionsHandle,{
 api:ScreenerAnalysisBackend;visible:boolean;disabled:boolean;canSave:boolean;request:AnalysisRequest;onLoad(request:AnalysisRequest):void;
}>(function ScreenerSavedConditions({api,visible,disabled,canSave,request,onLoad},ref){
 const [conditions,setConditions]=useState<SavedAnalysisCondition[]>([]),[ready,setReady]=useState(false),[listError,setListError]=useState(''),[retry,setRetry]=useState(0);
 const [open,setOpen]=useState(false),[name,setName]=useState(''),[saveError,setSaveError]=useState(''),[busy,setBusy]=useState(false),[notice,setNotice]=useState('');
 const [loadedId,setLoadedId]=useState<string|null>(null),selectedOnce=useRef(false);
 const [renaming,setRenaming]=useState<SavedAnalysisCondition|null>(null);
 const [context,setContext]=useState<{condition:SavedAnalysisCondition;x:number;y:number;origin:HTMLButtonElement}|null>(null),[deleteError,setDeleteError]=useState('');
 const menu=useRef<HTMLDivElement>(null),renameButton=useRef<HTMLButtonElement>(null),restoreDeleteFocus=useRef<HTMLButtonElement|null>(null),dialogOrigin=useRef<HTMLButtonElement|null>(null);
 const input=useRef<HTMLInputElement>(null),dialog=useRef<HTMLElement>(null),trigger=useRef<HTMLButtonElement>(null),mounted=useRef(false),flight=useRef<Promise<void>|null>(null);
 const listEpoch=useRef(0);
 useImperativeHandle(ref,()=>({flush:async()=>{await flight.current;}}),[]);
 useEffect(()=>{mounted.current=true;return ()=>{mounted.current=false;};},[]);
 useEffect(()=>{
  if(!visible)return;
  let current=true;const revision=++listEpoch.current;
  void api.GetScreenerSavedConditions().then(value=>{if(current&&revision===listEpoch.current){setConditions(value);setReady(true);setListError('');}}).catch(error=>{if(current&&revision===listEpoch.current){setListError(errorText(error));setReady(false);}});
  return ()=>{current=false;};
 },[api,visible,retry,disabled]);
 useEffect(()=>{if(!visible)setOpen(false);},[visible]);
 useEffect(()=>{if(!visible||disabled||busy||open)setContext(null);},[visible,disabled,busy,open]);
 useLayoutEffect(()=>{
  if(!context||!menu.current)return;
  const bounds=menu.current.getBoundingClientRect();
  menu.current.style.left=`${Math.max(8,Math.min(context.x,innerWidth-bounds.width-8))}px`;
  menu.current.style.top=`${Math.max(8,Math.min(context.y,innerHeight-bounds.height-8))}px`;
  renameButton.current?.focus();
 },[context]);
 useEffect(()=>{
  if(!context)return;
  const close=()=>setContext(null);
  const pointer=(event:PointerEvent)=>{if(!menu.current?.contains(event.target as Node))close();};
  const key=(event:KeyboardEvent)=>{
   if(event.key==='Escape'||event.key==='Tab'){
    if(event.key==='Escape'){event.preventDefault();event.stopPropagation();}
    close();if(context.origin.isConnected)context.origin.focus();
   }else if(['ArrowUp','ArrowDown','Home','End'].includes(event.key)){
    event.preventDefault();
    const items=[...(menu.current?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')??[])],index=items.findIndex(item=>item===document.activeElement);
    const next=event.key==='Home'?0:event.key==='End'?items.length-1:(index+(event.key==='ArrowDown'?1:-1)+items.length)%items.length;
    items[next]?.focus();
   }
  };
  document.addEventListener('pointerdown',pointer,true);document.addEventListener('keydown',key,true);
  window.addEventListener('resize',close);document.addEventListener('scroll',close,true);
  return ()=>{document.removeEventListener('pointerdown',pointer,true);document.removeEventListener('keydown',key,true);window.removeEventListener('resize',close);document.removeEventListener('scroll',close,true);};
 },[context]);
 useEffect(()=>{
  if(busy||!visible||!restoreDeleteFocus.current)return;
  const origin=restoreDeleteFocus.current;restoreDeleteFocus.current=null;
  (origin.isConnected&&!origin.disabled?origin:trigger.current)?.focus();
 },[busy,visible]);
 useEffect(()=>{
  if(!open)return;
  input.current?.focus();input.current?.select();
  const origin=dialogOrigin.current;
  const key=(event:KeyboardEvent)=>{
   if(event.key==='Escape'){event.preventDefault();event.stopPropagation();if(!flight.current)setOpen(false);}
   if(event.key==='Tab'){
    const items=[...(dialog.current?.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled)')??[])],first=items[0],last=items.at(-1);
    if(!items.length){event.preventDefault();dialog.current?.focus();}
    else if(!items.some(item=>item===document.activeElement)){event.preventDefault();(event.shiftKey?last:first)?.focus();}
    else if(event.shiftKey&&document.activeElement===first){event.preventDefault();last?.focus();}
    else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();first?.focus();}
   }
  };
  document.addEventListener('keydown',key);
  return ()=>{document.removeEventListener('keydown',key);if(origin?.isConnected&&!origin.disabled&&origin.getClientRects().length)origin.focus();};
 },[open]);
 const loaded=conditions.find(condition=>condition.id===loadedId),modified=!!loaded&&analysisRequestIdentity(loaded.request)!==analysisRequestIdentity(request);
 useEffect(()=>{if(selectedOnce.current||!ready||disabled)return;const same=conditions.filter(condition=>analysisRequestIdentity(condition.request)===analysisRequestIdentity(request));if(same.length===1){selectedOnce.current=true;setLoadedId(same[0].id);}},[conditions,request,ready,disabled]);
 const save=()=>{
  if(flight.current||disabled||!renaming&&!canSave)return;
  const title=name.trim();
  if(!title){setSaveError('Enter a condition name.');return;}
  listEpoch.current++;setBusy(true);setSaveError('');
  const operation=(async()=>{
   try{
    const saved=renaming?await api.RenameScreenerAnalysisCondition(renaming.id,title):await api.SaveScreenerAnalysisCondition(title,structuredClone(request));
    listEpoch.current++;
    if(mounted.current){if(!renaming){selectedOnce.current=true;setLoadedId(saved.id);}setConditions(current=>renaming?current.map(item=>item.id===saved.id?saved:item):[...current.filter(item=>item.id!==saved.id),saved]);setOpen(false);setNotice(`${renaming?'Renamed':'Saved'} “${saved.name}”.`);}
   }catch(error){if(mounted.current)setSaveError(errorText(error));throw error;}
   finally{flight.current=null;if(mounted.current)setBusy(false);}
  })();
  flight.current=operation;void operation.catch(()=>{});
 };
 const showMenu=(condition:SavedAnalysisCondition,origin:HTMLButtonElement,x:number,y:number)=>{
  if(!visible||!ready||disabled||flight.current||open)return;
  setContext({condition,origin,x,y});setDeleteError('');
 };
 const rename=()=>{
  if(!context||flight.current||disabled||!ready)return;
  dialogOrigin.current=context.origin;setRenaming(context.condition);setName(context.condition.name);setSaveError('');setNotice('');setContext(null);setOpen(true);
 };
 const remove=()=>{
  if(!context||flight.current||disabled||!ready)return;
  const {condition,origin}=context;setContext(null);listEpoch.current++;setBusy(true);setDeleteError('');setNotice(`Deleting “${condition.name}”…`);
  const operation=(async()=>{
   try{
    await api.DeleteScreenerAnalysisCondition(condition.id);listEpoch.current++;
    if(mounted.current){if(loadedId===condition.id)setLoadedId(null);setConditions(current=>current.filter(item=>item.id!==condition.id));setNotice(`Deleted “${condition.name}”.`);}
   }catch(error){if(mounted.current){setDeleteError(errorText(error));setNotice('');}throw error;}
   finally{flight.current=null;if(mounted.current){restoreDeleteFocus.current=origin;setBusy(false);}}
  })();
  flight.current=operation;void operation.catch(()=>{});
 };
 return <>
  <aside className="analysis-saved" aria-label="Saved conditions">
   <header><h2>Saved conditions</h2><span>{conditions.length}</span></header>
   <button ref={trigger} type="button" className="analysis-save-condition" disabled={!ready||disabled||!canSave||busy} onClick={()=>{dialogOrigin.current=trigger.current;setRenaming(null);setName('');setSaveError('');setNotice('');setOpen(true);}}>Safe Condition</button>
   <p>Select a condition to load all its rows. Apply conditions runs the scan. Right-click a saved condition to rename or delete it.</p>
   {loaded&&<p className="analysis-loaded-condition" aria-live="polite">Loaded condition: <strong>{loaded.name}</strong>{modified?' · Modified draft':''}</p>}
   {notice&&<p className="analysis-saved-notice" role="status">{notice}</p>}
   {listError&&<div className="analysis-saved-error" role="alert">{listError}<button type="button" onClick={()=>setRetry(value=>value+1)}>Retry saved conditions</button></div>}
   {deleteError&&<p className="analysis-saved-error" role="alert">{deleteError}</p>}
   {!ready&&!listError&&<p role="status">Loading saved conditions…</p>}
   {ready&&conditions.length===0&&<p className="analysis-saved-empty">No saved conditions yet.</p>}
   <div className="analysis-saved-list">{conditions.map(condition=><button type="button" key={condition.id} className={loadedId===condition.id?'analysis-condition-loaded':undefined} aria-pressed={loadedId===condition.id} disabled={!ready||disabled||busy} aria-label={`Load condition ${condition.name}`} aria-haspopup="menu" aria-expanded={context?.condition.id===condition.id} onContextMenu={event=>{event.preventDefault();event.stopPropagation();showMenu(condition,event.currentTarget,event.clientX,event.clientY);}} onKeyDown={event=>{if(event.key==='ContextMenu'||event.shiftKey&&event.key==='F10'){event.preventDefault();const bounds=event.currentTarget.getBoundingClientRect();showMenu(condition,event.currentTarget,bounds.left,bounds.bottom);}}} onClick={()=>{selectedOnce.current=true;setLoadedId(condition.id);setContext(null);setNotice('');setDeleteError('');onLoad(structuredClone(condition.request));}}><strong>{condition.name}</strong><small>{condition.request.conditions.length} {condition.request.conditions.length===1?'row':'rows'} · AND{loadedId===condition.id?modified?' · Loaded, modified':' · Loaded':''}</small></button>)}</div>
  </aside>
  {visible&&context&&!disabled&&!busy&&!open&&createPortal(<div ref={menu} className="analysis-saved-menu" role="menu" aria-label={`Saved condition ${context.condition.name}`} style={{left:context.x,top:context.y}} onContextMenu={event=>event.preventDefault()}><button ref={renameButton} type="button" role="menuitem" tabIndex={-1} onClick={rename}>Rename</button><button type="button" className="danger" role="menuitem" tabIndex={-1} onClick={remove}>Delete</button></div>,document.body)}
  {visible&&open&&<div className="modal-backdrop analysis-save-backdrop"><section ref={dialog} className="modal analysis-save-dialog" role="dialog" tabIndex={-1} aria-modal="true" aria-labelledby="analysis-save-title">
   <form aria-label={renaming?'Rename named condition':'Save named condition'} onSubmit={event=>{event.preventDefault();save();}}>
    <div className="modal-header"><h2 id="analysis-save-title">{renaming?'Rename condition':'Save condition'}</h2><button type="button" disabled={busy} aria-label={renaming?'Close rename condition':'Close save condition'} onClick={()=>setOpen(false)}>×</button></div>
    <p>{renaming?`Choose a new name for “${renaming.name}”.`:`Save all ${request.conditions.length} condition ${request.conditions.length===1?'row':'rows'} together for later use.`}</p>
    <label>Condition name<input ref={input} aria-label="Condition name" aria-invalid={!!saveError} aria-describedby={saveError?'analysis-condition-name-error':undefined} required maxLength={100} value={name} disabled={busy} onChange={event=>{setName(event.target.value);setSaveError('');}}/></label>
    {saveError&&<p id="analysis-condition-name-error" className="analysis-saved-error" role="alert">{saveError}</p>}
    <div className="modal-actions"><button type="button" disabled={busy} onClick={()=>setOpen(false)}>Cancel</button><button type="submit" className="primary" disabled={busy||!name.trim()}>{renaming?(busy?'Renaming…':'Rename'):(busy?'Saving…':'Save condition')}</button></div>
   </form>
  </section></div>}
 </>;
});

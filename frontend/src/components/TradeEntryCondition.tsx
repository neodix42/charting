import {useEffect,useRef,useState} from 'react';
import {errorText} from '../types';

export function TradeEntryCondition({value,label,save,onSaved}:{value:string;label:string;save:(value:string)=>Promise<string>;onSaved:(value:string)=>void}){
 const [editing,setEditing]=useState(false),[draft,setDraft]=useState(value),[saving,setSaving]=useState(false),[error,setError]=useState('');
 const input=useRef<HTMLInputElement>(null),button=useRef<HTMLButtonElement>(null),active=useRef(false),pending=useRef(false),mounted=useRef(true),returnFocus=useRef(false);
 useEffect(()=>{mounted.current=true;return()=>{mounted.current=false;};},[]);
 useEffect(()=>{if(editing){input.current?.focus();input.current?.select();}else if(returnFocus.current){returnFocus.current=false;button.current?.focus();}},[editing]);
 useEffect(()=>{if(editing&&!saving&&error&&returnFocus.current)input.current?.focus();},[editing,saving,error]);
 const finish=()=>{active.current=false;setEditing(false);};
 const commit=async(enter=false)=>{
  if(!active.current||pending.current)return;
  returnFocus.current=enter;
  if(draft===value){finish();return;}
  pending.current=true;setSaving(true);setError('');
  try{const stored=await save(draft);if(mounted.current){onSaved(stored);finish();}}
  catch(reason){if(mounted.current)setError(`Not saved: ${errorText(reason)}`);}
  finally{pending.current=false;if(mounted.current)setSaving(false);}
 };
 return <td className="trade-entry-condition">
  {editing?<div className="trade-entry-editor" aria-busy={saving}>
   <input ref={input} type="text" aria-label={label} maxLength={100} value={draft} disabled={saving} placeholder="Up to 100 characters" onChange={event=>setDraft(event.target.value)} onBlur={()=>void commit()} onKeyDown={event=>{
    if(event.key==='Enter'&&!event.nativeEvent.isComposing){event.preventDefault();event.stopPropagation();void commit(true);}
    if(event.key==='Escape'){event.preventDefault();event.stopPropagation();returnFocus.current=true;active.current=false;setError('');setEditing(false);}
   }}/>
   {saving&&<small role="status">Saving…</small>}{error&&<small role="alert">{error}</small>}
  </div>:<button ref={button} type="button" className="trade-entry-value" aria-label={`Edit ${label.toLowerCase()}`} title={value||'Add entry condition (up to 100 characters)'} onClick={()=>{setDraft(value);setError('');active.current=true;setEditing(true);}}>{value||'Add entry condition'}</button>}
 </td>;
}

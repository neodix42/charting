import {useEffect,useState} from 'react';
import type {Backend,StartupStatus} from '../types';
import './StartupProgress.css';

/** One lightweight, coalesced read; it never delays loading the actual layout. */
export function useStartupProgress(api:Backend|null,reload:number){
 const [value,setValue]=useState<StartupStatus|null>(null);
 useEffect(()=>{
  setValue(null);if(!api)return;
  let current=true,timer:ReturnType<typeof setTimeout>|undefined;
  const poll=async()=>{
   try{
    const status=await api.GetStartupStatus();
    if(!current)return;setValue(status);
    if(status.ready)return;
   }catch{/* Workspace loading reports actionable failures independently. */}
   if(current)timer=setTimeout(()=>void poll(),250);
  };
  void poll();return()=>{current=false;if(timer)clearTimeout(timer);};
 },[api,reload]);
 return value;
}

export function StartupProgress({api}:{api:Backend}){
 // Keep background polling inside this small notice. Active charts do not need
 // to rerender whenever a service's elapsed time changes.
 const value=useStartupProgress(api,0);
 const [dismissed,setDismissed]=useState(false);
 const pending=value?.tasks.filter(task=>task.state==='loading')??[],errors=value?.tasks.filter(task=>task.state==='error')??[];
 if(!value||!pending.length&&!errors.length||dismissed&&!pending.length)return null;
 return <div className={`background-startup ${errors.length?'error':''}`} role={errors.length?'alert':'status'} aria-live="polite" aria-busy={pending.length>0}>
  <span><strong>{pending.length?'Loading background services':'Background service unavailable'}</strong><span>{[...pending,...errors].map(task=>task.error||task.message).join(' · ')}</span></span>
  {!pending.length&&<button type="button" aria-label="Dismiss background startup notice" onClick={()=>setDismissed(true)}>×</button>}
 </div>;
}

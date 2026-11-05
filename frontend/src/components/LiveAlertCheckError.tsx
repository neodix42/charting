import {useEffect,useId,useRef,useState} from 'react';
import {createPortal} from 'react-dom';
import {liveAlertErrorText} from '../workspace/screenerLiveAlerts';

/** Failed check status opens details without replacing the execution history. */
export function LiveAlertCheckError({message,label,status,visible}:{message:string;label:string;status:string;visible:boolean}){
 const [open,setOpen]=useState(false),dialog=useRef<HTMLElement>(null),close=useRef<HTMLButtonElement>(null),trigger=useRef<HTMLButtonElement>(null),id=useId();
 useEffect(()=>{if(!visible)setOpen(false);},[visible]);
 useEffect(()=>{
  if(!open||!visible)return;
  const origin=trigger.current;close.current?.focus();
  const key=(event:KeyboardEvent)=>{
   if(event.key==='Escape'){event.preventDefault();event.stopPropagation();setOpen(false);}
   if(event.key==='Tab'){
    const items=[...dialog.current?.querySelectorAll<HTMLElement>('button:not(:disabled),a[href],input:not(:disabled),select:not(:disabled),textarea:not(:disabled),[tabindex="0"]')??[]],first=items[0],last=items.at(-1);
    if(!items.includes(document.activeElement as HTMLElement)){event.preventDefault();(event.shiftKey?last:first)?.focus();}
    else if(event.shiftKey&&document.activeElement===first){event.preventDefault();last?.focus();}
    else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();first?.focus();}
   }
  };
  document.addEventListener('keydown',key,true);
  return()=>{document.removeEventListener('keydown',key,true);if(origin?.isConnected&&origin.getClientRects().length)origin.focus({preventScroll:true});};
 },[open,visible]);
 return <><button ref={trigger} type="button" className="live-alert-check-error" aria-haspopup="dialog" aria-expanded={open&&visible} aria-controls={open&&visible?id:undefined} title={`Show error: ${label}`} onClick={()=>setOpen(true)}>{status}</button>{open&&visible&&createPortal(<div className="modal-backdrop live-alert-check-backdrop" onMouseDown={event=>{if(event.target===event.currentTarget){event.preventDefault();setOpen(false);}}}><section ref={dialog} id={id} className="modal live-alert-check-dialog" role="dialog" aria-modal="true" aria-labelledby={`${id}-title`} aria-describedby={`${id}-message`}><header className="modal-header"><h2 id={`${id}-title`}>{label}</h2><button ref={close} type="button" aria-label="Close check error" onClick={()=>setOpen(false)}>×</button></header><div id={`${id}-message`} className="live-alert-check-message" aria-label={`Error details: ${label}`}>{liveAlertErrorText(message)}</div></section></div>,document.body)}</>;
}

import {useLayoutEffect,useRef,useState} from 'react';

export interface TabContextTarget {
 id:string;
 x:number;
 y:number;
 trigger:HTMLButtonElement;
}

interface Props {
 target:TabContextTarget;
 tabName:string;
 canClose:boolean;
 onDismiss(restoreFocus?:boolean):void;
 onCloseTab():void;
}

export function TabContextMenu({target,tabName,canClose,onDismiss,onCloseTab}:Props){
 const menu=useRef<HTMLDivElement>(null);
 const [position,setPosition]=useState({left:target.x,top:target.y});
 const callbacks=useRef({onDismiss});callbacks.current={onDismiss};
 useLayoutEffect(()=>{
  const element=menu.current!;
  const bounds=element.getBoundingClientRect(),margin=8;
  setPosition({left:Math.max(margin,Math.min(target.x,window.innerWidth-bounds.width-margin)),top:Math.max(margin,Math.min(target.y,window.innerHeight-bounds.height-margin))});
  (element.querySelector<HTMLButtonElement>('button:not(:disabled)')??element).focus({preventScroll:true});
  const outside=(event:PointerEvent)=>{if(!element.contains(event.target as Node))callbacks.current.onDismiss(false);};
  const dismiss=()=>callbacks.current.onDismiss(false);
  document.addEventListener('pointerdown',outside,true);
  window.addEventListener('resize',dismiss);
  window.addEventListener('scroll',dismiss,true);
  window.addEventListener('blur',dismiss);
  return()=>{document.removeEventListener('pointerdown',outside,true);window.removeEventListener('resize',dismiss);window.removeEventListener('scroll',dismiss,true);window.removeEventListener('blur',dismiss);};
 },[target]);
 return <div id="tab-context-menu" className="tab-context-menu" ref={menu} role="menu" aria-label={`Actions for ${tabName}`} tabIndex={-1} style={position} onContextMenu={event=>event.preventDefault()} onKeyDown={event=>{
  if(event.key==='Escape'){event.preventDefault();event.stopPropagation();onDismiss(true);}
  else if(event.key==='Tab'){onDismiss(false);}
  else if(['ArrowDown','ArrowUp','Home','End'].includes(event.key)){event.preventDefault();menu.current?.querySelector<HTMLButtonElement>('button:not(:disabled)')?.focus();}
 }}>
  <button type="button" role="menuitem" className="danger" disabled={!canClose} title={canClose?'Remove this tab and all its charts':'Keep at least one tab'} onClick={onCloseTab}>Close tab</button>
 </div>;
}

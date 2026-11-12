import {useEffect,useRef,useState} from 'react';
import type {TradeHistoryBackend,TradeScreen} from '../workspace/tradeHistory';
import {errorText} from '../types';
import './TradeScreenViewer.css';

type ViewerBackend=Pick<TradeHistoryBackend,'GetTradeScreen'|'RemoveTradeScreen'>;
export interface TradeScreenViewerProps {
 api:ViewerBackend;
 screens:TradeScreen[];
 initialScreenId:string;
 label?:string;
 onClose():void;
 onRemoved(tradeId:string,screenId:string,warning?:string):void;
}
const key=(screen:TradeScreen)=>`${screen.tradeId}/${screen.id}`;
export function TradeScreenViewer({api,screens,initialScreenId,label,onClose,onRemoved}:TradeScreenViewerProps){
 const [screenId,setScreenId]=useState(initialScreenId),[removed,setRemoved]=useState<string[]>([]),[image,setImage]=useState(''),[loading,setLoading]=useState(true),[loadError,setLoadError]=useState(''),[removeError,setRemoveError]=useState(''),[warning,setWarning]=useState(''),[busy,setBusy]=useState(false),[actual,setActual]=useState(false),[retry,setRetry]=useState(0);
 const dialog=useRef<HTMLDivElement>(null),mounted=useRef(true),removing=useRef(false),request=useRef<{key:string;api:ViewerBackend;promise:Promise<string>}|null>(null);
 const entries=screens.filter(screen=>!removed.includes(key(screen))),index=entries.findIndex(screen=>screen.id===screenId),current=entries[index];
 const tradeId=current?.tradeId??'',id=current?.id??'';
 useEffect(()=>{mounted.current=true;const previous=document.activeElement as HTMLElement|null;dialog.current?.querySelector<HTMLButtonElement>('button')?.focus();return()=>{mounted.current=false;if(previous?.isConnected)previous.focus();};},[]);
 useEffect(()=>{
  let active=true;setImage('');setLoadError('');setRemoveError('');setLoading(true);setActual(false);
  if(!tradeId||!id){setLoading(false);return;}
  const requestKey=`${tradeId}/${id}/${retry}`;
  if(!request.current||request.current.key!==requestKey||request.current.api!==api)request.current={key:requestKey,api,promise:Promise.resolve().then(()=>api.GetTradeScreen(tradeId,id))};
  void request.current.promise.then(url=>{if(active)setImage(url);}).catch(reason=>{if(active)setLoadError(errorText(reason));}).finally(()=>{if(active)setLoading(false);});
  return()=>{active=false;};
 },[api,tradeId,id,retry]);
 useEffect(()=>{if(busy){const target=dialog.current?.querySelector<HTMLElement>('button:not(:disabled),[tabindex="0"]');(target??dialog.current)?.focus();}},[busy,image,loadError]);
 const close=()=>{if(!removing.current)onClose();};
 const remove=async()=>{
  if(!current||!api.RemoveTradeScreen||removing.current)return;
  const selected=current,remaining=entries.filter(screen=>key(screen)!==key(selected));
  removing.current=true;setBusy(true);setRemoveError('');
  try{
   const result=await api.RemoveTradeScreen(selected.tradeId,selected.id);
   if(!result.removed)throw Error('The image was not removed. Try again.');
   // Keep the parent's trade/image snapshot consistent even if navigation
   // unmounted this viewer while the confirmed storage mutation was pending.
   onRemoved(selected.tradeId,selected.id,result.warning);
   if(!mounted.current)return;
   setRemoved(previous=>[...previous,key(selected)]);setWarning(result.warning??'');
   if(remaining.length)setScreenId(remaining[Math.min(index,remaining.length-1)].id);else onClose();
  }catch(reason){if(mounted.current)setRemoveError(errorText(reason));}
  finally{removing.current=false;if(mounted.current)setBusy(false);}
 };
 const attribution=label|| (current?`${current.provider} ${current.instrument} · ${current.orderId||current.tradeId}`:'Trade screen');
 return <div className="modal-backdrop trade-screen-viewer-backdrop" onMouseDown={event=>{if(event.target===event.currentTarget){event.preventDefault();close();}}} onKeyDown={event=>{
  if(event.key==='Escape'){event.preventDefault();event.stopPropagation();close();}
  if(event.key==='Tab'){const elements=dialog.current?.querySelectorAll<HTMLElement>('button:not(:disabled),[tabindex="0"]');if(!elements?.length){event.preventDefault();dialog.current?.focus();}else{const first=elements[0],last=elements[elements.length-1],focused=document.activeElement;if(focused===dialog.current||!dialog.current?.contains(focused)){event.preventDefault();(event.shiftKey?last:first).focus();}else if(event.shiftKey&&focused===first){event.preventDefault();last.focus();}else if(!event.shiftKey&&focused===last){event.preventDefault();first.focus();}}}
 }}><div ref={dialog} tabIndex={-1} role="dialog" aria-modal="true" aria-label="Trade screen" className="modal trade-screen-dialog trade-screen-viewer trade-image-viewer" style={{width:`min(${Math.max(520,(current?.width??1000)+52)}px,94vw)`,height:`min(${Math.max(360,(current?.height??650)+230)}px,90vh)`}}>
  <header><h2>{current?.fileName||'Trade screen'}</h2><button type="button" aria-label="Close screens window" disabled={busy} onClick={close}>×</button></header>
  <p className="trade-screen-attribution">{attribution}</p>
  <div className="trade-screen-controls"><button type="button" disabled={busy||index<=0} onClick={()=>setScreenId(entries[index-1].id)}>Previous image</button><span>{index+1} / {entries.length}</span><button type="button" disabled={busy||index<0||index>=entries.length-1} onClick={()=>setScreenId(entries[index+1].id)}>Next image</button><button type="button" disabled={!image||busy} aria-pressed={actual} onClick={()=>setActual(value=>!value)}>{actual?'Fit to window':'Actual size'}</button><button type="button" className="danger" disabled={busy||!current||!api.RemoveTradeScreen} onClick={()=>void remove()}>{busy?'Removing image…':'Remove image'}</button></div>
  {current&&<p className="trade-screen-details">{current.width} × {current.height} pixels · {(current.bytes/1024).toLocaleString(undefined,{maximumFractionDigits:1})} KiB · Drag the lower-right corner to resize.</p>}
  {loading&&<p role="status">Loading image…</p>}
  {image&&!loadError&&<div className={`trade-screen-image ${actual?'actual-size':''}`} tabIndex={0}><img src={image} alt={`Screen ${index+1} for ${attribution}: ${current?.fileName??''}`} onError={()=>setLoadError('The saved image could not be displayed.')}/></div>}
  {loadError&&<><p role="alert" className="inline-error">{loadError}</p><button type="button" disabled={busy} onClick={()=>setRetry(value=>value+1)}>Retry image</button></>}
  {removeError&&<p role="alert" className="inline-error">{removeError}</p>}
  {warning&&<p role="status" className="inline-error">{warning}</p>}
 </div></div>;
}

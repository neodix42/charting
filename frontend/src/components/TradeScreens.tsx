import {useCallback,useEffect,useRef,useState} from 'react';
import type {TradeHistoryBackend,TradeScreen} from '../workspace/tradeHistory';
import {errorText} from '../types';
import {TradeScreenViewer} from './TradeScreenViewer';

const maxBytes=32*1024*1024;
type Mode={kind:'add'}|{kind:'view';id:string}|null;
type Upload={name:string;save:()=>Promise<TradeScreen>};
function transferredFiles(data:DataTransfer):File[]{
 const files=Array.from(data.files??[]);
 return files.length?files:Array.from(data.items??[]).filter(item=>item.kind==='file').map(item=>item.getAsFile()).filter((file):file is File=>file!==null);
}
const PictureIcon=()=> <svg width="18" height="18" viewBox="0 0 20 20" fill="none" aria-hidden="true"><rect x="2" y="3" width="16" height="14" rx="2" stroke="currentColor"/><circle cx="7" cy="7" r="1.5" fill="currentColor"/><path d="m3 15 5-5 3 3 3-5 4 7" stroke="currentColor"/></svg>;
function originalImage(file:File):Promise<string>{
 if(file.size===0||file.size>maxBytes)return Promise.reject(Error('Choose an image up to 32 MiB.'));
 return new Promise((resolve,reject)=>{const reader=new FileReader();reader.onerror=()=>reject(Error('The image could not be read.'));reader.onload=()=>{const url=String(reader.result);const image=new Image();image.onload=()=>resolve(url);image.onerror=()=>reject(Error('This file could not be opened as an image.'));image.src=url;};reader.readAsDataURL(file);});
}

export function TradeScreens({api,tradeId,label,screens,onAdded,onRemoved}:{api:TradeHistoryBackend;tradeId:string;label:string;screens:TradeScreen[];onAdded:(screen:TradeScreen)=>void;onRemoved?:(tradeId:string,screenId:string,warning?:string)=>void}){
 const [mode,setMode]=useState<Mode>(null),[busy,setBusy]=useState(false),[error,setError]=useState('');
 const dialog=useRef<HTMLDivElement>(null),drop=useRef<HTMLDivElement>(null),picker=useRef<HTMLInputElement>(null),mounted=useRef(true),saving=useRef(false),pasting=useRef(false),queue=useRef<Upload[]>([]);
 useEffect(()=>{mounted.current=true;return()=>{mounted.current=false;queue.current=[];};},[]);
 const kind=mode?.kind;
 useEffect(()=>{if(kind!=='add')return;const previous=document.activeElement as HTMLElement|null;drop.current?.focus();return()=>{if(previous?.isConnected)previous.focus();};},[kind]);
 const enqueue=useCallback(async(uploads:Upload[])=>{
  queue.current.push(...uploads);
  if(saving.current)return;
  saving.current=true;setBusy(true);setError('');const failures:string[]=[];
  try{while(queue.current.length&&mounted.current){const upload=queue.current.shift()!;try{const screen=await upload.save();if(mounted.current)onAdded(screen);}catch(reason){failures.push(`${upload.name||'Image'}: ${errorText(reason)}`);}}}
  finally{saving.current=false;if(mounted.current){setBusy(false);if(failures.length)setError(failures.join(' · '));else setMode(null);}}
 },[onAdded]);
 const add=(files:File[])=>enqueue(files.map(file=>({name:file.name,save:async()=>api.AddTradeScreen(tradeId,file.name,await originalImage(file))})));
 const paste=async()=>{
  if(pasting.current)return;
  pasting.current=true;
  try{
   const files:File[]=[];let browserFailure:unknown=Error('Clipboard access is unavailable. Use Ctrl+V / ⌘V here, or choose an image file.');
   try{
    if(navigator.clipboard?.read){const items=await navigator.clipboard.read();for(const item of items){const type=item.types.find(value=>value.startsWith('image/'));if(type){const blob=await item.getType(type);files.push(new File([blob],`Clipboard.${type.split('/')[1]}`,{type}));}}browserFailure=Error('The clipboard has no readable image. Copy an image or a screenshot, then try again.');}
   }catch(reason){browserFailure=reason;}
   if(files.length){await add(files);return;}
   if(api.PasteTradeScreen){await enqueue([{name:'Clipboard',save:()=>api.PasteTradeScreen!(tradeId)}]);return;}
   throw browserFailure;
  }catch(reason){if(mounted.current)setError(errorText(reason));}finally{pasting.current=false;}
 };
 const runtime=window.runtime,nativeDrop=!!(runtime?.OnFileDrop&&runtime.OnFileDropOff&&api.AddTradeScreenFile);
 useEffect(()=>{
  if(kind!=='add'||!nativeDrop)return;
  runtime!.OnFileDrop!((x,y,paths)=>{const bounds=drop.current?.getBoundingClientRect();if(!bounds||x<bounds.left||x>bounds.right||y<bounds.top||y>bounds.bottom||!paths.length)return;void enqueue(paths.map(path=>({name:path.split(/[\\/]/).at(-1)||'Image',save:()=>api.AddTradeScreenFile!(tradeId,path)})));},true);
  return()=>runtime!.OnFileDropOff!();
 },[kind,nativeDrop,runtime,api,tradeId,enqueue]);
 const close=()=>{if(!saving.current){setMode(null);setError('');}};
 return <td className="trade-screens-cell"><div className="trade-screen-icons">
  <button type="button" className="trade-screen-icon" aria-label={`Add screen for ${label}`} title="Paste or drop an image" onClick={()=>{setError('');setMode({kind:'add'});}}>+</button>
  {screens.map((screen,i)=><button type="button" key={screen.id} className="trade-screen-icon" aria-label={`Open screen ${i+1} for ${label}`} title={screen.fileName} onClick={()=>setMode({kind:'view',id:screen.id})}><PictureIcon/></button>)}
 </div>{mode?.kind==='view'&&<TradeScreenViewer api={api} screens={screens} initialScreenId={mode.id} label={label} onClose={close} onRemoved={(id,screenId,warning)=>onRemoved?.(id,screenId,warning)}/>}
 {mode?.kind==='add'&&<div className="modal-backdrop" onKeyDown={event=>{
  if(event.key==='Escape'){event.preventDefault();event.stopPropagation();close();}
  if(event.key==='Tab'){const elements=dialog.current?.querySelectorAll<HTMLElement>('button:not(:disabled),input:not(:disabled):not([type=file]),[tabindex="0"]');if(elements?.length){const first=elements[0],last=elements[elements.length-1];if(event.shiftKey&&document.activeElement===first){event.preventDefault();last.focus();}else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();first.focus();}}}
 }}><div ref={dialog} role="dialog" aria-modal="true" aria-label="Add trade screens" className="modal trade-screen-dialog" onPaste={event=>{event.preventDefault();if(pasting.current)return;const files=transferredFiles(event.clipboardData);if(files.length)void add(files);else void paste();}}>
  <header><h2>Add screens</h2><button type="button" aria-label="Close screens window" disabled={busy} onClick={close}>×</button></header>
  <p className="trade-screen-attribution">{label}</p>
   <div ref={drop} tabIndex={0} className="trade-screen-drop" aria-label="Paste or drop images" onDragOver={event=>{event.preventDefault();event.dataTransfer.dropEffect='copy';}} onDrop={event=>{event.preventDefault();if(nativeDrop)return;event.stopPropagation();const files=transferredFiles(event.dataTransfer);if(files.length)void add(files);else setError('No readable image file was dropped. Try dropping the file from your file manager, or choose an image.');}}>
    <strong>Paste an image here or drag and drop images</strong><p>PNG, JPEG, GIF, WebP or BMP · up to 32 MiB each</p><small>Original files and dimensions are preserved.</small>
   </div>
   <input ref={picker} type="file" hidden multiple accept="image/png,image/jpeg,image/gif,image/webp,image/bmp" aria-label="Choose trade images" onChange={event=>{const files=Array.from(event.target.files??[]);event.target.value='';if(files.length)void add(files);}}/>
   <div className="trade-screen-controls"><button type="button" disabled={busy} onClick={()=>void paste()}>Paste image</button><button type="button" disabled={busy} onClick={()=>picker.current?.click()}>Choose images…</button></div>
   {busy&&<p role="status">Saving images…</p>}
  {error&&<p role="alert" className="inline-error">{error}</p>}
 </div></div>}</td>;
}

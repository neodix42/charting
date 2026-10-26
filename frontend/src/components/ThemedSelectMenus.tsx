import {Fragment,useEffect,useId,useLayoutEffect,useRef,useState} from 'react';
import {createPortal} from 'react-dom';
import './ThemedSelectMenus.css';

interface Choice {index:number;label:string;disabled:boolean;group?:HTMLOptGroupElement}
interface Menu {select:HTMLSelectElement;label:string;items:Choice[];active:number;selected:number;font:string}

function choices(select:HTMLSelectElement):Choice[]{
 return [...select.options].flatMap((option,index)=>{
  const group=option.parentElement instanceof HTMLOptGroupElement?option.parentElement:undefined;
  return option.hidden||group?.hidden?[]:[{index,label:option.label,disabled:option.disabled||!!group?.disabled,group}];
 });
}
function selectable(select:HTMLSelectElement){return select.isConnected&&!select.matches(':disabled')&&!select.multiple&&select.size<=1&&select.getClientRects().length>0;}

/** Keep real selects and their change events, but render their popup inside the
 * webview. GTK/Windows native popup windows can ignore the document palette. */
export function ThemedSelectMenus(){
 const [menu,setMenu]=useState<Menu|null>(null),current=useRef(menu),popup=useRef<HTMLDivElement>(null),menuID=useId();
 const restoreAttributes=useRef<(()=>void)|null>(null),search=useRef({text:'',time:0});current.current=menu;
 const close=useRef((focus=false)=>{const previous=current.current;current.current=null;setMenu(null);restoreAttributes.current?.();restoreAttributes.current=null;if(focus&&previous?.select.isConnected)previous.select.focus({preventScroll:true});});
 const move=useRef((direction:number)=>{
  const state=current.current;if(!state)return;
  let index=state.active;
  for(let count=0;count<state.items.length;count++){index=(index+direction+state.items.length)%state.items.length;if(!state.items[index].disabled){const next={...state,active:index};current.current=next;setMenu(next);return;}}
 });
 const choose=useRef((position:number)=>{
  const state=current.current;if(!state)return;
  const item=state.items[position],option=item&&state.select.options[item.index];
  if(!item||item.disabled||!option||option.disabled||!selectable(state.select))return;
  close.current(true);
  Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,'selectedIndex')!.set!.call(state.select,item.index);
  state.select.dispatchEvent(new Event('input',{bubbles:true}));state.select.dispatchEvent(new Event('change',{bubbles:true}));
 });
 useEffect(()=>{
  let pointerSelect:HTMLSelectElement|null=null,pointerTime=0;
  const open=(select:HTMLSelectElement)=>{
   if(!selectable(select))return;
   close.current();const items=choices(select);if(!items.length)return;
   let active=items.findIndex(item=>item.index===select.selectedIndex&&!item.disabled);if(active<0)active=items.findIndex(item=>!item.disabled);if(active<0)return;
   const attributes=['aria-expanded','aria-controls','aria-activedescendant'].map(name=>[name,select.getAttribute(name)] as const);
   restoreAttributes.current=()=>{for(const [name,value] of attributes){if(value===null)select.removeAttribute(name);else select.setAttribute(name,value);}};
   const value:Menu={select,label:select.getAttribute('aria-label')||select.labels?.[0]?.textContent?.trim()||'Select',items,active,selected:select.selectedIndex,font:getComputedStyle(select).font};
   select.setAttribute('aria-expanded','true');select.setAttribute('aria-controls',menuID);select.focus({preventScroll:true});
   current.current=value;setMenu(value);search.current={text:'',time:0};
  };
  const selectFor=(target:EventTarget|null)=>target instanceof Element?target.closest('select'):null;
  const pointer=(event:PointerEvent)=>{
   const select=selectFor(event.target);
   if(select){if(event.button!==0||!selectable(select))return;event.preventDefault();pointerSelect=select;pointerTime=Date.now();if(current.current?.select===select)close.current(true);else open(select);}
   else if(current.current&&!popup.current?.contains(event.target as Node))close.current();
  };
  const mouse=(event:MouseEvent)=>{
   const select=selectFor(event.target);if(!select||event.button!==0||!selectable(select))return;
   event.preventDefault();if(pointerSelect===select&&Date.now()-pointerTime<600)return;
   pointerSelect=select;pointerTime=Date.now();if(current.current?.select===select)close.current(true);else open(select);
  };
  const click=(event:MouseEvent)=>{
   const select=selectFor(event.target);if(!select||!selectable(select))return;
   event.preventDefault();if(pointerSelect===select&&Date.now()-pointerTime<600)return;if(!current.current)open(select);
  };
  const keyboard=(event:KeyboardEvent)=>{
   const state=current.current,select=selectFor(event.target);
   if(!state){if(select&&selectable(select)&&!event.ctrlKey&&!event.metaKey&&['ArrowDown','ArrowUp','Enter',' '].includes(event.key)){event.preventDefault();event.stopPropagation();open(select);}return;}
   if(event.target!==state.select&&!popup.current?.contains(event.target as Node))return;
   if(event.key==='Tab'){close.current();return;}
   if(event.key==='Escape'){event.preventDefault();event.stopPropagation();close.current(true);return;}
   if(event.ctrlKey||event.metaKey||event.altKey)return;
   if(['ArrowDown','ArrowUp','Home','End','PageDown','PageUp','Enter',' '].includes(event.key)){
    event.preventDefault();event.stopPropagation();
    if(event.key==='Enter'||event.key===' ')choose.current(state.active);
    else if(event.key==='Home'||event.key==='End'){
     const indexes=state.items.map((item,index)=>item.disabled?-1:index).filter(index=>index>=0),active=event.key==='Home'?indexes[0]:indexes.at(-1)!;
     const next={...state,active};current.current=next;setMenu(next);
    }else{const direction=event.key==='ArrowUp'||event.key==='PageUp'?-1:1;for(let count=0;count<(event.key.startsWith('Page')?10:1);count++)move.current(direction);}
   }else if(event.key.length===1){
    event.preventDefault();event.stopPropagation();const now=Date.now();search.current={text:(now-search.current.time<700?search.current.text:'')+event.key.toLocaleLowerCase(),time:now};
    let prefix=search.current.text;if([...prefix].every(char=>char===prefix[0]))prefix=prefix[0];
    for(let step=prefix.length>1?0:1;step<=state.items.length;step++){const active=(state.active+step)%state.items.length,item=state.items[active];if(!item.disabled&&item.label.toLocaleLowerCase().startsWith(prefix)){const next={...state,active};current.current=next;setMenu(next);break;}}
   }
  };
  const focus=(event:FocusEvent)=>{if(current.current&&event.target!==current.current.select&&!popup.current?.contains(event.target as Node))close.current();};
  const change=(event:Event)=>{if(event.target===current.current?.select)close.current();};
  const blur=()=>close.current();
  document.addEventListener('pointerdown',pointer,true);document.addEventListener('mousedown',mouse,true);document.addEventListener('click',click,true);document.addEventListener('keydown',keyboard,true);document.addEventListener('focusin',focus,true);document.addEventListener('change',change,true);window.addEventListener('blur',blur);
  return()=>{document.removeEventListener('pointerdown',pointer,true);document.removeEventListener('mousedown',mouse,true);document.removeEventListener('click',click,true);document.removeEventListener('keydown',keyboard,true);document.removeEventListener('focusin',focus,true);document.removeEventListener('change',change,true);window.removeEventListener('blur',blur);restoreAttributes.current?.();restoreAttributes.current=null;};
 },[menuID]);
 useLayoutEffect(()=>{
  if(!menu||!popup.current)return;
  const select=menu.select,element=popup.current;
  const position=()=>{
   if(!selectable(select)){close.current();return;}
   const anchor=select.getBoundingClientRect();element.style.minWidth=`${Math.min(anchor.width,window.innerWidth-16)}px`;
   const bounds=element.getBoundingClientRect(),below=window.innerHeight-anchor.bottom-8,above=anchor.top-8;
   element.style.left=`${Math.max(8,Math.min(anchor.left,window.innerWidth-bounds.width-8))}px`;
   element.style.top=`${Math.max(8,Math.min(below<bounds.height&&above>below?anchor.top-bounds.height-4:anchor.bottom+4,window.innerHeight-bounds.height-8))}px`;
  };
  const scroll=(event:Event)=>{if(!element.contains(event.target as Node))close.current();};
  const mutation=new MutationObserver(()=>{if(!selectable(select))close.current();});mutation.observe(document.body,{childList:true,subtree:true});mutation.observe(select,{attributes:true,attributeFilter:['disabled'],subtree:true});
  const resize=new ResizeObserver(position);resize.observe(select);resize.observe(element);window.addEventListener('resize',position);document.addEventListener('scroll',scroll,true);position();
  return()=>{mutation.disconnect();resize.disconnect();window.removeEventListener('resize',position);document.removeEventListener('scroll',scroll,true);};
 },[menu?.select]);
 useLayoutEffect(()=>{
  if(!menu)return;menu.select.setAttribute('aria-activedescendant',`${menuID}-option-${menu.items[menu.active].index}`);
  document.getElementById(`${menuID}-option-${menu.items[menu.active].index}`)?.scrollIntoView({block:'nearest'});
 },[menu?.active,menu?.select,menuID]);
 if(!menu)return null;
 const groups:Array<{group?:HTMLOptGroupElement;items:Array<{choice:Choice;position:number}>}>=[];
 menu.items.forEach((choice,position)=>{if(!groups.length||groups.at(-1)!.group!==choice.group)groups.push({group:choice.group,items:[]});groups.at(-1)!.items.push({choice,position});});
 const options=(items:typeof groups[number]['items'])=>items.map(({choice,position})=><div key={choice.index} id={`${menuID}-option-${choice.index}`} role="option" aria-selected={choice.index===menu.selected} aria-disabled={choice.disabled||undefined} className="themed-select-option" data-active={position===menu.active||undefined} onPointerDown={event=>event.preventDefault()} onPointerEnter={()=>{if(!choice.disabled){const next={...menu,active:position};current.current=next;setMenu(next);}}} onClick={()=>choose.current(position)}><span>{choice.label}</span>{choice.index===menu.selected&&<span className="themed-select-check" aria-hidden="true">✓</span>}</div>);
 return createPortal(<div ref={popup} id={menuID} role="listbox" aria-label={`${menu.label} options`} className="themed-select-menu" style={{font:menu.font}} onMouseDown={event=>event.preventDefault()}>{groups.map(({group,items},index)=>group?<div key={index} role="group" aria-label={group.label} className="themed-select-group"><div className="themed-select-group-heading">{group.label}</div>{options(items)}</div>:<Fragment key={index}>{options(items)}</Fragment>)}</div>,document.body);
}

import {useEffect,useRef,useState,type ReactNode} from 'react';
import {createPortal} from 'react-dom';
import type {Exchange,Market} from '../types';
import './AssetPicker.css';

export interface AssetChoice {id:string;label:string;details:string;market?:Market;search:string}
export const marketLabel=(market:Market)=>({spot:'Spot',perpetual:'Perpetual',forex:'Forex',metal:'Metal',cfd:'CFD'})[market];
const filters=(exchange:Exchange):Market[]=>exchange==='dukascopy'?['forex','cfd','metal']:exchange==='hyperliquid'?['perpetual','spot']:[];

export function AssetPicker({exchange,choices,selected,label,buttonLabel='Select instrument',panelLabel='Instruments',title,disabled=false,active=true,loading=false,empty='No available instruments',notice,className='',placement='inline',onSelect}:{exchange:Exchange;choices:AssetChoice[];selected:string;label:string;buttonLabel?:string;panelLabel?:string;title?:string;disabled?:boolean;active?:boolean;loading?:boolean;empty?:string;notice?:ReactNode;className?:string;placement?:'inline'|'chart';onSelect(id:string):void}) {
 const [open,setOpen]=useState(false),[query,setQuery]=useState(''),[market,setMarket]=useState<Market|null>(null);
 const root=useRef<HTMLDivElement>(null),panel=useRef<HTMLDivElement>(null),button=useRef<HTMLButtonElement>(null);
 useEffect(()=>{if(!active)setOpen(false);},[active]);
 useEffect(()=>{setOpen(false);setQuery('');setMarket(null);},[exchange]);
 useEffect(()=>{
  if(!open)return;
  const outside=(event:PointerEvent)=>{if(event.button===1&&event.target instanceof Element&&event.target.closest('[role="tab"][data-tab-id]'))return;if(event.target instanceof Node&&!root.current?.contains(event.target)&&!panel.current?.contains(event.target))setOpen(false);};
  const escape=(event:KeyboardEvent)=>{if(event.key==='Escape'){event.preventDefault();event.stopPropagation();setOpen(false);button.current?.focus();}};
  document.addEventListener('pointerdown',outside,true);document.addEventListener('keydown',escape,true);
  return()=>{document.removeEventListener('pointerdown',outside,true);document.removeEventListener('keydown',escape,true);};
 },[open]);
 const queryText=query.trim().toLowerCase(),matches=choices.filter(choice=>(market===null||choice.market===market)&&`${choice.label} ${choice.details} ${choice.search} ${choice.market?marketLabel(choice.market):''}`.toLowerCase().includes(queryText));
 const markets=filters(exchange);
 const popup=<div ref={panel} className="instrument-picker" aria-label={panelLabel}>
   <div className="picker-search"><input autoFocus aria-label="Search instruments" placeholder="Search asset name or symbol…" value={query} onChange={event=>setQuery(event.target.value)} onKeyDown={event=>{if(event.key==='Enter')event.preventDefault();}}/><button type="button" aria-label="Close instrument selector" onClick={()=>{setOpen(false);button.current?.focus();}}>×</button></div>
   {markets.length>0&&<div className="picker-market-filters" role="group" aria-label="Market filter"><button type="button" aria-pressed={market===null} onClick={()=>setMarket(null)}>All</button>{markets.map(value=><button key={value} type="button" aria-pressed={market===value} onClick={()=>setMarket(value)}>{marketLabel(value)}</button>)}</div>}
   {notice}
   {!choices.length?<p>{loading?'Loading instruments…':empty}</p>:<div className="product-list">{matches.map(choice=><button type="button" key={choice.id} data-asset-id={choice.id} aria-label={`${choice.label}${choice.market?` ${marketLabel(choice.market)}`:''}`} aria-pressed={choice.id===selected} onClick={()=>{onSelect(choice.id);setOpen(false);button.current?.focus();}}><span className="product-details"><strong>{choice.label}</strong><small>{choice.details}</small></span>{choice.market&&<span className={`product-market ${choice.market}`}>{marketLabel(choice.market)}</span>}</button>)}{!matches.length&&<p>No matching instruments</p>}</div>}
  </div>;
 const portalTarget=placement==='chart'?root.current?.closest('.chart-cell'):null;
 return <div ref={root} className={`asset-selector ${className}`}>
  <button ref={button} type="button" className="instrument-button" aria-label={buttonLabel} aria-expanded={open} title={title} disabled={disabled} onClick={()=>{setOpen(!open);setQuery('');}}>{label}<span className="muted">⌄</span></button>
  {open&&(portalTarget?createPortal(popup,portalTarget):popup)}
 </div>;
}

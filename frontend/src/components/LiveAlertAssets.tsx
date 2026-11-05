import {useEffect,useId,useRef,useState} from 'react';
import {createPortal} from 'react-dom';
import type {Backend,Instrument} from '../types';
import {errorText} from '../types';
import {clearInstrumentCatalog,instruments} from '../bridge/instruments';
import type {SavedAnalysisCondition} from '../workspace/screenerAnalysis';
import {profitabilityLabel} from '../workspace/screenerAnalysis';
import type {LiveAlertConditionScope,LiveAlertDefinition} from '../workspace/screenerLiveAlerts';
import {liveAlertPeriodLabel,liveAlertSourceLabel} from '../workspace/screenerLiveAlerts';
import {publicLiveAlertAssetLabel,publicLiveAlertAssets} from '../workspace/liveAlertAssets';
import {LiveAlertError} from './LiveAlertError';

export function LiveAlertAssets({api,entry,condition,scope,origin,onClose}:{api:Backend;entry:LiveAlertDefinition;condition?:SavedAnalysisCondition;scope?:LiveAlertConditionScope;origin:HTMLElement;onClose():void}){
 const id=useId(),dialog=useRef<HTMLElement>(null),close=useRef<HTMLButtonElement>(null),[query,setQuery]=useState(''),[retry,setRetry]=useState(0);
 const [catalog,setCatalog]=useState<{source:string;items:Instrument[];error:string;warning:string}|null>(null),publicSource=entry.dataSource!=='dukascopy';
 useEffect(()=>{
  close.current?.focus();
  const key=(event:KeyboardEvent)=>{
   if(event.key==='Escape'){event.preventDefault();event.stopPropagation();onClose();}
   if(event.key==='Tab'){
    const items=[...dialog.current?.querySelectorAll<HTMLElement>('button:not(:disabled),input:not(:disabled),summary,a[href],[tabindex="0"]')??[]],first=items[0],last=items.at(-1);
    if(!items.includes(document.activeElement as HTMLElement)){event.preventDefault();(event.shiftKey?last:first)?.focus();}
    else if(event.shiftKey&&document.activeElement===first){event.preventDefault();last?.focus();}
    else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();first?.focus();}
   }
  };
  document.addEventListener('keydown',key,true);
  return()=>{document.removeEventListener('keydown',key,true);if(origin.isConnected&&origin.getClientRects().length)origin.focus({preventScroll:true});};
 },[]);
 useEffect(()=>{
  let active=true;setQuery('');setCatalog(null);
  if(publicSource){const source=entry.dataSource;void instruments(api,source).then(items=>{const eligible=publicLiveAlertAssets(items),warning=Array.from(new Set(items.map(item=>item.providerMetadata?.catalogWarning).filter(Boolean))).join(' ');if(active)setCatalog({source,items:eligible,error:'',warning});}).catch(error=>{if(active)setCatalog({source,items:[],error:errorText(error),warning:''});});}
  return()=>{active=false;};
 },[api,entry.dataSource,retry]);
 const current=catalog?.source===entry.dataSource?catalog:null,loading=publicSource&&!current;
 const warnings=current?.warning??'';
 const terms=query.trim().toLowerCase(),publicItems=current?.items.filter(item=>`${item.nativeId} ${publicLiveAlertAssetLabel(item)} ${item.market} ${item.baseAsset} ${item.quoteAsset} ${item.providerMetadata?.baseFullName??''} ${item.providerMetadata?.quoteFullName??''}`.toLowerCase().includes(terms))??[];
 const targets=scope?.targets.filter(target=>`${target.symbol} ${target.name} ${target.category} ${target.period}`.toLowerCase().includes(terms))??[];
 const retryAssets=()=>{clearInstrumentCatalog(entry.dataSource);setRetry(value=>value+1);};
 const title=`Monitored assets for ${entry.name}`;
 return createPortal(<div className="modal-backdrop live-alert-assets-backdrop" onMouseDown={event=>{if(event.target===event.currentTarget){event.preventDefault();onClose();}}}><section ref={dialog} id={id} className="modal live-alert-assets-dialog" role="dialog" aria-modal="true" aria-labelledby={`${id}-title`}><header className="modal-header"><h2 id={`${id}-title`}>{title}</h2><button ref={close} type="button" aria-label="Close monitored assets" onClick={onClose}>×</button></header><section aria-label={title} className="live-alert-assets-content"><p className="live-alert-assets-selection">{liveAlertSourceLabel(entry.dataSource)} · {liveAlertPeriodLabel(entry.period)} · {entry.enabled?'Enabled alert':'Disabled alert'}</p>{publicSource?<p>All tradable Spot and Perpetual assets on {liveAlertSourceLabel(entry.dataSource)}. This is the current catalog snapshot; the catalog is resolved again at check time. Opening this list downloads no history. Historical profitability is unavailable for these source markets; the Dukascopy Analysis profitability filter is not applied.</p>:<><p>{condition?.name??'Saved condition unavailable'} · Minimum profitability: {condition?.request.profitabilityFilter??0}%</p><p>Matching assets from the saved Analysis results for this alert’s period.</p>{!!condition?.analysis?.failedFiles&&<p>Analysis had {condition.analysis.failedFiles} failed files; monitoring uses its successfully analyzed matches.</p>}</>}{scope?.error&&<LiveAlertError label={title} message={scope.error}/>}{loading&&<p role="status">Loading {liveAlertSourceLabel(entry.dataSource)} assets…</p>}{current?.error&&<div className="live-alert-assets-error" role="alert"><p>{current.error}</p><button type="button" onClick={retryAssets}>Retry assets</button></div>}{warnings&&<div className="live-alert-assets-error" role="alert"><p>{warnings}</p><button type="button" onClick={retryAssets}>Retry assets</button></div>}{!publicSource&&!scope&&<p role="status">Loading saved monitoring scope…</p>}{(publicSource?!!current&&!current.error:!!scope)&&<><div className="live-alert-assets-toolbar"><input type="search" aria-label="Filter monitored assets" placeholder="Search symbols or asset names…" value={query} onChange={event=>setQuery(event.target.value)}/><span>{publicSource?`${publicItems.length} of ${current?.items.length??0} markets`:`${targets.length} of ${scope?.targets.length??0} assets`}</span></div><ul className="live-alert-assets-list">{publicSource?publicItems.map(item=><li key={`${item.market}|${item.nativeId}`}><div><strong>{publicLiveAlertAssetLabel(item)}</strong><small>{item.nativeId}{item.baseAsset&&item.quoteAsset?` · ${item.baseAsset} / ${item.quoteAsset}`:''}</small></div><span className={`product-market ${item.market}`}>{item.market==='perpetual'?'Perpetual':'Spot'}</span></li>):targets.map(target=><li key={`${target.symbol}|${target.period}`}><div><strong>{target.symbol.toUpperCase()} — {target.name}</strong><small>{target.period.toUpperCase()} · {target.category}</small>{target.profitability&&<small>{profitabilityLabel(target.profitability)}</small>}</div></li>)}</ul>{!(publicSource?publicItems:targets).length&&<p>{terms?'No matching assets.':publicSource?'No tradable Spot or Perpetual markets are available.':'No monitored assets are available for this alert.'}</p>}</>}</section></section></div>,document.body);
}

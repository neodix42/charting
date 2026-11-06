import {useEffect,useId,useRef,useState} from 'react';
import {createPortal} from 'react-dom';
import type {Backend,Instrument} from '../types';
import {errorText} from '../types';
import {clearInstrumentCatalog,instruments} from '../bridge/instruments';
import type {SavedAnalysisCondition} from '../workspace/screenerAnalysis';
import {profitabilityLabel} from '../workspace/screenerAnalysis';
import type {LiveAlertAssetRef,LiveAlertConditionScope,LiveAlertDefinition} from '../workspace/screenerLiveAlerts';
import {liveAlertPeriodLabel,liveAlertSourceLabel} from '../workspace/screenerLiveAlerts';
import {liveAlertAssetKey,liveAlertAssetSelection,publicLiveAlertAssetLabel,publicLiveAlertAssets} from '../workspace/liveAlertAssets';
import {LiveAlertError} from './LiveAlertError';

type AssetOption={asset:LiveAlertAssetRef;label:string;details:string[];category:string;available:boolean};
const selectionMap=(assets:LiveAlertAssetRef[]|undefined)=>assets?new Map(assets.map(asset=>[liveAlertAssetKey(asset),asset])):null;

export function LiveAlertAssets({api,entry,condition,scope,origin,onClose,onSave,disabled=false}:{api:Backend;entry:LiveAlertDefinition;condition?:SavedAnalysisCondition;scope?:LiveAlertConditionScope;origin:HTMLElement;onClose():void;onSave(assets:LiveAlertAssetRef[]|undefined):void;disabled?:boolean}){
 const id=useId(),dialog=useRef<HTMLElement>(null),close=useRef<HTMLButtonElement>(null),[query,setQuery]=useState(''),[retry,setRetry]=useState(0);
 const original=liveAlertAssetSelection(entry),signature=JSON.stringify(original?original.map(liveAlertAssetKey).sort():null),[draft,setDraft]=useState(()=>selectionMap(original));
 const [catalog,setCatalog]=useState<{source:string;items:Instrument[];error:string;warning:string}|null>(null),publicSource=entry.dataSource!=='dukascopy';
 useEffect(()=>{setDraft(selectionMap(liveAlertAssetSelection(entry)));},[entry.id,entry.dataSource,entry.conditionId,entry.period,signature]);
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
 const current=catalog?.source===entry.dataSource?catalog:null,loading=publicSource&&!current,warnings=current?.warning??'';
 const options=new Map<string,AssetOption>();
 if(publicSource){for(const item of current?.items??[]){const asset={market:item.market as 'spot'|'perpetual',symbol:item.nativeId};options.set(liveAlertAssetKey(asset),{asset,label:publicLiveAlertAssetLabel(item),details:[`${item.nativeId}${item.baseAsset&&item.quoteAsset?` · ${item.baseAsset} / ${item.quoteAsset}`:''}`],category:item.market==='perpetual'?'Perpetual':'Spot',available:true});}}
 else{for(const target of scope?.availableTargets??scope?.targets??[]){const asset={symbol:target.symbol},key=liveAlertAssetKey(asset),option:AssetOption=options.get(key)??{asset,label:`${target.symbol.toUpperCase()} — ${target.name}`,details:[],category:target.category,available:true};option.details.push(`${target.period.toUpperCase()} · ${target.category}${target.profitability?` · ${profitabilityLabel(target.profitability)}`:''}`);options.set(key,option);}}
 for(const asset of [...original??[],...draft?.values()??[]]){const key=liveAlertAssetKey(asset);if(!options.has(key))options.set(key,{asset,label:publicSource?asset.symbol:asset.symbol.toUpperCase(),details:['Not available in the current catalog or qualifying Analysis scope; it will be skipped.'],category:asset.market==='perpetual'?'Perpetual':asset.market==='spot'?'Spot':'Unavailable',available:false});}
 const all=[...options.values()],terms=query.trim().toLowerCase(),shown=all.filter(option=>[option.label,option.asset.symbol,option.asset.market,option.category,...option.details].join(' ').toLowerCase().includes(terms));
 const available=all.filter(option=>option.available),count=draft===null?available.length:draft.size,dirty=draft===null?!!original:!original||draft.size!==original.length||original.some(asset=>!draft.has(liveAlertAssetKey(asset)));
 const ready=publicSource?!!current&&!current.error:!!scope;
 const toggle=(option:AssetOption,checked:boolean)=>setDraft(previous=>{const next=previous?new Map(previous):new Map(available.map(item=>[liveAlertAssetKey(item.asset),item.asset]));const key=liveAlertAssetKey(option.asset);if(checked)next.set(key,option.asset);else next.delete(key);return next;});
 const title=`Monitored assets for ${entry.name}`;
 return createPortal(<div className="modal-backdrop live-alert-assets-backdrop" onMouseDown={event=>{if(event.target===event.currentTarget){event.preventDefault();onClose();}}}><section ref={dialog} id={id} className="modal live-alert-assets-dialog" role="dialog" aria-modal="true" aria-labelledby={`${id}-title`}>
  <header className="modal-header"><h2 id={`${id}-title`}>{title}</h2><button ref={close} type="button" aria-label="Close monitored assets" onClick={onClose}>×</button></header>
  <section aria-label={title} className="live-alert-assets-content">
   <p className="live-alert-assets-selection">{liveAlertSourceLabel(entry.dataSource)} · {liveAlertPeriodLabel(entry.period)} · {entry.enabled?'Enabled alert':'Disabled alert'}</p>
   {publicSource?<p>All tradable Spot and Perpetual assets on {liveAlertSourceLabel(entry.dataSource)} are enabled by default. Choose a custom set below, or Select all to include future listings too. The catalog is resolved again at check time. Opening this list downloads no history. Historical profitability is unavailable for these source markets; the Dukascopy Analysis profitability filter is not applied.</p>:<><p>{condition?.name??'Saved condition unavailable'} · Minimum profitability: {condition?.request.profitabilityFilter??0}%</p><p>Matching assets from the saved Analysis results for this alert’s period. All qualifying assets are enabled by default; required companion periods follow the selected assets.</p>{!!condition?.analysis?.failedFiles&&<p>Analysis had {condition.analysis.failedFiles} failed files; monitoring uses its successfully analyzed matches.</p>}</>}
   {scope?.error&&<LiveAlertError label={title} message={scope.error}/>}
   {loading&&<p role="status">Loading {liveAlertSourceLabel(entry.dataSource)} assets…</p>}
   {current?.error&&<div className="live-alert-assets-error" role="alert"><p>{current.error}</p><button type="button" onClick={()=>{clearInstrumentCatalog(entry.dataSource);setRetry(value=>value+1);}}>Retry assets</button></div>}
   {warnings&&<div className="live-alert-assets-error" role="alert"><p>{warnings}</p><p>Only listed markets can be chosen from this partial catalog.</p><button type="button" onClick={()=>{clearInstrumentCatalog(entry.dataSource);setRetry(value=>value+1);}}>Retry assets</button></div>}
   {!publicSource&&!scope&&<p role="status">Loading saved monitoring scope…</p>}
   {ready&&<>
    <div className="live-alert-assets-toolbar"><input type="search" aria-label="Filter monitored assets" placeholder="Search symbols or asset names…" value={query} onChange={event=>setQuery(event.target.value)}/><span>{count} selected · {available.length} available</span></div>
    <div className="live-alert-assets-actions"><button type="button" disabled={disabled||!available.length} onClick={()=>setDraft(null)}>Select all</button><button type="button" disabled={disabled||!all.length} onClick={()=>setDraft(new Map())}>Deselect all</button><small>{draft===null?'All current and future eligible assets':'Only the selected assets'}</small></div>
    <ul className="live-alert-assets-list">{shown.map(option=>{const key=liveAlertAssetKey(option.asset);return <li key={key}><label className="live-alert-asset-choice"><input type="checkbox" aria-label={`Monitor ${option.asset.symbol}${option.asset.market?` ${option.asset.market}`:''}`} disabled={disabled} checked={draft===null?option.available:draft.has(key)} onChange={event=>toggle(option,event.target.checked)}/><div><strong>{option.label}</strong>{option.details.map((detail,index)=><small key={index}>{detail}</small>)}</div></label><span className={`product-market ${option.asset.market??''}`}>{option.category}</span></li>;})}</ul>
    {!shown.length&&<p>{terms?'No matching assets.':publicSource?'No tradable Spot or Perpetual markets are available.':'No monitored assets are available for this alert.'}</p>}
    {!count&&<p>Choose at least one asset, or disable this alert to pause all its assets.</p>}
    <div className="modal-actions"><button type="button" onClick={onClose}>Cancel</button><button type="button" className="primary" disabled={disabled||!dirty||!count} onClick={()=>onSave(draft===null?undefined:[...draft.values()])}>Save assets</button></div>
   </>}
  </section>
 </section></div>,document.body);
}

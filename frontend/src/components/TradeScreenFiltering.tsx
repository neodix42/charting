import {useEffect,useRef,useState} from 'react';
import type {TradeHistoryBackend} from '../workspace/tradeHistory';
import {tradeMoney} from '../workspace/tradeHistory';
import type {TradeScreenGalleryItem,TradeScreenGalleryPage,TradeScreenGalleryQuery} from '../workspace/tradeScreenGallery';
import {errorText} from '../types';
import {TradeScreenViewer} from './TradeScreenViewer';
import './TradeScreenFiltering.css';

const pageSize=24;
const emptyFilters=()=>({conditions:[] as string[],directions:[] as ('long'|'short')[],outcomes:[] as ('won'|'lost')[],providers:[] as ('dukascopy'|'hyperliquid')[]});
type Filters=ReturnType<typeof emptyFilters>;
function toggle<T extends string>(values:T[],value:T):T[]{return values.includes(value)?values.filter(item=>item!==value):[...values,value];}

function ScreenThumbnail({api,item,onOpen}:{api:TradeHistoryBackend;item:TradeScreenGalleryItem;onOpen:()=>void}){
 const element=useRef<HTMLButtonElement>(null),[intersects,setIntersects]=useState(false),[image,setImage]=useState(''),[error,setError]=useState(''),[retry,setRetry]=useState(0);
 const {screen}=item;
 useEffect(()=>{
  const target=element.current;if(!target)return;
  if(typeof IntersectionObserver==='undefined'){setIntersects(true);return;}
  const observer=new IntersectionObserver(entries=>setIntersects(entries.some(entry=>entry.isIntersecting)),{rootMargin:'100px'});
  observer.observe(target);return()=>observer.disconnect();
 },[]);
 useEffect(()=>{
  setImage('');setError('');if(!intersects)return;
  let active=true;
  void api.GetTradeScreen(screen.tradeId,screen.id).then(value=>{if(active)setImage(value);}).catch(reason=>{if(active)setError(errorText(reason));});
  return()=>{active=false;};
 },[api,screen.tradeId,screen.id,intersects,retry]);
 const label=`Open screen ${screen.fileName} for ${screen.instrument}${item.entryCondition?` · ${item.entryCondition}`:''}`;
 const direction=screen.direction==='long'?'Long':screen.direction==='short'?'Short':'Direction unavailable';
 return <article className="trade-filter-card">
  <button ref={element} type="button" className="trade-filter-preview" aria-label={label} onClick={onOpen}>
   {image?<img src={image} alt={`${screen.instrument} · ${item.entryCondition||'No entry condition'}`} onError={()=>{setImage('');setError('The saved image could not be displayed.');}}/>:<span>{error?'Preview unavailable':intersects?'Loading image…':'Image'}</span>}
  </button>
  <div className="trade-filter-caption"><strong>{screen.instrument}</strong><span>{screen.provider==='dukascopy'?'Dukascopy':'Hyperliquid'} · {direction}</span><span className="trade-filter-condition">{item.entryCondition||'No entry condition'}</span>{item.outcome&&<span className={`trade-filter-outcome ${item.outcome}`}>{item.outcome==='won'?'Won':'Lost'} · {tradeMoney(item.profitLoss,item.currency)}</span>}</div>
  {error&&<div className="trade-filter-preview-error"><span>{error}</span><button type="button" onClick={()=>setRetry(value=>value+1)}>Retry preview</button></div>}
 </article>;
}

export function TradeScreenFiltering({api,visible,revision,onRemoved}:{api:TradeHistoryBackend;visible:boolean;revision:number;onRemoved:(tradeId:string,screenId:string,warning?:string)=>void}){
 const [filters,setFilters]=useState<Filters>(emptyFilters),[page,setPage]=useState(0),[info,setInfo]=useState<TradeScreenGalleryPage|null>(null),[loadedKey,setLoadedKey]=useState(''),[loadedRevision,setLoadedRevision]=useState(-1),[loading,setLoading]=useState(false),[error,setError]=useState(''),[notice,setNotice]=useState(''),[retry,setRetry]=useState(0),[screenId,setScreenId]=useState('');
 const active=Object.values(filters).some(group=>group.length>0),key=JSON.stringify({...filters,offset:page*pageSize,limit:pageSize});
 useEffect(()=>{
  if(!visible){setScreenId('');return;}
  let current=true;setLoading(true);setError('');
  const query:TradeScreenGalleryQuery=JSON.parse(key);
  if(!api.GetTradeScreenGallery){setLoading(false);setError('Trade image filtering is unavailable. Restart the updated desktop application.');return;}
  void api.GetTradeScreenGallery(query).then(result=>{
   if(!current)return;
   if(page>0&&page*pageSize>=result.total&&result.total>0){setPage(Math.ceil(result.total/pageSize)-1);return;}
   if(page>0&&result.total===0){setPage(0);return;}
   setInfo(result);setLoadedKey(key);setLoadedRevision(revision);
  }).catch(reason=>{if(current)setError(errorText(reason));}).finally(()=>{if(current)setLoading(false);});
  return()=>{current=false;};
 },[api,visible,key,page,revision,retry]);
 const choose=<K extends keyof Filters>(group:K,value:Filters[K][number])=>{setScreenId('');setPage(0);setFilters(previous=>({...previous,[group]:toggle(previous[group] as string[],value)}));};
 const clear=()=>{setFilters(emptyFilters());setPage(0);setScreenId('');setError('');setNotice('');setInfo(previous=>previous?{...previous,items:[],total:0,offset:0}:previous);};
 const current=loadedKey===key&&loadedRevision===revision;
 const items=visible&&active&&current?info?.items??[]:[];
 const total=active&&current?info?.total??0:0;
 const conditions=[...new Set([...(info?.conditions??[]),...filters.conditions])];
 return <section aria-label="Trade image filtering" hidden={!visible} className="trade-screen-filtering">
  <div className="trade-filter-toolbar" aria-label="Trade image filters">
   <div className="trade-filter-heading"><h2>Filtering</h2><button type="button" className="trade-filter-clear" onClick={clear}>Clear all</button></div>
   <div className="trade-filter-groups">
    <fieldset><legend>Entry conditions</legend><div className="trade-filter-chips">{conditions.map(condition=><button key={condition} type="button" className="trade-filter-chip" aria-pressed={filters.conditions.includes(condition)} onClick={()=>choose('conditions',condition)}>{condition}</button>)}{!loading&&!conditions.length&&<span className="trade-filter-no-conditions">No entry conditions recorded.</span>}</div></fieldset>
    <fieldset><legend>Direction</legend><div className="trade-filter-chips">{(['long','short'] as const).map(value=><button key={value} type="button" className="trade-filter-chip" aria-pressed={filters.directions.includes(value)} onClick={()=>choose('directions',value)}>{value==='long'?'Long':'Short'}</button>)}</div></fieldset>
    <fieldset><legend>Result</legend><div className="trade-filter-chips">{(['lost','won'] as const).map(value=><button key={value} type="button" className="trade-filter-chip" aria-pressed={filters.outcomes.includes(value)} title="Uses confirmed closed-trade profit/loss. Break-even, open and unconfirmed trades have no Won/Lost result." onClick={()=>choose('outcomes',value)}>{value==='lost'?'Lost':'Won'}</button>)}</div></fieldset>
    <fieldset><legend>Exchange</legend><div className="trade-filter-chips">{(['dukascopy','hyperliquid'] as const).map(value=><button key={value} type="button" className="trade-filter-chip" aria-pressed={filters.providers.includes(value)} onClick={()=>choose('providers',value)}>{value==='dukascopy'?'Dukascopy':'Hyperliquid'}</button>)}</div></fieldset>
   </div>
   <p>Combine filters across groups. Multiple choices within a group include either choice. Won/Lost uses confirmed closed-trade profit/loss.</p>
  </div>
  <div className="trade-filter-content">
   <div className="trade-filter-status" role="status">{loading?'Loading trade images…':active?`${total} ${total===1?'image':'images'}`:'Choose one or more filters to display trade images.'}</div>
   {notice&&<p className="inline-error" role="alert">{notice}</p>}
   {error&&<div className="inline-error" role="alert">{error} <button type="button" onClick={()=>setRetry(value=>value+1)}>Retry filtering</button></div>}
   {!!info?.errors.length&&<p className="inline-error" role="alert">{info.errors.join(' · ')}</p>}
   {active&&!loading&&!error&&!total&&<p>No images match the selected filters.</p>}
   <div className="trade-filter-grid">{items.map(item=><ScreenThumbnail key={`${item.screen.tradeId}:${item.screen.id}`} api={api} item={item} onOpen={()=>setScreenId(item.screen.id)}/>)}</div>
   {total>0&&<nav className="trade-filter-pages" aria-label="Trade image pages"><button type="button" disabled={page===0||loading} onClick={()=>setPage(value=>value-1)}>Previous images</button><span>{page*pageSize+1}–{Math.min((page+1)*pageSize,total)} of {total} images</span><button type="button" disabled={(page+1)*pageSize>=total||loading} onClick={()=>setPage(value=>value+1)}>Next images</button></nav>}
  </div>
  {screenId&&items.some(item=>item.screen.id===screenId)&&<TradeScreenViewer api={api} screens={items.map(item=>item.screen)} initialScreenId={screenId} onClose={()=>setScreenId('')} onRemoved={(tradeId,id,warning)=>{setScreenId('');setNotice(warning??'');setInfo(previous=>previous?{...previous,items:previous.items.filter(item=>item.screen.id!==id||item.screen.tradeId!==tradeId),total:Math.max(0,previous.total-1)}:previous);setRetry(value=>value+1);onRemoved(tradeId,id,warning);}}/>}
 </section>;
}

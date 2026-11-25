import {forwardRef,useEffect,useImperativeHandle,useRef,useState} from 'react';
import type {ComparisonAsset,ComparisonBackend,ComparisonCatalog,ComparisonResult,ComparisonView} from '../workspace/comparison';
import {comparisonColor,comparisonDate,comparisonLabel,comparisonSource,ComparisonViewWriter,copyComparisonView,defaultComparisonView} from '../workspace/comparison';
import {errorText,type Market} from '../types';
import {AssetPicker} from './AssetPicker';
import {ComparisonChart} from './ComparisonChart';
import './Comparison.css';

export interface ComparisonHandle {flush():Promise<void>}
export const Comparison=forwardRef<ComparisonHandle,{api:ComparisonBackend;visible:boolean;suspended?:boolean}>(function Comparison({api,visible,suspended=false},ref){
 const [view,setView]=useState<ComparisonView>(defaultComparisonView),[weeksInput,setWeeksInput]=useState('4'),[ready,setReady]=useState(false),[loading,setLoading]=useState(false),[catalog,setCatalog]=useState<ComparisonCatalog|null>(null),[result,setResult]=useState<ComparisonResult|null>(null);
 const [viewError,setViewError]=useState(''),[catalogError,setCatalogError]=useState(''),[loadError,setLoadError]=useState(''),[saveError,setSaveError]=useState(''),[reload,setReload]=useState(0),[catalogReload,setCatalogReload]=useState(0),[refresh,setRefresh]=useState(0);
 const [hiddenAssets,setHiddenAssets]=useState<ReadonlySet<string>>(()=>new Set());
 const mounted=useRef(false),draft=useRef(view),writer=useRef<ComparisonViewWriter|null>(null),epoch=useRef(0),request=useRef<string|null>(null);
 if(!writer.current)writer.current=new ComparisonViewWriter(async value=>{const saved=await api.SaveComparisonView(value);if(mounted.current)setSaveError('');return saved;},reason=>{if(mounted.current)setSaveError(errorText(reason));});
 useEffect(()=>{mounted.current=true;return()=>{mounted.current=false;epoch.current++;if(request.current)void api.CancelComparisonRequest(request.current).catch(()=>{});};},[]);
 useImperativeHandle(ref,()=>({flush:async()=>{
  if(ready){
   const weeks=Number(weeksInput);
   if(!Number.isInteger(weeks)||weeks<1||weeks>104)throw Error('Choose a whole number from 1 to 104 Comparison weeks.');
   if(weeks!==draft.current.weeks){const next={...draft.current,weeks};draft.current=next;setView(next);writer.current!.edit(next);}
  }
  await writer.current!.flush();
 }}),[ready,weeksInput]);
 useEffect(()=>{
  if(!visible||ready)return;
  let active=true;setViewError('');
  void api.GetComparisonView().then(saved=>{if(active){const value=copyComparisonView(saved);draft.current=value;setView(value);setWeeksInput(String(value.weeks));setReady(true);}}).catch(reason=>{if(active)setViewError(errorText(reason));});
  return()=>{active=false;};
 },[visible,ready,reload,api]);
 useEffect(()=>{
  if(!visible)return;
  let active=true;setCatalogError('');
  void api.GetComparisonCatalog().then(value=>{if(active)setCatalog(value);}).catch(reason=>{if(active)setCatalogError(errorText(reason));});
  return()=>{active=false;};
 },[visible,catalogReload,api]);
 useEffect(()=>{
  if(!visible||suspended||!ready||!catalog)return;
  const version=++epoch.current,id=`comparison-${crypto.randomUUID()}`;request.current=id;setLoadError('');
  if(!view.assets.length){setResult(null);setLoading(false);request.current=null;return;}
  setLoading(true);setResult(null);
  void api.GetComparisonDailyChanges(id,copyComparisonView(view)).then(value=>{if(mounted.current&&version===epoch.current){setResult(value);setLoading(false);}}).catch(reason=>{if(mounted.current&&version===epoch.current){setLoadError(errorText(reason));setLoading(false);}}).finally(()=>{if(request.current===id)request.current=null;});
  return()=>{epoch.current++;if(request.current===id)request.current=null;void api.CancelComparisonRequest(id).catch(()=>{});};
 },[visible,suspended,ready,catalog,view,refresh,api]);
 useEffect(()=>{
  if(!visible||suspended||!ready)return;
  // Closed daily observations change only after the next UTC day boundary.
  const next=(Math.floor(Date.now()/86400000)+1)*86400000+5000;
  const timer=setTimeout(()=>setRefresh(value=>value+1),next-Date.now());return()=>clearTimeout(timer);
 },[visible,suspended,ready,refresh]);
 const edit=(patch:Partial<ComparisonView>)=>{const next={...draft.current,...patch};if(JSON.stringify(next)===JSON.stringify(draft.current))return;draft.current=next;setView(next);writer.current!.edit(next);};
 const toggleAsset=(symbol:string)=>setHiddenAssets(previous=>{const next=new Set(previous);if(next.has(symbol))next.delete(symbol);else next.add(symbol);return next;});
 const weeks=Number(weeksInput),invalidWeeks=!Number.isInteger(weeks)||weeks<1||weeks>104;
 const selected=new Set(view.assets),choices=(catalog?.assets??[]).filter(asset=>!selected.has(asset.symbol)).map(asset=>({id:asset.symbol,label:comparisonLabel(asset),market:asset.market as Market,details:`${asset.name} · ${comparisonSource(asset)}`,search:`${asset.symbol} ${asset.name} ${asset.nativeId} ${asset.exchange}`}));
 const sourceFor=(symbol:string):ComparisonAsset|undefined=>result?.series.find(series=>series.asset.symbol===symbol)?.asset??catalog?.assets.find(asset=>asset.symbol===symbol);
 const seriesErrors=result?.series.filter(series=>series.error)??[],hasData=result?.series.some(series=>!series.error&&series.points.some(point=>point.pct!==null));
 return <section hidden={!visible} className="comparison" role="tabpanel" id="comparison-panel" aria-labelledby="comparison-tab">
  <header className="comparison-heading"><div><span className="analysis-eyebrow">ASSET PERFORMANCE</span><h1>Comparison</h1><p>Compare how assets have moved from the same starting close.</p></div><div className="comparison-subtabs" role="tablist" aria-label="Comparison sections"><button type="button" id="comparison-daily-tab" role="tab" aria-selected="true" aria-controls="comparison-daily-panel">Daily change</button></div></header>
  <div id="comparison-daily-panel" role="tabpanel" aria-labelledby="comparison-daily-tab">
   {viewError&&<p className="screener-error" role="alert">{viewError} <button type="button" onClick={()=>setReload(value=>value+1)}>Retry saved choices</button></p>}
   {saveError&&<p className="screener-error" role="alert">Could not save Comparison choices: {saveError} <button type="button" onClick={()=>void writer.current!.flush().catch(()=>{})}>Retry saving</button></p>}
   <div className="comparison-controls">
    <label>Weeks<input type="number" aria-label="Comparison weeks" min={1} max={104} step={1} value={weeksInput} disabled={!ready||suspended} onChange={event=>setWeeksInput(event.target.value)} onBlur={()=>{if(!invalidWeeks&&weeks!==view.weeks)edit({weeks});}} onKeyDown={event=>{if(event.key==='Enter'&&!invalidWeeks&&weeks!==view.weeks){edit({weeks});event.currentTarget.blur();}}}/></label>
    <div className="comparison-quick-weeks" aria-label="Common comparison periods">{[1,2,4,8,12,26,52].map(value=><button type="button" key={value} aria-pressed={view.weeks===value} disabled={!ready||suspended} onClick={()=>{setWeeksInput(String(value));edit({weeks:value});}}>{value}w</button>)}</div>
    <AssetPicker exchange="hyperliquid" active={visible} choices={choices} selected="" label="Add asset" buttonLabel="Add comparison asset" panelLabel="Comparison assets" title={view.assets.length>=20?'Up to 20 assets can be compared':'Add a Hyperliquid asset, or a Binance asset when absent from Hyperliquid'} loading={!catalog&&!catalogError} disabled={!ready||suspended||view.assets.length>=20} empty={catalogError?'Asset list unavailable':'All available assets are already selected'} onSelect={symbol=>edit({assets:[...draft.current.assets,symbol]})}/>
    <button type="button" disabled={!ready||loading||suspended||!view.assets.length||invalidWeeks} onClick={()=>{if(weeks!==view.weeks)edit({weeks});else setRefresh(value=>value+1);}}>Refresh</button>
    {loading&&<span className="muted" role="status">Loading daily closes…</span>}
    {invalidWeeks&&<p className="comparison-input-error" role="alert">Choose a whole number from 1 to 104 weeks.</p>}
   </div>
   <div className="comparison-assets" aria-label="Selected comparison assets">{view.assets.map(symbol=>{const asset=sourceFor(symbol);return <div className="comparison-asset" key={symbol}><i style={{background:comparisonColor(symbol)}}/><div><strong>{asset?comparisonLabel(asset):symbol}</strong><small>{asset?comparisonSource(asset):catalogError?'Source unavailable':'Resolving source…'}</small></div><button type="button" aria-label={`Remove ${symbol} from comparison`} disabled={!ready||suspended} onClick={()=>edit({assets:draft.current.assets.filter(value=>value!==symbol)})}>×</button></div>;})}</div>
   {catalogError&&<p className="screener-error" role="alert">{catalogError} <button type="button" onClick={()=>setCatalogReload(value=>value+1)}>Retry assets</button></p>}
   {catalog?.warnings.map(warning=><p className="comparison-warning" key={warning} role="status">{warning}</p>)}
   {loadError&&<p className="screener-error" role="alert">{loadError} <button type="button" onClick={()=>setRefresh(value=>value+1)}>Retry daily data</button></p>}
   {!!seriesErrors.length&&<ul className="comparison-series-errors" aria-label="Unavailable comparison assets">{seriesErrors.map(series=><li key={series.asset.symbol}><strong>{series.asset.symbol}</strong> · {series.error}</li>)}</ul>}
   {result?.warnings.map(warning=><p className="comparison-warning" key={warning} role="status">{warning}</p>)}
   {visible&&!suspended&&result&&hasData&&<ComparisonChart result={result} hiddenAssets={hiddenAssets} onToggleAsset={toggleAsset}/>}
   {ready&&!view.assets.length&&<div className="comparison-empty"><h2>Add assets to compare</h2><p>Choose assets using Add asset. Your weeks selection will be kept.</p></div>}
   {ready&&!loading&&result&&!hasData&&!loadError&&<div className="comparison-empty"><h2>No comparable daily data</h2><p>Each asset needs a real closing price on {comparisonDate(result.baselineTime)} to share the same starting point.</p></div>}
   <p className="comparison-note">Percentage change from the starting close, using completed UTC daily candles. Hyperliquid is preferred; Binance spot is used when no comparable Hyperliquid market is available. Changes are measured in each asset’s displayed quote currency; quotes are not converted. Missing closes remain gaps.</p>
  </div>
 </section>;
});

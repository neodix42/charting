import {forwardRef,useEffect,useImperativeHandle,useRef,useState} from 'react';
import type {ComparisonAsset,ComparisonBackend,ComparisonCatalog,ComparisonOverview,ComparisonResult,ComparisonView} from '../workspace/comparison';
import {comparisonColor,comparisonDate,comparisonExchange,comparisonFeedIdentity,comparisonIdentity,comparisonLabel,comparisonOptionName,comparisonSelectionAsset,comparisonSelectionLabel,comparisonSeriesLabel,comparisonSource,ComparisonViewWriter,copyComparisonView,defaultComparisonView} from '../workspace/comparison';
import {errorText,type Market} from '../types';
import {AssetPicker} from './AssetPicker';
import {ComparisonChart} from './ComparisonChart';
import './Comparison.css';

export interface ComparisonHandle {flush():Promise<void>}
export const Comparison=forwardRef<ComparisonHandle,{api:ComparisonBackend;visible:boolean;suspended?:boolean}>(function Comparison({api,visible,suspended=false},ref){
 const [view,setView]=useState<ComparisonView>(defaultComparisonView),[weeksInput,setWeeksInput]=useState('4'),[ready,setReady]=useState(false),[loading,setLoading]=useState(false),[catalog,setCatalog]=useState<ComparisonCatalog|null>(null),[result,setResult]=useState<ComparisonResult|null>(null);
 const [layout,setLayout]=useState<'overview'|'single'>('overview'),[overview,setOverview]=useState<ComparisonOverview|null>(null),[endTime,setEndTime]=useState(0),[utcRefresh,setUtcRefresh]=useState(0);
 const [viewError,setViewError]=useState(''),[catalogError,setCatalogError]=useState(''),[loadError,setLoadError]=useState(''),[saveError,setSaveError]=useState(''),[reload,setReload]=useState(0),[catalogReload,setCatalogReload]=useState(0),[refresh,setRefresh]=useState(0);
 const [hiddenAssets,setHiddenAssets]=useState<ReadonlySet<string>>(()=>new Set()),[pickerSource,setPickerSource]=useState('all');
 const mounted=useRef(false),draft=useRef(view),writer=useRef<ComparisonViewWriter|null>(null),epoch=useRef(0),request=useRef<string|null>(null);
 // Pinned historical windows stay fixed when the latest UTC day changes.
 const automaticRefresh=endTime===0?utcRefresh:0;
 if(!writer.current)writer.current=new ComparisonViewWriter(async value=>{const saved=await api.SaveComparisonView(value);if(mounted.current)setSaveError('');return saved;},reason=>{if(mounted.current)setSaveError(errorText(reason));});
 useEffect(()=>{mounted.current=true;return()=>{mounted.current=false;epoch.current++;if(request.current)void api.CancelComparisonRequest(request.current).catch(()=>{});};},[]);
 useImperativeHandle(ref,()=>({flush:async()=>{
  if(ready&&layout==='single'){
   const weeks=Number(weeksInput);
   if(!Number.isInteger(weeks)||weeks<1||weeks>104)throw Error('Choose a whole number from 1 to 104 Comparison weeks.');
   if(weeks!==draft.current.weeks){const next={...draft.current,weeks};draft.current=next;setView(next);writer.current!.edit(next);}
  }
  await writer.current!.flush();
 }}),[ready,weeksInput,layout]);
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
  setResult(null);setOverview(null);
  if(!view.assets.length){setLoading(false);request.current=null;return;}
  setLoading(true);
  const current=()=>mounted.current&&version===epoch.current;
  const selection=copyComparisonView(view);
  const pending=layout==='overview'?(endTime===0?api.GetComparisonOverview(id,selection):api.GetComparisonOverviewAt(id,selection,endTime)).then(value=>{if(current())setOverview(value);}):(endTime===0?api.GetComparisonDailyChanges(id,selection):api.GetComparisonDailyChangesAt(id,selection,endTime)).then(value=>{if(current())setResult(value);});
  void pending.then(()=>{if(current())setLoading(false);}).catch(reason=>{if(current()){setLoadError(errorText(reason));setLoading(false);}}).finally(()=>{if(request.current===id)request.current=null;});
  return()=>{epoch.current++;if(request.current===id)request.current=null;void api.CancelComparisonRequest(id).catch(()=>{});};
 },[visible,suspended,ready,catalog,view,layout,refresh,automaticRefresh,endTime,api]);
 useEffect(()=>{
  if(!visible||suspended||!ready)return;
  // Closed daily observations change only after the next UTC day boundary.
  const next=(Math.floor(Date.now()/86400000)+1)*86400000+5000;
  const timer=setTimeout(()=>setUtcRefresh(value=>value+1),next-Date.now());return()=>clearTimeout(timer);
 },[visible,suspended,ready,utcRefresh]);
 const edit=(patch:Partial<ComparisonView>)=>{const next={...draft.current,...patch};if(JSON.stringify(next)===JSON.stringify(draft.current))return;draft.current=next;setView(next);writer.current!.edit(next);};
 const toggleAsset=(symbol:string)=>setHiddenAssets(previous=>{const next=new Set(previous);if(next.has(symbol))next.delete(symbol);else next.add(symbol);return next;});
 const weeks=Number(weeksInput),invalidWeeks=!Number.isInteger(weeks)||weeks<1||weeks>104;
 const markets=catalog?.markets??catalog?.assets??[];
 const results=layout==='overview'?overview?.panels??[]:result?[result]:[];
 const day=86400,latestClosedTime=Math.floor(Date.now()/86400000)*day-day,through=endTime||results[0]?.lastClosedTime||latestClosedTime;
 const navigationDisabled=!ready||suspended||!view.assets.length,firstPossibleEnd=(layout==='overview'?12:view.weeks)*7*day;
 const advanceDate=()=>setEndTime(value=>{
  // A click can cross UTC midnight before the scheduled daily refresh runs.
  const latest=Math.floor(Date.now()/86400000)*day-day;
  return value===0||value+day>=latest?0:value+day;
 });
 const sourceFor=(id:string):ComparisonAsset|undefined=>{const asset=results.flatMap(panel=>panel.series).find(series=>comparisonIdentity(series.asset)===id)?.asset??[...markets,...(catalog?.assets??[])].find(asset=>comparisonIdentity(asset)===id);return asset?(asset.symbol===id&&id.includes('|')?{...asset,symbol:comparisonSelectionLabel(id)}:asset):comparisonSelectionAsset(id);};
 const selected=new Set(view.assets),selectedFeeds=new Set(view.assets.map(id=>sourceFor(id)).filter((asset):asset is ComparisonAsset=>!!asset).map(comparisonFeedIdentity).filter(id=>id!==undefined)),choices=markets.filter(asset=>!selected.has(comparisonIdentity(asset))&&!selectedFeeds.has(comparisonFeedIdentity(asset)??'')&&(pickerSource==='all'||asset.exchange===pickerSource)).map(asset=>({id:comparisonIdentity(asset),label:comparisonLabel(asset),ariaLabel:comparisonOptionName(asset),market:asset.market as Market,details:`${asset.name} · ${comparisonSource(asset)} · ${asset.nativeId}`,search:`${asset.symbol} ${asset.symbol==='TON'?'GRAM Toncoin':''} ${asset.name} ${asset.nativeId} ${asset.exchange}`}));
 const seriesErrors=result?.series.filter(series=>series.error)??[],hasData=result?.series.some(series=>!series.error&&series.points.some(point=>point.pct!==null));
 return <section hidden={!visible} className="comparison" data-layout={layout} role="tabpanel" id="comparison-panel" aria-labelledby="comparison-tab">
  <header className="comparison-heading"><div><span className="analysis-eyebrow">ASSET PERFORMANCE</span><h1>Comparison</h1><p>{layout==='overview'?'Compare asset performance over 2, 4, 8 and 12 weeks.':'Compare how assets have moved from the same starting close.'}</p></div><div className="comparison-subtabs" role="tablist" aria-label="Comparison sections"><button type="button" id="comparison-daily-tab" role="tab" aria-selected="true" aria-controls="comparison-daily-panel">Daily change</button></div></header>
  <div id="comparison-daily-panel" role="tabpanel" aria-labelledby="comparison-daily-tab">
   {viewError&&<p className="screener-error" role="alert">{viewError} <button type="button" onClick={()=>setReload(value=>value+1)}>Retry saved choices</button></p>}
   {saveError&&<p className="screener-error" role="alert">Could not save Comparison choices: {saveError} <button type="button" onClick={()=>void writer.current!.flush().catch(()=>{})}>Retry saving</button></p>}
   <div className="comparison-controls">
    <div className="comparison-quick-weeks comparison-date-navigation" role="group" aria-label="Comparison date navigation"><button type="button" disabled={navigationDisabled||through-day<firstPossibleEnd} onClick={()=>setEndTime(value=>(value||through)-day)}>Prev</button><button type="button" disabled={navigationDisabled} onClick={()=>{if(endTime===0)setRefresh(value=>value+1);else setEndTime(0);}}>Today</button><button type="button" disabled={navigationDisabled||endTime===0||endTime>=latestClosedTime} onClick={advanceDate}>Next</button></div>
    <span className="comparison-end-date">Through {comparisonDate(through)} UTC</span>
    <div className="comparison-quick-weeks" role="group" aria-label="Comparison layout"><button type="button" aria-pressed={layout==='overview'} disabled={suspended} onClick={()=>setLayout('overview')}>Four charts</button><button type="button" aria-pressed={layout==='single'} disabled={suspended} onClick={()=>setLayout('single')}>Single chart</button></div>
    {layout==='single'&&<><label>Weeks<input type="number" aria-label="Comparison weeks" min={1} max={104} step={1} value={weeksInput} disabled={!ready||suspended} onChange={event=>setWeeksInput(event.target.value)} onBlur={()=>{if(!invalidWeeks&&weeks!==view.weeks)edit({weeks});}} onKeyDown={event=>{if(event.key==='Enter'&&!invalidWeeks&&weeks!==view.weeks){edit({weeks});event.currentTarget.blur();}}}/></label>
    <div className="comparison-quick-weeks" aria-label="Common comparison periods">{[1,2,4,8,12,26,52].map(value=><button type="button" key={value} aria-pressed={view.weeks===value} disabled={!ready||suspended} onClick={()=>{setWeeksInput(String(value));edit({weeks:value});}}>{value}w</button>)}</div></>}
    <AssetPicker exchange="hyperliquid" active={visible} choices={choices} selected="" label="Add asset" buttonLabel="Add comparison asset" panelLabel="Comparison assets" title={view.assets.length>=20?'Up to 20 assets can be compared':'Choose an exchange, market and quote for each asset'} notice={<div className="picker-market-filters" role="group" aria-label="Comparison data source"><button type="button" aria-pressed={pickerSource==='all'} onClick={()=>setPickerSource('all')}>All sources</button>{['hyperliquid','binance','bybit','coinbase'].map(source=><button type="button" key={source} aria-pressed={pickerSource===source} disabled={!markets.some(asset=>asset.exchange===source)} onClick={()=>setPickerSource(source)}>{comparisonExchange(source)}</button>)}</div>} loading={!catalog&&!catalogError} disabled={!ready||suspended||view.assets.length>=20} empty={catalogError?'Asset list unavailable':'All available assets are already selected'} onSelect={symbol=>edit({assets:[...draft.current.assets,symbol]})}/>
    <button type="button" disabled={!ready||loading||suspended||!view.assets.length||layout==='single'&&invalidWeeks} onClick={()=>{if(layout==='single'&&weeks!==view.weeks)edit({weeks});else setRefresh(value=>value+1);}}>Refresh</button>
    {loading&&<span className="muted" role="status">Loading daily closes…</span>}
    {layout==='single'&&invalidWeeks&&<p className="comparison-input-error" role="alert">Choose a whole number from 1 to 104 weeks.</p>}
   </div>
   <div className="comparison-assets" aria-label="Selected comparison assets">{view.assets.map(symbol=>{const asset=sourceFor(symbol);return <div className="comparison-asset" key={symbol} data-comparison-id={symbol}><i style={{background:comparisonColor(symbol)}}/><div><strong>{asset?comparisonLabel(asset):comparisonSelectionLabel(symbol)}</strong><small>{asset?comparisonSource(asset):catalogError?'Source unavailable':'Resolving source…'}</small></div><button type="button" aria-label={`Remove ${symbol.includes('|')?(asset?comparisonOptionName(asset):comparisonSelectionLabel(symbol)):symbol} from comparison`} disabled={!ready||suspended} onClick={()=>edit({assets:draft.current.assets.filter(value=>value!==symbol)})}>×</button></div>;})}</div>
   {layout==='overview'&&<p className="comparison-note">Selected assets and legend visibility apply to all four charts.</p>}
   {catalogError&&<p className="screener-error" role="alert">{catalogError} <button type="button" onClick={()=>setCatalogReload(value=>value+1)}>Retry assets</button></p>}
   {catalog?.warnings.map(warning=><p className="comparison-warning" key={warning} role="status">{warning}</p>)}
   {loadError&&<p className="screener-error" role="alert">{loadError} <button type="button" onClick={()=>setRefresh(value=>value+1)}>Retry daily data</button></p>}
   {layout==='single'&&!!seriesErrors.length&&<ul className="comparison-series-errors" aria-label="Unavailable comparison assets">{seriesErrors.map(series=><li key={comparisonIdentity(series.asset)}><strong>{comparisonSeriesLabel(series.asset,result?.series.map(item=>item.asset)??[])}</strong> · {series.error}</li>)}</ul>}
   {layout==='single'&&result?.warnings.filter(warning=>!catalog?.warnings.includes(warning)).map(warning=><p className="comparison-warning" key={warning} role="status">{warning}</p>)}
   {layout==='single'&&visible&&!suspended&&result&&hasData&&<ComparisonChart result={result} hiddenAssets={hiddenAssets} onToggleAsset={toggleAsset}/>}
   {layout==='overview'&&visible&&!suspended&&overview&&<div className="comparison-grid" aria-label="Comparison periods">{overview.panels.map(panel=>{
    const errors=panel.series.filter(series=>series.error),available=panel.series.some(series=>!series.error&&series.points.some(point=>point.pct!==null));
    return <section className="comparison-panel" key={panel.weeks} data-comparison-weeks={panel.weeks} aria-label={`${panel.weeks}-week comparison`}>
     {!!errors.length&&<ul className="comparison-series-errors" aria-label="Unavailable comparison assets">{errors.map(series=><li key={comparisonIdentity(series.asset)}><strong>{comparisonSeriesLabel(series.asset,panel.series.map(item=>item.asset))}</strong> · {series.error}</li>)}</ul>}
     {panel.warnings.filter(warning=>!catalog?.warnings.includes(warning)).map(warning=><p className="comparison-warning" key={warning} role="status">{warning}</p>)}
     {available?<ComparisonChart chartId={`weeks-${panel.weeks}`} title={`${panel.weeks} weeks`} fillAvailable result={panel} hiddenAssets={hiddenAssets} onToggleAsset={toggleAsset}/>:<div className="comparison-empty"><h2>{panel.weeks} weeks</h2><p>No comparable daily data. Each asset needs a real closing price on {comparisonDate(panel.baselineTime)} UTC.</p></div>}
    </section>;
   })}</div>}
   {ready&&!view.assets.length&&<div className="comparison-empty"><h2>Add assets to compare</h2><p>Choose assets using Add asset. Your weeks selection will be kept.</p></div>}
   {layout==='single'&&ready&&!loading&&result&&!hasData&&!loadError&&<div className="comparison-empty"><h2>No comparable daily data</h2><p>Each asset needs a real closing price on {comparisonDate(result.baselineTime)} to share the same starting point.</p></div>}
   <p className="comparison-note">Percentage change from the starting close, using completed UTC daily candles. Choose markets from Hyperliquid, Binance, Bybit and Coinbase. Each line uses its selected exchange and market. Changes are measured in the displayed quote currency; quotes are not converted. Missing closes remain gaps.</p>
  </div>
 </section>;
});

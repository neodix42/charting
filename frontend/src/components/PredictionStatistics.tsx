import {useEffect,useRef,useState} from 'react';
import type {PredictionDataset,PredictionsBackend} from '../workspace/predictions';
import {predictionDate} from '../workspace/predictions';
import type {PredictionStatisticsForecast,PredictionStatisticsPeriod,PredictionStatisticsRequest} from '../workspace/predictionStatistics';
import {initialStatisticsRequest,statisticsKey,statisticsPrice,statisticsProbability,statisticsResultKey} from '../workspace/predictionStatistics';
import {errorText,type ScreenerDataSource} from '../types';
import {PredictionStatisticsChart,statisticsChartResources,statisticsScenarioColor} from './PredictionStatisticsChart';
import {usePredictionStatisticsDownload} from './usePredictionStatisticsDownload';
import {AssetPicker} from './AssetPicker';
import './PredictionStatistics.css';

const windows=[[0,'All downloaded history'],[52,'Last 52 weeks'],[156,'Last 156 weeks'],[260,'Last 260 weeks'],[520,'Last 520 weeks']] as const;
const supportedPeriods:PredictionStatisticsPeriod[]=['h1','d1','w1'];
const assetPeriods=(items:PredictionDataset[],request:PredictionStatisticsRequest)=>supportedPeriods.filter(period=>items.some(item=>item.dataSource===request.dataSource&&item.symbol===request.asset&&item.period===period));
const reconcilePeriod=(request:PredictionStatisticsRequest,items:PredictionDataset[]):PredictionStatisticsRequest=>{
 const periods=assetPeriods(items,request);
 return periods.length&&!periods.includes(request.period)?{...request,period:periods.includes('d1')?'d1':periods[0]}:request;
};
const sourceName=(value:string)=>value==='hyperliquid'?'Hyperliquid':'Dukascopy';

export function PredictionStatistics({api,visible,suspended=false}:{api:PredictionsBackend;visible:boolean;suspended?:boolean}) {
 const [request,setRequest]=useState(initialStatisticsRequest),[datasets,setDatasets]=useState<PredictionDataset[]>([]);
 const [forecast,setForecast]=useState<PredictionStatisticsForecast|null>(null),[error,setError]=useState(''),[catalogError,setCatalogError]=useState(''),[loading,setLoading]=useState(false),[calculating,setCalculating]=useState(false),[retry,setRetry]=useState(0);
 const mounted=useRef(false),foreground=useRef(false),generation=useRef(0),flight=useRef(false),current=useRef(request);current.current=request;foreground.current=visible&&!suspended;
 useEffect(()=>{mounted.current=true;return()=>{mounted.current=false;generation.current++;};},[]);
 useEffect(()=>{
  if(!visible||suspended){generation.current++;return;}
  let active=true;setLoading(true);setCatalogError('');
  void api.GetPredictionStatisticsDatasets().then(items=>{if(active){const files=items.filter(item=>supportedPeriods.some(period=>period===item.period)&&(item.dataSource==='dukascopy'||item.dataSource==='hyperliquid'));setDatasets(files);const next=reconcilePeriod(current.current,files);if(next!==current.current){generation.current++;current.current=next;setRequest(next);setForecast(null);setError('');}}}).catch(reason=>{if(active)setCatalogError(errorText(reason));}).finally(()=>{if(active)setLoading(false);});
  return()=>{active=false;generation.current++;};
 },[api,visible,suspended,retry]);
 useEffect(()=>{if(!window.__CRYPTO_TEST__)return;Object.assign(window,{__testStatisticsResources:()=>({...statisticsChartResources})});return()=>{delete (window as unknown as {__testStatisticsResources?:unknown}).__testStatisticsResources;};},[]);
 const change=(patch:Partial<PredictionStatisticsRequest>)=>{generation.current++;const next=reconcilePeriod({...current.current,...patch},datasets);current.current=next;setRequest(next);setForecast(null);setError('');};
 const assets=[...new Map(datasets.filter(item=>item.dataSource===request.dataSource).map(item=>[item.symbol,item])).values()].sort((a,b)=>a.name.localeCompare(b.name));
 const selected=assets.find(item=>item.symbol===request.asset);
 const choices=assets.map(item=>({id:item.symbol,label:item.name,details:item.symbol,market:item.market,search:item.symbol}));
 const periods=assetPeriods(datasets,request),selectedDataset=datasets.find(item=>item.dataSource===request.dataSource&&item.symbol===request.asset&&item.period===request.period);
 const result=forecast&&statisticsResultKey(forecast)===statisticsKey(request)?forecast:null;
 const finishDownload=async(input:PredictionStatisticsRequest):Promise<'done'|'deferred'|'recalculated'>=>{
  if(!foreground.current)return 'deferred';
  setLoading(true);setCatalogError('');
  try{
   const items=await api.GetPredictionStatisticsDatasets();
   if(!mounted.current)return 'done';
   if(!foreground.current)return 'deferred';
   const files=items.filter(item=>supportedPeriods.some(period=>period===item.period)&&(item.dataSource==='dukascopy'||item.dataSource==='hyperliquid'));
   setDatasets(files);const next=reconcilePeriod(current.current,files);
   if(next!==current.current){generation.current++;current.current=next;setRequest(next);setForecast(null);setError('');}
  }catch(reason){if(mounted.current&&foreground.current)setCatalogError(errorText(reason));throw reason;}
  finally{if(mounted.current)setLoading(false);}
  if(!foreground.current)return 'deferred';
  if(statisticsKey(input)!==statisticsKey(current.current))return 'done';
  const id=++generation.current;flight.current=true;setCalculating(true);setForecast(null);setError('');
  try{
   const next=await api.GetPredictionStatistics(input);
   if(mounted.current&&foreground.current&&id===generation.current){if(statisticsResultKey(next)!==statisticsKey(input))throw Error('Statistics result does not match the downloaded source, asset, time period and historical lookback.');setForecast(next);return 'recalculated';}
   return !foreground.current&&statisticsKey(input)===statisticsKey(current.current)?'deferred':'done';
  }catch(reason){if(mounted.current&&foreground.current&&id===generation.current)setError(errorText(reason));return !foreground.current&&statisticsKey(input)===statisticsKey(current.current)?'deferred':'done';}
  finally{flight.current=false;if(mounted.current)setCalculating(false);}
 };
 const dataDownload=usePredictionStatisticsDownload({api,visible:visible&&!suspended,onComplete:finishDownload});
 const download=dataDownload.download;
 const downloadAsset=download?datasets.find(item=>item.dataSource===download.request.dataSource&&item.symbol===download.request.asset)?.name||download.request.asset:'';
 const startDownload=()=>{if(flight.current||loading||!selectedDataset||dataDownload.isLocked()||!foreground.current)return;generation.current++;setError('');void dataDownload.start(structuredClone(current.current));};
 const calculate=async()=>{
  if(flight.current||dataDownload.isLocked()||!selectedDataset||!foreground.current)return;
  const input=structuredClone(current.current),id=++generation.current;flight.current=true;setCalculating(true);setForecast(null);setError('');
  try{
   const next=await api.GetPredictionStatistics(input);
   if(mounted.current&&foreground.current&&id===generation.current){if(statisticsResultKey(next)!==statisticsKey(input))throw Error('Statistics result does not match the selected source, asset, time period and historical lookback.');setForecast(next);}
  }catch(reason){if(mounted.current&&foreground.current&&id===generation.current)setError(errorText(reason));}
  finally{flight.current=false;if(mounted.current)setCalculating(false);}
 };
 return <div className="prediction-statistics">
  <header className="prediction-section-heading"><div><h2>Statistics</h2><p>Compare daily candle directions, conditioned on the selected time period.</p></div><button type="button" disabled={loading||calculating||dataDownload.locked} onClick={()=>setRetry(value=>value+1)}>Refresh assets</button></header>
  {catalogError&&<p className="screener-error" role="alert">Could not read downloaded candle data: {catalogError}<button type="button" disabled={dataDownload.locked} onClick={()=>setRetry(value=>value+1)}>Retry assets</button></p>}
  <form className="prediction-controls" aria-label="Statistics inputs" onSubmit={event=>{event.preventDefault();void calculate();}}>
   <label>Data source<select aria-label="Statistics data source" value={request.dataSource} onChange={event=>{change({dataSource:event.target.value as ScreenerDataSource,asset:''});}}><option value="dukascopy">Dukascopy</option><option value="hyperliquid">Hyperliquid</option></select></label>
   <div className="prediction-asset-choice"><span>Asset</span><AssetPicker className="statistics-asset-selector" exchange={request.dataSource} choices={choices} selected={request.asset} label={selected?`${selected.name} · ${selected.symbol}`:'Select a downloaded asset'} buttonLabel="Statistics asset" panelLabel="Statistics assets" active={visible&&!suspended} disabled={loading} loading={loading} empty={`No downloaded ${sourceName(request.dataSource)} assets`} onSelect={asset=>change({asset})}/></div>
   <label>Time period<select aria-label="Statistics time period" value={request.period} disabled={loading||!periods.length} onChange={event=>change({period:event.target.value as PredictionStatisticsPeriod})}>{!periods.length&&<option value={request.period}>Select an asset first</option>}{periods.map(period=><option key={period} value={period}>{period.toUpperCase()}</option>)}</select></label>
   <label>Historical lookback<select aria-label="Statistics history period" value={request.lookbackWeeks} onChange={event=>change({lookbackWeeks:Number(event.target.value)})}>{windows.map(([weeks,label])=><option key={weeks} value={weeks}>{label}</option>)}</select></label>
   <button className="primary" type="submit" aria-label="Calculate scenarios" disabled={loading||calculating||dataDownload.locked||!selectedDataset}>{calculating?'Calculating…':'Calculate scenarios'}</button>
   <button type="button" aria-label="Download missing data" disabled={loading||calculating||dataDownload.locked||!selectedDataset} onClick={startDownload}>Download missing data</button>

   <p className="prediction-input-help">Time period selects the downloaded candle interval; historical lookback selects the historical weeks to compare. UP means close above that candle's own open; DOWN means close below its own open.</p>
   {request.period!=='d1'&&<p className="prediction-input-help">H1 and W1 require matching downloaded D1 companion data for the same asset and source. Scenarios describe the remaining daily candle directions.</p>}
   {!loading&&!catalogError&&!assets.length&&<p className="prediction-input-help" role="status">No downloaded {sourceName(request.dataSource)} H1, D1 or W1 candles found. Download data in Screener → Downloader first.</p>}
  </form>
  {download&&<section className="statistics-download" role="region" aria-label="Statistics data download">
   <header><strong>Data download</strong><span>{sourceName(download.request.dataSource)} · {downloadAsset} · {download.request.period.toUpperCase()}</span></header>
   {download.phase==='starting'&&<p role="status">Starting missing-data download…</p>}
   {download.info?.downloadsPaused&&download.phase==='running'&&<p role="status">Downloads are paused for Live Alerts and will resume automatically.</p>}
   {download.info?.jobs.map(job=><div className="statistics-download-job" key={job.id}><div><strong>{job.timeframe.toUpperCase()}</strong><span>{job.status}</span><span>{Math.round(Math.min(100,Math.max(0,job.progress||0)))}%</span></div><progress aria-label={`${job.timeframe.toUpperCase()} missing data progress`} max={100} value={Math.min(100,Math.max(0,job.progress||0))}/>{job.message&&<p>{job.message}</p>}</div>)}
   {download.info?.error&&download.info.error!==download.error&&<p className="screener-error" role="alert">Downloader: {download.info.error}</p>}
   {download.error&&<p className="screener-error" role="alert">{download.error}</p>}{download.notice&&<p role="status">{download.notice}</p>}
   {dataDownload.cancelPending&&<p role="status">Cancelling data download…</p>}
   <div className="statistics-download-actions">{['running','status-error'].includes(download.phase)&&<button type="button" disabled={dataDownload.actionBusy||dataDownload.cancelPending} onClick={()=>void dataDownload.cancel()}>Cancel data download</button>}{download.phase==='status-error'&&<><button type="button" disabled={dataDownload.actionBusy||dataDownload.cancelPending} onClick={dataDownload.retryStatus}>Retry download status</button><button type="button" disabled={dataDownload.actionBusy||dataDownload.cancelPending} onClick={dataDownload.stopTracking}>Stop tracking download</button></>}</div>
  </section>}
  {loading&&<p role="status">Loading downloaded assets…</p>}{calculating&&<p role="status">Calculating historical weekly scenarios…</p>}{error&&<p className="screener-error" role="alert">{error}</p>}
  {result&&<section className="prediction-result statistics-result" aria-label="Weekly statistics forecast">
   <h2>{result.assetName||result.symbol} <span>{sourceName(result.dataSource)} · {result.period.toUpperCase()}</span></h2>
   <div className="prediction-metadata"><span title="Earliest fully closed candle in the selected source file, before historical lookback filters and chart history limits.">First closed bar <b>{predictionDate(result.firstClosedBar)}</b></span><span>Last closed bar <b>{predictionDate(result.anchor.time)}</b></span><span>Anchor close <b>{statisticsPrice(result.anchor.close)}</b></span><span>Data read <b>{predictionDate(result.asOf)}</b></span><span>Week <b>{predictionDate(result.weekStart).slice(0,10)}</b></span>{result.conditioning&&<span>Conditioning <b>{result.conditioning}</b></span>}</div>
   <p className="muted" aria-label="Statistics sample scope">Historical sample: {result.lookbackWeeks?`last ${result.lookbackWeeks} weeks before the displayed week`:'all downloaded history before the displayed week'}. First closed bar describes the full source file; the historical lookback limits the weeks compared.</p>
   <div className="statistics-summary"><div><span>Observed sequence</span><strong>{result.prefix.map(day=>`${day.day} ${day.direction==='up'?'↑':'↓'}`).join(' · ')||'—'}</strong></div><div><span>Matching weeks</span><strong>{result.matchedWeeks}</strong></div><div><span>Eligible weeks</span><strong>{result.eligibleWeeks}</strong></div><div><span>Remaining-week sequences</span><strong>{result.theoreticalScenarios}</strong></div></div>
   {result.warnings.map((warning,index)=><p key={index} className="statistics-warning" role="status">{warning}</p>)}
   {result.theoreticalScenarios===0?<p className="statistics-empty" role="status">The observed sequence matches {result.matchedWeeks} of {result.eligibleWeeks} eligible historical weeks. Friday is already complete; there are no remaining weekdays to project in this week.</p>:result.matchedWeeks===0?<p className="statistics-empty" role="status">No historical weeks match the observed sequence. Scenario probabilities cannot be estimated.</p>:!result.scenarios.length?<p className="statistics-empty" role="status">No observed scenarios are available for this sequence.</p>:null}
   {visible&&!suspended&&<PredictionStatisticsChart forecast={result}/>}
   {result.scenarios.length>0&&<div className="statistics-table-scroll"><table className="statistics-scenarios"><caption>Remaining-week scenarios</caption><thead><tr><th>Scenario</th><th>Probability</th><th>Matched weeks</th>{result.scenarios[0].points.map(point=><th key={point.time}>{new Intl.DateTimeFormat('en-GB',{timeZone:'UTC',weekday:'long'}).format(new Date(point.time))} close{point.observed?' (observed)':''}</th>)}</tr></thead><tbody>{result.scenarios.map(scenario=><tr key={scenario.id}><th scope="row"><i style={{backgroundColor:statisticsScenarioColor(scenario.id)}}/><span>{scenario.label}<small>{scenario.id}</small></span></th><td>{statisticsProbability(scenario.probability)}</td><td>{scenario.count} / {result.matchedWeeks}</td>{scenario.points.map(point=><td key={point.time} title={point.observed?'Observed downloaded daily close':'Estimated conditional average daily close'}>{statisticsPrice(point.close)}</td>)}</tr>)}</tbody></table></div>}
   <details className="statistics-method"><summary>How these scenarios are calculated</summary><p>Only complete historical Monday–Friday weeks are compared. Weeks must match every closed daily candle direction in the observed sequence and the selected time period's conditioning. Each remaining daily sequence's probability is its observed count divided by all matching weeks. Unobserved sequences receive no estimated path.</p><p>Paths start at the selected time period's anchor close. Projected prices use the average historical daily close-to-checkpoint ratio for the matched sequence; with an observed W1 bridge, the projection checkpoint is the latest known daily close. These lines are conditional averages, not individual historical paths. UP/DOWN describes each daily candle's body, so an overnight gap can make a line's slope differ from the candle direction. Prices and probabilities are estimates from the displayed sample.</p>{result.period==='h1'&&<p>H1 also matches the last closed hourly candle's body direction at the same historical weekday and hour. Each future point is a daily closing-price checkpoint plotted at that weekday's final H1 slot, 23:00 UTC. Intermediate future hourly candles are not estimated.</p>}{result.period==='w1'&&<p>W1 also matches the previous closed weekly candle's body direction. Downloaded D1 companion candles supply the observed current-week sequence and the remaining daily checkpoints. An initial point marked observed connects the weekly anchor to an already downloaded daily close.</p>}<p>Excluded historical weeks: {result.excludedIncompleteWeeks} incomplete · {result.excludedDojiWeeks} with doji candles · {result.excludedInvalidWeeks} with invalid prices{result.excludedCheckpointWeeks?` · ${result.excludedCheckpointWeeks} without a usable matching-period checkpoint`:''}. The current week is never part of the historical sample.</p></details>
   <p className="prediction-source">Source: {result.sourceFile}</p>
   {result.companionSourceFile&&<p className="prediction-source">D1 companion: {result.companionSourceFile}</p>}
  </section>}
  {!result&&!loading&&!calculating&&!error&&<p className="prediction-empty">Select a source, asset, time period and historical lookback, then calculate weekly scenarios.</p>}
 </div>;
}

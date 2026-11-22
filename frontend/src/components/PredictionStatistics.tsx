import {useEffect,useId,useRef,useState} from 'react';
import type {PredictionDataset,PredictionsBackend} from '../workspace/predictions';
import {predictionDate} from '../workspace/predictions';
import type {PredictionStatisticsDirection,PredictionStatisticsForecast,PredictionStatisticsPeriod,PredictionStatisticsRequest} from '../workspace/predictionStatistics';
import {initialStatisticsRequest,statisticsKey,statisticsBarCount,statisticsPatternMode,statisticsPrice,statisticsProbability,statisticsResultKey} from '../workspace/predictionStatistics';
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
 const [customPattern,setCustomPattern]=useState<PredictionStatisticsDirection[]>(()=>Array.from({length:10},()=> 'up'));
 const patternID=useId(),patternMode=statisticsPatternMode(request),barCount=statisticsBarCount(request);
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
   if(mounted.current&&foreground.current&&id===generation.current){if(statisticsResultKey(next)!==statisticsKey(input))throw Error('Statistics result does not match the downloaded source, asset, time period and historical lookback and selected pattern.');setForecast(next);return 'recalculated';}
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
   if(mounted.current&&foreground.current&&id===generation.current){if(statisticsResultKey(next)!==statisticsKey(input))throw Error('Statistics result does not match the selected source, asset, time period and historical lookback and selected pattern.');setForecast(next);}
  }catch(reason){if(mounted.current&&foreground.current&&id===generation.current)setError(errorText(reason));}
  finally{flight.current=false;if(mounted.current)setCalculating(false);}
 };
 return <div className="prediction-statistics">
  <header className="prediction-section-heading"><div><h2>Statistics</h2><p>Search candle-body patterns and estimate the next available bar.</p></div><button type="button" disabled={loading||calculating||dataDownload.locked} onClick={()=>setRetry(value=>value+1)}>Refresh assets</button></header>
  {catalogError&&<p className="screener-error" role="alert">Could not read downloaded candle data: {catalogError}<button type="button" disabled={dataDownload.locked} onClick={()=>setRetry(value=>value+1)}>Retry assets</button></p>}
  <form className="prediction-controls" aria-label="Statistics inputs" onSubmit={event=>{event.preventDefault();void calculate();}}>
   <label>Data source<select aria-label="Statistics data source" value={request.dataSource} onChange={event=>{change({dataSource:event.target.value as ScreenerDataSource,asset:''});}}><option value="dukascopy">Dukascopy</option><option value="hyperliquid">Hyperliquid</option></select></label>
   <div className="prediction-asset-choice"><span>Asset</span><AssetPicker className="statistics-asset-selector" exchange={request.dataSource} choices={choices} selected={request.asset} label={selected?`${selected.name} · ${selected.symbol}`:'Select a downloaded asset'} buttonLabel="Statistics asset" panelLabel="Statistics assets" active={visible&&!suspended} disabled={loading} loading={loading} empty={`No downloaded ${sourceName(request.dataSource)} assets`} onSelect={asset=>change({asset})}/></div>
   <label>Time period<select aria-label="Statistics time period" value={request.period} disabled={loading||!periods.length} onChange={event=>change({period:event.target.value as PredictionStatisticsPeriod})}>{!periods.length&&<option value={request.period}>Select an asset first</option>}{periods.map(period=><option key={period} value={period}>{period.toUpperCase()}</option>)}</select></label>
   <label>Historical lookback<select aria-label="Statistics history period" value={request.lookbackWeeks} onChange={event=>change({lookbackWeeks:Number(event.target.value)})}>{windows.map(([weeks,label])=><option key={weeks} value={weeks}>{label}</option>)}</select></label>
   <button className="primary" type="submit" aria-label="Calculate next bar" disabled={loading||calculating||dataDownload.locked||!selectedDataset}>{calculating?'Calculating…':'Calculate next bar'}</button>
   <button type="button" aria-label="Download missing data" disabled={loading||calculating||dataDownload.locked||!selectedDataset} onClick={startDownload}>Download missing data</button>

   <fieldset className="statistics-pattern" aria-label="Observed sequence"><legend>Observed sequence</legend>
    <div className="statistics-pattern-heading">
     <div className="statistics-pattern-mode" role="radiogroup" aria-label="Pattern source">{(['custom','current'] as const).map(mode=><label key={mode}><input type="radio" name={`${patternID}-source`} value={mode} checked={patternMode===mode} onChange={()=>change(mode==='current'?{patternMode:'current',patternLength:barCount,pattern:undefined}:{patternMode:undefined,patternLength:undefined,pattern:customPattern.slice(0,barCount)})}/>{mode==='custom'?'Custom bars':'Current bars'}</label>)}</div>
     <label>Number of bars<select aria-label="Pattern bar count" value={barCount} onChange={event=>{const count=Number(event.target.value);change(patternMode==='current'?{patternLength:count}:{pattern:customPattern.slice(0,count)});}}>{Array.from({length:8},(_,index)=>index+3).map(count=><option key={count} value={count}>{count} bars</option>)}</select></label>
     <p>{patternMode==='custom'?'Choose the pattern to search, from oldest to newest. These directions are your query, not a claim about the latest candles.':'Use the latest fully closed candles in this timeframe, ending at the most recent one. Calculate to read their actual directions.'} Weekdays do not affect matching.</p>
    </div>
    {patternMode==='custom'?<div className="statistics-pattern-bars">{(request.pattern??[]).map((direction,index)=><div key={index} className={`statistics-pattern-bar ${direction}`} role="group" aria-label={`Bar ${index+1}`}><span>Bar {index+1}{index===0?' · oldest':index===barCount-1?' · newest':''}</span><i aria-hidden="true"/><div className="statistics-pattern-directions" role="radiogroup" aria-label={`Bar ${index+1} direction`}>{(['up','down'] as const).map(option=><label key={option}><input type="radio" name={`${patternID}-bar-${index}`} value={option} checked={direction===option} onChange={()=>{const next=customPattern.map((current,position)=>position===index?option:current);setCustomPattern(next);change({pattern:next.slice(0,barCount)});}}/>{option==='up'?'Up':'Down'}</label>)}</div></div>)}</div>:result?.patternMode==='current'?<div className="statistics-pattern-bars" aria-label="Current closed-bar sequence">{result.pattern?.map((direction,index)=><div key={index} className={`statistics-pattern-bar ${direction}`}><span>Bar {index+1}{index===0?' · oldest':index===barCount-1?' · newest':''}</span><i aria-hidden="true"/><strong>{direction==='up'?'Up':'Down'}</strong>{result.prefix[index]&&<time dateTime={new Date(result.prefix[index].time).toISOString()}>{predictionDate(result.prefix[index].time)}</time>}</div>)}</div>:<p className="statistics-current-prompt" role="status">Calculate to load the latest {barCount} closed {request.period.toUpperCase()} bars. Their directions are read from the downloaded file; no custom directions are substituted.</p>}
   </fieldset>
   <p className="prediction-input-help">Bullish means close above the same candle's open; bearish means close below it. Search uses consecutive downloaded {request.period.toUpperCase()} bars and predicts the next available {request.period.toUpperCase()} candle. H1 uses hourly bars, D1 daily bars and W1 weekly bars; no daily companion or weekday alignment is required.</p>
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
  {loading&&<p role="status">Loading downloaded assets…</p>}{calculating&&<p role="status">Calculating next-bar probabilities…</p>}{error&&<p className="screener-error" role="alert">{error}</p>}
  {result&&<section className="prediction-result statistics-result" aria-label="Next-bar statistics forecast">
   <h2>{result.assetName||result.symbol} <span>{sourceName(result.dataSource)} · {result.period.toUpperCase()}</span></h2>
   <div className="prediction-metadata"><span title="Earliest fully closed candle in the selected source file, before historical lookback filters and chart history limits.">First closed bar <b>{predictionDate(result.firstClosedBar)}</b></span><span>Last closed bar <b>{predictionDate(result.anchor.time)}</b></span><span>Anchor close <b>{statisticsPrice(result.anchor.close)}</b></span><span>Data read <b>{predictionDate(result.asOf)}</b></span></div>
   <p className="muted" aria-label="Statistics sample scope">Historical sample: {result.lookbackWeeks?`last ${result.lookbackWeeks} weeks`:'all downloaded history'}, using {result.period.toUpperCase()} candle windows with a known next-bar outcome. First closed bar describes full file coverage.</p>
   <div className="statistics-summary"><div><span>{result.patternMode==='current'?'Current closed-bar sequence':'Selected sequence'} · oldest to newest</span><strong>{result.pattern?.map(direction=>direction==='up'?'↑':'↓').join(' · ')}</strong></div><div><span>Matching patterns</span><strong>{result.matchedPatterns??0}</strong></div><div><span>Eligible patterns</span><strong>{result.eligiblePatterns??0}</strong></div><div><span>Next-bar doji outcomes</span><strong>{result.nextBarDojiCount??0}</strong></div></div>
   {result.warnings.map((warning,index)=><p key={index} className="statistics-warning" role="status">{warning}</p>)}
   {!result.matchedPatterns?<p className="statistics-empty" role="status">No historical patterns match the selected sequence. Next-bar probabilities cannot be estimated.</p>:!result.scenarios.length?<p className="statistics-empty" role="status">No next-bar outcomes are available for this sequence.</p>:null}
   {result.scenarios.length>0&&<div className="statistics-table-scroll"><table className="statistics-scenarios"><caption>Next-bar outcomes</caption><thead><tr><th>Next available {result.period.toUpperCase()} bar</th><th>Probability</th><th>Matching patterns</th><th>Estimated close</th></tr></thead><tbody>{result.scenarios.map(scenario=><tr key={scenario.id}><th scope="row"><i style={{backgroundColor:statisticsScenarioColor(scenario.id)}}/><span>{scenario.label}</span></th><td>{statisticsProbability(scenario.probability)}</td><td>{scenario.count} / {result.matchedPatterns}</td><td title="Conditional average next-bar close, normalized to the anchor close">{scenario.points.length?statisticsPrice(scenario.points[0].close):'Unavailable'}</td></tr>)}</tbody></table></div>}
   {visible&&!suspended&&<PredictionStatisticsChart forecast={result}/>}
   <details className="statistics-method"><summary>How these probabilities are calculated</summary><p>Every eligible historical window contains the selected number of consecutive downloaded candles plus its next closed candle. The selected bullish/bearish bodies must match in order, independently of weekdays or hours. Each next-bar outcome's probability is its count divided by all matching patterns. A next-bar doji (open equals close) remains in that denominator and is shown separately.</p><p>Only fully closed {result.period.toUpperCase()} candles from this source are used. Missing market sessions do not create artificial candles. Windows containing doji pattern bars or invalid prices are excluded. Recent observations without a known next-bar outcome cannot enter the sample. Overlapping historical windows can share candles, so matches are not independent trials.</p><p>Paths start at the latest closed candle. Each next-bar close is the conditional average historical next-close/prior-close ratio, applied to the anchor close. The chart's projected slot means the next available candle, not a verified future opening date. Bullish/bearish refers to that candle's own body; gaps can make the projected line slope differ. These are historical frequencies and conditional averages, not guaranteed prices.</p><p>Excluded historical windows: {result.excludedDojiPatterns??0} containing doji pattern bars · {result.excludedInvalidPatterns??0} with invalid prices.</p></details>
   <p className="prediction-source">Source: {result.sourceFile}</p>
   {result.companionSourceFile&&<p className="prediction-source">D1 companion: {result.companionSourceFile}</p>}
  </section>}
  {!result&&!loading&&!calculating&&!error&&<p className="prediction-empty">Select a source, asset, time period and historical lookback, choose a 3–10-bar sequence, then calculate the next bar.</p>}
 </div>;
}

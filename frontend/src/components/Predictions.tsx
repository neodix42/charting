import {forwardRef,useEffect,useImperativeHandle,useRef,useState} from 'react';
import type {PredictionDataset,PredictionForecast,PredictionInfo,PredictionSelection,PredictionsBackend} from '../workspace/predictions';
import {initialPredictionSelection,predictionBytes,predictionDate,predictionModelKey,PredictionSelectionWriter} from '../workspace/predictions';
import {errorText} from '../types';
import {PredictionChart,predictionChartResources} from './PredictionChart';
import './Predictions.css';

export interface PredictionsHandle {flush():Promise<void>}
export const Predictions=forwardRef<PredictionsHandle,{api:PredictionsBackend;visible:boolean;suspended?:boolean}>(function Predictions({api,visible,suspended=false},ref){
 const [info,setInfo]=useState<PredictionInfo|null>(null),[datasets,setDatasets]=useState<PredictionDataset[]>([]),[selection,setSelection]=useState(initialPredictionSelection),[horizon,setHorizon]=useState('20'),[forecast,setForecast]=useState<PredictionForecast|null>(null),[error,setError]=useState(''),[saveError,setSaveError]=useState(''),[forecastError,setForecastError]=useState(''),[busy,setBusy]=useState(false),[loading,setLoading]=useState(false),[forecastLoading,setForecastLoading]=useState(false),[retry,setRetry]=useState(0),[forecastRetry,setForecastRetry]=useState(0);
 const alive=useRef(false),foreground=useRef(false),epoch=useRef(0),revision=useRef(0),initialized=useRef(false),attempted=useRef(new Set<string>()),currentInfo=useRef<PredictionInfo|null>(null),currentSelection=useRef(selection),operation=useRef<Promise<void>|null>(null),writer=useRef<PredictionSelectionWriter|null>(null);
 foreground.current=visible&&!suspended;
 if(!writer.current)writer.current=new PredictionSelectionWriter(value=>api.SavePredictionSelection(value).then(saved=>{if(alive.current)setSaveError('');return saved;}),reason=>{if(alive.current)setSaveError(errorText(reason));});
 const accept=(value:PredictionInfo)=>{currentInfo.current=value;setInfo(value);};
 const update=(value:PredictionSelection)=>{currentSelection.current=value;setSelection(value);writer.current!.edit(value);};
 useEffect(()=>{alive.current=true;return()=>{alive.current=false;epoch.current++;};},[]);
 useImperativeHandle(ref,()=>({flush:async()=>{await writer.current!.flush();}}),[]);
 const perform=(action:()=>Promise<PredictionInfo>)=>{
  if(operation.current)return;
  const generation=epoch.current;++revision.current;setBusy(true);setError('');
  const pending=Promise.resolve().then(action).then(next=>{if(alive.current&&generation===epoch.current)accept(next);}).catch(reason=>{if(alive.current&&generation===epoch.current)setError(errorText(reason));}).finally(()=>{operation.current=null;if(alive.current)setBusy(false);});operation.current=pending;
 };
 useEffect(()=>{
  if(!visible||suspended)return;
  const generation=++epoch.current;let stopped=false,timer:ReturnType<typeof setTimeout>|undefined;
  const current=()=>!stopped&&alive.current&&generation===epoch.current;
  const read=async(initial:boolean)=>{
   const requested=revision.current;
   try{
    if(initial){setLoading(true);setError('');}
    const [next,files]=await Promise.all([api.GetPredictions(),initial?api.GetPredictionDatasets():Promise.resolve(null)]);
    if(!current()||requested!==revision.current)return;
    if(operation.current)return;
    accept(next);if(files)setDatasets(files);
    if(!initialized.current){initialized.current=true;currentSelection.current=next.selection;setSelection(next.selection);setHorizon(String(next.selection.horizon));}
    const key=predictionModelKey(next.settings);
    if(next.active||next.error||next.event.phase==='cancelled')attempted.current.add(key);
    if(!next.active&&!next.runtime.ready&&!attempted.current.has(key)){
     attempted.current.add(key);perform(()=>api.PreparePredictionModel());
    }
   }catch(reason){if(current())setError(errorText(reason));}
   finally{if(current()){setLoading(false);timer=setTimeout(()=>void read(false),1000);}}
  };
  void read(true);return()=>{stopped=true;epoch.current++;if(timer)clearTimeout(timer);};
 },[api,visible,suspended,retry]);
 const summaries=info?.forecasts??[],selectedID=selection.forecastId||summaries[0]?.id||'';
 useEffect(()=>{
  if(!visible||suspended||!selectedID){setForecast(null);return;}
  let current=true;setForecastLoading(true);setForecastError('');setForecast(null);
  void api.GetPredictionForecast(selectedID).then(value=>{if(current){setForecast(value);setForecastLoading(false);}}).catch(reason=>{if(current){setForecastError(errorText(reason));setForecastLoading(false);}});
  return()=>{current=false;};
 },[api,visible,suspended,selectedID,forecastRetry]);
 useEffect(()=>{if(!window.__CRYPTO_TEST__)return;Object.assign(window,{__testPredictionResources:()=>({...predictionChartResources})});return()=>{delete (window as unknown as {__testPredictionResources?:unknown}).__testPredictionResources;};},[]);
 const assets=[...new Map(datasets.map(dataset=>[dataset.symbol,{symbol:dataset.symbol,name:dataset.name}])).values()].sort((a,b)=>a.name.localeCompare(b.name));
 const periods=[...new Set(datasets.filter(dataset=>dataset.symbol===selection.asset).map(dataset=>dataset.period))];
 const amount=Number(horizon),validHorizon=horizon.trim()!==''&&Number.isInteger(amount)&&amount>=1&&amount<=256;
 const valid=!!selection.asset&&periods.includes(selection.period)&&validHorizon&&[512,1024].includes(selection.contextLength);
 const working=busy||!!info?.active;
 const prepare=()=>{if(!info||working)return;attempted.current.add(predictionModelKey(info.settings));perform(()=>api.PreparePredictionModel());};
 const start=()=>{
  if(!info?.runtime.ready||!valid||working)return;
  const settingsKey=predictionModelKey(info.settings),request={...structuredClone(currentSelection.current),forecastId:''};update(request);
  perform(async()=>{await writer.current!.flush();const fresh=await api.GetPredictions();if(!foreground.current) return fresh;if(predictionModelKey(fresh.settings)!==settingsKey||!fresh.runtime.ready){accept(fresh);throw Error('Model settings changed. Prepare the selected model before starting a forecast.');}return api.StartPrediction(request);});
 };
 const changeAsset=(asset:string)=>{const available=datasets.filter(dataset=>dataset.symbol===asset).map(dataset=>dataset.period);update({...selection,asset,period:available.includes(selection.period)?selection.period:available[0]??''});};
 return <section hidden={!visible} className="predictions" role="tabpanel" id="predictions-panel" aria-labelledby="predictions-tab">
  <header className="predictions-heading"><div><span className="analysis-eyebrow">LOCAL PRICE FORECASTS</span><h1>Predictions</h1><p>Explore possible future closing prices using your downloaded historical data.</p></div><button type="button" disabled={loading||working} onClick={()=>setRetry(value=>value+1)}>Refresh assets</button></header>
  {loading&&!info&&<p role="status">Loading predictions…</p>}
  {(error||info?.error||info?.runtime.error)&&<div className="screener-error" role="alert">{error||info?.error||info?.runtime.error}{!info&&<button type="button" onClick={()=>setRetry(value=>value+1)}>Retry predictions</button>}</div>}
  {saveError&&<div className="screener-error" role="alert">Could not save prediction selection: {saveError}<button type="button" onClick={()=>void writer.current!.flush().catch(reason=>setSaveError(errorText(reason)))}>Retry selection save</button></div>}
  {info&&<section className="prediction-model-status" aria-label="Prediction model status"><div><strong>{info.settings.model}</strong><span role="status">{info.active||busy?info.event.message||'Preparing…':info.event.phase==='cancelled'?'Prediction cancelled':info.runtime.ready?'Model ready':'Model preparation required'}</span></div>{info.active&&<><progress aria-label="Prediction progress" max={100} value={Math.max(0,Math.min(100,info.event.progress))}/><small>{info.event.phase} · {Math.round(info.event.progress)}%{info.event.downloaded!==undefined?` · ${predictionBytes(info.event.downloaded)}${info.event.total?` / ${predictionBytes(info.event.total)}`:''}`:''}</small></>}{working?<button type="button" disabled={busy} onClick={()=>perform(()=>api.CancelPrediction())}>Cancel prediction</button>:!info.runtime.ready&&<button type="button" onClick={prepare}>Retry model preparation</button>}</section>}
  <form className="prediction-controls" aria-label="Prediction inputs" onSubmit={event=>{event.preventDefault();start();}}>
   <label>Asset<select aria-label="Prediction asset" value={selection.asset} disabled={!info||working} onChange={event=>changeAsset(event.target.value)}><option value="">Select a downloaded asset</option>{assets.map(asset=><option value={asset.symbol} key={asset.symbol}>{asset.symbol.toUpperCase()} · {asset.name}</option>)}</select></label>
   <label>Period<select aria-label="Prediction period" value={selection.period} disabled={!info||working||!periods.length} onChange={event=>update({...selection,period:event.target.value})}>{!periods.length&&<option value={selection.period}>Select an asset first</option>}{periods.map(period=><option key={period} value={period}>{period}</option>)}</select></label>
   <label>Historical bars<select aria-label="Prediction historical bars" value={selection.contextLength} disabled={!info||working} onChange={event=>update({...selection,contextLength:Number(event.target.value)})}><option value={512}>512 bars</option><option value={1024}>1024 bars</option></select></label>
   <label>Future bars<input type="number" min={1} max={256} step={1} required aria-label="Prediction future bars" value={horizon} disabled={!info||working} onChange={event=>{const text=event.target.value,value=Number(text);setHorizon(text);if(text.trim()&&Number.isInteger(value)&&value>=1&&value<=256)update({...selection,horizon:value});}}/></label>
   <button className="primary" type="submit" disabled={!info?.runtime.ready||!valid||working}>Start prediction</button>
   <p className="prediction-input-help">Target: closing price. Uses the latest {selection.contextLength} closed bars from the selected downloaded file; new market data is not downloaded here.</p>
   {!validHorizon&&<p className="screener-date-error">Enter a whole number from 1 to 256 future bars.</p>}
   {info&&!datasets.length&&<p className="muted">No downloaded candle files found. Download an asset in Screener → Downloader first.</p>}
  </form>
  <div className="prediction-results"><aside className="prediction-history" aria-label="Saved forecasts"><h2>Saved forecasts <span>{summaries.length}</span></h2>{!summaries.length&&<p className="muted">Completed forecasts appear here.</p>}{summaries.map(summary=><button key={summary.id} type="button" aria-pressed={selectedID===summary.id} onClick={()=>update({...selection,forecastId:summary.id})}><strong>{summary.symbol.toUpperCase()} · {summary.period}</strong><span>{summary.assetName}</span><small>{predictionDate(summary.createdAt)}</small><small>{summary.horizon} future bars · {summary.contextLength} historical bars</small></button>)}</aside>
   <section className="prediction-result" aria-label="Selected forecast">{forecastLoading&&<p role="status">Loading forecast…</p>}{forecastError&&<p className="screener-error" role="alert">{forecastError}<button type="button" onClick={()=>setForecastRetry(value=>value+1)}>Retry forecast</button></p>}{forecast&&<><h2>{forecast.assetName||forecast.symbol.toUpperCase()} <span>{forecast.period}</span></h2><div className="prediction-metadata"><span>Created <b>{predictionDate(forecast.createdAt)}</b></span><span>Data cutoff <b>{predictionDate(forecast.asOf)}</b></span><span>Last closed bar <b>{predictionDate(forecast.history.at(-1)?.time??forecast.asOf)}</b></span><span>Model <b>{forecast.model}</b></span><span>Context <b>{forecast.contextLength} bars</b></span><span>Forecast <b>{forecast.horizon} bars</b></span></div>{visible&&!suspended&&<PredictionChart forecast={forecast}/>}<p className="prediction-source">Source: {forecast.sourceFile}</p></>}{!forecast&&!forecastLoading&&!forecastError&&<p className="prediction-empty">Select an asset and start a prediction, or open a saved forecast.</p>}</section>
  </div>
 </section>;
});

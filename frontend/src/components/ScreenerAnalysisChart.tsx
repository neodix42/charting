import {useEffect,useRef,useState} from 'react';
import {CandlestickSeries,HistogramSeries,LineSeries,createChart,createSeriesMarkers,type AutoscaleInfoProvider,type IChartApi,type MouseEventHandler,type Time,type UTCTimestamp} from 'lightweight-charts';
import type {AnalysisBar,AnalysisChart,AnalysisResult,ScreenerAnalysisBackend} from '../workspace/screenerAnalysis';
import {analysisDate,analysisFieldGroup,analysisFieldLabel,analysisPriceOverlay} from '../workspace/screenerAnalysis';
import {errorText} from '../types';
import {chartFontSize,useAppearanceFontSize} from '../workspace/appearance';
import {getThemePalette} from '../workspace/themes';
import {chartThemeOptions} from '../charts/theme';
import {analysisMatchMarkers,analysisTradePriceLines,profitableMatchColor} from '../charts/analysisMarkers';

export interface AnalysisSelection {result:AnalysisResult;offset:number;occurrence:number}
export const analysisChartResources={charts:0,observers:0,listeners:0};
const rawFields=new Set(['timestamp','open','high','low','close','price','bidprice','askprice']);
const colors=['#e7ad51','#68b8f7','#b58bea','#f18b7b','#53c6b0','#dfb57f','#9dbc69','#dc8ec3'];
const value=(row:AnalysisBar,field:string)=>Object.entries(row.values).find(([key])=>key.toLowerCase()===field.toLowerCase())?.[1];
const price=(row:AnalysisBar)=>value(row,'close')??value(row,'price')??value(row,'bidprice')??value(row,'askprice');
const format=(number:number|undefined)=>number===undefined?'—':number.toLocaleString('en-US',{maximumSignificantDigits:10});

function AnalysisPlot({data}:{data:AnalysisChart}){
 const fontSize=useAppearanceFontSize(),[theme,setTheme]=useState(()=>document.documentElement.dataset.theme??'light'),chartRef=useRef<IChartApi|null>(null),applyTheme=useRef<(theme:string)=>void>(()=>{});
 const host=useRef<HTMLDivElement>(null),[shown,setShown]=useState<string[]>(()=>data.fields.filter(field=>!rawFields.has(field.toLowerCase()))),[hover,setHover]=useState<AnalysisBar|null>(null),[chartError,setChartError]=useState('');
 const plotFields=data.fields.filter(field=>!rawFields.has(field.toLowerCase())),selected=shown.filter(field=>plotFields.includes(field));
 const groups=[...new Set(selected.filter(field=>!analysisPriceOverlay(field)).map(analysisFieldGroup))];
 const height=Math.max(420,290+groups.length*115);
 const signature=selected.join('|');
 const row=hover??data.bars.find(bar=>bar.index===data.matchIndex)??data.bars.find(bar=>bar.time===data.matchTime);
 const duplicates=new Set(data.bars.map(bar=>bar.time)).size!==data.bars.length;
 const tradePrices=analysisTradePriceLines(data);
 useEffect(()=>{
  const update=()=>setTheme(document.documentElement.dataset.theme??'light');update();
  const observer=new MutationObserver(update);observer.observe(document.documentElement,{attributes:true,attributeFilter:['data-theme']});analysisChartResources.observers++;
  return ()=>{observer.disconnect();analysisChartResources.observers--;};
 },[]);
 useEffect(()=>{
  if(!host.current)return;
  setChartError('');
  const palette=getThemePalette(document.documentElement.dataset.theme??'light'),themeOptions=chartThemeOptions(palette);
  const ordered=[...new Map(data.bars.map(bar=>[bar.time,bar])).values()].sort((a,b)=>a.time-b.time);
  const timed=(bar:AnalysisBar)=>({time:bar.time/1000 as UTCTimestamp});
  const candle=ordered.length>0&&ordered.every(bar=>['open','high','low','close'].every(field=>Number.isFinite(value(bar,field))));
  if(!candle&&!ordered.some(bar=>Number.isFinite(price(bar)))){setChartError('This file has no OHLC or price field to chart. Its numeric fields remain available below.');return;}
  const chart=createChart(host.current,{...themeOptions,width:Math.max(300,host.current.clientWidth),height,layout:{...themeOptions.layout,fontFamily:'Inter, Ubuntu, system-ui, sans-serif',fontSize:chartFontSize(10,fontSize),attributionLogo:true},timeScale:{...themeOptions.timeScale,timeVisible:true,secondsVisible:data.timeframe==='tick'||data.timeframe==='s1',rightOffset:3}});
  chartRef.current=chart;analysisChartResources.charts++;
  let markers:ReturnType<typeof createSeriesMarkers<Time>>|undefined;
  let zoomFrame:number|undefined;
  let observer:ResizeObserver|undefined,resize:()=>void=()=>{},crosshair:MouseEventHandler<Time>|undefined,windowResize=false;
  try{
   const main=candle?chart.addSeries(CandlestickSeries,{upColor:palette.up,downColor:palette.danger,wickUpColor:palette.up,wickDownColor:palette.danger,borderVisible:false}):chart.addSeries(LineSeries,{color:palette.accent,lineWidth:2,title:data.timeframe==='tick'?'Tick price':'Price'});
   if(candle)main.setData(ordered.map(bar=>({...timed(bar),open:value(bar,'open')!,high:value(bar,'high')!,low:value(bar,'low')!,close:value(bar,'close')!})));
   else main.setData(ordered.map(bar=>Number.isFinite(price(bar))?{...timed(bar),value:price(bar)!}:timed(bar)));
   const tradeAutoscale:AutoscaleInfoProvider=original=>{
    const info=original();if(!info||!tradePrices.length)return info;
    // Marker autoscaling does not include stacked text heights. Reserve room
    // for Match plus each price, using the current global chart font size.
    return {...info,margins:{above:info.margins?.above??0,below:Math.max(info.margins?.below??0,chart.options().layout.fontSize*1.4*(tradePrices.length+1)+44)}};
   };
   main.applyOptions({autoscaleInfoProvider:tradeAutoscale});
   const matchMarker=(color:string)=>analysisMatchMarkers(data,color);
   markers=createSeriesMarkers(main,matchMarker(palette.accent));
   const panes=new Map<string,number>();
   const histograms:{series:ReturnType<typeof chart.addSeries<'Histogram'>>;field:string}[]=[];
   const histogramData=(field:string,theme:string)=>{
    const colors=getThemePalette(theme);
    return ordered.map(bar=>{const number=value(bar,field),positive=/histogram/i.test(field)?(number??0)>=0:(value(bar,'close')??0)>=(value(bar,'open')??0);return Number.isFinite(number)?{...timed(bar),value:number!,color:`${positive?colors.up:colors.danger}${/volume/i.test(field)?'88':'ff'}`}:timed(bar);});
   };
   selected.forEach(field=>{
    const group=analysisFieldGroup(field);let pane=0;
    if(!analysisPriceOverlay(field)){
     if(!panes.has(group)){const added=chart.addPane();added.setStretchFactor(1);panes.set(group,chart.panes().length-1);}
     pane=panes.get(group)!;
    }
    const options={color:colors[plotFields.indexOf(field)%colors.length],title:analysisFieldLabel(field),priceLineVisible:false,lastValueVisible:true};
    if(/volume(?:\[|$)|histogram/i.test(field)){
     const series=chart.addSeries(HistogramSeries,options,pane);series.setData(histogramData(field,document.documentElement.dataset.theme??'light'));histograms.push({series,field});
    }else{
     const series=chart.addSeries(LineSeries,{...options,lineWidth:1},pane);series.setData(ordered.map(bar=>{const number=value(bar,field);return Number.isFinite(number)?{...timed(bar),value:number!}:timed(bar);}));
    }
   });
   applyTheme.current=theme=>{
    const colors=getThemePalette(theme),range=chart.timeScale().getVisibleLogicalRange();
    chart.applyOptions(chartThemeOptions(colors));
    main.applyOptions(candle?{upColor:colors.up,downColor:colors.danger,wickUpColor:colors.up,wickDownColor:colors.danger}:{color:colors.accent});
    markers?.setMarkers(matchMarker(colors.accent));
    for(const {series,field} of histograms)series.setData(histogramData(field,theme));
    if(range)chart.timeScale().setVisibleLogicalRange(range);
   };
   chart.panes()[0]?.setStretchFactor(2.5);
   chart.timeScale().fitContent();
   // The chart applies fit/range changes on its render frame. Zoom after fit
   // completes, keeping the selected signal centered rather than the last bar.
   zoomFrame=requestAnimationFrame(()=>{
    const scale=chart.timeScale(),fittedRange=scale.getVisibleLogicalRange(),fittedSpacing=scale.options().barSpacing;
    if(fittedRange){
     const center=Math.max(0,ordered.findIndex(bar=>bar.time===data.matchTime)),span=Math.max(0,(fittedRange.to-fittedRange.from+1)/3-1);
     scale.setVisibleLogicalRange({from:center-span/2,to:center+span/2});
    }else scale.applyOptions({barSpacing:fittedSpacing*3});
    zoomFrame=requestAnimationFrame(()=>{
     if(host.current){host.current.dataset.fittedBarSpacing=String(fittedSpacing);host.current.dataset.initialBarSpacing=String(scale.options().barSpacing);}
    });
   });
   crosshair=event=>{if(typeof event.time==='number')setHover(ordered.find(bar=>bar.time===Math.round(Number(event.time)*1000))??null);else setHover(null);};
   chart.subscribeCrosshairMove(crosshair);analysisChartResources.listeners++;
   resize=()=>{if(host.current)chart.resize(Math.max(300,host.current.clientWidth),height);};
   if(typeof ResizeObserver!=='undefined'){observer=new ResizeObserver(resize);observer.observe(host.current);analysisChartResources.observers++;}
   else{window.addEventListener('resize',resize);windowResize=true;analysisChartResources.listeners++;}
  }catch(error){setChartError(errorText(error));}
  return ()=>{
   if(zoomFrame!==undefined)cancelAnimationFrame(zoomFrame);
   if(observer){observer.disconnect();analysisChartResources.observers--;}
   else if(windowResize){window.removeEventListener('resize',resize);analysisChartResources.listeners--;}
   if(crosshair){chart.unsubscribeCrosshairMove(crosshair);analysisChartResources.listeners--;}
   applyTheme.current=()=>{};markers?.detach();chart.remove();chartRef.current=null;analysisChartResources.charts--;
  };
 },[data,signature,height]);
 useEffect(()=>{applyTheme.current(theme);},[theme,data,signature,height]);
 useEffect(()=>{chartRef.current?.applyOptions({layout:{fontSize:chartFontSize(10,fontSize)}});},[fontSize]);
 return <>
  <div className="analysis-chart-readout" aria-label="Match bar values"><strong>{analysisDate(row?.time??data.matchTime)}</strong>{row&&Object.entries(row.values).filter(([field])=>rawFields.has(field.toLowerCase())&&field.toLowerCase()!=='timestamp').map(([field,number])=><span key={field}>{analysisFieldLabel(field)} <b>{format(number)}</b></span>)}</div>
  {!!data.profitableMatches?.length&&<p className="analysis-chart-note analysis-chart-legend"><span aria-hidden="true" style={{backgroundColor:profitableMatchColor}}/>Green dot: match whose simulated trade hit TP.</p>}
  {!!tradePrices.length&&<div className="analysis-chart-note analysis-trade-prices" aria-label="Simulated trade prices">{tradePrices.map(line=><span key={line}>{line}</span>)}</div>}
  {chartError&&<p className="analysis-error" role="alert">{chartError}</p>}
  <div className="analysis-plot-scroll"><div className="analysis-plot" data-testid="analysis-chart" data-chart-font-size={chartFontSize(10,fontSize)} data-pane-count={groups.length+1} data-indicator-count={selected.length} style={{height}} ref={host}/></div>
  {duplicates&&<p className="analysis-chart-note">Each observation is evaluated. Ticks sharing an identical timestamp display their last price on the chart.</p>}
  {!!plotFields.length&&<details className="analysis-chart-fields" open><summary>Enriched indicators and volume · {selected.length} displayed</summary><div>{plotFields.map((field,index)=><label key={field}><input type="checkbox" checked={shown.includes(field)} onChange={event=>setShown(current=>event.target.checked?[...current,field]:current.filter(value=>value!==field))}/><span style={{color:colors[index%colors.length]}}>{analysisFieldGroup(field)} · {analysisFieldLabel(field)}</span><b>{format(row?value(row,field):undefined)}</b></label>)}</div></details>}
 </>;
}

export function ScreenerAnalysisChart({api,selection,onClose,onNavigate}:{api:ScreenerAnalysisBackend;selection:AnalysisSelection;onClose():void;onNavigate(direction:-1|1):Promise<void>}){
 const dialog=useRef<HTMLElement>(null),close=useRef<HTMLButtonElement>(null),navigationBusy=useRef(false),[data,setData]=useState<AnalysisChart|null>(null),[error,setError]=useState(''),[loading,setLoading]=useState(true),[navigating,setNavigating]=useState(false),[retry,setRetry]=useState(0);
 const navigate=useRef(onNavigate);navigate.current=onNavigate;const closeAction=useRef(onClose);closeAction.current=onClose;
 useEffect(()=>{
  const previous=document.activeElement as HTMLElement|null;close.current?.focus();
  const key=(event:KeyboardEvent)=>{
   if(event.key==='Escape'){event.preventDefault();event.stopPropagation();closeAction.current();}
   if(event.key==='Tab'){
    const items=[...(dialog.current?.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), select:not(:disabled), [tabindex="0"]')??[])];
    const first=items[0],last=items.at(-1);
    if(event.shiftKey&&document.activeElement===first){event.preventDefault();last?.focus();}
    else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();first?.focus();}
   }
  };
  document.addEventListener('keydown',key);analysisChartResources.listeners++;
  return ()=>{document.removeEventListener('keydown',key);analysisChartResources.listeners--;if(previous?.isConnected)previous.focus();};
 },[]);
 useEffect(()=>{
  let current=true;setLoading(true);setData(null);setError('');
  void api.GetScreenerAnalysisChart(selection.result.id,selection.occurrence,120,80).then(value=>{if(current){setData(value);setLoading(false);}}).catch(error=>{if(current){setError(errorText(error));setLoading(false);}});
  return ()=>{current=false;};
 },[api,selection.result.id,selection.occurrence,retry]);
 const step=async(direction:-1|1)=>{if(navigationBusy.current)return;navigationBusy.current=true;setNavigating(true);setError('');try{await navigate.current(direction);}catch(error){setError(errorText(error));}finally{navigationBusy.current=false;setNavigating(false);}};
 return <div className="modal-backdrop analysis-chart-backdrop" onMouseDown={event=>{if(event.target===event.currentTarget)onClose();}}><section className="analysis-chart-modal" ref={dialog} role="dialog" aria-modal="true" aria-label="Analysis match chart">
  <header><div><span className="analysis-eyebrow">MATCH INSPECTION</span><h2>{selection.result.symbol.toUpperCase()||selection.result.name} <span>{selection.result.timeframe}</span></h2><p>{selection.result.name}</p></div><button ref={close} type="button" aria-label="Close analysis chart" onClick={onClose}>×</button></header>
  <nav aria-label="Matching occurrences"><button type="button" disabled={loading||navigating} onClick={()=>void step(-1)}>← Previous match</button><div><strong>Occurrence {selection.occurrence+1} of {data?.totalMatches??selection.result.matches}</strong><span>{data?analysisDate(data.matchTime):'Loading match…'}</span></div><button type="button" disabled={loading||navigating} onClick={()=>void step(1)}>Next match →</button></nav>
  <p className="analysis-chart-note">Closed bars from downloaded files. Previous / Next visits every occurrence, continuing across matching files.</p>
  {error&&<div className="analysis-error" role="alert">{error}<button type="button" onClick={()=>setRetry(value=>value+1)}>Retry chart</button></div>}
  {loading&&<p className="analysis-empty" role="status">Loading bars and enriched indicators around this match…</p>}
  {data&&<AnalysisPlot key={`${data.resultId}:${data.occurrence}`} data={data}/>}
  <footer>{data&&<span>{data.bars.length} nearby rows · matched row {data.matchIndex+1} · UTC</span>}<span>Lightweight Charts™ by TradingView</span></footer>
 </section></div>;
}

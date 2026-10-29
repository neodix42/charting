import {useEffect,useRef,useState} from 'react';
import {createChart,LineSeries,type IChartApi,type MouseEventHandler,type Time,type UTCTimestamp} from 'lightweight-charts';
import type {PredictionForecast} from '../workspace/predictions';
import {predictionBarLabel} from '../workspace/predictions';
import {PredictionBand} from '../charts/PredictionBand';
import {chartThemeOptions} from '../charts/theme';
import {getThemePalette} from '../workspace/themes';
import {chartFontSize,useAppearanceFontSize} from '../workspace/appearance';
import {errorText} from '../types';

export const predictionChartResources={charts:0,observers:0,listeners:0};
export function PredictionChart({forecast}:{forecast:PredictionForecast}){
 const host=useRef<HTMLDivElement>(null),chartRef=useRef<IChartApi|null>(null),fontSize=useAppearanceFontSize(),[error,setError]=useState(''),[hover,setHover]=useState('Move over the chart to inspect a bar.');
 useEffect(()=>{
  if(!host.current)return;
  setError('');
  if(!forecast.history.length||!forecast.points.length){setError('This forecast has no chart data.');return;}
  const palette=getThemePalette(document.documentElement.dataset.theme??'light'),options=chartThemeOptions(palette);
  const chart=createChart(host.current,{...options,width:Math.max(300,host.current.clientWidth),height:420,layout:{...options.layout,fontSize:chartFontSize(11,fontSize),fontFamily:'Inter, Ubuntu, system-ui, sans-serif'},timeScale:{...options.timeScale,fixLeftEdge:true,rightOffset:2,minBarSpacing:.1,tickMarkFormatter:(time:Time)=>predictionBarLabel(forecast,Number(time))},localization:{timeFormatter:(time:Time)=>predictionBarLabel(forecast,Number(time),true)}});
  chartRef.current=chart;predictionChartResources.charts++;
  let resize:ResizeObserver|undefined,theme:MutationObserver|undefined,band:PredictionBand|undefined,mouse:MouseEventHandler<Time>|undefined;
  try{
   const history=chart.addSeries(LineSeries,{color:palette.text,lineWidth:2,priceLineVisible:false,lastValueVisible:false,title:'Historical close'});
   // Numeric coordinates encode ordinal bars. Both axis and crosshair formatters
   // map them to real history timestamps or +N; they never imply future dates.
   history.setData(forecast.history.map((bar,index)=>({time:index+1 as UTCTimestamp,value:bar.close})));
   const last=forecast.history.at(-1)!,anchor={time:forecast.history.length as UTCTimestamp,value:last.close};
   const median=chart.addSeries(LineSeries,{color:palette.accent,lineWidth:2,priceLineVisible:false,lastValueVisible:false,title:'Median forecast'});
   median.setData([anchor,...forecast.points.map(point=>({time:forecast.history.length+point.step as UTCTimestamp,value:point.median}))]);
   const boundOptions={color:'#00000000',lineWidth:1 as const,priceLineVisible:false,lastValueVisible:false,crosshairMarkerVisible:false};
   for(const field of ['lower','upper'] as const)chart.addSeries(LineSeries,boundOptions).setData([anchor,...forecast.points.map(point=>({time:forecast.history.length+point.step as UTCTimestamp,value:point[field]}))]);
   band=new PredictionBand(median,[{time:anchor.time,lower:anchor.value,upper:anchor.value},...forecast.points.map(point=>({time:forecast.history.length+point.step as UTCTimestamp,lower:point.lower,upper:point.upper}))],`${palette.accent}33`);
   chart.panes()[0].attachPrimitive(band);
   const themeChanged=()=>{const colors=getThemePalette(document.documentElement.dataset.theme??'light');chart.applyOptions(chartThemeOptions(colors));history.applyOptions({color:colors.text});median.applyOptions({color:colors.accent});band?.setColor(`${colors.accent}33`);};
   theme=new MutationObserver(themeChanged);theme.observe(document.documentElement,{attributes:true,attributeFilter:['data-theme']});predictionChartResources.observers++;
   resize=new ResizeObserver(()=>{if(host.current)chart.resize(Math.max(300,host.current.clientWidth),420);});resize.observe(host.current);predictionChartResources.observers++;
   mouse=event=>{if(typeof event.time!=='number')return;const index=Number(event.time),historic=forecast.history[index-1],point=forecast.points.find(point=>point.step===index-forecast.history.length),format=(value:number)=>value.toLocaleString('en-US',{maximumSignificantDigits:8});setHover(historic?`${predictionBarLabel(forecast,index,true)} · Close ${format(historic.close)}`:point?`Future bar +${point.step} · Median ${format(point.median)} · P10 ${format(point.lower)} · P90 ${format(point.upper)}`:'');};
   chart.subscribeCrosshairMove(mouse);predictionChartResources.listeners++;
   chart.timeScale().setVisibleLogicalRange({from:Math.max(0,forecast.history.length-80),to:forecast.history.length+forecast.points.length+2});
  }catch(reason){setError(errorText(reason));}
  return()=>{if(resize){resize.disconnect();predictionChartResources.observers--;}if(theme){theme.disconnect();predictionChartResources.observers--;}if(mouse){chart.unsubscribeCrosshairMove(mouse);predictionChartResources.listeners--;}if(band)chart.panes()[0].detachPrimitive(band);chart.remove();chartRef.current=null;predictionChartResources.charts--;};
 },[forecast]);
 useEffect(()=>{chartRef.current?.applyOptions({layout:{fontSize:chartFontSize(11,fontSize)}});},[fontSize]);
 return <div className="prediction-chart"><div className="prediction-legend"><span><i className="prediction-history-key"/>Historical close</span><span><i className="prediction-median-key"/>Median forecast</span><span><i className="prediction-band-key"/>Estimated 10th–90th percentiles</span></div><div className="prediction-chart-actions"><button type="button" onClick={()=>chartRef.current?.timeScale().setVisibleLogicalRange({from:Math.max(0,forecast.history.length-80),to:forecast.history.length+forecast.points.length+2})}>Focus forecast</button><button type="button" onClick={()=>chartRef.current?.timeScale().fitContent()}>Show all history</button></div>{error&&<p role="alert" className="screener-error">{error}</p>}<div ref={host} className="prediction-plot" data-testid="prediction-chart" data-history-bars={forecast.history.length} data-forecast-bars={forecast.points.length} data-chart-font-size={chartFontSize(11,fontSize)} aria-label="Historical close and prediction chart"/><p className="prediction-readout" aria-live="off">{hover}</p><p className="muted">Future bars count from the last closed bar. Calendar dates are not assigned to future bars.</p></div>;
}

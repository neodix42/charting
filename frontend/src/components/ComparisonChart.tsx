import {useEffect,useRef,useState} from 'react';
import {createChart,LineSeries,LineStyle,type AutoscaleInfo,type IChartApi,type IPriceLine,type MouseEventHandler,type Time,type UTCTimestamp} from 'lightweight-charts';
import type {ComparisonResult} from '../workspace/comparison';
import {comparisonColor,comparisonDate,comparisonLabel,comparisonLineData,comparisonPercent,comparisonSource} from '../workspace/comparison';
import {chartThemeOptions} from '../charts/theme';
import {getThemePalette} from '../workspace/themes';
import {chartFontSize,useAppearanceFontSize} from '../workspace/appearance';
import {errorText} from '../types';

export const comparisonChartResources={charts:0,observers:0,listeners:0};
export const comparisonChartDiagnostics:{read:(time?:number)=>unknown}={read:()=>null};

export function ComparisonChart({result}:{result:ComparisonResult}){
 const host=useRef<HTMLDivElement>(null),chartRef=useRef<IChartApi|null>(null),fontSize=useAppearanceFontSize();
 const [error,setError]=useState(''),[hover,setHover]=useState<number|null>(null);
 const usable=result.series.filter(series=>!series.error&&series.points.some(point=>point.pct!==null));
 useEffect(()=>{
  if(!host.current||!usable.length)return;
  setError('');setHover(null);
  const palette=getThemePalette(document.documentElement.dataset.theme??'light'),themeOptions=chartThemeOptions(palette);
  const chart=createChart(host.current,{...themeOptions,width:Math.max(280,host.current.clientWidth),height:460,
   layout:{...themeOptions.layout,fontFamily:'Inter, Ubuntu, system-ui, sans-serif',fontSize:chartFontSize(12,fontSize)},
   leftPriceScale:{visible:true,borderColor:palette.chartBorder},rightPriceScale:{visible:false},
   grid:{vertLines:{visible:false},horzLines:{color:palette.chartGrid,style:LineStyle.Dotted}},
   timeScale:{...themeOptions.timeScale,rightOffset:1,minBarSpacing:.1,timeVisible:false,tickMarkFormatter:(time:Time)=>new Intl.DateTimeFormat('en-GB',{timeZone:'UTC',day:'numeric',month:'short'}).format(new Date(Number(time)*1000))},
   localization:{timeFormatter:(time:Time)=>comparisonDate(Number(time))}});
  chartRef.current=chart;comparisonChartResources.charts++;
  let resize:ResizeObserver|undefined,theme:MutationObserver|undefined,mouse:MouseEventHandler<Time>|undefined;
  let diagnostic:((time?:number)=>unknown)|undefined;
  let zeroLine:IPriceLine|undefined;
  try{
   const plotted=usable.map((series,index)=>{
    const line=chart.addSeries(LineSeries,{color:comparisonColor(series.asset.symbol),lineWidth:2,priceScaleId:'left',pointMarkersVisible:true,pointMarkersRadius:3,crosshairMarkerRadius:5,lastValueVisible:false,priceLineVisible:false,
     priceFormat:{type:'custom',formatter:(value:number)=>`${Number(value.toFixed(2))}%`,minMove:.01},autoscaleInfoProvider:(original:()=>AutoscaleInfo|null)=>{const info=original();return info?.priceRange?{...info,priceRange:{minValue:Math.min(0,info.priceRange.minValue),maxValue:Math.max(0,info.priceRange.maxValue)}}:info;}});
    line.setData(comparisonLineData(series.points,comparisonColor(series.asset.symbol)));
    // Separate markers retain real endpoint dots when the outgoing line is
    // transparent. This keeps gaps truthful with two bounded series per asset.
    line.applyOptions({pointMarkersVisible:false,crosshairMarkerBackgroundColor:comparisonColor(series.asset.symbol)});
    const dots=chart.addSeries(LineSeries,{color:comparisonColor(series.asset.symbol),priceScaleId:'left',lineVisible:false,pointMarkersVisible:true,pointMarkersRadius:3,crosshairMarkerVisible:false,lastValueVisible:false,priceLineVisible:false,priceFormat:{type:'custom',formatter:(value:number)=>`${Number(value.toFixed(2))}%`,minMove:.01}});
    dots.setData(series.points.map(point=>point.pct===null?{time:point.time as UTCTimestamp}:{time:point.time as UTCTimestamp,value:point.pct}));
    if(index===0)zeroLine=line.createPriceLine({price:0,color:palette.chartBorder,lineWidth:1,lineStyle:LineStyle.Dashed,axisLabelVisible:false});
    return{symbol:series.asset.symbol,line,dots};
   });
   const updateTheme=()=>{const next=getThemePalette(document.documentElement.dataset.theme??'light');chart.applyOptions({...chartThemeOptions(next),leftPriceScale:{borderColor:next.chartBorder},grid:{vertLines:{visible:false},horzLines:{color:next.chartGrid,style:LineStyle.Dotted}}});zeroLine?.applyOptions({color:next.chartBorder});};
   theme=new MutationObserver(updateTheme);theme.observe(document.documentElement,{attributes:true,attributeFilter:['data-theme']});comparisonChartResources.observers++;
   resize=new ResizeObserver(()=>{if(host.current)chart.resize(Math.max(280,host.current.clientWidth),460);});resize.observe(host.current);comparisonChartResources.observers++;
   mouse=event=>setHover(typeof event.time==='number'?Number(event.time):null);
   chart.subscribeCrosshairMove(mouse);comparisonChartResources.listeners++;chart.timeScale().fitContent();
   if(window.__CRYPTO_TEST__){diagnostic=(time?:number)=>({baselineTime:result.baselineTime,lastClosedTime:result.lastClosedTime,x:time===undefined?null:chart.timeScale().timeToCoordinate(time as UTCTimestamp),leftWidth:chart.priceScale('left').width(),series:plotted.map(item=>({symbol:item.symbol,line:item.line.data(),dots:item.dots.data()}))});comparisonChartDiagnostics.read=diagnostic;}
  }catch(reason){setError(errorText(reason));}
  return()=>{if(resize){resize.disconnect();comparisonChartResources.observers--;}if(theme){theme.disconnect();comparisonChartResources.observers--;}if(mouse){chart.unsubscribeCrosshairMove(mouse);comparisonChartResources.listeners--;}if(diagnostic&&comparisonChartDiagnostics.read===diagnostic)comparisonChartDiagnostics.read=()=>null;chart.remove();chartRef.current=null;comparisonChartResources.charts--;};
 },[result]);
 useEffect(()=>{chartRef.current?.applyOptions({layout:{fontSize:chartFontSize(12,fontSize)}});},[fontSize]);
 const date=hover??result.lastClosedTime;
 return <div className="comparison-chart">
  <div className="comparison-chart-heading"><div><h2>Change since the start of the period</h2><p>Daily market closes relative to {comparisonDate(result.baselineTime)} · UTC</p></div><button type="button" onClick={()=>chartRef.current?.timeScale().fitContent()}>Show entire period</button></div>
  {error&&<p className="screener-error" role="alert">{error}</p>}
  <div ref={host} className="comparison-plot" data-testid="comparison-chart" data-series-count={usable.length} data-baseline-time={result.baselineTime} aria-label="Daily percentage change since the common starting close"/>
  <div className="comparison-legend" aria-label="Comparison legend">{usable.map(series=>{
   const point=series.points.find(point=>point.time===date);
   return <div key={series.asset.symbol} title={`${comparisonSource(series.asset)} · ${series.asset.nativeId}`}><i style={{background:comparisonColor(series.asset.symbol)}}/><strong>{comparisonLabel(series.asset)}</strong><span className={(point?.pct??0)<0?'down':'up'}>{comparisonPercent(point?.pct)}</span></div>;
  })}</div>
  <p className="comparison-readout" aria-live="off">{comparisonDate(date)} UTC{hover===null?' · Latest completed daily close':''}</p>
 </div>;
}

import {useEffect,useRef,useState} from 'react';
import {createChart,LineSeries,LineStyle,type AutoscaleInfo,type IChartApi,type IPriceLine,type MouseEventHandler,type Time,type UTCTimestamp} from 'lightweight-charts';
import type {ComparisonResult} from '../workspace/comparison';
import {comparisonColor,comparisonDate,comparisonIdentity,comparisonSeriesLabel,comparisonLineData,comparisonPercent,comparisonPrice,comparisonSource} from '../workspace/comparison';
import {chartThemeOptions} from '../charts/theme';
import {getThemePalette} from '../workspace/themes';
import {chartFontSize,useAppearanceFontSize} from '../workspace/appearance';
import {errorText} from '../types';

export const comparisonChartResources={charts:0,observers:0,listeners:0};
export const comparisonChartDiagnostics:{read:(time?:number)=>unknown}={read:()=>null};

const hitDistance=(x:number,y:number,ax:number,ay:number,bx=ax,by=ay)=>{
 const dx=bx-ax,dy=by-ay,length=dx*dx+dy*dy,t=length?Math.max(0,Math.min(1,((x-ax)*dx+(y-ay)*dy)/length)):0;
 return Math.hypot(x-ax-t*dx,y-ay-t*dy);
};

export function ComparisonChart({result,hiddenAssets,onToggleAsset}:{result:ComparisonResult;hiddenAssets:ReadonlySet<string>;onToggleAsset:(symbol:string)=>void}){
 const host=useRef<HTMLDivElement>(null),chartRef=useRef<IChartApi|null>(null),fontSize=useAppearanceFontSize();
 const applyVisibility=useRef<((hidden:ReadonlySet<string>)=>void)|null>(null);
 const [error,setError]=useState(''),[hover,setHover]=useState<number|null>(null),[focus,setFocus]=useState<{symbol:string;x:number;y:number}|null>(null);
 const usable=result.series.filter(series=>!series.error&&series.points.some(point=>point.pct!==null));
 useEffect(()=>{
  if(!host.current||!usable.length)return;
  setError('');setHover(null);setFocus(null);
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
    const line=chart.addSeries(LineSeries,{color:comparisonColor(comparisonIdentity(series.asset)),lineWidth:2,priceScaleId:'left',pointMarkersVisible:true,pointMarkersRadius:3,crosshairMarkerRadius:5,lastValueVisible:false,priceLineVisible:false,
     priceFormat:{type:'custom',formatter:(value:number)=>`${Number(value.toFixed(2))}%`,minMove:.01},autoscaleInfoProvider:(original:()=>AutoscaleInfo|null)=>{const info=original();return info?.priceRange?{...info,priceRange:{minValue:Math.min(0,info.priceRange.minValue),maxValue:Math.max(0,info.priceRange.maxValue)}}:info;}});
    line.setData(comparisonLineData(series.points,comparisonColor(comparisonIdentity(series.asset))));
    // Separate markers retain real endpoint dots when the outgoing line is
    // transparent. This keeps gaps truthful with two bounded series per asset.
    line.applyOptions({pointMarkersVisible:false,crosshairMarkerBackgroundColor:comparisonColor(comparisonIdentity(series.asset))});
    const dots=chart.addSeries(LineSeries,{color:comparisonColor(comparisonIdentity(series.asset)),priceScaleId:'left',lineVisible:false,pointMarkersVisible:true,pointMarkersRadius:3,crosshairMarkerVisible:false,lastValueVisible:false,priceLineVisible:false,priceFormat:{type:'custom',formatter:(value:number)=>`${Number(value.toFixed(2))}%`,minMove:.01}});
    dots.setData(series.points.map(point=>point.pct===null?{time:point.time as UTCTimestamp}:{time:point.time as UTCTimestamp,value:point.pct}));
    if(index===0)zeroLine=line.createPriceLine({price:0,color:palette.chartBorder,lineWidth:1,lineStyle:LineStyle.Dashed,axisLabelVisible:false});
    return{id:comparisonIdentity(series.asset),symbol:series.asset.symbol,line,dots,visible:true,points:new Map(series.points.filter(point=>point.pct!==null).map(point=>[point.time,point.pct!]))};
   });
   let zeroOwner:typeof plotted[number]|undefined=plotted[0];
   const times=[...new Set(plotted.flatMap(item=>[...item.points.keys()]))].sort((a,b)=>a-b);
   const xAt=(index:number)=>chart.timeScale().timeToCoordinate(times[index] as UTCTimestamp);
   const lowerX=(x:number)=>{let low=0,high=times.length;while(low<high){const mid=(low+high)>>>1,value=xAt(mid);if(value===null)return times.length;if(value<x)low=mid+1;else high=mid;}return low;};
   let activeSymbol:string|null=null,styling=false;
   const highlight=(symbol:string|null,point?:{x:number;y:number})=>{
    if(symbol!==activeSymbol){
     const previous=activeSymbol;activeSymbol=symbol;styling=true;
     // Style updates synchronously emit another crosshair event. Let the real
     // pointer event or explicit clear finish before processing another hit.
     try{for(const item of plotted)if(item.id===previous||item.id===symbol)item.line.applyOptions({lineWidth:item.id===symbol?4:2});}finally{styling=false;}
    }
    setFocus(symbol&&point&&host.current?{symbol,x:Math.max(8,Math.min(point.x+chart.priceScale('left').width()+12,host.current.clientWidth-248)),y:Math.max(8,Math.min(point.y+12,host.current.clientHeight-160))}:null);
   };
   const updateTheme=()=>{const next=getThemePalette(document.documentElement.dataset.theme??'light');chart.applyOptions({...chartThemeOptions(next),leftPriceScale:{borderColor:next.chartBorder},grid:{vertLines:{visible:false},horzLines:{color:next.chartGrid,style:LineStyle.Dotted}}});zeroLine?.applyOptions({color:next.chartBorder});};
   // Keep visibility updates separate from chart creation and fitContent.
   applyVisibility.current=hidden=>{
    highlight(null);styling=true;
    try{
     for(const item of plotted){const visible=!hidden.has(item.id);if(item.visible!==visible){item.visible=visible;item.line.applyOptions({visible});item.dots.applyOptions({visible});}}
     const nextOwner=plotted.find(item=>item.visible);
     if(nextOwner!==zeroOwner){
      if(zeroOwner&&zeroLine)zeroOwner.line.removePriceLine(zeroLine);
      zeroOwner=nextOwner;
      zeroLine=zeroOwner?.line.createPriceLine({price:0,color:getThemePalette(document.documentElement.dataset.theme??'light').chartBorder,lineWidth:1,lineStyle:LineStyle.Dashed,axisLabelVisible:false});
     }
    }finally{styling=false;}
    setHover(null);
   };
   theme=new MutationObserver(updateTheme);theme.observe(document.documentElement,{attributes:true,attributeFilter:['data-theme']});comparisonChartResources.observers++;
   resize=new ResizeObserver(()=>{if(host.current)chart.resize(Math.max(280,host.current.clientWidth),460);highlight(null);});resize.observe(host.current);comparisonChartResources.observers++;
   mouse=event=>{
    if(styling)return;
    setHover(typeof event.time==='number'?Number(event.time):null);
    const point=event.point;
    // Resize/render recalculation replays saved coordinates without pointer
    // input. Those events may clear hover, but must never reactivate it.
    if(!point||!event.sourceEvent||point.x<0||point.y<0){highlight(null);return;}
    // Native hit testing also hits transparent gaps and dots-only line segments.
    // Test only real dots and adjacent daily segments using public coordinates.
    const radius=6,from=Math.max(0,lowerX(point.x-radius)-1),to=Math.min(times.length,lowerX(point.x+radius)+1);
    let nearest:string|null=null,distance=radius;
    for(const item of plotted.filter(item=>item.visible))for(let index=from;index<to;index++){
     const time=times[index],value=item.points.get(time),x=xAt(index);
     if(value===undefined||x===null)continue;
     const y=item.line.priceToCoordinate(value);if(y===null)continue;
     let candidate=hitDistance(point.x,point.y,x,y);
     const nextTime=times[index+1],nextValue=item.points.get(nextTime);
     if(nextTime-time===86400&&nextValue!==undefined){const nextX=xAt(index+1),nextY=item.line.priceToCoordinate(nextValue);if(nextX!==null&&nextY!==null)candidate=Math.min(candidate,hitDistance(point.x,point.y,x,y,nextX,nextY));}
     if(candidate<=distance){nearest=item.id;distance=candidate;}
    }
    highlight(nearest,point);
   };
   chart.subscribeCrosshairMove(mouse);comparisonChartResources.listeners++;chart.timeScale().fitContent();
   if(window.__CRYPTO_TEST__){diagnostic=(time?:number)=>({baselineTime:result.baselineTime,lastClosedTime:result.lastClosedTime,x:time===undefined?null:chart.timeScale().timeToCoordinate(time as UTCTimestamp),leftWidth:chart.priceScale('left').width(),logicalRange:chart.timeScale().getVisibleLogicalRange(),referenceSymbol:zeroOwner?.symbol??null,referenceId:zeroOwner?.id??null,hoveredSymbol:plotted.find(item=>item.id===activeSymbol)?.symbol??null,hoveredId:activeSymbol,series:plotted.map(item=>({id:item.id,symbol:item.symbol,line:item.line.data(),dots:item.dots.data(),visible:item.line.options().visible,dotsVisible:item.dots.options().visible,lineWidth:item.line.options().lineWidth,y:time===undefined||!item.points.has(time)?null:item.line.priceToCoordinate(item.points.get(time)!)}))});comparisonChartDiagnostics.read=diagnostic;}
  }catch(reason){setError(errorText(reason));}
  return()=>{applyVisibility.current=null;if(resize){resize.disconnect();comparisonChartResources.observers--;}if(theme){theme.disconnect();comparisonChartResources.observers--;}if(mouse){chart.unsubscribeCrosshairMove(mouse);comparisonChartResources.listeners--;}if(diagnostic&&comparisonChartDiagnostics.read===diagnostic)comparisonChartDiagnostics.read=()=>null;chart.remove();chartRef.current=null;comparisonChartResources.charts--;};
 },[result]);
 useEffect(()=>{applyVisibility.current?.(hiddenAssets);},[hiddenAssets,result]);
 useEffect(()=>{chartRef.current?.applyOptions({layout:{fontSize:chartFontSize(12,fontSize)}});},[fontSize]);
 const date=hover??result.lastClosedTime;
 const focusedSeries=focus?usable.find(series=>comparisonIdentity(series.asset)===focus.symbol):undefined,focused=focusedSeries?.asset;
 const focusedPoint=hover===null?undefined:focusedSeries?.points.find(point=>point.time===hover);
 return <div className="comparison-chart">
  <div className="comparison-chart-heading"><div><h2>Change since the start of the period</h2><p>Daily market closes relative to {comparisonDate(result.baselineTime)} · UTC</p></div><button type="button" onClick={()=>chartRef.current?.timeScale().fitContent()}>Show entire period</button></div>
  {error&&<p className="screener-error" role="alert">{error}</p>}
  <div className="comparison-plot-wrap"><div ref={host} className="comparison-plot" data-testid="comparison-chart" data-series-count={usable.length} data-baseline-time={result.baselineTime} aria-label="Daily percentage change since the common starting close"/>
   {focus&&focused&&<div className="comparison-line-tooltip" role="tooltip" data-asset-symbol={focused.symbol} style={{left:focus.x,top:focus.y,borderColor:comparisonColor(comparisonIdentity(focused))}}><strong>{comparisonSeriesLabel(focused,usable.map(series=>series.asset))}</strong><small>{focused.name}</small><small className="comparison-tooltip-price">Close {comparisonPrice(focusedPoint?.close,focused.quoteAsset)}</small><small>{hover===null?'Daily close unavailable':`${comparisonDate(hover)} UTC`}</small><small>{comparisonSource(focused)} · {focused.nativeId}</small></div>}
  </div>
  <div className="comparison-legend" aria-label="Comparison legend">{usable.map(series=>{
   const point=series.points.find(point=>point.time===date);
   const id=comparisonIdentity(series.asset),label=comparisonSeriesLabel(series.asset,usable.map(item=>item.asset)),visible=!hiddenAssets.has(id);
   return <button type="button" key={id} data-comparison-id={id} aria-label={label} aria-pressed={visible} data-highlighted={visible&&focus?.symbol===id} title={`${comparisonSource(series.asset)} · ${series.asset.nativeId}`} onClick={()=>onToggleAsset(id)}><i style={{background:visible?comparisonColor(comparisonIdentity(series.asset)):'var(--muted)'}}/><strong>{label}</strong><span className={(point?.pct??0)<0?'down':'up'}>{comparisonPercent(point?.pct)}</span><small className="comparison-legend-price">Close {comparisonPrice(point?.close,series.asset.quoteAsset)}</small></button>;
  })}</div>
  <p className="comparison-readout" aria-live="off">{comparisonDate(date)} UTC{hover===null?' · Latest completed daily close':''}</p>
 </div>;
}

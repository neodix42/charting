import {useEffect,useRef,useState} from 'react';
import {createChart,createSeriesMarkers,CrosshairMode,LineSeries,LineStyle,type AutoscaleInfo,type IChartApi,type IPriceLine,type ISeriesMarkersPluginApi,type MouseEventHandler,type Time,type UTCTimestamp} from 'lightweight-charts';
import type {ComparisonResult} from '../workspace/comparison';
import {comparisonColor,comparisonDate,comparisonIdentity,comparisonSeriesLabel,comparisonLineData,comparisonPercent,comparisonPrice,comparisonSource} from '../workspace/comparison';
import {chartThemeOptions} from '../charts/theme';
import {getThemePalette} from '../workspace/themes';
import {chartFontSize,useAppearanceFontSize} from '../workspace/appearance';
import {errorText} from '../types';

export const comparisonChartResources={charts:0,observers:0,listeners:0};
type ChartDiagnostic=(time?:number)=>Record<string,unknown>;
const diagnosticReaders=new Map<string,ChartDiagnostic>();
export interface ComparisonCursor {owner:string;time:number;id:string|null;normalizedY:number}
type CursorPosition=Omit<ComparisonCursor,'owner'>;
export const comparisonChartDiagnostics={
 read:(time?:number,id?:string)=>{
  const reader=id===undefined?(diagnosticReaders.get('single')??(diagnosticReaders.size===1?diagnosticReaders.values().next().value:undefined)):diagnosticReaders.get(id);
  return reader?.(time)??null;
 },
 readAll:(time?:number)=>[...diagnosticReaders].map(([chartId,reader])=>({...reader(time),chartId})),
};

const hitDistance=(x:number,y:number,ax:number,ay:number,bx=ax,by=ay)=>{
 const dx=bx-ax,dy=by-ay,length=dx*dx+dy*dy,t=length?Math.max(0,Math.min(1,((x-ax)*dx+(y-ay)*dy)/length)):0;
 return Math.hypot(x-ax-t*dx,y-ay-t*dy);
};

export function ComparisonChart({result,hiddenAssets,onToggleAsset,chartId='single',title,height=460,fillAvailable=false,cursor=null,onCursorChange,riseDays}:{result:ComparisonResult;hiddenAssets:ReadonlySet<string>;onToggleAsset:(symbol:string)=>void;chartId?:string;title?:string;height?:number;fillAvailable?:boolean;cursor?:ComparisonCursor|null;onCursorChange?:(chartId:string,position:CursorPosition|null)=>void;riseDays?:ReadonlyMap<string,readonly number[]>}){
 const host=useRef<HTMLDivElement>(null),chartRef=useRef<IChartApi|null>(null),fontSize=useAppearanceFontSize();
 const applyVisibility=useRef<((hidden:ReadonlySet<string>)=>void)|null>(null),applyCursor=useRef<((cursor:ComparisonCursor|null)=>void)|null>(null),applyMarkers=useRef<((days:ReadonlyMap<string,readonly number[]>|undefined)=>void)|null>(null),clearPointer=useRef<(()=>void)|null>(null),cursorCallback=useRef(onCursorChange);
 useEffect(()=>{cursorCallback.current=onCursorChange;},[onCursorChange]);
 const [error,setError]=useState(''),[hover,setHover]=useState<number|null>(null),[focus,setFocus]=useState<string|null>(null);
 const usable=result.series.filter(series=>!series.error&&series.points.some(point=>point.pct!==null));
 useEffect(()=>{
  if(!host.current||!usable.length)return;
  setError('');setHover(null);setFocus(null);
  const palette=getThemePalette(document.documentElement.dataset.theme??'light'),themeOptions=chartThemeOptions(palette);
  let plotWidth=Math.max(280,host.current.clientWidth),plotHeight=fillAvailable?Math.max(180,host.current.clientHeight):height;
  const chart=createChart(host.current,{...themeOptions,width:plotWidth,height:plotHeight,
   layout:{...themeOptions.layout,fontFamily:'Inter, Ubuntu, system-ui, sans-serif',fontSize:chartFontSize(12,fontSize)},
   leftPriceScale:{visible:true,borderColor:palette.chartBorder},rightPriceScale:{visible:false},
   grid:{vertLines:{visible:false},horzLines:{color:palette.chartGrid,style:LineStyle.Dotted}},
   // Normal mode preserves the selected ordinate and exact whitespace day.
   // Magnet mode can silently move a synchronized cursor to another series.
   crosshair:{...themeOptions.crosshair,mode:CrosshairMode.Normal,doNotSnapToHiddenSeriesIndices:false},
   timeScale:{...themeOptions.timeScale,rightOffset:1,minBarSpacing:.1,timeVisible:false,tickMarkFormatter:(time:Time)=>new Intl.DateTimeFormat('en-GB',{timeZone:'UTC',day:'numeric',month:'short'}).format(new Date(Number(time)*1000))},
   localization:{timeFormatter:(time:Time)=>comparisonDate(Number(time))}});
  chartRef.current=chart;comparisonChartResources.charts++;
  let resize:ResizeObserver|undefined,theme:MutationObserver|undefined,mouse:MouseEventHandler<Time>|undefined;
  let diagnostic:ChartDiagnostic|undefined;
  let zeroLine:IPriceLine|undefined;
  const markerPlugins:ISeriesMarkersPluginApi<Time>[]=[];
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
    const markers=createSeriesMarkers(dots,[],{autoScale:false});markerPlugins.push(markers);
    return{id:comparisonIdentity(series.asset),symbol:series.asset.symbol,line,dots,markers,riseDays:[] as readonly number[],visible:true,points:new Map(series.points.filter(point=>point.pct!==null).map(point=>[point.time,point.pct!]))};
   });
   let zeroOwner:typeof plotted[number]|undefined=plotted[0];
   const times=[...new Set(plotted.flatMap(item=>[...item.points.keys()]))].sort((a,b)=>a-b);
   const xAt=(index:number)=>chart.timeScale().timeToCoordinate(times[index] as UTCTimestamp);
   const lowerX=(x:number)=>{let low=0,high=times.length;while(low<high){const mid=(low+high)>>>1,value=xAt(mid);if(value===null)return times.length;if(value<x)low=mid+1;else high=mid;}return low;};
   let activeSymbol:string|null=null,pointerId:string|null=null,styling=false,synchronizing=false,currentCursor:ComparisonCursor|null=null;
   let nativeEvent:{time:number|null;point:{x:number;y:number}|null;physical:boolean}|null=null;
   let appliedCursor:{requestedTime:number|null;owner:string|null;normalizedY:number|null;visible:boolean;time:number|null;x:number|null;y:number|null;price:number|null;anchorId:string|null;reason:string}={requestedTime:null,owner:null,normalizedY:null,visible:false,time:null,x:null,y:null,price:null,anchorId:null,reason:'inactive'};
   const highlight=(candidate:string|null)=>{
    const symbol=plotted.some(item=>item.id===candidate&&item.visible)?candidate:null;
    if(symbol!==activeSymbol){
     const previous=activeSymbol;activeSymbol=symbol;styling=true;
     // Style updates synchronously emit another crosshair event. Let the real
     // pointer event or explicit clear finish before processing another hit.
     try{for(const item of plotted)if(item.id===previous||item.id===symbol)item.line.applyOptions({lineWidth:item.id===symbol?4:2});}finally{styling=false;}
    }
    setFocus(symbol);
   };
   const synchronize=(next:ComparisonCursor|null)=>{
    currentCursor=next;setHover(next?.time??null);highlight(next?.id??null);
    appliedCursor={requestedTime:next?.time??null,owner:next?.owner??null,normalizedY:next?.normalizedY??null,visible:false,time:null,x:null,y:null,price:null,anchorId:null,reason:next?'unavailable':'inactive'};
    const hide=(reason:string)=>{appliedCursor.reason=reason;synchronizing=true;try{chart.clearCrosshairPosition();}finally{synchronizing=false;}};
    if(!next){hide('inactive');return;}
    const {time,id,normalizedY}=next,scale=chart.timeScale(),pane=chart.paneSize();
    if(!Number.isFinite(time)||time%86400!==0||time<result.baselineTime||time>result.lastClosedTime||scale.timeToIndex(time as UTCTimestamp,false)===null){hide('outside-loaded-range');return;}
    const x=scale.timeToCoordinate(time as UTCTimestamp);
    // The native API intentionally finds a nearest bar and clamps to its visible
    // range. Prove exact time and visibility before calling it instead.
    if(x===null||x<0||x>pane.width){hide('outside-visible-range');return;}
    const anchor=id?plotted.find(item=>item.id===id&&item.visible&&item.points.has(time)):plotted.find(item=>item.visible&&item.points.has(time));
    if(!anchor){hide(plotted.some(item=>item.visible)?'missing-observation':'all-hidden');return;}
    // A hit line follows this pane's own rebased value. Empty-space movement
    // shares only the plot-height fraction; it never invents a market price.
    const price=id?anchor.points.get(time)!:anchor.line.coordinateToPrice(normalizedY*pane.height);
    const y=price===null?null:anchor.line.priceToCoordinate(price);
    if(price===null||!Number.isFinite(price)||y===null||y<0||y>pane.height){hide('outside-visible-price');return;}
    synchronizing=true;
    try{chart.setCrosshairPosition(price,time as UTCTimestamp,anchor.line);}finally{synchronizing=false;}
    appliedCursor={requestedTime:time,owner:next.owner,normalizedY,visible:true,time,x,y,price,anchorId:anchor.id,reason:'visible'};
   };
   applyCursor.current=synchronize;
   const publishCursor=(position:CursorPosition)=>{pointerId=position.id;const next={owner:chartId,...position};synchronize(next);cursorCallback.current?.(chartId,position);};
   clearPointer.current=()=>{pointerId=null;if(!currentCursor||currentCursor.owner===chartId)synchronize(null);cursorCallback.current?.(chartId,null);};
   const updateMarkers=(item:typeof plotted[number])=>item.markers.setMarkers(item.visible?item.riseDays.filter(time=>item.points.has(time)).map(time=>({time:time as UTCTimestamp,position:'belowBar',shape:'circle',size:0,text:'★',color:comparisonColor(item.id),id:`third-rise-${item.id}-${time}`})):[]);
   applyMarkers.current=days=>{styling=true;try{for(const item of plotted){item.riseDays=days?.get(item.id)??[];updateMarkers(item);}}finally{styling=false;}synchronize(currentCursor);};
   const updateTheme=()=>{const next=getThemePalette(document.documentElement.dataset.theme??'light');chart.applyOptions({...chartThemeOptions(next),leftPriceScale:{borderColor:next.chartBorder},grid:{vertLines:{visible:false},horzLines:{color:next.chartGrid,style:LineStyle.Dotted}}});zeroLine?.applyOptions({color:next.chartBorder});};
   // Keep visibility updates separate from chart creation and fitContent.
   applyVisibility.current=hidden=>{
    highlight(null);styling=true;
    try{
     for(const item of plotted){const visible=!hidden.has(item.id);if(item.visible!==visible){item.visible=visible;item.line.applyOptions({visible});item.dots.applyOptions({visible});updateMarkers(item);}}
     const nextOwner=plotted.find(item=>item.visible);
     if(nextOwner!==zeroOwner){
      if(zeroOwner&&zeroLine)zeroOwner.line.removePriceLine(zeroLine);
      zeroOwner=nextOwner;
      zeroLine=zeroOwner?.line.createPriceLine({price:0,color:getThemePalette(document.documentElement.dataset.theme??'light').chartBorder,lineWidth:1,lineStyle:LineStyle.Dashed,axisLabelVisible:false});
     }
    }finally{styling=false;}
    clearPointer.current?.();synchronize(currentCursor);
   };
   theme=new MutationObserver(updateTheme);theme.observe(document.documentElement,{attributes:true,attributeFilter:['data-theme']});comparisonChartResources.observers++;
   resize=new ResizeObserver(()=>{
    if(!host.current)return;
    const width=Math.max(280,host.current.clientWidth),nextHeight=fillAvailable?Math.max(180,host.current.clientHeight):height;
    if(width===plotWidth&&nextHeight===plotHeight)return;
    const range=chart.timeScale().getVisibleLogicalRange();
    plotWidth=width;plotHeight=nextHeight;
    chart.resize(width,nextHeight);
    if(range)chart.timeScale().setVisibleLogicalRange(range);
    clearPointer.current?.();synchronize(currentCursor);
   });resize.observe(host.current);comparisonChartResources.observers++;
   mouse=event=>{
    nativeEvent={time:typeof event.time==='number'?Number(event.time):null,point:event.point?{x:event.point.x,y:event.point.y}:null,physical:!!event.sourceEvent};
    if(styling||synchronizing)return;
    if(!event.sourceEvent){synchronize(currentCursor);return;}
    const point=event.point;
    // Only physical input can own the group. Programmatic cursor/style events
    // may refresh its rendering but never publish another hover or steal it.
    const pane=chart.paneSize();
    if(!point||point.x<0||point.y<0||point.x>pane.width||point.y>pane.height){clearPointer.current?.();return;}
    const time=chart.timeScale().coordinateToTime(point.x);
    if(typeof time!=='number'){clearPointer.current?.();return;}
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
    publishCursor({time:Number(time),id:nearest,normalizedY:point.y/pane.height});
   };
   chart.subscribeCrosshairMove(mouse);comparisonChartResources.listeners++;chart.timeScale().fitContent();
   if(window.__CRYPTO_TEST__){diagnostic=(time?:number)=>({chartId,height:plotHeight,width:plotWidth,fillAvailable,paneSize:chart.paneSize(),baselineTime:result.baselineTime,lastClosedTime:result.lastClosedTime,x:time===undefined?null:chart.timeScale().timeToCoordinate(time as UTCTimestamp),leftWidth:chart.priceScale('left').width(),logicalRange:chart.timeScale().getVisibleLogicalRange(),referenceSymbol:zeroOwner?.symbol??null,referenceId:zeroOwner?.id??null,hoveredSymbol:plotted.find(item=>item.id===activeSymbol)?.symbol??null,hoveredId:activeSymbol,pointerId,cursor:{...appliedCursor},nativeEvent,series:plotted.map(item=>({id:item.id,symbol:item.symbol,line:item.line.data(),dots:item.dots.data(),markers:item.markers.markers(),visible:item.line.options().visible,dotsVisible:item.dots.options().visible,lineWidth:item.line.options().lineWidth,y:time===undefined||!item.points.has(time)?null:item.line.priceToCoordinate(item.points.get(time)!)}))});diagnosticReaders.set(chartId,diagnostic);}
  }catch(reason){setError(errorText(reason));}
  return()=>{cursorCallback.current?.(chartId,null);clearPointer.current=null;applyCursor.current=null;applyMarkers.current=null;applyVisibility.current=null;if(resize){resize.disconnect();comparisonChartResources.observers--;}if(theme){theme.disconnect();comparisonChartResources.observers--;}if(mouse){chart.unsubscribeCrosshairMove(mouse);comparisonChartResources.listeners--;}if(diagnostic&&diagnosticReaders.get(chartId)===diagnostic)diagnosticReaders.delete(chartId);for(const plugin of markerPlugins)plugin.detach();chart.remove();chartRef.current=null;comparisonChartResources.charts--;};
 },[result,chartId,height,fillAvailable]);
 useEffect(()=>{applyVisibility.current?.(hiddenAssets);},[hiddenAssets,result,chartId,height,fillAvailable]);
 useEffect(()=>{applyMarkers.current?.(riseDays);},[riseDays,result,chartId,height,fillAvailable]);
 useEffect(()=>{applyCursor.current?.(cursor);},[cursor,hiddenAssets,result,chartId,height,fillAvailable]);
 useEffect(()=>{chartRef.current?.applyOptions({layout:{fontSize:chartFontSize(12,fontSize)}});},[fontSize]);
 const date=hover??result.lastClosedTime;
 const historical=result.lastClosedTime<Math.floor(result.asOf/86400)*86400-86400;
 return <div className={`comparison-chart${fillAvailable?' comparison-chart-fluid':''}`}>
  <div className="comparison-chart-heading"><div><h2>{title??'Change since the start of the period'}</h2><p>Daily market closes relative to {comparisonDate(result.baselineTime)} · UTC</p></div><button type="button" onClick={()=>chartRef.current?.timeScale().fitContent()}>Show entire period</button></div>
  {error&&<p className="screener-error" role="alert">{error}</p>}
  <div className="comparison-plot-wrap"><div ref={host} onPointerLeave={()=>clearPointer.current?.()} onPointerCancel={()=>clearPointer.current?.()} className="comparison-plot" style={fillAvailable?undefined:{height}} data-testid="comparison-chart" data-comparison-chart-id={chartId} data-fill-available={fillAvailable} data-series-count={usable.length} data-baseline-time={result.baselineTime} aria-label="Daily percentage change since the common starting close"/>
  </div>
  <div className="comparison-legend" aria-label="Comparison legend">{usable.map(series=>{
   const point=series.points.find(point=>point.time===date);
   const id=comparisonIdentity(series.asset),label=comparisonSeriesLabel(series.asset,usable.map(item=>item.asset)),visible=!hiddenAssets.has(id);
   return <button type="button" key={id} data-comparison-id={id} aria-label={label} aria-pressed={visible} data-highlighted={visible&&focus===id} title={`${comparisonSource(series.asset)} · ${series.asset.nativeId}`} onClick={()=>onToggleAsset(id)}><i style={{background:visible?comparisonColor(comparisonIdentity(series.asset)):'var(--muted)'}}/><strong>{label}</strong><span className={(point?.pct??0)<0?'down':'up'}>{comparisonPercent(point?.pct)}</span><small className="comparison-legend-price">Close {comparisonPrice(point?.close,series.asset.quoteAsset)}</small></button>;
  })}</div>
  <p className="comparison-readout" aria-live="off">{comparisonDate(date)} UTC{hover===null?historical?' · Final daily close in this window':' · Latest completed daily close':''}</p>
 </div>;
}

import {HistogramSeries,LineSeries,LineStyle,type IChartApi,type IPaneApi,type ISeriesApi,type LineWidth,type PriceFormatCustom,type Time,type UTCTimestamp} from 'lightweight-charts';
import {intervalSeconds,type Candle,type IndicatorConfig,type Interval} from '../../types';
import {CloudPrimitive} from './CloudPrimitive';
import {ema,ichimoku,macd,sma,type IndicatorPoint,type MACDValues} from './math';

interface Layer {type:IndicatorConfig['type'];lines:ISeriesApi<'Line'>[];histogram?:ISeriesApi<'Histogram'>;cloud?:CloudPrimitive;pane?:IPaneApi<Time>}
const defaultPaneRatio=.15; // LWC starts the main pane at factor 2; the original MACD factor was .3.
const colors={sma:'#e9af55',ema:'#48a9f8',ichimoku:'#4387ef',macd:'#48a9f8'};
const lineData=(points:readonly IndicatorPoint[])=>points.map(point=>({time:point.time as UTCTimestamp,value:point.value}));
function macdFormat(values:MACDValues):PriceFormatCustom{
 let magnitude=0;for(const points of [values.macd,values.signal,values.histogram])for(const point of points)magnitude=Math.max(magnitude,Math.abs(point.value));
 // Preserve roughly six significant figures even when the underlying asset trades far below one cent.
 const exponent=magnitude>0&&Number.isFinite(magnitude)?Math.max(-308,Math.min(0,Math.floor(Math.log10(magnitude))-5)):-8;
 // Supplying the decimal base directly avoids reciprocal rounding (1 / 1e-18 is not exactly 1e18).
 return {type:'custom',minMove:10**exponent,base:10**(-exponent),formatter:(value:number)=>Number(value.toPrecision(6)).toString()};
}

/** Owns only indicator series; changing parameters never recreates the chart or its market subscription. */
export class IndicatorLayer {
 private readonly layers=new Map<string,Layer>();
 private configKey='';
 private disposed=false;
 private previousBars:readonly Candle[]|null=null;
 private previousTimeframe:Interval|null=null;
 private observer:ResizeObserver|null=null;
 private observeFrame:number|undefined;
 private resizeTimer:ReturnType<typeof setTimeout>|undefined;
 constructor(private readonly chart:IChartApi,_mainSeries:ISeriesApi<'Candlestick'>,private readonly onPaneResize?:()=>void){
  if(onPaneResize&&typeof ResizeObserver!=='undefined')this.observer=new ResizeObserver(()=>{
   if(this.resizeTimer!==undefined)return;
   this.resizeTimer=setTimeout(()=>{this.resizeTimer=undefined;if(!this.disposed)this.onPaneResize?.();},100);
  });
 }
 private observePanes(){
  if(!this.observer||this.observeFrame!==undefined)return;
  this.observeFrame=requestAnimationFrame(()=>{
   this.observeFrame=undefined;if(this.disposed)return;
   this.observer!.disconnect();
   for(const pane of this.chart.panes()){const element=pane.getHTMLElement();if(element)this.observer!.observe(element);}
  });
 }
 /** Read live factors rather than rounded heights so window resizing cannot change the saved preference. */
 capturePaneLayout(configs:readonly IndicatorConfig[]):IndicatorConfig[]|null{
  if(this.disposed||!configs.some(config=>config.type==='macd'))return null;
  const main=this.chart.panes()[0]?.getStretchFactor();if(!main||!Number.isFinite(main))return null;
  let changed=false;
  const result=configs.map(config=>{
   const pane=this.layers.get(config.id)?.pane;if(config.type!=='macd'||!pane)return config;
   const ratio=Math.max(.001,Math.min(1000,Number((pane.getStretchFactor()/main).toFixed(8))));
   if(!Number.isFinite(ratio)||Math.abs(ratio-(config.paneStretchFactor??defaultPaneRatio))<.00000001)return config;
   changed=true;return {...config,paneStretchFactor:ratio};
  });
  return changed?result:null;
 }
 private addLine(color:string,title:string,paneIndex=0,lineWidth:LineWidth=1){return this.chart.addSeries(LineSeries,{color,title,lineWidth,priceLineVisible:false,lastValueVisible:false,crosshairMarkerVisible:false},paneIndex);}
 private create(config:IndicatorConfig):Layer{
  const color=config.color||colors[config.type];
  if(config.type==='sma'||config.type==='ema')return {type:config.type,lines:[this.addLine(color,config.type.toUpperCase(),0,config.lineWidth??2)]};
  if(config.type==='ichimoku'){
   const lines=[this.addLine(color,'Ichimoku conversion'),this.addLine('#d85f68','Ichimoku base'),this.addLine('#26a69a','Ichimoku span A'),this.addLine('#ef5350','Ichimoku span B'),this.addLine('#ab78d2','Ichimoku lagging')];
   const cloud=new CloudPrimitive();lines[2].attachPrimitive(cloud);return {type:config.type,lines,cloud};
  }
  // Each MACD owns a distinct pane; its scale stays linear and independent of the chart price scale.
  const pane=this.chart.addPane(),index=pane.paneIndex();pane.setStretchFactor(this.chart.panes()[0].getStretchFactor()*(config.paneStretchFactor??defaultPaneRatio));
  const histogram=this.chart.addSeries(HistogramSeries,{title:'MACD histogram',base:0,priceLineVisible:false,lastValueVisible:false,priceFormat:macdFormat({macd:[],signal:[],histogram:[]})},index);
  const lines=[this.addLine(color,'MACD',index),this.addLine('#e9af55','MACD signal',index)];
  lines[0].createPriceLine({price:0,color:'#81909e',lineWidth:1,lineStyle:LineStyle.Dashed,axisLabelVisible:false});
  histogram.priceScale().applyOptions({mode:0,autoScale:true,scaleMargins:{top:.12,bottom:.12}});
  return {type:config.type,lines,histogram,pane};
 }
 private remove(id:string){
  const layer=this.layers.get(id);if(!layer)return;
  if(layer.cloud)layer.lines[2].detachPrimitive(layer.cloud);
  for(const series of layer.lines)this.chart.removeSeries(series);
  if(layer.histogram)this.chart.removeSeries(layer.histogram);
  this.layers.delete(id);
 }
 update(bars:readonly Candle[],configs:readonly IndicatorConfig[],timeframe:Interval,preserveViewport=true):void{
  if(this.disposed)return;
  const nextKey=JSON.stringify(configs.map(config=>({id:config.id,type:config.type,parameters:config.parameters,color:config.color,lineWidth:config.type==='sma'||config.type==='ema'?config.lineWidth??2:undefined}))),changed=nextKey!==this.configKey;
  if(!changed&&bars===this.previousBars&&timeframe===this.previousTimeframe)return;
  // Historical charts preserve their absolute time window. Live charts keep their zoom and re-anchor the workspace gap.
  const range=changed&&preserveViewport?this.chart.timeScale().getVisibleRange():null;
  const expected=new Map(configs.map(config=>[config.id,config]));
  for(const [id,layer] of this.layers)if(!expected.has(id)||expected.get(id)!.type!==layer.type)this.remove(id);
  for(const config of configs){
   let layer=this.layers.get(config.id);if(!layer){layer=this.create(config);this.layers.set(config.id,layer);}
   const params=config.parameters,color=config.color||colors[config.type];layer.lines[0].applyOptions({color});
   switch(config.type){
    case 'sma':case 'ema':{
     const values=(config.type==='sma'?sma:ema)(bars,params.period??20);
     // At exactly one complete window there is one valid average, so no line segment exists yet.
     layer.lines[0].applyOptions({pointMarkersVisible:values.length===1,pointMarkersRadius:3,lineWidth:config.lineWidth??2});layer.lines[0].setData(lineData(values));break;
    }
    case 'ichimoku':{
     const values=ichimoku(bars,params.conversion??9,params.base??26,params.spanB??52,params.displacement??26,intervalSeconds[timeframe]);
     [values.conversion,values.base,values.leadingA,values.leadingB,values.lagging].forEach((points,index)=>layer!.lines[index].setData(lineData(points)));layer.cloud!.setData(values.cloud);break;
    }
    case 'macd':{
     const values=macd(bars,params.fast??12,params.slow??26,params.signal??9);
     const priceFormat=macdFormat(values);for(const series of layer.lines)series.applyOptions({priceFormat});layer.histogram!.applyOptions({priceFormat});
     layer.lines[0].setData(lineData(values.macd));layer.lines[1].setData(lineData(values.signal));layer.histogram!.setData(values.histogram.map(point=>({time:point.time as UTCTimestamp,value:point.value,color:point.value>=0?'#26a69a':'#ef5350'})));break;
    }
   }
  }
  this.configKey=nextKey;
  this.previousBars=bars;this.previousTimeframe=timeframe;
  if(changed)this.observePanes();
  if(range&&changed)this.chart.timeScale().setVisibleRange(range);
 }
 dispose():void{if(this.disposed)return;this.disposed=true;if(this.observeFrame!==undefined)cancelAnimationFrame(this.observeFrame);if(this.resizeTimer!==undefined)clearTimeout(this.resizeTimer);this.observer?.disconnect();for(const id of [...this.layers.keys()])this.remove(id);}
}

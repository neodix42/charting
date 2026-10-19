import {HistogramSeries,LineSeries,LineStyle,type IChartApi,type ISeriesApi,type PriceFormatCustom,type UTCTimestamp} from 'lightweight-charts';
import {intervalSeconds,type Candle,type IndicatorConfig,type Interval} from '../../types';
import {CloudPrimitive} from './CloudPrimitive';
import {ema,ichimoku,macd,sma,type IndicatorPoint,type MACDValues} from './math';

interface Layer {type:IndicatorConfig['type'];lines:ISeriesApi<'Line'>[];histogram?:ISeriesApi<'Histogram'>;cloud?:CloudPrimitive}
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
 constructor(private readonly chart:IChartApi,_mainSeries:ISeriesApi<'Candlestick'>){}
 private addLine(color:string,title:string,paneIndex=0){return this.chart.addSeries(LineSeries,{color,title,lineWidth:1,priceLineVisible:false,lastValueVisible:false,crosshairMarkerVisible:false},paneIndex);}
 private create(config:IndicatorConfig):Layer{
  const color=config.color||colors[config.type];
  if(config.type==='sma'||config.type==='ema')return {type:config.type,lines:[this.addLine(color,config.type.toUpperCase())]};
  if(config.type==='ichimoku'){
   const lines=[this.addLine(color,'Ichimoku conversion'),this.addLine('#d85f68','Ichimoku base'),this.addLine('#26a69a','Ichimoku span A'),this.addLine('#ef5350','Ichimoku span B'),this.addLine('#ab78d2','Ichimoku lagging')];
   const cloud=new CloudPrimitive();lines[2].attachPrimitive(cloud);return {type:config.type,lines,cloud};
  }
  // Each MACD owns a distinct pane; its scale stays linear and independent of the chart price scale.
  const pane=this.chart.addPane(),index=pane.paneIndex();pane.setStretchFactor(.3);
  const histogram=this.chart.addSeries(HistogramSeries,{title:'MACD histogram',base:0,priceLineVisible:false,lastValueVisible:false,priceFormat:macdFormat({macd:[],signal:[],histogram:[]})},index);
  const lines=[this.addLine(color,'MACD',index),this.addLine('#e9af55','MACD signal',index)];
  lines[0].createPriceLine({price:0,color:'#81909e',lineWidth:1,lineStyle:LineStyle.Dashed,axisLabelVisible:false});
  histogram.priceScale().applyOptions({mode:0,autoScale:true,scaleMargins:{top:.12,bottom:.12}});
  return {type:config.type,lines,histogram};
 }
 private remove(id:string){
  const layer=this.layers.get(id);if(!layer)return;
  if(layer.cloud)layer.lines[2].detachPrimitive(layer.cloud);
  for(const series of layer.lines)this.chart.removeSeries(series);
  if(layer.histogram)this.chart.removeSeries(layer.histogram);
  this.layers.delete(id);
 }
 update(bars:readonly Candle[],configs:readonly IndicatorConfig[],timeframe:Interval):void{
  if(this.disposed)return;
  const nextKey=JSON.stringify(configs),changed=nextKey!==this.configKey;
  // Adding future cloud spans must not move the user's current time window.
  const range=changed?this.chart.timeScale().getVisibleRange():null;
  const expected=new Map(configs.map(config=>[config.id,config]));
  for(const [id,layer] of this.layers)if(!expected.has(id)||expected.get(id)!.type!==layer.type)this.remove(id);
  for(const config of configs){
   let layer=this.layers.get(config.id);if(!layer){layer=this.create(config);this.layers.set(config.id,layer);}
   const params=config.parameters,color=config.color||colors[config.type];layer.lines[0].applyOptions({color});
   switch(config.type){
    case 'sma':case 'ema':{
     const values=(config.type==='sma'?sma:ema)(bars,params.period??20);
     // At exactly one complete window there is one valid average, so no line segment exists yet.
     layer.lines[0].applyOptions({pointMarkersVisible:values.length===1,pointMarkersRadius:3});layer.lines[0].setData(lineData(values));break;
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
  if(range&&changed)this.chart.timeScale().setVisibleRange(range);
 }
 dispose():void{if(this.disposed)return;for(const id of [...this.layers.keys()])this.remove(id);this.disposed=true;}
}

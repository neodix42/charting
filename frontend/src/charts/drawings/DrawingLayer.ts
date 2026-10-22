import type {ChartOptions,IChartApi,IPaneApi,Time} from 'lightweight-charts';
import type {Candle,DrawingConfig,DrawingPoint,DrawingType} from '../../types';
import {DrawingPrimitive,type DrawingPriceSeries} from './DrawingPrimitive';
import {drawingCoordinateToLogical,drawingLogicalToTime} from './geometry';

/** Entries exist only when ChartCell explicitly enables its browser-test diagnostics. */
export const drawingDiagnostics=new Map<string,DrawingLayer>();

export class DrawingLayer {
 private readonly primitive:DrawingPrimitive;
 private readonly pane:IPaneApi<Time>;
 private readonly element:HTMLDivElement;
 private readonly overlay:HTMLDivElement;
 private readonly ownerDocument:Document;
 private readonly previousPosition:string|null;
 private disposed=false;
 private tool:DrawingType|null=null;
 private bars:readonly Candle[]=[];
 private step=60;
 private draft:DrawingPoint[]=[];
 private hover:DrawingPoint|null=null;
 private activePointer:number|null=null;
 private previousInteraction:Pick<ChartOptions,'handleScroll'|'handleScale'>|null=null;
 private readonly resize:ResizeObserver|null;
 constructor(private readonly chart:IChartApi,private series:DrawingPriceSeries,private readonly onCommit:(type:DrawingType,points:DrawingPoint[])=>void,private readonly onToolChange?:()=>void){
  this.element=chart.chartElement();this.ownerDocument=this.element.ownerDocument;
  const position=this.ownerDocument.defaultView?.getComputedStyle(this.element).position||this.element.style.position||'static';
  this.previousPosition=position==='static'?this.element.style.position||'':null;
  // Overlay coordinates and the price pane must share the chart's origin,
  // rather than the positioned chart cell above its header and OHLC rows.
  if(this.previousPosition!==null)this.element.style.position='relative';
  this.overlay=this.ownerDocument.createElement('div');this.overlay.className='chart-drawing-overlay';this.overlay.setAttribute('aria-hidden','true');this.overlay.dataset.testid='drawing-overlay';
  Object.assign(this.overlay.style,{position:'absolute',zIndex:'20',display:'none',cursor:'crosshair',touchAction:'none',userSelect:'none'});
  this.element.appendChild(this.overlay);this.pane=series.getPane();
  this.primitive=new DrawingPrimitive(series,()=>this.syncOverlay());this.pane.attachPrimitive(this.primitive);
  this.overlay.addEventListener('pointerdown',this.pointerDown);this.overlay.addEventListener('pointermove',this.pointerMove);this.overlay.addEventListener('pointerup',this.pointerUp);this.overlay.addEventListener('pointercancel',this.pointerCancel);this.overlay.addEventListener('pointerleave',this.pointerLeave);this.overlay.addEventListener('wheel',this.preventWheel,{passive:false});
  this.ownerDocument.addEventListener('keydown',this.keyDown);
  this.resize=typeof ResizeObserver==='undefined'?null:new ResizeObserver(()=>this.syncOverlay());this.resize?.observe(this.element);this.syncOverlay();
 }
 private syncOverlay(){
  if(this.disposed||!this.overlay)return;const pane=this.pane?.getHTMLElement();if(!pane)return;
  const origin=this.element.getBoundingClientRect(),bounds=pane.getBoundingClientRect(),size=this.chart.paneSize(this.pane.paneIndex()),left=this.chart.priceScale('left',this.pane.paneIndex()).width();
  Object.assign(this.overlay.style,{left:`${bounds.left-origin.left+left}px`,top:`${bounds.top-origin.top}px`,width:`${size.width}px`,height:`${size.height}px`});
 }
 private anchor(event:PointerEvent):DrawingPoint|null{
  const bounds=this.overlay.getBoundingClientRect(),x=event.clientX-bounds.left,y=event.clientY-bounds.top;if(x<0||y<0||x>bounds.width||y>bounds.height)return null;
  const scale=this.chart.timeScale(),logical=drawingCoordinateToLogical(scale,x),time=logical===null?null:drawingLogicalToTime(scale,logical,this.bars,this.step),price=this.series.coordinateToPrice(y);
  if(time===null||price===null||!Number.isFinite(price)||price<=0||!Number.isFinite(time))return null;
  const seconds=Math.round(time);return seconds>=1&&seconds<=253402300799?{time:seconds,price}:null;
 }
 private preview(){
  if(!this.tool){this.primitive.setPreview(null);return;}
  const points=this.tool==='trendline'?(this.draft.length?[this.draft[0],...(this.hover?[this.hover]:[])]:this.hover?[this.hover]:[]):this.hover?[this.hover]:[];
  this.primitive.setPreview(points.length?{type:this.tool,points}:null);
 }
 private pointerDown=(event:PointerEvent)=>{
  if(!this.tool||this.disposed||event.button!==0||this.activePointer!==null)return;
  event.preventDefault();this.activePointer=event.pointerId;
  // Synthetic events and a pointer cancelled by the host webview cannot acquire capture.
  try{this.overlay.setPointerCapture?.(event.pointerId);}catch{/* Pointerup inside the overlay still works. */}
  this.hover=this.anchor(event);this.preview();
 };
 private pointerMove=(event:PointerEvent)=>{if(!this.tool||this.disposed||(this.activePointer!==null&&this.activePointer!==event.pointerId))return;this.hover=this.anchor(event);this.preview();};
 private pointerUp=(event:PointerEvent)=>{
  if(!this.tool||this.disposed||this.activePointer!==event.pointerId)return;event.preventDefault();this.releasePointer();const point=this.anchor(event);if(!point)return;
  const type=this.tool;this.hover=point;
  if(type==='trendline'&&!this.draft.length){this.draft=[point];this.preview();return;}
  if(type==='trendline'&&this.draft[0].time===point.time&&this.draft[0].price===point.price)return;
  const points=type==='trendline'?[this.draft[0],point]:[point];this.setTool(null);this.onCommit(type,points);this.onToolChange?.();
 };
 private releasePointer(){const id=this.activePointer;this.activePointer=null;try{if(id!==null&&this.overlay.hasPointerCapture?.(id))this.overlay.releasePointerCapture(id);}catch{/* The webview may already have released or cancelled this pointer. */}}
 private pointerCancel=(event:PointerEvent)=>{if(this.activePointer!==event.pointerId)return;this.releasePointer();this.hover=null;this.preview();};
 private pointerLeave=()=>{if(this.activePointer!==null)return;this.hover=null;this.preview();};
 private preventWheel=(event:WheelEvent)=>{if(this.tool){event.preventDefault();event.stopPropagation();}};
 private keyDown=(event:KeyboardEvent)=>{if(event.key==='Escape'&&this.tool){event.preventDefault();this.setTool(null);this.onToolChange?.();}};
 update(drawings:readonly DrawingConfig[],bars:readonly Candle[],timeframeSeconds:number){if(this.disposed)return;this.bars=bars;this.step=timeframeSeconds;this.primitive.setData(drawings,bars,timeframeSeconds);}
 setSeries(series:DrawingPriceSeries){if(this.disposed||series===this.series)return;this.series=series;this.primitive.setSeries(series);}
 setTool(tool:DrawingType|null){
  if(this.disposed||tool===this.tool)return;
  this.releasePointer();this.draft=[];this.hover=null;
  if(tool&&!this.tool){const options=this.chart.options();this.previousInteraction=structuredClone({handleScroll:options.handleScroll,handleScale:options.handleScale});this.chart.applyOptions({handleScroll:false,handleScale:false});}
  if(!tool&&this.previousInteraction){this.chart.applyOptions(this.previousInteraction);this.previousInteraction=null;}
  this.tool=tool;this.overlay.style.display=tool?'block':'none';this.syncOverlay();this.preview();
 }
 read(){const size=this.chart.paneSize(this.pane.paneIndex());return {tool:this.tool,draft:this.draft.map(point=>({...point})),drawings:this.primitive.read(),preview:this.primitive.readPreview(),width:size.width,height:size.height};}
 dispose(){
  if(this.disposed)return;this.setTool(null);this.disposed=true;this.resize?.disconnect();
  this.overlay.removeEventListener('pointerdown',this.pointerDown);this.overlay.removeEventListener('pointermove',this.pointerMove);this.overlay.removeEventListener('pointerup',this.pointerUp);this.overlay.removeEventListener('pointercancel',this.pointerCancel);this.overlay.removeEventListener('pointerleave',this.pointerLeave);this.overlay.removeEventListener('wheel',this.preventWheel);this.ownerDocument.removeEventListener('keydown',this.keyDown);
  this.pane.detachPrimitive(this.primitive);this.overlay.remove();if(this.previousPosition!==null)this.element.style.position=this.previousPosition;this.bars=[];this.draft=[];this.hover=null;
 }
}

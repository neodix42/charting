import type {ChartOptions,IChartApi,IPaneApi,Time} from 'lightweight-charts';
import type {Candle,DrawingConfig,DrawingPoint,DrawingType} from '../../types';
import {DrawingPrimitive,type DrawingPriceSeries} from './DrawingPrimitive';
import {drawingCoordinateToLogical,drawingLogicalToTime,type ScreenPoint} from './geometry';
import {hitTestDrawing} from './hitTest';

/** Entries exist only when ChartCell explicitly enables its browser-test diagnostics. */
export const drawingDiagnostics=new Map<string,DrawingLayer>();

interface MovingDrawing {
 drawing:DrawingConfig;
 origin:ScreenPoint;
 pointer:ScreenPoint;
 coordinates:ScreenPoint[];
 points:DrawingPoint[];
}

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
 private drawings:readonly DrawingConfig[]=[];
 private step=60;
 private draft:DrawingPoint[]=[];
 private hover:DrawingPoint|null=null;
 private moving:MovingDrawing|null=null;
 private activePointer:number|null=null;
 private previousInteraction:Pick<ChartOptions,'handleScroll'|'handleScale'>|null=null;
 private readonly resize:ResizeObserver|null;
 constructor(private readonly chart:IChartApi,private series:DrawingPriceSeries,private readonly onCommit:(type:DrawingType,points:DrawingPoint[])=>void,private readonly onToolChange?:()=>void,private readonly onMove?:(id:string,points:DrawingPoint[])=>void,private readonly onMoveChange?:(moving:boolean)=>void){
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
  this.element.addEventListener('dblclick',this.doubleClick,true);this.ownerDocument.addEventListener('keydown',this.keyDown);
  this.resize=typeof ResizeObserver==='undefined'?null:new ResizeObserver(()=>this.syncOverlay());this.resize?.observe(this.element);this.syncOverlay();
 }
 private syncOverlay(){
  if(this.disposed||!this.overlay)return;const pane=this.pane?.getHTMLElement();if(!pane)return;
  const origin=this.element.getBoundingClientRect(),bounds=pane.getBoundingClientRect(),size=this.chart.paneSize(this.pane.paneIndex()),left=this.chart.priceScale('left',this.pane.paneIndex()).width();
  Object.assign(this.overlay.style,{left:`${bounds.left-origin.left+left}px`,top:`${bounds.top-origin.top}px`,width:`${size.width}px`,height:`${size.height}px`});
  // Live candles can change the viewport while the pointer is stationary.
  // Refresh the anchors against that viewport without recursively invalidating it.
  if(this.moving){const points=this.translatedPoints(this.moving.pointer);if(points&&points.some((point,index)=>point.time!==this.moving!.points[index].time||point.price!==this.moving!.points[index].price)){this.moving.points=points;this.preview();}}
 }
 /** A hidden overlay has no browser layout box; selection always uses the actual pane. */
 private panePoint(event:MouseEvent):ScreenPoint|null{
  const bounds=this.pane.getHTMLElement()?.getBoundingClientRect();if(!bounds)return null;
  const size=this.chart.paneSize(this.pane.paneIndex()),left=this.chart.priceScale('left',this.pane.paneIndex()).width(),x=event.clientX-bounds.left-left,y=event.clientY-bounds.top;
  return Number.isFinite(x)&&Number.isFinite(y)&&x>=0&&y>=0&&x<=size.width&&y<=size.height?{x,y}:null;
 }
 private anchorAt(point:ScreenPoint):DrawingPoint|null{
  const scale=this.chart.timeScale(),logical=drawingCoordinateToLogical(scale,point.x),time=logical===null?null:drawingLogicalToTime(scale,logical,this.bars,this.step),price=this.series.coordinateToPrice(point.y);
  if(time===null||price===null||!Number.isFinite(price)||price<=0||!Number.isFinite(time))return null;
  const seconds=Math.round(time);return seconds>=1&&seconds<=253402300799?{time:seconds,price}:null;
 }
 private anchor(event:PointerEvent):DrawingPoint|null{const point=this.panePoint(event);return point?this.anchorAt(point):null;}
 private preview(){
  if(this.moving){this.primitive.setPreview({type:this.moving.drawing.type,points:this.moving.points});return;}
  if(!this.tool){this.primitive.setPreview(null);return;}
  const points=this.tool==='trendline'?(this.draft.length?[this.draft[0],...(this.hover?[this.hover]:[])]:this.hover?[this.hover]:[]):this.hover?[this.hover]:[];
  this.primitive.setPreview(points.length?{type:this.tool,points}:null);
 }
 private renderData(){this.primitive.setData(this.moving?this.drawings.filter(drawing=>drawing.id!==this.moving!.drawing.id):this.drawings,this.bars,this.step);}
 private doubleClick=(event:MouseEvent)=>{
  if(this.disposed||this.tool||this.moving||!this.onMove||event.button!==0)return;
  const point=this.panePoint(event);if(!point)return;
  const size=this.chart.paneSize(this.pane.paneIndex()),projected=this.primitive.read(),id=hitTestDrawing(projected,point,size.width,size.height),drawing=this.drawings.find(item=>item.id===id),item=projected.find(item=>item.id===id);
  if(!drawing||!item||drawing.points.length!==(drawing.type==='trendline'?2:1))return;
  // Reject malformed saved anchors rather than starting an unplaceable move.
  if(drawing.points.some(anchor=>!Number.isInteger(anchor.time)||anchor.time<1||anchor.time>253402300799||!Number.isFinite(anchor.price)||anchor.price<=0)||drawing.type==='trendline'&&drawing.points[0].time===drawing.points[1].time&&drawing.points[0].price===drawing.points[1].price)return;
  event.preventDefault();event.stopImmediatePropagation();this.releasePointer();this.draft=[];this.hover=null;
  this.moving={drawing:{...drawing,points:drawing.points.map(anchor=>({...anchor}))},origin:point,pointer:point,coordinates:item.coordinates.map(anchor=>({...anchor})),points:drawing.points.map(anchor=>({...anchor}))};
  this.acquireInteraction();this.overlay.style.display='block';this.overlay.style.cursor='move';this.syncOverlay();this.renderData();this.preview();this.onMoveChange?.(true);
 };
 private translatedPoints(point:ScreenPoint):DrawingPoint[]|null{
  const moving=this.moving;if(!moving)return null;
  const dx=point.x-moving.origin.x,dy=point.y-moving.origin.y,points:DrawingPoint[]=[];
  // Convert each translated screen endpoint separately, preserving the segment's
  // shape even on a logarithmic price scale or across unequal candle intervals.
  for(const anchor of moving.coordinates){const next=this.anchorAt({x:anchor.x+dx,y:anchor.y+dy});if(!next)return null;points.push(next);}
  if(moving.drawing.type==='trendline'&&(points.length!==2||points[0].time===points[1].time&&points[0].price===points[1].price))return null;
  return points;
 }
 private moveAt(event:PointerEvent):DrawingPoint[]|null{
  const point=this.panePoint(event);if(!this.moving||!point)return null;const points=this.translatedPoints(point);if(!points)return null;
  this.moving.pointer=point;this.moving.points=points;this.preview();return points;
 }
 private pointerDown=(event:PointerEvent)=>{
  if((!this.tool&&!this.moving)||this.disposed||event.button!==0||this.activePointer!==null)return;
  event.preventDefault();this.activePointer=event.pointerId;
  // Synthetic events and a pointer cancelled by the host webview cannot acquire capture.
  try{this.overlay.setPointerCapture?.(event.pointerId);}catch{/* Pointerup inside the overlay still works. */}
  if(this.moving){this.moveAt(event);return;}this.hover=this.anchor(event);this.preview();
 };
 private pointerMove=(event:PointerEvent)=>{if((!this.tool&&!this.moving)||this.disposed||(this.activePointer!==null&&this.activePointer!==event.pointerId))return;if(this.moving){this.moveAt(event);return;}this.hover=this.anchor(event);this.preview();};
 private pointerUp=(event:PointerEvent)=>{
  if((!this.tool&&!this.moving)||this.disposed||this.activePointer!==event.pointerId)return;event.preventDefault();this.releasePointer();
  if(this.moving){const id=this.moving.drawing.id,points=this.moveAt(event);if(!points)return;this.setTool(null);this.onMove?.(id,points);return;}
  const point=this.anchor(event);if(!point)return;
  const type=this.tool!;this.hover=point;
  if(type==='trendline'&&!this.draft.length){this.draft=[point];this.preview();return;}
  if(type==='trendline'&&this.draft[0].time===point.time&&this.draft[0].price===point.price)return;
  const points=type==='trendline'?[this.draft[0],point]:[point];this.setTool(null);this.onCommit(type,points);this.onToolChange?.();
 };
 private releasePointer(){const id=this.activePointer;this.activePointer=null;try{if(id!==null&&this.overlay.hasPointerCapture?.(id))this.overlay.releasePointerCapture(id);}catch{/* The webview may already have released or cancelled this pointer. */}}
 private pointerCancel=(event:PointerEvent)=>{if(this.activePointer!==event.pointerId)return;if(this.moving){this.setTool(null);return;}this.releasePointer();this.hover=null;this.preview();};
 private pointerLeave=()=>{if(this.activePointer!==null||this.moving)return;this.hover=null;this.preview();};
 private preventWheel=(event:WheelEvent)=>{if(this.tool||this.moving){event.preventDefault();event.stopPropagation();}};
 private keyDown=(event:KeyboardEvent)=>{if(event.key==='Escape'&&(this.tool||this.moving)){event.preventDefault();const hadTool=!!this.tool;this.setTool(null);if(hadTool)this.onToolChange?.();}};
 update(drawings:readonly DrawingConfig[],bars:readonly Candle[],timeframeSeconds:number){
  if(this.disposed)return;this.drawings=drawings;this.bars=bars;this.step=timeframeSeconds;
  if(this.moving){const original=this.moving.drawing,current=drawings.find(drawing=>drawing.id===original.id);if(!bars.length||!current||current.type!==original.type||current.exchange!==original.exchange||current.market!==original.market||current.instrument!==original.instrument||current.points.length!==original.points.length||current.points.some((point,index)=>point.time!==original.points[index].time||point.price!==original.points[index].price))this.setTool(null);}
  this.renderData();
 }
 setSeries(series:DrawingPriceSeries){if(this.disposed||series===this.series)return;if(this.moving)this.setTool(null);this.series=series;this.primitive.setSeries(series);}
 private acquireInteraction(){if(this.previousInteraction)return;const options=this.chart.options();this.previousInteraction=structuredClone({handleScroll:options.handleScroll,handleScale:options.handleScale});this.chart.applyOptions({handleScroll:false,handleScale:false});}
 setTool(tool:DrawingType|null){
  if(this.disposed||tool===this.tool&&!this.moving)return;
  this.releasePointer();this.draft=[];this.hover=null;const wasMoving=!!this.moving;this.moving=null;
  if(tool)this.acquireInteraction();
  if(!tool&&this.previousInteraction){this.chart.applyOptions(this.previousInteraction);this.previousInteraction=null;}
  this.tool=tool;this.overlay.style.display=tool?'block':'none';this.overlay.style.cursor='crosshair';this.syncOverlay();this.renderData();this.preview();if(wasMoving)this.onMoveChange?.(false);
 }
 read(){const size=this.chart.paneSize(this.pane.paneIndex());return {tool:this.tool,movingId:this.moving?.drawing.id??null,draft:this.draft.map(point=>({...point})),drawings:this.primitive.read(),preview:this.primitive.readPreview(),width:size.width,height:size.height};}
 dispose(){
  if(this.disposed)return;this.setTool(null);this.disposed=true;this.resize?.disconnect();
  this.overlay.removeEventListener('pointerdown',this.pointerDown);this.overlay.removeEventListener('pointermove',this.pointerMove);this.overlay.removeEventListener('pointerup',this.pointerUp);this.overlay.removeEventListener('pointercancel',this.pointerCancel);this.overlay.removeEventListener('pointerleave',this.pointerLeave);this.overlay.removeEventListener('wheel',this.preventWheel);this.element.removeEventListener('dblclick',this.doubleClick,true);this.ownerDocument.removeEventListener('keydown',this.keyDown);
  this.pane.detachPrimitive(this.primitive);this.overlay.remove();if(this.previousPosition!==null)this.element.style.position=this.previousPosition;this.bars=[];this.drawings=[];this.draft=[];this.hover=null;
 }
}

import type {IChartApi,IPanePrimitive,IPanePrimitivePaneView,IPrimitivePaneRenderer,ISeriesApi,PaneAttachedParameter} from 'lightweight-charts';
import type {Candle,DrawingConfig,DrawingPoint,DrawingType} from '../../types';
import {clipSegment,drawingLogicalToCoordinate,drawingTimeToLogical,type ScreenPoint} from './geometry';
import {arrowOutline} from './hitTest';

export type DrawingPriceSeries=ISeriesApi<'Candlestick'>|ISeriesApi<'Line'>;
export interface ProjectedDrawing {id:string;type:DrawingType;points:DrawingPoint[];coordinates:ScreenPoint[]}
const colors={arrowUp:'#26a69a',arrowDown:'#ef5350',horizontal:'#e9af55',trendline:'#48a9f8'};

/** Main-pane canvas rendering in media coordinates remains sharp at every device pixel ratio. */
export function drawDrawings(context:CanvasRenderingContext2D,drawings:readonly ProjectedDrawing[],width:number,height:number,preview=false){
 context.save();context.beginPath();context.rect(0,0,width,height);context.clip();context.lineWidth=2;context.lineCap='round';context.lineJoin='round';context.globalAlpha=preview?.7:1;context.setLineDash(preview?[5,4]:[]);
 for(const drawing of drawings){
  const point=drawing.coordinates[0];if(!point)continue;context.strokeStyle=colors[drawing.type];context.fillStyle=colors[drawing.type];
  if(drawing.type==='horizontal'){
   if(point.y<0||point.y>height)continue;
   context.beginPath();context.moveTo(0,point.y);context.lineTo(width,point.y);context.stroke();
  }else if(drawing.type==='trendline'){
   const other=drawing.coordinates[1];if(!other)continue;const clipped=clipSegment(point,other,width,height);if(!clipped)continue;
   context.beginPath();context.moveTo(clipped[0].x,clipped[0].y);context.lineTo(clipped[1].x,clipped[1].y);context.stroke();
  }else{
   if(point.x<-10||point.x>width+10||point.y<-30||point.y>height+30)continue;
   // The chosen price is the arrow tip; the shaft extends away from that exact anchor.
   const outline=arrowOutline(point,drawing.type);context.beginPath();context.moveTo(outline[0].x,outline[0].y);for(const vertex of outline.slice(1))context.lineTo(vertex.x,vertex.y);context.closePath();context.fill();
  }
  if(preview)for(const anchor of drawing.coordinates){context.beginPath();context.arc(anchor.x,anchor.y,3,0,Math.PI*2);context.fill();}
 }
 context.restore();
}

/** A pane primitive remains visible when switching between candle and line price series. */
export class DrawingPrimitive implements IPanePrimitive {
 private chart:IChartApi|null=null;
 private requestUpdate:(()=>void)|null=null;
 private drawings:readonly DrawingConfig[]=[];
 private preview:{type:DrawingType;points:DrawingPoint[]}|null=null;
 private step=60;
 private hasBars=false;
 private projected:ProjectedDrawing[]=[];
 private projectedPreview:ProjectedDrawing[]=[];
 private readonly renderer:IPrimitivePaneRenderer={draw:target=>target.useMediaCoordinateSpace(({context,mediaSize})=>{drawDrawings(context,this.projected,mediaSize.width,mediaSize.height);drawDrawings(context,this.projectedPreview,mediaSize.width,mediaSize.height,true);})};
 private readonly views:readonly IPanePrimitivePaneView[]=[{zOrder:()=> 'top',renderer:()=>this.renderer}];
 constructor(private series:DrawingPriceSeries,private readonly onViewUpdate?:()=>void){}
 attached({chart,requestUpdate}:PaneAttachedParameter){this.chart=chart as IChartApi;this.requestUpdate=requestUpdate;}
 detached(){this.chart=null;this.requestUpdate=null;this.projected=[];this.projectedPreview=[];this.drawings=[];this.preview=null;}
 paneViews(){return this.views;}
 setSeries(series:DrawingPriceSeries){this.series=series;this.invalidate();}
 setData(drawings:readonly DrawingConfig[],bars:readonly Candle[],step:number){this.drawings=drawings;this.hasBars=bars.length>0;this.step=step;this.invalidate();}
 setPreview(preview:{type:DrawingType;points:DrawingPoint[]}|null){this.preview=preview;this.invalidate();}
 private invalidate(){this.updateAllViews();this.requestUpdate?.();}
 private project(id:string,type:DrawingType,points:DrawingPoint[]):ProjectedDrawing|null{
  const scale=this.chart?.timeScale();if(!scale||!this.hasBars)return null;
  const coordinates:ScreenPoint[]=[];
  for(const point of points){
   const logical=drawingTimeToLogical(scale,point.time,this.step),x=logical===null?null:drawingLogicalToCoordinate(scale,logical),y=this.series.priceToCoordinate(point.price);
   if(x===null||y===null||!Number.isFinite(x)||!Number.isFinite(y))return null;
   coordinates.push({x,y});
  }
  return {id,type,points:points.map(point=>({...point})),coordinates};
 }
 updateAllViews(){
  this.projected=this.drawings.flatMap(drawing=>{const projected=this.project(drawing.id,drawing.type,drawing.points);return projected?[projected]:[];});
  const preview=this.preview&&this.project('preview',this.preview.type,this.preview.points);this.projectedPreview=preview?[preview]:[];
  this.onViewUpdate?.();
 }
 read(){this.updateAllViews();return this.projected.map(drawing=>({...drawing,points:drawing.points.map(point=>({...point})),coordinates:drawing.coordinates.map(point=>({...point}))}));}
 readPreview(){this.updateAllViews();return this.projectedPreview.map(drawing=>({...drawing,points:drawing.points.map(point=>({...point})),coordinates:drawing.coordinates.map(point=>({...point}))}));}
}

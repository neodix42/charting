import type {IChartApi,IPanePrimitive,IPanePrimitivePaneView,IPrimitivePaneRenderer,ISeriesApi,PaneAttachedParameter,UTCTimestamp} from 'lightweight-charts';

export interface PredictionBandPoint {time:UTCTimestamp;lower:number;upper:number}
/** Public pane primitive; transparent boundary series provide the autoscale range. */
export class PredictionBand implements IPanePrimitive {
 private chart:IChartApi|null=null;private requestUpdate:(()=>void)|null=null;
 private renderer:IPrimitivePaneRenderer={draw:target=>target.useMediaCoordinateSpace(({context})=>{
  if(!this.chart)return;
  const coordinates=this.points.map(point=>({x:this.chart!.timeScale().timeToCoordinate(point.time),low:this.series.priceToCoordinate(point.lower),high:this.series.priceToCoordinate(point.upper)}));
  if(coordinates.length<2||coordinates.some(point=>point.x===null||point.low===null||point.high===null))return;
  context.save();context.fillStyle=this.color;context.beginPath();coordinates.forEach((point,index)=>{if(index===0)context.moveTo(point.x!,point.high!);else context.lineTo(point.x!,point.high!);});
  for(let index=coordinates.length-1;index>=0;index--)context.lineTo(coordinates[index].x!,coordinates[index].low!);
  context.closePath();context.fill();context.restore();
 })};
 private views:readonly IPanePrimitivePaneView[]=[{zOrder:()=> 'bottom',renderer:()=>this.renderer}];
 constructor(private series:ISeriesApi<'Line'>,private points:PredictionBandPoint[],private color:string){}
 attached({chart,requestUpdate}:PaneAttachedParameter){this.chart=chart as IChartApi;this.requestUpdate=requestUpdate;}
 detached(){this.chart=null;this.requestUpdate=null;}
 paneViews(){return this.views;}
 setColor(color:string){this.color=color;this.requestUpdate?.();}
}

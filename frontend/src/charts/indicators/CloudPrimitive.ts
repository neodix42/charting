import type {IChartApi, IPrimitivePaneRenderer, IPrimitivePaneView, ISeriesApi, ISeriesPrimitive, SeriesAttachedParameter, UTCTimestamp} from 'lightweight-charts';
import type {CloudPoint} from './math';

export interface CloudCoordinate {x:number;a:number;b:number}
const upFill='rgba(38, 166, 154, 0.20)',downFill='rgba(239, 83, 80, 0.20)';

/** Fill only the area between the spans; split at intersections so both cloud colors remain accurate. */
export function drawCloud(context:CanvasRenderingContext2D,points:readonly (CloudCoordinate|null)[],width:number,height:number){
 context.save();context.beginPath();context.rect(0,0,width,height);context.clip();
 const fill=(left:CloudCoordinate,right:CloudCoordinate,bullish:boolean)=>{
  context.fillStyle=bullish?upFill:downFill;context.beginPath();context.moveTo(left.x,left.a);context.lineTo(right.x,right.a);context.lineTo(right.x,right.b);context.lineTo(left.x,left.b);context.closePath();context.fill();
 };
 for(let i=1;i<points.length;i++){
  const left=points[i-1],right=points[i];if(!left||!right||right.x<0||left.x>width)continue;
  const d0=left.b-left.a,d1=right.b-right.a;
  if(d0*d1<0){const ratio=d0/(d0-d1),cross:CloudCoordinate={x:left.x+(right.x-left.x)*ratio,a:left.a+(right.a-left.a)*ratio,b:left.b+(right.b-left.b)*ratio};fill(left,cross,d0>=0);fill(cross,right,d1>=0);}
  else fill(left,right,(d0||d1)>=0);
 }
 context.restore();
}

/** Public v5 series primitive. Media-coordinate rendering accounts for the canvas device pixel ratio. */
export class CloudPrimitive implements ISeriesPrimitive {
 private chart:IChartApi|null=null;
 private series:ISeriesApi<'Line'>|null=null;
 private requestUpdate:(()=>void)|null=null;
 private points:readonly CloudPoint[]=[];
 private coordinates:(CloudCoordinate|null)[]=[];
 private readonly renderer:IPrimitivePaneRenderer={draw:target=>target.useMediaCoordinateSpace(({context,mediaSize})=>drawCloud(context,this.coordinates,mediaSize.width,mediaSize.height))};
 private readonly views:readonly IPrimitivePaneView[]=[{zOrder:()=> 'bottom',renderer:()=>this.renderer}];
 attached({chart,series,requestUpdate}:SeriesAttachedParameter){this.chart=chart as IChartApi;this.series=series as ISeriesApi<'Line'>;this.requestUpdate=requestUpdate;}
 detached(){this.chart=null;this.series=null;this.requestUpdate=null;this.coordinates=[];this.points=[];}
 paneViews(){return this.views;}
 setData(points:readonly CloudPoint[]){this.points=points;this.updateAllViews();this.requestUpdate?.();}
 updateAllViews(){
  const chart=this.chart,series=this.series;if(!chart||!series){this.coordinates=[];return;}
  const scale=chart.timeScale();this.coordinates=this.points.map(point=>{const x=scale.timeToCoordinate(point.time as UTCTimestamp),a=series.priceToCoordinate(point.a),b=series.priceToCoordinate(point.b);return x===null||a===null||b===null?null:{x,a,b};});
 }
}

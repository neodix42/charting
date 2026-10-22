import type {ITimeScaleApi, Logical, Time, UTCTimestamp} from 'lightweight-charts';

export interface TimedPoint {time:number}
export interface ScreenPoint {x:number;y:number}
export type DrawingTimeScale=Pick<ITimeScaleApi<Time>,'coordinateToLogical'|'logicalToCoordinate'|'coordinateToTime'|'timeToIndex'>;

function timeAt(scale:DrawingTimeScale,index:number):number|null{
 const x=scale.logicalToCoordinate(index as Logical);if(x===null)return null;
 const time=scale.coordinateToTime(x);return typeof time==='number'&&Number.isFinite(time)?time:null;
}

/** Use the chart's complete shared time scale, including dates added by overlay indicators. */
export function drawingTimeToLogical(scale:DrawingTimeScale,time:number,step:number):number|null{
 if(!Number.isFinite(time)||!Number.isFinite(step)||step<=0)return null;
 const exact=scale.timeToIndex(time as UTCTimestamp);if(exact!==null)return exact;
 const nearest=scale.timeToIndex(time as UTCTimestamp,true);if(nearest===null)return null;
 const anchor=timeAt(scale,nearest);if(anchor===null)return null;
 const direction=time<anchor?-1:1,next=timeAt(scale,nearest+direction);
 if(next!==null&&next!==anchor)return nearest+(time-anchor)/(next-anchor)*direction;
 return nearest+(time-anchor)/step;
}

/** Interpolate within candles; extrapolate a real UTC anchor into either blank chart margin. */
export function drawingLogicalToTime(scale:DrawingTimeScale,logical:number,bars:readonly TimedPoint[],step:number):number|null{
 if(!Number.isFinite(logical)||!Number.isFinite(step)||step<=0||!bars.length)return null;
 const lower=Math.floor(logical),upper=Math.ceil(logical),a=timeAt(scale,lower),b=timeAt(scale,upper);
 if(a!==null&&b!==null)return a+(b-a)*(logical-lower);
 if(a!==null)return a+(logical-lower)*step;
 if(b!==null)return b+(logical-upper)*step;
 // Both neighboring indices can lie beyond the data. timeToIndex includes prepended history.
 const point=logical<0?bars[0]:bars[bars.length-1],index=scale.timeToIndex(point.time as UTCTimestamp);
 return index===null?null:point.time+(logical-index)*step;
}

/** Clip arbitrary trend segments, including those with both endpoints outside the price pane. */
export function clipSegment(a:ScreenPoint,b:ScreenPoint,width:number,height:number):[ScreenPoint,ScreenPoint]|null{
 if(![a.x,a.y,b.x,b.y,width,height].every(Number.isFinite)||width<=0||height<=0)return null;
 const dx=b.x-a.x,dy=b.y-a.y;let start=0,end=1;
 for(const [p,q] of [[-dx,a.x],[dx,width-a.x],[-dy,a.y],[dy,height-a.y]]){
  if(p===0){if(q<0)return null;continue;}
  const ratio=q/p;if(p<0)start=Math.max(start,ratio);else end=Math.min(end,ratio);
  if(start>end)return null;
 }
 return [{x:a.x+start*dx,y:a.y+start*dy},{x:a.x+end*dx,y:a.y+end*dy}];
}

import type {ProjectedDrawing} from './DrawingPrimitive';
import {clipSegment,type ScreenPoint} from './geometry';

/** Keep selection geometry identical to the painted arrow, including its shaft. */
export function arrowOutline(point:ScreenPoint,type:'arrowUp'|'arrowDown'):ScreenPoint[]{
 const direction=type==='arrowUp'?1:-1;
 return [[0,0],[-7,10],[-3,10],[-3,24],[3,24],[3,10],[7,10]].map(([x,y])=>({x:point.x+x,y:point.y+y*direction}));
}
function segmentDistance(point:ScreenPoint,a:ScreenPoint,b:ScreenPoint){
 const dx=b.x-a.x,dy=b.y-a.y,length=dx*dx+dy*dy;
 const ratio=length?Math.max(0,Math.min(1,((point.x-a.x)*dx+(point.y-a.y)*dy)/length)):0;
 return Math.hypot(point.x-a.x-ratio*dx,point.y-a.y-ratio*dy);
}
function insidePolygon(point:ScreenPoint,polygon:ScreenPoint[]){
 let inside=false;
 for(let i=0,j=polygon.length-1;i<polygon.length;j=i++){
  const a=polygon[i],b=polygon[j];
  if((a.y>point.y)!==(b.y>point.y)&&point.x<(b.x-a.x)*(point.y-a.y)/(b.y-a.y)+a.x)inside=!inside;
 }
 return inside;
}
/** Select the last painted visible item, with a small tolerance in CSS pixels. */
export function hitTestDrawing(drawings:readonly ProjectedDrawing[],point:ScreenPoint,width:number,height:number):string|null{
 if(![point.x,point.y,width,height].every(Number.isFinite)||width<=0||height<=0||point.x<0||point.y<0||point.x>width||point.y>height)return null;
 const tolerance=6;
 for(let i=drawings.length-1;i>=0;i--){
  const drawing=drawings[i],anchor=drawing.coordinates[0];
  if(!anchor||drawing.coordinates.some(value=>!Number.isFinite(value.x)||!Number.isFinite(value.y)))continue;
  if(drawing.type==='horizontal'){
   if(anchor.y>=0&&anchor.y<=height&&Math.abs(point.y-anchor.y)<=tolerance)return drawing.id;
  }else if(drawing.type==='trendline'){
   const other=drawing.coordinates[1],segment=other&&clipSegment(anchor,other,width,height);
   if(segment&&segmentDistance(point,...segment)<=tolerance)return drawing.id;
  }else{
   const polygon=arrowOutline(anchor,drawing.type);
   if(polygon.every(value=>value.x<0)||polygon.every(value=>value.x>width)||polygon.every(value=>value.y<0)||polygon.every(value=>value.y>height))continue;
   if(insidePolygon(point,polygon)||polygon.some((value,index)=>segmentDistance(point,value,polygon[(index+1)%polygon.length])<=tolerance))return drawing.id;
  }
 }
 return null;
}

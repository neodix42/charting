import {describe,expect,it} from 'vitest';
import type {Coordinate,Logical,TimePointIndex,UTCTimestamp} from 'lightweight-charts';
import {clipSegment,drawingCoordinateToLogical,drawingLogicalToCoordinate,drawingLogicalToTime,drawingTimeToLogical,type DrawingTimeScale} from './geometry';

function timeScale(times:number[],zoom=10,offset=0):DrawingTimeScale{
 return {
  logicalToCoordinate:index=>(index*zoom+offset) as Coordinate,
  coordinateToLogical:x=>((x-offset)/zoom) as Logical,
  coordinateToTime:x=>{const time=times[Math.round((x-offset)/zoom)];return time===undefined?null:time as UTCTimestamp;},
  timeToIndex:(time,nearest)=>{const index=times.indexOf(Number(time));if(index!==-1)return index as TimePointIndex;if(!nearest||!times.length)return null;const right=times.findIndex(value=>value>Number(time));return (right===-1?times.length-1:right) as TimePointIndex;},
 };
}

describe('drawing UTC anchors',()=>{
 it('retains pointer positions between candles even when the public input API snaps to bar indices',()=>{
  const scale=timeScale([1000,1060,1120],40,11);
  scale.coordinateToLogical=x=>Math.ceil((x-11)/40) as Logical;
  scale.logicalToCoordinate=index=>(Number.isInteger(index)?index*40+11:0) as Coordinate;
  for(const x of [-25.5,11,23.25,71.75,200.5]){
   const logical=drawingCoordinateToLogical(scale,x)!;
   expect(drawingLogicalToCoordinate(scale,logical)).toBeCloseTo(x,10);
  }
  expect(drawingCoordinateToLogical({...scale,coordinateToLogical:()=>null},25)).toBeNull();
 });
 it('interpolates actual candle gaps and preserves anchors through zoom and prepended history',()=>{
  const bars=[{time:1000},{time:1060},{time:1180}],scale=timeScale(bars.map(bar=>bar.time));
  expect(drawingLogicalToTime(scale,1.5,bars,60)).toBe(1120);expect(drawingTimeToLogical(scale,1120,60)).toBe(1.5);
  const older=[{time:880},{time:940},...bars],zoomed=timeScale(older.map(bar=>bar.time),30,-10);
  expect(drawingTimeToLogical(zoomed,1120,60)).toBe(3.5);expect(drawingLogicalToTime(zoomed,3.5,older,60)).toBe(1120);
 });
 it('round trips future and earlier whitespace without snapping positions to a candle',()=>{
  const bars=[{time:1000},{time:1060},{time:1120}],scale=timeScale(bars.map(bar=>bar.time));
  for(const logical of [-4.75,-.5,.125,2.75,8.25]){
   const time=drawingLogicalToTime(scale,logical,bars,60)!;expect(time).toBe(1000+logical*60);expect(drawingTimeToLogical(scale,time,60)).toBe(logical);
  }
 });
 it('uses the shared time scale when an indicator contributes extra timestamps',()=>{
  const bars=[{time:1000},{time:1120}],scale=timeScale([1000,1060,1120,1180,1240]);
  expect(drawingLogicalToTime(scale,1.25,bars,60)).toBe(1075);expect(drawingTimeToLogical(scale,1075,60)).toBe(1.25);
  expect(drawingLogicalToTime(scale,5.25,bars,60)).toBe(1315);expect(drawingTimeToLogical(scale,1315,60)).toBe(5.25);
  const timeframeChanged=timeScale([960,1260]);expect(drawingTimeToLogical(timeframeChanged,1075,300)).toBeCloseTo(115/300);
 });
 it('does not manufacture points without candle data or with invalid geometry',()=>{
  const scale=timeScale([]);expect(drawingLogicalToTime(scale,1,[],60)).toBeNull();expect(drawingTimeToLogical(scale,1000,60)).toBeNull();
  expect(drawingLogicalToTime(scale,Infinity,[{time:1000}],60)).toBeNull();expect(drawingTimeToLogical(scale,1000,0)).toBeNull();
 });
});

describe('trendline pane clipping',()=>{
 it('clips a crossing line with both anchors outside the pane',()=>{expect(clipSegment({x:-20,y:10},{x:120,y:80},100,70)).toEqual([{x:0,y:20},{x:100,y:70}]);});
 it('rejects noncrossing and invalid lines while retaining vertical/horizontal segments',()=>{
  expect(clipSegment({x:-20,y:10},{x:-10,y:40},100,70)).toBeNull();expect(clipSegment({x:10,y:-10},{x:10,y:80},100,70)).toEqual([{x:10,y:0},{x:10,y:70}]);
  expect(clipSegment({x:NaN,y:0},{x:100,y:40},100,70)).toBeNull();expect(clipSegment({x:0,y:10},{x:100,y:10},100,70)).toEqual([{x:0,y:10},{x:100,y:10}]);
 });
});

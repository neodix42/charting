import {describe,expect,it} from 'vitest';
import type {ITimeScaleApi,Time,UTCTimestamp} from 'lightweight-charts';
import {applyRightGap,effectiveRightGapBars,latestCandleVisible,readRightGap} from './rightGap';

/** Public time-scale geometry, including timestamps contributed by other series. */
function scale({latest=99,future=0,offset=0,spacing=6,width=900}={}){
 let rightOffset=offset,barSpacing=spacing;
 const api={
  options:()=>({barSpacing,rightOffset}),width:()=>width,
  timeToIndex:()=>latest,
  timeToCoordinate:()=>width-1-(future+rightOffset+.5)*barSpacing,
  getVisibleLogicalRange:()=>({from:latest+future+rightOffset-width/barSpacing+1,to:latest+future+rightOffset}),
  scrollPosition:()=>rightOffset,
  scrollToPosition:(next:number)=>{rightOffset=next;},
  applyOptions:(next:{rightOffset?:number;barSpacing?:number})=>{if(next.rightOffset!==undefined)rightOffset=next.rightOffset;if(next.barSpacing!==undefined)barSpacing=next.barSpacing;},
 };
 return api as unknown as ITimeScaleApi<Time>;
}
const latestTime=1_700_000_000 as UTCTimestamp;
describe('workspace right gap geometry',()=>{
 it('places ten bar slots after the actual latest candle',()=>{
  const ts=scale();applyRightGap(ts,latestTime,{unit:'bars',value:10});
  expect(readRightGap(ts,latestTime)).toMatchObject({gapBars:10,gapPixels:60,barSpacing:6});
 });
 it('compensates future Ichimoku timestamps instead of anchoring after the cloud',()=>{
  const ts=scale({future:26});applyRightGap(ts,latestTime,{unit:'bars',value:10});
  expect(ts.scrollPosition()).toBe(-16);
  expect(readRightGap(ts,latestTime)?.gapBars).toBe(10);
  const changed=scale({future:40,offset:-16});applyRightGap(changed,latestTime,{unit:'bars',value:10});
  expect(changed.scrollPosition()).toBe(-30);
  expect(readRightGap(changed,latestTime)?.gapPixels).toBe(60);
 });
 it('keeps a pixel gap fixed when zoom changes bar spacing, including future cloud points',()=>{
  const ts=scale({future:26});applyRightGap(ts,latestTime,{unit:'pixels',value:79});
  expect(readRightGap(ts,latestTime)?.gapPixels).toBeCloseTo(79);
  ts.applyOptions({barSpacing:11});applyRightGap(ts,latestTime,{unit:'pixels',value:79});
  expect(readRightGap(ts,latestTime)?.gapPixels).toBeCloseTo(79);
  expect(readRightGap(ts,latestTime)?.gapBars).toBeCloseTo(79/11);
 });
 it('keeps the bars gap in bar units when zooming after a pixel setting',()=>{
  const ts=scale();applyRightGap(ts,latestTime,{unit:'pixels',value:79});
  applyRightGap(ts,latestTime,{unit:'bars',value:10});ts.applyOptions({barSpacing:11});
  applyRightGap(ts,latestTime,{unit:'bars',value:10});
  expect(readRightGap(ts,latestTime)?.gapBars).toBeCloseTo(10);
  expect(readRightGap(ts,latestTime)?.gapPixels).toBeCloseTo(110);
 });
 it('supports zero gap and caps oversized settings to leave candle slots visible',()=>{
  expect(effectiveRightGapBars({unit:'bars',value:500},6,900)).toBe(148);
  expect(effectiveRightGapBars({unit:'pixels',value:5000},10,100)).toBe(8);
  expect(effectiveRightGapBars({unit:'pixels',value:5000},10,5)).toBe(0);
  const ts=scale({future:26,offset:10});applyRightGap(ts,latestTime,{unit:'bars',value:0});
  expect(readRightGap(ts,latestTime)?.gapPixels).toBe(0);
 });
 it('detects historical views using the actual candle index rather than loaded candle count',()=>{
  const historical=scale({latest:149,future:26,offset:-28});
  expect(latestCandleVisible(historical,latestTime)).toBe(false);
  const live=scale({latest:149,future:26,offset:-16});
  expect(latestCandleVisible(live,latestTime)).toBe(true);
 });
});

import {describe,expect,it,vi} from 'vitest';
import type {ITimeScaleApi,LogicalRange,Time,UTCTimestamp} from 'lightweight-charts';
import {captureTimeViewport,restoreTimeViewport} from './timeViewport';

const anchor=1_795_478_400 as UTCTimestamp;
function harness(range:{from:number;to:number}|null,initialIndex=40){
 let index:number|null=initialIndex;
 const restore=vi.fn();
 const scale={getVisibleLogicalRange:()=>range as LogicalRange|null,setVisibleLogicalRange:restore,timeToIndex:()=>index as ReturnType<ITimeScaleApi<Time>['timeToIndex']>} satisfies Pick<ITimeScaleApi<Time>,'getVisibleLogicalRange'|'setVisibleLogicalRange'|'timeToIndex'>;
 return {scale,restore,shift:(value:number|null)=>{index=value;}};
}

describe('manual time viewport preservation',()=>{
 it('keeps fractional zoom and whitespace through a same-bar price update',()=>{
  const range={from:-220.375,to:64.125},h=harness(range);
  const viewport=captureTimeViewport(h.scale,anchor);
  range.from=0;range.to=99; // A series update can change the library's current range.
  restoreTimeViewport(h.scale,viewport);
  expect(h.restore).toHaveBeenCalledWith({from:-220.375,to:64.125});
 });
 it.each([500,-200,26])('keeps the same candle position when its timeline index shifts by %s',shift=>{
  const h=harness({from:12.125,to:83.875}),viewport=captureTimeViewport(h.scale,anchor);
  h.shift(40+shift);restoreTimeViewport(h.scale,viewport);
  const [range]=h.restore.mock.calls[0];
  expect(range.to-range.from).toBe(71.75);
  expect((40+shift)-range.from).toBe(40-12.125);
 });
 it('keeps the zoom span when no shared candle remains instead of refitting to available data',()=>{
  const h=harness({from:-20.5,to:64.25}),viewport=captureTimeViewport(h.scale,anchor);
  h.shift(null);restoreTimeViewport(h.scale,viewport);
  expect(h.restore).toHaveBeenCalledWith({from:-20.5,to:64.25});
 });
 it('ignores unavailable or invalid ranges',()=>{
  for(const range of [null,{from:NaN,to:5},{from:0,to:Infinity},{from:5,to:5}]){
   const h=harness(range);restoreTimeViewport(h.scale,captureTimeViewport(h.scale,anchor));
   expect(h.restore).not.toHaveBeenCalled();
  }
 });
});

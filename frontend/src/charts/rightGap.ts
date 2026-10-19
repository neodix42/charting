import type {ITimeScaleApi,Time} from 'lightweight-charts';
import type {ChartRightGap} from '../types';

type GapTimeScale=Pick<ITimeScaleApi<Time>,'options'|'width'|'timeToCoordinate'|'timeToIndex'|'getVisibleLogicalRange'|'scrollPosition'|'scrollToPosition'|'applyOptions'>;
export interface RightGapGeometry {gapPixels:number;gapBars:number;barSpacing:number;timeScaleWidth:number;lastX:number;latestLogical:number;visibleTo:number}
export interface ChartGapDiagnostic extends RightGapGeometry {id:string;unit:ChartRightGap['unit'];value:number;effectiveGapBars:number;followingLive:boolean;latestTime:number}
/** Registered only by the explicit test harness; production does not expose chart APIs. */
export const chartGapDiagnostics=new Map<string,{read():ChartGapDiagnostic|null;zoom(barSpacing:number):void;pan(position:number):void}>();

/** LWC reserves half a bar after a candle's centre; measure the requested gap beyond that slot. */
export function readRightGap(scale:GapTimeScale,latestTime:Time):RightGapGeometry|null{
 const latestLogical=scale.timeToIndex(latestTime,false),lastX=scale.timeToCoordinate(latestTime),range=scale.getVisibleLogicalRange(),barSpacing=scale.options().barSpacing;
 if(latestLogical===null||lastX===null||!range||!Number.isFinite(barSpacing)||barSpacing<=0)return null;
 const timeScaleWidth=scale.width();
 return {gapPixels:timeScaleWidth-1-lastX-barSpacing/2,gapBars:range.to-latestLogical,barSpacing,timeScaleWidth,lastX,latestLogical,visibleTo:range.to};
}

/** Keep at least two candle slots available when a requested gap exceeds the chart's width. */
export function effectiveRightGapBars(gap:ChartRightGap,barSpacing:number,width:number):number{
 const requested=gap.unit==='pixels'?gap.value/barSpacing:gap.value;
 return Math.max(0,Math.min(requested,width/barSpacing-2));
}

/** Compensate all future indicator points, anchoring the gap to the actual latest candle. */
export function applyRightGap(scale:GapTimeScale,latestTime:Time,gap:ChartRightGap):void{
 const geometry=readRightGap(scale,latestTime);if(!geometry)return;
 const target=effectiveRightGapBars(gap,geometry.barSpacing,geometry.timeScaleWidth);
 const offset=scale.scrollPosition()+target-geometry.gapBars;
 scale.applyOptions({rightOffset:offset,rightBarStaysOnScroll:true,shiftVisibleRangeOnNewBar:true});
 // scrollToRealTime animates and only consults rightOffset; use the immediate public API instead.
 if(Math.abs(geometry.gapBars-target)>.00001)scale.scrollToPosition(offset,false);
}

export function latestCandleVisible(scale:GapTimeScale,latestTime:Time):boolean{
 const logical=scale.timeToIndex(latestTime,false),range=scale.getVisibleLogicalRange();
 return logical!==null&&range!==null&&range.to>=logical-1;
}

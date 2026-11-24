import type {ITimeScaleApi,LogicalRange,Time} from 'lightweight-charts';

type ViewportScale=Pick<ITimeScaleApi<Time>,'getVisibleLogicalRange'|'setVisibleLogicalRange'|'timeToIndex'>;
interface TimeViewport {range:LogicalRange;anchorTime?:Time;anchorIndex:number|null}

/** Timestamp ranges clip whitespace and round fractional bars; retain the actual zoom instead. */
export function captureTimeViewport(scale:ViewportScale,anchorTime?:Time):TimeViewport|null {
 const range=scale.getVisibleLogicalRange();
 if(!range||!Number.isFinite(range.from)||!Number.isFinite(range.to)||range.from>=range.to)return null;
 return {range:{...range},anchorTime,anchorIndex:anchorTime===undefined?null:scale.timeToIndex(anchorTime,false)};
}

/** Follow a retained candle when prepends, eviction or indicator points change its logical index. */
export function restoreTimeViewport(scale:ViewportScale,viewport:TimeViewport|null):void {
 if(!viewport)return;
 const index=viewport.anchorTime===undefined?null:scale.timeToIndex(viewport.anchorTime,false);
 const shift=index!==null&&viewport.anchorIndex!==null?index-viewport.anchorIndex:0;
 scale.setVisibleLogicalRange({from:viewport.range.from+shift,to:viewport.range.to+shift});
}

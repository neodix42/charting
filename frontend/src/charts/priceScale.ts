import type {IPriceScaleApi} from 'lightweight-charts';
import type {ChartConfig} from '../types';

type PriceScale=ChartConfig['priceScale'];
/** Registered only in explicit test mode to exercise the installed public chart API. */
export const chartPriceScaleDiagnostics=new Map<string,Pick<IPriceScaleApi,'options'|'getVisibleRange'|'setVisibleRange'>>();

/** Capture only ranges that can be restored from the validated workspace. */
export function capturePriceScale(scale:Pick<IPriceScaleApi,'options'|'getVisibleRange'>,previous:PriceScale):PriceScale{
 if(scale.options().autoScale)return {...previous,autoScale:true,manualRange:null};
 const range=scale.getVisibleRange();
 // LWC can have no range during a data/layout change, and a linear drag can cross zero.
 // Keep the last valid setting until the chart reports a persistable manual range.
 if(!range||!Number.isFinite(range.from)||!Number.isFinite(range.to)||range.from<=0||range.from>=range.to)return previous;
 return {...previous,autoScale:false,manualRange:{min:range.from,max:range.to}};
}

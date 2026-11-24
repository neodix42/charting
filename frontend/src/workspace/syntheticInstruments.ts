import type {FeedKey,Instrument,Interval,ScreenerInstrument} from '../types';
import {intervals} from '../types';

export const SYNTHETIC_CATEGORY='Synthetic Instruments';
export const syntheticPeriods=['d1','w1'] as const;
export const syntheticIntervals:Interval[]=['1d','1w'];
const family=new Set(['BTCUSD','BTCXAU','XAUSPX','ETHBTC','XAUXAG']);
const calculated=new Set(['BTCXAU','XAUSPX','ETHBTC']);

/** The two native family members remain distinct from calculated ratios. */
export const isSyntheticFamilyKey=(key:Pick<FeedKey,'exchange'|'instrument'>)=>key.exchange==='dukascopy'&&family.has(key.instrument.toUpperCase());
export const isCalculatedInstrument=(key:Pick<FeedKey,'exchange'|'instrument'>,metadata?:Instrument['providerMetadata'])=>key.exchange==='dukascopy'&&(calculated.has(key.instrument.toUpperCase())||metadata?.synthetic==='true');
export const isSyntheticFamily=(key:Pick<FeedKey,'exchange'|'instrument'>,metadata?:Instrument['providerMetadata'])=>isCalculatedInstrument(key,metadata)||key.exchange==='dukascopy'&&metadata?.syntheticCategory==='true';
export const chartInstrumentIntervals=(key:Pick<FeedKey,'exchange'|'instrument'>,metadata?:Instrument['providerMetadata']):Interval[]=>isSyntheticFamily(key,metadata)?syntheticIntervals:intervals;
export const isSyntheticSymbol=(symbol:string|undefined)=>!!symbol&&family.has(symbol.toUpperCase());
export const isCalculatedSymbol=(symbol:string|undefined)=>!!symbol&&calculated.has(symbol.toUpperCase());
export const syntheticOnlyCategories=(categories:readonly string[]|null|undefined)=>!!categories?.length&&categories.every(category=>category===SYNTHETIC_CATEGORY);
export const isSyntheticDownloadInstrument=(instrument:ScreenerInstrument)=>instrument.category===SYNTHETIC_CATEGORY||instrument.synthetic===true||isCalculatedSymbol(instrument.id);

export function downloadInstrumentPeriods(instrument:ScreenerInstrument):readonly string[]|undefined {
 return instrument.supportedTimeframes?.length?instrument.supportedTimeframes:isSyntheticDownloadInstrument(instrument)?syntheticPeriods:undefined;
}

export function supportedDownloadPeriods(available:readonly string[],selected:readonly ScreenerInstrument[]):string[] {
 return available.filter(period=>selected.every(instrument=>!downloadInstrumentPeriods(instrument)||downloadInstrumentPeriods(instrument)!.includes(period)));
}

export const calculatedLabel='Calculated · Approximate · Non-tradable';

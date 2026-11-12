import type {TradeScreen} from './tradeHistory';

export interface TradeScreenGalleryQuery {
 conditions?:string[];
 directions?:('long'|'short')[];
 outcomes?:('won'|'lost')[];
 providers?:('dukascopy'|'hyperliquid')[];
 offset?:number;
 limit?:number;
}
export interface TradeScreenGalleryItem {
 screen:TradeScreen;
 entryCondition:string;
 outcome:'won'|'lost'|'';
 profitLoss:string;
 currency:string;
 closeDate:number;
}
export interface TradeScreenGalleryPage {
 items:TradeScreenGalleryItem[];
 conditions:string[];
 total:number;
 offset:number;
 limit:number;
 errors:string[];
}

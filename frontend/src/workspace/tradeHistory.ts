import type {FeedKey} from '../types';
export interface TradeRecord {
 id:string;provider:'dukascopy'|'hyperliquid';environment:string;account:string;instrument:string;market:string;orderId:string;submittedAt:number;openDate:number;closeDate:number;size:string;remainingSize:string;direction:string;entryPrice:string;stopLoss:string;takeProfit:string;slAmount:string;tpAmount:string;currency:string;balanceAtOpen:string;balanceCurrency:string;balanceTime:number;balanceBasis?:string;marginUsed:string;marginCurrency:string;marginBasis?:string;status:string;canClose:boolean;closeReason:string;checkedAt:number;error?:string;
}
export interface TradeHistoryPage {rows:TradeRecord[];total:number;offset:number;limit:number;errors:string[]}
export interface TradeClosePreview {id:string;tradeId:string;provider:string;environment:string;account:string;instrument:string;orderId:string;quantity:string;expiresAt:number;warnings:string[]}
export interface TradeHistoryBackend {
 GetChartTrades(keys:FeedKey[]):Promise<TradeHistoryPage>;
 GetTradeHistory(offset:number,limit:number):Promise<TradeHistoryPage>;
 RefreshTradeHistory(ids:string[]):Promise<TradeRecord[]>;
 PreviewTradeClose(id:string):Promise<TradeClosePreview>;
 ConfirmTradeClose(id:string):Promise<TradeRecord>;
}
export function tradeNumber(raw:string|undefined,money=false):string {if(raw===undefined||raw==='')return '—';const n=Number(raw);if(!Number.isFinite(n))return '—';if(n!==0&&Math.abs(n)<.0001)return n.toPrecision(5);return n.toLocaleString('en-US',{minimumFractionDigits:money?2:0,maximumFractionDigits:money?2:12});}
export function tradeMoney(raw:string|undefined,currency:string){const value=tradeNumber(raw,true);return value==='—'||!currency.trim()?'—':`${value} ${currency||''}`.trim();}
export function tradeDate(value:number,timezone:string){if(!Number.isFinite(value)||value<=0)return '—';return new Intl.DateTimeFormat('en-GB',{timeZone:timezone==='system'?undefined:timezone,year:'numeric',month:'short',day:'2-digit',hour:'2-digit',minute:'2-digit',second:'2-digit'}).format(new Date(value));}

import type {FeedKey} from '../types';
export interface TradeScreen {id:string;tradeId:string;provider:string;environment:string;account:string;orderId:string;instrument:string;market:string;direction:string;tradeOpenDate:number;createdAt:number;fileName:string;mimeType:string;bytes:number;width:number;height:number;sha256:string}
export interface TradeScreenQuery {tradeId?:string;provider?:string;instrument?:string;orderId?:string;environment?:string;entryCondition?:string;from?:number;to?:number;offset?:number;limit?:number}
export interface TradeScreenPage {screens:TradeScreen[];total:number;offset:number;limit:number}
export interface TradeRecord {
 screens?:TradeScreen[];
 entryCondition?:string;
 stopLossSource?:string;takeProfitSource?:string;
 exitPrice?:string;pipSize?:string;profitLossPips?:string;profitLossPipsUnit?:string;profitLossPipsTime?:number;profitLossPipsState?:string;profitLossPipsBasis?:string;profitLossPipsError?:string;
 profitPerPriceUnit?:string;valuationTime?:number;valuationError?:string;profitLoss?:string;profitLossCurrency?:string;profitLossTime?:number;profitLossState?:string;profitLossBasis?:string;profitLossError?:string;
 id:string;provider:'dukascopy'|'hyperliquid';environment:string;account:string;instrument:string;market:string;orderId:string;submittedAt:number;openDate:number;closeDate:number;size:string;remainingSize:string;direction:string;entryPrice:string;stopLoss:string;takeProfit:string;slAmount:string;tpAmount:string;currency:string;balanceAtOpen:string;balanceCurrency:string;balanceTime:number;balanceBasis?:string;marginUsed:string;marginCurrency:string;marginBasis?:string;status:string;canClose:boolean;closeReason:string;checkedAt:number;error?:string;
}
export interface TradeHistoryPage {rows:TradeRecord[];total:number;offset:number;limit:number;errors:string[]}
export interface TradeClosePreview {id:string;tradeId:string;provider:string;environment:string;account:string;instrument:string;orderId:string;quantity:string;expiresAt:number;warnings:string[]}
export interface TradeHistoryBackend {
 PasteTradeScreen?(tradeId:string):Promise<TradeScreen>;
 AddTradeScreenFile?(tradeId:string,path:string):Promise<TradeScreen>;
 AddTradeScreen(tradeId:string,fileName:string,dataURL:string):Promise<TradeScreen>;
 GetTradeScreen(tradeId:string,screenId:string):Promise<string>;
 QueryTradeScreens(query:TradeScreenQuery):Promise<TradeScreenPage>;
 SetTradeEntryCondition(id:string,value:string):Promise<string>;
 GetChartTrades(keys:FeedKey[]):Promise<TradeHistoryPage>;
 GetTradeHistory(offset:number,limit:number):Promise<TradeHistoryPage>;
 RefreshTradeHistory(ids:string[]):Promise<TradeRecord[]>;
 PreviewTradeClose(id:string):Promise<TradeClosePreview>;
 ConfirmTradeClose(id:string):Promise<TradeRecord>;
}
export function tradeNumber(raw:string|undefined,money=false):string {if(raw===undefined||raw==='')return '—';const n=Number(raw);if(!Number.isFinite(n))return '—';if(n!==0&&Math.abs(n)<.0001)return n.toPrecision(5);return n.toLocaleString('en-US',{minimumFractionDigits:money?2:0,maximumFractionDigits:money?2:12});}
export function tradeMoney(raw:string|undefined,currency:string){const value=tradeNumber(raw,true);return value==='—'||!currency.trim()?'—':`${value} ${currency||''}`.trim();}
export function tradeDate(value:number,timezone:string){if(!Number.isFinite(value)||value<=0)return '—';return new Intl.DateTimeFormat('en-GB',{timeZone:timezone==='system'?undefined:timezone,year:'numeric',month:'short',day:'2-digit',hour:'2-digit',minute:'2-digit',second:'2-digit'}).format(new Date(value));}

export function tradeProfitLoss(row:TradeRecord,now=Date.now()){
 const closed=row.status==='closed'&&row.closeDate>0,raw=row.profitLoss;
 const state=closed?row.profitLossState==='closed':['open','closing'].includes(row.profitLossState??'');
 const fresh=closed||!!row.profitLossTime&&now-row.profitLossTime<=45000&&row.profitLossTime<=now+5000;
 const available=raw!==undefined&&raw!==''&&Number.isFinite(Number(raw))&&state&&fresh&&(closed||!row.error)&&!!row.profitLossCurrency?.trim()&&(closed||Number(row.remainingSize)>0);
 return {text:available?tradeMoney(raw,row.profitLossCurrency!):'—',label:available?(closed?'Closed':'Current'):'',tone:available?(Number(raw)>0?'profit':Number(raw)<0?'loss':''):'',title:available?row.profitLossBasis||'Exchange-reported position profit/loss':row.profitLossError||(!fresh?'Waiting for fresh exchange profit/loss':'Profit/loss is not yet confirmed')};
}

export function tradePipsProfitLoss(row:TradeRecord,now=Date.now()){
 const closed=row.status==='closed'&&row.closeDate>0,raw=row.profitLossPips,unit=row.profitLossPipsUnit;
 const state=closed?row.profitLossPipsState==='closed':['open','closing'].includes(row.profitLossPipsState??'');
 const fresh=closed||!!row.profitLossPipsTime&&now-row.profitLossPipsTime<=45000&&row.profitLossPipsTime<=now+5000;
 const available=raw!==undefined&&raw!==''&&Number.isFinite(Number(raw))&&state&&fresh&&(closed||!row.error)&&(unit==='pips'||unit==='ticks')&&(closed||Number(row.remainingSize)>0);
 return {text:available?`${tradeNumber(raw)} ${unit}`:'—',label:available?(closed?'Closed':'Current'):'',tone:available?(Number(raw)>0?'profit':Number(raw)<0?'loss':''):'',title:available?row.profitLossPipsBasis||`Price profit/loss in ${unit}${row.pipSize?` (unit size ${row.pipSize})`:''}`:row.profitLossPipsError||(!fresh?'Waiting for fresh exchange pip/tick profit/loss':'Pip/tick profit/loss is not yet confirmed')};
}

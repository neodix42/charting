import type {FeedKey} from '../types';
import type {TradeRecord} from './tradeHistory';
export const TRADES_CHANGED='cryptodesk:trades-changed';
export interface TradeLevel {id:string;tradeId:string;kind:'entry'|'sl'|'tp';price:number;title:string;orderId:string}
export function chartTradeIdentity(key:FeedKey):string{return `${key.exchange}|${key.exchange==='dukascopy'?'jforex':key.market}|${key.instrument}`;}
export function chartTradeKeys(keys:FeedKey[]):FeedKey[]{
 const unique=new Map<string,FeedKey>();
 for(const key of keys){if(key.exchange!=='dukascopy'&&!(key.exchange==='hyperliquid'&&key.market==='perpetual'))continue;unique.set(chartTradeIdentity(key),{...key,timeframe:'1h'});}
 return [...unique.values()].sort((a,b)=>JSON.stringify(a).localeCompare(JSON.stringify(b)));
}
export function matchesChartTrade(row:TradeRecord,key:FeedKey):boolean{return row.provider===key.exchange&&row.instrument===key.instrument&&(key.exchange==='dukascopy'||row.market===key.market);}
const openingTime=(row:TradeRecord)=>row.openDate>0?row.openDate:row.submittedAt;
export function compareChartTrades(a:TradeRecord,b:TradeRecord):number{return openingTime(a)-openingTime(b)||a.submittedAt-b.submittedAt||a.id.localeCompare(b.id);}
export function latestChartTrade(rows:TradeRecord[],key:FeedKey):TradeRecord|undefined {
 return rows.filter(row=>matchesChartTrade(row,key)&&['open','closing','closed','filled','cancelled'].includes(row.status)&&openingTime(row)>0&&Number.isFinite(Number(row.size))&&Number(row.size)>0&&Number.isFinite(Number(row.entryPrice))&&Number(row.entryPrice)>0).sort((a,b)=>compareChartTrades(b,a))[0];
}
/** Retain data, never hidden chart instances or inactive provider subscriptions. */
export class ChartTradeCache {
 private latest=new Map<string,TradeRecord>();
 merge(rows:TradeRecord[],keys:FeedKey[]){
  for(const key of chartTradeKeys(keys)){
   const id=chartTradeIdentity(key),incoming=latestChartTrade(rows,key),previous=this.latest.get(id);
   if(!incoming||previous&&incoming.id!==previous.id&&compareChartTrades(incoming,previous)<=0)continue;
   const next=structuredClone(incoming);
   if(previous?.id===next.id&&next.status==='closed'){
    next.stopLoss ||= previous.stopLoss;next.takeProfit ||= previous.takeProfit;
   }
   this.latest.set(id,next);
  }
 }
 rows(keys:FeedKey[]):TradeRecord[]{return chartTradeKeys(keys).flatMap(key=>{const row=this.latest.get(chartTradeIdentity(key));return row?[row]:[];});}
 retain(keys:FeedKey[]){const keep=new Set(chartTradeKeys(keys).map(chartTradeIdentity));for(const id of this.latest.keys())if(!keep.has(id))this.latest.delete(id);}
 clear(){this.latest.clear();}
}
export function chartTradeLevels(rows:TradeRecord[],key:FeedKey):TradeLevel[]{
 const row=latestChartTrade(rows,key),levels:TradeLevel[]=[];
 if(!row)return levels;
 const orderId=row.orderId||row.id,side=row.direction==='short'?'Short':'Long';
 for(const [kind,value,title] of [['entry',row.entryPrice,`${side} entry`],['sl',row.stopLoss,'SL'],['tp',row.takeProfit,'TP']] as const){
  const price=Number(value);if(!Number.isFinite(price)||price<=0)continue;
  levels.push({id:`${row.id}:${kind}`,tradeId:row.id,kind,price,title:`${title} #${orderId}`,orderId});
 }
 return levels;
}

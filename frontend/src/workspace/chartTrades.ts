import type {FeedKey} from '../types';
import type {TradeRecord} from './tradeHistory';
export const TRADES_CHANGED='cryptodesk:trades-changed';
export interface TradeLevel {id:string;tradeId:string;kind:'entry'|'sl'|'tp';price:number;title:string;orderId:string}
export function chartTradeKeys(keys:FeedKey[]):FeedKey[]{
 const unique=new Map<string,FeedKey>();
 for(const key of keys){if(key.exchange!=='dukascopy'&&!(key.exchange==='hyperliquid'&&key.market==='perpetual'))continue;unique.set(`${key.exchange}|${key.market}|${key.instrument}`,{...key,timeframe:'1h'});}
 return [...unique.values()].sort((a,b)=>JSON.stringify(a).localeCompare(JSON.stringify(b)));
}
export function matchesChartTrade(row:TradeRecord,key:FeedKey):boolean{return row.provider===key.exchange&&row.instrument===key.instrument&&(key.exchange==='dukascopy'||row.market===key.market);}
export function chartTradeLevels(rows:TradeRecord[],key:FeedKey):TradeLevel[]{
 const levels:TradeLevel[]=[];
 for(const row of rows){
  if(!matchesChartTrade(row,key)||row.error||!['open','closing'].includes(row.status)||row.openDate<=0||row.closeDate>0||!Number.isFinite(Number(row.remainingSize))||Number(row.remainingSize)<=0)continue;
  if(!Number.isFinite(Number(row.entryPrice))||Number(row.entryPrice)<=0)continue;
  const orderId=row.orderId||row.id,side=row.direction==='short'?'Short':'Long';
  for(const [kind,value,title] of [['entry',row.entryPrice,`${side} entry`],['sl',row.stopLoss,'SL'],['tp',row.takeProfit,'TP']] as const){
   const price=Number(value);if(!Number.isFinite(price)||price<=0)continue;
   levels.push({id:`${row.id}:${kind}`,tradeId:row.id,kind,price,title:`${title} #${orderId}`,orderId});
  }
 }
 return levels;
}

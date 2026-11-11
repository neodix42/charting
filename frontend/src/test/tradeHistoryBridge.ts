import {matchesChartTrade} from '../workspace/chartTrades';
import type {TradeHistoryBackend,TradeRecord,TradeClosePreview} from '../workspace/tradeHistory';
export function tradeHistoryBridge(){
 const key='cryptodesk-explicit-test-trade-history';let rows:TradeRecord[]=JSON.parse(sessionStorage.getItem(key)||'[]'),reads=0,chartReads=0,refreshes:string[][]=[],closes:string[]=[];const previews=new Map<string,TradeClosePreview>();
 const save=()=>sessionStorage.setItem(key,JSON.stringify(rows));
 const api:TradeHistoryBackend={
  async SetTradeEntryCondition(id,value){if(Array.from(value).length>100||/[\r\n]/.test(value))throw Error('Entry condition must be a single line of at most 100 characters');const row=rows.find(row=>row.id===id);if(!row)throw Error('Platform trade missing');row.entryCondition=value;save();return value;},
  async GetChartTrades(keys){chartReads++;const found=rows.filter(row=>keys.some(key=>matchesChartTrade(row,key))&&!['closed','rejected','cancelled','filled'].includes(row.status));return{rows:structuredClone(found),total:found.length,offset:0,limit:100,errors:[]};},
  async GetTradeHistory(offset,limit){reads++;return{rows:structuredClone(rows.slice(offset,offset+limit)),total:rows.length,offset,limit,errors:[]};},
  async RefreshTradeHistory(ids){refreshes.push([...ids]);return structuredClone(rows.filter(row=>ids.includes(row.id)));},
  async PreviewTradeClose(id){const row=rows.find(row=>row.id===id);if(!row||!row.canClose)throw Error('This platform position cannot be closed');const preview={id:crypto.randomUUID(),tradeId:id,provider:row.provider,environment:row.environment,account:row.account,instrument:row.instrument,orderId:row.orderId,quantity:row.remainingSize,expiresAt:Date.now()+60000,warnings:['Explicit offline close fixture; no exchange request.']};previews.set(preview.id,preview);return structuredClone(preview);},
  async ConfirmTradeClose(id){const preview=previews.get(id);previews.delete(id);if(!preview)throw Error('Confirmation already used');const row=rows.find(row=>row.id===preview.tradeId);if(!row)throw Error('Platform trade missing');closes.push(row.id);Object.assign(row,{status:'closed',closeDate:Date.now(),canClose:false,remainingSize:'0',closeReason:'Position closed'});save();return structuredClone(row);},
 };
 Object.assign(window,{__testChartTradeReads:()=>chartReads,__testTradeHistoryReads:()=>reads,__testTradeHistoryRefreshes:()=>structuredClone(refreshes),__testTradeHistoryCloses:()=>[...closes],__testSetTradeHistory:(items:TradeRecord[])=>{rows=structuredClone(items);save();},__testTradeHistory:()=>structuredClone(rows)});
 return api;
}

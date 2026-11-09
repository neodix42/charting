import type {SeriesMarkerBar,UTCTimestamp} from 'lightweight-charts';
import type {AnalysisChart} from '../workspace/screenerAnalysis';
import {analysisDate} from '../workspace/screenerAnalysis';

// A winning trade stays green in every theme, including themes with non-green up candles.
export const profitableMatchColor='#16a34a';
export function analysisTradePriceLines(data:AnalysisChart):string[] {
 const signal=data.signal;
 if(signal){
  const format=(price:number)=>price.toLocaleString('en-US',{maximumSignificantDigits:15});
  return [Number.isFinite(signal.entry)?`Entry ${format(signal.entry)}`:'',Number.isFinite(signal.stopLoss)?`SL ${format(signal.stopLoss!)}`:'',Number.isFinite(signal.takeProfit)?`TP ${format(signal.takeProfit!)}`:''].filter(Boolean);
 }
 const trade=data.trade;
 if(!trade||![trade.entry,trade.stopLoss,trade.takeProfit].every(Number.isFinite))return [];
 const format=(price:number)=>price.toLocaleString('en-US',{maximumSignificantDigits:15});
 return [`Entry ${format(trade.entry)}`,`SL ${format(trade.stopLoss)}`,`TP ${format(trade.takeProfit)}`];
}
export function analysisTradeExecutionDetails(data:AnalysisChart):Array<{label:string;value:string}> {
 const execution=data.tradeExecution;
 if(!execution||!Number.isFinite(execution.entryTime))return [];
 const outcomes={takeProfit:'Take Profit hit',stopLoss:'Stop Loss hit',expired:'Expired',insufficientData:'Insufficient future data',unavailable:'Unavailable data'};
 const details=[{label:'Signal bar start',value:analysisDate(data.matchTime)},{label:'Entry bar start',value:analysisDate(execution.entryTime)},{label:'Outcome',value:outcomes[execution.outcome]??'Unavailable data'}];
 if(['takeProfit','stopLoss','expired'].includes(execution.outcome)&&execution.exitTime!==undefined&&Number.isFinite(execution.exitTime))details.push({label:'Exit bar start',value:analysisDate(execution.exitTime)});
 return details;
}
export function analysisMatchMarkers(data:AnalysisChart,selectionColor:string):SeriesMarkerBar<UTCTimestamp>[] {
 const visible=new Set(data.bars.map(bar=>`${bar.index}:${bar.time}`));
 const times=new Set((data.profitableMatches??[]).filter(match=>Number.isFinite(match.time)&&visible.has(`${match.index}:${match.time}`)).map(match=>match.time));
 const markers:SeriesMarkerBar<UTCTimestamp>[]=[...times].map(time=>({time:time/1000 as UTCTimestamp,position:'belowBar',color:profitableMatchColor,shape:'circle',size:0.75}));
 markers.push({time:data.matchTime/1000 as UTCTimestamp,position:'belowBar',color:selectionColor,shape:'arrowUp',text:'Match'});
 for(const text of analysisTradePriceLines(data))markers.push({time:data.matchTime/1000 as UTCTimestamp,position:'belowBar',color:selectionColor,shape:'circle',size:0,text});
 return markers.sort((left,right)=>Number(left.time)-Number(right.time));
}

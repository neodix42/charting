import type {SeriesMarkerBar,UTCTimestamp} from 'lightweight-charts';
import type {AnalysisChart} from '../workspace/screenerAnalysis';
import {analysisDate} from '../workspace/screenerAnalysis';

// A winning trade stays green in every theme, including themes with non-green up candles.
export const profitableMatchColor='#16a34a';
const formatPrice=(price:number)=>price.toLocaleString('en-US',{maximumSignificantDigits:15});
export function analysisEntryLabel(data:AnalysisChart):string|undefined {
 const entry=(data.signal??data.trade)?.entry;
 if((data.direction!=='long'&&data.direction!=='short')||!Number.isFinite(entry))return undefined;
 return `${data.direction==='short'?'Short':'Long'} ${formatPrice(entry!)}`;
}
export function analysisExitLabels(data:AnalysisChart):string[] {
 const levels=data.signal??data.trade;
 return [Number.isFinite(levels?.stopLoss)?`SL ${formatPrice(levels!.stopLoss!)}`:'',Number.isFinite(levels?.takeProfit)?`TP ${formatPrice(levels!.takeProfit!)}`:''].filter(Boolean);
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
 markers.push({time:data.matchTime/1000 as UTCTimestamp,position:'belowBar',color:selectionColor,shape:'arrowUp',text:analysisEntryLabel(data)??'Match'});
 for(const text of analysisExitLabels(data))markers.push({time:data.matchTime/1000 as UTCTimestamp,position:'belowBar',color:selectionColor,shape:'circle',size:0,text});
 return markers.sort((left,right)=>Number(left.time)-Number(right.time));
}

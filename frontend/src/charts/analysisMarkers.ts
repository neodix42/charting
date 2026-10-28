import type {SeriesMarkerBar,UTCTimestamp} from 'lightweight-charts';
import type {AnalysisChart} from '../workspace/screenerAnalysis';

// A winning trade stays green in every theme, including themes with non-green up candles.
export const profitableMatchColor='#16a34a';
export function analysisTradePriceText(data:AnalysisChart):string {
 const trade=data.trade;
 if(!trade||!Number.isFinite(trade.stopLoss)||!Number.isFinite(trade.takeProfit))return '';
 const format=(price:number)=>price.toLocaleString('en-US',{maximumSignificantDigits:15});
 return `SL ${format(trade.stopLoss)} · TP ${format(trade.takeProfit)}`;
}
export function analysisMatchMarkers(data:AnalysisChart,selectionColor:string):SeriesMarkerBar<UTCTimestamp>[] {
 const visible=new Set(data.bars.map(bar=>`${bar.index}:${bar.time}`));
 const times=new Set((data.profitableMatches??[]).filter(match=>Number.isFinite(match.time)&&visible.has(`${match.index}:${match.time}`)).map(match=>match.time));
 const markers:SeriesMarkerBar<UTCTimestamp>[]=[...times].map(time=>({time:time/1000 as UTCTimestamp,position:'belowBar',color:profitableMatchColor,shape:'circle',size:0.75}));
 markers.push({time:data.matchTime/1000 as UTCTimestamp,position:'belowBar',color:selectionColor,shape:'arrowUp',text:'Match'});
 const tradeText=analysisTradePriceText(data);
 if(tradeText)markers.push({time:data.matchTime/1000 as UTCTimestamp,position:'belowBar',color:selectionColor,shape:'circle',size:0,text:tradeText});
 return markers.sort((left,right)=>Number(left.time)-Number(right.time));
}

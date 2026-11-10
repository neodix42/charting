import {intervalSeconds,type Candle,type IndicatorConfig,type Interval} from '../../types';
import {calendarRange,calendarRangeOffset,isCalendarRange,isIntradayPeriod,type CalendarRangeType} from '../../../../screener-runtime/calendar-ranges.mjs';

export interface IndicatorPoint {time:number;value:number}
export interface CloudPoint {time:number;a:number;b:number}
export interface IchimokuValues {conversion:IndicatorPoint[];base:IndicatorPoint[];leadingA:IndicatorPoint[];leadingB:IndicatorPoint[];lagging:IndicatorPoint[];cloud:CloudPoint[]}
export interface MACDValues {macd:IndicatorPoint[];signal:IndicatorPoint[];histogram:IndicatorPoint[]}
const validPeriod=(period:number)=>Number.isInteger(period)&&period>=1&&period<=2000;

/** Required history for the complete indicator, including MACD's separate signal seed. */
export function indicatorWarmupBars(config:IndicatorConfig,timeframe:Interval='1h'):number{
 if(isCalendarRange(config.type)){
  if(!isIntradayPeriod(timeframe))return 0;
  const days=config.type.includes('week')?7:1,periods=calendarRangeOffset(config.type)+1;
  return Math.ceil(days*periods*86400/intervalSeconds[timeframe])+1;
 }
 const p=config.parameters;
 switch(config.type){
  case 'sma':case 'ema':return p.period??20;
  case 'low':case 'high':return p.period??10;
  case 'ichimoku':return Math.max(p.conversion??9,p.base??26,p.spanB??52);
  case 'macd':return (p.slow??26)+(p.signal??9)-1;
 }
}

/** Keep ordinary windows at 10,000 bars; one-minute previous-week ranges need two weeks plus a boundary bar. */
export function indicatorHistoryLimit(configs:readonly IndicatorConfig[],timeframe:Interval){
 return Math.max(10000,...configs.filter(config=>isCalendarRange(config.type)).map(config=>indicatorWarmupBars(config,timeframe)));
}

export function calendarRangeValues(bars:readonly Candle[],type:CalendarRangeType,timeframe:Interval):IndicatorPoint[]{
 if(!isIntradayPeriod(timeframe))return [];
 const next=calendarRange(type),field=type.endsWith('_low')?'low':'high',result:IndicatorPoint[]=[];
 for(const bar of bars){const value=next(bar.time*1000,Number(bar[field]));if(value!==undefined)result.push({time:bar.time,value});}
 return result;
}

/** Averages use the close, include the open candle, and emit nothing before a complete seed window. */
export function sma(bars:readonly Candle[],period:number):IndicatorPoint[]{
 if(!validPeriod(period))return [];
 let sum=0;const result:IndicatorPoint[]=[];
 for(let i=0;i<bars.length;i++){sum+=Number(bars[i].close);if(i>=period)sum-=Number(bars[i-period].close);if(i>=period-1)result.push({time:bars[i].time,value:sum/period});}
 return result;
}

/** Extrema include this bar's wick and wait for a full window of observed bars (gaps add no bars). */
export function rollingExtrema(bars:readonly Candle[],period:number,field:'low'|'high'):IndicatorPoint[]{
 if(!validPeriod(period))return [];
 const queue:number[]=[],result:IndicatorPoint[]=[];let head=0;
 for(let i=0;i<bars.length;i++){
  const value=Number(bars[i][field]);
  while(queue.length>head&&(field==='low'?Number(bars[queue.at(-1)!][field])>=value:Number(bars[queue.at(-1)!][field])<=value))queue.pop();
  queue.push(i);
  while(queue[head]<=i-period)head++;
  if(i>=period-1)result.push({time:bars[i].time,value:Number(bars[queue[head]][field])});
 }
 return result;
}

/** The first EMA is the arithmetic mean of its first period closes, never a future-dependent seed. */
function emaValues(values:readonly number[],period:number):(number|null)[]{
 const result:(number|null)[]=Array(values.length).fill(null);if(!validPeriod(period))return result;
 let seed=0,previous=0;const alpha=2/(period+1);
 for(let i=0;i<values.length;i++){
  if(i<period){seed+=values[i];if(i===period-1){previous=seed/period;result[i]=previous;}}
  else{previous=alpha*values[i]+(1-alpha)*previous;result[i]=previous;}
 }
 return result;
}
export function ema(bars:readonly Candle[],period:number):IndicatorPoint[]{
 const values=emaValues(bars.map(bar=>Number(bar.close)),period);
 return values.flatMap((value,i)=>value===null?[]:[{time:bars[i].time,value}]);
}

/** Rolling high/low deques keep a 2,000-period cloud linear in the available history. */
function midpoints(bars:readonly Candle[],period:number):(number|null)[]{
 const result:(number|null)[]=Array(bars.length).fill(null);if(!validPeriod(period))return result;
 const highs:number[]=[],lows:number[]=[];let highHead=0,lowHead=0;
 for(let i=0;i<bars.length;i++){
  while(highs.length>highHead&&Number(bars[highs.at(-1)!].high)<=Number(bars[i].high))highs.pop();
  while(lows.length>lowHead&&Number(bars[lows.at(-1)!].low)>=Number(bars[i].low))lows.pop();
  highs.push(i);lows.push(i);
  while(highs[highHead]<=i-period)highHead++;
  while(lows[lowHead]<=i-period)lowHead++;
  if(i>=period-1)result[i]=(Number(bars[highs[highHead]].high)+Number(bars[lows[lowHead]].low))/2;
 }
 return result;
}

/** Displacements count observed bars. Only positions beyond history extrapolate the fixed UTC interval. */
export function ichimoku(bars:readonly Candle[],conversion=9,base=26,spanB=52,displacement=26,step=60):IchimokuValues{
 const result:IchimokuValues={conversion:[],base:[],leadingA:[],leadingB:[],lagging:[],cloud:[]};
 if(!bars.length||![conversion,base,spanB].every(validPeriod)||!Number.isInteger(displacement)||displacement<0||displacement>500||!Number.isFinite(step)||step<=0)return result;
 const tenkan=midpoints(bars,conversion),kijun=midpoints(bars,base),senkouB=midpoints(bars,spanB);
 const timeAt=(i:number)=>i<bars.length?bars[i].time:bars[bars.length-1].time+(i-bars.length+1)*step;
 for(let i=0;i<bars.length;i++){
  const t=tenkan[i],k=kijun[i],b=senkouB[i],a=t!==null&&k!==null?(t+k)/2:null;
  if(t!==null)result.conversion.push({time:bars[i].time,value:t});
  if(k!==null)result.base.push({time:bars[i].time,value:k});
  const projectedTime=timeAt(i+displacement);
  if(a!==null)result.leadingA.push({time:projectedTime,value:a});
  if(b!==null)result.leadingB.push({time:projectedTime,value:b});
  if(a!==null&&b!==null)result.cloud.push({time:projectedTime,a,b});
  // Do not manufacture prices before the beginning of the loaded history.
  if(i>=displacement)result.lagging.push({time:bars[i-displacement].time,value:Number(bars[i].close)});
 }
 return result;
}

export function macd(bars:readonly Candle[],fast=12,slow=26,signal=9):MACDValues{
 const result:MACDValues={macd:[],signal:[],histogram:[]};
 if(![fast,slow,signal].every(validPeriod)||fast>=slow)return result;
 const closes=bars.map(bar=>Number(bar.close)),fastValues=emaValues(closes,fast),slowValues=emaValues(closes,slow);
 for(let i=slow-1;i<bars.length;i++)result.macd.push({time:bars[i].time,value:fastValues[i]!-slowValues[i]!});
 const signals=emaValues(result.macd.map(point=>point.value),signal);
 for(let i=0;i<signals.length;i++){const value=signals[i];if(value===null)continue;const point=result.macd[i];result.signal.push({time:point.time,value});result.histogram.push({time:point.time,value:point.value-value});}
 return result;
}

import {useEffect,useRef,useState} from 'react';
import {CandlestickSeries,createChart,LineSeries,type IChartApi,type MouseEventHandler,type Time,type UTCTimestamp} from 'lightweight-charts';
import type {PredictionStatisticsForecast,PredictionStatisticsPeriod} from '../workspace/predictionStatistics';
import {statisticsPrice,statisticsProbability} from '../workspace/predictionStatistics';
import {predictionDate} from '../workspace/predictions';
import {chartThemeOptions} from '../charts/theme';
import {getThemePalette} from '../workspace/themes';
import {chartFontSize,useAppearanceFontSize} from '../workspace/appearance';
import {errorText} from '../types';

export const statisticsChartResources={charts:0,observers:0,listeners:0};
const colors=['#2774c4','#ba4270','#19836a','#b27616','#7955b0','#13849b','#ce6034','#5263a3','#a04592','#5c8030','#ab582d','#426d88','#b03d4c','#38744b','#826aab','#8b7140'];
export const statisticsScenarioColor=(id:string)=>colors[[...id].reduce((value,char)=>(value*2+(char==='U'?1:0))%colors.length,0)];
const dateLabel=(time:Time,period:PredictionStatisticsPeriod)=>new Intl.DateTimeFormat('en-GB',{timeZone:'UTC',weekday:'short',day:'2-digit',month:'short',...(period==='h1'?{hour:'2-digit',minute:'2-digit',hourCycle:'h23'} as const:{})}).format(new Date(Number(time)*1000));

export function PredictionStatisticsChart({forecast}:{forecast:PredictionStatisticsForecast}) {
 const host=useRef<HTMLDivElement>(null),chartRef=useRef<IChartApi|null>(null),fontSize=useAppearanceFontSize();
 const [error,setError]=useState(''),[readout,setReadout]=useState('Move over a candle or scenario to inspect its closing price.');
 const weekIndex=forecast.history.findIndex(bar=>bar.time>=forecast.weekStart);
 const lastPoint=Math.max(forecast.anchor.time,...forecast.scenarios.flatMap(scenario=>scenario.points.map(point=>point.time)));
 const forecastGridBars=forecast.period==='h1'?Math.max(0,Math.floor((lastPoint-forecast.anchor.time)/3_600_000)):0;
 const futureBars=forecast.period==='h1'?forecastGridBars:Math.max(0,...forecast.scenarios.map(scenario=>scenario.points.length));
 const focus=()=>chartRef.current?.timeScale().setVisibleLogicalRange({from:Math.max(0,(weekIndex<0?forecast.history.length-1:weekIndex)-3),to:forecast.history.length+futureBars+1});
 const focusRef=useRef(focus);focusRef.current=focus;
 useEffect(()=>{
  if(!host.current||!forecast.history.length)return;
  setError('');setReadout('Move over a candle or scenario to inspect its closing price.');
  const palette=getThemePalette(document.documentElement.dataset.theme??'light'),themeOptions=chartThemeOptions(palette);
  const chart=createChart(host.current,{...themeOptions,width:Math.max(280,host.current.clientWidth),height:460,layout:{...themeOptions.layout,fontFamily:'Inter, Ubuntu, system-ui, sans-serif',fontSize:chartFontSize(11,fontSize)},timeScale:{...themeOptions.timeScale,rightOffset:2,minBarSpacing:.1,timeVisible:forecast.period==='h1',tickMarkFormatter:(time:Time)=>dateLabel(time,forecast.period)},localization:{timeFormatter:(time:Time)=>predictionDate(Number(time)*1000)}});
  chartRef.current=chart;statisticsChartResources.charts++;
  let resize:ResizeObserver|undefined,theme:MutationObserver|undefined,mouse:MouseEventHandler<Time>|undefined;
  try{
   const priceFormat={type:'custom' as const,formatter:statisticsPrice,minMove:Math.max(1e-12,10**(Math.floor(Math.log10(forecast.anchor.close))-7))};
   const candles=chart.addSeries(CandlestickSeries,{upColor:palette.up,downColor:palette.danger,borderVisible:false,wickUpColor:palette.up,wickDownColor:palette.danger,lastValueVisible:false,priceLineVisible:false,priceFormat});
   // Keep H1's future calendar axis spaced in hours without inventing OHLC
   // candles between the daily scenario checkpoints. These are empty slots,
   // independent of market sessions or any claimed future trading activity.
   const futureGrid=Array.from({length:forecastGridBars},(_,index)=>({time:(forecast.anchor.time+(index+1)*3_600_000)/1000 as UTCTimestamp}));
   candles.setData([...forecast.history.map(bar=>({time:bar.time/1000 as UTCTimestamp,open:bar.open,high:bar.high,low:bar.low,close:bar.close})),...futureGrid]);
   const series=forecast.scenarios.map(scenario=>{
    const line=chart.addSeries(LineSeries,{color:statisticsScenarioColor(scenario.id),lineWidth:2,title:`${statisticsProbability(scenario.probability)} · ${scenario.id}`,priceFormat,lastValueVisible:true,priceLineVisible:false});
    line.setData([{time:forecast.anchor.time/1000 as UTCTimestamp,value:forecast.anchor.close},...scenario.points.map(point=>({time:point.time/1000 as UTCTimestamp,value:point.close}))]);
    return {line,scenario};
   });
   const updateTheme=()=>{const next=getThemePalette(document.documentElement.dataset.theme??'light');chart.applyOptions(chartThemeOptions(next));candles.applyOptions({upColor:next.up,downColor:next.danger,wickUpColor:next.up,wickDownColor:next.danger});};
   theme=new MutationObserver(updateTheme);theme.observe(document.documentElement,{attributes:true,attributeFilter:['data-theme']});statisticsChartResources.observers++;
   resize=new ResizeObserver(()=>{if(host.current)chart.resize(Math.max(280,host.current.clientWidth),460);});resize.observe(host.current);statisticsChartResources.observers++;
   mouse=event=>{
    if(typeof event.time!=='number')return;
    const timestamp=Number(event.time)*1000,bar=forecast.history.find(bar=>bar.time===timestamp),values=[...new Set(series.flatMap(({scenario})=>{const point=scenario.points.find(point=>point.time===timestamp);return point?[point.observed?`Observed daily close ${statisticsPrice(point.close)}`:`${scenario.id} · ${statisticsProbability(scenario.probability)} · Daily close ${statisticsPrice(point.close)}`]:[];}))];
    setReadout(`${predictionDate(timestamp)}${bar?` · Close ${statisticsPrice(bar.close)}`:''}${values.length?` · ${values.join(' | ')}`:''}`);
   };
   chart.subscribeCrosshairMove(mouse);statisticsChartResources.listeners++;focusRef.current();
  }catch(reason){setError(errorText(reason));}
  return()=>{if(resize){resize.disconnect();statisticsChartResources.observers--;}if(theme){theme.disconnect();statisticsChartResources.observers--;}if(mouse){chart.unsubscribeCrosshairMove(mouse);statisticsChartResources.listeners--;}chart.remove();chartRef.current=null;statisticsChartResources.charts--;};
 },[forecast]);
 useEffect(()=>{chartRef.current?.applyOptions({layout:{fontSize:chartFontSize(11,fontSize)}});},[fontSize]);
 return <div className="statistics-chart"><div className="prediction-chart-actions"><button type="button" onClick={focus}>Focus week</button><button type="button" onClick={()=>chartRef.current?.timeScale().fitContent()}>Show all history</button></div>{error&&<p className="screener-error" role="alert">{error}</p>}<div ref={host} className="statistics-plot" data-testid="statistics-chart" data-period={forecast.period} data-anchor-time={forecast.anchor.time} data-friday-time={lastPoint} data-forecast-grid-bars={forecastGridBars} data-scenario-count={forecast.scenarios.length} aria-label={`Historical ${forecast.period.toUpperCase()} candles and weekly scenario paths`}/><p className="prediction-readout" aria-live="off">{readout}</p><p className="muted statistics-chart-note">Every path starts at the selected period's last closed candle. Line probabilities describe daily candle directions, and price labels show the final estimated daily close. {forecast.period==='h1'?'Daily close checkpoints use the final H1 slot at 23:00 UTC; future hourly candles between them are not estimated.':'Future daily checkpoints use UTC D1 candle dates; Friday closes Saturday at 00:00 UTC.'}{forecast.period==='w1'?' An observed daily point, when present, connects the weekly anchor to known data.':''}</p></div>;
}

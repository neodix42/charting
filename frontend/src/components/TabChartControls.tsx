import {useState} from 'react';
import {intervals,type ChartConfig,type IndicatorConfig,type IndicatorType,type Interval} from '../types';
import {MAX_INDICATORS} from '../workspace/model';
import {IndicatorDialog,indicatorNames} from '../charts/IndicatorDialog';

interface Props {
 charts:readonly ChartConfig[];
 onTimeframe(timeframe:Interval):void;
 onIndicator(indicator:IndicatorConfig):void;
}

export function TabChartControls({charts,onTimeframe,onIndicator}:Props){
 const [indicatorType,setIndicatorType]=useState<IndicatorType|null>(null);
 const commonPeriod=charts.length&&charts.every(chart=>chart.timeframe===charts[0].timeframe)?charts[0].timeframe:null;
 const full=charts.some(chart=>(chart.indicators?.length??0)>=MAX_INDICATORS);
 return <div className="tab-chart-controls" data-testid="tab-chart-controls" role="toolbar" aria-label="Current tab chart controls">
  <div className="timeframe-buttons" role="group" aria-label="Period for current tab" title={commonPeriod?'Apply a period to every chart in this tab':'Mixed chart periods. Choose a period for every chart in this tab.'}>
   {intervals.map(period=><button key={period} type="button" aria-label={`Apply ${period} to current tab`} aria-pressed={commonPeriod===period} title={`Use ${period} on every chart in this tab`} onClick={()=>{if(commonPeriod!==period)onTimeframe(period);}}>{period}</button>)}
  </div>
  <select aria-label="Indicators for current tab" value="" disabled={full} title={full?'A chart has 12 indicators. Remove an indicator before adding one to the whole tab.':'Add an indicator to every chart in this tab'} onChange={event=>{if(event.target.value)setIndicatorType(event.target.value as IndicatorType);}}>
   <option value="">Indicators</option>{(Object.keys(indicatorNames) as IndicatorType[]).map(type=><option key={type} value={type}>{indicatorNames[type]}</option>)}
  </select>
  {indicatorType&&<IndicatorDialog type={indicatorType} scopeLabel={`Applies to all ${charts.length} chart${charts.length===1?'':'s'} in this tab.`} onClose={()=>setIndicatorType(null)} onSave={indicator=>{onIndicator(indicator);setIndicatorType(null);}}/>}
 </div>;
}

import {useEffect,useId,useState} from 'react';
import type {AnalysisCatalog,AnalysisResult,AnalysisTradeSimulation} from '../workspace/screenerAnalysis';
import {analysisFieldGroups,analysisFieldLabel} from '../workspace/screenerAnalysis';

export function ScreenerTradeSimulation({value,catalog,onChange,onRemove,onInputError}:{value:AnalysisTradeSimulation;catalog:AnalysisCatalog|null;onChange(value:AnalysisTradeSimulation):void;onRemove():void;onInputError(error:string):void}){
 const [duration,setDuration]=useState(String(value.duration)),helpID=useId();
 useEffect(()=>{setDuration(String(value.duration));onInputError('');},[value,onInputError]);
 const available=catalog?.fields??[],groups=analysisFieldGroups(available),known=new Set(available);
 return <fieldset className="analysis-trade-simulation" aria-describedby={helpID}>
  <legend>Simulate trade <span>LONG</span></legend>
  <div className="analysis-trade-controls">
   {(['stopLoss','takeProfit'] as const).map(key=><label key={key}>{key==='stopLoss'?'Stop Loss':'Take profit'}<select aria-label={key==='stopLoss'?'Stop Loss field':'Take profit field'} required value={value[key]} onChange={event=>onChange({...value,[key]:event.target.value})}>
    <option value="">Choose field…</option>
    {value[key]&&!known.has(value[key])&&<option value={value[key]}>{value[key]} (not in current files)</option>}
    {groups.map(([group,fields])=><optgroup key={group} label={group}>{fields.map(field=><option key={field} value={field}>{analysisFieldLabel(field)}</option>)}</optgroup>)}
   </select></label>)}
   <label>Duration<input aria-label="Trade duration in bars" type="number" required min={1} max={10000} step={1} value={duration} onChange={event=>{const text=event.target.value,next=Number(text);setDuration(text);if(text!==''&&Number.isInteger(next)&&next>=1&&next<=10000){onInputError('');onChange({...value,duration:next});}else onInputError('Trade duration must be a whole number from 1 to 10,000 bars.');}}/><span>bars</span></label>
   <button type="button" className="analysis-trade-remove" onClick={onRemove}>Remove simulation</button>
  </div>
  <p id={helpID}>Open a long trade at the next closed bar’s open. Stop Loss and Take profit are fixed price levels from the matching bar; Stop Loss must be below entry and Take profit above it. Duration includes the entry bar.</p>
  <p>Opening gaps resolve first. If both levels are touched within one bar, Stop Loss counts first. Missing data and invalid levels stay in the total match count.</p>
 </fieldset>;
}

export function AnalysisSimulationCell({result}:{result:AnalysisResult}){
 const counts=result.simulation;
 if(!result.matches)return <td className="analysis-profit"><strong>—</strong><small>No matching bars</small></td>;
 if(!counts)return <td className="analysis-profit"><strong>{['queued','indexing','running'].includes(result.status)?'Pending':'Unavailable'}</strong></td>;
 const unavailable=counts.unavailable===result.matches;
 return <td className="analysis-profit"><strong>{unavailable?'Unavailable':`${counts.profitablePercent.toLocaleString('en-US',{maximumFractionDigits:2})}%`}</strong>
  {['queued','indexing','running'].includes(result.status)&&<small>In progress</small>}
  <details onClick={event=>event.stopPropagation()}><summary>{counts.takeProfitHits.toLocaleString('en-US')} / {result.matches.toLocaleString('en-US')} hit TP</summary><div>
   <span>{counts.stopLossHits.toLocaleString('en-US')} hit SL</span><span>{counts.expired.toLocaleString('en-US')} expired</span><span>{counts.insufficientData.toLocaleString('en-US')} insufficient future data</span><span>{counts.invalidLevels.toLocaleString('en-US')} invalid levels</span><span>{counts.unavailable.toLocaleString('en-US')} unavailable</span>
   {counts.reason&&<span>{counts.reason}</span>}
  </div></details>
 </td>;
}

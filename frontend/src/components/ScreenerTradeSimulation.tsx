import {useEffect,useId,useRef,useState} from 'react';
import type {AnalysisCatalog,AnalysisResult,AnalysisTradeSimulation} from '../workspace/screenerAnalysis';
import {analysisFieldGroups,analysisFieldLabel} from '../workspace/screenerAnalysis';

export function ScreenerTradeSimulation({value,catalog,onChange,onRemove,onInputError}:{value:AnalysisTradeSimulation;catalog:AnalysisCatalog|null;onChange(value:AnalysisTradeSimulation):void;onRemove():void;onInputError(error:string):void}){
 const [duration,setDuration]=useState(String(value.duration)),[barsAgo,setBarsAgo]=useState(String(value.stopLossBarsAgo??0)),[inputErrors,setInputErrors]=useState({duration:'',barsAgo:''}),helpID=useId();
 const emitted=useRef<AnalysisTradeSimulation|null>(null);
 useEffect(()=>{if(value!==emitted.current){setDuration(String(value.duration));setBarsAgo(String(value.stopLossBarsAgo??0));setInputErrors({duration:'',barsAgo:''});}},[value]);
 useEffect(()=>{onInputError(inputErrors.barsAgo||inputErrors.duration);},[inputErrors,onInputError]);
 const change=(next:AnalysisTradeSimulation)=>{emitted.current=next;onChange(next);};
 const numberChange=(key:'duration'|'barsAgo',text:string)=>{
  const next=Number(text),valid=text!==''&&Number.isInteger(next)&&next>=(key==='duration'?1:0)&&next<=10000;
  (key==='duration'?setDuration:setBarsAgo)(text);
  setInputErrors(current=>({...current,[key]:valid?'':key==='duration'?'Trade duration must be a whole number from 1 to 10,000 bars.':'Stop Loss Bars ago must be a whole number from 0 to 10,000.'}));
  if(valid)change({...value,[key==='duration'?'duration':'stopLossBarsAgo']:next});
 };
 const available=catalog?.fields??[],groups=analysisFieldGroups(available),known=new Set(available);
 const fieldOptions=(field:string,prefix='')=><><option value="">Choose field…</option>{field&&!known.has(field)&&<option value={prefix+field}>{field} (not in current files)</option>}{groups.map(([group,fields])=><optgroup key={group} label={group}>{fields.map(field=><option key={field} value={prefix+field}>{analysisFieldLabel(field)}</option>)}</optgroup>)}</>;
 const profitValue=value.takeProfitMultiple?`risk:${value.takeProfitMultiple}`:value.takeProfit?`field:${value.takeProfit}`:'';
 return <fieldset className="analysis-trade-simulation" aria-describedby={helpID}>
  <legend>Simulate trade <span>LONG</span></legend>
  <div className="analysis-trade-controls">
   <label>Stop Loss<select aria-label="Stop Loss field" required value={value.stopLoss} onChange={event=>change({...value,stopLoss:event.target.value})}>{fieldOptions(value.stopLoss)}</select></label>
   <label>Bars ago<input aria-label="Stop Loss bars ago" type="number" required min={0} max={10000} step={1} value={barsAgo} onChange={event=>numberChange('barsAgo',event.target.value)}/><span>bars</span></label>
   <label>Take profit<select aria-label="Take profit field" required value={profitValue} onChange={event=>{const selected=event.target.value;change(selected.startsWith('risk:')?{...value,takeProfit:'',takeProfitMultiple:Number(selected.slice(5))}:{...value,takeProfit:selected.slice(6),takeProfitMultiple:undefined});}}>
    <optgroup label="Stop Loss distance">{[1,2,3].map(multiple=><option key={multiple} value={`risk:${multiple}`}>{multiple}xSL</option>)}</optgroup>{fieldOptions(value.takeProfit,'field:')}
   </select></label>
   <label>Duration<input aria-label="Trade duration in bars" type="number" required min={1} max={10000} step={1} value={duration} onChange={event=>numberChange('duration',event.target.value)}/><span>bars</span></label>
   <button type="button" className="analysis-trade-remove" onClick={onRemove}>Remove simulation</button>
  </div>
  <p id={helpID}>Open a long trade at the next closed bar’s open. Stop Loss uses its selected field from the matching bar (Bars ago = 0), or an earlier bar. Take profit uses a field from the matching bar or entry + 1, 2 or 3 × (entry − Stop Loss). Levels stay fixed; Duration includes the entry bar.</p>
  <p>Stop Loss must be below entry and Take profit above it. Opening gaps resolve first. If both levels are touched within one bar, Stop Loss counts first. Missing data and invalid levels stay in the total match count.</p>
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

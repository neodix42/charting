import {useState} from 'react';
import type {ChartRightGap} from '../types';

export function ChartGapSetting({value,onApply}:{value:ChartRightGap;onApply(gap:ChartRightGap):void}){
 const [unit,setUnit]=useState(value.unit),[amount,setAmount]=useState(String(value.value)),[error,setError]=useState('');
 const maximum=unit==='bars'?500:5000;
 return <form className="setting-row chart-gap-setting" noValidate onSubmit={event=>{
  event.preventDefault();const number=Number(amount);
  if(!amount.trim()||!Number.isSafeInteger(number)||number<0||number>maximum){setError(`Enter a whole number from 0 to ${maximum.toLocaleString('en-US')} ${unit}.`);return;}
  setError('');onApply({unit,value:number});
 }}>
  <span><strong>Chart right gap</strong><small>Space after the latest candle. Applies to all tabs.</small><small>Apply returns charts to live, keeping their zoom.</small></span>
  <div className="chart-gap-fields"><div className="chart-gap-controls">
   <input aria-label="Chart right gap" type="number" inputMode="numeric" min="0" max={maximum} step="1" value={amount} aria-invalid={!!error} aria-describedby={error?'chart-gap-error':undefined} onChange={event=>{setAmount(event.target.value);setError('');}}/>
   <select aria-label="Chart right gap unit" value={unit} onChange={event=>{setUnit(event.target.value as ChartRightGap['unit']);setError('');}}><option value="bars">Bars</option><option value="pixels">Pixels</option></select>
   <button type="submit" aria-label="Apply chart gap">Apply</button>
  </div>{error&&<small id="chart-gap-error" className="danger" role="alert">{error}</small>}</div>
 </form>;
}

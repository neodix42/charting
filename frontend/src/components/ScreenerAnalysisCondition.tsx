import {useEffect,useState} from 'react';
import type {AnalysisCatalog,AnalysisCondition,AnalysisOperand,AnalysisOperator} from '../workspace/screenerAnalysis';
import {analysisFieldGroups,analysisFieldLabel,analysisOperators,fieldOperand,numberOperand} from '../workspace/screenerAnalysis';

function NumberInput({value,onChange,...props}:{value:number;onChange(value:number):void;min?:number;max?:number;step?:number|string;'aria-label':string}){
 const [draft,setDraft]=useState(String(value));
 useEffect(()=>{setDraft(String(value));},[value]);
 return <input {...props} type="number" required value={draft} onChange={event=>{const text=event.target.value;setDraft(text);if(text!==''&&Number.isFinite(Number(text)))onChange(Number(text));}} onBlur={()=>{if(draft==='')setDraft(String(value));}}/>;
}

export function AnalysisOperandEditor({label,value,catalog,onChange}:{label:string;value:AnalysisOperand;catalog:AnalysisCatalog|null;onChange(value:AnalysisOperand):void}){
 const available=[...new Set(['price','close',...(catalog?.fields??[])])],groups=analysisFieldGroups(available),known=new Set(available);
 return <div className="analysis-operand" role="group" aria-label={label}>
  <div className="analysis-operand-main"><select aria-label={`${label} type`} value={value.kind} onChange={event=>onChange(event.target.value==='number'?numberOperand():fieldOperand(known.has('close')?'close':catalog?.fields[0]||'close'))}><option value="field">Field</option><option value="number">Constant</option></select>
   {value.kind==='number'?<NumberInput aria-label={`${label} constant`} value={value.value??0} step="any" onChange={next=>onChange({...value,value:next})}/>:<select aria-label={`${label} field`} value={value.field??''} onChange={event=>onChange({...value,field:event.target.value})}>
    {!known.has(value.field??'')&&<option value={value.field??''}>{value.field||'Choose field'} (not in current files)</option>}
    {groups.map(([group,fields])=><optgroup key={group} label={group}>{fields.map(field=><option key={field} value={field}>{analysisFieldLabel(field)}</option>)}</optgroup>)}
   </select>}
  </div>
  {value.kind==='field'&&<div className="analysis-operand-context">
   <label>Timeframe<select aria-label={`${label} timeframe`} value={value.timeframe||'same'} onChange={event=>onChange({...value,timeframe:event.target.value})}><option value="same">Same as file</option>{[...new Set([...(catalog?.timeframes??[]),...(value.timeframe&&value.timeframe!=='same'?[value.timeframe]:[])])].map(timeframe=><option key={timeframe} value={timeframe}>{timeframe}</option>)}</select></label>
   <label>Bars ago<NumberInput aria-label={`${label} bars ago`} min={0} max={10000} step={1} value={value.offset??0} onChange={next=>onChange({...value,offset:next})}/></label>
   <label>Value<select aria-label={`${label} aggregation`} value={value.aggregate||'value'} onChange={event=>onChange({...value,aggregate:event.target.value as AnalysisOperand['aggregate'],window:Math.max(1,value.window??1)})}><option value="value">At bar</option><option value="highest">Highest</option><option value="lowest">Lowest</option></select></label>
   {value.aggregate&&value.aggregate!=='value'&&<label>Window<NumberInput aria-label={`${label} aggregation window`} min={1} max={10000} step={1} value={value.window??1} onChange={next=>onChange({...value,window:next})}/></label>}
  </div>}
 </div>;
}

export function ScreenerAnalysisCondition({condition,index,catalog,onChange,onRemove,canRemove=true}:{condition:AnalysisCondition;index:number;catalog:AnalysisCatalog|null;onChange(value:AnalysisCondition):void;onRemove():void;canRemove?:boolean}){
 const temporal=condition.operator==='occurredWithin'||condition.operator==='remainedFor',operator=temporal?condition.comparison||'above':condition.operator;
 const patch=(value:Partial<AnalysisCondition>)=>onChange({...condition,...value});
 const fields=(catalog?.fields??[]).filter(field=>field.toLowerCase()!=='timestamp');
 const upper=condition.upper&&(condition.upper.kind==='field'||condition.upper.kind==='number')?condition.upper:numberOperand(100);
 const distance=condition.distance&&(condition.distance.kind==='field'||condition.distance.kind==='number')?condition.distance:fieldOperand(fields.find(field=>/^atr(?:_|$)/i.test(field))??fields[0]??'atr_14');
 return <fieldset className="analysis-condition" aria-label={`Condition ${index+1}`}>
  <legend><span className="analysis-condition-number">{index+1}</span>{index===0?'When':'AND'}<button type="button" className="analysis-remove" aria-label={`Remove condition ${index+1}`} disabled={!canRemove} title={canRemove?'Remove this condition':'Keep at least one condition'} onClick={onRemove}>−</button></legend>
  <div className="analysis-condition-body">
   <AnalysisOperandEditor label={`Condition ${index+1} left`} value={condition.left} catalog={catalog} onChange={left=>patch({left})}/>
   <div className="analysis-comparison"><label>Condition<select aria-label={`Condition ${index+1} operator`} value={condition.operator} onChange={event=>{
    const operator=event.target.value as AnalysisOperator;
    patch({operator,bars:condition.bars??3,multiplier:condition.multiplier??0.2,comparison:condition.comparison||'above',upper,distance});
   }}>{analysisOperators.map(item=><option key={item.id} value={item.id}>{item.label}</option>)}</select></label>
   {temporal&&<label>Comparison<select aria-label={`Condition ${index+1} comparison`} value={operator} onChange={event=>patch({comparison:event.target.value})}>{analysisOperators.filter(item=>!['occurredWithin','remainedFor','highest','lowest'].includes(item.id)).map(item=><option key={item.id} value={item.id}>{item.label}</option>)}</select></label>}
   {['highest','lowest','occurredWithin','remainedFor'].includes(condition.operator)&&<label>Closed bars<NumberInput aria-label={`Condition ${index+1} bar window`} min={1} max={10000} step={1} value={condition.bars??3} onChange={bars=>patch({bars})}/></label>}
   </div>
   <AnalysisOperandEditor label={`Condition ${index+1} right`} value={condition.right} catalog={catalog} onChange={right=>patch({right})}/>
   {operator==='between'&&<div className="analysis-extra-operand"><span>and</span><AnalysisOperandEditor label={`Condition ${index+1} upper`} value={upper} catalog={catalog} onChange={upper=>patch({upper})}/></div>}
   {operator==='withinDistance'&&<div className="analysis-distance"><label>Distance multiplier<NumberInput aria-label={`Condition ${index+1} distance multiplier`} min={0} step="any" value={condition.multiplier??0.2} onChange={multiplier=>patch({multiplier})}/></label><span>×</span><AnalysisOperandEditor label={`Condition ${index+1} distance`} value={distance} catalog={catalog} onChange={distance=>patch({distance})}/></div>}
  </div>
 </fieldset>;
}

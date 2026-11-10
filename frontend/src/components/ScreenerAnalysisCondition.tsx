import {memo,useEffect,useId,useMemo,useState,type ReactNode} from 'react';
import type {AnalysisCatalog,AnalysisCondition,AnalysisFieldScope,AnalysisOperand,AnalysisOperator} from '../workspace/screenerAnalysis';
import {analysisFieldGroups,analysisFieldLabel,analysisOperators,analysisRollingField,analysisRollingFieldMessage,fieldOperand,numberOperand} from '../workspace/screenerAnalysis';
import {analysisOperatorHelp} from '../workspace/screenerOperatorHelp';
import './ScreenerAnalysisCondition.css';

// The same catalog serves every operand. Reuse its grouped labels and immutable
// option tree instead of rebuilding thousands of options for each draft edit.
const emptyFields:string[]=[];
const fieldMenus=new WeakMap<string[],{known:ReadonlySet<string>;options:ReactNode}>();
function fieldMenu(fields:string[]){
 let menu=fieldMenus.get(fields);if(menu)return menu;
 const available=[...new Set(['price','close','low_10','high_10',...fields])];
 menu={known:new Set(available),options:analysisFieldGroups(available).map(([group,items])=><optgroup key={group} label={group}>{items.map(field=><option key={field} value={field}>{analysisFieldLabel(field)}</option>)}</optgroup>)};
 fieldMenus.set(fields,menu);return menu;
}
const FieldOptions=memo(function FieldOptions({fields}:{fields:string[]}){return fieldMenu(fields).options;});

function OperatorSelect({label,name,value,options=analysisOperators,onChange}:{label:string;name:string;value:string;options?:typeof analysisOperators;onChange(value:AnalysisOperator):void}){
 const [showHelp,setShowHelp]=useState(false),helpID=useId();
 useEffect(()=>{
  if(!showHelp)return;
  const dismiss=(event:KeyboardEvent)=>{if(event.key==='Escape')setShowHelp(false);};
  window.addEventListener('keydown',dismiss);return()=>window.removeEventListener('keydown',dismiss);
 },[showHelp]);
 const selected=options.find(item=>item.id===value)??options[0],help=analysisOperatorHelp[selected.id];
 return <label className="analysis-operator-control" onMouseEnter={()=>setShowHelp(true)} onMouseLeave={()=>setShowHelp(false)} onBlur={event=>{if(!event.currentTarget.contains(event.relatedTarget))setShowHelp(false);}} onKeyDown={event=>{if(event.key==='Escape'){setShowHelp(false);event.stopPropagation();}}}>
  <span className="analysis-operator-label">{label}<button type="button" className="analysis-operator-help" aria-label={`Help for ${name}`} aria-describedby={helpID} onFocus={()=>setShowHelp(true)} onClick={()=>setShowHelp(value=>!value)}>?</button></span>
  <select aria-label={name} aria-describedby={helpID} value={value} onChange={event=>{setShowHelp(false);onChange(event.target.value as AnalysisOperator);}}>{options.map(item=><option key={item.id} value={item.id}>{item.label}</option>)}</select>
  <span id={helpID} role="tooltip" className="analysis-operator-tooltip" hidden={!showHelp}><strong>{selected.label}</strong><span>{help.description}</span><span>Example: {help.example}</span></span>
 </label>;
}

function NumberInput({value,onChange,...props}:{value:number;onChange(value:number):void;min?:number;max?:number;step?:number|string;'aria-label':string}){
 const [draft,setDraft]=useState(String(value));
 useEffect(()=>{setDraft(String(value));},[value]);
 return <input {...props} type="number" required value={draft} onChange={event=>{const text=event.target.value;setDraft(text);if(text!==''&&Number.isFinite(Number(text)))onChange(Number(text));}} onBlur={()=>{if(draft==='')setDraft(String(value));}}/>;
}

export function AnalysisOperandEditor({label,value,catalog,scope,onChange}:{label:string;value:AnalysisOperand;catalog:AnalysisCatalog|null;scope?:AnalysisFieldScope;onChange(value:AnalysisOperand):void}){
 const fields=catalog?.fields??emptyFields,{known}=fieldMenu(fields);
 const rolling=value.kind==='field'?analysisRollingField(value.field??''):null,helpID=useId();
 const enrichmentMessage=useMemo(()=>analysisRollingFieldMessage(value,catalog,scope),[value.kind,value.field,value.timeframe,catalog,scope?.period,scope?.categories]);
 const timeframes=useMemo(()=>[...new Set([...(catalog?.timeframes??[]),...(value.timeframe&&value.timeframe!=='same'?[value.timeframe]:[])])],[catalog?.timeframes,value.timeframe]);
 return <div className="analysis-operand" role="group" aria-label={label}>
  <span className="analysis-operand-label">{label.endsWith(' left')?'Left value':label.endsWith(' right')?'Right value':label.endsWith(' upper')?'Upper value':'Distance value'}</span>
  <div className="analysis-operand-main"><select aria-label={`${label} type`} value={value.kind} onChange={event=>onChange(event.target.value==='number'?numberOperand():fieldOperand(known.has('close')?'close':catalog?.fields[0]||'close'))}><option value="field">Field</option><option value="number">Constant</option></select>
   {value.kind==='number'?<NumberInput aria-label={`${label} constant`} value={value.value??0} step="any" onChange={next=>onChange({...value,value:next})}/>:<select aria-label={`${label} field`} aria-describedby={enrichmentMessage?helpID:undefined} value={value.field??''} onChange={event=>onChange({...value,field:event.target.value})}>
    {!known.has(value.field??'')&&<option value={value.field??''}>{value.field?analysisFieldLabel(value.field):'Choose field'}{!rolling?' (not in current files)':''}</option>}
    <FieldOptions fields={fields}/>
   </select>}
  </div>
  {value.kind==='field'&&<div className="analysis-operand-context">
   {rolling&&<label title="Includes the selected bar. Use Bars ago to shift the window.">Lookback bars<NumberInput aria-label={`${label} lookback bars`} min={1} max={2000} step={1} value={rolling.period} onChange={next=>onChange({...value,field:`${rolling.family}_${next}`})}/></label>}
   <label>Timeframe<select aria-label={`${label} timeframe`} value={value.timeframe||'same'} onChange={event=>onChange({...value,timeframe:event.target.value})}><option value="same">Same as file</option>{timeframes.map(timeframe=><option key={timeframe} value={timeframe}>{timeframe}</option>)}</select></label>
   <label>Bars ago<NumberInput aria-label={`${label} bars ago`} min={0} max={10000} step={1} value={value.offset??0} onChange={next=>onChange({...value,offset:next})}/></label>
   <label>Value<select aria-label={`${label} aggregation`} value={value.aggregate||'value'} onChange={event=>onChange({...value,aggregate:event.target.value as AnalysisOperand['aggregate'],window:Math.max(1,value.window??1)})}><option value="value">At bar</option><option value="highest">Highest</option><option value="lowest">Lowest</option></select></label>
   {value.aggregate&&value.aggregate!=='value'&&<label>Window<NumberInput aria-label={`${label} aggregation window`} min={1} max={10000} step={1} value={value.window??1} onChange={next=>onChange({...value,window:next})}/></label>}
  </div>}
  {enrichmentMessage&&<p id={helpID} className="analysis-enrichment-message" role="status">{enrichmentMessage}</p>}
 </div>;
}

export function ScreenerAnalysisCondition({condition,index,catalog,scope,onChange,onRemove,canRemove=true}:{condition:AnalysisCondition;index:number;catalog:AnalysisCatalog|null;scope?:AnalysisFieldScope;onChange(value:AnalysisCondition):void;onRemove():void;canRemove?:boolean}){
 const temporal=condition.operator==='occurredWithin'||condition.operator==='remainedFor',operator=temporal?condition.comparison||'above':condition.operator;
 const patch=(value:Partial<AnalysisCondition>)=>onChange({...condition,...value});
 const fields=(catalog?.fields??[]).filter(field=>field.toLowerCase()!=='timestamp');
 const upper=condition.upper&&(condition.upper.kind==='field'||condition.upper.kind==='number')?condition.upper:numberOperand(100);
 const distance=condition.distance&&(condition.distance.kind==='field'||condition.distance.kind==='number')?condition.distance:fieldOperand(fields.find(field=>/^atr(?:_|$)/i.test(field))??fields[0]??'atr_14');
 return <fieldset className="analysis-condition" aria-label={`Condition ${index+1}`}>
  <legend><span className="analysis-condition-number">{index+1}</span>{index===0?'When':'AND'}<button type="button" className="analysis-remove" aria-label={`Remove condition ${index+1}`} disabled={!canRemove} title={canRemove?'Remove this condition':'Keep at least one condition'} onClick={onRemove}>−</button></legend>
  <div className="analysis-condition-body">
   <AnalysisOperandEditor label={`Condition ${index+1} left`} value={condition.left} catalog={catalog} scope={scope} onChange={left=>patch({left})}/>
   <div className="analysis-comparison"><OperatorSelect label="Condition" name={`Condition ${index+1} operator`} value={condition.operator} onChange={operator=>{
    patch({operator,bars:condition.bars??3,multiplier:condition.multiplier??0.2,comparison:condition.comparison||'above',upper,distance});
   }}/>
   {temporal&&<OperatorSelect label="Comparison" name={`Condition ${index+1} comparison`} value={operator} onChange={comparison=>patch({comparison})} options={analysisOperators.filter(item=>!['occurredWithin','remainedFor','highest','lowest'].includes(item.id))}/>}
   {['highest','lowest','occurredWithin','remainedFor'].includes(condition.operator)&&<label>Closed bars<NumberInput aria-label={`Condition ${index+1} bar window`} min={1} max={10000} step={1} value={condition.bars??3} onChange={bars=>patch({bars})}/></label>}
   </div>
   <AnalysisOperandEditor label={`Condition ${index+1} right`} value={condition.right} catalog={catalog} scope={scope} onChange={right=>patch({right})}/>
   {operator==='between'&&<div className="analysis-extra-operand"><span>and</span><AnalysisOperandEditor label={`Condition ${index+1} upper`} value={upper} catalog={catalog} scope={scope} onChange={upper=>patch({upper})}/></div>}
   {operator==='withinDistance'&&<div className="analysis-distance"><label><span>Distance multiplier</span><NumberInput aria-label={`Condition ${index+1} distance multiplier`} min={0} step="any" value={condition.multiplier??0.2} onChange={multiplier=>patch({multiplier})}/></label><span>×</span><AnalysisOperandEditor label={`Condition ${index+1} distance`} value={distance} catalog={catalog} scope={scope} onChange={distance=>patch({distance})}/></div>}
  </div>
 </fieldset>;
}

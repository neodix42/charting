import {useEffect,useRef,useState} from 'react';
import {createPortal} from 'react-dom';
import type {IndicatorConfig,IndicatorParameters,IndicatorType} from '../types';
import {uuid} from '../workspace/model';

export const indicatorNames:Record<IndicatorType,string>={sma:'SMA',ema:'EMA',ichimoku:'Ichimoku Cloud',macd:'MACD'};
const defaults:Record<IndicatorType,IndicatorParameters>={sma:{period:20},ema:{period:20},ichimoku:{conversion:9,base:26,spanB:52,displacement:26},macd:{fast:12,slow:26,signal:9}};
const colors:Record<IndicatorType,string>={sma:'#e7ad51',ema:'#61b6ef',ichimoku:'#b58cf1',macd:'#61b6ef'};
const fields:Record<IndicatorType,Array<[keyof IndicatorParameters,string]>>={sma:[['period','Period']],ema:[['period','Period']],ichimoku:[['conversion','Conversion period'],['base','Base period'],['spanB','Leading span B period'],['displacement','Displacement']],macd:[['fast','Fast period'],['slow','Slow period'],['signal','Signal period']]};
export function indicatorLabel(config:IndicatorConfig){const p=config.parameters;return `${indicatorNames[config.type]} (${config.type==='sma'||config.type==='ema'?p.period:config.type==='macd'?`${p.fast}, ${p.slow}, ${p.signal}`:`${p.conversion}, ${p.base}, ${p.spanB}, ${p.displacement}`})`;}

export function IndicatorDialog({type,existing,onSave,onClose}:{type:IndicatorType;existing?:IndicatorConfig;onSave(value:IndicatorConfig):void;onClose():void}){
 const [values,setValues]=useState<IndicatorParameters>(()=>({...defaults[type],...existing?.parameters})),[color,setColor]=useState(existing?.color??colors[type]),[error,setError]=useState('');
 const form=useRef<HTMLFormElement>(null),previousFocus=useRef<HTMLElement|null>(null);
 useEffect(()=>{previousFocus.current=document.activeElement as HTMLElement;form.current?.querySelector<HTMLInputElement>('input')?.focus();return ()=>{previousFocus.current?.focus();};},[]);
 const submit=()=>{for(const [key,label] of fields[type]){const value=values[key];const min=key==='displacement'?0:1,max=key==='displacement'?500:2000;if(value===undefined||!Number.isInteger(value)||value<min||value>max){setError(`${label} must be a whole number from ${min} to ${max}.`);return;}}if(type==='macd'&&values.fast!>=values.slow!){setError('Fast period must be smaller than slow period.');return;}if(!/^#[0-9a-f]{6}$/i.test(color)){setError('Choose a valid color.');return;}onSave({id:existing?.id??uuid(),type,parameters:values,color});};
 return createPortal(<div className="modal-backdrop indicator-backdrop" onMouseDown={event=>{if(event.target===event.currentTarget)onClose();}} onKeyDown={event=>{
  event.stopPropagation();if(event.key==='Escape'){event.preventDefault();onClose();return;}if(event.key!=='Tab')return;const controls=form.current?.querySelectorAll<HTMLElement>('button:not(:disabled),input:not(:disabled)');if(!controls?.length)return;const first=controls[0],last=controls[controls.length-1];if(event.shiftKey&&document.activeElement===first){event.preventDefault();last.focus();}else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();first.focus();}
 }}><form ref={form} className="modal indicator-modal" role="dialog" aria-modal="true" aria-label={`${indicatorNames[type]} parameters`} onSubmit={event=>{event.preventDefault();submit();}}>
  <div className="modal-header"><h2>{indicatorNames[type]} parameters</h2><button type="button" aria-label="Close indicator parameters" onClick={onClose}>×</button></div>
  <p className="indicator-description">{type==='ichimoku'?'Periods and displacement are measured in chart bars. The cloud projects forward by the displacement.':type==='macd'?'MACD uses the close price and appears in a separate pane. Periods are measured in chart bars.':'Calculated from candle close prices. The period is measured in chart bars.'}</p>
  {fields[type].map(([key,label])=><label className="indicator-field" key={key}><span>{label}</span><input aria-label={label} type="number" min={key==='displacement'?0:1} max={key==='displacement'?500:2000} step="1" required value={values[key]??''} onChange={event=>{setValues({...values,[key]:event.target.value===''?undefined:Number(event.target.value)});setError('');}}/></label>)}
  <label className="indicator-field"><span>Color</span><input aria-label="Indicator color" type="color" value={color} onChange={event=>setColor(event.target.value)}/></label>
  {error&&<p className="indicator-error" role="alert">{error}</p>}
  <div className="modal-actions"><button type="button" onClick={onClose}>Cancel</button><button className="primary" type="submit">OK</button></div>
 </form></div>,document.body);
}

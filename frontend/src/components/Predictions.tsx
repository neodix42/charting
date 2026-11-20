import {forwardRef,useEffect,useImperativeHandle,useRef,useState,type KeyboardEvent} from 'react';
import type {PredictionsBackend} from '../workspace/predictions';
import {errorText} from '../types';
import {LLMPredictions,type LLMPredictionsHandle} from './LLMPredictions';
import {PredictionStatistics} from './PredictionStatistics';
import './Predictions.css';

export interface PredictionsHandle {flush():Promise<void>}
type Section='llm'|'statistics';
const sections:Section[]=['llm','statistics'];

export const Predictions=forwardRef<PredictionsHandle,{api:PredictionsBackend;visible:boolean;suspended?:boolean}>(function Predictions({api,visible,suspended=false},ref){
 const [section,setSection]=useState<Section>('llm'),[error,setError]=useState('');
 const llm=useRef<LLMPredictionsHandle>(null),tabs=useRef<Array<HTMLButtonElement|null>>([]),mounted=useRef(false),navigation=useRef(0);
 useEffect(()=>{mounted.current=true;return()=>{mounted.current=false;navigation.current++;};},[]);
 useImperativeHandle(ref,()=>({flush:async()=>{await llm.current?.flush();}}),[]);
 const select=async(next:Section,focus=false)=>{
  const request=++navigation.current;
  setError('');
  try{
   await llm.current?.flush();
   if(!mounted.current||request!==navigation.current)return;
   setSection(next);
   if(focus)tabs.current[sections.indexOf(next)]?.focus();
  }catch(reason){if(mounted.current&&request===navigation.current)setError(`Could not save before switching prediction sections: ${errorText(reason)}`);}
 };
 const navigate=(event:KeyboardEvent<HTMLButtonElement>,index:number)=>{
  const next=event.key==='Home'?0:event.key==='End'?sections.length-1:event.key==='ArrowRight'?(index+1)%sections.length:event.key==='ArrowLeft'?(index+sections.length-1)%sections.length:-1;
  if(next<0)return;
  event.preventDefault();void select(sections[next],true);
 };
 return <section hidden={!visible} className="predictions" role="tabpanel" id="predictions-panel" aria-labelledby="predictions-tab">
  <header className="predictions-heading"><div><span className="analysis-eyebrow">LOCAL PRICE FORECASTS</span><h1>Predictions</h1><p>Explore possible future closing prices using your downloaded historical data.</p></div><div className="prediction-subtabs" role="tablist" aria-label="Prediction sections">{sections.map((value,index)=><button key={value} ref={element=>{tabs.current[index]=element;}} id={`prediction-${value}-tab`} type="button" role="tab" aria-controls={`prediction-${value}-panel`} aria-selected={section===value} tabIndex={section===value?0:-1} disabled={suspended} onClick={()=>void select(value)} onKeyDown={event=>navigate(event,index)}>{value==='llm'?'LLM':'Statistics'}</button>)}</div></header>
  {error&&<p className="screener-error" role="alert">{error}</p>}
  <LLMPredictions ref={llm} api={api} visible={visible&&section==='llm'} suspended={suspended}/>
  <div id="prediction-statistics-panel" className="prediction-section" role="tabpanel" aria-labelledby="prediction-statistics-tab" hidden={section!=='statistics'}><PredictionStatistics api={api} visible={visible&&section==='statistics'} suspended={suspended}/></div>
 </section>;
});

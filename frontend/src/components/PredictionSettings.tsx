import {forwardRef,useEffect,useImperativeHandle,useRef,useState} from 'react';
import type {PredictionsBackend,PredictionInfo,PredictionSettings as Settings} from '../workspace/predictions';
import {errorText} from '../types';

export interface PredictionSettingsHandle {flush():Promise<void>}
export const PredictionSettings=forwardRef<PredictionSettingsHandle,{api:PredictionsBackend;visible:boolean}>(function PredictionSettings({api,visible},ref){
 const [info,setInfo]=useState<PredictionInfo|null>(null),[draft,setDraft]=useState<Settings>({model:'Datadog/Toto-2.0-313m',modelDirectory:''}),[loading,setLoading]=useState(false),[applying,setApplying]=useState(false),[choosing,setChoosing]=useState(false),[error,setError]=useState(''),[retry,setRetry]=useState(0);
 const alive=useRef(false),pending=useRef<Promise<void>|null>(null),choosingNow=useRef(false);
 useEffect(()=>{alive.current=true;return()=>{alive.current=false;};},[]);
 useImperativeHandle(ref,()=>({flush:async()=>{await pending.current;}}),[]);
 useEffect(()=>{if(!visible||info)return;let current=true;setLoading(true);setError('');void api.GetPredictions().then(next=>{if(current){setInfo(next);setDraft(next.settings);setLoading(false);}}).catch(reason=>{if(current){setError(errorText(reason));setLoading(false);}});return()=>{current=false;};},[api,visible,info,retry]);
 const dirty=!!info&&(draft.model!==info.settings.model||draft.modelDirectory.trim()!==info.settings.modelDirectory);
 const apply=()=>{if(!info||!dirty||pending.current||choosingNow.current)return;setApplying(true);setError('');const operation=Promise.resolve().then(()=>api.ConfigurePredictions({...draft,modelDirectory:draft.modelDirectory.trim()})).then(next=>{if(alive.current){setInfo(next);setDraft(next.settings);}}).catch(reason=>{if(alive.current)setError(errorText(reason));throw reason;}).finally(()=>{pending.current=null;if(alive.current)setApplying(false);});pending.current=operation;void operation.catch(()=>{});};
 const choose=async()=>{if(pending.current||choosingNow.current)return;choosingNow.current=true;setChoosing(true);setError('');try{const directory=await api.ChoosePredictionModelDirectory();if(alive.current&&directory)setDraft(value=>({...value,modelDirectory:directory}));}catch(reason){if(alive.current)setError(errorText(reason));}finally{choosingNow.current=false;if(alive.current)setChoosing(false);}};
 return <form hidden={!visible} className="settings-section prediction-settings" aria-label="Prediction model settings" onSubmit={event=>{event.preventDefault();apply();}}>
  <h3>Prediction model</h3><p>Models run locally on downloaded closing prices. Opening Predictions prepares the selected model and shows download progress.</p>
  {loading&&<p role="status">Loading model settings…</p>}
  <label className="setting-row"><span><strong>Model</strong><small>Choose the model used for new forecasts.</small></span><select aria-label="Prediction model" value={draft.model} disabled={!info||applying||choosing} onChange={event=>setDraft(value=>({...value,model:event.target.value}))}>{(info?.models??[{id:'Datadog/Toto-2.0-313m',name:'Toto 2.0 · 313M'}]).map(model=><option key={model.id} value={model.id}>{model.name}</option>)}</select></label>
  <label className="candle-storage-path">Model folder<input aria-label="Prediction model folder" value={draft.modelDirectory} placeholder={info?.defaultModelDirectory} disabled={!info||applying||choosing} onChange={event=>setDraft(value=>({...value,modelDirectory:event.target.value}))}/></label>
  {info&&<p className="muted" role="status" aria-label="Applied prediction model">Applied: {info.settings.model}<br/>Model folder: {info.settings.modelDirectory||info.defaultModelDirectory}{dirty?' · Unapplied changes':''}</p>}
  {info&&<p className="muted">Default model folder: {info.defaultModelDirectory}</p>}
  <p className="muted">Leave the folder empty to use the default. Settings are saved on this computer. Finish or cancel an active prediction before changing its model or folder.</p>
  {error&&<p className="candle-storage-error" role="alert">{error}{!info&&<button type="button" onClick={()=>setRetry(value=>value+1)}>Retry model settings</button>}</p>}
  <div className="candle-storage-actions"><button type="button" disabled={!info||applying||choosing} onClick={()=>void choose()}>{choosing?'Choosing…':'Choose folder…'}</button><button type="submit" aria-label="Apply prediction settings" disabled={!dirty||applying||choosing}>{applying?'Applying…':'Apply'}</button></div>
 </form>;
});

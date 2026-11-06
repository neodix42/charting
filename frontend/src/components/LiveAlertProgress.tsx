import type {LiveAlertCheck} from '../workspace/screenerLiveAlerts';
import {liveAlertCheckProgress} from '../workspace/screenerLiveAlerts';

/** Runtime queue progress stays separate from the last actual check metadata. */
export function LiveAlertProgress({check,name}:{check:LiveAlertCheck;name:string}){
 const {state,phase,value}=liveAlertCheckProgress(check),active=state==='running'||state==='queued',showBar=active||value!==undefined;
 const percent=value===undefined?'':`${Math.round(value)}%`,description=[phase,check.currentAsset,percent].filter(Boolean).join(' · ');
 return <div className={`live-alert-row-progress ${state}`}><span className="live-alert-progress-phase">{phase}{percent&&<span className="live-alert-progress-percent">{percent}</span>}</span>{showBar&&<progress aria-label={`Progress for ${name}`} aria-valuetext={description} max={100} value={value}/>} {check.currentAsset&&<span className="live-alert-progress-asset">{check.currentAsset}</span>}{check.progressMode&&<small>{check.progressMode==='manual'?'Manual check':'Scheduled check'}</small>}</div>;
}

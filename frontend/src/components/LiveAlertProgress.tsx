import type {LiveAlertCheck} from '../workspace/screenerLiveAlerts';
import {liveAlertAssetCounts,liveAlertCheckProgress} from '../workspace/screenerLiveAlerts';

/** Runtime queue progress stays separate from the last actual check metadata. */
export function LiveAlertProgress({check,name}:{check:LiveAlertCheck;name:string}){
 const {state,phase,value}=liveAlertCheckProgress(check),active=state==='running'||state==='queued',showBar=active||value!==undefined;
 const percent=value===undefined?'':`${Number(value.toFixed(1))}%`,description=[phase,check.currentAsset,percent].filter(Boolean).join(' · ');
 const labels:Record<string,string>={never:'Not checked yet',queued:'Queued',running:'Running',completed:'Completed',failed:'Failed',cancelled:'Cancelled',skipped:'Skipped'};
 const counts=liveAlertAssetCounts(check);
 return <div className={`live-alert-row-progress ${state}`}><span className="live-alert-progress-phase">{labels[state]??state}<span className="live-alert-progress-percent">{percent||'—'}</span></span><progress className={!showBar?'live-alert-progress-empty':undefined} aria-hidden={!showBar} aria-label={`Progress for ${name}`} aria-valuetext={description} max={100} value={value}/><small className="live-alert-progress-counts" title={counts}>{counts||'\u00a0'}</small><small>{check.progressMode?(check.progressMode==='manual'?'Manual check':'Scheduled check'):'\u00a0'}</small></div>;
}

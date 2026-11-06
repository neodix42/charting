import {forwardRef,useEffect,useImperativeHandle,useRef,useState} from 'react';
import type {Backend} from '../types';
import {errorText} from '../types';
import type {SavedAnalysisCondition,SavedConditionRename} from '../workspace/screenerAnalysis';
import {analysisDate,profitabilityLabel} from '../workspace/screenerAnalysis';
import type {LiveAlertAssetRef,LiveAlertDefinition,LiveAlertsConfig,LiveAlertsInfo} from '../workspace/screenerLiveAlerts';
import {initialLiveAlertsConfig,liveAlertBarClose,liveAlertPeriodLabel,liveAlertDefinitions,orderedLiveAlertChecks,configWithLiveAlerts,renameConditionAlerts,liveAlertSourceLabel,liveAlertErrorText,LiveAlertsConfigWriter} from '../workspace/screenerLiveAlerts';
import './ScreenerLiveAlerts.css';
import {LiveAlertError} from './LiveAlertError';
import {LiveAlertCheckError} from './LiveAlertCheckError';
import {LiveAlertEditor} from './LiveAlertEditor';
import {LiveAlertAssets} from './LiveAlertAssets';
import {withLiveAlertAssets} from '../workspace/liveAlertAssets';
import {LiveAlertProgress} from './LiveAlertProgress';

export interface ScreenerLiveAlertsHandle {flush():Promise<void>;conditionRenamed(rename:SavedConditionRename):Promise<void>}
const outcomeLabels={sent:'Sent',failed:'Failed',claimed:'Delivery pending',uncertain:'Delivery uncertain — it may have arrived; no automatic retry'};
const checkLabels={never:'Not checked yet',running:'Running',completed:'Completed',failed:'Failed',cancelled:'Cancelled'};
const preserveStopInfo=(next:LiveAlertsInfo,previous:LiveAlertsInfo|null)=>next.savedConditionsRevision===0&&!next.conditionScopes.length&&previous?{...next,conditionScopes:previous.conditionScopes,assets:previous.assets,savedConditionsRevision:previous.savedConditionsRevision}:next;
export const ScreenerLiveAlerts=forwardRef<ScreenerLiveAlertsHandle,{api:Backend;visible:boolean;renamedCondition?:SavedConditionRename}>(function ScreenerLiveAlerts({api,visible,renamedCondition},ref){
 const [info,setInfo]=useState<LiveAlertsInfo|null>(null),[config,setConfig]=useState(initialLiveAlertsConfig),[conditions,setConditions]=useState<SavedAnalysisCondition[]>([]);
 const [editor,setEditor]=useState<{entry:LiveAlertDefinition|null}|null>(null),[stopping,setStopping]=useState(false),[checkPending,setCheckPending]=useState(false),[assets,setAssets]=useState<{id:string;origin:HTMLElement}|null>(null);
 useEffect(()=>{if(!visible)setAssets(null);},[visible]);
 const allEnabled=useRef<HTMLInputElement>(null);
 const [ready,setReady]=useState(false),[readError,setReadError]=useState(''),[writeError,setWriteError]=useState(''),[actionError,setActionError]=useState(''),[busy,setBusy]=useState(false),[retry,setRetry]=useState(0),[notice,setNotice]=useState('');
 const alive=useRef(false),lifetime=useRef(0),epoch=useRef(0),initialized=useRef(false),draft=useRef(config),action=useRef<Promise<void>|null>(null),writer=useRef<LiveAlertsConfigWriter|null>(null);
 if(!writer.current)writer.current=new LiveAlertsConfigWriter(async value=>{
  const session=lifetime.current;epoch.current++;
  const next=await api.ConfigureScreenerLiveAlerts(value);
  if(alive.current&&session===lifetime.current){setInfo(previous=>preserveStopInfo(next,previous));setWriteError('');}
  return next;
 },error=>{if(alive.current)setWriteError(errorText(error));});
 useImperativeHandle(ref,()=>({
  flush:async()=>{await writer.current!.flush();if(action.current)await action.current;},
  conditionRenamed:async rename=>{
   await writer.current!.flush();if(action.current)await action.current;
   const session=lifetime.current,loaded=initialized.current?null:await api.GetScreenerLiveAlerts();
   if(action.current)await action.current;
   if(!alive.current||session!==lifetime.current)return;
   const current=initialized.current?draft.current:loaded!.config,next=renameConditionAlerts(current,rename.condition.id,rename.previousName,rename.condition.name);
   if(next===current)return;
   draft.current=next;setConfig(next);setNotice('');setWriteError('');writer.current!.edit(next);await writer.current!.flush();
  },
 }),[]);
 useEffect(()=>{alive.current=true;lifetime.current++;return()=>{alive.current=false;lifetime.current++;};},[]);
 useEffect(()=>{
  if(!renamedCondition)return;
  epoch.current++;
  setConditions(current=>current.map(condition=>condition.id===renamedCondition.condition.id?renamedCondition.condition:condition));
 },[renamedCondition]);
 useEffect(()=>{
  if(!visible)return;
  let active=true,needConditions=true,savedRevision:number|undefined,timer:ReturnType<typeof setTimeout>|undefined;
  const read=async()=>{
   const version=epoch.current;
   try{
    if(!action.current&&!writer.current!.busy){
     const next=await api.GetScreenerLiveAlerts(),saved=needConditions||next.savedConditionsRevision!==savedRevision?await api.GetScreenerSavedConditions():null;
     if(active&&version===epoch.current){
      setInfo(next);setReadError('');if(saved){setConditions(saved);needConditions=false;savedRevision=next.savedConditionsRevision;}
      draft.current=structuredClone(next.config);setConfig(draft.current);
      if(!initialized.current){initialized.current=true;setReady(true);}
     }
    }
   }catch(error){if(active&&version===epoch.current)setReadError(errorText(error));}
   finally{if(active)timer=setTimeout(()=>void read(),1500);}
  };
  void read();return()=>{active=false;if(timer)clearTimeout(timer);};
 },[api,visible,retry,renamedCondition]);
 const change=(patch:Partial<LiveAlertsConfig>)=>{
  if(!initialized.current||action.current)return;
  const next={...draft.current,...patch};draft.current=next;setConfig(next);setNotice('');setWriteError('');writer.current!.edit(next);
 };
 const run=(operation:()=>Promise<LiveAlertsInfo|void>,message='')=>{
  if(action.current)return;
  const session=lifetime.current;epoch.current++;setBusy(true);setActionError('');setNotice('');
  let pending!:Promise<void>;
  pending=(async()=>{
   try{
    await writer.current!.flush();const next=await operation();
    if(alive.current&&session===lifetime.current){if(next){setInfo(previous=>preserveStopInfo(next,previous));draft.current=structuredClone(next.config);setConfig(draft.current);}setNotice(message);}
   }catch(error){if(alive.current&&session===lifetime.current)setActionError(errorText(error));throw error;}
   finally{if(action.current===pending)action.current=null;if(alive.current&&session===lifetime.current){setBusy(false);setStopping(false);setCheckPending(false);}}
  })();
  action.current=pending;void pending.catch(()=>{});
 };
 const enabled=config.enabled,stoppingNow=stopping||(!config.enabled&&!!info?.running),locked=!ready||busy,entries=liveAlertDefinitions(config,conditions),scopes=info?.conditionScopes??[];
 const rules=entries.map(entry=>({entry,condition:conditions.find(condition=>condition.id===entry.conditionId),scope:scopes.find(scope=>(scope.alertId??scope.conditionId)===entry.id&&(scope.dataSource??'dukascopy')===entry.dataSource&&scope.period===entry.period&&(!scope.savedConditionId||scope.savedConditionId===entry.conditionId))}));
 const assetsRule=assets?rules.find(rule=>rule.entry.id===assets.id):undefined;
 useEffect(()=>{if(ready&&assets&&!assetsRule)setAssets(null);},[ready,assets?.id,assetsRule?.entry.id]);
 const selectedRules=rules.filter(rule=>rule.entry.enabled),checks=orderedLiveAlertChecks(entries,info?.checks??[]).map(check=>checkPending&&entries.some(entry=>entry.id===(check.alertId??check.conditionId)&&entry.enabled)?{...check,progress:0,progressState:'queued' as const,progressMode:'manual' as const,phase:'Queued',currentAsset:''}:check);
 const checkInProgress=checks.some(check=>check.progressState==='queued'||check.progressState==='running');
 const invalid=!selectedRules.length?'Enable at least one alert.':selectedRules.find(rule=>!rule.condition)?'Choose an available saved condition for every enabled alert.':selectedRules.find(rule=>rule.scope?.error)?.scope?.error??(selectedRules.some(rule=>rule.entry.dataSource==='dukascopy'&&!rule.scope?.targets.length)?'Run Analysis and save matching assets for each enabled Dukascopy alert.':'');
 useEffect(()=>{if(allEnabled.current)allEnabled.current.indeterminate=entries.some(entry=>entry.enabled)&&entries.some(entry=>!entry.enabled);},[entries]);
 const changeEntries=(next:LiveAlertDefinition[])=>change(configWithLiveAlerts(draft.current,next));
 const editEntry=(entry:LiveAlertDefinition)=>{const current=liveAlertDefinitions(draft.current,conditions);changeEntries(current.some(row=>row.id===entry.id)?current.map(row=>row.id===entry.id?entry:row):[...current,entry]);setEditor(null);};
 const saveAssets=(entry:LiveAlertDefinition,selection:LiveAlertAssetRef[]|undefined)=>{
  const current=liveAlertDefinitions(draft.current,conditions),latest=current.find(row=>row.id===entry.id);
  if(!latest||latest.conditionId!==entry.conditionId||latest.dataSource!==entry.dataSource||latest.period!==entry.period)return;
  changeEntries(current.map(row=>row.id===entry.id?withLiveAlertAssets(row,selection):row));setAssets(null);
 };
 const rowErrors=new Set(checks.flatMap(check=>liveAlertErrorText(check.error).split('\n')).filter(Boolean));
 const unassignedError=liveAlertErrorText(info?.error??'').split('\n').filter(line=>!rowErrors.has(line)).join('\n');
 const configurationError=actionError||writeError||readError||unassignedError;
 const stop=()=>{setStopping(true);run(async()=>{try{return await api.ConfigureScreenerLiveAlerts({...draft.current,enabled:false});}finally{setStopping(false);}});};
 return <section className="live-alerts" aria-label="Live Alerts configuration">
  <header className="live-alerts-heading"><div><span className="analysis-eyebrow">CLOSED-BAR SIGNALS</span><h2>Live Alerts</h2><p>Create named alerts from saved conditions, with independent periods and market sources.</p></div><span className={`live-alerts-state${enabled?' enabled':''}`}>{stoppingNow?'Stopping...':enabled?'Enabled':'Stopped'}</span></header>

  {!ready&&!readError&&<p role="status" className="live-alerts-note">Loading saved alert settings…</p>}
  <section className="live-alerts-card" aria-label="Configured alerts"><div className="live-alerts-card-heading"><h3>Alerts</h3><button type="button" disabled={locked||!conditions.length} onClick={()=>setEditor({entry:null})}>Add alert</button></div><p>Each alert has its own name, saved condition, period and source. The same condition can be used by several alerts.</p>
   <div className="live-alerts-definition-scroll"><table className="live-alerts-definition-table" aria-label="Configured alerts"><thead><tr><th scope="col"><input ref={allEnabled} type="checkbox" aria-label="Enable all alerts" disabled={locked||!entries.length} checked={entries.length>0&&entries.every(entry=>entry.enabled)} onChange={event=>changeEntries(entries.map(entry=>({...entry,enabled:event.target.checked})))}/></th><th scope="col">Alert name</th><th scope="col">Saved condition</th><th scope="col">Period</th><th scope="col">Data source</th><th scope="col">Assets</th><th scope="col">Actions</th></tr></thead><tbody>{rules.map(({entry,condition,scope})=><tr key={entry.id} data-alert-id={entry.id}><td><input type="checkbox" aria-label={`Enable alert ${entry.name}`} disabled={locked} checked={entry.enabled} onChange={event=>changeEntries(entries.map(row=>row.id===entry.id?{...row,enabled:event.target.checked}:row))}/></td><th scope="row">{entry.name}</th><td>{condition?.name??'Unavailable saved condition'}</td><td>{liveAlertPeriodLabel(entry.period)}</td><td>{liveAlertSourceLabel(entry.dataSource)}</td><td><button type="button" aria-label={`View assets for ${entry.name}`} aria-haspopup="dialog" onClick={event=>setAssets({id:entry.id,origin:event.currentTarget})}>Assets{entry.dataSource==='dukascopy'&&scope?` (${scope.targets.length})`:''}</button></td><td><div className="live-alerts-row-actions"><button type="button" aria-label={`Edit alert ${entry.name}`} disabled={locked} onClick={()=>setEditor({entry})}>Edit</button><button type="button" className="danger" aria-label={`Remove alert ${entry.name}`} disabled={locked} onClick={()=>changeEntries(entries.filter(row=>row.id!==entry.id))}>Remove</button></div></td></tr>)}</tbody></table></div>
   {configurationError&&<div className="live-alerts-configuration-error" role="alert"><LiveAlertError label="Live Alerts" message={configurationError}/><button type="button" disabled={busy} onClick={()=>{setRetry(value=>value+1);if(writeError)run(async()=>{await writer.current!.flush();});}}>Retry</button></div>}
   {ready&&!entries.length&&<p>Add an alert to start monitoring saved conditions.</p>}{ready&&!conditions.length&&<p>Create a named condition in Analysis using Save as new.</p>}
   <p>Hourly checks run after UTC hour closes, daily checks after 00:00 UTC, and weekly checks after Monday 00:00 UTC. Row checkboxes control individual alerts; Start / Stop controls delivery for the whole list.</p>
  </section>
  <div className="live-alerts-controls"><button type="button" disabled={locked} onClick={()=>run(async()=>{await writer.current!.flush();},'Alert configuration saved.')}>Save config</button>{enabled?<button type="button" className="danger" disabled={busy} onClick={stop}>{stoppingNow?'Stopping...':'Stop alerts'}</button>:stoppingNow?<button type="button" disabled>Stopping...</button>:<button type="button" className="primary" disabled={!ready||busy||info?.running||!!invalid} onClick={()=>run(()=>api.ConfigureScreenerLiveAlerts({...draft.current,enabled:true}),'Live Alerts started.')}>Start alerts</button>}<button type="button" disabled={!enabled||busy||info?.running||checkInProgress||!selectedRules.length} onClick={()=>{setCheckPending(true);run(()=>api.CheckScreenerLiveAlerts());}}>Check now</button>{stoppingNow&&<span role="status">Stopping...</span>}{notice&&<span role="status">{notice}</span>}</div>
  <div className="live-alerts-note">{!enabled&&invalid&&<p>{invalid}</p>}<p>Configuration changes cancel the current check and apply to the next one. CryptoDesk must remain running; enabled alerts resume when you restart it.</p><p>The indicative entry is the latest closed bar’s close. Stop Loss and Take profit use the saved condition’s settings. Prices are signals, not executed trades.</p></div>
  <section className="live-alerts-card live-alerts-status" aria-label="Live alert status">
   <div className="live-alerts-checks"><div className="live-alerts-card-heading"><h3>Check history</h3><span>{info?.sent??0} alerts sent</span></div><p>Enabled alerts are checked sequentially in the order shown above. Latest check attempt per alert. Sent alerts counts confirmed deliveries since check tracking began.</p><div className="live-alerts-check-table-scroll"><table aria-label="Alert check history"><thead><tr><th scope="col">Alert name</th><th scope="col">Progress</th><th scope="col">Number of assets scanned</th><th scope="col">Last checked</th><th scope="col">Next check</th><th scope="col">Sent alerts</th></tr></thead><tbody>{checks.map(check=>{const entry=entries.find(entry=>entry.id===(check.alertId??check.conditionId)),name=entry?.name??check.name;return <tr key={check.alertId??check.conditionId}><th scope="row">{name}</th><td className="live-alert-progress-cell"><LiveAlertProgress check={check} name={name}/></td><td>{check.status==='never'?'—':check.assetsScanned.toLocaleString('en-US')}</td><td>{check.lastChecked?analysisDate(check.lastChecked):'Not checked yet'}{check.status!=='never'&&(check.error?<LiveAlertCheckError visible={visible} label={`Check for ${name}`} message={check.error} status={`${check.manual?'Manual':'Scheduled'} · ${check.status==='completed'&&check.error?'Completed with errors':checkLabels[check.status]}`}/>:<small>{check.manual?'Manual':'Scheduled'} · {checkLabels[check.status]}</small>)}</td><td>{check.nextCheck?analysisDate(check.nextCheck):enabled&&entry?.enabled?'—':'Stopped'}{check.timingHint&&<small>{check.timingHint}</small>}</td><td>{check.sentAlerts.toLocaleString('en-US')}</td></tr>;})}</tbody></table></div>{ready&&!checks.length&&<p>Add alerts to see their check history.</p>}</div>
   {!!info?.recent?.length&&<div className="live-alerts-recent"><h3>Recent alerts</h3><ul>{info.recent.map((outcome,index)=><li key={`${outcome.dataSource??'dukascopy'}-${outcome.market??''}-${outcome.symbol}-${outcome.timeframe}-${outcome.time}-${outcome.alertId??outcome.conditionId??outcome.conditionName}-${index}`} className={outcome.status}><strong>{outcome.dataSource&&outcome.dataSource!=='dukascopy'?outcome.symbol:outcome.symbol.toUpperCase()} — {outcome.assetName} · {outcome.timeframe.toUpperCase()} · {outcome.alertName??outcome.conditionName}{outcome.dataSource&&outcome.dataSource!=='dukascopy'?` · ${liveAlertSourceLabel(outcome.dataSource)}${outcome.market?` ${outcome.market==='perpetual'?'Perpetual':outcome.market==='spot'?'Spot':outcome.market}`:''}`:''}</strong><span>{outcomeLabels[outcome.status]} · Closed {analysisDate(liveAlertBarClose(outcome.time,outcome.timeframe))}</span>{outcome.profitability&&<p>{profitabilityLabel(outcome.profitability)}</p>}{outcome.message&&<p className={outcome.status==='failed'||outcome.status==='uncertain'?'live-alerts-delivery-error':undefined}>{liveAlertErrorText(outcome.message)}</p>}</li>)}</ul></div>}
  </section>
  {visible&&assets&&assetsRule&&<LiveAlertAssets key={assetsRule.entry.id} api={api} entry={assetsRule.entry} condition={assetsRule.condition} scope={assetsRule.scope} origin={assets.origin} disabled={locked} onSave={selection=>saveAssets(assetsRule.entry,selection)} onClose={()=>setAssets(null)}/>}
  {visible&&editor&&<LiveAlertEditor entry={editor.entry} conditions={conditions} onSave={editEntry} onClose={()=>setEditor(null)}/>}
 </section>;
});

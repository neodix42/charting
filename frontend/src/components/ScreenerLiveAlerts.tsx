import {forwardRef,useEffect,useImperativeHandle,useRef,useState} from 'react';
import type {Backend} from '../types';
import {errorText} from '../types';
import type {SavedAnalysisCondition} from '../workspace/screenerAnalysis';
import {analysisDate,profitabilityLabel} from '../workspace/screenerAnalysis';
import type {LiveAlertsConfig,LiveAlertsInfo} from '../workspace/screenerLiveAlerts';
import {initialLiveAlertsConfig,LiveAlertsConfigWriter} from '../workspace/screenerLiveAlerts';
import './ScreenerLiveAlerts.css';

export interface ScreenerLiveAlertsHandle {flush():Promise<void>}
const outcomeLabels={sent:'Sent',failed:'Failed',claimed:'Delivery pending',uncertain:'Delivery uncertain — it may have arrived; no automatic retry'};
const checkLabels={never:'Not checked yet',running:'Running',completed:'Completed',failed:'Failed',cancelled:'Cancelled'};
export const ScreenerLiveAlerts=forwardRef<ScreenerLiveAlertsHandle,{api:Backend;visible:boolean}>(function ScreenerLiveAlerts({api,visible},ref){
 const [info,setInfo]=useState<LiveAlertsInfo|null>(null),[config,setConfig]=useState(initialLiveAlertsConfig),[conditions,setConditions]=useState<SavedAnalysisCondition[]>([]);
 const [ready,setReady]=useState(false),[readError,setReadError]=useState(''),[writeError,setWriteError]=useState(''),[actionError,setActionError]=useState(''),[busy,setBusy]=useState(false),[retry,setRetry]=useState(0),[notice,setNotice]=useState('');
 const alive=useRef(false),lifetime=useRef(0),epoch=useRef(0),initialized=useRef(false),draft=useRef(config),action=useRef<Promise<void>|null>(null),writer=useRef<LiveAlertsConfigWriter|null>(null);
 if(!writer.current)writer.current=new LiveAlertsConfigWriter(async value=>{
  const session=lifetime.current;epoch.current++;
  const next=await api.ConfigureScreenerLiveAlerts(value);
  if(alive.current&&session===lifetime.current){setInfo(next);setWriteError('');}
  return next;
 },error=>{if(alive.current)setWriteError(errorText(error));});
 useImperativeHandle(ref,()=>({flush:async()=>{await writer.current!.flush();if(action.current)await action.current;}}),[]);
 useEffect(()=>{alive.current=true;lifetime.current++;return()=>{alive.current=false;lifetime.current++;};},[]);
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
 },[api,visible,retry]);
 const change=(patch:Partial<LiveAlertsConfig>)=>{
  if(!initialized.current||draft.current.enabled||action.current)return;
  const next={...draft.current,...patch,enabled:false};draft.current=next;setConfig(next);setNotice('');setWriteError('');writer.current!.edit(next);
 };
 const run=(operation:()=>Promise<LiveAlertsInfo|void>,message='')=>{
  if(action.current)return;
  const session=lifetime.current;epoch.current++;setBusy(true);setActionError('');setNotice('');
  let pending!:Promise<void>;
  pending=(async()=>{
   try{
    await writer.current!.flush();const next=await operation();
    if(alive.current&&session===lifetime.current){if(next){setInfo(next);draft.current=structuredClone(next.config);setConfig(draft.current);}setNotice(message);}
   }catch(error){if(alive.current&&session===lifetime.current)setActionError(errorText(error));throw error;}
   finally{if(action.current===pending)action.current=null;if(alive.current&&session===lifetime.current)setBusy(false);}
  })();
  action.current=pending;void pending.catch(()=>{});
 };
 const enabled=info?.config.enabled??false,locked=!ready||busy||enabled||!!info?.running;
 const selectedConditions=new Set(config.conditionIds),scopes=info?.conditionScopes??[],currentConditions=conditions.filter(condition=>scopes.some(scope=>scope.conditionId===condition.id));
 const selectedRules=currentConditions.filter(condition=>selectedConditions.has(condition.id)).map(condition=>({id:condition.id,condition,scope:scopes.find(scope=>scope.conditionId===condition.id)}));
 const checks=(info?.checks??[]).filter(check=>selectedRules.some(rule=>rule.id===check.conditionId));
 const invalid=!selectedRules.length?'Select at least one saved condition.':selectedRules.find(rule=>rule.scope?.error)?.scope?.error??(selectedRules.some(rule=>!rule.scope?.targets.length)?'Run Analysis and save matching assets for every selected condition before starting alerts.':'');
 const toggleCondition=(id:string,checked:boolean)=>{const next=new Set(draft.current.conditionIds.filter(value=>currentConditions.some(condition=>condition.id===value)));if(checked)next.add(id);else next.delete(id);change({conditionIds:[...next]});};
 const periodLabel=(period:string)=>period==='d1'?'Daily (D1)':period==='h1'?'Hourly (H1)':'All analyzed periods (D1 / H1)';
 return <section className="live-alerts" aria-label="Live Alerts configuration">
  <header className="live-alerts-heading"><div><span className="analysis-eyebrow">CLOSED-BAR SIGNALS</span><h2>Live Alerts</h2><p>Send matching saved conditions to your Telegram recipients on daily and hourly closes.</p></div><span className={`live-alerts-state${enabled?' enabled':''}`}>{enabled?'Enabled':'Stopped'}</span></header>
  {(readError||writeError||actionError||info?.error)&&<div className="live-alerts-error" role="alert">{actionError||writeError||readError||info?.error}<button type="button" disabled={busy} onClick={()=>{setRetry(value=>value+1);if(writeError)run(async()=>{await writer.current!.flush();});}}>Retry</button></div>}
  {!ready&&!readError&&<p role="status" className="live-alerts-note">Loading saved alert settings…</p>}
  <div className="live-alerts-columns">
   <section className="live-alerts-card" aria-label="Alert conditions and periods"><h3>Saved conditions</h3><p>Each selected condition inherits its saved Analysis period, matching assets and profitability filter.</p>
    <fieldset disabled={locked} className="live-alerts-condition-selection"><legend className="sr-only">Saved conditions for alerts</legend>
     <table className="live-alerts-condition-table" aria-label="Saved conditions and periods"><thead><tr><th scope="col">Saved condition</th><th scope="col">Period from Analysis</th></tr></thead><tbody>
      {currentConditions.map(condition=><tr key={condition.id}><td><label><input type="checkbox" aria-label={`Alert condition ${condition.name}`} checked={selectedConditions.has(condition.id)} onChange={event=>toggleCondition(condition.id,event.target.checked)}/><span><strong>{condition.name}</strong><small>{condition.request.conditions.length} condition rows · Minimum profitability: {condition.request.profitabilityFilter??0}%</small></span></label></td><td>{periodLabel(scopes.find(scope=>scope.conditionId===condition.id)?.period??condition.request.period??'all')}</td></tr>)}
     </tbody></table>
    </fieldset>
    {ready&&!currentConditions.length&&<p>Create a named condition in Analysis using Save as new.</p>}
    <p>Hourly checks start on the next UTC hour. Daily checks start at the next 00:00 UTC. Only conditions whose assigned bar has closed are checked automatically.</p>
   </section>
   <section className="live-alerts-card live-alerts-assets" aria-label="Monitored assets"><div className="live-alerts-card-heading"><h3>Monitored assets</h3><span>{selectedRules.reduce((count,rule)=>count+(rule.scope?.targets.length??0),0)} condition / asset pairs</span></div>
    <p>These assets come from each condition’s saved Analysis results. Update and analyze the condition in Analysis to change its scope.</p>
    <div className="live-alerts-asset-list">{selectedRules.filter(rule=>rule.condition).map(rule=><section className="live-alerts-monitored-condition" aria-label={`Monitored assets for ${rule.condition!.name}`} key={rule.id}><h4>{rule.condition!.name} · {periodLabel(rule.scope?.period??rule.condition!.request.period??'all')}</h4>{!!rule.condition!.analysis?.failedFiles&&<p>Analysis had {rule.condition!.analysis!.failedFiles} failed files; monitoring uses its successfully analyzed matches.</p>}{rule.scope?.error&&<p className="live-alerts-scope-error">{rule.scope.error}</p>}{!!rule.scope?.targets.length&&<ul>{rule.scope.targets.map(target=><li key={`${target.symbol}-${target.period}`}><strong>{target.symbol.toUpperCase()} — {target.name}</strong><small>{target.period.toUpperCase()} · {target.category}</small>{target.profitability&&<small>{profitabilityLabel(target.profitability)}</small>}</li>)}</ul>}</section>)}
     {ready&&!selectedRules.length&&<p>Select saved conditions to see the assets they monitor.</p>}
    </div>
   </section>
  </div>
  <div className="live-alerts-controls"><button type="button" disabled={locked} onClick={()=>run(async()=>{await writer.current!.flush();},'Alert configuration saved.')}>Save config</button>{enabled?<button type="button" className="danger" disabled={busy} onClick={()=>run(()=>api.ConfigureScreenerLiveAlerts({...draft.current,enabled:false}),'Live Alerts stopped.')}>Stop alerts</button>:<button type="button" className="primary" disabled={!ready||busy||info?.running||!!invalid} onClick={()=>run(()=>api.ConfigureScreenerLiveAlerts({...draft.current,enabled:true}),'Live Alerts started.')}>Start alerts</button>}<button type="button" disabled={!enabled||busy||info?.running} onClick={()=>run(()=>api.CheckScreenerLiveAlerts())}>Check now</button>{notice&&<span role="status">{notice}</span>}</div>
  <div className="live-alerts-note">{!enabled&&invalid&&<p>{invalid}</p>}<p>{enabled?'Stop alerts to edit the configuration.':'Start alerts sends matching signals to the recipients configured in Settings → Telegram Bot.'} CryptoDesk must remain running; enabled alerts resume when you restart it.</p><p>Only saved matching D1 / H1 assets with downloaded histories are monitored. The indicative entry is the latest closed bar’s close; Stop Loss and Take profit use the saved condition’s simulation settings. Prices are signals, not executed trades.</p></div>
  <section className="live-alerts-card live-alerts-status" aria-label="Live alert status"><div className="live-alerts-card-heading"><h3>{info?.status||'Stopped'}</h3><span>{info?.sent??0} alerts sent</span></div>{info?.running&&<><p>{info.currentAsset||'Checking selected assets…'}</p><progress aria-label="Live alerts progress" value={Math.max(0,Math.min(100,info.progress||0))} max={100}/></>}<dl><div><dt>Last check</dt><dd>{info?.lastRun?analysisDate(info.lastRun):'Not checked yet'}</dd></div><div><dt>Next check</dt><dd>{info?.nextRun?analysisDate(info.nextRun):'—'}</dd></div></dl>
   <div className="live-alerts-checks"><h3>Check history</h3><p>Latest check attempt per selected alert. Sent alerts counts confirmed deliveries since check tracking began.</p><div className="live-alerts-check-table-scroll"><table aria-label="Alert check history"><thead><tr><th scope="col">Alert name</th><th scope="col">Number of assets scanned</th><th scope="col">Last checked</th><th scope="col">Next check</th><th scope="col">Sent alerts</th></tr></thead><tbody>{checks.map(check=><tr key={check.conditionId}><th scope="row">{currentConditions.find(condition=>condition.id===check.conditionId)?.name??check.name}</th><td>{check.status==='never'?'—':check.assetsScanned.toLocaleString('en-US')}</td><td>{check.lastChecked?analysisDate(check.lastChecked):'Not checked yet'}{check.status!=='never'&&<small>{check.manual?'Manual':'Scheduled'} · {checkLabels[check.status]}</small>}{check.error&&<small className="live-alerts-scope-error">{check.error}</small>}</td><td>{check.nextCheck?analysisDate(check.nextCheck):enabled?'—':'Stopped'}{check.timingHint&&<small>{check.timingHint}</small>}</td><td>{check.sentAlerts.toLocaleString('en-US')}</td></tr>)}</tbody></table></div>{ready&&!checks.length&&<p>Select saved conditions to see their check history.</p>}</div>
   {!!info?.recent?.length&&<div className="live-alerts-recent"><h3>Recent alerts</h3><ul>{info.recent.map((outcome,index)=><li key={`${outcome.symbol}-${outcome.timeframe}-${outcome.time}-${outcome.conditionName}-${index}`} className={outcome.status}><strong>{outcome.symbol.toUpperCase()} — {outcome.assetName} · {outcome.timeframe.toUpperCase()} · {outcome.conditionName}</strong><span>{outcomeLabels[outcome.status]} · Closed {analysisDate(outcome.time+(outcome.timeframe==='d1'?86400000:3600000))}</span>{outcome.profitability&&<p>{profitabilityLabel(outcome.profitability)}</p>}{outcome.message&&<p>{outcome.message}</p>}</li>)}</ul></div>}
  </section>
 </section>;
});

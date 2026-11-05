import {forwardRef,useEffect,useImperativeHandle,useRef,useState} from 'react';
import type {Backend} from '../types';
import {errorText} from '../types';
import type {SavedAnalysisCondition} from '../workspace/screenerAnalysis';
import {analysisDate,profitabilityLabel} from '../workspace/screenerAnalysis';
import type {LiveAlertDataSource,LiveAlertsConfig,LiveAlertsInfo} from '../workspace/screenerLiveAlerts';
import {initialLiveAlertsConfig,liveAlertBarClose,liveAlertPeriodLabel,liveAlertConditionSource,liveAlertDataSources,liveAlertSourceLabel,withLiveAlertSource,liveAlertErrorText,LiveAlertsConfigWriter} from '../workspace/screenerLiveAlerts';
import './ScreenerLiveAlerts.css';
import {LiveAlertError} from './LiveAlertError';
import {LiveAlertCheckError} from './LiveAlertCheckError';

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
 const selectedConditions=new Set(config.conditionIds),scopes=info?.conditionScopes??[],currentConditions=conditions;
 const selectedRules=currentConditions.filter(condition=>selectedConditions.has(condition.id)).map(condition=>{const source=liveAlertConditionSource(config,condition.id);return{id:condition.id,condition,source,scope:scopes.find(scope=>scope.conditionId===condition.id&&(scope.dataSource??'dukascopy')===source)};});
 const checks=(info?.checks??[]).filter(check=>selectedRules.some(rule=>rule.id===check.conditionId));
 const invalid=!selectedRules.length?'Select at least one saved condition.':selectedRules.find(rule=>rule.scope?.error)?.scope?.error??(selectedRules.some(rule=>rule.source==='dukascopy'&&!rule.scope?.targets.length)?'Run Analysis and save matching assets for every selected condition before starting alerts.':'');
 const toggleCondition=(id:string,checked:boolean)=>{const next=new Set(draft.current.conditionIds.filter(value=>currentConditions.some(condition=>condition.id===value)));if(checked)next.add(id);else next.delete(id);change({conditionIds:[...next]});};
 return <section className="live-alerts" aria-label="Live Alerts configuration">
  <header className="live-alerts-heading"><div><span className="analysis-eyebrow">CLOSED-BAR SIGNALS</span><h2>Live Alerts</h2><p>Send matching saved conditions to your Telegram recipients on weekly, daily and hourly closes.</p></div><span className={`live-alerts-state${enabled?' enabled':''}`}>{enabled?'Enabled':'Stopped'}</span></header>
  {(readError||writeError||actionError||info?.error)&&<div className="live-alerts-error" role="alert"><LiveAlertError label="Live Alerts" message={actionError||writeError||readError||info?.error||''}/><button type="button" disabled={busy} onClick={()=>{setRetry(value=>value+1);if(writeError)run(async()=>{await writer.current!.flush();});}}>Retry</button></div>}
  {!ready&&!readError&&<p role="status" className="live-alerts-note">Loading saved alert settings…</p>}
  <div className="live-alerts-columns">
   <section className="live-alerts-card" aria-label="Alert conditions and periods"><h3>Saved conditions</h3><p>Each condition inherits its saved Analysis period. Dukascopy uses its analyzed matching assets and profitability filter; other sources monitor all their tradable Spot and Perpetual markets.</p>
    <fieldset disabled={locked} className="live-alerts-condition-selection"><legend className="sr-only">Saved conditions for alerts</legend>
     <table className="live-alerts-condition-table" aria-label="Saved conditions and periods"><thead><tr><th scope="col">Saved condition</th><th scope="col">Period from Analysis</th><th scope="col">Data source</th></tr></thead><tbody>
      {currentConditions.map(condition=><tr key={condition.id}><td><label><input type="checkbox" aria-label={`Alert condition ${condition.name}`} checked={selectedConditions.has(condition.id)} onChange={event=>toggleCondition(condition.id,event.target.checked)}/><span><strong>{condition.name}</strong><small>{condition.request.conditions.length} condition rows · {liveAlertConditionSource(config,condition.id)==='dukascopy'?`Minimum profitability: ${condition.request.profitabilityFilter??0}%`:'Historical profitability unavailable for this source'}</small></span></label></td><td>{liveAlertPeriodLabel(scopes.find(scope=>scope.conditionId===condition.id)?.period??condition.request.period??'all')}</td><td><select aria-label={`Data source for ${condition.name}`} value={liveAlertConditionSource(config,condition.id)} onChange={event=>change(withLiveAlertSource(draft.current,condition.id,event.target.value as LiveAlertDataSource))}>{liveAlertDataSources.map(({source,label})=><option value={source} key={source}>{label}</option>)}</select></td></tr>)}
     </tbody></table>
    </fieldset>
    {ready&&!currentConditions.length&&<p>Create a named condition in Analysis using Save as new.</p>}
    <p>Hourly checks start on the next UTC hour. Daily checks start at the next 00:00 UTC. Weekly checks start at the next Monday 00:00 UTC, when the previous weekly bar closes. Only conditions whose assigned bar has closed are checked automatically.</p>
   </section>
   <section className="live-alerts-card live-alerts-assets" aria-label="Monitored assets"><div className="live-alerts-card-heading"><h3>Monitored assets</h3><span>{selectedRules.some(rule=>rule.source!=='dukascopy')?'Public catalogs resolved at check time':`${selectedRules.reduce((count,rule)=>count+(rule.scope?.targets.length??0),0)} condition / asset pairs`}</span></div>
    <p>Dukascopy assets come from saved Analysis results. Public sources discover all tradable Spot and Perpetual markets before each check, then fetch and cache the bars required by the condition.</p>
    <div className="live-alerts-asset-list">{selectedRules.filter(rule=>rule.condition).map(rule=><section className="live-alerts-monitored-condition" aria-label={`Monitored assets for ${rule.condition!.name}`} key={rule.id}><h4>{rule.condition!.name} · {liveAlertPeriodLabel(rule.scope?.period??rule.condition!.request.period??'all')} · {liveAlertSourceLabel(rule.source)}</h4>{rule.source!=='dukascopy'&&<p>All tradable Spot and Perpetual assets on {liveAlertSourceLabel(rule.source)}. The catalog resolves at check time. Historical profitability is unavailable for these source markets; the Dukascopy Analysis profitability filter is not applied.</p>}{rule.source==='dukascopy'&&!!rule.condition!.analysis?.failedFiles&&<p>Analysis had {rule.condition!.analysis!.failedFiles} failed files; monitoring uses its successfully analyzed matches.</p>}{rule.scope?.error&&<LiveAlertError label={`Monitored assets for ${rule.condition!.name}`} message={rule.scope.error}/>}{!!rule.scope?.targets.length&&<ul>{rule.scope.targets.map(target=><li key={`${target.symbol}-${target.period}`}><strong>{rule.source==='dukascopy'?target.symbol.toUpperCase():target.symbol} — {target.name}</strong><small>{target.period.toUpperCase()} · {target.category}</small>{target.profitability&&<small>{profitabilityLabel(target.profitability)}</small>}</li>)}</ul>}</section>)}
     {ready&&!selectedRules.length&&<p>Select saved conditions to see the assets they monitor.</p>}
    </div>
   </section>
  </div>
  <div className="live-alerts-controls"><button type="button" disabled={locked} onClick={()=>run(async()=>{await writer.current!.flush();},'Alert configuration saved.')}>Save config</button>{enabled?<button type="button" className="danger" disabled={busy} onClick={()=>run(()=>api.ConfigureScreenerLiveAlerts({...draft.current,enabled:false}),'Live Alerts stopped.')}>Stop alerts</button>:<button type="button" className="primary" disabled={!ready||busy||info?.running||!!invalid} onClick={()=>run(()=>api.ConfigureScreenerLiveAlerts({...draft.current,enabled:true}),'Live Alerts started.')}>Start alerts</button>}<button type="button" disabled={!enabled||busy||info?.running} onClick={()=>run(()=>api.CheckScreenerLiveAlerts())}>Check now</button>{notice&&<span role="status">{notice}</span>}</div>
  <div className="live-alerts-note">{!enabled&&invalid&&<p>{invalid}</p>}<p>{enabled?'Stop alerts to edit the configuration.':'Start alerts sends matching signals to the recipients configured in Settings → Telegram Bot.'} CryptoDesk must remain running; enabled alerts resume when you restart it.</p><p>Checks use the saved W1 / D1 / H1 periods. Dukascopy requires downloaded matching histories; public sources fetch and cache required history when checked. The indicative entry is the latest closed bar’s close; Stop Loss and Take profit use the saved condition’s simulation settings. Prices are signals, not executed trades.</p></div>
  <section className="live-alerts-card live-alerts-status" aria-label="Live alert status"><div className="live-alerts-card-heading"><h3>{info?.status||'Stopped'}</h3><span>{info?.sent??0} alerts sent</span></div>{info?.running&&<><p>{info.currentAsset||'Checking selected assets…'}</p><progress aria-label="Live alerts progress" value={Math.max(0,Math.min(100,info.progress||0))} max={100}/></>}<dl><div><dt>Last check</dt><dd>{info?.lastRun?analysisDate(info.lastRun):'Not checked yet'}</dd></div><div><dt>Next check</dt><dd>{info?.nextRun?analysisDate(info.nextRun):'—'}</dd></div></dl>
   <div className="live-alerts-checks"><h3>Check history</h3><p>Latest check attempt per selected alert. Sent alerts counts confirmed deliveries since check tracking began.</p><div className="live-alerts-check-table-scroll"><table aria-label="Alert check history"><thead><tr><th scope="col">Alert name</th><th scope="col">Number of assets scanned</th><th scope="col">Last checked</th><th scope="col">Next check</th><th scope="col">Sent alerts</th></tr></thead><tbody>{checks.map(check=><tr key={check.conditionId}><th scope="row">{currentConditions.find(condition=>condition.id===check.conditionId)?.name??check.name}</th><td>{check.status==='never'?'—':check.assetsScanned.toLocaleString('en-US')}</td><td>{check.lastChecked?analysisDate(check.lastChecked):'Not checked yet'}{check.status!=='never'&&(check.error?<LiveAlertCheckError visible={visible} label={`Check for ${currentConditions.find(condition=>condition.id===check.conditionId)?.name??check.name}`} message={check.error} status={`${check.manual?'Manual':'Scheduled'} · ${checkLabels[check.status]}`}/>:<small>{check.manual?'Manual':'Scheduled'} · {checkLabels[check.status]}</small>)}</td><td>{check.nextCheck?analysisDate(check.nextCheck):enabled?'—':'Stopped'}{check.timingHint&&<small>{check.timingHint}</small>}</td><td>{check.sentAlerts.toLocaleString('en-US')}</td></tr>)}</tbody></table></div>{ready&&!checks.length&&<p>Select saved conditions to see their check history.</p>}</div>
   {!!info?.recent?.length&&<div className="live-alerts-recent"><h3>Recent alerts</h3><ul>{info.recent.map((outcome,index)=><li key={`${outcome.dataSource??'dukascopy'}-${outcome.market??''}-${outcome.symbol}-${outcome.timeframe}-${outcome.time}-${outcome.conditionId??outcome.conditionName}-${index}`} className={outcome.status}><strong>{outcome.dataSource&&outcome.dataSource!=='dukascopy'?outcome.symbol:outcome.symbol.toUpperCase()} — {outcome.assetName} · {outcome.timeframe.toUpperCase()} · {outcome.conditionName}{outcome.dataSource&&outcome.dataSource!=='dukascopy'?` · ${liveAlertSourceLabel(outcome.dataSource)}${outcome.market?` ${outcome.market==='perpetual'?'Perpetual':outcome.market==='spot'?'Spot':outcome.market}`:''}`:''}</strong><span>{outcomeLabels[outcome.status]} · Closed {analysisDate(liveAlertBarClose(outcome.time,outcome.timeframe))}</span>{outcome.profitability&&<p>{profitabilityLabel(outcome.profitability)}</p>}{outcome.message&&<p className={outcome.status==='failed'||outcome.status==='uncertain'?'live-alerts-delivery-error':undefined}>{liveAlertErrorText(outcome.message)}</p>}</li>)}</ul></div>}
  </section>
 </section>;
});

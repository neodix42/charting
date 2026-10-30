import {forwardRef,useEffect,useImperativeHandle,useRef,useState} from 'react';
import type {Backend} from '../types';
import {errorText} from '../types';
import type {SavedAnalysisCondition} from '../workspace/screenerAnalysis';
import {analysisDate} from '../workspace/screenerAnalysis';
import type {LiveAlertsConfig,LiveAlertsInfo} from '../workspace/screenerLiveAlerts';
import {initialLiveAlertsConfig,LiveAlertsConfigWriter} from '../workspace/screenerLiveAlerts';
import './ScreenerLiveAlerts.css';

export interface ScreenerLiveAlertsHandle {flush():Promise<void>}
const outcomeLabels={sent:'Sent',failed:'Failed',claimed:'Delivery pending',uncertain:'Delivery uncertain — it may have arrived; no automatic retry'};
export const ScreenerLiveAlerts=forwardRef<ScreenerLiveAlertsHandle,{api:Backend;visible:boolean}>(function ScreenerLiveAlerts({api,visible},ref){
 const [info,setInfo]=useState<LiveAlertsInfo|null>(null),[config,setConfig]=useState(initialLiveAlertsConfig),[conditions,setConditions]=useState<SavedAnalysisCondition[]>([]);
 const [ready,setReady]=useState(false),[query,setQuery]=useState(''),[readError,setReadError]=useState(''),[writeError,setWriteError]=useState(''),[actionError,setActionError]=useState(''),[busy,setBusy]=useState(false),[retry,setRetry]=useState(0),[notice,setNotice]=useState('');
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
  let active=true,needConditions=true,timer:ReturnType<typeof setTimeout>|undefined;
  const read=async()=>{
   const version=epoch.current;
   try{
    if(!action.current&&!writer.current!.busy){
     const [next,saved]=await Promise.all([api.GetScreenerLiveAlerts(),needConditions?api.GetScreenerSavedConditions():Promise.resolve(null)]);
     if(active&&version===epoch.current){
      setInfo(next);setReadError('');if(saved){setConditions(saved);needConditions=false;}
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
 const selectedConditions=new Map(config.conditionPeriods.map(condition=>[condition.conditionId,condition.period])),selectedSymbols=new Set(config.symbols),assets=info?.assets??[],periods=new Set(config.conditionPeriods.map(condition=>condition.period)),eligible=assets.filter(asset=>asset.periods.some(period=>periods.has(period)));
 const eligibleSymbols=new Set(eligible.map(asset=>asset.symbol)),search=query.trim().toLowerCase();
 const groups=[...new Set(assets.map(asset=>asset.category))].sort().map(category=>({category,assets:assets.filter(asset=>asset.category===category&&(!search||`${asset.symbol} ${asset.name} ${asset.category}`.toLowerCase().includes(search)))})).filter(group=>group.assets.length);
 const missingConditions=config.conditionPeriods.filter(selected=>!conditions.some(condition=>condition.id===selected.conditionId)),missingAssets=config.symbols.filter(symbol=>!assets.some(asset=>asset.symbol===symbol));
 const uncoveredPeriod=[...periods].some(period=>!assets.some(asset=>selectedSymbols.has(asset.symbol)&&asset.periods.includes(period)));
 const invalid=!config.conditionPeriods.length?'Select at least one saved condition.':missingConditions.length?'Remove saved conditions that are no longer available.':config.conditionPeriods.some(condition=>!['d1','h1'].includes(condition.period))?'Choose Daily or Hourly for each saved condition.':!config.symbols.length?'Select at least one downloaded asset.':config.symbols.some(symbol=>!eligibleSymbols.has(symbol))?'Select assets with a downloaded file in the chosen periods.':uncoveredPeriod?'Select at least one downloaded asset for every assigned period.':'';
 const toggleSymbol=(symbol:string,checked:boolean)=>{const next=new Set(draft.current.symbols);if(checked)next.add(symbol);else next.delete(symbol);change({symbols:[...next]});};
 const setConditionPeriod=(conditionId:string,period?:string)=>{
  const selected=draft.current.conditionPeriods;
  const next=period===undefined?selected.filter(condition=>condition.conditionId!==conditionId):selected.some(condition=>condition.conditionId===conditionId)?selected.map(condition=>condition.conditionId===conditionId?{conditionId,period}:condition):[...selected,{conditionId,period}];
  change({conditionPeriods:next});
 };
 const selectAssets=(symbols:string[],checked:boolean)=>{const next=new Set(draft.current.symbols);for(const symbol of symbols){if(checked)next.add(symbol);else next.delete(symbol);}change({symbols:[...next]});};
 return <section className="live-alerts" aria-label="Live Alerts configuration">
  <header className="live-alerts-heading"><div><span className="analysis-eyebrow">CLOSED-BAR SIGNALS</span><h2>Live Alerts</h2><p>Send matching saved conditions to your Telegram recipient on daily and hourly closes.</p></div><span className={`live-alerts-state${enabled?' enabled':''}`}>{enabled?'Enabled':'Stopped'}</span></header>
  {(readError||writeError||actionError||info?.error)&&<div className="live-alerts-error" role="alert">{actionError||writeError||readError||info?.error}<button type="button" disabled={busy} onClick={()=>{setRetry(value=>value+1);if(writeError)run(async()=>{await writer.current!.flush();});}}>Retry</button></div>}
  {!ready&&!readError&&<p role="status" className="live-alerts-note">Loading saved alert settings…</p>}
  <div className="live-alerts-columns">
   <section className="live-alerts-card" aria-label="Alert conditions and periods"><h3>Saved conditions</h3><p>Each selected condition checks only its assigned period, with all its visual rows combined.</p>
    <fieldset disabled={locked} className="live-alerts-condition-selection"><legend className="sr-only">Saved conditions for alerts</legend>
     <table className="live-alerts-condition-table" aria-label="Saved conditions and periods"><thead><tr><th scope="col">Saved condition</th><th scope="col">Period</th></tr></thead><tbody>
      {conditions.map(condition=><tr key={condition.id}><td><label><input type="checkbox" aria-label={`Alert condition ${condition.name}`} checked={selectedConditions.has(condition.id)} onChange={event=>setConditionPeriod(condition.id,event.target.checked?'d1':undefined)}/><span><strong>{condition.name}</strong><small>{condition.request.conditions.length} condition rows{condition.request.tradeSimulation?' · SL / TP configured':' · No SL / TP configured'}</small></span></label></td><td><select aria-label={`Alert period for ${condition.name}`} disabled={!selectedConditions.has(condition.id)} value={selectedConditions.get(condition.id)??'d1'} onChange={event=>setConditionPeriod(condition.id,event.target.value)}><option value="">Choose period</option><option value="d1">Daily (D1)</option><option value="h1">Hourly (H1)</option></select></td></tr>)}
      {missingConditions.map(({conditionId,period})=><tr key={conditionId}><td><label><input type="checkbox" aria-label={`Remove unavailable condition ${conditionId}`} checked onChange={()=>setConditionPeriod(conditionId)}/><span>Unavailable saved condition<small>{conditionId}</small></span></label></td><td>{period==='h1'?'Hourly (H1)':period==='d1'?'Daily (D1)':'Choose period'}</td></tr>)}
     </tbody></table>
    </fieldset>
    {ready&&!conditions.length&&<p>Create a named condition in Analysis using Safe Condition.</p>}
    <p>Hourly checks start on the next UTC hour. Daily checks start at the next 00:00 UTC. Only conditions whose assigned bar has closed are checked automatically.</p>
   </section>
   <section className="live-alerts-card live-alerts-assets" aria-label="Downloaded assets for alerts"><div className="live-alerts-card-heading"><h3>Downloaded assets</h3><span>{config.symbols.length} selected</span></div><input type="search" aria-label="Search live alert assets" placeholder="Search assets or categories…" value={query} onChange={event=>setQuery(event.target.value)}/>
    <div className="live-alerts-select-buttons"><button type="button" disabled={locked||!eligible.length} onClick={()=>change({symbols:eligible.map(asset=>asset.symbol)})}>Select all assets</button><button type="button" disabled={locked||!config.symbols.length} onClick={()=>change({symbols:[]})}>Deselect all assets</button></div>
    <div className="live-alerts-asset-list">{groups.map(group=><details key={group.category} open className="live-alerts-category"><summary>{group.category}<span>{group.assets.filter(asset=>selectedSymbols.has(asset.symbol)).length} / {group.assets.length}</span></summary><button type="button" disabled={locked||!group.assets.some(asset=>eligibleSymbols.has(asset.symbol))} onClick={()=>selectAssets(group.assets.filter(asset=>eligibleSymbols.has(asset.symbol)).map(asset=>asset.symbol),!group.assets.filter(asset=>eligibleSymbols.has(asset.symbol)).every(asset=>selectedSymbols.has(asset.symbol)))}>Select / deselect {group.category}</button><fieldset disabled={locked} className="live-alerts-choices"><legend className="sr-only">{group.category} assets</legend>{group.assets.map(asset=><label key={asset.symbol}><input type="checkbox" aria-label={`Alert asset ${asset.symbol.toUpperCase()} — ${asset.name}`} checked={selectedSymbols.has(asset.symbol)} disabled={!eligibleSymbols.has(asset.symbol)&&!selectedSymbols.has(asset.symbol)} onChange={event=>toggleSymbol(asset.symbol,event.target.checked)}/><span><strong>{asset.symbol.toUpperCase()} — {asset.name}</strong><small>{asset.periods.map(period=>period.toUpperCase()).join(' · ')} downloaded</small></span></label>)}</fieldset></details>)}
     {missingAssets.map(symbol=><label className="live-alerts-missing" key={symbol}><input type="checkbox" aria-label={`Remove unavailable asset ${symbol}`} disabled={locked} checked onChange={()=>toggleSymbol(symbol,false)}/>{symbol.toUpperCase()} — downloaded file unavailable</label>)}
     {ready&&!assets.length&&<p>Download D1 or H1 files in Downloader before selecting assets.</p>}{assets.length>0&&!groups.length&&<p>No assets match your search.</p>}
    </div>
   </section>
  </div>
  <div className="live-alerts-controls"><button type="button" disabled={locked} onClick={()=>run(async()=>{await writer.current!.flush();},'Alert configuration saved.')}>Save config</button>{enabled?<button type="button" className="danger" disabled={busy} onClick={()=>run(()=>api.ConfigureScreenerLiveAlerts({...draft.current,enabled:false}),'Live Alerts stopped.')}>Stop alerts</button>:<button type="button" className="primary" disabled={!ready||busy||info?.running||!!invalid} onClick={()=>run(()=>api.ConfigureScreenerLiveAlerts({...draft.current,enabled:true}),'Live Alerts started.')}>Start alerts</button>}<button type="button" disabled={!enabled||busy||info?.running} onClick={()=>run(()=>api.CheckScreenerLiveAlerts())}>Check now</button>{notice&&<span role="status">{notice}</span>}</div>
  <div className="live-alerts-note">{!enabled&&invalid&&<p>{invalid}</p>}<p>{enabled?'Stop alerts to edit the configuration.':'Start alerts sends matching signals to the recipient configured in Settings → Telegram Bot.'} CryptoDesk must remain running; enabled alerts resume when you restart it.</p><p>Only downloaded D1 / H1 files are monitored. The indicative entry is the latest closed bar’s close; Stop Loss and Take profit use the saved condition’s simulation settings. Prices are signals, not executed trades.</p></div>
  <section className="live-alerts-card live-alerts-status" aria-label="Live alert status"><div className="live-alerts-card-heading"><h3>{info?.status||'Stopped'}</h3><span>{info?.sent??0} alerts sent</span></div>{info?.running&&<><p>{info.currentAsset||'Checking selected assets…'}</p><progress aria-label="Live alerts progress" value={Math.max(0,Math.min(100,info.progress||0))} max={100}/></>}<dl><div><dt>Last check</dt><dd>{info?.lastRun?analysisDate(info.lastRun):'Not checked yet'}</dd></div><div><dt>Next check</dt><dd>{info?.nextRun?analysisDate(info.nextRun):'—'}</dd></div></dl>
   {!!info?.recent?.length&&<div className="live-alerts-recent"><h3>Recent alerts</h3><ul>{info.recent.map((outcome,index)=><li key={`${outcome.symbol}-${outcome.timeframe}-${outcome.time}-${outcome.conditionName}-${index}`} className={outcome.status}><strong>{outcome.symbol.toUpperCase()} — {outcome.assetName} · {outcome.timeframe.toUpperCase()} · {outcome.conditionName}</strong><span>{outcomeLabels[outcome.status]} · Closed {analysisDate(outcome.time+(outcome.timeframe==='d1'?86400000:3600000))}</span>{outcome.message&&<p>{outcome.message}</p>}</li>)}</ul></div>}
  </section>
 </section>;
});

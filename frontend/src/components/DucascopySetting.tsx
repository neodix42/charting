import {forwardRef,useEffect,useImperativeHandle,useRef,useState} from 'react';
import type {Backend,DucascopyInfo,DucascopySettings} from '../types';
import {clearInstrumentCatalog,DUCASCOPY_CONFIGURED} from '../bridge/instruments';

export interface DucascopySettingHandle {flush():Promise<void>}
const loadError='Could not load JForex settings. Saved credentials have been preserved.';
const saveError='Could not save JForex credentials. Please retry.';
const testError='Could not test the JForex connection. Check your connection, credentials and Java runtime, then retry.';
const challengeError='Could not load the JForex PIN image. Check your connection, credentials and Java runtime, then retry.';
const connectionGuards=new Set(['Close or reselect active DucasCopy charts before changing JForex credentials','Close the active Dukascopy charts and finish connection requests before changing these credentials']);
const safeSaveError=(reason:unknown)=>{const message=reason instanceof Error?reason.message:typeof reason==='string'?reason:'';return connectionGuards.has(message)?message:saveError;};

export const DucascopySetting=forwardRef<DucascopySettingHandle,{api:Backend;visible:boolean}>(function DucascopySetting({api,visible},ref){
 const [info,setInfo]=useState<DucascopyInfo|null>(null),[username,setUsername]=useState(''),[password,setPassword]=useState(''),[environment,setEnvironment]=useState<DucascopySettings['environment']>('demo'),[clearPassword,setClearPassword]=useState(false),[loading,setLoading]=useState(false),[operation,setOperation]=useState<'save'|'test'|'challenge'|null>(null),[error,setError]=useState(''),[status,setStatus]=useState(''),[retry,setRetry]=useState(0),[challenge,setChallenge]=useState(''),[pin,setPIN]=useState(''),[cancelling,setCancelling]=useState(false);
 const alive=useRef(false),pending=useRef<Promise<void>|null>(null),cancellation=useRef<Promise<void>|null>(null),cancelRequested=useRef(false);
 useEffect(()=>{alive.current=true;return()=>{alive.current=false;};},[]);
 useImperativeHandle(ref,()=>({flush:async()=>{await pending.current;await cancellation.current;}}),[]);
 useEffect(()=>{
  if(!visible||info)return;let current=true;setLoading(true);setError('');
  void api.GetDucascopy().then(next=>{if(current){setInfo(next);setUsername(next.username);setEnvironment(next.environment);setLoading(false);}}).catch(()=>{if(current){setError(loadError);setLoading(false);}});
  return()=>{current=false;};
 },[api,visible,info,retry]);
 const sameIdentity=!!info&&username.trim()===info.username&&environment===info.environment;
 const retainedPassword=!!info?.passwordConfigured&&sameIdentity&&!clearPassword;
 const dirty=!!info&&(!sameIdentity||password!==''||clearPassword);
 const valid=!!username.trim()&&(retainedPassword||password!==''||clearPassword);
 const disabled=!info||loading||!!operation||cancelling;
 const edit=()=>{setError('');setStatus('');setChallenge('');setPIN('');};
 const changed=()=>{clearInstrumentCatalog('dukascopy');window.dispatchEvent(new Event(DUCASCOPY_CONFIGURED));};
 const perform=(kind:'save'|'test'|'challenge')=>{
  if(pending.current||cancellation.current||!info||!valid||(kind!=='save'&&clearPassword))return;
  cancelRequested.current=false;setOperation(kind);setError('');setStatus('');
  const request:DucascopySettings={username:username.trim(),password:clearPassword?'':password,environment,...(clearPassword?{clearPassword:true}:{})};
  let saving=true;
  const task=Promise.resolve().then(()=>api.ConfigureDucascopy(request)).then(async saved=>{
   saving=false;
   if(alive.current){setInfo(saved);setUsername(saved.username);setEnvironment(saved.environment);setPassword('');setClearPassword(false);}
   if(cancelRequested.current)return;
   if(kind==='save'){changed();if(alive.current){setPIN('');setChallenge('');setStatus(saved.passwordConfigured?'JForex credentials saved.':'Saved JForex password removed.');}return;}
   if(kind==='challenge'){const next=await api.GetDucascopyLoginChallenge();if(cancelRequested.current)return;if(!/^data:image\/png;base64,[A-Za-z0-9+/=]+$/.test(next.imageDataUrl))throw Error(challengeError);if(alive.current){setPIN('');setChallenge(next.imageDataUrl);}return;}
   const tested=await(environment==='live'&&pin.trim()?api.TestDucascopyConnectionWithPIN(pin.trim()):api.TestDucascopyConnection());
   if(cancelRequested.current)return;
   if(tested.connected)changed();
   if(alive.current){setInfo(tested);setPIN('');setChallenge('');if(tested.connected)setStatus(`Connected to JForex ${tested.environment==='live'?'Live':'Demo'} · ${tested.instrumentCount} forex pairs available.`);else setError(tested.error||testError);}
  }).catch(reason=>{if(cancelRequested.current)return;const message=saving?safeSaveError(reason):kind==='challenge'?challengeError:testError;if(alive.current){setError(message);setPIN('');setChallenge('');}if(saving)throw new Error(message);}).finally(()=>{pending.current=null;if(alive.current)setOperation(null);});
  pending.current=task;void task.catch(()=>{});
 };
 const cancel=()=>{
  if(!pending.current||operation==='save'||cancellation.current)return;
  cancelRequested.current=true;setCancelling(true);setPIN('');setChallenge('');setError('');
  const task=Promise.resolve().then(()=>api.CancelDucascopyConnectionTest()).then(async()=>{await pending.current;if(alive.current)setStatus('JForex connection check canceled.');}).catch(()=>{cancelRequested.current=false;if(alive.current)setError('Could not cancel the JForex connection check. Please retry.');}).finally(()=>{cancellation.current=null;if(alive.current)setCancelling(false);});
  cancellation.current=task;void task;
 };
 return <form hidden={!visible} className="settings-section ducascopy-setting" aria-label="Ducas Copy settings" onSubmit={event=>{event.preventDefault();perform('save');}}>
  <h3>JForex connection</h3><p>Use your Dukascopy JForex credentials for forex history and live Bid quotes. Select DucasCopy as the chart source after saving.</p>
  {loading&&<p role="status">Loading JForex settings…</p>}
  <label className="setting-row"><span><strong>Environment</strong><small>Use credentials for the selected JForex environment.</small></span><select aria-label="JForex environment" value={environment} disabled={disabled} onChange={event=>{setEnvironment(event.target.value as DucascopySettings['environment']);edit();}}><option value="demo">Demo</option><option value="live">Live</option></select></label>
  <label className="candle-storage-path"><span>JForex Username</span><input aria-label="JForex Username" autoComplete="off" autoCapitalize="off" spellCheck={false} maxLength={256} value={username} disabled={disabled} onChange={event=>{setUsername(event.target.value);edit();}}/></label>
  <label className="candle-storage-path"><span>JForex Password</span><input type="password" aria-label="JForex Password" autoComplete="off" autoCapitalize="off" spellCheck={false} maxLength={4096} value={password} placeholder={retainedPassword?'Saved password · leave blank to keep':''} disabled={disabled||clearPassword} onChange={event=>{setPassword(event.target.value);edit();}}/></label>
  {info?.passwordConfigured&&sameIdentity&&<label className="ducascopy-remove-password"><input type="checkbox" checked={clearPassword} disabled={disabled} onChange={event=>{setClearPassword(event.target.checked);edit();}}/>Remove saved password</label>}
  {info?.passwordConfigured&&!sameIdentity&&!password&&<p className="muted">Enter a password for the new username or environment.</p>}
  {environment==='live'&&<div className="ducascopy-pin"><p>Live accounts with a registered IP can connect without a PIN. Otherwise, load the PIN challenge, enter the code shown and click Test connection. The PIN is used once and is not saved.</p><button type="button" disabled={disabled||!valid||clearPassword} onClick={()=>perform('challenge')}>{operation==='challenge'?'Loading PIN challenge…':'Load PIN challenge'}</button>{challenge&&<><img src={challenge} alt="JForex login PIN challenge"/><label className="candle-storage-path"><span>JForex PIN</span><input aria-label="JForex PIN" autoComplete="off" spellCheck={false} maxLength={32} value={pin} disabled={disabled} onChange={event=>setPIN(event.target.value)}/></label></>}</div>}
  <p className="muted">Credentials are saved on this computer separately from workspace exports. The saved password is never sent back to this screen. Opening settings does not log in.</p>
  {info&&<div className="ducascopy-runtime"><p><strong>Java runtime:</strong> {info.javaPath||'Java 17 or newer is required.'}</p><p><strong>Bridge folder:</strong> {info.runtimeDirectory||'Not available'}</p><p className="muted">Install a Java 17 or newer runtime (JRE or JDK). The bridge is included with CryptoDesk. The first connection downloads the pinned Dukascopy SDK files into your private app profile and can take a moment.</p>{!info.runtimeAvailable&&<p className="candle-storage-error">{info.error||'The JForex bridge or Java runtime is unavailable. Install Java 17 or newer, then retry the connection.'}</p>}</div>}
  {error&&<p className="candle-storage-error" role="alert">{error}{!info&&<button type="button" disabled={loading} onClick={()=>setRetry(value=>value+1)}>Retry JForex settings</button>}</p>}
  {status&&<p role="status">{status}</p>}
  <div className="candle-storage-actions">{(operation==='test'||operation==='challenge'||cancelling)&&<button type="button" disabled={cancelling} onClick={cancel}>{cancelling?'Cancelling…':'Cancel test'}</button>}<button type="submit" disabled={disabled||!dirty||!valid}>{operation==='save'?'Saving…':'Save credentials'}</button><button type="button" disabled={disabled||!valid||clearPassword} onClick={()=>perform('test')}>{operation==='test'?'Testing connection…':'Test connection'}</button></div>
 </form>;
});

import {forwardRef,useEffect,useImperativeHandle,useRef,useState} from 'react';
import type {Backend,TelegramBotSettings} from '../types';

export interface TelegramBotSettingHandle {flush():Promise<void>}
const saveError='Could not save Telegram Bot settings. Please retry.';

export const TelegramBotSetting=forwardRef<TelegramBotSettingHandle,{api:Backend;visible:boolean}>(function TelegramBotSetting({api,visible},ref){
 const [settings,setSettings]=useState<TelegramBotSettings|null>(null),[token,setToken]=useState(''),[loading,setLoading]=useState(false),[saving,setSaving]=useState(false),[error,setError]=useState(''),[saved,setSaved]=useState(false),[retry,setRetry]=useState(0);
 const alive=useRef(false),pending=useRef<Promise<void>|null>(null);
 useEffect(()=>{alive.current=true;return ()=>{alive.current=false;};},[]);
 useImperativeHandle(ref,()=>({flush:async()=>{await pending.current;}}),[]);
 useEffect(()=>{
  if(!visible||settings)return;
  let current=true;setLoading(true);setError('');
  void api.GetTelegramBotSettings().then(value=>{if(current){setSettings(value);setToken(value.apiToken);setLoading(false);}}).catch(()=>{if(current){setError('Could not load Telegram Bot settings. The saved token has been preserved.');setLoading(false);}});
  return ()=>{current=false;};
 },[api,visible,settings,retry]);
 const save=()=>{
  if(pending.current||!settings)return;
  setSaving(true);setError('');setSaved(false);
  const operation=Promise.resolve().then(()=>api.ConfigureTelegramBot(token)).then(value=>{if(alive.current){setSettings(value);setToken(value.apiToken);setSaved(true);}}).catch(()=>{if(alive.current)setError(saveError);throw new Error(saveError);}).finally(()=>{pending.current=null;if(alive.current)setSaving(false);});
  pending.current=operation;void operation.catch(()=>{});
 };
 return <form hidden={!visible} className="settings-section telegram-bot-setting" aria-label="Telegram Bot settings" onSubmit={event=>{event.preventDefault();save();}}>
  <h3>Telegram Bot</h3><p>Save a token for future bot alerts. No Telegram requests or messages are sent.</p>
  {loading&&<p role="status">Loading Telegram Bot settings…</p>}
  <label className="candle-storage-path"><span>Telegram Bot API Token</span><input type="password" aria-label="Telegram Bot API Token" autoComplete="off" autoCapitalize="off" spellCheck={false} maxLength={4096} value={token} disabled={!settings||loading||saving} onChange={event=>{setToken(event.target.value);setError('');setSaved(false);}}/></label>
  <p className="muted">Stored on this computer, separately from workspace exports. Leave empty and save to remove the token.</p>
  {error&&<p className="candle-storage-error" role="alert">{error}{!settings&&<button type="button" onClick={()=>setRetry(value=>value+1)} disabled={loading}>Retry Telegram settings</button>}</p>}
  {saved&&<p role="status">Telegram Bot settings saved.</p>}
  <div className="candle-storage-actions"><button type="submit" disabled={!settings||loading||saving||token===settings.apiToken}>{saving?'Saving…':'Save Telegram Bot settings'}</button></div>
 </form>;
});

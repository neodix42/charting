import {forwardRef,useEffect,useImperativeHandle,useRef,useState} from 'react';
import type {Backend,TelegramBotSettings} from '../types';
import {telegramRecipients} from '../workspace/telegramRecipients';

export interface TelegramBotSettingHandle {flush():Promise<void>}
const saveError='Could not save Telegram Bot settings. Please retry.';
const testErrors:Record<string,string>={
 auth:'Telegram rejected the API token. Check the token and try again.',
 recipientNotFound:'Recipient not found. Open the bot and press Start (or send /start again), then click Test.',
 blocked:'The recipient must unblock the bot and press Start before trying Test again.',
 webhook:'This bot uses a webhook. Enter the recipient’s numeric private chat ID for testing. The webhook was not changed.',
 rateLimit:'Telegram is limiting requests. Wait before testing again.',
 network:'Could not contact Telegram. Check your connection and try again.',
 uncertain:'Delivery was not confirmed. Check Telegram before retrying; no automatic resend was made.',
 storage:'Could not save the verified recipient. Retry saving settings before testing again.',
 changed:'Settings changed during the test. Try Test again with the current settings.',
 busy:'Another Telegram test is already running.',
 response:'Telegram returned an unexpected response. Please try again later.',
};

export const TelegramBotSetting=forwardRef<TelegramBotSettingHandle,{api:Backend;visible:boolean}>(function TelegramBotSetting({api,visible},ref){
 const [settings,setSettings]=useState<TelegramBotSettings|null>(null),[token,setToken]=useState(''),[recipient,setRecipient]=useState(''),[loading,setLoading]=useState(false),[operation,setOperation]=useState<'save'|'test'|null>(null),[error,setError]=useState(''),[status,setStatus]=useState(''),[retry,setRetry]=useState(0);
 const [botUsername,setBotUsername]=useState(''),[botLoading,setBotLoading]=useState(false),[botError,setBotError]=useState(false),[botRetry,setBotRetry]=useState(0);
 const alive=useRef(false),pending=useRef<Promise<void>|null>(null),botEpoch=useRef(0);
 const recipients=telegramRecipients(recipient),namedRecipients=recipients?.filter(value=>value.startsWith('@'))??[];
 const namedRecipient=namedRecipients.length>0,recipientValid=recipients!==null;
 useEffect(()=>{alive.current=true;return ()=>{alive.current=false;};},[]);
 useImperativeHandle(ref,()=>({flush:async()=>{await pending.current;}}),[]);
 useEffect(()=>{
  if(!visible||settings)return;
  let current=true;setLoading(true);setError('');
  void api.GetTelegramBotSettings().then(value=>{if(current){setSettings(value);setToken(value.apiToken);setRecipient(value.recipient??'');setLoading(false);}}).catch(()=>{if(current){setError('Could not load Telegram Bot settings. The saved configuration has been preserved.');setLoading(false);}});
  return ()=>{current=false;};
 },[api,visible,settings,retry]);
 useEffect(()=>{
  const revision=++botEpoch.current;
  setBotUsername('');setBotError(false);setBotLoading(false);
  if(!visible||!namedRecipient||!token.trim())return;
  let current=true;setBotLoading(true);
  const timer=setTimeout(()=>{
   if(!current||revision!==botEpoch.current)return;
   void api.GetTelegramBotIdentity(token.trim()).then(bot=>{if(current&&revision===botEpoch.current){setBotUsername(bot.username);setBotLoading(false);}}).catch(()=>{if(current&&revision===botEpoch.current){setBotError(true);setBotLoading(false);}});
  },350);
  return ()=>{current=false;clearTimeout(timer);};
 },[api,visible,namedRecipient,token,botRetry]);
 const perform=(kind:'save'|'test')=>{
  if(pending.current||!settings||!recipientValid)return;
  setOperation(kind);setError('');setStatus('');
  const task=Promise.resolve().then(()=>api.ConfigureTelegramBotSettings(token,recipient)).then(async value=>{
   if(alive.current){setSettings(value);setToken(value.apiToken);setRecipient(value.recipient??'');}
   if(kind==='save'){if(alive.current)setStatus('Telegram Bot settings saved.');return;}
   try{
    const result=await api.TestTelegramBot();
    if(alive.current){
     if(result.botUsername){botEpoch.current++;setBotUsername(result.botUsername);setBotLoading(false);setBotError(false);}
     const total=result.totalRecipients??1,sent=result.sentCount??(result.sent?total:0);
     if(result.sent)setStatus(total>1?`Test notifications sent to ${sent} recipients.`:'Test notification sent.');
     else{
      const details=testErrors[result.code??'response']??testErrors.response;
      setError(total>1?`${sent>0?`Test notifications sent to ${sent} of ${total} recipients.`:`No test notifications were confirmed for ${total} recipients.`} ${details}${sent>0?' Check Telegram first; testing again also notifies recipients who already received this test.':''}`:details);
     }
    }
   }catch{if(alive.current)setError(testErrors.uncertain);}
  }).catch(()=>{if(alive.current)setError(saveError);throw new Error(saveError);}).finally(()=>{pending.current=null;if(alive.current)setOperation(null);});
  pending.current=task;void task.catch(()=>{});
 };
 const edit=()=>{setError('');setStatus('');};
 const disabled=!settings||loading||!!operation;
 const dirty=!!settings&&(token!==settings.apiToken||recipient!==(settings.recipient??''));
 return <form hidden={!visible} className="settings-section telegram-bot-setting" aria-label="Telegram Bot settings" onSubmit={event=>{event.preventDefault();perform('save');}}>
  <h3>Telegram Bot</h3><p>Save a bot and comma-separated recipients for alerts. Test sends one notification to each recipient.</p>
  {loading&&<p role="status">Loading Telegram Bot settings…</p>}
  <label className="candle-storage-path"><span>Telegram Bot API Token</span><input type="password" aria-label="Telegram Bot API Token" autoComplete="off" autoCapitalize="off" spellCheck={false} maxLength={4096} value={token} disabled={disabled} onChange={event=>{setToken(event.target.value);edit();}}/></label>
  <label className="candle-storage-path"><span>Recipients (comma-separated)</span><input aria-label="Recipient" aria-describedby="telegram-recipient-help" autoComplete="off" autoCapitalize="off" spellCheck={false} placeholder="@alice, @bob, 12345678" maxLength={4096} value={recipient} disabled={disabled} onChange={event=>{setRecipient(event.target.value);edit();}}/></label>
  <p id="telegram-recipient-help" className="muted">Enter up to 20 @usernames or positive private chat IDs, separated by commas. Each recipient must open your bot and press Start. Duplicate recipients receive one notification.</p>
  {namedRecipient&&<div className="telegram-start-help" role="note"><p>{namedRecipients.join(', ')} must open bot <strong>{botUsername?`@${botUsername}`:botLoading?'(looking up bot name…)':'for the entered token'}</strong> in Telegram and press <strong>Start</strong>. Then click <strong>Test</strong>.</p>{botError&&<p>Could not look up the bot name. Check the token or <button type="button" className="text-button" disabled={!!operation} onClick={()=>setBotRetry(value=>value+1)}>Retry bot name</button>.</p>}</div>}
  {!recipientValid&&<p className="candle-storage-error">Use up to 20 @usernames or positive private chat IDs separated by commas, without empty entries.</p>}
  <p className="muted">Stored locally, separately from workspace exports. Clear a field and save to remove its setting. Start automatic alerts in Screener → Live Alerts.</p>
  {error&&<p className="candle-storage-error" role="alert">{error}{!settings&&<button type="button" onClick={()=>setRetry(value=>value+1)} disabled={loading}>Retry Telegram settings</button>}</p>}
  {status&&<p role="status">{status}</p>}
  <div className="candle-storage-actions"><button type="submit" disabled={disabled||!dirty||!recipientValid}>{operation==='save'?'Saving…':'Save Telegram Bot settings'}</button><button type="button" disabled={disabled||!token.trim()||!recipients?.length||!recipientValid} onClick={()=>perform('test')}>{operation==='test'?'Testing…':'Test'}</button></div>
 </form>;
});

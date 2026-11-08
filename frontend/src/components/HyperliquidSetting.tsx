import {forwardRef,useEffect,useImperativeHandle,useRef,useState} from 'react';
import type {Backend,HyperliquidInfo,HyperliquidSettings} from '../types';
import {errorText} from '../types';
import {TRADING_CONFIGURED} from '../workspace/trading';

export interface HyperliquidSettingHandle {flush():Promise<void>}
export const HyperliquidSetting=forwardRef<HyperliquidSettingHandle,{api:Backend;visible:boolean}>(function HyperliquidSetting({api,visible},ref){
 const [info,setInfo]=useState<HyperliquidInfo|null>(null),[account,setAccount]=useState(''),[secret,setSecret]=useState(''),[network,setNetwork]=useState<HyperliquidSettings['network']>('mainnet'),[clear,setClear]=useState(false),[help,setHelp]=useState(false),[busy,setBusy]=useState(false),[error,setError]=useState(''),[message,setMessage]=useState(''),[retry,setRetry]=useState(0);
 const alive=useRef(true),pending=useRef<Promise<void>|null>(null),helpButton=useRef<HTMLButtonElement>(null);
 useEffect(()=>{alive.current=true;return()=>{alive.current=false;};},[]);
 useImperativeHandle(ref,()=>({flush:async()=>{await pending.current;}}),[]);
 useEffect(()=>{if(!visible||info)return;let active=true;void api.GetHyperliquid().then(next=>{if(active){setInfo(next);setAccount(next.accountAddress);setNetwork(next.network);setError('');}}).catch(()=>{if(active)setError('Could not load Hyperliquid settings. Saved credentials were preserved.');});return()=>{active=false;};},[api,visible,info,retry]);
 const same=!!info&&info.accountAddress.toLowerCase()===account.trim().toLowerCase()&&info.network===network,retained=same&&info.privateKeyConfigured&&!clear;
 const valid=/^0x[0-9a-fA-F]{40}$/.test(account.trim())&&(retained||/^(?:0x)?[0-9a-fA-F]{64}$/.test(secret.trim())||clear),dirty=!!info&&(!same||!!secret||clear);
 const edit=()=>{setError('');setMessage('');};
 const perform=(test:boolean)=>{
  if(pending.current||!info||!valid||(test&&clear))return;
  setBusy(true);setError('');setMessage('');
  const request:HyperliquidSettings={accountAddress:account.trim(),privateKey:clear?'':secret.trim(),network,...(clear?{clearPrivateKey:true}:{})};
  let saved=false;
  const task=Promise.resolve().then(()=>api.ConfigureHyperliquid(request)).then(async next=>{
   saved=true;window.dispatchEvent(new Event(TRADING_CONFIGURED));
   if(alive.current){setInfo(next);setAccount(next.accountAddress);setNetwork(next.network);setSecret('');setClear(false);}
   if(test)next=await api.TestHyperliquidConnection();
   if(alive.current){setInfo(next);if(test&&!next.connected)setError(next.error||'The API agent connection could not be verified.');else setMessage(test?`Hyperliquid ${next.network} API agent verified. No order was placed.`:next.privateKeyConfigured?'Hyperliquid credentials saved.':'Saved Hyperliquid API key removed.');}
  }).catch(reason=>{if(alive.current)setError(errorText(reason).replace(/(?:0x)?[a-fA-F0-9]{64}/g,'[redacted]').slice(0,1024)|| (saved?'Could not verify the API agent. Check the account address, network and API wallet approval.':'Could not save Hyperliquid credentials. Please retry.'));if(!saved)throw Error('Could not save Hyperliquid credentials.');}).finally(()=>{pending.current=null;if(alive.current)setBusy(false);});
  pending.current=task;void task.catch(()=>{});
 };
 const closeHelp=()=>{setHelp(false);helpButton.current?.focus();};
 return <form hidden={!visible} className="settings-section" aria-label="Hyperliquid settings" onSubmit={event=>{event.preventDefault();perform(false);}}>
  <h3>Hyperliquid API access <button ref={helpButton} type="button" className="icon" aria-label="How to create a Hyperliquid API key" onClick={()=>setHelp(true)}>?</button></h3>
  <p>Use a dedicated approved API wallet to sign trades. Public charts do not need a key.</p>
  <label className="setting-row"><span>Network</span><select aria-label="Hyperliquid network" disabled={!info||busy} value={network} onChange={event=>{setNetwork(event.target.value as HyperliquidSettings['network']);edit();}}><option value="mainnet">Mainnet · real funds</option><option value="testnet">Testnet</option></select></label>
  <label className="candle-storage-path"><span>Trading account address</span><input aria-label="Hyperliquid account address" autoComplete="off" spellCheck={false} value={account} disabled={!info||busy} onChange={event=>{setAccount(event.target.value);edit();}}/></label>
  <label className="candle-storage-path"><span>API wallet private key</span><input type="password" aria-label="Hyperliquid API private key" autoComplete="off" spellCheck={false} value={secret} maxLength={66} placeholder={retained?'Saved key · leave blank to keep':''} disabled={!info||busy||clear} onChange={event=>{setSecret(event.target.value);edit();}}/></label>
  {same&&info?.privateKeyConfigured&&<label><input type="checkbox" checked={clear} disabled={busy} onChange={event=>{setClear(event.target.checked);edit();}}/>Remove saved API key</label>}
  {info?.agentAddress&&<p>API wallet address: <code>{info.agentAddress}</code></p>}
  <p className="muted">The key is saved on this computer separately from workspace exports and is never returned to this screen. Use the trading account address, not the API wallet address. Opening Settings does not place orders.</p>
  {error&&<p role="alert" className="candle-storage-error">{error}{!info&&<button type="button" onClick={()=>setRetry(value=>value+1)}>Retry Hyperliquid settings</button>}</p>}
  {message&&<p role="status">{message}</p>}
  <div className="button-row"><button type="submit" disabled={!dirty||!valid||busy}>{busy?'Working…':'Save credentials'}</button><button type="button" disabled={!info||!valid||clear||busy} onClick={()=>perform(true)}>Test connection</button></div>
  {help&&<div className="modal-backdrop" onMouseDown={event=>{if(event.target===event.currentTarget)closeHelp();}}><section className="modal" role="dialog" aria-modal="true" aria-label="Create a Hyperliquid API key" onKeyDown={event=>{if(event.key==='Escape'){event.stopPropagation();closeHelp();}}}>
   <div className="modal-header"><h3>Create a Hyperliquid API key</h3><button type="button" autoFocus aria-label="Close API key help" onClick={closeHelp}>×</button></div>
   <ol><li>Open the official Hyperliquid app for the intended network and connect your trading account wallet.</li><li>Open its API page, create a dedicated named API wallet for CryptoDesk and approve it with your account wallet.</li><li>Copy the generated API wallet private key into this form. Enter your trading account’s public address above it, select the matching network, then click Test connection.</li></ol>
   <p>Use the dedicated API wallet key, never your main wallet’s private key or seed phrase. Revoking or expiring the API wallet removes its authorization. The test checks approval without placing an order.</p>
   <div className="button-row"><button type="button" onClick={()=>void api.OpenHyperliquidHelp(network==='testnet'?'testnetApi':'api').catch(()=>setError('Could not open the Hyperliquid API page.'))}>Open Hyperliquid API page</button><button type="button" onClick={()=>void api.OpenHyperliquidHelp('docs').catch(()=>setError('Could not open API wallet documentation.'))}>Official API wallet documentation</button></div>
  </section></div>}
 </form>;
});

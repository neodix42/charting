import {useEffect,useRef,useState} from 'react';
import type {Backend,HyperliquidTokenBalance,HyperliquidTransferPreview,HyperliquidTransferResult,HyperliquidVenueBalance} from '../types';
import {errorText} from '../types';
import {accountModeLabel,balanceIsStale,normalizedTransferAmount,orderedVenues,transferAmountProblem,transferTokens,venueLabel} from '../workspace/hyperliquidBalances';
import {metricAmount} from './TradeMetricsPanel';
import {useHyperliquidBalances} from './useHyperliquidBalances';
import './HyperliquidBalancesPanel.css';

type Phase='idle'|'edit'|'reviewing'|'review'|'submitting'|'done';
interface Props {
 api:Backend;network:string;selectedDex:string;orderReviewed:boolean;disabled:boolean;
 onInvalidateOrder():void;onTransferActivity(open:boolean,busy:boolean):void;onRefreshOrder():void;
}

export function HyperliquidBalancesPanel({api,network,selectedDex,orderReviewed,disabled,onInvalidateOrder,onTransferActivity,onRefreshOrder}:Props){
 const [phase,setPhase]=useState<Phase>('idle'),[refresh,setRefresh]=useState(0),[sourceDex,setSourceDex]=useState(''),[destinationDex,setDestinationDex]=useState('spot'),[token,setToken]=useState<number|null>(null),[amount,setAmount]=useState(''),[preview,setPreview]=useState<HyperliquidTransferPreview|null>(null),[result,setResult]=useState<HyperliquidTransferResult|null>(null),[error,setError]=useState(''),[clock,setClock]=useState(Date.now());
 const {value,error:loadError,loading}=useHyperliquidBalances(api,network,orderReviewed||disabled||phase==='reviewing'||phase==='review'||phase==='submitting',refresh);
 const alive=useRef(true),generation=useRef(0),inFlight=useRef(false),submitting=useRef(false),consumed=useRef(false),trigger=useRef<HTMLButtonElement>(null),form=useRef<HTMLFormElement>(null),confirm=useRef<HTMLButtonElement>(null);
 useEffect(()=>{alive.current=true;const timer=setInterval(()=>setClock(Date.now()),1000);return()=>{alive.current=false;generation.current++;clearInterval(timer);};},[]);
 useEffect(()=>{if(phase==='edit')form.current?.querySelector<HTMLSelectElement>('select')?.focus();if(phase==='review')confirm.current?.focus();},[phase]);
 const move=(next:Phase)=>{setPhase(next);onTransferActivity(next!=='idle',next==='submitting');};
 const stale=!!value&&balanceIsStale(value.observedAt,clock),manual=!!value&&!value.shared&&value.accountMode==='disabled';
 const venues=value?orderedVenues(value.venues):[],common=venues.filter(venue=>['','xyz','para','spot'].includes(venue.dex)),other=venues.filter(venue=>!['','xyz','para','spot'].includes(venue.dex));
 const source=value?.venues.find(venue=>venue.dex===sourceDex),destination=value?.venues.find(venue=>venue.dex===destinationDex);
 const tokens=value?transferTokens(value,sourceDex,destinationDex):[],selected=tokens.find(item=>item.token===token);
 const balance=source?.balances.find(item=>item.token===token);
 const availableProblem=stale?'Balances are stale. Refresh before reviewing a transfer.':source?.error||balance?.error||balance?.availableError||(balanceIsStale(balance?.observedAt,clock)?'The source balance is stale or unavailable.':'');
 const available=availableProblem?undefined:balance?.available;
 const amountProblem=selected?transferAmountProblem(amount,available,selected.weiDecimals):'Choose a supported token.';
 const busy=phase==='reviewing'||phase==='submitting',expired=!!preview&&clock>=preview.expiresAt;
 const transferAvailable=manual&&!stale&&!loadError&&value?.network===network;

 const route=(from:string,to:string)=>{
  setSourceDex(from);setDestinationDex(to);setToken(value?transferTokens(value,from,to)[0]?.token??null:null);setAmount('');setPreview(null);setResult(null);setError('');
 };
 const open=()=>{
  if(disabled||inFlight.current||!value||!transferAvailable)return;
  onInvalidateOrder();generation.current++;consumed.current=false;
  const target=value.venues.find(venue=>venue.dex===selectedDex),funded=venues.find(venue=>!venue.shared&&!venue.error&&venue.balances.some(item=>item.available&&!item.availableError&&!item.error&&Number(item.available)>0));
  const from=funded?.dex??'',to=target&&target.dex!==from?target.dex:venues.find(venue=>venue.dex!==from&&venue.dex==='spot')?.dex??venues.find(venue=>venue.dex!==from)?.dex??'spot';
  route(from,to);move('edit');
 };
 const close=()=>{
  if(submitting.current)return;
  generation.current++;inFlight.current=false;consumed.current=false;setPreview(null);setResult(null);setError('');move('idle');onRefreshOrder();trigger.current?.focus();
 };
 const refreshBalances=()=>{
  if(disabled||busy)return;
  onInvalidateOrder();generation.current++;setPreview(null);setError('');if(phase==='review')move('edit');setRefresh(next=>next+1);onRefreshOrder();
 };
 const review=()=>{
  if(!value||!source||!destination||!selected||amountProblem||disabled||inFlight.current||!transferAvailable)return;
  const version=++generation.current,identity={account:value.accountAddress.toLowerCase(),network:value.network},request={sourceDex,destinationDex,token:selected.token,amount:normalizedTransferAmount(amount)},tokenIdentity={coin:selected.coin,tokenId:selected.tokenId};
  inFlight.current=true;consumed.current=false;setError('');move('reviewing');
  void Promise.resolve().then(()=>api.PreviewHyperliquidTransfer(request)).then(next=>{
   if(!alive.current||version!==generation.current)return;
   const now=Date.now();
   if(!next.id||next.sourceDex!==request.sourceDex||next.destinationDex!==request.destinationDex||next.token!==request.token||normalizedTransferAmount(next.amount)!==request.amount||next.accountAddress.toLowerCase()!==identity.account||next.network!==identity.network||next.network!==network||next.coin!==tokenIdentity.coin||next.tokenId!==tokenIdentity.tokenId||!Array.isArray(next.warnings)||next.warnings.some(warning=>typeof warning!=='string')||balanceIsStale(next.observedAt,now)||!Number.isFinite(next.expiresAt)||next.expiresAt<=now||next.expiresAt>now+90000)throw Error('The transfer review does not match this account, network, token and route. Refresh balances and review again.');
   setPreview(next);setClock(now);move('review');
  }).catch(reason=>{if(alive.current&&version===generation.current){setError(errorText(reason));move('edit');}}).finally(()=>{if(version===generation.current)inFlight.current=false;});
 };
 const submit=()=>{
  if(!preview||disabled||inFlight.current||consumed.current||Date.now()>=preview.expiresAt)return;
  const current=preview,version=++generation.current;inFlight.current=true;submitting.current=true;consumed.current=true;setError('');move('submitting');
  void Promise.resolve().then(()=>api.SubmitHyperliquidTransfer(current.id)).then(next=>{
   if(!alive.current||version!==generation.current)return;
   if(next.id!==current.id||!['confirmed','rejected','uncertain'].includes(next.status))throw Error('Unverified transfer result');
   setResult(next);
  }).catch(()=>{if(alive.current&&version===generation.current)setResult({id:current.id,status:'uncertain',message:'The transfer result is unknown. Check your Hyperliquid balances before creating another transfer. This transfer cannot be resent.'});}).finally(()=>{
   inFlight.current=false;submitting.current=false;if(!alive.current||version!==generation.current)return;move('done');setRefresh(next=>next+1);onRefreshOrder();
  });
 };
 const renderBalance=(venue:HyperliquidVenueBalance,item:HyperliquidTokenBalance|undefined,index:number)=>{
  const sharedSpot=venue.shared&&venue.market==='spot',sharedPerp=venue.shared&&venue.market==='perpetual';
  const problem=loadError||venue.error||item?.error||(!sharedSpot?item?.availableError:'')||(stale||item&&balanceIsStale(item.observedAt,clock)?'Balance is stale. Refresh to update.':'');
  const label=venueLabel(venue);
  const duplicateCoin=!!item&&value!.tokens.filter(token=>token.coin===item.coin).length>1,coin=item?item.coin+(duplicateCoin?` #${item.token}`:''):'';
  return <tr key={`${venue.dex}-${item?.token??index}`}><th scope="row">{label}{item&&venue.market==='spot'?` · ${coin}`:''}</th><td>{sharedPerp?'Shared with spot':coin||'—'}</td><td aria-label={`${label}${venue.market==='spot'&&item?` ${coin}`:''} ${sharedSpot?'holdings':'available'}`}>{sharedPerp?'See spot balances':!problem?metricAmount(sharedSpot?item?.total:item?.available,item?.coin)??'Unavailable':'Unavailable'}{sharedSpot&&<small>Shared spot holdings; a free trading balance is not determined.</small>}{problem&&<small>{problem}</small>}{!sharedPerp&&item?.basis&&<small>{item.basis}</small>}</td></tr>;
 };
 const rows=(items:HyperliquidVenueBalance[])=>items.flatMap(venue=>venue.balances.length?venue.balances.map((item,index)=>renderBalance(venue,item,index)):[renderBalance(venue,undefined,0)]);

 return <section className="hyperliquid-balances" aria-label="Hyperliquid balances" onKeyDown={event=>{if(event.key==='Escape'&&phase!=='idle'){event.preventDefault();event.stopPropagation();close();}}}>
  <header><h3>Hyperliquid balances</h3><button type="button" disabled={disabled||busy||loading} onClick={refreshBalances}>{loading?'Refreshing balances…':'Refresh balances'}</button></header>
  {value&&<><p className="hyperliquid-account"><strong>{value.network}</strong> · {accountModeLabel(value.accountMode)}<span>Account: {value.accountAddress}</span></p><p className="muted">All balances use this account address. Internal transfers select the balance names below; no separate DEX address is needed.</p></>}
  {!value&&!loadError&&<p role="status">Loading Hyperliquid balances…</p>}
  {loadError&&<p role="alert" className="inline-error">{loadError}</p>}
  {value&&<><table><thead><tr><th>Balance</th><th>Token</th><th>{value.shared?'Available / holdings':'Available'}</th></tr></thead><tbody>{rows(common)}</tbody></table>{other.length>0&&<details><summary>Other DEXs ({other.length})</summary><table><thead><tr><th>Balance</th><th>Token</th><th>Available</th></tr></thead><tbody>{rows(other)}</tbody></table></details>}<small>Retrieved {new Date(value.observedAt).toLocaleTimeString('en-GB',{hour12:false})}{stale?' · Stale':''}{orderReviewed?' · Paused during order review':''}</small>{value.notice&&<p className="muted">{value.notice}</p>}{value.shared&&<p className="muted">This account shares collateral. Spot lists the common holdings; independent DEX balances are not added together. Transfers between these balances are unavailable in this account mode.</p>}</>}
  {phase==='idle'&&<button ref={trigger} type="button" disabled={disabled||loading||!transferAvailable} onClick={open}>Move assets between balances…</button>}
  {phase==='idle'&&value&&!manual&&!value.shared&&<p role="status">Transfers require a verified Manual / Standard account mode.</p>}
  {phase==='idle'&&stale&&<p role="status">Refresh stale balances before moving assets.</p>}
  {(phase==='edit'||phase==='reviewing')&&<form ref={form} className="hyperliquid-transfer" aria-label="Internal transfer" onSubmit={event=>{event.preventDefault();review();}}>
   <h4>Internal transfer</h4><p>The destination is your same account. Transfers move one token without converting it.</p>
   <label>From<select aria-label="Transfer from" value={sourceDex} disabled={busy||disabled} onChange={event=>route(event.target.value,destinationDex)}>{venues.map(venue=><option key={venue.dex} value={venue.dex} disabled={venue.shared||!!venue.error}>{venueLabel(venue)}</option>)}</select></label>
   <label>To<select aria-label="Transfer to" value={destinationDex} disabled={busy||disabled} onChange={event=>route(sourceDex,event.target.value)}>{venues.map(venue=><option key={venue.dex} value={venue.dex} disabled={venue.dex===sourceDex||venue.shared||!!venue.error}>{venueLabel(venue)}</option>)}</select></label>
   <label>Token<select aria-label="Transfer token" value={selected?String(selected.token):''} disabled={busy||disabled||!tokens.length} onChange={event=>{setToken(Number(event.target.value));setAmount('');setError('');}}>{!selected&&<option value="">Choose a token</option>}{tokens.map(item=><option key={item.token} value={item.token}>{item.coin} · #{item.token}</option>)}</select></label>
   {sourceDex===destinationDex?<p role="status">Choose different source and destination balances.</p>:!tokens.length&&<p role="status">These balances have no compatible collateral token. Choose another route; this action does not exchange tokens.</p>}
   <label>Amount<span className="hyperliquid-transfer-amount"><input aria-label="Transfer amount" inputMode="decimal" maxLength={64} value={amount} disabled={busy||disabled||!selected} onChange={event=>{setAmount(event.target.value);setError('');}}/><button type="button" disabled={busy||disabled||available===undefined} onClick={()=>setAmount(available??'')}>Max</button></span></label>
   {selected&&<small>Available at source: {metricAmount(available,selected.coin)??'Unavailable'}</small>}{availableProblem&&selected&&<p role="status">{availableProblem}</p>}{amount.trim()&&amountProblem&&<p role="status">{amountProblem}</p>}
   <div className="button-row"><button type="button" disabled={busy} onClick={close}>Cancel transfer</button><button type="submit" className="primary" disabled={busy||disabled||!transferAvailable||!!amountProblem||!selected}>{phase==='reviewing'?'Preparing transfer review…':'Review transfer'}</button></div>
  </form>}
  {(phase==='review'||phase==='submitting')&&preview&&<section className="hyperliquid-transfer-review" aria-label="Transfer review"><h4>Review internal transfer</h4><dl><dt>Network</dt><dd>{preview.network}</dd><dt>Account</dt><dd>{preview.accountAddress}</dd><dt>From</dt><dd>{preview.sourceName}</dd><dt>To</dt><dd>{preview.destinationName}</dd><dt>Token</dt><dd>{preview.coin} · #{preview.token}<small>{preview.tokenId}</small></dd><dt>Amount</dt><dd>{preview.amount} {preview.coin}</dd><dt>Available before</dt><dd>{preview.availableBefore} {preview.coin}</dd><dt>Remaining at source</dt><dd>{preview.availableAfter} {preview.coin}</dd></dl>{preview.warnings.map((warning,index)=><p key={index}>{warning}</p>)}{expired&&<p role="alert">This transfer review expired. Edit and review again.</p>}<div className="button-row"><button type="button" disabled={busy} onClick={()=>{generation.current++;setPreview(null);setError('');move('edit');}}>Edit transfer</button><button ref={confirm} type="button" className="danger" disabled={busy||disabled||expired||consumed.current} onClick={submit}>{phase==='submitting'?'Transferring…':'Confirm transfer'}</button></div></section>}
  {phase==='done'&&result&&<section className="hyperliquid-transfer-result" role={result.status==='confirmed'?'status':'alert'}><h4>{result.status==='confirmed'?'Transfer confirmed':result.status==='rejected'?'Transfer rejected':'Transfer status unknown'}</h4><p>{result.message}</p>{result.status==='uncertain'&&<p>Check balances on Hyperliquid before creating another transfer. This request will not be resent.</p>}<div className="button-row"><button type="button" onClick={close}>Close transfer</button><button type="button" disabled={disabled||loading||!transferAvailable} onClick={open}>Start a new transfer</button></div></section>}
  {error&&<p role="alert" className="inline-error">{error}</p>}
 </section>;
}

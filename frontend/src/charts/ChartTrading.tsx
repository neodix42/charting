import {useEffect,useRef,useState,type RefObject} from 'react';
import {createPortal} from 'react-dom';
import type {Backend,FeedKey,TradeEstimate,TradePreview,TradeResult,TradingStatus} from '../types';
import {errorText} from '../types';
import {formatTradeLevelEstimate,tradeLevelEstimate} from '../workspace/tradeEstimates';
import {TRADING_CONFIGURED} from '../workspace/trading';
import './ChartTrading.css';

interface Ticket {key:FeedKey;side:'buy'|'sell';status:TradingStatus}
export function ChartTrading({api,feedKey:currentKey,target}:{api:Backend;feedKey:FeedKey;target:RefObject<HTMLDivElement|null>}){
 const [menu,setMenu]=useState<{x:number;y:number;key:FeedKey;status:TradingStatus}|null>(null),[ticket,setTicket]=useState<Ticket|null>(null),[quantity,setQuantity]=useState(''),[stopLoss,setStopLoss]=useState(''),[takeProfit,setTakeProfit]=useState(''),[leverage,setLeverage]=useState('1'),[preview,setPreview]=useState<TradePreview|null>(null),[result,setResult]=useState<TradeResult|null>(null),[phase,setPhase]=useState<'edit'|'previewing'|'submitting'|'done'>('edit'),[error,setError]=useState(''),[clock,setClock]=useState(Date.now());
 const [estimate,setEstimate]=useState<TradeEstimate|null>(null),[estimateError,setEstimateError]=useState(''),[estimateLoading,setEstimateLoading]=useState(false),[quoteRefresh,setQuoteRefresh]=useState(0);
 const alive=useRef(true),sequence=useRef(0),inFlight=useRef(false),submitted=useRef(false),menuRef=useRef<HTMLDivElement>(null),submitButton=useRef<HTMLButtonElement>(null);
 const keyText=JSON.stringify(currentKey),supported=currentKey.exchange==='hyperliquid'||currentKey.exchange==='dukascopy';
 useEffect(()=>{alive.current=true;return()=>{alive.current=false;sequence.current++;};},[]);
 useEffect(()=>{
  const host=target.current;if(!host||!supported)return;
  const show=(event:MouseEvent)=>{
   if(inFlight.current)return;event.preventDefault();const version=++sequence.current,key=JSON.parse(keyText) as FeedKey;
   setMenu(null);void api.GetTradingStatus(key).then(status=>{if(alive.current&&version===sequence.current&&status.configured)setMenu({x:Math.min(event.clientX,window.innerWidth-220),y:Math.min(event.clientY,window.innerHeight-150),key,status});}).catch(reason=>{if(alive.current&&version===sequence.current)setError(errorText(reason));});
  };
  host.addEventListener('contextmenu',show);return()=>{sequence.current++;host.removeEventListener('contextmenu',show);};
 },[api,keyText,supported,target]);
 useEffect(()=>{const invalidate=()=>{sequence.current++;setMenu(null);if(!submitted.current){setPreview(null);setTicket(null);setPhase('edit');}};window.addEventListener(TRADING_CONFIGURED,invalidate);return()=>window.removeEventListener(TRADING_CONFIGURED,invalidate);},[]);
 useEffect(()=>{sequence.current++;setMenu(null);if(!submitted.current){setPreview(null);setTicket(null);setPhase('edit');}},[keyText]);
 useEffect(()=>{if(!menu)return;menuRef.current?.querySelector<HTMLButtonElement>('button:not(:disabled)')?.focus();const dismiss=(event:PointerEvent)=>{if(!menuRef.current?.contains(event.target as Node))setMenu(null);};const escape=(event:KeyboardEvent)=>{if(event.key==='Escape')setMenu(null);};window.addEventListener('pointerdown',dismiss);window.addEventListener('keydown',escape);return()=>{window.removeEventListener('pointerdown',dismiss);window.removeEventListener('keydown',escape);};},[menu]);
 useEffect(()=>{if(!preview)return;setClock(Date.now());submitButton.current?.focus();const timer=setInterval(()=>setClock(Date.now()),500);return()=>clearInterval(timer);},[preview]);
 useEffect(()=>{
  if(!ticket||ticket.key.exchange!=='dukascopy'||preview||phase!=='edit')return;
  let active=true,pending=false;setEstimate(null);setEstimateError('');
  const load=async()=>{
   if(pending)return;pending=true;setEstimateLoading(true);
   try{
    const next=await api.EstimateTrade(ticket.key,ticket.side);
    if(!active||!alive.current)return;
    if(['exchange','market','instrument','timeframe'].some(field=>next.feedKey[field as keyof FeedKey]!==ticket.key[field as keyof FeedKey])||next.side!==ticket.side)throw Error('The quote does not match this order.');
    setEstimate(next);setClock(Date.now());setEstimateError(next.valuationError||'');
   }catch(reason){if(active&&alive.current){setEstimate(null);setEstimateError(errorText(reason));}}
   finally{pending=false;if(active&&alive.current)setEstimateLoading(false);}
  };
  void load();const timer=setInterval(()=>void load(),10000);
  return()=>{active=false;clearInterval(timer);};
 },[api,ticket,preview,phase,quoteRefresh]);
 useEffect(()=>{if(!ticket||ticket.key.exchange!=='dukascopy'||preview||result)return;setClock(Date.now());const timer=setInterval(()=>setClock(Date.now()),1000);return()=>clearInterval(timer);},[ticket,preview,result]);
 const start=(side:'buy'|'sell')=>{if(!menu)return;setTicket({key:menu.key,side,status:menu.status});setMenu(null);setQuantity('');setStopLoss('');setTakeProfit('');setLeverage('1');setPreview(null);setResult(null);setPhase('edit');setError('');setEstimate(null);setEstimateError('');submitted.current=false;};
 const busy=phase==='previewing'||phase==='submitting',expired=!!preview&&clock>=preview.expiresAt;
 const close=()=>{if(inFlight.current)return;sequence.current++;setTicket(null);setPreview(null);setResult(null);setError('');target.current?.focus();};
 const review=()=>{
  if(!ticket||inFlight.current||submitted.current)return;inFlight.current=true;setPhase('previewing');setError('');const version=++sequence.current;
  const request={feedKey:ticket.key,side:ticket.side,quantity:quantity.trim(),stopLoss:ticket.status.protectiveOrdersSupported?stopLoss.trim():'',takeProfit:ticket.status.protectiveOrdersSupported?takeProfit.trim():'',leverage:ticket.status.leverageSupported?Number(leverage):0};
  void api.PreviewTrade(request).then(next=>{if(!alive.current||version!==sequence.current)return;if(['exchange','market','instrument','timeframe'].some(field=>next.feedKey[field as keyof FeedKey]!==request.feedKey[field as keyof FeedKey])||next.side!==request.side)throw Error('The trade preview does not match this instrument.');setPreview(next);}).catch(reason=>{if(alive.current&&version===sequence.current)setError(errorText(reason));}).finally(()=>{inFlight.current=false;if(alive.current&&version===sequence.current)setPhase('edit');});
 };
 const submit=()=>{
  if(!preview||inFlight.current||submitted.current||Date.now()>=preview.expiresAt)return;
  submitted.current=true;inFlight.current=true;setPhase('submitting');setError('');const id=preview.id;
  void api.SubmitTrade(id).then(next=>{if(alive.current)setResult(next);}).catch(()=>{if(alive.current)setResult({status:'uncertain',orderIds:[],message:'The submission result is unknown. Check your exchange account and open orders before taking further action. This ticket cannot be resent.'});}).finally(()=>{inFlight.current=false;if(alive.current)setPhase('done');});
 };
 const edit=()=>{setPreview(null);setError('');};
 const refreshQuote=()=>{if(inFlight.current||submitted.current)return;setPreview(null);setEstimate(null);setEstimateError('');setQuoteRefresh(value=>value+1);};
 const quoteStale=!preview&&!!estimate&&(!estimate.valuationTime||!Number.isFinite(estimate.valuationTime)||clock-estimate.valuationTime>30000||estimate.valuationTime>clock+5000);
 const valuation=preview??estimate,entry=valuation?.entryPrice??'',estimateQuantity=preview?.quantity??quantity;
 const levelEstimate=(level:string)=>formatTradeLevelEstimate(tradeLevelEstimate(ticket?.side??'buy',entry,estimateQuantity,level,quoteStale?{...valuation,valuationError:'Quote stale'}:valuation??{}));
 const dukascopy=ticket?.key.exchange==='dukascopy';

 return <>
  {error&&!ticket&&createPortal(<div className="trade-context-error" role="alert">{error}<button onClick={()=>setError('')}>Close</button></div>,document.body)}
  {menu&&createPortal(<div ref={menuRef} className="trade-context-menu" role="menu" aria-label="Chart trading" style={{left:menu.x,top:menu.y}} onKeyDown={event=>{if(event.key==='ArrowDown'||event.key==='ArrowUp'){event.preventDefault();const buttons=Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>('button:not(:disabled)'));const at=buttons.indexOf(document.activeElement as HTMLButtonElement);buttons[(at+(event.key==='ArrowDown'?1:buttons.length-1))%buttons.length]?.focus();}}}>
   <span>{menu.key.instrument} · {menu.status.environment}</span><button role="menuitem" disabled={!menu.status.enabled||!menu.status.canBuy} onClick={()=>start('buy')}>Buy at market…</button><button role="menuitem" disabled={!menu.status.enabled||!menu.status.canSell} onClick={()=>start('sell')}>Sell at market…</button>{menu.status.error&&<small role="alert">{menu.status.error}</small>}
  </div>,document.body)}
  {ticket&&createPortal(<div className="modal-backdrop" onMouseDown={event=>{if(event.target===event.currentTarget)close();}}><section className="modal trade-ticket" role="dialog" aria-modal="true" aria-label="Market order" onKeyDown={event=>{if(event.key==='Escape'){event.stopPropagation();close();}if(event.key==='Tab'){const controls=Array.from(event.currentTarget.querySelectorAll<HTMLElement>('button:not(:disabled),input:not(:disabled)'));const first=controls[0],last=controls.at(-1);if(event.shiftKey&&document.activeElement===first){event.preventDefault();last?.focus();}else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();first?.focus();}}}}>
   <div className="modal-header"><h2>{ticket.side==='buy'?'Buy':'Sell'} {ticket.key.instrument}</h2><button disabled={busy} aria-label="Close market order" onClick={close}>×</button></div>
   <p><strong>{preview?.environment??ticket.status.environment}</strong> · {ticket.key.exchange} · {ticket.key.market} · Market entry</p>
   {ticket.key.exchange==='hyperliquid'&&ticket.key.market==='spot'&&<p>Spot Sell sells an owned balance; it does not open a short. Spot leverage is 1×. Hyperliquid does not support attached spot SL/TP orders.</p>}
   {ticket.key.exchange==='dukascopy'&&<p>Dukascopy leverage is set by your account contract and cannot be changed per order. Its current value appears in the review.</p>}
   {dukascopy&&!result&&<div className="trade-valuation" aria-label="Order valuation"><p><strong>Indicative entry:</strong> {entry||'Unavailable'}{quoteStale?' (stale)':''}{valuation?.accountCurrency?` · ${valuation.accountCurrency} account`:''}{valuation?.valuationTime?` · Quote ${new Date(valuation.valuationTime).toLocaleTimeString('en-GB',{hour12:false})}`:''}</p><p className="muted">Estimates use entry-price basis points and your account currency, before fees, financing and slippage.</p>{quoteStale&&<p role="status">Quote stale. Account-currency estimates are unavailable until a fresh quote arrives.</p>}{(preview?.valuationError||(!preview&&estimateError))&&<p role="status">Account-currency estimate unavailable: {preview?.valuationError||estimateError}</p>}<button type="button" disabled={busy||submitted.current||(!preview&&estimateLoading)} onClick={refreshQuote} title="Refresh the market quote and account conversion. An existing review is discarded; review again before confirming.">{estimateLoading&&!preview?'Refreshing quote…':'Refresh quote'}</button></div>}
   {!result&&<form onSubmit={event=>{event.preventDefault();if(!preview)review();}}>
    <label>Quantity ({ticket.status.quantityLabel||'base units'})<input autoFocus aria-label="Order quantity" inputMode="decimal" required value={quantity} disabled={busy||!!preview} onChange={event=>{setQuantity(event.target.value);edit();}}/></label>
    <label>Stop loss price<span className="trade-level-control"><input aria-label="Order stop loss" inputMode="decimal" value={stopLoss} disabled={busy||!!preview||!ticket.status.protectiveOrdersSupported} placeholder="Optional" onChange={event=>{setStopLoss(event.target.value);edit();}}/>{dukascopy&&<small aria-label="Stop loss estimate">{levelEstimate(preview?.stopLoss??stopLoss)}</small>}</span></label>
    <label>Take profit price<span className="trade-level-control"><input aria-label="Order take profit" inputMode="decimal" value={takeProfit} disabled={busy||!!preview||!ticket.status.protectiveOrdersSupported} placeholder="Optional" onChange={event=>{setTakeProfit(event.target.value);edit();}}/>{dukascopy&&<small aria-label="Take profit estimate">{levelEstimate(preview?.takeProfit??takeProfit)}</small>}</span></label>
    <label>{ticket.key.exchange==='dukascopy'?'Account leverage':'Leverage'}<input aria-label="Order leverage" type={ticket.status.leverageSupported?'number':'text'} min={1} max={ticket.status.maxLeverage>0?ticket.status.maxLeverage:undefined} step={1} value={!ticket.status.leverageSupported?(preview?`${preview.leverage}×`:ticket.key.exchange==='dukascopy'?'Shown in review':'1×'):leverage} disabled={busy||!!preview||!ticket.status.leverageSupported} onChange={event=>{setLeverage(event.target.value);edit();}}/></label>
    {!preview&&<button type="submit" className="primary" disabled={busy||!quantity.trim()}>{phase==='previewing'?'Preparing review…':'Review order'}</button>}
   </form>}
   {preview&&!result&&<div className="trade-review" aria-label="Order review"><dl><dt>Side</dt><dd>{preview.side.toUpperCase()}</dd><dt>Quantity</dt><dd>{preview.quantity}</dd><dt>Indicative market entry</dt><dd>{preview.entryPrice}</dd><dt>Stop loss</dt><dd>{preview.stopLoss||'None'}{dukascopy&&preview.stopLoss&&<small className="trade-review-estimate" aria-label="Review stop loss estimate">{levelEstimate(preview.stopLoss)}</small>}</dd><dt>Take profit</dt><dd>{preview.takeProfit||'None'}{dukascopy&&preview.takeProfit&&<small className="trade-review-estimate" aria-label="Review take profit estimate">{levelEstimate(preview.takeProfit)}</small>}</dd><dt>Leverage</dt><dd>{preview.leverage}×</dd></dl><p>The fill price can differ from the indicative quote.</p>{preview.warnings?.map((warning,index)=><p key={index}>{warning}</p>)}{expired&&<p role="alert">This review expired. Edit and review again before submitting.</p>}<div className="button-row"><button disabled={busy||submitted.current} onClick={edit}>Edit order</button><button ref={submitButton} className="danger" disabled={busy||expired||submitted.current} onClick={submit}>{phase==='submitting'?'Submitting…':`Confirm ${preview.side==='buy'?'Buy':'Sell'} at market`}</button></div></div>}
   {error&&<p className="inline-error" role="alert">{error}</p>}
   {result&&<div role={result.status==='submitted'?'status':'alert'}><strong>{result.status==='submitted'?'Order submitted':result.status==='partial'?'Order partially submitted':result.status==='uncertain'?'Submission result unknown':'Order rejected'}</strong><p>{result.message}</p>{result.orderIds?.length>0&&<p>Order IDs: {result.orderIds.join(', ')}</p>}{result.status!=='submitted'&&<p>Check positions and protective orders on the exchange. This ticket will not automatically retry.</p>}<button onClick={close}>Close</button></div>}
  </section></div>,document.body)}
 </>;
}

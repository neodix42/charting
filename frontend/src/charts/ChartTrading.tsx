import {TRADES_CHANGED} from '../workspace/chartTrades';
import {useEffect,useRef,useState,type RefObject} from 'react';
import {createPortal} from 'react-dom';
import type {Backend,FeedKey,Health,Instrument,TradeAccountMetrics,TradeEstimate,TradePreview,TradeResult,TradeValuation,TradingStatus} from '../types';
import {errorText} from '../types';
import {formatTradeLevelEstimate,tradeLevelEstimate,tradeValuationIsStale} from '../workspace/tradeEstimates';
import {utcDate} from '../workspace/time';
import {maximumOrderQuantity} from '../workspace/tradeQuantity';
import {formatCurrencyAmount,formatCurrencyValue} from '../workspace/currencyFormat';
import {forexRiskSizing,type ForexRiskResult} from '../workspace/forexRiskSizing';
import {useTradeMetrics} from './useTradeMetrics';
import {TradeMetricsPanel} from './TradeMetricsPanel';
import {HyperliquidBalancesPanel} from './HyperliquidBalancesPanel';
import {TRADING_CONFIGURED} from '../workspace/trading';
import {orderMarketStatus} from './orderMarketStatus';
import './ChartTrading.css';
import {isCalculatedInstrument} from '../workspace/syntheticInstruments';

const validLeverageCap=(value:number|undefined)=>Number.isSafeInteger(value)&&Number(value)>=1&&Number(value)<=1000?Number(value):0;
interface Ticket {key:FeedKey;side:'buy'|'sell';status:TradingStatus;quoteCurrency?:string}
interface RiskSnapshot {key:string;metrics:TradeAccountMetrics;valuation:TradeValuation;entryPrice:string;forex:ForexRiskResult|null}
export function ChartTrading({api,feedKey:currentKey,target,quoteCurrency,health,providerMetadata}:{api:Backend;feedKey:FeedKey;target:RefObject<HTMLDivElement|null>;quoteCurrency?:string;health?:Health;providerMetadata?:Instrument['providerMetadata']}){
 const [menu,setMenu]=useState<{x:number;y:number;key:FeedKey;status:TradingStatus}|null>(null),[ticket,setTicket]=useState<Ticket|null>(null),[quantity,setQuantity]=useState(''),[stopLoss,setStopLoss]=useState(''),[takeProfit,setTakeProfit]=useState(''),[leverage,setLeverage]=useState('1'),[preview,setPreview]=useState<TradePreview|null>(null),[result,setResult]=useState<TradeResult|null>(null),[phase,setPhase]=useState<'edit'|'previewing'|'submitting'|'done'>('edit'),[error,setError]=useState(''),[clock,setClock]=useState(Date.now());
 const [estimate,setEstimate]=useState<TradeEstimate|null>(null),[estimateError,setEstimateError]=useState(''),[estimateLoading,setEstimateLoading]=useState(false),[quoteRefresh,setQuoteRefresh]=useState(0),[leverageNotice,setLeverageNotice]=useState('');
 const [transferOpen,setTransferOpen]=useState(false),[transferBusy,setTransferBusy]=useState(false),[ticketInvalidated,setTicketInvalidated]=useState(false),transferOpenRef=useRef(false),transferBusyRef=useRef(false),ticketInvalidatedRef=useRef(false);
 const alive=useRef(true),sequence=useRef(0),inFlight=useRef(false),submitted=useRef(false),menuRef=useRef<HTMLDivElement>(null),submitButton=useRef<HTMLButtonElement>(null);
 const initializeLeverage=useRef(false);
 const [riskSnapshot,setRiskSnapshot]=useState<RiskSnapshot|null>(null),[riskAccountRevision,setRiskAccountRevision]=useState(0);
 const {metrics,request:metricsRequest,loading:metricsLoading,error:metricsError}=useTradeMetrics(api,ticket&&!preview&&phase==='edit'&&!result&&!transferOpen&&!ticketInvalidated?{feedKey:ticket.key,side:ticket.side,quantity:quantity.trim(),stopLoss:ticket.key.exchange==='hyperliquid'&&ticket.key.market==='perpetual'?stopLoss.trim():'',takeProfit:'',leverage:ticket.status.leverageSupported?Number(leverage):0}:null,quoteRefresh);
 const calculated=isCalculatedInstrument(currentKey,providerMetadata);
 const keyText=JSON.stringify(currentKey),supported=currentKey.exchange==='hyperliquid'||currentKey.exchange==='dukascopy';
 useEffect(()=>{alive.current=true;return()=>{alive.current=false;sequence.current++;};},[]);
 useEffect(()=>{
  const host=target.current;if(!host||!supported)return;
  const show=(event:MouseEvent)=>{
   if(inFlight.current||transferBusyRef.current)return;event.preventDefault();const version=++sequence.current,key=JSON.parse(keyText) as FeedKey;
   setMenu(null);if(calculated){setMenu({x:Math.min(event.clientX,window.innerWidth-220),y:Math.min(event.clientY,window.innerHeight-150),key,status:{configured:true,enabled:false,environment:'Calculated instrument',canBuy:false,canSell:false,leverageSupported:false,maxLeverage:0,protectiveOrdersSupported:false,quantityLabel:'',error:'Calculated synthetic instruments are approximate analytical series and cannot be traded.'}});return;}void api.GetTradingStatus(key).then(status=>{if(alive.current&&version===sequence.current&&status.configured)setMenu({x:Math.min(event.clientX,window.innerWidth-220),y:Math.min(event.clientY,window.innerHeight-150),key,status});}).catch(reason=>{if(alive.current&&version===sequence.current)setError(errorText(reason));});
  };
  host.addEventListener('contextmenu',show);return()=>{sequence.current++;host.removeEventListener('contextmenu',show);};
 },[api,keyText,supported,target,calculated]);
 useEffect(()=>{const invalidate=()=>{sequence.current++;setMenu(null);if(transferBusyRef.current){ticketInvalidatedRef.current=true;setTicketInvalidated(true);setError('Hyperliquid settings changed. Close and reopen this ticket before trading again.');}else if(!submitted.current){setPreview(null);setTicket(null);setPhase('edit');transferOpenRef.current=false;setTransferOpen(false);}};window.addEventListener(TRADING_CONFIGURED,invalidate);return()=>window.removeEventListener(TRADING_CONFIGURED,invalidate);},[]);
 useEffect(()=>{sequence.current++;setMenu(null);if(transferBusyRef.current){ticketInvalidatedRef.current=true;setTicketInvalidated(true);setError('The chart changed. Close and reopen this ticket before trading again.');}else if(!submitted.current){setPreview(null);setTicket(null);setPhase('edit');transferOpenRef.current=false;setTransferOpen(false);}},[keyText,calculated]);
 useEffect(()=>{if(!menu)return;menuRef.current?.querySelector<HTMLButtonElement>('button:not(:disabled)')?.focus();const dismiss=(event:PointerEvent)=>{if(!menuRef.current?.contains(event.target as Node))setMenu(null);};const escape=(event:KeyboardEvent)=>{if(event.key==='Escape')setMenu(null);};window.addEventListener('pointerdown',dismiss);window.addEventListener('keydown',escape);return()=>{window.removeEventListener('pointerdown',dismiss);window.removeEventListener('keydown',escape);};},[menu]);
 useEffect(()=>{if(!preview)return;setClock(Date.now());submitButton.current?.focus();const timer=setInterval(()=>setClock(Date.now()),500);return()=>clearInterval(timer);},[preview]);
 useEffect(()=>{
  if(!ticket||preview||phase!=='edit'||transferOpen||ticketInvalidated)return;
  let active=true,pending=false;setEstimate(null);setEstimateError('');
  const load=async()=>{
   if(pending)return;pending=true;setEstimateLoading(true);
   try{
    const next=await api.EstimateTrade(ticket.key,ticket.side);
    if(!active||!alive.current)return;
    if(['exchange','market','instrument','timeframe'].some(field=>next.feedKey[field as keyof FeedKey]!==ticket.key[field as keyof FeedKey])||next.side!==ticket.side)throw Error('The quote does not match this order.');
    if(next.referenceOnly&&ticket.key.exchange!=='dukascopy')throw Error('The provider did not return an executable quote. Refresh quote to retry.');
    if(next.referenceOnly&&tradeValuationIsStale(next,Date.now(),true))throw Error('The reference quote timestamp is invalid or older than 14 days. Refresh quote to retry.');
    setEstimate(next);setClock(Date.now());setEstimateError(next.valuationError||'');
   }catch(reason){if(active&&alive.current){setEstimate(null);setEstimateError(errorText(reason));}}
   finally{pending=false;if(active&&alive.current)setEstimateLoading(false);}
  };
  void load();const timer=setInterval(()=>void load(),10000);
  return()=>{active=false;clearInterval(timer);};
 },[api,ticket,preview,phase,quoteRefresh,transferOpen,ticketInvalidated]);
 useEffect(()=>{if(!ticket||preview||result)return;setClock(Date.now());const timer=setInterval(()=>setClock(Date.now()),1000);return()=>clearInterval(timer);},[ticket,preview,result]);
 useEffect(()=>{if(!ticket||ticket.key.exchange!=='hyperliquid'||ticket.key.market!=='perpetual'||preview||tradeValuationIsStale(estimate,Date.now()))return;const cap=validLeverageCap(estimate?.maxLeverage);if(cap&&initializeLeverage.current){initializeLeverage.current=false;setLeverage(String(cap));setLeverageNotice(`Default leverage set to ${cap}×, the current asset maximum.`);}else if(cap&&Number(leverage)>cap){setLeverage(String(cap));setLeverageNotice(`Leverage adjusted to ${cap}×, the current asset maximum.`);}},[ticket,estimate,leverage,preview]);
 const start=(side:'buy'|'sell')=>{if(!menu)return;initializeLeverage.current=menu.key.exchange==='hyperliquid'&&menu.key.market==='perpetual';setRiskSnapshot(null);setTicket({key:menu.key,side,status:menu.status,quoteCurrency});setMenu(null);setQuantity('');setStopLoss('');setTakeProfit('');setLeverage('1');setPreview(null);setResult(null);setPhase('edit');setError('');setEstimate(null);setEstimateError('');setLeverageNotice('');transferOpenRef.current=false;transferBusyRef.current=false;ticketInvalidatedRef.current=false;setTransferOpen(false);setTransferBusy(false);setTicketInvalidated(false);submitted.current=false;};
 const busy=phase==='previewing'||phase==='submitting'||transferBusy,expired=!!preview&&clock>=preview.expiresAt;
 const close=()=>{if(inFlight.current||transferBusyRef.current)return;sequence.current++;setTicket(null);setPreview(null);setResult(null);setError('');transferOpenRef.current=false;setTransferOpen(false);target.current?.focus();};
 const review=()=>{
  if(!ticket||inFlight.current||transferOpenRef.current||ticketInvalidatedRef.current||submitted.current||referenceQuote||(requiresLeverageCap&&(!leverageCap||quoteStale||!Number.isInteger(Number(leverage))||Number(leverage)<1||Number(leverage)>leverageCap)))return;inFlight.current=true;setPhase('previewing');setError('');const version=++sequence.current;
  const request={feedKey:ticket.key,side:ticket.side,quantity:quantity.trim(),stopLoss:ticket.status.protectiveOrdersSupported?stopLoss.trim():'',takeProfit:ticket.status.protectiveOrdersSupported?takeProfit.trim():'',leverage:ticket.status.leverageSupported?Number(leverage):0};
  void api.PreviewTrade(request).then(next=>{if(!alive.current||version!==sequence.current)return;if(['exchange','market','instrument','timeframe'].some(field=>next.feedKey[field as keyof FeedKey]!==request.feedKey[field as keyof FeedKey])||next.side!==request.side)throw Error('The trade preview does not match this instrument.');setPreview(next);}).catch(reason=>{if(alive.current&&version===sequence.current)setError(errorText(reason));}).finally(()=>{inFlight.current=false;if(alive.current&&version===sequence.current)setPhase('edit');});
 };
 const submit=()=>{
  if(!preview||inFlight.current||transferOpenRef.current||ticketInvalidatedRef.current||submitted.current||Date.now()>=preview.expiresAt||(requiresLeverageCap&&(!validLeverageCap(preview.maxLeverage)||preview.leverage>Number(preview.maxLeverage)||preview.leverage<1)))return;
  submitted.current=true;inFlight.current=true;setPhase('submitting');setError('');const id=preview.id;
  void api.SubmitTrade(id).then(next=>{if(alive.current)setResult(next);}).catch(()=>{if(alive.current)setResult({status:'uncertain',orderIds:[],message:'The submission result is unknown. Check your exchange account and open orders before taking further action. This ticket cannot be resent.'});}).finally(()=>{window.dispatchEvent(new Event(TRADES_CHANGED));inFlight.current=false;if(alive.current)setPhase('done');});
 };
 const edit=()=>{setPreview(null);setError('');};
 const refreshQuote=()=>{if(inFlight.current||transferOpenRef.current||ticketInvalidatedRef.current||submitted.current)return;setPreview(null);setEstimate(null);setEstimateError('');setQuoteRefresh(value=>value+1);};
 const dukascopy=ticket?.key.exchange==='dukascopy',requiresLeverageCap=ticket?.key.exchange==='hyperliquid'&&ticket.key.market==='perpetual';
 const referenceQuote=!preview&&!!dukascopy&&estimate?.referenceOnly===true;
 const quoteStale=!preview&&tradeValuationIsStale(estimate,clock,!!dukascopy);
 const valuation=preview??estimate,entry=valuation?.entryPrice??'',estimateQuantity=preview?.quantity??quantity;
 const leverageCap=quoteStale?0:validLeverageCap(valuation?.maxLeverage);
 const capUnavailable=requiresLeverageCap&&!leverageCap;
 const estimateSupported=!!ticket?.status.protectiveOrdersSupported;
 const valuationProblem=preview?.valuationError||(!preview&&estimateError)||(valuation&&(!valuation.accountCurrency?.trim()||!Number.isFinite(Number(valuation.profitPerPriceUnit))||Number(valuation.profitPerPriceUnit)<=0)?dukascopy?'No usable account-currency conversion was returned. Refresh quote; if your installed JForex bridge lacks valuation support, update the installed bridge and restart CryptoDesk.':'The provider did not return a verified collateral conversion. Refresh quote to retry.':'');
 const levelEstimate=(level:string)=>!level.trim()?'Enter a price to estimate':formatTradeLevelEstimate(tradeLevelEstimate(ticket?.side??'buy',entry,estimateQuantity,level,quoteStale?{...valuation,valuationError:'Quote stale'}:valuation??{}),dukascopy?'Account amount unavailable':'Collateral amount unavailable');
 const maximum=maximumOrderQuantity(preview??metrics,!preview&&metricsLoading,preview?'':metricsError,clock,!!preview);
 const sourceMetrics=preview??metrics;
 const slPrice=Number(preview?.stopLoss??stopLoss),entryNumber=Number(entry);
 const liveForexSizing=dukascopy&&stopLoss.trim()&&!referenceQuote&&!quoteStale&&!valuationProblem&&!metricsLoading&&!metricsError&&sourceMetrics?.equity!==undefined&&sourceMetrics.usedMargin!==undefined
  ?forexRiskSizing({equity:Number(sourceMetrics.equity),usedMargin:Number(sourceMetrics.usedMargin),
   entry:entryNumber,stop:slPrice,side:ticket!.side,profitPerPriceUnit:Number(valuation?.profitPerPriceUnit),
   marginPerUnit:Number(sourceMetrics.marginPerUnit),step:Number(sourceMetrics.quantityStep),
   maxQuantity:Number(maximum.text),marginCutUsePercent:sourceMetrics.marginCutUsePercent,accountEquityStopLoss:Number(sourceMetrics.accountEquityStopLoss??0),targetLevel:150}):null;
 const printable=(n:number)=>Number.isFinite(n)?n.toLocaleString('en-US',{maximumFractionDigits:2}):'—';
 const riskNumbers=[sourceMetrics?.riskSizingQuantity,sourceMetrics?.riskSizingSafeQuantity,sourceMetrics?.riskSizingLoss,sourceMetrics?.riskSizingLiquidationPrice];
 const liveHyperliquidSizing=requiresLeverageCap&&!quoteStale&&!referenceQuote&&!metricsLoading&&!metricsError&&!sourceMetrics?.riskSizingError&&!!sourceMetrics?.availableBalanceCurrency?.trim()&&sourceMetrics?.metricsTime&&Number.isFinite(sourceMetrics.metricsTime)&&sourceMetrics.metricsTime<=clock+5000&&(!!preview||clock-sourceMetrics.metricsTime<=30000)&&(!!preview||metrics?.leverage===Number(leverage))&&riskNumbers.every(value=>!!value?.trim()&&Number.isFinite(Number(value))&&Number(value)>=0)&&Number.isFinite(Number(sourceMetrics?.equity))&&Number(sourceMetrics?.equity)>0;
 const riskKey=JSON.stringify([ticket,stopLoss.trim(),requiresLeverageCap?leverage:'',riskAccountRevision]);
 const matchingRiskRequest=!!preview||(!!metricsRequest&&metricsRequest.quantity===quantity.trim()&&metricsRequest.leverage===(ticket?.status.leverageSupported?Number(leverage):0)&&(!requiresLeverageCap||metricsRequest.stopLoss===stopLoss.trim()));
 useEffect(()=>{
  // Retain the first usable advisory result for these inputs. Quotes and account
  // polls stay live elsewhere; they must not replace a suggestion under the pointer.
  if(!ticket||!stopLoss.trim()||transferOpen||ticketInvalidated||!matchingRiskRequest||!sourceMetrics||!valuation||(!liveHyperliquidSizing&&(!liveForexSizing||liveForexSizing.error)))return;
  setRiskSnapshot(current=>current?.key===riskKey?current:{key:riskKey,metrics:{...sourceMetrics},valuation:{...valuation},entryPrice:requiresLeverageCap?sourceMetrics.entryPrice:entry,forex:liveForexSizing});
 },[ticket,stopLoss,transferOpen,ticketInvalidated,matchingRiskRequest,sourceMetrics,valuation,liveHyperliquidSizing,liveForexSizing,riskKey,requiresLeverageCap,entry]);
 const sizing=riskSnapshot?.key===riskKey?riskSnapshot:null,forexSizing=sizing?.forex??null;
 const riskMetrics=sizing?.metrics,riskValuation=sizing?.valuation,hyperliquidSizing=!!sizing&&!!requiresLeverageCap;
 const entered=Number(preview?.quantity??quantity),unitLoss=Math.abs(Number(sizing?.entryPrice)-slPrice)*Number(riskValuation?.profitPerPriceUnit);
 const proposedLoss=unitLoss*entered,proposedMargin=Number(riskMetrics?.usedMargin)+entered*Number(riskMetrics?.marginPerUnit);
 const proposedLevel=proposedMargin>0?(Number(riskMetrics?.equity)-proposedLoss)/proposedMargin*100:NaN;
 const price=(value:string|undefined)=>formatCurrencyValue(value,ticket?.quoteCurrency)??value??'Unavailable';
 const marketStatus=orderMarketStatus(health,providerMetadata,clock);
 const riskMoney=(value:string|number|undefined)=>formatCurrencyAmount(value,dukascopy?riskValuation?.accountCurrency:riskMetrics?.availableBalanceCurrency)??'—';
 const riskBasis=riskMetrics?.riskSizingBasis?.replace(/^At entry (\d+(?:\.\d+)?), stop (\d+(?:\.\d+)?) and isolated leverage (\d+)×:/,(_,snapshotEntry:string,snapshotStop:string,snapshotLeverage:string)=>`At entry ${price(snapshotEntry)}, stop ${price(snapshotStop)} and isolated leverage ${snapshotLeverage}×:`);

 return <>
  {error&&!ticket&&createPortal(<div className="trade-context-error" role="alert">{error}<button onClick={()=>setError('')}>Close</button></div>,document.body)}
  {menu&&createPortal(<div ref={menuRef} className="trade-context-menu" role="menu" aria-label="Chart trading" style={{left:menu.x,top:menu.y}} onKeyDown={event=>{if(event.key==='ArrowDown'||event.key==='ArrowUp'){event.preventDefault();const buttons=Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>('button:not(:disabled)'));const at=buttons.indexOf(document.activeElement as HTMLButtonElement);buttons[(at+(event.key==='ArrowDown'?1:buttons.length-1))%buttons.length]?.focus();}}}>
   <span>{menu.key.instrument} · {menu.status.environment}</span><button role="menuitem" disabled={!menu.status.enabled||!menu.status.canBuy} onClick={()=>start('buy')}>Buy at market…</button><button role="menuitem" disabled={!menu.status.enabled||!menu.status.canSell} onClick={()=>start('sell')}>Sell at market…</button>{menu.status.error&&<small role="alert">{menu.status.error}</small>}
  </div>,document.body)}
  {ticket&&createPortal(<div className="modal-backdrop trade-ticket-backdrop" onMouseDown={event=>{if(event.target===event.currentTarget)close();}}><section className="modal trade-ticket" role="dialog" aria-modal="true" aria-label="Market order" onKeyDown={event=>{if(event.key==='Escape'){event.stopPropagation();close();}if(event.key==='Tab'){const controls=Array.from(event.currentTarget.querySelectorAll<HTMLElement>('button:not(:disabled),input:not(:disabled),select:not(:disabled),[tabindex="0"]'));const first=controls[0],last=controls.at(-1);if(event.shiftKey&&document.activeElement===first){event.preventDefault();last?.focus();}else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();first?.focus();}}}}>
   <div className="modal-header"><h2>{ticket.side==='buy'?'Buy':'Sell'} {ticket.key.instrument}</h2><button disabled={busy} aria-label="Close market order" onClick={close}>×</button></div>
   <p><strong>{dukascopy?`${(preview?.environment??ticket.status.environment)==='live'?'Live':'Demo'} account`:preview?.environment??ticket.status.environment}</strong> · {ticket.key.exchange} · {ticket.key.market} · Market entry{dukascopy&&<> · <span aria-label="Order market status" title={marketStatus.basis}>{marketStatus.text}</span></>}</p>
   {ticket.key.exchange==='hyperliquid'&&ticket.key.market==='spot'&&<p>Spot Sell sells an owned balance; it does not open a short. Spot leverage is 1×. Hyperliquid does not support attached spot SL/TP orders.</p>}
   {ticket.key.exchange==='dukascopy'&&<p>Dukascopy leverage is set by your account contract and cannot be changed per order. Its current value appears in the review.</p>}
   {!result&&<div className="trade-ticket-body"><div className="trade-ticket-main">
   {!result&&<div className="trade-valuation" aria-label="Order valuation"><p><strong>{referenceQuote?'Last market quote (reference only):':'Indicative entry:'}</strong> {entry?price(entry):'Unavailable'}{quoteStale?' (stale)':''}{valuation?.accountCurrency?` · ${valuation.accountCurrency} ${dukascopy?'account':ticket.key.market==='spot'?'quote currency':'collateral'}`:''}{valuation?.valuationTime?` · Quote ${referenceQuote?utcDate(valuation.valuationTime):new Date(valuation.valuationTime).toLocaleTimeString('en-GB',{hour12:false})}`:''}</p><p className="muted">Estimates use entry-price basis points and {dukascopy?'your account currency':'the asset’s settlement currency'}, before fees, financing and slippage.</p>{valuation?.valuationNotice&&<p className="muted" title={valuation.valuationSource}>{valuation.valuationNotice}</p>}{referenceQuote&&<p role="status">Last market quotes are for planning estimates only. A fresh executable quote is required before reviewing an order.</p>}{quoteStale&&<p role="status">Quote stale. Currency estimates are unavailable until a fresh quote arrives.</p>}{valuationProblem&&<p role="status">{dukascopy?'Account-currency':'Collateral-currency'} estimate unavailable: {valuationProblem}</p>}<button type="button" disabled={busy||transferOpen||ticketInvalidated||submitted.current||(!preview&&estimateLoading)} onClick={refreshQuote} title="Refresh the market quote and account conversion. An existing review is discarded; review again before confirming.">{estimateLoading&&!preview?'Refreshing quote…':'Refresh quote'}</button></div>}
   {!result&&<TradeMetricsPanel value={preview??metrics} entryPrice={preview?.entryPrice??metrics?.entryPrice} quoteCurrency={ticket.quoteCurrency} loading={!preview&&metricsLoading} error={preview?'':metricsError} now={clock} reviewed={!!preview}/>}
   {!result&&ticket.key.exchange==='hyperliquid'&&<HyperliquidBalancesPanel api={api} network={ticket.status.environment} selectedDex={ticket.key.market==='spot'?'spot':ticket.key.instrument.includes(':')?ticket.key.instrument.split(':')[0]:''} orderReviewed={!!preview} disabled={busy||submitted.current||ticketInvalidated} onInvalidateOrder={()=>{setRiskSnapshot(null);setRiskAccountRevision(value=>value+1);setPreview(null);setEstimate(null);setEstimateError('');setEstimateLoading(false);setError('');}} onTransferActivity={(open,active)=>{transferOpenRef.current=open;transferBusyRef.current=active;setTransferOpen(open);setTransferBusy(active);}} onRefreshOrder={()=>setQuoteRefresh(value=>value+1)}/>}
   {!result&&<form onSubmit={event=>{event.preventDefault();if(!preview)review();}}>
    <label><span>Quantity ({ticket.status.quantityLabel||'base units'}), maximum (<span aria-label="Maximum quantity" title={maximum.reason}>{maximum.text}</span>)</span><input autoFocus aria-label="Order quantity" inputMode="decimal" required value={quantity} disabled={busy||!!preview} onChange={event=>{setQuantity(event.target.value);edit();}}/></label>
    <label className="trade-level-field"><span>Stop loss price</span><input aria-label="Order stop loss" inputMode="decimal" value={stopLoss} disabled={busy||!!preview||!ticket.status.protectiveOrdersSupported} placeholder="Optional" onChange={event=>{setRiskSnapshot(null);setStopLoss(event.target.value);edit();}}/>{estimateSupported&&<small tabIndex={0} aria-label="Stop loss estimate">{levelEstimate(preview?.stopLoss??stopLoss)}</small>}</label>
    <label className="trade-level-field"><span>Take profit price</span><input aria-label="Order take profit" inputMode="decimal" value={takeProfit} disabled={busy||!!preview||!ticket.status.protectiveOrdersSupported} placeholder="Optional" onChange={event=>{setTakeProfit(event.target.value);edit();}}/>{estimateSupported&&<small tabIndex={0} aria-label="Take profit estimate">{levelEstimate(preview?.takeProfit??takeProfit)}</small>}</label>
    <label>{dukascopy?'Account leverage':'Leverage'}{requiresLeverageCap?<span className="trade-leverage-control"><select aria-label="Order leverage" value={leverageCap?String(preview?.leverage??leverage):''} disabled={busy||!!preview||!leverageCap} onChange={event=>{initializeLeverage.current=false;setRiskSnapshot(null);setLeverage(event.target.value);setLeverageNotice('');edit();}}>{!leverageCap&&<option value="">{estimateLoading?'Loading asset limit…':'Asset limit unavailable'}</option>}{Array.from({length:leverageCap},(_,index)=><option key={index+1} value={index+1}>{index+1}×</option>)}</select><small aria-label="Maximum leverage">{leverageCap?`Max ${leverageCap}×`:'Maximum unavailable'}</small></span>:<input aria-label="Order leverage" type="text" value={preview?`${preview.leverage}×`:dukascopy?'Shown in review':'1×'} disabled/>}</label>
    {leverageNotice&&<p role="status">{leverageNotice}</p>}
    {capUnavailable&&<p role="status">The asset’s current leverage limit is unavailable. Refresh quote to retry before reviewing an order.</p>}
    {!preview&&<button type="submit" className="primary" disabled={busy||transferOpen||ticketInvalidated||!quantity.trim()||capUnavailable||referenceQuote}>{phase==='previewing'?'Preparing review…':'Review order'}</button>}
   </form>}
   </div><div className="trade-ticket-sizing">
   {!result&&dukascopy&&<section className="trade-risk-sizing" aria-label="Forex risk and margin">
    <strong>Forex position sizing · target loss 50% of equity</strong>
    {!stopLoss.trim()?<p>Enter a stop loss to calculate a quantity for the target risk.</p>:forexSizing&&!forexSizing.error?<>
     <p>50% risk quantity: <b>{forexSizing.riskQuantity.toLocaleString('en-US',{maximumFractionDigits:20})}</b> base units.
      Safe quantity (150% margin level at stop, liquidity capped): <b>{forexSizing.safeQuantity.toLocaleString('en-US',{maximumFractionDigits:20})}</b>.</p>
     <p>Safe quantity loss at stop: <b>{riskMoney(forexSizing.riskAmount)}</b> ({printable(100*forexSizing.riskAmount/Number(riskMetrics?.equity))}% equity).</p>
     <p>Projected equity: <b>{riskMoney(forexSizing.projectedEquity)}</b> · margin used: <b>{riskMoney(forexSizing.projectedUsedMargin)}</b> · margin level at stop: <b>{printable(forexSizing.projectedMarginLevel)}%</b>.</p>
     <p>Broker margin cut: <b>{printable(forexSizing.cutLevel??NaN)}%</b> margin level · account equity stop: <b>{riskMoney(riskMetrics?.accountEquityStopLoss)}</b> · target buffer: <b>150%</b>.</p>
     <p className="muted">Sizing snapshot: entry {price(sizing!.entryPrice)} · stop {price(stopLoss.trim())} · {utcDate(riskMetrics!.metricsTime!)}. Change stop loss to recalculate.</p>
     <button type="button" disabled={busy||transferOpen||ticketInvalidated||!!preview||forexSizing.safeQuantity<=0} onClick={()=>{setQuantity(String(forexSizing.safeQuantity));edit();}}>Use safe risk-sized quantity</button>
     {!forexSizing.canTargetRisk&&<p role="status">A 50% loss target is not achievable with the requested 150% margin buffer and the current executable-size limit. The suggested size risks less.</p>}
     {quantity.trim()&&Number.isFinite(proposedLevel)&&<p role={proposedLevel<150?'alert':'status'}>Entered position at snapshot prices: estimated loss {riskMoney(proposedLoss)}; margin level at stop {printable(proposedLevel)}%. {proposedLevel<150?'Below buffer — reduce quantity.':'Above buffer under current assumptions.'}</p>}
    </>:<p role="status">{liveForexSizing?.error||'Risk sizing requires a fresh account snapshot, stop, conversion rate and verified liquidity/margin limits. Refresh quote to retry.'}</p>}
    <small>Advisory calculation only. Market gaps, commissions, existing exposure, weekend leverage, dynamic tiers and broker changes can cause earlier margin cut or a larger loss. Partial execution cannot be ruled out.</small>
   </section>}
   {!result&&requiresLeverageCap&&<section className="trade-risk-sizing" aria-label="Hyperliquid risk and margin">
    <strong>Position sizing · target loss 50% of equity</strong>
    {!stopLoss.trim()?<p>Enter a stop loss to calculate a quantity for the target risk.</p>:hyperliquidSizing?<>
     <p>50% risk quantity: <b>{riskMetrics!.riskSizingQuantity}</b> base units. Risk-sized quantity (margin and liquidity capped): <b>{riskMetrics!.riskSizingSafeQuantity}</b>.</p>
     <p>Estimated loss at stop: <b>{riskMoney(riskMetrics!.riskSizingLoss)}</b> ({printable(100*Number(riskMetrics!.riskSizingLoss)/Number(riskMetrics!.equity))}% equity).</p>
     <p>Projected equity at stop: <b>{riskMoney(Number(riskMetrics!.equity)-Number(riskMetrics!.riskSizingLoss))}</b> · Estimated isolated liquidation price: <b>{Number(riskMetrics!.riskSizingLiquidationPrice)>0?price(riskMetrics!.riskSizingLiquidationPrice):'No positive price threshold'}</b>.</p>
     <p>{riskBasis}</p>
     <p className="muted">Sizing snapshot: entry {price(sizing!.entryPrice)} · stop {price(stopLoss.trim())} · leverage {leverage}× · {utcDate(riskMetrics!.metricsTime!)}. Change stop loss or leverage to recalculate.</p>
     <button type="button" disabled={busy||transferOpen||ticketInvalidated||!!preview||Number(riskMetrics!.riskSizingSafeQuantity)<=0} onClick={()=>{setQuantity(riskMetrics!.riskSizingSafeQuantity!);edit();}}>Use risk-sized quantity</button>
     {!riskMetrics!.riskSizingCanTarget&&<p role="status">The 50% loss target exceeds the current margin, liquidity or liquidation constraint. The suggested quantity risks less.</p>}
    </>:<p role="status">{sourceMetrics?.riskSizingError||'Risk sizing requires a fresh account snapshot, stop, selected leverage and verified executable-size and liquidation limits. Refresh quote to retry.'}</p>}
    <small>Advisory estimates for isolated perpetuals. Stops must precede estimated liquidation at the selected leverage. Fees, funding, market gaps and changing liquidity can change the final loss or fill.</small>
   </section>}
   </div></div>}
   {preview&&!result&&<div className="trade-review" aria-label="Order review"><dl><dt>Side</dt><dd>{preview.side.toUpperCase()}</dd><dt>Quantity</dt><dd>{preview.quantity}</dd><dt>Indicative market entry</dt><dd title={preview.entryPrice}>{price(preview.entryPrice)}</dd><dt>Stop loss</dt><dd>{preview.stopLoss?price(preview.stopLoss):'None'}{estimateSupported&&preview.stopLoss&&<small className="trade-review-estimate" aria-label="Review stop loss estimate">{levelEstimate(preview.stopLoss)}</small>}</dd><dt>Take profit</dt><dd>{preview.takeProfit?price(preview.takeProfit):'None'}{estimateSupported&&preview.takeProfit&&<small className="trade-review-estimate" aria-label="Review take profit estimate">{levelEstimate(preview.takeProfit)}</small>}</dd><dt>Leverage</dt><dd>{preview.leverage}×</dd></dl><p>The fill price can differ from the indicative quote.</p>{preview.warnings?.map((warning,index)=><p key={index}>{warning}</p>)}{expired&&<p role="alert">This review expired. Edit and review again before submitting.</p>}<div className="button-row"><button disabled={busy||submitted.current} onClick={edit}>Edit order</button><button ref={submitButton} className="danger" disabled={busy||expired||submitted.current||capUnavailable} onClick={submit}>{phase==='submitting'?'Submitting…':`Confirm ${preview.side==='buy'?'Buy':'Sell'} at market`}</button></div></div>}
   {error&&<p className="inline-error" role="alert">{error}</p>}
   {result&&<div role={result.status==='submitted'?'status':'alert'}><strong>{result.status==='submitted'?'Order submitted':result.status==='partial'?'Order partially submitted':result.status==='uncertain'?'Submission result unknown':'Order rejected'}</strong><p>{result.message}</p>{result.orderIds?.length>0&&<p>Order IDs: {result.orderIds.join(', ')}</p>}{result.status!=='submitted'&&<p>Check positions and protective orders on the exchange. This ticket will not automatically retry.</p>}<button onClick={close}>Close</button></div>}
  </section></div>,document.body)}
 </>;
}

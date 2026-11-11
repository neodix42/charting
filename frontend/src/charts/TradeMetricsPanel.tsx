import type {TradeAccountMetrics} from '../types';

const amountFormat=new Intl.NumberFormat('en-US',{minimumFractionDigits:2,maximumFractionDigits:8});
const smallAmountFormat=new Intl.NumberFormat('en-US',{maximumSignificantDigits:8});
export function metricAmount(value:string|undefined,currency:string|undefined):string|null {
 if(!value?.trim()||!currency?.trim()||!Number.isFinite(Number(value)))return null;
 const number=Number(value),formatted=number!==0&&Math.abs(number)<.01?smallAmountFormat.format(number):amountFormat.format(number);
 return `${formatted} ${currency.trim()}`;
}
const price=(value:string|undefined)=>value?.trim()&&Number.isFinite(Number(value))&&Number(value)>0?new Intl.NumberFormat('en-US',{maximumSignificantDigits:12}).format(Number(value)):null;

export function TradeMetricsPanel({value,loading,error,now,reviewed,entryPrice}:{value:TradeAccountMetrics|null;loading:boolean;error:string;now:number;reviewed:boolean;entryPrice?:string}){
 const stale=!!value&&(!value.metricsTime||!Number.isFinite(value.metricsTime)||value.metricsTime>now+5000||(!reviewed&&now-value.metricsTime>30000));
 const sharedReason=error||(stale?(!value?.metricsTime?'The provider did not return an account-data timestamp. Refresh quote to retry.':'Account data is stale. Refresh quote to retry.'):reviewed&&!value?'Account metrics are unavailable for this reviewed order.':'');
 const balance=!sharedReason&&!value?.availableBalanceError?metricAmount(value?.availableBalance,value?.availableBalanceCurrency):null;
 const margin=!sharedReason&&!value?.marginError?metricAmount(value?.marginRequired,value?.marginRequiredCurrency):null;
 const liquidation=!sharedReason&&!value?.liquidationError&&value?.liquidationKind==='estimate'?price(value.liquidationPrice):null;
 const notApplicable=value?.liquidationKind==='not-applicable';
 const unavailable=loading&&!value?'Loading…':'Unavailable';
 return <section className="trade-account-metrics" tabIndex={0} aria-label={reviewed?'Reviewed account metrics':'Account metrics'}>
  <div className="trade-metrics-heading"><strong>{reviewed?'Account snapshot for this review':'Account and margin'}</strong>{loading&&<small role="status">Updating account data…</small>}{value?.metricsTime&&Number.isFinite(value.metricsTime)?<small>As of {new Date(value.metricsTime).toLocaleTimeString('en-GB',{hour12:false})}</small>:null}</div>
  {(margin!==null||liquidation!==null)&&price(entryPrice)&&<small className="trade-metrics-entry" aria-label="Metrics projection entry">Projection entry: {entryPrice}</small>}
  <dl>
   <div><dt>Available balance</dt><dd aria-label="Available balance">{balance??unavailable}</dd>{(value?.availableBalanceError||sharedReason)&&<p>{value?.availableBalanceError||sharedReason}</p>}{value?.availableBalanceBasis&&<small>{value.availableBalanceBasis}</small>}
    {!sharedReason&&<div className="trade-account-details">{(['accountBalance','equity','usedMargin'] as const).map(field=>{const amount=metricAmount(value?.[field],value?.availableBalanceCurrency);return amount!==null?<small key={field}>{({accountBalance:'Account balance',equity:'Equity',usedMargin:'Used margin'})[field]}: {amount}</small>:null;})}</div>}
   </div>
   <div><dt>Margin required <small>(estimate)</small></dt><dd aria-label="Margin required">{margin??unavailable}</dd>{(value?.marginError||sharedReason)&&<p>{value?.marginError||sharedReason}</p>}{value?.marginBasis&&<small>{value.marginBasis}</small>}</div>
   <div><dt>Liquidation price {value?.liquidationKind==='estimate'&&<small>(estimate)</small>}</dt><dd aria-label="Liquidation price">{notApplicable?'Not applicable':liquidation??unavailable}</dd>{(value?.liquidationError||sharedReason)&&<p>{value?.liquidationError||sharedReason}</p>}{value?.liquidationBasis&&<small>{value.liquidationBasis}</small>}</div>
  </dl>
 </section>;
}

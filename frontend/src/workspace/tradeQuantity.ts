import type {TradeAccountMetrics} from '../types';

/** Preserve the provider's rounded-down decimal: display rounding could exceed its limit. */
export function maximumOrderQuantity(value:TradeAccountMetrics|null,loading:boolean,error:string,now:number,reviewed=false):{text:string;reason:string}{
 if(error)return {text:'Unavailable',reason:error};
 if(!value)return {text:loading?'Loading…':'Unavailable',reason:'No verified maximum quantity is available. Refresh quote to retry.'};
 if(!value.metricsTime||!Number.isFinite(value.metricsTime)||value.metricsTime>now+5000||(!reviewed&&now-value.metricsTime>30000))return {text:'Unavailable',reason:'Account data is stale. Refresh quote to retry.'};
 if(value.maximumQuantityError)return {text:'Unavailable',reason:value.maximumQuantityError};
 const quantity=value.maximumQuantity?.trim();
 if(!quantity||!/^\d+(?:\.\d+)?$/.test(quantity)||!Number.isFinite(Number(quantity)))return {text:'Unavailable',reason:'The provider did not return a verified maximum quantity. Refresh quote to retry.'};
 return {text:quantity,reason:value.maximumQuantityBasis||'Estimated from the current account and executable market snapshot.'};
}

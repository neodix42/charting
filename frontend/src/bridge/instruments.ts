import type {Backend,Exchange,Instrument} from '../types';

const catalog=new Map<Exchange,Promise<Instrument[]>>();
export const DUCASCOPY_CONFIGURED='cryptodesk:ducascopy-configured';
export function clearInstrumentCatalog(exchange:Exchange){catalog.delete(exchange);}
export function instruments(api:Backend,exchange:Exchange){
 const cached=catalog.get(exchange);if(cached)return cached;
 const request=api.ListInstruments(exchange).then(items=>{if(items.some(item=>item.providerMetadata?.catalogWarning)&&catalog.get(exchange)===request)catalog.delete(exchange);return items;}).catch(error=>{if(catalog.get(exchange)===request)catalog.delete(exchange);throw error;});
 catalog.set(exchange,request);return request;
}

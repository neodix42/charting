import type {Backend,Exchange,Instrument} from '../types';

const catalog=new Map<Exchange,Promise<Instrument[]>>(),snapshots=new Map<Exchange,Instrument[]>();
const revisions=new Map<Exchange,number>(),refreshes=new Map<Exchange,Promise<Instrument[]>>();
const listeners=new Map<Exchange,Set<(items:Instrument[])=>void>>();
export const DUCASCOPY_CONFIGURED='cryptodesk:ducascopy-configured';
export function clearInstrumentCatalog(exchange:Exchange){revisions.set(exchange,(revisions.get(exchange)??0)+1);catalog.delete(exchange);snapshots.delete(exchange);refreshes.delete(exchange);for(const listener of listeners.get(exchange)??[])listener([]);}
export function cachedInstruments(exchange:Exchange){return snapshots.get(exchange);}
export function subscribeInstrumentCatalog(exchange:Exchange,listener:(items:Instrument[])=>void){let group=listeners.get(exchange);if(!group){group=new Set();listeners.set(exchange,group);}group.add(listener);return()=>{group.delete(listener);if(!group.size&&listeners.get(exchange)===group)listeners.delete(exchange);};}
function publish(exchange:Exchange,items:Instrument[]){snapshots.set(exchange,items);for(const listener of listeners.get(exchange)??[])listener(items);return items;}
export function instruments(api:Backend,exchange:Exchange){
 const cached=catalog.get(exchange);if(cached)return cached;
 const request=api.ListInstruments(exchange).then(items=>{if(catalog.get(exchange)!==request)throw Error('Instrument settings changed; retry discovery.');publish(exchange,items);if(items.some(item=>item.providerMetadata?.catalogWarning))catalog.delete(exchange);return items;}).catch(error=>{if(catalog.get(exchange)===request)catalog.delete(exchange);throw error;});
 catalog.set(exchange,request);return request;
}
export function refreshDucascopyCatalog(api:Backend,force=false){
 const exchange='dukascopy',pending=refreshes.get(exchange);if(pending)return pending;
 const revision=revisions.get(exchange)??0;
 const request=api.RefreshDucascopyInstruments(force).then(items=>{if((revisions.get(exchange)??0)!==revision)throw Error('JForex account changed; retry discovery.');catalog.set(exchange,Promise.resolve(items));return publish(exchange,items);}).finally(()=>{if(refreshes.get(exchange)===request)refreshes.delete(exchange);});
 refreshes.set(exchange,request);return request;
}

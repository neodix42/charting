import type {Instrument} from '../types';

/** Match the public monitor's tradable universe, preserving native identities. */
export function publicLiveAlertAssets(items:Instrument[]):Instrument[]{
 const seen=new Set<string>(),eligible:Instrument[]=[];
 for(const item of items){
  if(!['spot','perpetual'].includes(item.market)||!['available','trading','online'].includes(item.availability.toLowerCase()))continue;
  const key=`${item.market}|${item.nativeId}`;if(seen.has(key))continue;seen.add(key);eligible.push(item);
  if(eligible.length>20000)throw Error('Public asset discovery exceeds the supported 20,000-market bound.');
 }
 return eligible;
}
export const publicLiveAlertAssetLabel=(item:Instrument)=>item.providerMetadata?.displaySymbol||item.nativeId;

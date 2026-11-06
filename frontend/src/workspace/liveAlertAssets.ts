import type {Instrument} from '../types';
import type {LiveAlertAssetRef,LiveAlertDefinition} from './screenerLiveAlerts';

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


export const liveAlertAssetKey=(asset:LiveAlertAssetRef)=>JSON.stringify([asset.market??'',asset.symbol]);
export const liveAlertAssetSelection=(entry:LiveAlertDefinition)=>entry.assetSelections?.find(selection=>selection.dataSource===entry.dataSource)?.assets;
/** Undefined restores all eligible assets, including future catalog additions. */
export function withLiveAlertAssets(entry:LiveAlertDefinition,assets:LiveAlertAssetRef[]|undefined):LiveAlertDefinition {
 const selections=(entry.assetSelections??[]).filter(selection=>selection.dataSource!==entry.dataSource);
 if(assets)selections.push({dataSource:entry.dataSource,assets:structuredClone(assets)});
 const {assetSelections:_,...base}=entry;
 return selections.length?{...base,assetSelections:selections}:base;
}

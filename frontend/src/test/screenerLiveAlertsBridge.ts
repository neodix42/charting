import type {ScreenerCatalog} from '../types';
import type {LiveAlertAsset,LiveAlertsConfig,LiveAlertsInfo,ScreenerLiveAlertsBackend} from '../workspace/screenerLiveAlerts';
import {initialLiveAlertsConfig} from '../workspace/screenerLiveAlerts';

/** Deterministic fixture only. Never connects to Telegram or market providers. */
export function screenerLiveAlertsBridge(catalog:()=>ScreenerCatalog):ScreenerLiveAlertsBackend{
 const key='cryptodesk-explicit-test-live-alerts';
 let info:LiveAlertsInfo={config:JSON.parse(sessionStorage.getItem(key)||'null')??initialLiveAlertsConfig(),running:false,lastRun:0,nextRun:0,sent:0,status:'Disabled',assets:[],progress:0,currentAsset:'',recent:[]};
 let reads=0,checks=0,statusDelay=0,gate:Promise<void>|undefined,release:(()=>void)|undefined;
 const writes:LiveAlertsConfig[]=[];let errors:Partial<Record<'read'|'save'|'check',string>>={};
 const assets=():LiveAlertAsset[]=>{
  const files=(window as unknown as {__testEnrichmentFiles?:()=>Array<{symbol?:string;timeframe?:string;error?:string}>}).__testEnrichmentFiles?.()??[];
  const grouped=new Map<string,LiveAlertAsset>();
  for(const file of files){
   if(!file.symbol||!file.timeframe||!['d1','h1'].includes(file.timeframe)||file.error)continue;
   let asset=grouped.get(file.symbol);
   if(!asset){const group=catalog().groups.find(group=>group.instruments.some(item=>item.id===file.symbol)),instrument=group?.instruments.find(item=>item.id===file.symbol);asset={symbol:file.symbol,name:instrument?.name??file.symbol.toUpperCase(),category:group?.name??'Other',periods:[]};grouped.set(file.symbol,asset);}
   if(!asset.periods.includes(file.timeframe))asset.periods.push(file.timeframe);
  }
  return [...grouped.values()];
 };
 const snapshot=()=>structuredClone({...info,assets:assets()});
 const api:ScreenerLiveAlertsBackend={
  async GetScreenerLiveAlerts(){reads++;const response=snapshot();if(statusDelay)await new Promise(resolve=>setTimeout(resolve,statusDelay));if(errors.read)throw Error(errors.read);return response;},
  async ConfigureScreenerLiveAlerts(value){
   writes.push(structuredClone(value));if(gate)await gate;if(errors.save)throw Error(errors.save);
   if(value.enabled&&(!value.conditionIds.length||!value.periods.length||!value.symbols.length))throw Error('Select conditions, periods and downloaded assets.');
   info={...info,config:structuredClone(value),running:false,status:value.enabled?'Waiting for next closed bar':'Disabled',nextRun:value.enabled?Date.now()+3600000:0};
   sessionStorage.setItem(key,JSON.stringify(info.config));return snapshot();
  },
  async CheckScreenerLiveAlerts(){checks++;if(errors.check)throw Error(errors.check);if(!info.config.enabled)throw Error('Start Live Alerts before checking.');info={...info,running:true,status:'Checking selected assets',currentAsset:info.config.symbols[0]??'',progress:0};return snapshot();},
 };
 Object.assign(window,{
  __testLiveAlertsInfo:()=>snapshot(),__testLiveAlertsWrites:()=>structuredClone(writes),__testLiveAlertsReads:()=>reads,__testLiveAlertsChecks:()=>checks,
  __testSetLiveAlertsInfo:(value:Partial<LiveAlertsInfo>)=>{info={...info,...structuredClone(value)};if(value.config)sessionStorage.setItem(key,JSON.stringify(value.config));},
  __testLiveAlertsErrors:(value:typeof errors)=>{errors=structuredClone(value);},
  __testHoldLiveAlertsSaves:()=>{gate=new Promise(resolve=>{release=resolve;});},__testReleaseLiveAlertsSaves:()=>{release?.();gate=undefined;},
  __testLiveAlertsReadDelay:(delay:number)=>{statusDelay=delay;},
 });
 return api;
}

import type {SavedAnalysisCondition} from '../workspace/screenerAnalysis';
import {savedConditionProfitability} from '../workspace/screenerAnalysis';
import type {ScreenerCatalog} from '../types';
import type {LiveAlertAsset,LiveAlertConditionScope,LiveAlertsConfig,LiveAlertsInfo,ScreenerLiveAlertsBackend} from '../workspace/screenerLiveAlerts';
import {initialLiveAlertsConfig,liveAlertDefinitions} from '../workspace/screenerLiveAlerts';

/** Deterministic fixture only. Never connects to Telegram or market providers. */
export function screenerLiveAlertsBridge(catalog:()=>ScreenerCatalog):ScreenerLiveAlertsBackend{
 const key='cryptodesk-explicit-test-live-alerts';
 const stored=JSON.parse(sessionStorage.getItem(key)||'null');
 const historyKey=key+'-history',history=JSON.parse(sessionStorage.getItem(historyKey)||'null');
 const restored:LiveAlertsConfig=stored?structuredClone(stored):initialLiveAlertsConfig();
 let info:LiveAlertsInfo={checks:history?.checks??[],savedConditionsRevision:0,conditionScopes:[],config:restored,running:false,lastRun:history?.lastRun??0,nextRun:0,sent:history?.sent??0,status:'Disabled',assets:[],progress:0,currentAsset:'',recent:history?.recent??[]};
 let reads=0,checks=0,statusDelay=0,gate:Promise<void>|undefined,release:(()=>void)|undefined;
 const writes:LiveAlertsConfig[]=[];let errors:Partial<Record<'read'|'save'|'check',string>>={};
 const savedConditions=()=>(window as unknown as {__testSavedAnalysisConditions?:()=>SavedAnalysisCondition[]}).__testSavedAnalysisConditions?.()??[];
 const persistHistory=()=>sessionStorage.setItem(historyKey,JSON.stringify({checks:info.checks,recent:info.recent,sent:info.sent,lastRun:info.lastRun}));
 const assets=():LiveAlertAsset[]=>{
  const files=(window as unknown as {__testEnrichmentFiles?:()=>Array<{symbol?:string;timeframe?:string;error?:string}>}).__testEnrichmentFiles?.()??[];
  const grouped=new Map<string,LiveAlertAsset>();
  for(const file of files){
   if(!file.symbol||!file.timeframe||!['w1','d1','h1'].includes(file.timeframe)||file.error)continue;
   let asset=grouped.get(file.symbol);
   if(!asset){const group=catalog().groups.find(group=>group.instruments.some(item=>item.id===file.symbol)),instrument=group?.instruments.find(item=>item.id===file.symbol);asset={symbol:file.symbol,name:instrument?.name??file.symbol.toUpperCase(),category:group?.name??'Other',periods:[]};grouped.set(file.symbol,asset);}
   if(!asset.periods.includes(file.timeframe))asset.periods.push(file.timeframe);
  }
  return [...grouped.values()];
 };
 const scopes=(config:LiveAlertsConfig=info.config):LiveAlertConditionScope[]=>{
  const saved=savedConditions(),inventory=assets();
  return liveAlertDefinitions(config,saved).map(entry=>{
   const original=saved.find(condition=>condition.id===entry.conditionId),condition=original?{...original,request:{...original.request,period:entry.period}}:undefined;
   const period=entry.period,dataSource=entry.dataSource;
   if(!condition)return{conditionId:entry.id,alertId:entry.id,savedConditionId:entry.conditionId,period,dataSource,targets:[],error:'The saved condition is unavailable.'};
   if(dataSource!=='dukascopy')return{conditionId:entry.id,alertId:entry.id,savedConditionId:entry.conditionId,period,dataSource,allAssets:true,targets:[],error:''};
   const targets=(condition.analysis?.assets??[]).filter(row=>row.matches>0&&['w1','d1','h1'].includes(row.timeframe)&&(period==='all'||row.timeframe===period)&&inventory.some(asset=>asset.symbol===row.symbol&&asset.periods.includes(row.timeframe))&&((condition.request.profitabilityFilter??0)===0||!!savedConditionProfitability(condition,row.symbol,row.timeframe))).map(row=>{const asset=inventory.find(asset=>asset.symbol===row.symbol)!,profitability=savedConditionProfitability(condition,row.symbol,row.timeframe);return{symbol:row.symbol,name:asset.name,category:asset.category,period:row.timeframe,...(profitability?{profitability}:{})};});
   const error=!condition.analysis?`${condition.name} has no recorded Analysis matching assets; apply this saved condition in Analysis before enabling its ${period.toUpperCase()} alerts`:!targets.length?(condition.request.profitabilityFilter??0)>0?`${condition.name} has no downloaded ${period.toUpperCase()} assets meeting its recorded ${condition.request.profitabilityFilter}% profitability filter; reapply its saved rules in Analysis`:`${condition.name} has no downloaded ${period.toUpperCase()} assets in its recorded Analysis matches; reapply its saved rules in Analysis`:'';
   return{conditionId:entry.id,alertId:entry.id,savedConditionId:entry.conditionId,period,dataSource,targets,error};
  });
 };
 const nextClose=(period:string,now=Date.now())=>{
  const interval=period==='w1'?604800000:period==='d1'?86400000:3600000,origin=period==='w1'?4*86400000:0;
  return (Math.floor((now-origin)/interval)+1)*interval+origin;
 };
 const nextScopeClose=(scope:ReturnType<typeof scopes>[number]|undefined)=>Math.min(...(scope?.targets.length?scope.targets.map(target=>nextClose(target.period)):[nextClose(scope?.period==='w1'?'w1':scope?.period==='h1'||scope?.period==='all'?'h1':'d1')]));
 const definitions=(config:LiveAlertsConfig=info.config)=>liveAlertDefinitions(config,savedConditions());
 const snapshot=()=>{
  const saved=savedConditions(),available=new Set(saved.map(condition=>condition.id)),all=definitions(),entries=all.filter(entry=>available.has(entry.conditionId)),ids=new Set(entries.map(entry=>entry.id));
  if(entries.length!==all.length||info.checks.some(check=>!ids.has(check.alertId??check.conditionId))){info={...info,config:{enabled:info.config.enabled,alerts:entries},checks:info.checks.filter(check=>ids.has(check.alertId??check.conditionId))};sessionStorage.setItem(key,JSON.stringify(info.config));persistHistory();}
  const conditionScopes=scopes({...info.config,alerts:entries}),checks=entries.map(entry=>{
   const previous=info.checks.find(check=>(check.alertId??check.conditionId)===entry.id),scope=conditionScopes.find(scope=>scope.conditionId===entry.id);
   return{conditionId:entry.id,alertId:entry.id,savedConditionId:entry.conditionId,name:entry.name,assetsScanned:previous?.assetsScanned??0,lastChecked:previous?.lastChecked??0,nextCheck:info.config.enabled&&entry.enabled?(previous?.nextCheck||nextScopeClose(scope)):0,sentAlerts:previous?.sentAlerts??0,status:previous?.status??'never' as const,manual:previous?.manual??false,error:previous?.error??'',...(previous?.timingHint?{timingHint:previous.timingHint}:{})};
  });
  return structuredClone({...info,config:{enabled:info.config.enabled,alerts:entries},checks,savedConditionsRevision:(window as unknown as {__testSavedAnalysisConditionsRevision?:()=>number}).__testSavedAnalysisConditionsRevision?.()??0,conditionScopes,assets:assets()});
 };
 const api:ScreenerLiveAlertsBackend={
  async GetScreenerLiveAlerts(){reads++;const response=snapshot();if(statusDelay)await new Promise(resolve=>setTimeout(resolve,statusDelay));if(errors.read)throw Error(errors.read);return response;},
  async ConfigureScreenerLiveAlerts(value){
   writes.push(structuredClone(value));if(gate)await gate;if(errors.save)throw Error(errors.save);
   const entries=definitions(value),selected=scopes(value).filter(scope=>entries.some(entry=>entry.id===scope.conditionId&&entry.enabled));
   if(value.enabled&&selected.some(scope=>scope.error||(!scope.allAssets&&!scope.targets.length)))throw Error('Choose valid periods and saved conditions for enabled alerts.');
   const nextRun=selected.length?Math.min(...selected.map(nextScopeClose)):0;
   info={...info,config:{enabled:value.enabled,alerts:structuredClone(entries)},error:'',running:false,status:value.enabled?'Waiting for next closed bar':'Disabled',nextRun:value.enabled?nextRun:0};
   sessionStorage.setItem(key,JSON.stringify(info.config));return snapshot();
  },
  async CheckScreenerLiveAlerts(){checks++;if(errors.check)throw Error(errors.check);if(!info.config.enabled)throw Error('Start Live Alerts before checking.');const started=Date.now(),selected=new Set(definitions().filter(entry=>entry.enabled).map(entry=>entry.id)),attempts=snapshot().checks.filter(check=>selected.has(check.conditionId)).map(check=>({...check,assetsScanned:0,lastChecked:started,status:'running' as const,manual:true,error:''}));info={...info,checks:[...info.checks.filter(check=>!selected.has(check.conditionId)),...attempts],running:true,status:'Checking enabled alerts',currentAsset:scopes().filter(scope=>selected.has(scope.conditionId)).flatMap(scope=>scope.targets)[0]?.symbol??'',progress:0};persistHistory();return snapshot();},
 };
 Object.assign(window,{
  __testLiveAlertsInfo:()=>snapshot(),__testLiveAlertsWrites:()=>structuredClone(writes),__testLiveAlertsReads:()=>reads,__testLiveAlertsChecks:()=>checks,
  __testSetLiveAlertsInfo:(value:Partial<LiveAlertsInfo>)=>{info={...info,...structuredClone(value)};if(value.config)sessionStorage.setItem(key,JSON.stringify(value.config));persistHistory();},
  __testLiveAlertsErrors:(value:typeof errors)=>{errors=structuredClone(value);},
  __testHoldLiveAlertsSaves:()=>{gate=new Promise(resolve=>{release=resolve;});},__testReleaseLiveAlertsSaves:()=>{release?.();gate=undefined;},
  __testLiveAlertsReadDelay:(delay:number)=>{statusDelay=delay;},
 });
 return api;
}

import type {SavedAnalysisCondition} from '../workspace/screenerAnalysis';
import {savedConditionProfitability} from '../workspace/screenerAnalysis';
import type {ScreenerCatalog} from '../types';
import type {LiveAlertAsset,LiveAlertsConfig,LiveAlertsInfo,ScreenerLiveAlertsBackend} from '../workspace/screenerLiveAlerts';
import {initialLiveAlertsConfig} from '../workspace/screenerLiveAlerts';

/** Deterministic fixture only. Never connects to Telegram or market providers. */
export function screenerLiveAlertsBridge(catalog:()=>ScreenerCatalog):ScreenerLiveAlertsBackend{
 const key='cryptodesk-explicit-test-live-alerts';
 const stored=JSON.parse(sessionStorage.getItem(key)||'null');
 const historyKey=key+'-history',history=JSON.parse(sessionStorage.getItem(historyKey)||'null');
 const restored=stored?{enabled:stored.conditionIds?stored.enabled:false,conditionIds:stored.conditionIds??[...new Set((stored.conditionPeriods??[]).map((row:{conditionId:string})=>row.conditionId))]}:initialLiveAlertsConfig();
 let info:LiveAlertsInfo={checks:history?.checks??[],savedConditionsRevision:0,conditionScopes:[],config:restored,running:false,lastRun:history?.lastRun??0,nextRun:0,sent:history?.sent??0,status:'Disabled',assets:[],progress:0,currentAsset:'',recent:history?.recent??[]};
 let reads=0,checks=0,statusDelay=0,gate:Promise<void>|undefined,release:(()=>void)|undefined;
 const writes:LiveAlertsConfig[]=[];let errors:Partial<Record<'read'|'save'|'check',string>>={};
 const savedConditions=()=>(window as unknown as {__testSavedAnalysisConditions?:()=>SavedAnalysisCondition[]}).__testSavedAnalysisConditions?.()??[];
 const persistHistory=()=>sessionStorage.setItem(historyKey,JSON.stringify({checks:info.checks,recent:info.recent,sent:info.sent,lastRun:info.lastRun}));
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
 const scopes=()=>{
  const saved=savedConditions(),inventory=assets();
  return saved.map(condition=>{
   const period=condition.request.period||'all',targets=(condition.analysis?.assets??[]).filter(row=>row.matches>0&&['d1','h1'].includes(row.timeframe)&&(period==='all'||row.timeframe===period)&&inventory.some(asset=>asset.symbol===row.symbol&&asset.periods.includes(row.timeframe))&&((condition.request.profitabilityFilter??0)===0||!!savedConditionProfitability(condition,row.symbol,row.timeframe))).map(row=>{const asset=inventory.find(asset=>asset.symbol===row.symbol)!,profitability=savedConditionProfitability(condition,row.symbol,row.timeframe);return{symbol:row.symbol,name:asset.name,category:asset.category,period:row.timeframe,...(profitability?{profitability}:{})};});
   const error=!condition.analysis?`${condition.name} has no recorded Analysis matching assets; apply this saved condition in Analysis before enabling its ${period.toUpperCase()} alerts`:!targets.length?(condition.request.profitabilityFilter??0)>0?`${condition.name} has no downloaded ${period.toUpperCase()} assets meeting its recorded ${condition.request.profitabilityFilter}% profitability filter; reapply its saved rules in Analysis`:`${condition.name} has no downloaded ${period.toUpperCase()} assets in its recorded Analysis matches; reapply its saved rules in Analysis`:'';
   return{conditionId:condition.id,period,targets,error};
  });
 };
 const snapshot=()=>{
  const saved=savedConditions(),available=new Set(saved.map(condition=>condition.id)),ids=info.config.conditionIds.filter(id=>available.has(id));
  if(ids.length!==info.config.conditionIds.length||info.checks.some(check=>!available.has(check.conditionId))){info={...info,config:{enabled:info.config.enabled&&ids.length>0,conditionIds:ids},checks:info.checks.filter(check=>available.has(check.conditionId))};sessionStorage.setItem(key,JSON.stringify(info.config));persistHistory();}
  const conditionScopes=scopes(),checks=ids.map(conditionId=>{
   const previous=info.checks.find(check=>check.conditionId===conditionId),scope=conditionScopes.find(scope=>scope.conditionId===conditionId),interval=scope?.targets.some(target=>target.period==='h1')?3600000:86400000;
   return{conditionId,name:saved.find(condition=>condition.id===conditionId)!.name,assetsScanned:previous?.assetsScanned??0,lastChecked:previous?.lastChecked??0,nextCheck:info.config.enabled?(previous?.nextCheck||(Math.floor(Date.now()/interval)+1)*interval):0,sentAlerts:previous?.sentAlerts??0,status:previous?.status??'never' as const,manual:previous?.manual??false,error:previous?.error??'',...(previous?.timingHint?{timingHint:previous.timingHint}:{})};
  });
  return structuredClone({...info,checks,savedConditionsRevision:(window as unknown as {__testSavedAnalysisConditionsRevision?:()=>number}).__testSavedAnalysisConditionsRevision?.()??0,conditionScopes,assets:assets()});
 };
 const api:ScreenerLiveAlertsBackend={
  async GetScreenerLiveAlerts(){reads++;const response=snapshot();if(statusDelay)await new Promise(resolve=>setTimeout(resolve,statusDelay));if(errors.read)throw Error(errors.read);return response;},
  async ConfigureScreenerLiveAlerts(value){
   writes.push(structuredClone(value));if(gate)await gate;if(errors.save)throw Error(errors.save);
   const selected=scopes().filter(scope=>value.conditionIds.includes(scope.conditionId));
   if(value.enabled&&(!value.conditionIds.length||selected.length!==value.conditionIds.length||selected.some(scope=>scope.error||!scope.targets.length)))throw Error('Select saved conditions with analyzed matching assets.');
   const interval=selected.some(scope=>scope.targets.some(target=>target.period==='h1'))?3600000:86400000;
   info={...info,config:structuredClone(value),error:'',running:false,status:value.enabled?'Waiting for next closed bar':'Disabled',nextRun:value.enabled?(Math.floor(Date.now()/interval)+1)*interval:0};
   sessionStorage.setItem(key,JSON.stringify(info.config));return snapshot();
  },
  async CheckScreenerLiveAlerts(){checks++;if(errors.check)throw Error(errors.check);if(!info.config.enabled)throw Error('Start Live Alerts before checking.');const started=Date.now(),selected=new Set(info.config.conditionIds),attempts=snapshot().checks.map(check=>({...check,assetsScanned:0,lastChecked:started,status:'running' as const,manual:true,error:''}));info={...info,checks:[...info.checks.filter(check=>!selected.has(check.conditionId)),...attempts],running:true,status:'Checking selected assets',currentAsset:scopes().filter(scope=>info.config.conditionIds.includes(scope.conditionId)).flatMap(scope=>scope.targets)[0]?.symbol??'',progress:0};persistHistory();return snapshot();},
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

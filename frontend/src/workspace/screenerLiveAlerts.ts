import type {SavedAnalysisCondition,SavedConditionProfitability} from './screenerAnalysis';
export interface LiveAlertConditionPeriod {conditionId:string;period:string}
export type LiveAlertDataSource='dukascopy'|'hyperliquid'|'binance'|'bybit';
export interface LiveAlertConditionSource {conditionId:string;source:LiveAlertDataSource}
export type LiveAlertPeriod='h1'|'d1'|'w1'|'all';
export interface LiveAlertDefinition {id:string;name:string;conditionId:string;period:LiveAlertPeriod;dataSource:LiveAlertDataSource;enabled:boolean}
export interface LiveAlertsConfig {enabled:boolean;alerts?:LiveAlertDefinition[];conditionIds?:string[];conditionSources?:LiveAlertConditionSource[]}
export interface LiveAlertAsset {symbol:string;name:string;category:string;periods:string[]}
export interface LiveAlertOutcome {alertId?:string;alertName?:string;savedConditionId?:string;savedConditionName?:string;conditionId?:string;dataSource?:LiveAlertDataSource;market?:string;confirmedRecipients?:number;assetName:string;symbol:string;timeframe:string;time:number;conditionName:string;status:'sent'|'failed'|'claimed'|'uncertain';message:string;profitability?:SavedConditionProfitability|null}
export interface LiveAlertTarget {symbol:string;name:string;category:string;period:string;profitability?:SavedConditionProfitability|null}
export interface LiveAlertConditionScope {alertId?:string;savedConditionId?:string;conditionId:string;period:string;dataSource?:LiveAlertDataSource;allAssets?:boolean;targets:LiveAlertTarget[];error:string}
export type LiveAlertProgressState='queued'|'running'|'completed'|'failed'|'cancelled'|'skipped';
export interface LiveAlertCheck {progress?:number;phase?:string;currentAsset?:string;progressState?:LiveAlertProgressState;progressMode?:'manual'|'scheduled';alertId?:string;savedConditionId?:string;conditionId:string;name:string;assetsScanned:number;lastChecked:number;nextCheck:number;sentAlerts:number;status:'never'|'running'|'completed'|'failed'|'cancelled';manual:boolean;error:string;timingHint?:string}
/** Follow the visible alert list even when a compact Stop response orders IDs. */
export function orderedLiveAlertChecks(entries:LiveAlertDefinition[],checks:LiveAlertCheck[]):LiveAlertCheck[]{
 const byID=new Map(checks.map(check=>[check.alertId??check.conditionId,check]));
 return entries.map(entry=>byID.get(entry.id)??{alertId:entry.id,savedConditionId:entry.conditionId,conditionId:entry.id,name:entry.name,assetsScanned:0,lastChecked:0,nextCheck:0,sentAlerts:0,status:'never',manual:false,error:''});
}
export function liveAlertCheckProgress(check:LiveAlertCheck):{state:string;phase:string;value:number|undefined}{
 const state=check.progressState??check.status,labels:Record<string,string>={never:'Not checked yet',queued:'Queued',running:'Running',completed:'Completed',failed:'Failed',cancelled:'Cancelled',skipped:'Skipped'};
 // Go omits zero-valued progress; a runtime state still proves that zero.
 const reported=check.progress??(check.progressState?0:undefined);
 const value=typeof reported==='number'&&Number.isFinite(reported)?Math.max(0,Math.min(100,reported)):state==='queued'?0:undefined;
 return{state,phase:check.phase||labels[state]||state,value};
}
export interface LiveAlertsInfo {checks:LiveAlertCheck[];savedConditionsRevision:number;conditionScopes:LiveAlertConditionScope[];config:LiveAlertsConfig;running:boolean;lastRun:number;nextRun:number;sent:number;error?:string;status:string;assets:LiveAlertAsset[];progress:number;currentAsset:string;recent:LiveAlertOutcome[]}
export interface ScreenerLiveAlertsBackend {
 GetScreenerLiveAlerts():Promise<LiveAlertsInfo>;
 ConfigureScreenerLiveAlerts(config:LiveAlertsConfig):Promise<LiveAlertsInfo>;
 CheckScreenerLiveAlerts():Promise<LiveAlertsInfo>;
}
export const liveAlertDataSources:ReadonlyArray<{source:LiveAlertDataSource;label:string}>=[{source:'dukascopy',label:'Dukascopy'},{source:'hyperliquid',label:'Hyperliquid'},{source:'binance',label:'Binance'},{source:'bybit',label:'Bybit'}];
export function liveAlertConditionSource(config:LiveAlertsConfig,id:string):LiveAlertDataSource{return config.conditionSources?.find(row=>row.conditionId===id)?.source??'dukascopy';}
export function liveAlertSourceLabel(source:LiveAlertDataSource):string{return liveAlertDataSources.find(row=>row.source===source)?.label??source;}
export function withLiveAlertSource(config:LiveAlertsConfig,id:string,source:LiveAlertDataSource):LiveAlertsConfig {
 const conditionSources=(config.conditionSources??[]).filter(row=>row.conditionId!==id);
 if(source!=='dukascopy')conditionSources.push({conditionId:id,source});
 const {conditionSources:_,...base}=config;
 return conditionSources.length?{...base,conditionSources}:base;
}
export const initialLiveAlertsConfig=():LiveAlertsConfig=>({enabled:false,alerts:[]});
/** Legacy entries keep their identity and enabled state until an explicit edit. */
export function liveAlertDefinitions(config:LiveAlertsConfig,conditions:SavedAnalysisCondition[]):LiveAlertDefinition[]{
 if(config.alerts)return config.alerts;
 return (config.conditionIds??[]).map(id=>{const saved=conditions.find(condition=>condition.id===id),period=saved?.request.period;return{id,name:saved?.name??'Unavailable saved condition',conditionId:id,period:period==='h1'||period==='d1'||period==='w1'?period:'all',dataSource:liveAlertConditionSource(config,id),enabled:true};});
}
export const configWithLiveAlerts=(config:LiveAlertsConfig,alerts:LiveAlertDefinition[]):LiveAlertsConfig=>({enabled:config.enabled,alerts});
export function defaultLiveAlertPeriod(condition:SavedAnalysisCondition|undefined):LiveAlertPeriod{const period=condition?.request.period;return period==='h1'||period==='d1'||period==='w1'?period:'h1';}
export function liveAlertPeriodLabel(period:string):string {
 switch(period){case 'w1':return 'Weekly (W1)';case 'd1':return 'Daily (D1)';case 'h1':return 'Hourly (H1)';case '':case 'all':return 'All analyzed periods (W1 / D1 / H1)';default:return period.toUpperCase();}
}
export function liveAlertBarClose(timestamp:number,period:string):number {
 const duration:Record<string,number>={w1:604800000,d1:86400000,h1:3600000};
 return timestamp+(duration[period]??0);
}

/** User edits retain the master state; loading settings never starts delivery. */
export class LiveAlertsConfigWriter {
 private pending?:LiveAlertsConfig;private flight?:Promise<void>;
 constructor(private persist:(config:LiveAlertsConfig)=>Promise<LiveAlertsInfo>,private onError:(error:unknown)=>void){}
 get busy(){return !!this.pending||!!this.flight;}
 edit(value:LiveAlertsConfig){this.pending=structuredClone(value);void this.flush().catch(this.onError);}
 async flush():Promise<void>{
  if(this.flight){await this.flight;if(this.pending)return this.flush();return;}
  const next=this.pending;if(!next)return;this.pending=undefined;
  this.flight=(async()=>{try{await this.persist(next);}catch(error){if(!this.pending)this.pending=next;throw error;}finally{this.flight=undefined;}})();
  await this.flight;if(this.pending)return this.flush();
 }
}

/** Also cleans old persisted checks whose joined errors were flattened to one line. */
export function liveAlertErrorText(message:string):string {
 const pending='the newly closed bar is not yet available from the provider';
 let text=[...new Set(message.split(/\r?\n/).map(line=>line.replace(/\s+/g,' ').trim()).filter(Boolean))].join('\n');
 while(text.includes(`${pending} ${pending}`))text=text.replaceAll(`${pending} ${pending}`,pending);
 return text;
}

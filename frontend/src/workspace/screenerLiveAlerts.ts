import type {SavedConditionProfitability} from './screenerAnalysis';
export interface LiveAlertConditionPeriod {conditionId:string;period:string}
export interface LiveAlertsConfig {enabled:boolean;conditionIds:string[]}
export interface LiveAlertAsset {symbol:string;name:string;category:string;periods:string[]}
export interface LiveAlertOutcome {assetName:string;symbol:string;timeframe:string;time:number;conditionName:string;status:'sent'|'failed'|'claimed'|'uncertain';message:string;profitability?:SavedConditionProfitability|null}
export interface LiveAlertTarget {symbol:string;name:string;category:string;period:string;profitability?:SavedConditionProfitability|null}
export interface LiveAlertConditionScope {conditionId:string;period:string;targets:LiveAlertTarget[];error:string}
export interface LiveAlertCheck {conditionId:string;name:string;assetsScanned:number;lastChecked:number;nextCheck:number;sentAlerts:number;status:'never'|'running'|'completed'|'failed'|'cancelled';manual:boolean;error:string;timingHint?:string}
export interface LiveAlertsInfo {checks:LiveAlertCheck[];savedConditionsRevision:number;conditionScopes:LiveAlertConditionScope[];config:LiveAlertsConfig;running:boolean;lastRun:number;nextRun:number;sent:number;error?:string;status:string;assets:LiveAlertAsset[];progress:number;currentAsset:string;recent:LiveAlertOutcome[]}
export interface ScreenerLiveAlertsBackend {
 GetScreenerLiveAlerts():Promise<LiveAlertsInfo>;
 ConfigureScreenerLiveAlerts(config:LiveAlertsConfig):Promise<LiveAlertsInfo>;
 CheckScreenerLiveAlerts():Promise<LiveAlertsInfo>;
}
export const initialLiveAlertsConfig=():LiveAlertsConfig=>({enabled:false,conditionIds:[]});

/** Only disabled drafts autosave; starting delivery is an explicit action. */
export class LiveAlertsConfigWriter {
 private pending?:LiveAlertsConfig;private flight?:Promise<void>;
 constructor(private persist:(config:LiveAlertsConfig)=>Promise<LiveAlertsInfo>,private onError:(error:unknown)=>void){}
 get busy(){return !!this.pending||!!this.flight;}
 edit(value:LiveAlertsConfig){this.pending=structuredClone({...value,enabled:false});void this.flush().catch(this.onError);}
 async flush():Promise<void>{
  if(this.flight){await this.flight;if(this.pending)return this.flush();return;}
  const next=this.pending;if(!next)return;this.pending=undefined;
  this.flight=(async()=>{try{await this.persist(next);}catch(error){if(!this.pending)this.pending=next;throw error;}finally{this.flight=undefined;}})();
  await this.flight;if(this.pending)return this.flush();
 }
}

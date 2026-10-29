export interface PredictionSettings {model:string;modelDirectory:string}
export interface PredictionSelection {asset:string;period:string;contextLength:number;horizon:number;forecastId?:string}
export interface PredictionDataset {id:string;symbol:string;name:string;period:string;filename:string}
export interface PredictionObservation {time:number;close:number}
export interface PredictionPoint {step:number;lower:number;median:number;upper:number}
export interface PredictionSummary {id:string;createdAt:number;asOf:number;model:string;symbol:string;assetName:string;period:string;contextLength:number;horizon:number}
export interface PredictionForecast extends PredictionSummary {modelRevision:string;sourceFile:string;sourceHash:string;history:PredictionObservation[];points:PredictionPoint[]}
export interface PredictionInfo {
 settings:PredictionSettings;models:Array<{id:string;name:string}>;defaultModelDirectory:string;forecastDirectory:string;selection:PredictionSelection;
 runtime:{ready:boolean;modelReady:boolean;runtimeReady:boolean;error?:string};active:boolean;
 event:{phase:string;progress:number;message:string;downloaded?:number;total?:number};error?:string;forecasts:PredictionSummary[];
}
export interface PredictionsBackend {
 GetPredictions():Promise<PredictionInfo>;GetPredictionDatasets():Promise<PredictionDataset[]>;
 ConfigurePredictions(settings:PredictionSettings):Promise<PredictionInfo>;SavePredictionSelection(selection:PredictionSelection):Promise<PredictionSelection>;
 PreparePredictionModel():Promise<PredictionInfo>;StartPrediction(selection:PredictionSelection):Promise<PredictionInfo>;CancelPrediction():Promise<PredictionInfo>;
 GetPredictionForecast(id:string):Promise<PredictionForecast>;ChoosePredictionModelDirectory():Promise<string>;
}
export const initialPredictionSelection=():PredictionSelection=>({asset:'',period:'d1',contextLength:512,horizon:20,forecastId:''});
export const predictionModelKey=(settings:PredictionSettings)=>JSON.stringify([settings.model,settings.modelDirectory]);
export const predictionDate=(time:number)=>Number.isFinite(time)?new Date(time).toISOString().replace('T',' ').replace('.000Z',' UTC'):'—';
export function predictionBytes(bytes:number){if(!Number.isFinite(bytes)||bytes<0)return '0 B';const unit=Math.min(3,Math.floor(Math.log(Math.max(1,bytes))/Math.log(1024)));return `${(bytes/1024**unit).toLocaleString('en-US',{maximumFractionDigits:unit?1:0})} ${['B','KiB','MiB','GiB'][unit]}`;}
/** Ordered, coalesced durable writes; an unacknowledged selection stays retryable. */
export class PredictionSelectionWriter {
 private pending?:PredictionSelection;private flight?:Promise<void>;
 constructor(private persist:(selection:PredictionSelection)=>Promise<PredictionSelection>,private onError:(reason:unknown)=>void){}
 get busy(){return !!this.pending||!!this.flight;}
 edit(selection:PredictionSelection){this.pending=structuredClone(selection);void this.flush().catch(this.onError);}
 async flush():Promise<void>{
  if(this.flight){await this.flight;return this.flush();}
  const next=this.pending;if(!next)return;this.pending=undefined;
  // Invoke the bridge in a microtask so even a synchronous bridge exception
  // reaches the finalizer after this.flight has been assigned.
  this.flight=Promise.resolve().then(()=>this.persist(next)).then(()=>{}).catch(reason=>{if(!this.pending)this.pending=next;throw reason;}).finally(()=>{this.flight=undefined;});
  await this.flight;return this.flush();
 }
}
/** The chart's horizontal coordinate is an ordinal bar, never an invented date. */
export function predictionBarLabel(forecast:Pick<PredictionForecast,'history'>&Partial<Pick<PredictionForecast,'period'>>,index:number,full=false){
 const bar=Math.round(index)-1;if(bar<0)return '';
 if(bar>=forecast.history.length)return `+${bar-forecast.history.length+1}`;
 const utc=predictionDate(forecast.history[bar].time);return full?utc:utc.slice(0,forecast.period?.startsWith('s')?19:16);
}

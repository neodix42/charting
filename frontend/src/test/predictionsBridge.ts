import type {PredictionDataset,PredictionForecast,PredictionInfo,PredictionSelection,PredictionsBackend} from '../workspace/predictions';
import {initialPredictionSelection} from '../workspace/predictions';

/** Explicit, offline browser fixture. This module is never a production fallback. */
export function predictionsBridge():PredictionsBackend {
 const key='cryptodesk-explicit-test-predictions';
 const datasets:PredictionDataset[]=[{id:'bitcoin-daily',symbol:'btcusd',name:'Bitcoin vs US Dollar',period:'d1',filename:'btcusd-d1.csv'},{id:'bitcoin-hourly',symbol:'btcusd',name:'Bitcoin vs US Dollar',period:'h1',filename:'btcusd-h1.csv'},{id:'euro-daily',symbol:'eurusd',name:'Euro vs US Dollar',period:'d1',filename:'eurusd-d1.csv'}];
 const saved=JSON.parse(sessionStorage.getItem(key)||'null') as {info:PredictionInfo;forecasts:PredictionForecast[]}|null;
 let forecasts:PredictionForecast[]=saved?.forecasts??[],info:PredictionInfo=saved?.info??{settings:{model:'Datadog/Toto-2.0-313m',modelDirectory:'/explicit-test/models'},models:[{id:'Datadog/Toto-2.0-313m',name:'Toto 2.0 · 313M'}],defaultModelDirectory:'/explicit-test/models',forecastDirectory:'/explicit-test/predictions',selection:initialPredictionSelection(),runtime:{ready:false,runtimeReady:true,modelReady:false},active:false,event:{phase:'idle',progress:0,message:''},forecasts:[]};
 const calls={get:0,datasets:0,prepare:0,start:0,cancel:0,configure:0,save:0,forecast:0};
 const persist=()=>sessionStorage.setItem(key,JSON.stringify({info,forecasts}));
 const snapshot=()=>structuredClone(info);
 const validate=(value:PredictionSelection)=>{if(![512,1024].includes(value.contextLength)||!Number.isInteger(value.horizon)||value.horizon<1||value.horizon>256)throw Error('Select 512 or 1024 historical bars and 1–256 future bars');};
 const finish=()=>{
  const dataset=datasets.find(value=>value.symbol===info.selection.asset&&value.period===info.selection.period);if(!dataset)throw Error('No selected fixture dataset');
  const asOf=Date.UTC(2026,9,29),step=dataset.period==='h1'?3600000:86400000,createdAt=asOf+forecasts.length+1;
  const history=Array.from({length:info.selection.contextLength},(_,index)=>({time:asOf-(info.selection.contextLength-index)*step,close:100+index*.1+Math.sin(index/8)*2})),last=history.at(-1)!.close;
  const forecast:PredictionForecast={id:`${createdAt}-fixture-${forecasts.length}`,createdAt,asOf,model:info.settings.model,modelRevision:'explicit-fixture-revision',symbol:dataset.symbol,assetName:dataset.name,period:dataset.period,contextLength:info.selection.contextLength,horizon:info.selection.horizon,sourceFile:dataset.filename,sourceHash:'explicit-test-hash',history,points:Array.from({length:info.selection.horizon},(_,index)=>({step:index+1,median:last+index*.2,lower:last+index*.2-5-index*.2,upper:last+index*.2+5+index*.2}))};
  forecasts=[forecast,...forecasts];const {history:_history,points:_points,sourceFile:_source,sourceHash:_hash,modelRevision:_revision,...summary}=forecast;
  info={...info,active:false,event:{phase:'completed',progress:100,message:'Forecast complete'},forecasts:[summary,...info.forecasts]};persist();return structuredClone(forecast);
 };
 Object.assign(window,{__testPredictionInfo:snapshot,__testPredictionCalls:()=>({...calls}),__testSetPredictions:(patch:Partial<PredictionInfo>)=>{info={...info,...structuredClone(patch)};persist();},__testPredictionReady:()=>{info={...info,active:false,error:'',runtime:{ready:true,runtimeReady:true,modelReady:true},event:{phase:'ready',progress:100,message:'Model ready'}};persist();},__testFinishPrediction:finish});
 return {
  async GetPredictions(){calls.get++;return snapshot();},async GetPredictionDatasets(){calls.datasets++;return structuredClone(datasets);},
  async ConfigurePredictions(settings){calls.configure++;if(info.active)throw Error('Finish or cancel the active prediction before changing model settings');if(!info.models.some(model=>model.id===settings.model))throw Error('Unknown model');info={...info,settings:{...settings,modelDirectory:settings.modelDirectory||info.defaultModelDirectory},runtime:{ready:false,modelReady:false,runtimeReady:true},event:{phase:'idle',progress:0,message:''},error:''};persist();return snapshot();},
  async SavePredictionSelection(selection){calls.save++;validate(selection);info={...info,selection:structuredClone(selection)};persist();return structuredClone(info.selection);},
  async PreparePredictionModel(){calls.prepare++;if(info.active||info.runtime.ready)return snapshot();info={...info,active:true,error:'',event:{phase:'download',progress:25,message:'Downloading model',downloaded:104857600,total:419430400}};return snapshot();},
  async StartPrediction(selection){calls.start++;validate(selection);if(info.active)throw Error('A prediction is already active');if(!info.runtime.ready)throw Error('Prepare the model first');if(!datasets.some(dataset=>dataset.symbol===selection.asset&&dataset.period===selection.period))throw Error('Choose a downloaded asset and period');info={...info,selection:structuredClone(selection),active:true,error:'',event:{phase:'inference',progress:5,message:'Forecasting closing prices'}};persist();return snapshot();},
  async CancelPrediction(){calls.cancel++;info={...info,active:false,event:{phase:'cancelled',progress:0,message:'Cancelled'}};persist();return snapshot();},
  async GetPredictionForecast(id){calls.forecast++;const forecast=forecasts.find(value=>value.id===id);if(!forecast)throw Error('Stored forecast not found');return structuredClone(forecast);},
  async ChoosePredictionModelDirectory(){return '/explicit-test/chosen-models';},
 };
}

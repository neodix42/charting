import {useEffect,useRef,useState} from 'react';
import type {ScreenerInfo} from '../types';
import {errorText} from '../types';
import type {PredictionStatisticsBackend,PredictionStatisticsRequest} from '../workspace/predictionStatistics';

type Phase='starting'|'running'|'ready'|'finishing'|'completed'|'failed'|'cancelled'|'status-error';
export interface StatisticsDownload {operation:number;request:PredictionStatisticsRequest;ids:string[];phase:Phase;info?:ScreenerInfo;error:string;notice:string}
const pending=(phase:Phase)=>['starting','running','ready','finishing','status-error'].includes(phase);
const cancellable=(phase:Phase)=>phase==='running'||phase==='status-error';
const sameIDs=(expected:string[],actual:string[])=>expected.length===actual.length&&new Set(actual).size===actual.length&&actual.every(id=>expected.includes(id));

/** Own one accepted batch independently from the currently selected forecast. */
export function usePredictionStatisticsDownload({api,visible,onComplete}:{api:PredictionStatisticsBackend;visible:boolean;onComplete(request:PredictionStatisticsRequest):Promise<'done'|'deferred'|'recalculated'>}) {
 const [download,setDownload]=useState<StatisticsDownload|null>(null),[actionBusy,setActionBusy]=useState(false),[cancelPending,setCancelPending]=useState(false);
 const current=useRef<StatisticsDownload|null>(null),alive=useRef(false),foreground=useRef(visible),serial=useRef(0),inFlight=useRef(false),queuedCancel=useRef(false),complete=useRef(onComplete);
 foreground.current=visible;complete.current=onComplete;
 useEffect(()=>{alive.current=true;return()=>{alive.current=false;serial.current++;};},[]);
 const replace=(value:StatisticsDownload)=>{current.current=value;if(alive.current)setDownload(value);};
 const statusError=(record:StatisticsDownload,reason:unknown)=>replace({...record,phase:'status-error',error:`Could not read download status: ${errorText(reason)}`,notice:''});
 const accept=(record:StatisticsDownload,info:ScreenerInfo)=>{
  const actual=info.jobs.map(job=>job.id);
  if(!sameIDs(record.ids,actual)){statusError(record,'The owned download jobs are missing or were replaced. No scenarios were recalculated.');return;}
  const failed=info.jobs.filter(job=>job.status==='failed'),cancelled=info.jobs.some(job=>job.status==='cancelled'),working=info.jobs.some(job=>job.status==='queued'||job.status==='running');
  const error=failed.length?failed.map(job=>`${job.timeframe.toUpperCase()}: ${job.message||'Download failed'}`).join(' · '):'';
  const phase:Phase=working?'running':failed.length?'failed':cancelled?'cancelled':info.jobs.every(job=>job.status==='completed')?'ready':'status-error';
  replace({...record,info,phase,error:error||(phase==='status-error'?'The download returned an unknown job state.':''),notice:phase==='cancelled'?'Data download cancelled. Downloaded candles were kept.':phase==='ready'?'Download completed. Preparing scenarios…':''});
 };
 const performCancel=async()=>{
  const record=current.current;
  if(!record||!cancellable(record.phase)||!record.ids.length){queuedCancel.current=false;if(alive.current)setCancelPending(false);return;}
  if(inFlight.current){queuedCancel.current=true;if(alive.current)setCancelPending(true);return;}
  queuedCancel.current=false;inFlight.current=true;setActionBusy(true);setCancelPending(true);
  try{const info=await api.CancelPredictionStatisticsDownload(record.ids);if(alive.current&&current.current?.operation===record.operation)accept(current.current,info);}
  catch(reason){if(alive.current&&current.current?.operation===record.operation)statusError(current.current,`Cancellation was not confirmed: ${errorText(reason)}`);}
  finally{inFlight.current=false;if(alive.current){setActionBusy(false);setCancelPending(false);}}
 };
 const cancelRef=useRef(performCancel);cancelRef.current=performCancel;
 useEffect(()=>{
  if(!visible||!download||!['running','ready'].includes(download.phase))return;
  let active=true,timer:ReturnType<typeof setTimeout>|undefined;
  const schedule=()=>{if(active)timer=setTimeout(()=>void read(),750);};
  const read=async()=>{
   const record=current.current;
   if(!active||!record||record.operation!==download.operation||!foreground.current)return;
   if(inFlight.current){schedule();return;}
   inFlight.current=true;
   try{
    if(record.phase==='ready'){
     replace({...record,phase:'finishing'});
     const result=await complete.current(record.request);
     if(alive.current&&current.current?.operation===record.operation)replace({...current.current,phase:result==='deferred'?'ready':'completed',notice:result==='deferred'?'Download completed. Scenarios will refresh when Statistics is visible.':result==='recalculated'?'Download completed; scenarios recalculated.':'Data download completed.',error:''});
    }else if(record.phase==='running'){
     const info=await api.GetPredictionStatisticsDownload(record.ids);
     if(active&&alive.current&&current.current?.operation===record.operation)accept(current.current,info);
    }
   }catch(reason){if(alive.current&&current.current?.operation===record.operation){if(record.phase==='ready')replace({...current.current,phase:'completed',error:`Data download completed, but scenarios could not be refreshed: ${errorText(reason)}`,notice:''});else if(active)statusError(current.current,reason);}}
   finally{
    inFlight.current=false;
    if(queuedCancel.current)void cancelRef.current();
    if(active&&current.current?.operation===record.operation&&['running','ready'].includes(current.current.phase))schedule();
   }
  };
  void read();return()=>{active=false;if(timer)clearTimeout(timer);};
 },[api,visible,download?.operation,download?.phase]);
 const start=async(request:PredictionStatisticsRequest)=>{
  if(inFlight.current||current.current&&pending(current.current.phase)||!foreground.current)return;
  const record:StatisticsDownload={operation:++serial.current,request:structuredClone(request),ids:[],phase:'starting',error:'',notice:''};
  replace(record);inFlight.current=true;setActionBusy(true);
  try{
   const info=await api.DownloadPredictionStatisticsData(record.request);
   if(!alive.current||current.current?.operation!==record.operation)return;
   const ids=info.jobs.map(job=>job.id);
   if(!ids.length||ids.some(id=>!id)||new Set(ids).size!==ids.length){replace({...record,phase:'failed',error:'The downloader returned no identifiable jobs. No scenarios were recalculated.'});return;}
   const wrongJob=info.jobs.some(job=>(job.dataSource??'dukascopy')!==record.request.dataSource||job.symbol!==record.request.asset||![record.request.period,...(record.request.period==='d1'?[]:['d1'])].includes(job.timeframe));
   if(wrongJob){replace({...record,phase:'failed',error:'The downloader returned jobs for another selection. No scenarios were recalculated.'});return;}
   accept({...record,ids},info);
  }catch(reason){if(alive.current&&current.current?.operation===record.operation)replace({...record,phase:'failed',error:errorText(reason)});}
  finally{inFlight.current=false;if(alive.current)setActionBusy(false);}
 };
 const retryStatus=()=>{const record=current.current;if(record?.phase==='status-error'&&!inFlight.current)replace({...record,phase:'running',error:'',notice:''});};
 const stopTracking=()=>{const record=current.current;if(record?.phase!=='status-error'||inFlight.current)return;queuedCancel.current=false;setCancelPending(false);replace({...record,operation:++serial.current,ids:[],phase:'failed',notice:'Stopped tracking this download. Any running downloads continue in Screener.'});};
 return {download,locked:!!download&&pending(download.phase),isLocked:()=>!!current.current&&pending(current.current.phase),actionBusy,cancelPending,start,cancel:performCancel,retryStatus,stopTracking};
}

import catalog from '../../../internal/screener/indicators.json' with {type:'json'};
import type {Backend,ScreenerDataFile,ScreenerEnrichmentInfo,ScreenerEnrichmentRequest,ScreenerIndicators,ScreenerRefreshInfo} from '../types';

/** Deterministic browser fixtures, installed only by the explicit test bridge. */
export function screenerEnrichmentBridge(directory:()=>string){
 const fileKey='cryptodesk-explicit-test-enrichment-files';
 let files:ScreenerDataFile[]=JSON.parse(sessionStorage.getItem(fileKey)||'[]');
 let request:ScreenerEnrichmentRequest|null=null,reads=0,fileError='',startError='';
 let info:ScreenerEnrichmentInfo={directory:directory(),active:false,runtimeAvailable:true,indicator:'',total:0,completed:0,failed:0,cancelled:0,currentFile:'',currentProgress:0,jobs:[]};
 let refresh:ScreenerRefreshInfo={directory:directory(),active:false,runtimeAvailable:true,total:0,completed:0,failed:0,cancelled:0,currentFile:'',currentProgress:0,jobs:[]},refreshStarts=0,refreshStartError='';
 const saveFiles=()=>sessionStorage.setItem(fileKey,JSON.stringify(files));
 const api:Pick<Backend,'GetScreenerIndicators'|'GetScreenerFiles'|'GetScreenerEnrichment'|'StartScreenerEnrichment'|'CancelScreenerEnrichment'|'GetScreenerRefresh'|'StartScreenerRefresh'|'CancelScreenerRefresh'>={
  async GetScreenerIndicators(){return structuredClone(catalog as ScreenerIndicators);},
  async GetScreenerFiles(offset,limit){reads++;if(fileError)throw Error(fileError);return{directory:directory(),total:files.length,offset,limit,files:structuredClone(files.slice(offset,offset+limit))};},
  async GetScreenerEnrichment(ids){return structuredClone({...info,directory:directory(),jobs:info.jobs.filter(job=>ids.includes(job.fileId))});},
  async StartScreenerEnrichment(value){
   if(startError)throw Error(startError);
   request=structuredClone(value);
   info={...info,active:true,indicator:value.indicator,total:files.length,completed:0,failed:0,cancelled:0,jobs:files.map(file=>({id:crypto.randomUUID(),fileId:file.id,name:file.name,status:'queued',progress:0,rows:0,message:'Queued in explicit test mode',columns:[]}))};
   return structuredClone({...info,jobs:[]});
  },
  async CancelScreenerEnrichment(){
   info={...info,active:false,currentFile:'',currentProgress:0,jobs:info.jobs.map(job=>job.status==='queued'||job.status==='running'?{...job,status:'cancelled',message:'Enrichment cancelled; original file kept'}:job)};
   info.cancelled=info.jobs.filter(job=>job.status==='cancelled').length;
   return structuredClone({...info,jobs:[]});
  },
  async GetScreenerRefresh(ids){return structuredClone({...refresh,directory:directory(),jobs:refresh.jobs.filter(job=>ids.includes(job.fileId))});},
  async StartScreenerRefresh(){
   if(refreshStartError)throw Error(refreshStartError);
   refreshStarts++;
   refresh={...refresh,active:true,asOf:Date.now(),total:files.length,completed:0,failed:0,cancelled:0,jobs:files.map(file=>({id:crypto.randomUUID(),fileId:file.id,name:file.name,status:'queued',progress:0,rows:0,addedRows:0,updatedRows:0,message:'Refreshing in explicit test mode',columns:file.fields.filter(field=>!['timestamp','open','high','low','close','volume'].includes(field))}))};
   return structuredClone({...refresh,jobs:[]});
  },
  async CancelScreenerRefresh(){
   refresh={...refresh,active:false,currentFile:'',currentProgress:0,jobs:refresh.jobs.map(job=>job.status==='queued'||job.status==='running'?{...job,status:'cancelled',message:'Refresh cancelled; original file kept'}:job)};
   refresh.cancelled=refresh.jobs.filter(job=>job.status==='cancelled').length;
   return structuredClone({...refresh,jobs:[]});
  },
 };
 Object.assign(window,{
  __testSetEnrichmentFiles:(value:ScreenerDataFile[])=>{files=structuredClone(value);saveFiles();},
  __testEnrichmentFiles:()=>structuredClone(files),__testEnrichmentRequest:()=>structuredClone(request),__testEnrichmentReads:()=>reads,
  __testEnrichment:()=>structuredClone(info),
  __testSetEnrichment:(value:Partial<ScreenerEnrichmentInfo>)=>{info={...info,...structuredClone(value)};},
  __testEnrichmentErrors:(file:string,start:string)=>{fileError=file;startError=start;},
  __testRefresh:()=>structuredClone(refresh),__testRefreshStarts:()=>refreshStarts,
  __testSetRefresh:(value:Partial<ScreenerRefreshInfo>)=>{refresh={...refresh,...structuredClone(value)};},
  __testRefreshError:(value:string)=>{refreshStartError=value;},
 });
 return api;
}

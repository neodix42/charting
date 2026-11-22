import type {PredictionDataset} from '../workspace/predictions';
import type {ScreenerInfo,ScreenerJob} from '../types';
import type {PredictionStatisticsBackend,PredictionStatisticsForecast,PredictionStatisticsRequest} from '../workspace/predictionStatistics';

/** Explicit offline fixtures; no estimator or generated result is used in production. */
export function predictionStatisticsBridge():PredictionStatisticsBackend {
 const datasets:PredictionDataset[]=[
  {id:'statistics-dukas-eur',dataSource:'dukascopy',market:'forex',symbol:'eurusd',name:'Euro vs US Dollar',period:'d1',filename:'eurusd-d1.csv'},
  {id:'statistics-dukas-eur-h1',dataSource:'dukascopy',market:'forex',symbol:'eurusd',name:'Euro vs US Dollar',period:'h1',filename:'eurusd-h1.csv'},
  {id:'statistics-dukas-eur-w1',dataSource:'dukascopy',market:'forex',symbol:'eurusd',name:'Euro vs US Dollar',period:'w1',filename:'eurusd-w1.csv'},
  {id:'statistics-dukas-bitcoin',dataSource:'dukascopy',market:'cfd',symbol:'btcusd',name:'Bitcoin vs US Dollar',period:'d1',filename:'btcusd-d1.csv'},
  {id:'statistics-hyper-bitcoin',dataSource:'hyperliquid',market:'perpetual',symbol:'perp-BTC',name:'Bitcoin perpetual',period:'d1',filename:'hyperliquid-perp-BTC-d1.csv'},
  {id:'statistics-hyper-bitcoin-h1',dataSource:'hyperliquid',market:'perpetual',symbol:'perp-BTC',name:'Bitcoin perpetual',period:'h1',filename:'hyperliquid-perp-BTC-h1.csv'},
  {id:'statistics-hyper-bitcoin-w1',dataSource:'hyperliquid',market:'perpetual',symbol:'perp-BTC',name:'Bitcoin perpetual',period:'w1',filename:'hyperliquid-perp-BTC-w1.csv'},
  {id:'statistics-hyper-ether',dataSource:'hyperliquid',market:'perpetual',symbol:'perp-ETH',name:'Ether perpetual',period:'d1',filename:'hyperliquid-perp-ETH-d1.csv'},
  {id:'statistics-hyper-spot-bitcoin',dataSource:'hyperliquid',market:'spot',symbol:'spot-UBTC-USDC',name:'UBTC / USDC',period:'d1',filename:'hyperliquid-spot-UBTC-USDC-d1.csv'},
 ];
 const day=86400000,weekStart=Date.UTC(2026,10,16),anchor={time:weekStart,open:99,high:101,low:98,close:100};
 const dates:number[]=[];
 for(let time=weekStart;dates.length<160;time-=day)if(![0,6].includes(new Date(time).getUTCDay()))dates.unshift(time);
 const history=dates.map((time,index)=>({time,open:90+index/16,high:91+index/16,low:89+index/16,close:90.5+index/16}));
 history[history.length-1]=anchor;
 const template:PredictionStatisticsForecast={
  dataSource:'dukascopy',symbol:'eurusd',assetName:'Euro vs US Dollar',period:'d1',lookbackWeeks:0,
  asOf:weekStart+day,firstClosedBar:Date.UTC(2018,0,1),weekStart,sourceFile:'eurusd-d1.csv',sourceHash:'e'.repeat(64),history,anchor,
  prefix:[{time:weekStart,day:'Mon',direction:'up'}],
  scenarios:[
   {id:'UDDD',label:'Tue ↑ · Wed ↓ · Thu ↓ · Fri ↓',probability:.75,count:3,points:[
    {time:weekStart+day,close:102,direction:'up'},{time:weekStart+2*day,close:101,direction:'down'},
    {time:weekStart+3*day,close:99,direction:'down'},{time:weekStart+4*day,close:98,direction:'down'},
   ]},
   {id:'DUDU',label:'Tue ↓ · Wed ↑ · Thu ↓ · Fri ↑',probability:.25,count:1,points:[
    {time:weekStart+day,close:98,direction:'down'},{time:weekStart+2*day,close:101,direction:'up'},
    {time:weekStart+3*day,close:99,direction:'down'},{time:weekStart+4*day,close:102,direction:'up'},
   ]},
  ],
  matchedWeeks:4,eligibleWeeks:12,excludedIncompleteWeeks:2,excludedDojiWeeks:1,excludedInvalidWeeks:0,theoreticalScenarios:16,
  warnings:['Only 4 matching weeks. These historical frequencies have a small sample size.'],
 };
 let override:PredictionStatisticsForecast|{error:string}|null=null;
 let datasetReads=0;
 const requests:PredictionStatisticsRequest[]=[];
 const downloadRequests:PredictionStatisticsRequest[]=[],statusRequests:string[][]=[],cancelRequests:string[][]=[];
 let batch=0,latestJobIDs:string[]=[];
 let downloadInfo:ScreenerInfo={directory:'/explicit-test/screener',runtimeAvailable:true,concurrency:2,maxConcurrency:20,active:false,jobs:[]};
 const ownership=new Map<string,string>();
 const identity=(job:ScreenerJob)=>JSON.stringify([job.dataSource,job.symbol,job.timeframe]);
 const ownedDownload=(jobIDs:string[]):ScreenerInfo=>{
  if(!jobIDs.length||new Set(jobIDs).size!==jobIDs.length)throw Error('Statistics download jobs are missing or were replaced');
  const jobs=jobIDs.map(id=>{
   const job=downloadInfo.jobs.find(job=>job.id===id);
   if(!job||!ownership.has(id)||ownership.get(id)!==identity(job))throw Error('Statistics download jobs are missing or were replaced');
   return job;
  });
  return structuredClone({...downloadInfo,active:jobs.some(job=>job.status==='queued'||job.status==='running'),jobs});
 };
 Object.assign(window,{
  __testStatisticsCalls:()=>({datasets:datasetReads,calculate:requests.length,requests:structuredClone(requests)}),
  __testSetStatisticsForecast:(value:PredictionStatisticsForecast|{error:string}|null)=>{override=structuredClone(value);},
  __testStatisticsDownloadCalls:()=>structuredClone({start:downloadRequests.length,status:statusRequests.length,cancel:cancelRequests.length,requests:downloadRequests,statusRequests,cancelRequests,jobIDs:latestJobIDs}),
  __testStatisticsDownloadInfo:()=>structuredClone(downloadInfo),
  __testSetStatisticsDownloadInfo:(value:Partial<ScreenerInfo>)=>{
   downloadInfo={...downloadInfo,...structuredClone(value)};
   if(value.active===undefined)downloadInfo.active=downloadInfo.jobs.some(job=>job.status==='queued'||job.status==='running');
  },
  __testFinishStatisticsDownload:(value?:PredictionStatisticsForecast|null)=>{
   const ids=new Set(latestJobIDs);
   downloadInfo={...downloadInfo,jobs:downloadInfo.jobs.map(job=>ids.has(job.id)?{...job,status:'completed',progress:100,message:'Downloaded explicit fixture candles'}:job)};
   downloadInfo.active=downloadInfo.jobs.some(job=>job.status==='queued'||job.status==='running');
   if(value!==undefined)override=structuredClone(value);
   return ownedDownload(latestJobIDs);
  },
 });
 return {
  async DownloadPredictionStatisticsData(request){
   if(downloadInfo.jobs.some(job=>ownership.has(job.id)&&(job.status==='queued'||job.status==='running')))throw Error('A Statistics data download is already running');
   downloadRequests.push(structuredClone(request));batch++;
   const periods=request.patternMode==='current'||request.pattern?.length?[request.period]:request.period==='d1'?['d1']:[request.period,'d1'];
   const jobs:ScreenerJob[]=periods.map(timeframe=>({id:`explicit-statistics-download-${batch}-${timeframe}`,dataSource:request.dataSource,symbol:request.asset,timeframe,from:'2018-01-01',to:'2026-11-20',status:'queued',progress:0,message:'Queued explicit fixture download'}));
   latestJobIDs=jobs.map(job=>job.id);
   for(const job of jobs)ownership.set(job.id,identity(job));
   downloadInfo={...downloadInfo,error:undefined,downloadsPaused:false,active:true,jobs:[...downloadInfo.jobs.filter(job=>!ownership.has(job.id)),...jobs]};
   return ownedDownload(latestJobIDs);
  },
  async GetPredictionStatisticsDownload(jobIDs){statusRequests.push([...jobIDs]);return ownedDownload(jobIDs);},
  async CancelPredictionStatisticsDownload(jobIDs){
   cancelRequests.push([...jobIDs]);ownedDownload(jobIDs);
   const ids=new Set(jobIDs);
   downloadInfo={...downloadInfo,jobs:downloadInfo.jobs.map(job=>ids.has(job.id)&&(job.status==='queued'||job.status==='running')?{...job,status:'cancelled',message:'Cancelled explicit fixture download'}:job)};
   downloadInfo.active=downloadInfo.jobs.some(job=>job.status==='queued'||job.status==='running');
   return ownedDownload(jobIDs);
  },
  async GetPredictionStatisticsDatasets(){datasetReads++;return structuredClone(datasets);},
  async GetPredictionStatistics(request){
   requests.push(structuredClone(request));
   if(override&&'error' in override)throw Error(override.error);
   if(override)return structuredClone(override);
   const dataset=datasets.find(item=>item.dataSource===request.dataSource&&item.symbol===request.asset&&item.period===request.period);
   if(!dataset)throw Error('Choose a downloaded asset and time period from the selected data source');
   const result:PredictionStatisticsForecast={...template,dataSource:request.dataSource,symbol:dataset.symbol,assetName:dataset.name,period:request.period,lookbackWeeks:request.lookbackWeeks,sourceFile:dataset.filename};
   if(request.patternMode==='current'||request.pattern?.length){
    // Fixed synthetic outcomes exercise presentation/lifecycle, not the estimator.
    const step=request.period==='h1'?3600000:request.period==='w1'?7*day:day;
    const time=request.period==='h1'?weekStart+day+11*3600000:request.period==='w1'?weekStart-7*day:weekStart;
    result.firstClosedBar=request.period==='h1'?Date.UTC(2019,0,1):request.period==='w1'?Date.UTC(2017,0,2):template.firstClosedBar;
    result.anchor={time,open:99,high:101,low:98,close:100};result.asOf=time+step;
    result.history=Array.from({length:160},(_,index)=>({...result.anchor,time:time-(159-index)*step}));
    const resolved=request.patternMode==='current'?Array.from({length:request.patternLength!},(_,index)=>index%2===0?'down' as const:'up' as const):[...request.pattern!];
    result.patternMode=request.patternMode==='current'?'current':'custom';result.patternLength=resolved.length;
    result.pattern=resolved;result.prefix=resolved.map((direction,index)=>({time:time-(resolved.length-index-1)*step,day:'',direction}));
    result.matchedPatterns=4;result.eligiblePatterns=12;result.excludedDojiPatterns=1;result.excludedInvalidPatterns=0;result.nextBarDojiCount=0;result.nextBarTime=time+step;
    result.scenarios=[{id:'U',label:'Next bar bullish',probability:.75,count:3,points:[{time:time+step,close:102,direction:'up'}]},{id:'D',label:'Next bar bearish',probability:.25,count:1,points:[{time:time+step,close:98,direction:'down'}]}];
    result.warnings=['Only 4 matching patterns. These historical frequencies have a small sample size.'];
    return structuredClone(result);
   }
   if(request.period==='h1'){
    result.firstClosedBar=Date.UTC(2019,0,1);
    const hour=3600000,time=weekStart+day+11*hour;
    result.anchor={time,open:99,high:101,low:98,close:100};
    result.asOf=time+hour;
    result.history=Array.from({length:160},(_,index)=>({...result.anchor,time:time-(159-index)*hour}));
    result.scenarios=template.scenarios.map(scenario=>({...scenario,points:scenario.points.map(point=>({...point,time:point.time+23*hour}))}));
    result.conditioning='Daily directions plus the closed Tuesday 11:00 UTC H1 candle direction';
    result.companionSourceFile=dataset.filename.replace(/-h1\.csv$/,'-d1.csv');
    result.companionSourceHash='d'.repeat(64);
    result.excludedCheckpointWeeks=0;
   }else if(request.period==='w1'){
    result.firstClosedBar=Date.UTC(2017,0,2);
    const time=weekStart-7*day;
    result.anchor={time,open:89,high:91,low:88,close:90};
    result.history=Array.from({length:160},(_,index)=>({...result.anchor,time:time-(159-index)*7*day}));
    result.scenarios=template.scenarios.map(scenario=>({...scenario,points:[{time:weekStart,close:100,direction:'up',observed:true},...scenario.points]}));
    result.conditioning='Previous closed W1 candle direction plus the current closed daily directions';
    result.companionSourceFile=dataset.filename.replace(/-w1\.csv$/,'-d1.csv');
    result.companionSourceHash='d'.repeat(64);
    result.excludedCheckpointWeeks=0;
   }
   return structuredClone(result);
  },
 };
}

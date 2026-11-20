import type {PredictionDataset} from '../workspace/predictions';
import type {PredictionStatisticsBackend,PredictionStatisticsForecast,PredictionStatisticsRequest} from '../workspace/predictionStatistics';

/** Explicit offline fixtures; no estimator or generated result is used in production. */
export function predictionStatisticsBridge():PredictionStatisticsBackend {
 const datasets:PredictionDataset[]=[
  {id:'statistics-dukas-eur',dataSource:'dukascopy',symbol:'eurusd',name:'Euro vs US Dollar',period:'d1',filename:'eurusd-d1.csv'},
  {id:'statistics-dukas-eur-h1',dataSource:'dukascopy',symbol:'eurusd',name:'Euro vs US Dollar',period:'h1',filename:'eurusd-h1.csv'},
  {id:'statistics-dukas-eur-w1',dataSource:'dukascopy',symbol:'eurusd',name:'Euro vs US Dollar',period:'w1',filename:'eurusd-w1.csv'},
  {id:'statistics-dukas-bitcoin',dataSource:'dukascopy',symbol:'btcusd',name:'Bitcoin vs US Dollar',period:'d1',filename:'btcusd-d1.csv'},
  {id:'statistics-hyper-bitcoin',dataSource:'hyperliquid',symbol:'perp-BTC',name:'Bitcoin perpetual',period:'d1',filename:'hyperliquid-perp-BTC-d1.csv'},
  {id:'statistics-hyper-bitcoin-h1',dataSource:'hyperliquid',symbol:'perp-BTC',name:'Bitcoin perpetual',period:'h1',filename:'hyperliquid-perp-BTC-h1.csv'},
  {id:'statistics-hyper-bitcoin-w1',dataSource:'hyperliquid',symbol:'perp-BTC',name:'Bitcoin perpetual',period:'w1',filename:'hyperliquid-perp-BTC-w1.csv'},
  {id:'statistics-hyper-ether',dataSource:'hyperliquid',symbol:'perp-ETH',name:'Ether perpetual',period:'d1',filename:'hyperliquid-perp-ETH-d1.csv'},
 ];
 const day=86400000,weekStart=Date.UTC(2026,10,16),anchor={time:weekStart,open:99,high:101,low:98,close:100};
 const dates:number[]=[];
 for(let time=weekStart;dates.length<160;time-=day)if(![0,6].includes(new Date(time).getUTCDay()))dates.unshift(time);
 const history=dates.map((time,index)=>({time,open:90+index/16,high:91+index/16,low:89+index/16,close:90.5+index/16}));
 history[history.length-1]=anchor;
 const template:PredictionStatisticsForecast={
  dataSource:'dukascopy',symbol:'eurusd',assetName:'Euro vs US Dollar',period:'d1',lookbackWeeks:0,
  asOf:weekStart+day,weekStart,sourceFile:'eurusd-d1.csv',sourceHash:'e'.repeat(64),history,anchor,
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
 Object.assign(window,{
  __testStatisticsCalls:()=>({datasets:datasetReads,calculate:requests.length,requests:structuredClone(requests)}),
  __testSetStatisticsForecast:(value:PredictionStatisticsForecast|{error:string}|null)=>{override=structuredClone(value);},
 });
 return {
  async GetPredictionStatisticsDatasets(){datasetReads++;return structuredClone(datasets);},
  async GetPredictionStatistics(request){
   requests.push(structuredClone(request));
   if(override&&'error' in override)throw Error(override.error);
   if(override)return structuredClone(override);
   const dataset=datasets.find(item=>item.dataSource===request.dataSource&&item.symbol===request.asset&&item.period===request.period);
   if(!dataset)throw Error('Choose a downloaded asset and time period from the selected data source');
   const result:PredictionStatisticsForecast={...template,dataSource:request.dataSource,symbol:dataset.symbol,assetName:dataset.name,period:request.period,lookbackWeeks:request.lookbackWeeks,sourceFile:dataset.filename};
   if(request.period==='h1'){
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

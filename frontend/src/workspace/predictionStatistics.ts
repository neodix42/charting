import type {ScreenerDataSource,ScreenerInfo} from '../types';
import type {PredictionDataset} from './predictions';

export type PredictionStatisticsPeriod='h1'|'d1'|'w1';
export interface PredictionStatisticsRequest {dataSource:ScreenerDataSource;asset:string;period:PredictionStatisticsPeriod;lookbackWeeks:number}
export interface PredictionStatisticsCandle {time:number;open:number;high:number;low:number;close:number}
export interface PredictionStatisticsPrefix {time:number;day:string;direction:'up'|'down'}
export interface PredictionStatisticsPoint {time:number;close:number;direction:'up'|'down';observed?:boolean}
export interface PredictionStatisticsScenario {id:string;label:string;probability:number;count:number;points:PredictionStatisticsPoint[]}
export interface PredictionStatisticsForecast {
 dataSource:ScreenerDataSource;symbol:string;assetName:string;period:PredictionStatisticsPeriod;lookbackWeeks:number;asOf:number;firstClosedBar:number;weekStart:number;sourceFile:string;sourceHash:string;
 history:PredictionStatisticsCandle[];anchor:PredictionStatisticsCandle;prefix:PredictionStatisticsPrefix[];scenarios:PredictionStatisticsScenario[];
 matchedWeeks:number;eligibleWeeks:number;excludedIncompleteWeeks:number;excludedDojiWeeks:number;excludedInvalidWeeks:number;theoreticalScenarios:number;warnings:string[];
 conditioning?:string;companionSourceFile?:string;companionSourceHash?:string;excludedCheckpointWeeks?:number;
}
export interface PredictionStatisticsBackend {
 GetPredictionStatisticsDatasets():Promise<PredictionDataset[]>;
 GetPredictionStatistics(request:PredictionStatisticsRequest):Promise<PredictionStatisticsForecast>;
 DownloadPredictionStatisticsData(request:PredictionStatisticsRequest):Promise<ScreenerInfo>;
 GetPredictionStatisticsDownload(jobIDs:string[]):Promise<ScreenerInfo>;
 CancelPredictionStatisticsDownload(jobIDs:string[]):Promise<ScreenerInfo>;
}
export const initialStatisticsRequest=():PredictionStatisticsRequest=>({dataSource:'dukascopy',asset:'',period:'d1',lookbackWeeks:260});
export const statisticsProbability=(value:number)=>`${(value*100).toLocaleString('en-US',{minimumFractionDigits:1,maximumFractionDigits:1})}%`;
export const statisticsPrice=(value:number)=>value.toLocaleString('en-US',{maximumSignificantDigits:8});
export const statisticsKey=(request:PredictionStatisticsRequest)=>JSON.stringify([request.dataSource,request.asset,request.period,request.lookbackWeeks]);
export const statisticsResultKey=(forecast:PredictionStatisticsForecast)=>statisticsKey({dataSource:forecast.dataSource,asset:forecast.symbol,period:forecast.period,lookbackWeeks:forecast.lookbackWeeks});

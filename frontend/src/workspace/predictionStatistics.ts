import type {ScreenerDataSource,ScreenerInfo} from '../types';
import type {PredictionDataset} from './predictions';

export type PredictionStatisticsPeriod='h1'|'d1'|'w1';
export type PredictionStatisticsDirection='up'|'down';
export type PredictionStatisticsPatternMode='custom'|'current';
export interface PredictionStatisticsRequest {dataSource:ScreenerDataSource;asset:string;period:PredictionStatisticsPeriod;lookbackWeeks:number;pattern?:PredictionStatisticsDirection[];patternMode?:PredictionStatisticsPatternMode;patternLength?:number}
export interface PredictionStatisticsCandle {time:number;open:number;high:number;low:number;close:number}
export interface PredictionStatisticsPrefix {time:number;day:string;direction:'up'|'down'}
export interface PredictionStatisticsPoint {time:number;close:number;direction:'up'|'down'|'doji';observed?:boolean}
export interface PredictionStatisticsScenario {id:string;label:string;probability:number;count:number;points:PredictionStatisticsPoint[]}
export interface PredictionStatisticsForecast {
 dataSource:ScreenerDataSource;symbol:string;assetName:string;period:PredictionStatisticsPeriod;lookbackWeeks:number;asOf:number;firstClosedBar:number;weekStart:number;sourceFile:string;sourceHash:string;
 history:PredictionStatisticsCandle[];anchor:PredictionStatisticsCandle;prefix:PredictionStatisticsPrefix[];scenarios:PredictionStatisticsScenario[];
 matchedWeeks:number;eligibleWeeks:number;excludedIncompleteWeeks:number;excludedDojiWeeks:number;excludedInvalidWeeks:number;theoreticalScenarios:number;warnings:string[];
 pattern?:PredictionStatisticsDirection[];matchedPatterns?:number;eligiblePatterns?:number;excludedDojiPatterns?:number;excludedInvalidPatterns?:number;nextBarTime?:number;nextBarDojiCount?:number;
 patternMode?:PredictionStatisticsPatternMode;patternLength?:number;
 conditioning?:string;companionSourceFile?:string;companionSourceHash?:string;excludedCheckpointWeeks?:number;
}
export interface PredictionStatisticsBackend {
 GetPredictionStatisticsDatasets():Promise<PredictionDataset[]>;
 GetPredictionStatistics(request:PredictionStatisticsRequest):Promise<PredictionStatisticsForecast>;
 DownloadPredictionStatisticsData(request:PredictionStatisticsRequest):Promise<ScreenerInfo>;
 GetPredictionStatisticsDownload(jobIDs:string[]):Promise<ScreenerInfo>;
 CancelPredictionStatisticsDownload(jobIDs:string[]):Promise<ScreenerInfo>;
}
export const initialStatisticsRequest=():PredictionStatisticsRequest=>({dataSource:'dukascopy',asset:'',period:'d1',lookbackWeeks:260,pattern:['up','up','up','up','up']});
export const statisticsProbability=(value:number)=>`${(value*100).toLocaleString('en-US',{minimumFractionDigits:1,maximumFractionDigits:1})}%`;
export const statisticsPrice=(value:number)=>value.toLocaleString('en-US',{maximumSignificantDigits:8});
export const statisticsPatternMode=(request:{patternMode?:PredictionStatisticsPatternMode})=>request.patternMode??'custom';
export const statisticsBarCount=(request:PredictionStatisticsRequest)=>statisticsPatternMode(request)==='current'?request.patternLength??5:request.pattern?.length??5;
export const statisticsKey=(request:PredictionStatisticsRequest)=>JSON.stringify([request.dataSource,request.asset,request.period,request.lookbackWeeks,statisticsPatternMode(request),statisticsPatternMode(request)==='current'?request.patternLength:request.pattern??[]]);
export const statisticsResultKey=(forecast:PredictionStatisticsForecast)=>statisticsKey({dataSource:forecast.dataSource,asset:forecast.symbol,period:forecast.period,lookbackWeeks:forecast.lookbackWeeks,pattern:forecast.pattern,patternMode:forecast.patternMode,patternLength:forecast.patternLength});

export type Exchange = 'binance'|'bybit'|'coinbase';
export type Market = 'spot'|'perpetual';
export type Interval = '1m'|'5m'|'15m'|'1h'|'4h'|'1d'|'1w';
export const intervals: Interval[] = ['1m','5m','15m','1h','4h','1d','1w'];
export const intervalSeconds: Record<Interval,number> = {'1m':60,'5m':300,'15m':900,'1h':3600,'4h':14400,'1d':86400,'1w':604800};
export type IndicatorType = 'sma'|'ema'|'ichimoku'|'macd';
export interface IndicatorParameters {period?:number;conversion?:number;base?:number;spanB?:number;displacement?:number;fast?:number;slow?:number;signal?:number}
export type IndicatorLineWidth = 1|2|3|4;
export interface IndicatorConfig {id:string;type:IndicatorType;parameters:IndicatorParameters;color?:string;lineWidth?:IndicatorLineWidth;paneStretchFactor?:number}
export interface FeedKey {exchange:Exchange; market:Market; instrument:string; timeframe:Interval}
export type DrawingType = 'arrowUp'|'arrowDown'|'horizontal'|'trendline';
export interface DrawingPoint {time:number;price:number}
export interface DrawingConfig {id:string;type:DrawingType;exchange:Exchange;market:Market;instrument:string;points:DrawingPoint[]}
export interface View {mode:'live'|'historical'; fromSec:number|null; toSec:number|null; barSpacing:number; rightOffset:number}
export interface ChartConfig extends FeedKey {id:string; chartType:'candlestick'|'line'; showVolume:boolean; priceScale:{mode:'linear'|'log'; autoScale:boolean; manualRange:{min:number;max:number}|null}; view:View; indicators?:IndicatorConfig[];drawings?:DrawingConfig[]}
export interface Grid {rows:number;columns:number;rowFractions:number[];columnFractions:number[]}
export interface Tab {id:string;name:string;grid:Grid;charts:ChartConfig[]}
export interface ChartRightGap {unit:'bars'|'pixels';value:number}
export interface Workspace {schemaVersion:1;app:'cryptodesk';id:string;theme:'dark'|'light';displayTimezone:string;chartRightGap?:ChartRightGap;activeTabId:string;window:{width:number;height:number;maximized:boolean};tabs:Tab[]}
export interface Candle {time:number;open:number|string;high:number|string;low:number|string;close:number|string;volume:number|string;closed:boolean;quality:string;sourceRevision:number}
export interface Health {state:string;reason?:string;lastMessageAt?:number|string;lastTradeAt?:number|string}
export interface Snapshot {leaseId:string;revision:number;bars:Candle[];health:Health}
export interface MarketEvent extends Snapshot {chartId:string;generation:number;feedKey:FeedKey;kind:'bars'|'health'|'resyncRequired'}
export interface Instrument {nativeId:string;market:Market;baseAsset:string;quoteAsset:string;priceIncrement:string;sizeIncrement:string;availability:string;supportedIntervals:Interval[];providerMetadata?:Record<string,string>}
export interface LoadResult {workspace:Workspace;revision:number;recovery?:string}
export interface SaveResult {revision:number;acceptedEditSequence:number}
export interface CandleStorageInfo {enabled:boolean;directory:string;sizeBytes:number;error?:string}
export interface ScreenerInstrument {id:string;name:string;earliestDate:string}
export interface ScreenerCatalog {version:string;groups:Array<{name:string;instruments:ScreenerInstrument[]}>;timeframes:string[]}
export interface ScreenerDownloadRequest {symbols:string[];timeframe:string;from:string;to:string}
export interface ScreenerJob {id:string;symbol:string;timeframe:string;from:string;to:string;status:'queued'|'running'|'completed'|'failed'|'cancelled';progress:number;message:string;outputPath?:string}
export interface ScreenerInfo {directory:string;runtimeAvailable:boolean;runtimeError?:string;error?:string;jobs:ScreenerJob[]}
export interface HistoryResult {requestId:string;feedKey:FeedKey;bars:Candle[];moreHistory:boolean}
export interface Backend {
 LoadWorkspace():Promise<LoadResult>; SaveWorkspace(revision:number,sessionId:string,sequence:number,workspace:Workspace):Promise<SaveResult>;
 ListInstruments(exchange:Exchange):Promise<Instrument[]>;
 AttachChart(id:string,generation:number,key:FeedKey,fromSec:number,toSec:number):Promise<Snapshot>; DetachChart(leaseId:string):Promise<void>;
 GetFeedSnapshot(leaseId:string,revision:number):Promise<Snapshot>;
 GetHistory(requestId:string,key:FeedKey,fromSec:number,toSec:number,maxBars:number):Promise<HistoryResult>;
 GetCandleStorage():Promise<CandleStorageInfo>;ConfigureCandleStorage(enabled:boolean,directory:string):Promise<CandleStorageInfo>;ChooseCandleStorageDirectory():Promise<string>;
 GetScreenerCatalog():Promise<ScreenerCatalog>;GetScreener():Promise<ScreenerInfo>;ConfigureScreenerDirectory(directory:string):Promise<ScreenerInfo>;ChooseScreenerDirectory():Promise<string>;
 StartScreenerDownload(request:ScreenerDownloadRequest):Promise<ScreenerInfo>;CancelScreenerDownload():Promise<ScreenerInfo>;
 GetDiagnostics():Promise<Record<string,unknown>>; ImportWorkspace():Promise<LoadResult>;ExportWorkspace():Promise<string>;ResetWorkspace():Promise<LoadResult>;
 GetBackups():Promise<Array<{id:number;reason:string;createdAt:string}>>;RecoverWorkspace(id:number):Promise<LoadResult>;
 FrontendReady():Promise<void>; ConfirmClose(token:string,discard:boolean):Promise<void>;OpenTradingView():Promise<void>;
}
export interface Runtime {EventsOn(name:string,callback:(...args:any[])=>void):()=>void}
declare global {interface Window {go?:{main?:{App?:Backend}};runtime?:Runtime;__CRYPTO_TEST__?:boolean;__cryptodeskDiagnostics?:()=>Record<string,unknown>}}
export const errorText=(e:unknown)=> e instanceof Error?e.message:typeof e==='string'?e:JSON.stringify(e);
export const feedKey=(c:FeedKey):FeedKey=>({exchange:c.exchange,market:c.market,instrument:c.instrument,timeframe:c.timeframe});

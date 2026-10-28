export type AnalysisOperator='above'|'below'|'equal'|'crossesAbove'|'crossesBelow'|'between'|'withinDistance'|'occurredWithin'|'remainedFor'|'highest'|'lowest';
export interface AnalysisOperand {kind:'field'|'number';field?:string;value?:number;timeframe?:string;offset?:number;aggregate?:'value'|'highest'|'lowest'|'';window?:number}
export interface AnalysisCondition {id:string;operator:AnalysisOperator;comparison?:string;left:AnalysisOperand;right:AnalysisOperand;upper?:AnalysisOperand;distance?:AnalysisOperand;multiplier?:number;bars?:number}
export interface AnalysisTradeSimulation {stopLoss:string;stopLossBarsAgo?:number;takeProfit:string;takeProfitMultiple?:number;duration:number}
export interface AnalysisSimulationResult {takeProfitHits:number;stopLossHits:number;expired:number;insufficientData:number;invalidLevels:number;unavailable:number;profitablePercent:number;reason?:string}
export interface AnalysisRequest {conditions:AnalysisCondition[];categories?:string[]|null;tradeSimulation?:AnalysisTradeSimulation|null}
export interface AnalysisResultFilter {categories?:string[]|null;includeEmpty:boolean;profitableSort?:'asc'|'desc'|''}
export interface SavedAnalysisCondition {id:string;name:string;request:AnalysisRequest}
export interface AnalysisFile {id:string;name:string;symbol:string;timeframe:string;fields:string[];bytes:number;from?:string;to?:string;error?:string;description?:string;category?:string}
export interface AnalysisCatalog {directory:string;fields:string[];timeframes:string[];files:AnalysisFile[];categories:string[]}
export interface AnalysisResult {id:string;fileId:string;name:string;symbol:string;timeframe:string;status:string;error?:string;rows:number;matches:number;assetMatches?:number;assetFiles?:number;firstTime?:number;lastTime?:number;description?:string;category?:string;simulation?:AnalysisSimulationResult|null}
export interface AnalysisInfo {active:boolean;phase:'idle'|'indexing'|'scanning'|'completed'|'cancelled';total:number;completed:number;failed:number;currentFile:string;progress:number;rows:number;totalMatches:number;matchedFiles:number;results:AnalysisResult[];offset:number;limit:number;totalResults:number;error?:string;tradeSimulation?:AnalysisTradeSimulation|null}
export interface AnalysisBar {index:number;time:number;values:Record<string,number>}
export interface AnalysisChart {resultId:string;name:string;symbol:string;timeframe:string;occurrence:number;totalMatches:number;matchTime:number;matchIndex:number;startIndex:number;fields:string[];bars:AnalysisBar[];profitableMatches?:Array<{index:number;time:number}>}
export interface ScreenerAnalysisBackend {
 GetScreenerAnalysisCatalog():Promise<AnalysisCatalog>;
 GetScreenerAnalysisView():Promise<AnalysisRequest>;
 SaveScreenerAnalysisView(request:AnalysisRequest):Promise<AnalysisRequest>;
 GetScreenerSavedConditions():Promise<SavedAnalysisCondition[]>;
 SaveScreenerAnalysisCondition(name:string,request:AnalysisRequest):Promise<SavedAnalysisCondition>;
 RenameScreenerAnalysisCondition(id:string,name:string):Promise<SavedAnalysisCondition>;
 DeleteScreenerAnalysisCondition(id:string):Promise<void>;
 StartScreenerAnalysis(request:AnalysisRequest):Promise<AnalysisInfo>;
 GetScreenerAnalysis(offset:number,limit:number):Promise<AnalysisInfo>;
 GetScreenerAnalysisFiltered(offset:number,limit:number,filter:AnalysisResultFilter):Promise<AnalysisInfo>;
 CancelScreenerAnalysis():Promise<AnalysisInfo>;
 GetScreenerAnalysisChart(resultID:string,occurrence:number,before:number,after:number):Promise<AnalysisChart>;
}

export const numberOperand=(value=0):AnalysisOperand=>({kind:'number',value});
export const fieldOperand=(field='close'):AnalysisOperand=>({kind:'field',field,timeframe:'same',offset:0,aggregate:'value',window:1});
export const newAnalysisCondition=(id:string=crypto.randomUUID()):AnalysisCondition=>({id,operator:'above',left:fieldOperand(),right:numberOperand(),upper:numberOperand(100),distance:fieldOperand('atr_14'),multiplier:0.2,bars:3,comparison:'above'});
export const initialAnalysisView=():AnalysisRequest=>({conditions:[newAnalysisCondition('condition-1')]});
export const analysisOperators:Array<{id:AnalysisOperator;label:string}>=[
 {id:'above',label:'Is above'},{id:'below',label:'Is below'},{id:'equal',label:'Is equal'},
 {id:'crossesAbove',label:'Crosses above'},{id:'crossesBelow',label:'Crosses below'},
 {id:'between',label:'Is between'},{id:'withinDistance',label:'Is within a distance'},
 {id:'occurredWithin',label:'Occurred within'},{id:'remainedFor',label:'Remained true for'},
 {id:'highest',label:'Highest in window'},{id:'lowest',label:'Lowest in window'},
];
const priceFields=new Set(['open','high','low','close','price','bidprice','askprice']);
const fieldNames:Record<string,string>={open:'Open',high:'High',low:'Low',close:'Close',price:'Price',bidprice:'Bid price',askprice:'Ask price',volume:'Volume',askvolume:'Ask volume',bidvolume:'Bid volume',weekday:'Weekday',hour:'Hour'};
const families:Record<string,string>={bb:'Bollinger Bands',bbw:'Bollinger Band Width',donchian:'Donchian Channels',stochastic:'Stochastic',tenkan:'Ichimoku',kijun:'Ichimoku',senkou:'Ichimoku',force:'Force Index'};
function fieldParts(field:string){const [qualified,qualifiers='']=field.split(/(?=\[)/,2),colon=qualified.indexOf(':');return{base:(colon<0?qualified:qualified.slice(colon+1)).trim(),timeframe:colon<0?'':qualified.slice(0,colon),qualifiers:field.includes('[')?field.slice(field.indexOf('[')):qualifiers};}
export function analysisFieldGroup(field:string){const {base,timeframe}=fieldParts(field),key=base.toLowerCase(),family=key.split('_')[0];const group=priceFields.has(key)?'Price / OHLC':['weekday','hour'].includes(key)?'Time':/volume$/.test(key)?'Volume':families[family]??family.toUpperCase();return`${group}${timeframe?` · ${timeframe}`:''}`;}
export function analysisFieldLabel(field:string){
 const {base,timeframe,qualifiers}=fieldParts(field),key=base.toLowerCase(),suffix=`${timeframe?` · ${timeframe}`:''}${qualifiers?` ${qualifiers}`:''}`;if(fieldNames[key])return fieldNames[key]+suffix;
 const parts=key.split('_'),family=parts.shift()!,parameters=parts.filter(part=>/^\d+(?:\.\d+)?$/.test(part)),component=parts.filter(part=>!/^\d+(?:\.\d+)?$/.test(part)).map(part=>part.charAt(0).toUpperCase()+part.slice(1)).join(' ');
 return `${component||families[family]||family.toUpperCase()}${parameters.length?` (${parameters.join(', ')})`:''}${suffix}`;
}
export function analysisFieldGroups(fields:string[]){
 const groups=new Map<string,string[]>();for(const field of fields){if(field.toLowerCase()==='timestamp')continue;const group=analysisFieldGroup(field);groups.set(group,[...(groups.get(group)??[]),field]);}return [...groups].sort(([a],[b])=>a==='Price / OHLC'?-1:b==='Price / OHLC'?1:a.localeCompare(b));
}
export function analysisPriceOverlay(field:string){const base=fieldParts(field).base;return priceFields.has(base.toLowerCase())||/^(?:sma|ema|wma|wsma|rma|dema|tema|trima|zlema|hma|kama|alma|vwma|vwap|bb_(?:lower|middle|upper)|donchian|tenkan|kijun|senkou)(?:_|$)/i.test(base);}
export function analysisDate(value?:number){if(value===undefined||!Number.isFinite(value))return'—';const date=new Date(value);return Number.isFinite(date.getTime())?date.toISOString().replace('T',' ').replace('.000Z',' UTC').replace('Z',' UTC'):'Invalid UTC time';}
export function analysisInputError(view:AnalysisRequest){
 if(view.categories&&view.categories.length===0)return'Select at least one asset category before scanning.';
 if(!view.conditions.length)return'Add at least one condition before scanning.';
 if(view.conditions.length>32)return'Use at most 32 conditions.';
 if(view.tradeSimulation){
  const {stopLoss,takeProfit,stopLossBarsAgo=0,takeProfitMultiple=0}=view.tradeSimulation;
  if(![0,1,2,3].includes(takeProfitMultiple))return'Take profit must use a field, 1xSL, 2xSL or 3xSL.';
  if(!stopLoss.trim()||!takeProfitMultiple&&!takeProfit.trim())return'Choose a Stop Loss field and a Take profit field or SL multiple to simulate trades.';
  if(!Number.isInteger(stopLossBarsAgo)||stopLossBarsAgo<0||stopLossBarsAgo>10000)return'Stop Loss Bars ago must be a whole number from 0 to 10,000.';
  if(!Number.isInteger(view.tradeSimulation.duration)||view.tradeSimulation.duration<1||view.tradeSimulation.duration>10000)return'Trade duration must be a whole number from 1 to 10,000 bars.';
 }
 for(const condition of view.conditions){
  const operator=condition.operator==='occurredWithin'||condition.operator==='remainedFor'?condition.comparison||'above':condition.operator;
  const operands=[condition.left,condition.right,...(operator==='between'?[condition.upper]:[]),...(operator==='withinDistance'?[condition.distance]:[])];
  if(operands.some(operand=>!operand||!['field','number'].includes(operand.kind)||operand.kind==='number'&&!Number.isFinite(operand.value)||operand.kind==='field'&&(!operand.field||!Number.isInteger(operand.offset??0)||(operand.offset??0)<0||(operand.offset??0)>10000||operand.aggregate&&operand.aggregate!=='value'&&(!Number.isInteger(operand.window)||(operand.window??0)<1||(operand.window??0)>10000))))return'Choose a valid field or constant for every operand; bar windows and offsets cannot exceed 10,000.';
  if((['highest','lowest','occurredWithin','remainedFor'].includes(condition.operator))&&(!Number.isInteger(condition.bars)||(condition.bars??0)<1||(condition.bars??0)>10000))return'Bar windows must be whole numbers from 1 to 10,000.';
  if(operator==='withinDistance'&&(!Number.isFinite(condition.multiplier)||(condition.multiplier??0)<0))return'Distance multiplier must be a non-negative number.';
 }
 return'';
}

/** One durable writer. Edits made during a save replace only the pending draft. */
export class AnalysisViewWriter {
 private pending?:AnalysisRequest;private flight?:Promise<void>;
 constructor(private persist:(request:AnalysisRequest)=>Promise<AnalysisRequest>,private onError:(error:unknown)=>void){}
 edit(value:AnalysisRequest){this.pending=structuredClone(value);void this.flush().catch(this.onError);}
 async flush():Promise<void>{
  if(this.flight){await this.flight;if(this.pending)return this.flush();return;}
  const next=this.pending;if(!next)return;this.pending=undefined;
  this.flight=(async()=>{try{await this.persist(next);}catch(error){if(!this.pending)this.pending=next;throw error;}finally{this.flight=undefined;}})();
  await this.flight;if(this.pending)return this.flush();
 }
}

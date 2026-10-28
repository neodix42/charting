import {describe,expect,it,vi} from 'vitest';
import {analysisMatchMarkers,profitableMatchColor} from '../charts/analysisMarkers';
import type {AnalysisChart} from '../workspace/screenerAnalysis';
import {AnalysisViewWriter,analysisFieldGroup,analysisFieldGroups,analysisFieldLabel,analysisInputError,analysisPriceOverlay,fieldOperand,initialAnalysisView,newAnalysisCondition,numberOperand} from '../workspace/screenerAnalysis';

describe('Analysis draft persistence',()=>{
 it('serializes saves, snapshots edits and coalesces pending changes into the latest draft',async()=>{
  let release!:()=>void;const gate=new Promise<void>(resolve=>{release=resolve;});
  const saved=vi.fn(async value=>{if(saved.mock.calls.length===1)await gate;return value;});
  const writer=new AnalysisViewWriter(saved,()=>{}),first={...initialAnalysisView(),period:'d1' as const,tradeSimulation:{stopLoss:'low',takeProfit:'high',duration:3}};
  writer.edit(first);first.conditions[0].right=numberOperand(99);first.tradeSimulation.duration=10;
  const second=initialAnalysisView();second.conditions[0].left=fieldOperand('ema_200');writer.edit(second);
  const last={period:'h1' as const,conditions:[{...newAnalysisCondition('last'),operator:'withinDistance' as const,right:fieldOperand('ema_200'),distance:fieldOperand('atr_14'),multiplier:0.2}],tradeSimulation:{stopLoss:'low',takeProfit:'ema_200',duration:5}};
  writer.edit(last);const flushed=writer.flush();expect(saved).toHaveBeenCalledTimes(1);release();await flushed;
  expect(saved).toHaveBeenCalledTimes(2);expect(saved.mock.calls[0][0].conditions[0].right.value).toBe(0);expect(saved.mock.calls[0][0].period).toBe('d1');expect(saved.mock.calls[0][0].tradeSimulation.duration).toBe(3);expect(saved.mock.calls[1][0]).toEqual(last);
 });
 it('retains the latest draft after a failure so close or Retry can flush it',async()=>{
  const onError=vi.fn(),saved=vi.fn().mockRejectedValueOnce(Error('disk full')).mockImplementation(async value=>value);
  const writer=new AnalysisViewWriter(saved,onError),latest={conditions:[{...newAnalysisCondition('restored'),operator:'highest' as const,bars:20,right:fieldOperand('high')}]};
  writer.edit(latest);await expect(writer.flush()).rejects.toThrow('disk full');await writer.flush();
  expect(onError).toHaveBeenCalled();expect(saved.mock.calls[1][0]).toEqual(latest);
 });
});

describe('Analysis field selection and validation',()=>{
 it('keeps MACD components grouped and qualified higher-timeframe labels explicit',()=>{
  expect(analysisFieldGroup('macd_histogram_12_26_9')).toBe('MACD');
  expect(analysisFieldLabel('macd_histogram_12_26_9')).toBe('Histogram (12, 26, 9)');
  expect(analysisFieldLabel('d1:macd_histogram_12_26_9[offset 1]')).toBe('Histogram (12, 26, 9) · d1 [offset 1]');
  expect(analysisFieldGroup('d1:macd_histogram_12_26_9')).toBe('MACD · d1');
  expect(analysisFieldGroups(['timestamp','close','macd_12_26','macd_histogram_12_26_9']).map(([group])=>group)).toEqual(['Price / OHLC','MACD']);
 });
 it('classifies price overlays and separate indicator panes even with timeframe/offset qualifiers',()=>{
  for(const field of ['ema_200','bb_upper_20_2','senkou_a_9_26','d1:sma_200[offset 1][highest 20]','d1:close'])expect(analysisPriceOverlay(field)).toBe(true);
  for(const field of ['atr_14','macd_signal_12_26_9','rsi_14','d1:macd_histogram_12_26_9'])expect(analysisPriceOverlay(field)).toBe(false);
 });
 it('groups calendar fields as UTC time values, including qualified timeframe fields',()=>{
  expect(analysisFieldGroup('weekday')).toBe('Time');expect(analysisFieldGroup('hour')).toBe('Time');
  expect(analysisFieldLabel('weekday')).toBe('Weekday');expect(analysisFieldLabel('hour')).toBe('Hour');
  expect(analysisFieldGroup('d1:weekday[offset 1]')).toBe('Time · d1');
  expect(analysisFieldLabel('d1:weekday[offset 1]')).toBe('Weekday · d1 [offset 1]');
  expect(analysisPriceOverlay('hour')).toBe(false);
 });
 it('requires both sides of highest/lowest, permits negative constants and checks distance/window bounds',()=>{
  expect(analysisInputError(initialAnalysisView())).toBe('');
  const rule={...newAnalysisCondition('1'),operator:'highest' as const,bars:20,right:fieldOperand('high')};
  expect(analysisInputError({conditions:[rule]})).toBe('');
  expect(analysisInputError({conditions:[{...rule,right:{kind:'field'}}]})).toContain('valid field');
  expect(analysisInputError({conditions:[{...rule,right:{kind:''} as never}]})).toContain('valid field');
  expect(analysisInputError({conditions:[{...rule,right:numberOperand(-100)}]})).toBe('');
  expect(analysisInputError({conditions:[{...rule,bars:10001}]})).toContain('10,000');
  expect(analysisInputError({conditions:[{...rule,left:{...fieldOperand(),offset:10001}}]})).toContain('10,000');
  expect(analysisInputError({conditions:[{...rule,operator:'withinDistance',multiplier:-0.2}]})).toContain('non-negative');
 });
 it('validates temporal between/distance operands and every AND row',()=>{
  const temporal={...newAnalysisCondition('1'),operator:'occurredWithin' as const,comparison:'between',bars:3,upper:numberOperand(10)};
  expect(analysisInputError({conditions:[temporal]})).toBe('');
  expect(analysisInputError({conditions:[{...temporal,upper:undefined}]})).toContain('valid field');
  expect(analysisInputError({conditions:[{...temporal,comparison:'withinDistance',distance:undefined}]})).toContain('valid field');
  expect(analysisInputError({conditions:[]})).toContain('at least one');
  expect(analysisInputError({conditions:Array.from({length:33},(_,i)=>newAnalysisCondition(String(i)))})).toContain('32');
 });
 it('keeps legacy all-category requests valid while rejecting an explicitly empty scan scope',()=>{
  const request=initialAnalysisView();
  expect(analysisInputError(request)).toBe('');
  expect(analysisInputError({...request,categories:null})).toBe('');
  expect(analysisInputError({...request,categories:['Forex']})).toBe('');
  expect(analysisInputError({...request,categories:[]})).toContain('Select at least one asset category');
 });
 it('keeps legacy all-period requests valid and accepts only supported base periods',()=>{
  const request=initialAnalysisView();
  expect(analysisInputError(request)).toBe('');
  for(const period of ['','all','d1','h1'] as const)expect(analysisInputError({...request,period})).toBe('');
  for(const period of ['h4','D1','same'])expect(analysisInputError({...request,period:period as never})).toContain('Choose All periods, d1 or h1');
 });
 it('requires both simulation fields and a bounded integer duration, while retaining legacy requests',()=>{
  const request=initialAnalysisView(),tradeSimulation={stopLoss:'low',takeProfit:'ema_200',duration:20};
  expect(analysisInputError({...request,tradeSimulation})).toBe('');
  expect(analysisInputError({...request,tradeSimulation:null})).toBe('');
  for(const field of ['stopLoss','takeProfit'] as const)expect(analysisInputError({...request,tradeSimulation:{...tradeSimulation,[field]:' '}})).toContain('Choose a Stop Loss field and a Take profit field');
  for(const duration of [0,-1,1.5,10001,NaN,Infinity])expect(analysisInputError({...request,tradeSimulation:{...tradeSimulation,duration}})).toContain('whole number');
  for(const duration of [1,10000])expect(analysisInputError({...request,tradeSimulation:{...tradeSimulation,duration}})).toBe('');
 });
});


describe('Simulation offsets and risk targets',()=>{
 it('accepts old field targets and validates bars ago and each supported risk multiple',()=>{
  const request=initialAnalysisView(),simulation={stopLoss:'low',takeProfit:'high',duration:20};
  for(const stopLossBarsAgo of [0,1,10000])expect(analysisInputError({...request,tradeSimulation:{...simulation,stopLossBarsAgo}})).toBe('');
  for(const stopLossBarsAgo of [-1,0.5,10001,NaN,Infinity])expect(analysisInputError({...request,tradeSimulation:{...simulation,stopLossBarsAgo}})).toContain('Stop Loss Bars ago');
  for(const takeProfitMultiple of [1,2,3])expect(analysisInputError({...request,tradeSimulation:{...simulation,takeProfit:'',takeProfitMultiple}})).toBe('');
  for(const takeProfitMultiple of [-1,0.5,4,NaN,Infinity])expect(analysisInputError({...request,tradeSimulation:{...simulation,takeProfit:'',takeProfitMultiple}})).toContain('1xSL, 2xSL or 3xSL');
  expect(analysisInputError({...request,tradeSimulation:{...simulation,takeProfitMultiple:0}})).toBe('');
  expect(analysisInputError({...request,tradeSimulation:{...simulation,takeProfit:'',takeProfitMultiple:0}})).toContain('Choose a Stop Loss');
  expect(analysisInputError({...request,tradeSimulation:{...simulation,stopLoss:' ',takeProfitMultiple:2}})).toContain('Choose a Stop Loss');
 });
});

describe('Profitable match chart markers',()=>{
 const chart:AnalysisChart={resultId:'file',name:'test',symbol:'test',timeframe:'d1',occurrence:1,totalMatches:3,matchTime:2000,matchIndex:1,startIndex:0,fields:['close'],bars:[0,1,2].map(index=>({index,time:(index+1)*1000,values:{close:10}}))};
 it('draws only visible winners, deduplicates timestamps and keeps selected arrow in chronological order',()=>{
  const data={...chart,profitableMatches:[{index:2,time:3000},{index:1,time:2000},{index:1,time:2000},{index:0,time:1000},{index:3,time:4000},{index:99,time:3000},{index:1,time:NaN}]};
  const markers=analysisMatchMarkers(data,'orange');
  expect(markers.map(marker=>[marker.time,marker.shape])).toEqual([[1,'circle'],[2,'circle'],[2,'arrowUp'],[3,'circle']]);
  expect(markers.filter(marker=>marker.shape==='circle').every(marker=>marker.position==='belowBar'&&marker.color===profitableMatchColor)).toBe(true);
  expect(markers.find(marker=>marker.shape==='arrowUp')).toMatchObject({color:'orange',text:'Match',position:'belowBar'});
  expect(data.profitableMatches).toHaveLength(7);
 });
 it('keeps winning circles green through theme changes and supports legacy/no-winning chart responses',()=>{
  const data={...chart,profitableMatches:[{index:1,time:2000}]};
  for(const color of ['#111111','#ffffff','#ff00ff','#fda352'])expect(analysisMatchMarkers(data,color)[0]).toMatchObject({color:'#16a34a',shape:'circle'});
  expect(analysisMatchMarkers(chart,'blue')).toEqual([{time:2,position:'belowBar',color:'blue',shape:'arrowUp',text:'Match'}]);
  expect(analysisMatchMarkers({...chart,profitableMatches:[]},'blue')).toHaveLength(1);
 });
 it('stacks recorded Entry, SL and TP below Match without adding visible shapes or requiring a win',()=>{
  const markers=analysisMatchMarkers({...chart,trade:{entry:105,stopLoss:90,takeProfit:135}},'orange');
  expect(markers.map(marker=>marker.text)).toEqual(['Match','Entry 105','SL 90','TP 135']);
  for(const marker of markers.slice(1))expect(marker).toMatchObject({time:2,position:'belowBar',size:0,color:'orange'});
  expect(analysisMatchMarkers({...chart,trade:{entry:0.0000002,stopLoss:0.000000123456789,takeProfit:0.000000234567891}},'blue').slice(1).map(marker=>marker.text)).toEqual(['Entry 0.0000002','SL 0.000000123456789','TP 0.000000234567891']);
  for(const field of ['entry','stopLoss','takeProfit'])for(const invalid of [NaN,Infinity])expect(analysisMatchMarkers({...chart,trade:{entry:105,stopLoss:90,takeProfit:135,[field]:invalid}},'blue')).toHaveLength(1);
  expect(analysisMatchMarkers({...chart,trade:{entry:0,stopLoss:-1,takeProfit:1}},'blue')[1].text).toBe('Entry 0');
 });
});

import {describe,expect,it,vi} from 'vitest';
import {analysisEntryLabel,analysisMatchMarkers,analysisTradeExecutionDetails,profitableMatchColor} from '../charts/analysisMarkers';
import type {AnalysisChart} from '../workspace/screenerAnalysis';
import {AnalysisViewWriter,analysisFieldGroup,analysisFieldGroups,analysisFieldLabel,analysisInputError,analysisPriceOverlay,analysisRequestIdentity,changeSimulationDirection,fieldOperand,initialAnalysisView,newAnalysisCondition,numberOperand} from '../workspace/screenerAnalysis';

describe('Analysis draft persistence',()=>{
 it('defaults to Dukascopy and keeps source identity distinct while accepting legacy drafts',()=>{
  const {dataSource,...legacy}=initialAnalysisView();
  expect(dataSource).toBe('dukascopy');
  expect(analysisRequestIdentity(legacy)).toBe(analysisRequestIdentity({...legacy,dataSource:'dukascopy'}));
  expect(analysisRequestIdentity({...legacy,dataSource:'hyperliquid'})).not.toBe(analysisRequestIdentity(legacy));
  expect(analysisRequestIdentity({...legacy,dataSource:'hyperliquid'},false)).not.toBe(analysisRequestIdentity(legacy,false));
 });
 it('persists the selected data source in the serial draft writer',async()=>{
  const saved=vi.fn(async value=>value),writer=new AnalysisViewWriter(saved,()=>{}),draft={...initialAnalysisView(),dataSource:'hyperliquid' as const};
  writer.edit(draft);await writer.flush();
  expect(saved).toHaveBeenCalledWith(draft);
 });
 it('serializes saves, snapshots edits and coalesces pending changes into the latest draft',async()=>{
  let release!:()=>void;const gate=new Promise<void>(resolve=>{release=resolve;});
  const saved=vi.fn(async value=>{if(saved.mock.calls.length===1)await gate;return value;});
  const writer=new AnalysisViewWriter(saved,()=>{}),first={...initialAnalysisView(),period:'d1' as const,tradeSimulation:{stopLoss:'low',takeProfit:'high',duration:3}};
  writer.edit(first);const firstFlush=writer.flush();first.conditions[0].right=numberOperand(99);first.tradeSimulation.duration=10;
  const second=initialAnalysisView();second.conditions[0].left=fieldOperand('ema_200');writer.edit(second);
  const last={period:'h1' as const,conditions:[{...newAnalysisCondition('last'),operator:'withinDistance' as const,right:fieldOperand('ema_200'),distance:fieldOperand('atr_14'),multiplier:0.2}],tradeSimulation:{stopLoss:'low',takeProfit:'ema_200',duration:5}};
  writer.edit(last);const flushed=writer.flush();await Promise.resolve();expect(saved).toHaveBeenCalledTimes(1);release();await firstFlush;await flushed;
  expect(saved).toHaveBeenCalledTimes(2);expect(saved.mock.calls[0][0].conditions[0].right.value).toBe(0);expect(saved.mock.calls[0][0].period).toBe('d1');expect(saved.mock.calls[0][0].tradeSimulation.duration).toBe(3);expect(saved.mock.calls[1][0]).toEqual(last);
 });
 it('retains the latest draft after a failure so close or Retry can flush it',async()=>{
  const onError=vi.fn(),saved=vi.fn().mockRejectedValueOnce(Error('disk full')).mockImplementation(async value=>value);
  const writer=new AnalysisViewWriter(saved,onError),latest={conditions:[{...newAnalysisCondition('restored'),operator:'highest' as const,bars:20,right:fieldOperand('high')}]};
  writer.edit(latest);await expect(writer.flush()).rejects.toThrow('disk full');await writer.flush();
  expect(saved.mock.calls[1][0]).toEqual(latest);
 });
 it('debounces rapid edits, saves only the latest snapshot and avoids equivalent drafts',async()=>{
  vi.useFakeTimers();
  try{
   const saved=vi.fn(async value=>value),writer=new AnalysisViewWriter(saved,()=>{}),initial=initialAnalysisView();writer.restore(initial);
   writer.edit({...initial,dataSource:undefined});await vi.advanceTimersByTimeAsync(1000);expect(saved).not.toHaveBeenCalled();
   writer.edit({...initial,period:'d1'});await vi.advanceTimersByTimeAsync(400);writer.edit({...initial,period:'w1'});await vi.advanceTimersByTimeAsync(499);expect(saved).not.toHaveBeenCalled();
   await vi.advanceTimersByTimeAsync(1);expect(saved).toHaveBeenCalledTimes(1);expect(saved.mock.calls[0][0].period).toBe('w1');
   writer.edit({...initial,period:'w1'});await writer.flush();expect(saved).toHaveBeenCalledTimes(1);
  }finally{vi.useRealTimers();}
 });
 it('explicit flush bypasses the debounce and a reverted in-flight edit needs no duplicate write',async()=>{
  let release!:()=>void;const gate=new Promise<void>(resolve=>{release=resolve;}),saved=vi.fn(async value=>{await gate;return value;});
  const writer=new AnalysisViewWriter(saved,()=>{}),first={...initialAnalysisView(),period:'d1' as const};writer.edit(first);const flushed=writer.flush();await Promise.resolve();expect(saved).toHaveBeenCalledTimes(1);
  writer.edit({...first,period:'w1'});writer.edit(first);release();await flushed;expect(saved).toHaveBeenCalledTimes(1);
 });
 it('scheduled failures notify once and keep the latest pending edit available for explicit retry',async()=>{
  vi.useFakeTimers();
  try{
   const onError=vi.fn(),saved=vi.fn().mockRejectedValueOnce(Error('disk full')).mockImplementation(async value=>value),writer=new AnalysisViewWriter(saved,onError),latest={...initialAnalysisView(),period:'h1' as const};
   writer.edit(latest);await vi.advanceTimersByTimeAsync(500);expect(onError).toHaveBeenCalledTimes(1);await writer.flush();expect(saved).toHaveBeenCalledTimes(2);expect(saved.mock.calls[1][0]).toEqual(latest);
  }finally{vi.useRealTimers();}
 });
 it('concurrent flush callers wait for the latest write started by another waiter',async()=>{
  const gates:Array<{resolve():void;reject(error:Error):void}>=[],saved=vi.fn(value=>new Promise<typeof value>((resolve,reject)=>gates.push({resolve:()=>resolve(value),reject})));
  const writer=new AnalysisViewWriter(saved,()=>{}),initial=initialAnalysisView();writer.edit({...initial,period:'d1'});
  const first=writer.flush();await Promise.resolve();const second=writer.flush();let secondDone=false;void second.then(()=>{secondDone=true;});
  writer.edit({...initial,period:'w1'});gates[0].resolve();await vi.waitFor(()=>expect(saved).toHaveBeenCalledTimes(2));
  expect(secondDone).toBe(false);gates[1].resolve();await Promise.all([first,second]);expect(secondDone).toBe(true);
 });
 it('all concurrent flush callers observe a later write failure and a retry retains that latest draft',async()=>{
  const gates:Array<{resolve():void;reject(error:Error):void}>=[],saved=vi.fn(value=>new Promise<typeof value>((resolve,reject)=>gates.push({resolve:()=>resolve(value),reject})));
  const writer=new AnalysisViewWriter(saved,()=>{}),initial=initialAnalysisView();writer.edit({...initial,period:'d1'});
  const first=writer.flush();await Promise.resolve();const second=writer.flush();writer.edit({...initial,period:'h1'});
  const settled=Promise.allSettled([first,second]);gates[0].resolve();await vi.waitFor(()=>expect(saved).toHaveBeenCalledTimes(2));gates[1].reject(Error('late disk failure'));
  const results=await settled;expect(results.every(result=>result.status==='rejected'&&String(result.reason).includes('late disk failure'))).toBe(true);
  const retry=writer.flush();await Promise.resolve();expect(saved).toHaveBeenCalledTimes(3);expect(saved.mock.calls[2][0].period).toBe('h1');gates[2].resolve();await retry;
 });
});

describe('Analysis field selection and validation',()=>{
 it('accepts downloaded source choices and rejects other providers',()=>{
  const draft=initialAnalysisView();
  for(const dataSource of [undefined,'dukascopy','hyperliquid'] as const)expect(analysisInputError({...draft,dataSource})).toBe('');
  expect(analysisInputError({...draft,dataSource:'binance' as never})).toContain('Dukascopy or Hyperliquid');
 });
 it('keeps MACD components grouped and qualified higher-timeframe labels explicit',()=>{
  expect(analysisFieldGroup('macd_histogram_12_26_9')).toBe('MACD');
  expect(analysisFieldLabel('macd_histogram_12_26_9')).toBe('Histogram (12, 26, 9)');
  expect(analysisFieldLabel('d1:macd_histogram_12_26_9[offset 1]')).toBe('Histogram (12, 26, 9) · d1 [offset 1]');
  expect(analysisFieldGroup('d1:macd_histogram_12_26_9')).toBe('MACD · d1');
  expect(analysisFieldGroups(['timestamp','close','macd_12_26','macd_histogram_12_26_9']).map(([group])=>group)).toEqual(['Price / OHLC','MACD']);
 });
 it('classifies price overlays and separate indicator panes even with timeframe/offset qualifiers',()=>{
  for(const field of ['low_10','high_10','d1:low_200[offset 1]','ema_200','bb_upper_20_2','senkou_a_9_26','d1:sma_200[offset 1][highest 20]','d1:close'])expect(analysisPriceOverlay(field)).toBe(true);
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
  for(const period of ['','all','w1','d1','h1'] as const)expect(analysisInputError({...request,period})).toBe('');
  for(const period of ['h4','D1','same'])expect(analysisInputError({...request,period:period as never})).toContain('Choose All periods, w1, d1 or h1');
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
 it('retains legacy Long, accepts Short and rejects unsupported trade directions',()=>{
  const request=initialAnalysisView(),simulation={stopLoss:'high',takeProfit:'low',duration:20};
  for(const direction of [undefined,'','long','short'] as const)expect(analysisInputError({...request,tradeSimulation:{...simulation,direction}})).toBe('');
  for(const direction of ['Short','both','buy'])expect(analysisInputError({...request,tradeSimulation:{...simulation,direction:direction as never}})).toContain('Choose Long or Short');
 });
 it('adapts conventional levels by direction while preserving custom indicators, offsets and risk targets',()=>{
  const simulation={stopLoss:'low',stopLossBarsAgo:2,takeProfit:'high',duration:5},fields=['low','high','ema_200'];
  const short=changeSimulationDirection(simulation,'short',fields);
  expect(short).toEqual({...simulation,direction:'short',stopLoss:'high',takeProfit:'low'});
  expect(changeSimulationDirection(short,'long',fields)).toEqual({...simulation,direction:'long'});
  expect(simulation).toEqual({stopLoss:'low',stopLossBarsAgo:2,takeProfit:'high',duration:5});
  const custom={...simulation,stopLoss:'ema_200',takeProfit:'bb_lower_20_2'};
  expect(changeSimulationDirection(custom,'short',fields)).toEqual({...custom,direction:'short'});
  const multiple={...simulation,takeProfit:'',takeProfitMultiple:3};
  expect(changeSimulationDirection(multiple,'short',fields)).toEqual({...multiple,direction:'short',stopLoss:'high'});
 });
 it('offers Short OHLC defaults only for fields actually in the downloaded files',()=>{
  const simulation={stopLoss:'',takeProfit:'',duration:20};
  expect(changeSimulationDirection(simulation,'short',['high','low'])).toEqual({...simulation,direction:'short',stopLoss:'high',takeProfit:'low'});
  expect(changeSimulationDirection(simulation,'short',['bidprice'])).toEqual({...simulation,direction:'short'});
 });
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
 it('replaces the old price stack with one Long or Short entry label on the signal arrow',()=>{
  for(const direction of ['long','short'] as const){
   const data={...chart,direction,trade:{entry:105,stopLoss:90,takeProfit:135}},markers=analysisMatchMarkers(data,'orange');
   expect(markers).toEqual([{time:2,position:'belowBar',color:'orange',shape:'arrowUp',text:`${direction==='short'?'Short':'Long'} at 105`}]);
   expect(data.trade).toEqual({entry:105,stopLoss:90,takeProfit:135});
  }
  expect(analysisEntryLabel({...chart,direction:'long',trade:{entry:0.0000002,stopLoss:0.000000123456789,takeProfit:0.000000234567891}})).toBe('Long at 0.0000002');
  expect(analysisEntryLabel({...chart,direction:'short',trade:{entry:12345.6789012345,stopLoss:12350,takeProfit:12000}})).toBe('Short at 12,345.6789012345');
 });
 it('keeps unknown direction or unavailable entry as a generic match without guessing a trade',()=>{
  const trade={entry:105,stopLoss:90,takeProfit:135};
  for(const data of [{...chart,trade},{...chart,direction:'long' as const},{...chart,direction:'long' as const,trade:{...trade,entry:NaN}},{...chart,direction:'short' as const,signal:{entry:Infinity}}]){
   expect(analysisEntryLabel(data)).toBeUndefined();
   expect(analysisMatchMarkers(data,'blue')).toEqual([{time:2,position:'belowBar',color:'blue',shape:'arrowUp',text:'Match'}]);
  }
 });
 it('uses the live signal entry without inventing protective exits or a future fill',()=>{
  for(const signal of [{entry:105},{entry:105,stopLoss:90},{entry:105,takeProfit:135},{entry:105,stopLoss:90,takeProfit:135}]){
   const data={...chart,direction:'short' as const,signal};
   expect(analysisMatchMarkers(data,'orange').map(marker=>marker.text)).toEqual(['Short at 105']);
   expect(analysisTradeExecutionDetails(data)).toEqual([]);
  }
  expect(analysisEntryLabel({...chart,direction:'long',signal:{entry:105},trade:{entry:999,stopLoss:900,takeProfit:1100}})).toBe('Long at 105');
 });
 it('does not require hidden protective levels to display a verified directional entry',()=>{
  expect(analysisEntryLabel({...chart,direction:'long',trade:{entry:0,stopLoss:NaN,takeProfit:Infinity}})).toBe('Long at 0');
 });
});

describe('Selected simulated trade timing and outcome',()=>{
 const chart:AnalysisChart={resultId:'file',name:'test',symbol:'test',timeframe:'d1',occurrence:0,totalMatches:1,matchTime:Date.UTC(2025,0,8),matchIndex:0,startIndex:0,fields:['close'],bars:[{index:0,time:Date.UTC(2025,0,8),values:{close:100}}]};
 it('reports the recorded signal, entry and exit bars even when execution is outside the visible chart',()=>{
  const details=analysisTradeExecutionDetails({...chart,tradeExecution:{entryTime:Date.UTC(2025,0,9),outcome:'stopLoss',exitTime:Date.UTC(2025,0,10)}});
  expect(details).toEqual([{label:'Signal bar start',value:'2025-01-08 00:00:00 UTC'},{label:'Entry bar start',value:'2025-01-09 00:00:00 UTC'},{label:'Outcome',value:'Stop Loss hit'},{label:'Exit bar start',value:'2025-01-10 00:00:00 UTC'}]);
 });
 it('keeps Stop Loss outcomes distinct from TP independently of Long or Short price brackets',()=>{
  for(const trade of [{entry:100,stopLoss:90,takeProfit:110},{entry:100,stopLoss:110,takeProfit:90}]){
   const details=analysisTradeExecutionDetails({...chart,trade,tradeExecution:{entryTime:Date.UTC(2025,0,9),outcome:'stopLoss',exitTime:Date.UTC(2025,0,9)}});
   expect(details.find(detail=>detail.label==='Outcome')?.value).toBe('Stop Loss hit');
   expect(details.some(detail=>detail.value==='Take Profit hit')).toBe(false);
  }
  expect(analysisTradeExecutionDetails({...chart,tradeExecution:{entryTime:Date.UTC(2025,0,9),outcome:'takeProfit',exitTime:Date.UTC(2025,0,9)}}).find(detail=>detail.label==='Outcome')?.value).toBe('Take Profit hit');
 });
 it('does not invent an exit for unresolved outcomes or legacy chart responses',()=>{
  expect(analysisTradeExecutionDetails(chart)).toEqual([]);
  expect(analysisTradeExecutionDetails({...chart,tradeExecution:{entryTime:NaN,outcome:'stopLoss'}})).toEqual([]);
  for(const outcome of ['insufficientData','unavailable'] as const){
   const details=analysisTradeExecutionDetails({...chart,tradeExecution:{entryTime:Date.UTC(2025,0,9),outcome,exitTime:Date.UTC(2025,0,10)}});
   expect(details.map(detail=>detail.label)).toEqual(['Signal bar start','Entry bar start','Outcome']);
  }
  expect(analysisTradeExecutionDetails({...chart,tradeExecution:{entryTime:Date.UTC(2025,0,9),outcome:'expired',exitTime:Date.UTC(2025,0,10)}}).at(-1)).toEqual({label:'Exit bar start',value:'2025-01-10 00:00:00 UTC'});
 });
});

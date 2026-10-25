import {describe,expect,it,vi} from 'vitest';
import {AnalysisViewWriter,analysisFieldGroup,analysisFieldGroups,analysisFieldLabel,analysisInputError,analysisPriceOverlay,fieldOperand,initialAnalysisView,newAnalysisCondition,numberOperand} from '../workspace/screenerAnalysis';

describe('Analysis draft persistence',()=>{
 it('serializes saves, snapshots edits and coalesces pending changes into the latest draft',async()=>{
  let release!:()=>void;const gate=new Promise<void>(resolve=>{release=resolve;});
  const saved=vi.fn(async value=>{if(saved.mock.calls.length===1)await gate;return value;});
  const writer=new AnalysisViewWriter(saved,()=>{}),first=initialAnalysisView();
  writer.edit(first);first.conditions[0].right=numberOperand(99);
  const second=initialAnalysisView();second.conditions[0].left=fieldOperand('ema_200');writer.edit(second);
  const last={conditions:[{...newAnalysisCondition('last'),operator:'withinDistance' as const,right:fieldOperand('ema_200'),distance:fieldOperand('atr_14'),multiplier:0.2}]};
  writer.edit(last);const flushed=writer.flush();expect(saved).toHaveBeenCalledTimes(1);release();await flushed;
  expect(saved).toHaveBeenCalledTimes(2);expect(saved.mock.calls[0][0].conditions[0].right.value).toBe(0);expect(saved.mock.calls[1][0]).toEqual(last);
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
});

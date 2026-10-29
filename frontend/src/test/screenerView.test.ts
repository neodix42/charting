import {expect,it,vi} from 'vitest';
import {initialScreenerView,normalizeScreenerView,ScreenerViewWriter} from '../workspace/screenerView';

it('serializes durable Screener choices and flushes the latest symbols, period and category states',async()=>{
 let release!:()=>void;const first=new Promise<void>(resolve=>{release=resolve;});
 const persist=vi.fn(async value=>{if(persist.mock.calls.length===1)await first;return value;});
 const writer=new ScreenerViewWriter(persist,()=>{}),view=initialScreenerView();
 writer.edit({...view,symbols:['btcusd']});writer.edit({...view,symbols:['ethusd'],timeframe:'m15'});writer.edit({...view,symbols:['btcusd','ethusd'],timeframe:'h4',concurrency:7,collapsedCategories:['Bonds']});
 expect(persist).toHaveBeenCalledTimes(1);const flushed=writer.flush();release();await flushed;
 expect(persist).toHaveBeenCalledTimes(2);expect(persist.mock.calls[1][0]).toMatchObject({symbols:['btcusd','ethusd'],timeframe:'h4',concurrency:7,collapsedCategories:['Bonds']});
});

it('retains a failed latest selection so controlled close can retry instead of losing it',async()=>{
 const error=vi.fn(),persist=vi.fn().mockRejectedValueOnce(Error('disk full')).mockImplementation(async value=>value);
 const writer=new ScreenerViewWriter(persist,error),view={...initialScreenerView(),timeframe:'m1'};
 writer.edit(view);await expect(writer.flush()).rejects.toThrow('disk full');await writer.flush();
 expect(error).toHaveBeenCalled();expect(persist.mock.calls[1][0]).toEqual(view);
});

it('preserves legacy worker metadata without forcing serial downloads or changing saved selections',async()=>{
 const original={...initialScreenerView(),symbols:['btcusd','ethusd'],timeframe:'h4',from:'2021-01-01',to:'2024-01-01',concurrency:20,collapsedCategories:['Bonds','Forex']};
 const normalized=normalizeScreenerView(original);
 expect(normalized).toEqual(original);expect(original.concurrency).toBe(20);
 const persist=vi.fn(async value=>value),writer=new ScreenerViewWriter(persist,()=>{});
 writer.edit(original);await writer.flush();expect(persist).toHaveBeenCalledWith(normalized);
 expect(initialScreenerView().concurrency).toBe(5);
});

it('normalizes invalid compatibility worker counts without aliasing saved symbol selections',()=>{
 for(const concurrency of [0,-1,21,1.5,NaN]){
  const original={...initialScreenerView(),symbols:['btcusd'],concurrency},normalized=normalizeScreenerView(original);
  expect(normalized.concurrency).toBe(5);normalized.symbols.push('ethusd');expect(original.symbols).toEqual(['btcusd']);
 }
});

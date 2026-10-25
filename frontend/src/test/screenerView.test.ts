import {expect,it,vi} from 'vitest';
import {initialScreenerView,ScreenerViewWriter} from '../workspace/screenerView';

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

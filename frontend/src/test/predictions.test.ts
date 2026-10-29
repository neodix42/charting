import {expect,it,vi} from 'vitest';
import {initialPredictionSelection,predictionBarLabel,predictionBytes,predictionModelKey,PredictionSelectionWriter} from '../workspace/predictions';

it('serializes prediction selections and flushes the latest edit after an in-flight write',async()=>{
 let release!:()=>void;const held=new Promise<void>(resolve=>{release=resolve;});
 const persist=vi.fn(async value=>{if(persist.mock.calls.length===1)await held;return value;});
 const writer=new PredictionSelectionWriter(persist,()=>{}),draft={...initialPredictionSelection(),asset:'btcusd'};
 writer.edit(draft);draft.asset='mutated';writer.edit({...initialPredictionSelection(),asset:'eurusd',contextLength:1024,horizon:256,forecastId:'saved'});
 const flushing=writer.flush();await Promise.resolve();expect(persist).toHaveBeenCalledTimes(1);expect(writer.busy).toBe(true);release();await flushing;
 expect(persist.mock.calls[0][0].asset).toBe('btcusd');expect(persist.mock.calls[1][0]).toMatchObject({asset:'eurusd',contextLength:1024,horizon:256,forecastId:'saved'});expect(writer.busy).toBe(false);
});
it('retains a failed prediction selection until an explicit flush succeeds',async()=>{
 const persist=vi.fn().mockRejectedValueOnce(Error('disk full')).mockImplementation(async value=>value),errors=vi.fn(),writer=new PredictionSelectionWriter(persist,errors);
 writer.edit({...initialPredictionSelection(),asset:'btcusd'});await expect(writer.flush()).rejects.toThrow('disk full');expect(writer.busy).toBe(true);await writer.flush();expect(errors).toHaveBeenCalled();expect(writer.busy).toBe(false);
});
it('recovers from a synchronous bridge exception instead of retaining a rejected flight forever',async()=>{
 let attempts=0;const errors=vi.fn(),persist=vi.fn(value=>{if(++attempts===1)throw Error('bridge not ready');return Promise.resolve(value);}),writer=new PredictionSelectionWriter(persist,errors);
 writer.edit({...initialPredictionSelection(),asset:'eurusd'});await expect(writer.flush()).rejects.toThrow('bridge not ready');expect(writer.busy).toBe(true);
 await writer.flush();expect(persist).toHaveBeenCalledTimes(2);expect(persist.mock.calls[1][0].asset).toBe('eurusd');expect(writer.busy).toBe(false);
 writer.edit({...initialPredictionSelection(),asset:'btcusd'});await writer.flush();expect(persist.mock.calls[2][0].asset).toBe('btcusd');expect(writer.busy).toBe(false);
});
it('maps history to real UTC times and future bars to offsets without fabricating weekend dates',()=>{
 const data={history:[{time:Date.UTC(2026,9,23),close:100},{time:Date.UTC(2026,9,26),close:101}]};
 expect(predictionBarLabel(data,1)).toBe('2026-10-23 00:00');expect(predictionBarLabel(data,2,true)).toBe('2026-10-26 00:00:00 UTC');expect(predictionBarLabel(data,3)).toBe('+1');expect(predictionBarLabel(data,22,true)).toBe('+20');
 expect(predictionModelKey({model:'a',modelDirectory:'b'})).not.toBe(predictionModelKey({model:'a',modelDirectory:'c'}));expect(predictionBytes(104857600)).toBe('100 MiB');expect(predictionBytes(0)).toBe('0 B');
});

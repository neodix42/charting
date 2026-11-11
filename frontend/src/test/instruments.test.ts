import {beforeEach,describe,expect,it,vi} from 'vitest';
import type {Backend,Instrument} from '../types';
import {cachedInstruments,clearInstrumentCatalog,instruments,refreshDucascopyCatalog,subscribeInstrumentCatalog} from '../bridge/instruments';
const item=(nativeId:string):Instrument=>({nativeId,market:'forex',baseAsset:nativeId.slice(0,3),quoteAsset:'USD',priceIncrement:'0.00001',sizeIncrement:'',availability:'available',supportedIntervals:['1h']});
beforeEach(()=>{clearInstrumentCatalog('dukascopy');clearInstrumentCatalog('binance');});
describe('saved instrument snapshots and background refresh',()=>{
 it('shares initial discovery and keeps the last snapshot available during a coalesced background check',async()=>{
  let finish!:(items:Instrument[])=>void;const old=[item('EURUSD')],fresh=[...old,item('GBPUSD')];
  const api={ListInstruments:vi.fn(async()=>old),RefreshDucascopyInstruments:vi.fn(()=>new Promise<Instrument[]>(resolve=>{finish=resolve;}))} as unknown as Backend;
  const first=instruments(api,'dukascopy');expect(instruments(api,'dukascopy')).toBe(first);await first;
  const changed=vi.fn(),unsubscribe=subscribeInstrumentCatalog('dukascopy',changed);
  const pending=refreshDucascopyCatalog(api);expect(refreshDucascopyCatalog(api)).toBe(pending);expect(cachedInstruments('dukascopy')).toEqual(old);expect(await instruments(api,'dukascopy')).toEqual(old);
  finish(fresh);await pending;expect(changed).toHaveBeenCalledWith(fresh);expect(await instruments(api,'dukascopy')).toEqual(fresh);expect(api.ListInstruments).toHaveBeenCalledTimes(1);expect(api.RefreshDucascopyInstruments).toHaveBeenCalledTimes(1);unsubscribe();
 });
 it('retains usable choices on refresh failure and supports an explicit forced retry',async()=>{
  const old=[item('EURUSD')];const api={ListInstruments:vi.fn(async()=>old),RefreshDucascopyInstruments:vi.fn().mockRejectedValueOnce(Error('offline')).mockResolvedValueOnce([...old,item('GBPUSD')])} as unknown as Backend;
  await instruments(api,'dukascopy');await expect(refreshDucascopyCatalog(api)).rejects.toThrow('offline');expect(await instruments(api,'dukascopy')).toEqual(old);await refreshDucascopyCatalog(api,true);expect(api.RefreshDucascopyInstruments).toHaveBeenLastCalledWith(true);expect(cachedInstruments('dukascopy')).toHaveLength(2);
 });
 it('rejects discovery and refresh responses from a previous account without restoring its choices',async()=>{
  let finish!:(items:Instrument[])=>void;const api={ListInstruments:vi.fn(()=>new Promise<Instrument[]>(resolve=>{finish=resolve;})),RefreshDucascopyInstruments:vi.fn(()=>new Promise<Instrument[]>(resolve=>{finish=resolve;}))} as unknown as Backend;
  const old=instruments(api,'dukascopy');clearInstrumentCatalog('dukascopy');finish([item('EURUSD')]);await expect(old).rejects.toThrow('settings changed');expect(cachedInstruments('dukascopy')).toBeUndefined();
  const pending=refreshDucascopyCatalog(api);clearInstrumentCatalog('dukascopy');finish([item('GBPUSD')]);await expect(pending).rejects.toThrow('account changed');expect(cachedInstruments('dukascopy')).toBeUndefined();
 });
 it('retains partial public catalogs for rendering while allowing discovery retry',async()=>{
  const partial=[{...item('BTCUSD'),providerMetadata:{catalogWarning:'One market unavailable'}}];const api={ListInstruments:vi.fn(async()=>partial)} as unknown as Backend;
  await instruments(api,'binance');expect(cachedInstruments('binance')).toEqual(partial);await instruments(api,'binance');expect(api.ListInstruments).toHaveBeenCalledTimes(2);
 });
});

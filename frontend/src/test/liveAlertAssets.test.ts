import {expect,it} from 'vitest';
import type {Instrument} from '../types';
import {liveAlertAssetKey,liveAlertAssetSelection,withLiveAlertAssets,publicLiveAlertAssetLabel,publicLiveAlertAssets} from '../workspace/liveAlertAssets';
import type {LiveAlertAssetRef,LiveAlertDefinition} from '../workspace/screenerLiveAlerts';
const item=(nativeId:string,market:Instrument['market']='spot',availability='available'):Instrument=>({nativeId,market,availability,baseAsset:'BTC',quoteAsset:'USDT',priceIncrement:'0.1',sizeIncrement:'0.001',supportedIntervals:['1h']});
it('matches the public monitoring filter and preserves distinct native market identities',()=>{
 const values=[item('BTCUSDT','spot','Trading'),item('BTCUSDT','perpetual','TRADING'),item('BTCUSDT'),item('@107','spot','online'),item('xyz:XYZ100','perpetual'),item('OFF','spot','disabled'),item('FOREX','forex'),item('PADDED','spot',' trading '),item('NONE','perpetual','')];
 expect(publicLiveAlertAssets(values).map(value=>[value.nativeId,value.market])).toEqual([['BTCUSDT','spot'],['BTCUSDT','perpetual'],['@107','spot'],['xyz:XYZ100','perpetual']]);expect(values).toHaveLength(9);
});
it('uses provider display symbols without rewriting native IDs',()=>{const value={...item('@107'),providerMetadata:{displaySymbol:'HYPE/USDC'}};expect(publicLiveAlertAssetLabel(value)).toBe('HYPE/USDC');expect(value.nativeId).toBe('@107');expect(publicLiveAlertAssetLabel(item('xyz:XYZ100','perpetual'))).toBe('xyz:XYZ100');});
it('keeps the same bounded public universe as the backend',()=>{expect(()=>publicLiveAlertAssets(Array.from({length:20001},(_,index)=>item(`COIN${index}`)))).toThrow('20,000-market bound');});
it('stores independent source choices and restores the dynamic all-assets default',()=>{
 const entry:LiveAlertDefinition={id:'a'.repeat(32),name:'Rule',conditionId:'b'.repeat(32),period:'d1',dataSource:'hyperliquid',enabled:true};
 expect(liveAlertAssetSelection(entry)).toBeUndefined();
 const selected:LiveAlertAssetRef[]=[{market:'spot',symbol:'@107'},{market:'perpetual',symbol:'xyz:RELIANCE'}];
 const custom=withLiveAlertAssets(entry,selected);selected[0].symbol='changed';
 expect(liveAlertAssetSelection(custom)?.[0].symbol).toBe('@107');expect(entry.assetSelections).toBeUndefined();
 const bybit={...custom,dataSource:'bybit' as const};expect(liveAlertAssetSelection(bybit)).toBeUndefined();
 const second=withLiveAlertAssets(bybit,[{market:'spot',symbol:'BTCUSDT'}]);
 const all=withLiveAlertAssets({...second,dataSource:'hyperliquid'},undefined);
 expect(all.assetSelections).toEqual([{dataSource:'bybit',assets:[{market:'spot',symbol:'BTCUSDT'}]}]);
 expect(withLiveAlertAssets({...all,dataSource:'bybit'},undefined).assetSelections).toBeUndefined();
 expect(liveAlertAssetKey({market:'spot',symbol:'BTCUSDT'})).not.toBe(liveAlertAssetKey({market:'perpetual',symbol:'BTCUSDT'}));
 expect(liveAlertAssetKey({market:'perpetual',symbol:'xyz:RELIANCE'})).not.toBe(liveAlertAssetKey({market:'perpetual',symbol:'xyz:Reliance'}));
});

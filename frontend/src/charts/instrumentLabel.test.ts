import {describe,it,expect} from 'vitest';
import type {Instrument} from '../types';
import {instrumentLabel} from './instrumentLabel';
const spot:Instrument={nativeId:'@107',market:'spot',baseAsset:'HYPE',quoteAsset:'USDC',priceIncrement:'0.001',sizeIncrement:'0.01',availability:'available',supportedIntervals:['1h']};
describe('instrument display preserves native selections',()=>{
 it('resolves Hyperliquid spot pair IDs to readable asset pairs without mutating IDs',()=>{expect(instrumentLabel('hyperliquid',spot)).toBe('HYPE/USDC');expect(spot.nativeId).toBe('@107');expect(instrumentLabel('hyperliquid',{...spot,providerMetadata:{displaySymbol:'Unit Bitcoin/USDC'}})).toBe('Unit Bitcoin/USDC');});
 it('keeps builder DEX prefixes and other provider-native names',()=>{expect(instrumentLabel('hyperliquid',{...spot,market:'perpetual',nativeId:'xyz:XYZ100'})).toBe('xyz:XYZ100');expect(instrumentLabel('binance',{...spot,nativeId:'BTCUSDT'})).toBe('BTCUSDT');expect(instrumentLabel('dukascopy',{...spot,market:'forex',nativeId:'EURUSD',providerMetadata:{sdkSymbol:'EUR/USD'}})).toBe('EUR/USD');});
});

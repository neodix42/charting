import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {describe,expect,it} from 'vitest';
import {HyperliquidMarginOverlay,hyperliquidMarginTerms} from '../charts/HyperliquidMarginOverlay';
import type {FeedKey,Instrument} from '../types';

const key:FeedKey={exchange:'hyperliquid',market:'perpetual',instrument:'BTC',timeframe:'1d'};
const asset=(maxLeverage='40'):Instrument=>({nativeId:'BTC',market:'perpetual',baseAsset:'BTC',quoteAsset:'USDC',priceIncrement:'0.1',sizeIncrement:'0.00001',availability:'available',supportedIntervals:['1d'],providerMetadata:{maxLeverage}});
const render=(feedKey=key,instrument:Instrument|undefined=asset(),loading=false)=>renderToStaticMarkup(createElement(HyperliquidMarginOverlay,{feedKey,instrument,loading}));

describe('public Hyperliquid chart margin terms',()=>{
 it('uses the selected provider maximum and computes initial margin as one over leverage',()=>{
  expect(hyperliquidMarginTerms(key,asset())).toEqual({maximum:40,minimumInitialPercent:2.5,spot:false});
  expect(hyperliquidMarginTerms(key,asset('5'))).toEqual({maximum:5,minimumInitialPercent:20,spot:false});
  expect(render()).toContain('Asset max leverage <b>1:40</b>');
  expect(render()).toContain('Minimum initial margin <b>2.5%</b> of position value');
  expect(render()).toContain('Selected leverage and position-size tiers can require more');
 });
 it('does not impose an independent app leverage ceiling or round tiny requirements to zero',()=>{
  expect(hyperliquidMarginTerms(key,asset('1500'))).toEqual({maximum:1500,minimumInitialPercent:100/1500,spot:false});
  expect(render(key,asset('1500'))).toContain('1:1500');
  expect(render(key,asset(String(Number.MAX_SAFE_INTEGER)))).not.toContain('<b>0%</b>');
 });
 it('keeps each exact native builder market separate and never reuses old asset metadata',()=>{
  const builder={...key,instrument:'xyz:XYZ100'};
  expect(hyperliquidMarginTerms(builder,asset())).toBeNull();
  const metadata={...asset('5'),nativeId:'xyz:XYZ100',baseAsset:'XYZ100'};
  expect(hyperliquidMarginTerms(builder,metadata)?.maximum).toBe(5);
  expect(hyperliquidMarginTerms({...builder,instrument:'xyz:xyz100'},metadata)).toBeNull();
  expect(hyperliquidMarginTerms({...builder,market:'spot'},metadata)).toBeNull();
 });
 it('shows full spot funding rather than inferring leverage from unrelated metadata',()=>{
  const spot={...key,market:'spot' as const,instrument:'@107'},metadata={...asset('999'),market:'spot' as const,nativeId:'@107',baseAsset:'HYPE'};
  expect(hyperliquidMarginTerms(spot,metadata)).toEqual({maximum:1,minimumInitialPercent:100,spot:true});
  const output=render(spot,metadata);
  expect(output).toContain('Spot leverage <b>1:1</b>');
  expect(output).toContain('Funding requirement <b>100%</b> of trade value');
  expect(output).toContain('Fully funded spot; no borrowing');
 });
 it('rejects invalid, unsafe, missing and inactive perpetual metadata without inventing a default',()=>{
  for(const value of ['', '0','-1','1.5','NaN','Infinity','1e3','01','40 ','9007199254740992'])expect(hyperliquidMarginTerms(key,asset(value))).toBeNull();
  for(const availability of ['delisted','disabled','unavailable',''])expect(hyperliquidMarginTerms(key,{...asset(),availability})).toBeNull();
  expect(hyperliquidMarginTerms(key,undefined)).toBeNull();
  expect(hyperliquidMarginTerms(key,{...asset(),providerMetadata:{}})).toBeNull();
  const output=render(key,asset('0'));
  expect(output).toContain('Asset max leverage <b>—</b>');
  expect(output).toContain('Minimum initial margin <b>—</b>');
  expect(output).toContain('Asset margin terms unavailable');
  expect(output).not.toContain('2.5%');
 });
 it('keeps loading and unsupported-provider states explicit without account requests or listeners',()=>{
  expect(renderToStaticMarkup(createElement(HyperliquidMarginOverlay,{feedKey:key,loading:true}))).toContain('Loading asset margin terms');
  expect(render({...key,exchange:'binance'})).toBe('');
  expect(hyperliquidMarginTerms({...key,exchange:'binance'},asset())).toBeNull();
 });
});

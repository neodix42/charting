import {describe,expect,it,vi} from 'vitest';
import {comparisonColor,comparisonDate,comparisonExchange,comparisonFeedIdentity,comparisonIdentity,comparisonOptionName,comparisonSelectionAsset,comparisonSelectionLabel,comparisonSeriesLabel,comparisonSource,comparisonLineData,comparisonPercent,comparisonPrice,ComparisonViewWriter,copyComparisonView,defaultComparisonView,type ComparisonView} from '../workspace/comparison';

describe('Comparison view and chart values',()=>{
 it('defaults to four weeks and exactly the requested seven assets with independent copies',()=>{
  const value=defaultComparisonView(),copy=copyComparisonView(value);copy.assets.pop();
  expect(value).toEqual({weeks:4,assets:['BTC','ETH','TON','HYPE','ZEN','APT','NEAR']});
  expect(defaultComparisonView().assets).toHaveLength(7);
  expect(new Set(value.assets.map(comparisonColor)).size).toBe(7);
  expect(comparisonColor('BTC')).toBe(comparisonColor('BTC'));
 });
 it('keeps source identities and labels distinct for the same canonical asset',()=>{
  const auto={symbol:'TON',displaySymbol:'TON (GRAM)',name:'Toncoin',exchange:'hyperliquid',market:'perpetual',nativeId:'GRAM',quoteAsset:'USDC'};
  const bybit={...auto,id:'bybit|spot|GRAMUSDT',exchange:'bybit',market:'spot',nativeId:'GRAMUSDT',quoteAsset:'USDT'};
  const coinbase={...auto,id:'coinbase|spot|TON-USD',displaySymbol:'TON',exchange:'coinbase',market:'spot',nativeId:'TON-USD',quoteAsset:'USD'};
  expect(comparisonIdentity(auto)).toBe('TON');expect(comparisonIdentity(bybit)).toBe('bybit|spot|GRAMUSDT');
  expect(comparisonSource(bybit)).toBe('Bybit · Spot · USDT');expect(comparisonSource(coinbase)).toBe('Coinbase · Spot · USD');
  expect(comparisonExchange('binance')).toBe('Binance');expect(comparisonExchange('hyperliquid')).toBe('Hyperliquid');
  expect(comparisonOptionName(bybit)).toBe('TON (GRAM) Bybit Spot USDT');
  expect(comparisonSeriesLabel(auto,[auto])).toBe('TON (GRAM)');
  expect(comparisonSeriesLabel(bybit,[auto,bybit,coinbase])).toBe('TON (GRAM) · Bybit · Spot · USDT');
  expect(new Set([auto,bybit,coinbase].map(asset=>comparisonColor(comparisonIdentity(asset)))).size).toBe(3);
  const copy=copyComparisonView({weeks:26,assets:['TON',bybit.id,coinbase.id]});expect(copy.assets).toEqual(['TON','bybit|spot|GRAMUSDT','coinbase|spot|TON-USD']);
 });
 it('recognizes the same native feed through automatic and explicit selection IDs',()=>{
  const auto={symbol:'TON',exchange:'hyperliquid',market:'perpetual',nativeId:'GRAM',quoteAsset:'USDC',name:'Gram'};
  const explicit={...auto,id:'hyperliquid|perpetual|GRAM'};
  expect(comparisonFeedIdentity(auto)).toBe(comparisonFeedIdentity(explicit));
  expect(comparisonFeedIdentity({...explicit,exchange:'bybit'})).not.toBe(comparisonFeedIdentity(auto));
  expect(comparisonFeedIdentity({...explicit,market:'spot'})).not.toBe(comparisonFeedIdentity(auto));
  expect(comparisonFeedIdentity({...explicit,nativeId:'TON'})).not.toBe(comparisonFeedIdentity(auto));
  expect(comparisonFeedIdentity({...explicit,feedKey:{exchange:'coinbase',market:'spot',instrument:'TON-USD',timeframe:'1d'}})).toBe(JSON.stringify(['coinbase','spot','TON-USD']));
  expect(comparisonFeedIdentity({...auto,exchange:'',nativeId:''})).toBeUndefined();
 });
 it('displays saved market IDs safely without inventing quote or feed metadata',()=>{
  const saved=comparisonSelectionAsset('hyperliquid|perpetual|xyz%3AaBc');
  expect(saved).toMatchObject({symbol:'xyz:aBc',nativeId:'xyz:aBc',exchange:'hyperliquid',market:'perpetual',quoteAsset:''});
  expect(saved?.feedKey).toBeUndefined();expect(comparisonSource(saved!)).toBe('Hyperliquid · Perpetual');
  expect(comparisonSelectionLabel('coinbase|spot|TON-USD')).toBe('TON-USD');
  expect(comparisonSelectionLabel('bybit|spot|%E6%B5%8BUSDT')).toBe('测USDT');
  expect(comparisonSelectionAsset('coinbase|spot|%XX')).toBeUndefined();
  expect(comparisonSelectionLabel('coinbase|spot|%XX')).toBe('Selected market');
  expect(comparisonSelectionLabel('BTC')).toBe('BTC');
 });
 it('formats UTC baseline dates and genuine missing/zero/signed percentage values',()=>{
  expect(comparisonDate(Date.UTC(2026,10,24,23,59)/1000)).toBe('24 Nov 2026');
  expect(comparisonPercent(null)).toBe('—');expect(comparisonPercent(undefined)).toBe('—');
  expect(comparisonPercent(0)).toBe('0.00%');expect(comparisonPercent(-.001)).toBe('0.00%');
  expect(comparisonPercent(12.345)).toBe('+12.35%');expect(comparisonPercent(-7.5)).toBe('-7.50%');
 });
 it('formats actual daily closes with their own quote and keeps tiny prices distinct from zero',()=>{
  expect(comparisonPrice(1280,'USDC')).toBe('1,280 USDC');
  expect(comparisonPrice(42.125,'USD')).toBe('42.125 USD');
  expect(comparisonPrice(.000000000031,'USDT')).toBe('0.000000000031 USDT');
  expect(comparisonPrice(1234.5678901234567,'USDC')).toBe('1,234.5678901234567 USDC');
  for(const value of [null,undefined,0,-1,NaN,Infinity,-Infinity])expect(comparisonPrice(value,'USDT')).toBe('—');
 });
 it('breaks outgoing lines across missing closes without inventing any intermediate values',()=>{
  const points=[{time:0,close:100,pct:0},{time:86400,close:null,pct:null},{time:172800,close:110,pct:10},{time:259200,close:90,pct:-10}];
  expect(comparisonLineData(points,'#123456')).toEqual([{time:0,value:0,color:'rgba(0, 0, 0, 0)'},{time:86400},{time:172800,value:10,color:'#123456'},{time:259200,value:-10,color:'#123456'}]);
  expect(points[1]).toEqual({time:86400,close:null,pct:null});
  expect(comparisonLineData([points[0],points[2]],'#123456')[0]).toMatchObject({color:'rgba(0, 0, 0, 0)'});
 });
});

describe('Comparison ordered view persistence',()=>{
 it('coalesces edits while a save is pending and preserves an explicitly empty selection',async()=>{
  let release!:()=>void;const first=new Promise<void>(resolve=>{release=resolve;}),saved:ComparisonView[]=[];
  const persist=vi.fn(async(view:ComparisonView)=>{saved.push(copyComparisonView(view));if(saved.length===1)await first;return view;});
  const writer=new ComparisonViewWriter(persist,()=>{});
  writer.edit(defaultComparisonView());writer.edit({weeks:8,assets:['BTC']});writer.edit({weeks:12,assets:[]});
  release();await writer.flush();
  expect(saved).toEqual([defaultComparisonView(),{weeks:12,assets:[]}]);
 });
 it('retains the newest edit after a failed save and makes a later flush await its retry',async()=>{
  let reject!:(error:Error)=>void;const first=new Promise<ComparisonView>((_resolve,fail)=>{reject=fail;}),error=vi.fn();
  const persist=vi.fn<(view:ComparisonView)=>Promise<ComparisonView>>().mockImplementationOnce(()=>first).mockImplementation(async view=>view);
  const writer=new ComparisonViewWriter(persist,error);
  writer.edit(defaultComparisonView());writer.edit({weeks:2,assets:['TON','HYPE']});
  reject(new Error('Disk is full'));await Promise.resolve();await Promise.resolve();
  await writer.flush();
  expect(persist).toHaveBeenLastCalledWith({weeks:2,assets:['TON','HYPE']});
 });
});

import {describe,it,expect} from 'vitest';
import {ChartTradeCache,chartTradeKeys,chartTradeLevels} from '../workspace/chartTrades';
import type {FeedKey} from '../types';
import type {TradeRecord} from '../workspace/tradeHistory';
import {TradeLevelLayer} from '../charts/TradeLevelLayer';
import type {ISeriesApi,SeriesType} from 'lightweight-charts';
import {getThemePalette} from '../workspace/themes';
const key:FeedKey={exchange:'hyperliquid',market:'perpetual',instrument:'BTC',timeframe:'1h'};
const row:TradeRecord={id:'hyperliquid:'+'a'.repeat(32),provider:'hyperliquid',market:'perpetual',instrument:'BTC',account:'account',environment:'testnet',orderId:'101',submittedAt:1,openDate:2,closeDate:0,size:'1',remainingSize:'1',direction:'long',entryPrice:'61000',stopLoss:'60700',takeProfit:'61100',slAmount:'',tpAmount:'',currency:'USDC',balanceAtOpen:'',balanceCurrency:'',balanceTime:0,marginUsed:'',marginCurrency:'',status:'open',canClose:true,closeReason:'Verified',checkedAt:3};
describe('open platform trade levels',()=>{
 it('shares refreshes across timeframes and excludes unsupported providers and spot executions',()=>{
  expect(chartTradeKeys([key,{...key,timeframe:'1d'},{...key,market:'spot',instrument:'@107'},{...key,exchange:'binance',market:'spot',instrument:'BTCUSDT'}])).toEqual([key]);
 });
 it('uses actual open execution prices and exact source/market/native identity with optional protection',()=>{
  expect(chartTradeLevels([row],key).map(level=>[level.kind,level.price,level.title])).toEqual([['entry',61000,'Long entry #101'],['sl',60700,'SL #101'],['tp',61100,'TP #101']]);
  expect(chartTradeLevels([{...row,direction:'short',stopLoss:'',takeProfit:''}],key).map(level=>level.title)).toEqual(['Short entry #101']);
  for(const patch of [{status:'unconfirmed',openDate:0},{status:'failed'},{openDate:0,submittedAt:0},{entryPrice:'NaN'},{instrument:'xyz:BTC'},{provider:'dukascopy' as const},{market:'spot'}])expect(chartTradeLevels([{...row,...patch}],key)).toEqual([]);
  expect(chartTradeLevels([{...row,provider:'dukascopy',market:'jforex',instrument:'USA500IDXUSD'}],{...key,exchange:'dukascopy',market:'cfd',instrument:'USA500IDXUSD'})).toHaveLength(3);
 });
 it('keeps only the latest actual opening including closed trades and retains it through empty/error/older updates',()=>{
  const cache=new ChartTradeCache(),newer={...row,id:'hyperliquid:'+'b'.repeat(32),orderId:'102',openDate:4,submittedAt:3};
  cache.merge([row], [key]);expect(cache.rows([key])[0].orderId).toBe('101');
  cache.merge([], [key]);expect(chartTradeLevels(cache.rows([key]),key)).toHaveLength(3);
  cache.merge([{...row,error:'Provider disconnected',status:'closed',closeDate:5,remainingSize:'0',stopLoss:'',takeProfit:''}], [key]);
  expect(chartTradeLevels(cache.rows([key]),key).map(level=>level.price)).toEqual([61000,60700,61100]);
  cache.merge([newer,{...newer,id:'pending',status:'unconfirmed',openDate:9}], [key]);
  cache.merge([row], [key]);expect(chartTradeLevels(cache.rows([key]),key).map(level=>level.orderId)).toEqual(['102','102','102']);
  expect(chartTradeLevels([row,{...newer,status:'closed',closeDate:5,remainingSize:'0'}],key).every(level=>level.tradeId===newer.id)).toBe(true);
  cache.retain([]);expect(cache.rows([key])).toEqual([]);
  cache.merge([row],[key]);cache.clear();expect(cache.rows([key])).toEqual([]);
 });
 it('updates native price lines without duplication, removes missing protection and releases the old series',()=>{
  const fake=()=>{const created:any[]=[],removed:any[]=[];return{created,removed,series:{createPriceLine:(options:any)=>{const line={options,updates:0,applyOptions(next:any){line.options=next;line.updates++;}};created.push(line);return line;},removePriceLine:(line:any)=>removed.push(line)} as unknown as ISeriesApi<SeriesType>};};
  const first=fake(),second=fake(),layer=new TradeLevelLayer(first.series),levels=chartTradeLevels([row],key);
  layer.update(levels,getThemePalette('light'));layer.update(levels,getThemePalette('light'));expect(first.created).toHaveLength(3);expect(first.created[0].updates).toBe(0);
  layer.update(chartTradeLevels([{...row,stopLoss:'60800',takeProfit:''}],key),getThemePalette('dark'));expect(first.created).toHaveLength(3);expect(first.removed).toHaveLength(1);expect(first.created[1].options.price).toBe(60800);expect(first.created[1].updates).toBe(1);
  layer.setSeries(second.series);expect(first.removed).toHaveLength(3);layer.update(levels,getThemePalette('dark'));expect(second.created).toHaveLength(3);layer.update([],getThemePalette('dark'));expect(second.removed).toHaveLength(3);layer.dispose();expect(second.removed).toHaveLength(3);
 });
});

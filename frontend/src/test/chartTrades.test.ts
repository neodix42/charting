import {describe,it,expect} from 'vitest';
import {chartTradeKeys,chartTradeLevels} from '../workspace/chartTrades';
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
  for(const patch of [{status:'closed',closeDate:4},{status:'unconfirmed',openDate:0},{remainingSize:'0'},{entryPrice:'NaN'},{error:'Exchange status unavailable'},{instrument:'xyz:BTC'},{provider:'dukascopy' as const},{market:'spot'}])expect(chartTradeLevels([{...row,...patch}],key)).toEqual([]);
  expect(chartTradeLevels([{...row,provider:'dukascopy',market:'jforex',instrument:'USA500IDXUSD'}],{...key,exchange:'dukascopy',market:'cfd',instrument:'USA500IDXUSD'})).toHaveLength(3);
 });
 it('updates native price lines without duplication, removes closed/missing protection and releases the old series',()=>{
  const fake=()=>{const created:any[]=[],removed:any[]=[];return{created,removed,series:{createPriceLine:(options:any)=>{const line={options,updates:0,applyOptions(next:any){line.options=next;line.updates++;}};created.push(line);return line;},removePriceLine:(line:any)=>removed.push(line)} as unknown as ISeriesApi<SeriesType>};};
  const first=fake(),second=fake(),layer=new TradeLevelLayer(first.series),levels=chartTradeLevels([row],key);
  layer.update(levels,getThemePalette('light'));layer.update(levels,getThemePalette('light'));expect(first.created).toHaveLength(3);expect(first.created[0].updates).toBe(0);
  layer.update(chartTradeLevels([{...row,stopLoss:'60800',takeProfit:''}],key),getThemePalette('dark'));expect(first.created).toHaveLength(3);expect(first.removed).toHaveLength(1);expect(first.created[1].options.price).toBe(60800);expect(first.created[1].updates).toBe(1);
  layer.setSeries(second.series);expect(first.removed).toHaveLength(3);layer.update(levels,getThemePalette('dark'));expect(second.created).toHaveLength(3);layer.update([],getThemePalette('dark'));expect(second.removed).toHaveLength(3);layer.dispose();expect(second.removed).toHaveLength(3);
 });
});

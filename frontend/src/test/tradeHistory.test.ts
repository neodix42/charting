import type {TradeRecord} from '../workspace/tradeHistory';
import {describe,it,expect} from 'vitest';
import {tradeDate,tradeMoney,tradeNumber,tradeProfitLoss} from '../workspace/tradeHistory';

describe('trade history values',()=>{
 it('keeps a real zero balance distinct from an unavailable snapshot and labels account currencies',()=>{
  expect(tradeMoney('0','EUR')).toBe('0.00 EUR');expect(tradeMoney('','USD')).toBe('—');expect(tradeMoney(undefined,'USDC')).toBe('—');
  expect(tradeMoney('-9.27','EUR')).toBe('-9.27 EUR');expect(tradeMoney('17.73','EUR')).toBe('17.73 EUR');expect(tradeMoney('1200','USDC')).toBe('1,200.00 USDC');expect(tradeMoney('10','USDT')).toBe('10.00 USDT');expect(tradeMoney('NaN','USD')).toBe('—');expect(tradeMoney('10','')).toBe('—');
 });
 it('preserves small asset quantities and displays execution dates in the workspace timezone',()=>{
  expect(tradeNumber('0.00000123')).toBe('0.0000012300');expect(tradeNumber('1000')).toBe('1,000');expect(tradeDate(0,'UTC')).toBe('—');
  const date=Date.UTC(2026,10,11,20,30,10);expect(tradeDate(date,'UTC')).toContain('11 Nov 2026');expect(tradeDate(date,'Asia/Dubai')).toContain('12 Nov 2026');expect(tradeDate(date,'Asia/Dubai')).toContain('00:30:10');
 });
});

it('shows current exchange P/L and preserves confirmed final zero/loss values after closure',()=>{
 const now=Date.now(),base={status:'open',closeDate:0,remainingSize:'1',profitLoss:'12.5',profitLossCurrency:'EUR',profitLossState:'open',profitLossTime:now,profitLossBasis:'Broker reported'} as TradeRecord;
 expect(tradeProfitLoss(base,now)).toMatchObject({text:'12.50 EUR',label:'Current',tone:'profit'});
 expect(tradeProfitLoss({...base,profitLoss:'0'},now).text).toBe('0.00 EUR');
 expect(tradeProfitLoss({...base,profitLossTime:now-46000},now).text).toBe('—');
 expect(tradeProfitLoss({...base,error:'Exchange unavailable'},now).text).toBe('—');
 expect(tradeProfitLoss({...base,status:'closed',closeDate:now,remainingSize:'0'},now).text).toBe('—');
 const closed={...base,status:'closed',closeDate:now,remainingSize:'0',profitLoss:'-3.75',profitLossState:'closed'};
 expect(tradeProfitLoss(closed,now+365*86400000)).toMatchObject({text:'-3.75 EUR',label:'Closed',tone:'loss'});
 expect(tradeProfitLoss({...closed,profitLoss:'0'},now+365*86400000).text).toBe('0.00 EUR');
 expect(tradeProfitLoss({...base,profitLossCurrency:''},now).text).toBe('—');
});

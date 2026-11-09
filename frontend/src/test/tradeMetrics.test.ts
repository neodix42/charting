import {expect,it} from 'vitest';
import {metricAmount} from '../charts/TradeMetricsPanel';

it('keeps real zero balances distinct from missing values and does not assume a currency',()=>{
 expect(metricAmount('0','USD')).toBe('0.00 USD');
 expect(metricAmount('0.00','USDC')).toBe('0.00 USDC');
 expect(metricAmount('-12.5','EUR')).toBe('-12.50 EUR');
 expect(metricAmount('0.00000123','BTC')).toBe('0.00000123 BTC');
 expect(metricAmount('0.000000000001','HYPE')).toBe('0.000000000001 HYPE');
 for(const absent of [undefined,'',' ','NaN','Infinity'])expect(metricAmount(absent,'USD')).toBeNull();
 expect(metricAmount('100','')).toBeNull();
 expect(metricAmount('100',undefined)).toBeNull();
});

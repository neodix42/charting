import {describe,it,expect} from 'vitest';
import {tradeDate,tradeMoney,tradeNumber} from '../workspace/tradeHistory';

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

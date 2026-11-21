import {describe,expect,it} from 'vitest';
import {forexRiskSizing} from '../workspace/forexRiskSizing';
const base={equity:1000,usedMargin:0,entry:1.1,stop:1.078,side:'buy' as const,profitPerPriceUnit:1,marginPerUnit:1.1/100,step:100,maxQuantity:100000,marginCutUsePercent:200};
describe('Forex risk sizing',()=>{
 it('sizes a 50% equity loss while retaining 150% projected margin level',()=>{
  const result=forexRiskSizing(base);
  expect(result.error).toBe('');
  expect(result.riskQuantity).toBe(22700);
  expect(result.safeQuantity).toBe(22700);
  expect(result.canTargetRisk).toBe(true);
  expect(result.projectedMarginLevel).toBeGreaterThan(150);
 });
 it('reduces quantity when margin buffer is insufficient',()=>{
  const result=forexRiskSizing({...base,marginPerUnit:.03});
  expect(result.safeQuantity).toBeLessThan(result.riskQuantity);
  expect(result.canTargetRisk).toBe(false);
  expect(result.projectedMarginLevel).toBeGreaterThanOrEqual(150);
 });
 it('caps the order by executable liquidity',()=>{
  const result=forexRiskSizing({...base,maxQuantity:2000});
  expect(result.safeQuantity).toBe(2000);
  expect(result.canTargetRisk).toBe(false);
 });
 it('respects the broker account equity stop-out threshold',()=>{
  const result=forexRiskSizing({...base,accountEquityStopLoss:800});
  expect(result.safeQuantity).toBeLessThan(result.riskQuantity);
  expect(result.projectedEquity).toBeGreaterThan(800);
 });
 it('rejects invalid stop direction and unknown margin-cut limits',()=>{
  expect(forexRiskSizing({...base,stop:1.2}).error).toMatch(/Stop/);
  expect(forexRiskSizing({...base,marginCutUsePercent:0}).error).toMatch(/margin-cut/);
 });
});

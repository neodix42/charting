import type {TradeRecord} from '../workspace/tradeHistory';
import {describe,it,expect} from 'vitest';
import {tradeDate,tradeMoney,tradeNumber,tradeProfitLoss,tradePipsProfitLoss,tradeVerification,tradeProtectionDisplay} from '../workspace/tradeHistory';

describe('trade history values',()=>{
 it('keeps a real zero balance distinct from an unavailable snapshot and labels account currencies',()=>{
  expect(tradeMoney('0','EUR')).toBe('0.0000 EUR');expect(tradeMoney('','USD')).toBe('—');expect(tradeMoney(undefined,'USDC')).toBe('—');
  expect(tradeMoney('-9.27','EUR')).toBe('-9.2700 EUR');expect(tradeMoney('17.73','EUR')).toBe('17.7300 EUR');expect(tradeMoney('1200','USDC')).toBe('1,200.00 USDC');expect(tradeMoney('10','USDT')).toBe('10.00 USDT');expect(tradeMoney('NaN','USD')).toBe('—');expect(tradeMoney('10','')).toBe('—');
 });
 it('preserves small asset quantities and displays execution dates in the workspace timezone',()=>{
  expect(tradeNumber('0.00000123')).toBe('0.0000012300');expect(tradeNumber('1000')).toBe('1,000');expect(tradeDate(0,'UTC')).toBe('—');
  const date=Date.UTC(2026,10,11,20,30,10);expect(tradeDate(date,'UTC')).toContain('11 Nov 2026');expect(tradeDate(date,'Asia/Dubai')).toContain('12 Nov 2026');expect(tradeDate(date,'Asia/Dubai')).toContain('00:30:10');
 });
});

describe('trade history verification and protection provenance',()=>{
 const now=Date.UTC(2026,10,26,6),checkedAt=Date.UTC(2026,10,1,12);
 const base={status:'open',closeDate:0,checkedAt,stopLossSource:'exchange',takeProfitSource:'exchange',stopLoss:'1.09',takeProfit:'1.12'} as TradeRecord;
 it('retains prices but distinguishes failed, old and fresh successful checks using UTC',()=>{
  const stale=tradeVerification(base,now);
  expect(stale).toMatchObject({unverified:true,label:'Last verified'});expect(stale.title).toContain('2026-11-01 12:00:00 UTC');
  expect(tradeProtectionDisplay(base,'SL',stale)).toMatchObject({note:'Last verified'});
  const failed=tradeVerification({...base,error:'JForex order does not match the local platform submission'},now);
  expect(failed).toMatchObject({unverified:true,label:'Unverified'});expect(tradeProtectionDisplay(base,'TP',failed).title).toContain('does not match');
  expect(base.stopLoss).toBe('1.09');expect(base.takeProfit).toBe('1.12');
  expect(tradeVerification({...base,checkedAt:now},now)).toMatchObject({unverified:false,label:''});
  expect(tradeVerification({...base,checkedAt:now},now,'Refresh transport failed')).toMatchObject({unverified:true,label:'Unverified'});
 });
 it('keeps submitted and closed levels distinct from live exchange verification',()=>{
  const submitted={...base,status:'submitted',checkedAt:0,stopLossSource:'submitted'};
  expect(tradeProtectionDisplay(submitted,'SL',tradeVerification(submitted,now))).toMatchObject({note:'At submission · Unverified'});
  const closed={...base,status:'closed',closeDate:checkedAt+1000,stopLossSource:'submitted',takeProfitSource:'recorded',error:'Exchange unavailable'};
  expect(tradeVerification(closed,now).unverified).toBe(false);
  expect(tradeProtectionDisplay(closed,'SL')).toMatchObject({note:'At submission'});
  expect(tradeProtectionDisplay(closed,'TP')).toMatchObject({note:'Last recorded'});
 });
 it('expires live verification after 30 seconds and rejects missing or invalid check timestamps',()=>{
  expect(tradeVerification({...base,checkedAt:now-30000},now).unverified).toBe(false);
  expect(tradeVerification({...base,checkedAt:now-30001},now)).toMatchObject({unverified:true,label:'Last verified'});
  for(const time of [0,-1,NaN,Infinity,now+5001,8640000000000001])expect(tradeVerification({...base,checkedAt:time},now)).toMatchObject({unverified:true,label:'Unverified',title:expect.stringContaining('No successful exchange check recorded')});
 });
});

it('shows current exchange P/L and preserves confirmed final zero/loss values after closure',()=>{
 const now=Date.now(),base={status:'open',closeDate:0,remainingSize:'1',profitLoss:'12.5',profitLossCurrency:'EUR',profitLossState:'open',profitLossTime:now,profitLossBasis:'Broker reported'} as TradeRecord;
 expect(tradeProfitLoss(base,now)).toMatchObject({text:'12.5000 EUR',label:'Current',tone:'profit'});
 expect(tradeProfitLoss({...base,profitLoss:'0'},now).text).toBe('0.0000 EUR');
 expect(tradeProfitLoss({...base,profitLossTime:now-46000},now).text).toBe('—');
 expect(tradeProfitLoss({...base,error:'Exchange unavailable'},now).text).toBe('—');
 expect(tradeProfitLoss({...base,status:'closed',closeDate:now,remainingSize:'0'},now).text).toBe('—');
 const closed={...base,status:'closed',closeDate:now,remainingSize:'0',profitLoss:'-3.75',profitLossState:'closed'};
 expect(tradeProfitLoss(closed,now+365*86400000)).toMatchObject({text:'-3.7500 EUR',label:'Closed',tone:'loss'});
 expect(tradeProfitLoss({...closed,profitLoss:'0'},now+365*86400000).text).toBe('0.0000 EUR');
 expect(tradeProfitLoss({...base,profitLossCurrency:''},now).text).toBe('—');
});


it('labels native pips and Hyperliquid ticks, preserves zero and expires only open values',()=>{
 const now=Date.now(),base={status:'open',closeDate:0,remainingSize:'1',profitLossPips:'125.5',profitLossPipsUnit:'pips',profitLossPipsState:'open',profitLossPipsTime:now} as TradeRecord;
 expect(tradePipsProfitLoss(base,now)).toMatchObject({text:'125.5 pips',label:'Current',tone:'profit'});
 expect(tradePipsProfitLoss({...base,profitLossPips:'0'},now).text).toBe('0 pips');
 expect(tradePipsProfitLoss({...base,profitLossPips:'-100',profitLossPipsUnit:'ticks'},now)).toMatchObject({text:'-100 ticks',tone:'loss'});
 expect(tradePipsProfitLoss({...base,profitLossPipsUnit:undefined},now).text).toBe('—');
 expect(tradePipsProfitLoss({...base,profitLossPips:undefined},now).text).toBe('—');
 expect(tradePipsProfitLoss({...base,profitLossPipsTime:now-46000},now).text).toBe('—');
 expect(tradePipsProfitLoss({...base,profitLossPipsTime:now+6000},now).text).toBe('—');
 expect(tradePipsProfitLoss({...base,error:'Exchange unavailable'},now).text).toBe('—');
 expect(tradePipsProfitLoss({...base,status:'closed',closeDate:now,remainingSize:'0'},now).text).toBe('—');
 const closed={...base,status:'closed',closeDate:now,remainingSize:'0',profitLossPips:'-37.5',profitLossPipsState:'closed'};
 expect(tradePipsProfitLoss(closed,now+365*86400000)).toMatchObject({text:'-37.5 pips',label:'Closed',tone:'loss'});
 expect(tradePipsProfitLoss({...closed,profitLossPips:'0'},now+365*86400000).text).toBe('0 pips');
});

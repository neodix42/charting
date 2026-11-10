import {describe,it,expect} from 'vitest';
import {balanceIsStale,normalizedTransferAmount,orderedVenues,transferAmountProblem,transferTokens} from '../workspace/hyperliquidBalances';
import type {HyperliquidBalances,HyperliquidTokenBalance} from '../types';

const token=(id:number,coin:string):HyperliquidTokenBalance=>({token:id,coin,tokenId:`token-${id}`,szDecimals:8,weiDecimals:8,observedAt:1000});
const overview=():HyperliquidBalances=>({accountAddress:'0x'+'1'.repeat(40),network:'mainnet',accountMode:'disabled',shared:false,observedAt:1000,tokens:[token(0,'USDC'),token(1,'USDT'),token(2,'HYPE')],venues:[
 {dex:'xyz',name:'XYZ',market:'perpetual',collateralToken:0,shared:false,balances:[{...token(0,'USDC'),available:'0'}]},
 {dex:'',name:'Main',market:'perpetual',collateralToken:0,shared:false,balances:[{...token(0,'USDC'),available:'100'}]},
 {dex:'cash',name:'Cash',market:'perpetual',collateralToken:1,shared:false,balances:[{...token(1,'USDT'),available:'10'}]},
 {dex:'spot',name:'Spot',market:'spot',shared:false,balances:[{...token(0,'USDC'),available:'1'}, {...token(1,'USDT'),available:'2'}, {...token(2,'HYPE'),available:'3'}]},
 {dex:'para',name:'Para',market:'perpetual',collateralToken:0,shared:false,balances:[{...token(0,'USDC'),available:'0'}]},
]});

describe('Hyperliquid internal-balance choices',()=>{
 it('keeps separate balances and offers only the same collateral token along each supported route',()=>{
  const value=overview(),original=structuredClone(value);
  expect(orderedVenues(value.venues).map(venue=>venue.dex)).toEqual(['','xyz','para','spot','cash']);
  expect(transferTokens(value,'','xyz').map(item=>item.coin)).toEqual(['USDC']);
  expect(transferTokens(value,'','spot').map(item=>item.coin)).toEqual(['USDC']);
  expect(transferTokens(value,'spot','cash').map(item=>item.coin)).toEqual(['USDT']);
  expect(transferTokens(value,'','cash')).toEqual([]);
  expect(transferTokens(value,'spot','spot')).toEqual([]);
  expect(transferTokens(value,'missing','xyz')).toEqual([]);
  expect(value).toEqual(original);
 });
 it('does not offer independent transfers for shared, unavailable or unverified collateral identities',()=>{
  const value=overview();value.shared=true;expect(transferTokens(value,'','xyz')).toEqual([]);value.shared=false;
  value.venues[0].error='Metadata unavailable';expect(transferTokens(value,'','xyz')).toEqual([]);delete value.venues[0].error;
  value.tokens[0].tokenId='';expect(transferTokens(value,'','xyz')).toEqual([]);
 });
 it('compares amount and available balance exactly beyond Number precision and preserves fractional Max values',()=>{
  expect(transferAmountProblem('9007199254740993','9007199254740992',8)).toContain('exceeds');
  expect(transferAmountProblem('0.00000001','0.00000001',8)).toBe('');
  expect(transferAmountProblem('0.00000002','0.00000001',8)).toContain('exceeds');
  expect(transferAmountProblem('1.00000001','1.00000002',8)).toBe('');
  expect(transferAmountProblem('0','0',8)).toContain('positive');
  expect(transferAmountProblem('0.000000001','1',8)).toContain('8 decimal');
  expect(transferAmountProblem('1.000000000','2',8)).toBe('');
  for(const invalid of ['-1','+1','1e1','Infinity','NaN','1.2.3'])expect(transferAmountProblem(invalid,'10',8)).not.toBe('');
  expect(transferAmountProblem('1',undefined,8)).toContain('unavailable');
  expect(normalizedTransferAmount('001.2300')).toBe('1.23');
 });
 it('expires source data and rejects impossible future timestamps while retaining a verified zero timestamp policy',()=>{
  expect(balanceIsStale(1000,31000)).toBe(false);expect(balanceIsStale(1000,31001)).toBe(true);
  expect(balanceIsStale(6001,1000)).toBe(true);expect(balanceIsStale(undefined,1000)).toBe(true);
 });
});

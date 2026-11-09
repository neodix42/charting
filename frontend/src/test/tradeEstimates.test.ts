import {describe,expect,it} from 'vitest';
import {formatTradeLevelEstimate,tradeLevelEstimate} from '../workspace/tradeEstimates';

describe('account-currency level estimates',()=>{
 it('uses signed entry basis points and real conversion for buy losses/profits',()=>{
  expect(tradeLevelEstimate('buy','100','4','95',{accountCurrency:'EUR',profitPerPriceUnit:'0.9'})).toEqual({basisPoints:-500,amount:-18,currency:'EUR'});
  expect(tradeLevelEstimate('buy','100','4','110',{accountCurrency:'EUR',profitPerPriceUnit:'0.9'})).toEqual({basisPoints:1000,amount:36,currency:'EUR'});
 });
 it('reverses sell direction, quantity scales money only and entry changes both values',()=>{
  expect(tradeLevelEstimate('sell','100','10','105',{accountCurrency:'USD',profitPerPriceUnit:'1'})).toEqual({basisPoints:-500,amount:-50,currency:'USD'});
  expect(tradeLevelEstimate('sell','100','20','90',{accountCurrency:'USD',profitPerPriceUnit:'1'})).toEqual({basisPoints:1000,amount:200,currency:'USD'});
  expect(tradeLevelEstimate('sell','120','20','90',{accountCurrency:'USD',profitPerPriceUnit:'1'})).toEqual({basisPoints:2500,amount:600,currency:'USD'});
 });
 it('does not assume USD, a one-to-one conversion or an account denomination',()=>{
  expect(tradeLevelEstimate('buy','100','5','95')).toEqual({basisPoints:-500,amount:null,currency:null});
  expect(tradeLevelEstimate('buy','100','5','95',{accountCurrency:'EUR'}).amount).toBeNull();
  expect(formatTradeLevelEstimate(tradeLevelEstimate('buy','100','5','95'))).toBe('-500.00 bps · Account amount unavailable');
 });
 it('handles blank editing fields, nonfinite values and missing quantity without invented P/L',()=>{
  for(const invalid of ['',' ','NaN','Infinity','-1','0'])expect(tradeLevelEstimate('buy',invalid,'1','1').basisPoints).toBeNull();
  expect(tradeLevelEstimate('buy','100','','95',{accountCurrency:'USD',profitPerPriceUnit:'1'})).toEqual({basisPoints:-500,amount:null,currency:'USD'});
  expect(tradeLevelEstimate('buy','1e-308','1e308','1e308',{accountCurrency:'USD',profitPerPriceUnit:'1e308'})).toEqual({basisPoints:null,amount:null,currency:'USD'});
  expect(formatTradeLevelEstimate({basisPoints:10,amount:1.235,currency:'EUR'})).toBe('+10.00 bps · +1.24 EUR');
 });
 it('retains the provider collateral denomination for linear Hyperliquid amounts',()=>{
  for(const currency of ['USDC','USDT','USDH']){
   const result=tradeLevelEstimate('sell','200','10','210',{accountCurrency:currency,profitPerPriceUnit:'1'});
   expect(result).toEqual({basisPoints:-500,amount:-100,currency});
   expect(formatTradeLevelEstimate(result,'Collateral amount unavailable')).toBe(`-500.00 bps · -100.00 ${currency}`);
  }
 });
 it('discards an explicitly unavailable valuation even if stale numeric fields remain',()=>{
  const result=tradeLevelEstimate('buy','100','4','95',{accountCurrency:'USDC',profitPerPriceUnit:'1',valuationError:'Collateral metadata unavailable'});
  expect(result).toEqual({basisPoints:-500,amount:null,currency:'USDC'});
  expect(formatTradeLevelEstimate(result,'Collateral amount unavailable')).toBe('-500.00 bps · Collateral amount unavailable');
 });
});

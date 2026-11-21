import {describe,expect,it} from 'vitest';
import {currencyDecimalPlaces,formatCurrencyAmount,formatCurrencyValue} from '../workspace/currencyFormat';

describe('currency display precision',()=>{
 it('rounds fiat values to four places and USDT/USDC to two, including prices and zero',()=>{
  expect(formatCurrencyAmount('35.516606','USDC')).toBe('35.52 USDC');
  expect(formatCurrencyAmount('28.2685775','USDT')).toBe('28.27 USDT');
  expect(formatCurrencyValue('79.0351282051','USDC')).toBe('79.04');
  expect(formatCurrencyValue('1.09693','USD')).toBe('1.0969');
  expect(formatCurrencyAmount('0','EUR')).toBe('0.0000 EUR');
  expect(formatCurrencyAmount('0','USDC')).toBe('0.00 USDC');
  expect(formatCurrencyAmount('1234.1','JPY')).toBe('1,234.1000 JPY');
  expect(currencyDecimalPlaces(' usdc ')).toBe(2);
  expect(currencyDecimalPlaces('CNH')).toBe(4);
 });
 it('rounds source decimal strings without losing integer precision or half boundaries',()=>{
  expect(formatCurrencyValue('9007199254740993.12345','USD')).toBe('9,007,199,254,740,993.1235');
  expect(formatCurrencyValue('1.005','USDT')).toBe('1.01');
  expect(formatCurrencyValue('-1.005','USDT')).toBe('-1.01');
  expect(formatCurrencyValue('9.99995','EUR')).toBe('10.0000');
  expect(formatCurrencyValue('5e-5','USD')).toBe('0.0001');
  expect(formatCurrencyValue('1.23455e2','USD')).toBe('123.4550');
  expect(formatCurrencyAmount(1.235,'EUR',{signed:true})).toBe('+1.2350 EUR');
  expect(formatCurrencyAmount(-1.235,'EUR',{signed:true})).toBe('-1.2350 EUR');
 });
 it('keeps native token precision for nonfiat currencies and leaves invalid data unavailable',()=>{
  expect(currencyDecimalPlaces('BTC')).toBeNull();
  expect(currencyDecimalPlaces('XAU')).toBeNull();
  expect(formatCurrencyAmount('0.000000000001','HYPE')).toBe('0.000000000001 HYPE');
  expect(formatCurrencyAmount('0.00000123','BTC')).toBe('0.00000123 BTC');
  expect(formatCurrencyAmount('12.3456789','HYPE')).toBe('12.3456789 HYPE');
  for(const raw of [undefined,'',' ','NaN','Infinity','0x10','.','1e'])expect(formatCurrencyAmount(raw,'USD')).toBeNull();
  expect(formatCurrencyAmount('5','')).toBeNull();
  expect(formatCurrencyAmount('5',undefined)).toBeNull();
 });
});

import {describe,it,expect} from 'vitest';
import type {IPriceScaleApi} from 'lightweight-charts';
import type {ChartConfig} from '../types';
import {capturePriceScale} from './priceScale';

const automatic:ChartConfig['priceScale']={mode:'linear',autoScale:true,manualRange:null};
const manual:ChartConfig['priceScale']={mode:'log',autoScale:false,manualRange:{min:1,max:100}};
const scale=(autoScale:boolean,range:{from:number;to:number}|null)=>({options:()=>({autoScale} as ReturnType<IPriceScaleApi['options']>),getVisibleRange:()=>range});

describe('price-scale workspace capture',()=>{
 it('captures the actual positive manual range and preserves logarithmic mode',()=>{
  expect(capturePriceScale(scale(false,{from:12.25,to:345.75}),manual)).toEqual({mode:'log',autoScale:false,manualRange:{min:12.25,max:345.75}});
 });
 it('clears the previous manual range when the chart returns to autoscale',()=>{
  expect(capturePriceScale(scale(true,null),manual)).toEqual({mode:'log',autoScale:true,manualRange:null});
 });
 it.each([null,{from:0,to:100},{from:-1,to:100},{from:-10,to:-1},{from:1,to:1},{from:2,to:1},{from:NaN,to:100},{from:1,to:NaN},{from:-Infinity,to:100},{from:1,to:Infinity}])('retains the last valid setting when a manual range cannot be persisted: %j',range=>{
  for(const previous of [automatic,manual]){
   const captured=capturePriceScale(scale(false,range),previous);
   expect(captured).toBe(previous);
   expect(captured.autoScale||captured.manualRange!==null).toBe(true);
  }
 });
 it('does not mutate the last saved manual range',()=>{
  const previous=structuredClone(manual);
  capturePriceScale(scale(false,{from:10,to:20}),previous);
  expect(previous).toEqual(manual);
 });
});

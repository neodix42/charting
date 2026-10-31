import {expect,it} from 'vitest';
import type {SavedAnalysisCondition} from '../workspace/screenerAnalysis';
import {analysisRequestIdentity,initialAnalysisView,profitabilityLabel,savedConditionAllowsAsset,savedConditionProfitability} from '../workspace/screenerAnalysis';

const condition=():SavedAnalysisCondition=>({id:'rule',name:'High hit rate',request:{...initialAnalysisView(),profitabilityFilter:85},analysis:{analyzedAt:1,assets:[
 {symbol:'btcusd',timeframe:'h1',fileName:'high.csv',matches:11,takeProfitHits:10,profitablePercent:1000/11},
 {symbol:'eurusd',timeframe:'h1',fileName:'boundary.csv',matches:20,takeProfitHits:17,profitablePercent:85},
 {symbol:'eurusd',timeframe:'d1',fileName:'poor.csv',matches:20,takeProfitHits:16,profitablePercent:80},
 {symbol:'ethusd',timeframe:'d1',fileName:'perfect.csv',matches:5,takeProfitHits:5,profitablePercent:100},
]}});
it('uses recorded counts and the assigned period with inclusive minimum thresholds',()=>{
 const value=condition();
 expect(savedConditionAllowsAsset(value,'btcusd','h1')).toBe(true);
 expect(savedConditionAllowsAsset(value,'btcusd','d1')).toBe(false);
 expect(savedConditionAllowsAsset(value,'eurusd','h1')).toBe(true);
 expect(savedConditionAllowsAsset(value,'eurusd','d1')).toBe(false);
 value.request.profitabilityFilter=100;
 expect(savedConditionAllowsAsset(value,'ethusd','d1')).toBe(true);
 expect(savedConditionAllowsAsset(value,'btcusd','h1')).toBe(false);
 value.request.profitabilityFilter=0;
 expect(savedConditionAllowsAsset(value,'unknown','h1')).toBe(false);
 expect(savedConditionAllowsAsset(value,'btcusd','h1')).toBe(true);
 expect(profitabilityLabel(savedConditionProfitability(value,'btcusd','h1')!)).toBe('Profitability: 90.91%, 10 / 11 hit TP');
});
it('requires evidence for filtered rules and deterministically picks the best saved file',()=>{
 const value=condition();delete value.analysis;
 expect(savedConditionAllowsAsset(value,'btcusd','h1')).toBe(false);
 value.analysis={analyzedAt:1,assets:[{symbol:'btcusd',timeframe:'h1',fileName:'z.csv',matches:10,takeProfitHits:9,profitablePercent:90},{symbol:'btcusd',timeframe:'h1',fileName:'A.csv',matches:20,takeProfitHits:18,profitablePercent:90}]};
 expect(savedConditionProfitability(value,'btcusd','h1')!.fileName).toBe('A.csv');
});
it('keeps matching evidence without simulation eligible only for an unrestricted recorded scope',()=>{
 const value=condition();value.analysis={analyzedAt:1,failedFiles:1,assets:[{symbol:'btcusd',timeframe:'h1',fileName:'matches.csv',matches:10,takeProfitHits:0,profitablePercent:0,profitabilityUnavailable:true}]};
 expect(savedConditionAllowsAsset(value,'btcusd','h1')).toBe(false);value.request.profitabilityFilter=0;expect(savedConditionAllowsAsset(value,'btcusd','h1')).toBe(true);expect(savedConditionProfitability(value,'btcusd','h1')).toBeUndefined();delete value.analysis;expect(savedConditionAllowsAsset(value,'btcusd','h1')).toBe(false);
});
it('compares working drafts with normalized Go JSON without false modified markers',()=>{
 const draft=initialAnalysisView();draft.profitabilityFilter=0;
 const normalized=structuredClone(draft);delete normalized.profitabilityFilter;normalized.period='';normalized.categories=null;
 normalized.conditions[0].right={kind:'number',value:0,field:'',timeframe:'',offset:0,aggregate:'',window:0};
 expect(analysisRequestIdentity(normalized)).toBe(analysisRequestIdentity(draft));
 normalized.conditions[0].right.value=1;
 expect(analysisRequestIdentity(normalized)).not.toBe(analysisRequestIdentity(draft));
 expect(initialAnalysisView().profitabilityFilter).toBe(85);
});

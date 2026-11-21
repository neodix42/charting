export interface ForexRiskInput {
 equity:number; usedMargin:number; entry:number; stop:number; side:'buy'|'sell';
 profitPerPriceUnit:number; marginPerUnit:number; step:number; maxQuantity:number;
 marginCutUsePercent?:number; accountEquityStopLoss?:number; targetLevel?:number;
}
export interface ForexRiskResult {
 riskQuantity:number; safeQuantity:number; riskAmount:number;
 projectedEquity:number; projectedUsedMargin:number; projectedMarginLevel:number;
 cutLevel:number|null; canTargetRisk:boolean; error:string;
}
/** Advisory only: the broker may change margin tiers, fills or quotes. */
export function forexRiskSizing(v:ForexRiskInput):ForexRiskResult {
 const empty=(error:string):ForexRiskResult=>({riskQuantity:0,safeQuantity:0,riskAmount:0,projectedEquity:0,projectedUsedMargin:0,projectedMarginLevel:0,cutLevel:null,canTargetRisk:false,error});
 const numbers=[v.equity,v.usedMargin,v.entry,v.stop,v.profitPerPriceUnit,v.marginPerUnit,v.step,v.maxQuantity];
 if(numbers.some(n=>!Number.isFinite(n))||v.equity<=0||v.usedMargin<0||v.entry<=0||v.stop<=0||v.profitPerPriceUnit<=0||v.marginPerUnit<=0||v.step<=0||v.maxQuantity<=0)return empty('Fresh account, quote, conversion, quantity step and maximum are required');
 if(v.side==='buy'?v.stop>=v.entry:v.stop<=v.entry)return empty('Stop must be below Buy entry or above Sell entry');
 const lossUnit=Math.abs(v.entry-v.stop)*v.profitPerPriceUnit;
 const target=v.targetLevel??150;
 const cut=v.marginCutUsePercent&&v.marginCutUsePercent>0?10000/v.marginCutUsePercent:null;
 if(!Number.isFinite(target)||target<=0||cut===null)return empty('Verified broker margin-cut level is required');
 if(target<=cut)return empty('Target margin level must exceed broker margin-cut level');
 const equityFloor=v.accountEquityStopLoss??0;
 if(!Number.isFinite(equityFloor)||equityFloor<0||equityFloor>=v.equity)return empty('Account equity stop-out threshold is invalid or already reached');
 const floor=(n:number)=>Math.max(0,Number((Math.floor(Math.max(0,n)/v.step+1e-9)*v.step).toPrecision(12)));
 const targetUnits=floor(v.equity*.5/lossUnit);
 const marginBound=(v.equity-target/100*v.usedMargin)/(lossUnit+target/100*v.marginPerUnit);
 const safeUnits=floor(Math.min(targetUnits,v.maxQuantity,Math.max(0,marginBound),Math.max(0,(v.equity-equityFloor)/lossUnit-v.step*1e-9)));
 const candidate=targetUnits;
 const canTargetRisk=candidate>0&&candidate<=v.maxQuantity+v.step*1e-9&&candidate<=safeUnits+v.step*1e-9;
 const riskAmount=safeUnits*lossUnit,projectedEquity=v.equity-riskAmount,projectedUsedMargin=v.usedMargin+safeUnits*v.marginPerUnit;
 return {riskQuantity:targetUnits,safeQuantity:safeUnits,riskAmount,projectedEquity,projectedUsedMargin,projectedMarginLevel:projectedUsedMargin>0?100*projectedEquity/projectedUsedMargin:Infinity,cutLevel:cut,canTargetRisk,error:safeUnits>0?'':'No quantity satisfies the requested 150% margin buffer'};
}

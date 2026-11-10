import type {IndicatorComponent,IndicatorConfig,IndicatorType} from '../types';

export const indicatorComponents:Record<IndicatorType,ReadonlyArray<{key:IndicatorComponent;label:string}>>={
 sma:[],ema:[],low:[],high:[],today_low:[],today_high:[],previous_day_low:[],previous_day_high:[],week_low:[],week_high:[],previous_week_low:[],previous_week_high:[],two_days_ago_low:[],two_days_ago_high:[],three_days_ago_low:[],three_days_ago_high:[],
 ichimoku:[
  {key:'conversion',label:'Show conversion line'},
  {key:'base',label:'Show base line'},
  {key:'spanA',label:'Show leading span A'},
  {key:'spanB',label:'Show leading span B'},
  {key:'lagging',label:'Show lagging line'},
  {key:'cloud',label:'Show cloud fill'},
 ],
 macd:[
  {key:'macd',label:'Show MACD line'},
  {key:'signal',label:'Show signal line'},
  {key:'histogram',label:'Show histogram'},
  {key:'zero',label:'Show zero line'},
 ],
};

/** Older workspaces and omitted components keep the original appearance. */
export const indicatorComponentVisible=(config:IndicatorConfig,key:IndicatorComponent)=>config.visibility?.[key]!==false;

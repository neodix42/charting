export const calendarRangeNames: Readonly<{
 today_low:"Today's low";today_high:"Today's high";previous_day_low:'Previous day low';previous_day_high:'Previous day high';
 week_low:"Week's low";week_high:"Week's high";previous_week_low:'Previous week low';previous_week_high:'Previous week high';
 two_days_ago_low:'2 days ago low';two_days_ago_high:'2 days ago high';three_days_ago_low:'3 days ago low';three_days_ago_high:'3 days ago high';
 today_open:"Today's open";today_close:"Today's close";previous_day_open:'Previous day open';previous_day_close:'Previous day close';
 two_days_ago_open:'2 days ago open';two_days_ago_close:'2 days ago close';three_days_ago_open:'3 days ago open';three_days_ago_close:'3 days ago close';
 week_open:"Week's open";week_close:"Week's close";previous_week_open:'Previous Week open';previous_week_close:'Previous Week close';
}>;
export type CalendarRangeType=keyof typeof calendarRangeNames;
export function isCalendarRange(id:string):id is CalendarRangeType;
export function isIntradayPeriod(period:string):boolean;
export function calendarRangeField(id:CalendarRangeType):'open'|'close'|'low'|'high';
export function calendarRangeOffset(id:string):number;
export function calendarRange(id:CalendarRangeType,coveredFrom?:number):(timestamp:number,value:number)=>number|undefined;

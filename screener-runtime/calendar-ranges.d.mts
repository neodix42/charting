export const calendarRangeNames: Readonly<{
 today_low:"Today's low";today_high:"Today's high";previous_day_low:'Previous day low';previous_day_high:'Previous day high';
 week_low:"Week's low";week_high:"Week's high";previous_week_low:'Previous week low';previous_week_high:'Previous week high';
 two_days_ago_low:'2 days ago low';two_days_ago_high:'2 days ago high';three_days_ago_low:'3 days ago low';three_days_ago_high:'3 days ago high';
}>;
export type CalendarRangeType=keyof typeof calendarRangeNames;
export function isCalendarRange(id:string):id is CalendarRangeType;
export function isIntradayPeriod(period:string):boolean;
export function calendarRangeOffset(id:string):number;
export function calendarRange(id:CalendarRangeType,coveredFrom?:number):(timestamp:number,value:number)=>number|undefined;

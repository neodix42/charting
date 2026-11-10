export const calendarRangeNames: Readonly<{
 today_low:"Today's low";today_high:"Today's high";previous_day_low:'Previous day low';previous_day_high:'Previous day high';
 week_low:"Week's low";week_high:"Week's high";previous_week_low:'Previous week low';previous_week_high:'Previous week high';
}>;
export type CalendarRangeType=keyof typeof calendarRangeNames;
export function isCalendarRange(id:string):id is CalendarRangeType;
export function isIntradayPeriod(period:string):boolean;
export function calendarRange(id:CalendarRangeType,coveredFrom?:number):(timestamp:number,value:number)=>number|undefined;

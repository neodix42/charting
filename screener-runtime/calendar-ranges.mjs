// Shared by CSV enrichment and chart overlays. Timestamps are UTC milliseconds;
// only the current observation and earlier observations can affect a result.
export const calendarRangeNames = Object.freeze({
  today_low: "Today's low",
  today_high: "Today's high",
  previous_day_low: 'Previous day low',
  previous_day_high: 'Previous day high',
  week_low: "Week's low",
  week_high: "Week's high",
  previous_week_low: 'Previous week low',
  previous_week_high: 'Previous week high',
  two_days_ago_low: '2 days ago low',
  two_days_ago_high: '2 days ago high',
  three_days_ago_low: '3 days ago low',
  three_days_ago_high: '3 days ago high',
});
export const isCalendarRange = id => Object.hasOwn(calendarRangeNames, id);
export const isIntradayPeriod = period => ['s1','m1','m5','m15','m30','h1','h4','1m','5m','15m','1h','4h'].includes(period);
export const calendarRangeOffset = id => id.startsWith('three_days_ago_') ? 3 : id.startsWith('two_days_ago_') ? 2 : id.startsWith('previous_') ? 1 : 0;

export function calendarRange(id, coveredFrom) {
  if (!isCalendarRange(id)) throw Error('Unsupported day/week range.');
  const weekly = id.includes('week'), offset = calendarRangeOffset(id), lowest = id.endsWith('_low');
  const duration = (weekly ? 7 : 1) * 86400000, origin = weekly ? -3 * 86400000 : 0; // Monday before the Unix epoch.
  let bucket, current, complete = false, last = -Infinity;
  const previous = [];
  return (timestamp, value) => {
    if (!Number.isSafeInteger(timestamp) || Math.abs(timestamp) > 8640000000000000 || timestamp < last || !Number.isFinite(value)) throw Error('Day/week ranges require chronological UTC timestamps and finite candle prices.');
    const next = Math.floor((timestamp - origin) / duration);
    coveredFrom ??= timestamp;
    if (next !== bucket) {
      // Skip empty market-closure buckets without manufacturing observations.
      if(bucket!==undefined&&offset){previous.unshift(complete?current:undefined);previous.length=Math.min(previous.length,offset);}
      current = value; bucket = next;
      complete = next * duration + origin >= coveredFrom;
    } else current = lowest ? Math.min(current, value) : Math.max(current, value);
    last = timestamp;
    return offset ? previous[offset-1] : complete ? current : undefined;
  };
}

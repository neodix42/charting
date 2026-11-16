/** Display UTC timestamps at whole-second precision without rounding the instant. */
export function utcDate(value?:number):string {
 if(value===undefined||!Number.isFinite(value))return '—';
 const date=new Date(value);
 return Number.isFinite(date.getTime())?date.toISOString().replace('T',' ').replace(/\.\d{3}Z$/,' UTC'):'Invalid UTC time';
}

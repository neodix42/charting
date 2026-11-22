import type {Health,Instrument} from '../types';
import {calendarMarketSession,regularMarketStatus} from './DukascopyTradingHours';

/** Connection/account environment alone cannot establish asset tradability. */
export function orderMarketStatus(health:Health|undefined,metadata:Instrument['providerMetadata'],now:number){
 if(health?.state==='marketClosed')return {text:'Market closed',basis:health.reason||'JForex reports this market is closed.'};
 const calendar=calendarMarketSession(metadata,now);
 if(calendar)return {text:calendar.status==='Closed'?'Market closed':'Market opened',basis:`JForex instrument calendar · ${calendar.hours}`};
 const regular=regularMarketStatus(metadata,now);
 if(regular)return {text:regular==='Closed'?'Market closed':'Market opened',basis:'Regular session schedule; holidays, breaks and halts may differ.'};
 return {text:'Market status unavailable',basis:'No current broker closure report or supported session schedule is available. Connection state does not establish market opening.'};
}

import {liveAlertErrorText} from '../workspace/screenerLiveAlerts';

/** Native disclosure keeps errors keyboard accessible without opening a second application dialog. */
export function LiveAlertError({message,label}:{message:string;label:string}){
 const text=liveAlertErrorText(message);
 return <details className="live-alert-error-details" key={text}><summary aria-label={`Show error: ${label}`} title={`Show error: ${label}`}><svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" aria-hidden="true"><circle cx="12" cy="12" r="9"/><path d="M12 7v6m0 3v1"/></svg></summary><div className="live-alert-error-message" role="note" aria-label={`Error details: ${label}`}>{text}</div></details>;
}

import type {FeedKey,Instrument} from '../types';

interface MarginTerms {maximum:number;minimumInitialPercent:number;spot:boolean}
const available=(value:unknown)=>typeof value==='string'&&['available','trading','online','active'].includes(value.trim().toLowerCase());
const percent=(value:number)=>new Intl.NumberFormat('en-US',{maximumSignificantDigits:6}).format(value)+'%';

/** Public asset terms only: no account leverage or position margin is inferred. */
export function hyperliquidMarginTerms(feedKey:FeedKey,instrument?:Instrument):MarginTerms|null {
 if(feedKey.exchange!=='hyperliquid'||!instrument||instrument.nativeId!==feedKey.instrument||instrument.market!==feedKey.market||!available(instrument.availability))return null;
 if(feedKey.market==='spot')return {maximum:1,minimumInitialPercent:100,spot:true};
 if(feedKey.market!=='perpetual')return null;
 const raw=instrument.providerMetadata?.maxLeverage??'';
 if(typeof raw!=='string'||!/^[1-9]\d*$/.test(raw))return null;
 const maximum=Number(raw);
 if(!Number.isSafeInteger(maximum)||maximum<1)return null;
 return {maximum,minimumInitialPercent:100/maximum,spot:false};
}

export function HyperliquidMarginOverlay({feedKey,instrument,loading=false}:{feedKey:FeedKey;instrument?:Instrument;loading?:boolean}) {
 if(feedKey.exchange!=='hyperliquid')return null;
 const terms=hyperliquidMarginTerms(feedKey,instrument),spot=feedKey.market==='spot';
 return <aside className="hyperliquid-margin-overlay" aria-label="Hyperliquid leverage and margin">
  <strong>Hyperliquid {spot?'spot':'margin'}</strong>
  <span>{spot?'Spot leverage':'Asset max leverage'} <b>{terms?`1:${terms.maximum}`:'—'}</b></span>
  <span>{spot?'Funding requirement':'Minimum initial margin'} <b>{terms?percent(terms.minimumInitialPercent):'—'}</b>{terms?` of ${spot?'trade':'position'} value`:''}</span>
  {terms?<small>{terms.spot?'Fully funded spot; no borrowing.':'At asset max leverage. Selected leverage and position-size tiers can require more.'}</small>:<small>{loading?'Loading asset margin terms…':'Asset margin terms unavailable.'}</small>}
 </aside>;
}

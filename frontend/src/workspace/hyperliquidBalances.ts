import type {HyperliquidBalances,HyperliquidTokenBalance,HyperliquidVenueBalance} from '../types';

export const balanceIsStale=(observedAt:number|undefined,now:number)=>!observedAt||!Number.isFinite(observedAt)||observedAt>now+5000||now-observedAt>30000;
export const accountModeLabel=(mode:string)=>({disabled:'Manual / Standard',default:'Manual / Standard',unifiedAccount:'Unified Account',portfolioMargin:'Portfolio margin',dexAbstraction:'Legacy DEX abstraction'}[mode]??mode);
export const venueLabel=(venue:HyperliquidVenueBalance)=>venue.dex===''?'Main perps':venue.dex==='spot'?'Spot':venue.name||venue.dex;
export function orderedVenues(venues:readonly HyperliquidVenueBalance[]){
 const order=['','xyz','para','spot'];
 return [...venues].sort((a,b)=>{const left=order.indexOf(a.dex),right=order.indexOf(b.dex);return (left<0?order.length:left)-(right<0?order.length:right)||venueLabel(a).localeCompare(venueLabel(b));});
}
export function transferTokens(value:HyperliquidBalances,sourceDex:string,destinationDex:string):HyperliquidTokenBalance[]{
 if(sourceDex===destinationDex||value.shared)return [];
 const source=value.venues.find(venue=>venue.dex===sourceDex),destination=value.venues.find(venue=>venue.dex===destinationDex);
 if(!source||!destination||source.shared||destination.shared||source.error||destination.error)return [];
 return value.tokens.filter(token=>!!token.tokenId&&!!token.coin&&Number.isSafeInteger(token.token)&&token.token>=0
  &&(source.market==='spot'?source.balances.some(balance=>balance.token===token.token):source.collateralToken===token.token)
  &&(destination.market==='spot'||destination.collateralToken===token.token));
}

// Exact decimal comparison avoids rounding a Max balance upward through Number.
function decimalParts(value:string){const match=/^(\d+)(?:\.(\d+))?$/.exec(value.trim());return match?{whole:match[1],fraction:match[2]??''}:null;}
export function normalizedTransferAmount(value:string){const parts=decimalParts(value);if(!parts||value.length>64)return '';const fraction=parts.fraction.replace(/0+$/,'');return parts.whole.replace(/^0+(?=\d)/,'')+(fraction?'.'+fraction:'');}
export function transferAmountProblem(amount:string,available:string|undefined,precision:number):string {
 if(!amount.trim())return 'Enter an amount.';
 const input=decimalParts(amount),limit=available===undefined?null:decimalParts(available);
 if(!input||amount.length>64||!Number.isSafeInteger(precision)||precision<0||precision>18)return 'Enter a positive decimal amount.';
 if(input.fraction.replace(/0+$/,'').length>precision)return `Use at most ${precision} decimal places for this token.`;
 if(!limit||available!.length>200)return 'The available source balance is unavailable.';
 const places=Math.max(input.fraction.length,limit.fraction.length);
 const units=(value:{whole:string;fraction:string})=>BigInt(value.whole+value.fraction.padEnd(places,'0'));
 const requested=units(input);if(requested<=0n)return 'Enter a positive decimal amount.';
 return requested>units(limit)?'The amount exceeds the available source balance.':'';
}

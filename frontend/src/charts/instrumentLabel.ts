import type {Exchange,Instrument} from '../types';

// Keep provider-native IDs in saved selections; metadata only changes the label.
export function instrumentLabel(exchange:Exchange,instrument:Instrument):string {
 if(exchange==='hyperliquid'&&instrument.market==='spot')return instrument.providerMetadata?.displaySymbol||`${instrument.baseAsset}/${instrument.quoteAsset}`;
 if(['forex','metal','cfd'].includes(instrument.market))return instrument.providerMetadata?.sdkSymbol||`${instrument.baseAsset}/${instrument.quoteAsset}`;
 return instrument.nativeId;
}

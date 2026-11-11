import type {Backend,DucascopyInfo,DucascopySettings,Instrument} from '../types';
import {intervals} from '../types';

/** Credentials and quotes here are explicit browser fixtures; no JForex login is made. */
export function ducascopyBridge(){
 const key='cryptodesk-explicit-test-ducascopy';
 let saved:DucascopySettings=JSON.parse(sessionStorage.getItem(key)||'null')??{username:'',password:'',environment:'demo'};
 let connected=false,tests=0;
 const pairs=[['EUR','USD'],['GBP','USD'],['USD','JPY']];
 const catalog:Instrument[]=[...pairs.map(([baseAsset,quoteAsset])=>({nativeId:baseAsset+quoteAsset,market:'forex' as const,baseAsset,quoteAsset,priceIncrement:quoteAsset==='JPY'?'0.001':'0.00001',sizeIncrement:'0.001',availability:'available',supportedIntervals:intervals,providerMetadata:{priceSide:'bid',volumeUnit:`million ${baseAsset}`,sdkSymbol:`${baseAsset}/${quoteAsset}`,description:`${baseAsset} / ${quoteAsset} · explicit JForex test fixture`}})),
  ...[
   {nativeId:'XAUUSD',market:'metal' as const,baseAsset:'XAU',sdkSymbol:'XAU/USD',description:'Gold'},
   {nativeId:'XAGUSD',market:'metal' as const,baseAsset:'XAG',sdkSymbol:'XAG/USD',description:'Silver'},
   {nativeId:'USA500IDXUSD',market:'cfd' as const,baseAsset:'USA500.IDX',sdkSymbol:'USA500.IDX/USD',description:'US 500 index'},
   {nativeId:'BRENTCMDUSD',market:'cfd' as const,baseAsset:'BRENT.CMD',sdkSymbol:'BRENT.CMD/USD',description:'Brent crude oil'},
   {nativeId:'AAPLUSUSD',market:'cfd' as const,baseAsset:'AAPL.US',sdkSymbol:'AAPL.US/USD',description:'Apple stock'},
   {nativeId:'SPYUSUSD',market:'cfd' as const,baseAsset:'SPY.US',sdkSymbol:'SPY.US/USD',description:'SPDR S&P 500 ETF'},
   {nativeId:'BTCUSD',market:'cfd' as const,baseAsset:'BTC',sdkSymbol:'BTC/USD',description:'Bitcoin',assetType:'crypto'},
  ].map(({sdkSymbol,description,assetType,...item})=>({...item,quoteAsset:'USD',priceIncrement:'0.01',sizeIncrement:'',availability:'available',supportedIntervals:intervals,providerMetadata:{priceSide:'bid',volumeUnit:'provider units',sdkSymbol,description,assetType:assetType||item.market}})),
 ];
 const info=():DucascopyInfo=>({username:saved.username,environment:saved.environment,passwordConfigured:!!saved.password,runtimeAvailable:true,runtimeDirectory:'/explicit-test/jforex',javaPath:'/explicit-test/java/bin/java',connected,instrumentCount:connected?catalog.length:0});
 const connect=()=>{if(!saved.username||!saved.password)throw Error('Save JForex credentials in Settings → Dukascopy.');connected=true;};
 const instruments=():Instrument[]=>{connect();return structuredClone(catalog);};
 const api:Pick<Backend,'GetDucascopy'|'ConfigureDucascopy'|'TestDucascopyConnection'|'GetDucascopyLoginChallenge'|'TestDucascopyConnectionWithPIN'|'CancelDucascopyConnectionTest'|'RefreshDucascopyInstruments'>={
  async GetDucascopy(){return info();},
  async RefreshDucascopyInstruments(){return instruments();},
  async ConfigureDucascopy(request){
   const username=request.username.trim(),sameIdentity=username===saved.username&&request.environment===saved.environment;
   const password=request.clearPassword?'':request.password||(sameIdentity?saved.password:'');
   if(!username)throw Error('Enter a JForex username.');
   if(!sameIdentity&&!password)throw Error('Enter a password for the new JForex identity.');
   if(username!==saved.username||password!==saved.password||request.environment!==saved.environment)connected=false;
   saved={username,password,environment:request.environment};sessionStorage.setItem(key,JSON.stringify(saved));return info();
  },
  async TestDucascopyConnection(){tests++;connect();return info();},
  async GetDucascopyLoginChallenge(){return{imageDataUrl:'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aX6kAAAAASUVORK5CYII='};},
  async TestDucascopyConnectionWithPIN(){tests++;connect();return info();},
  async CancelDucascopyConnectionTest(){return info();},
 };
 Object.assign(window,{__testDucascopyTests:()=>tests});
 return {api,instruments,connect};
}

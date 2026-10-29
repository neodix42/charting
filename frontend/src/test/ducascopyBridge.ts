import type {Backend,DucascopyInfo,DucascopySettings,Instrument} from '../types';
import {intervals} from '../types';

/** Credentials and quotes here are explicit browser fixtures; no JForex login is made. */
export function ducascopyBridge(){
 const key='cryptodesk-explicit-test-ducascopy';
 let saved:DucascopySettings=JSON.parse(sessionStorage.getItem(key)||'null')??{username:'',password:'',environment:'demo'};
 let connected=false,tests=0;
 const pairs=[['EUR','USD'],['GBP','USD'],['USD','JPY']];
 const info=():DucascopyInfo=>({username:saved.username,environment:saved.environment,passwordConfigured:!!saved.password,runtimeAvailable:true,runtimeDirectory:'/explicit-test/jforex',javaPath:'/explicit-test/java/bin/java',connected,instrumentCount:connected?pairs.length:0});
 const connect=()=>{if(!saved.username||!saved.password)throw Error('Save JForex credentials in Settings → Ducas Copy.');connected=true;};
 const instruments=():Instrument[]=>{connect();return pairs.map(([baseAsset,quoteAsset])=>({nativeId:baseAsset+quoteAsset,market:'forex',baseAsset,quoteAsset,priceIncrement:quoteAsset==='JPY'?'0.001':'0.00001',sizeIncrement:'0.001',availability:'available',supportedIntervals:intervals,providerMetadata:{priceType:'bid',volumeUnit:`million ${baseAsset}`,description:`${baseAsset} / ${quoteAsset} · explicit JForex test fixture`}}));};
 const api:Pick<Backend,'GetDucascopy'|'ConfigureDucascopy'|'TestDucascopyConnection'|'GetDucascopyLoginChallenge'|'TestDucascopyConnectionWithPIN'|'CancelDucascopyConnectionTest'>={
  async GetDucascopy(){return info();},
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

import type {DucascopyInfo,HyperliquidInfo,HyperliquidSettings,TradePreview,TradeRequest,TradeResult,TradingBackend} from '../types';

/** Explicit browser fixtures only. This adapter never accesses an exchange. */
export function tradingBridge(dukascopy:()=>Promise<DucascopyInfo>):TradingBackend {
 const storage='cryptodesk-explicit-test-hyperliquid';
 let saved:HyperliquidSettings=JSON.parse(sessionStorage.getItem(storage)||'null')??{accountAddress:'',privateKey:'',network:'mainnet'};
 let connected=false,tests=0;const previews=new Map<string,TradePreview>(),submissions:string[]=[],requests:TradeRequest[]=[];
 const info=():HyperliquidInfo=>({accountAddress:saved.accountAddress,network:saved.network,privateKeyConfigured:!!saved.privateKey,agentAddress:saved.privateKey?'0x'+'a'.repeat(40):'',connected});
 const api:TradingBackend={
  async OpenHyperliquidHelp(kind){Object.assign(window,{__testHyperliquidHelp:kind});},
  async GetHyperliquid(){return info();},
  async ConfigureHyperliquid(next){const same=next.accountAddress.toLowerCase()===saved.accountAddress.toLowerCase()&&next.network===saved.network;saved={accountAddress:next.accountAddress.trim(),network:next.network,privateKey:next.clearPrivateKey?'':next.privateKey|| (same?saved.privateKey:'')};connected=false;sessionStorage.setItem(storage,JSON.stringify(saved));return info();},
  async TestHyperliquidConnection(){tests++;if(!saved.privateKey)throw Error('Missing dedicated API wallet');connected=true;return info();},
  async GetTradingStatus(key){const duca=key.exchange==='dukascopy'?await dukascopy():null,configured=duca?duca.passwordConfigured&&!!duca.username:key.exchange==='hyperliquid'&&!!saved.privateKey,blocked=key.exchange==='hyperliquid'&&key.market==='spot'&&saved.network==='testnet';return {configured,enabled:configured&&!blocked,error:blocked?'Spot chart IDs are mainnet IDs; testnet spot trading is unavailable to prevent trading a different token':undefined,environment:duca?.environment??saved.network,canBuy:!blocked,canSell:!blocked,leverageSupported:key.exchange==='hyperliquid'&&key.market==='perpetual',maxLeverage:0,protectiveOrdersSupported:key.exchange==='dukascopy'||key.market==='perpetual',quantityLabel:'base units'};},
  async PreviewTrade(request){requests.push(structuredClone(request));const status=await api.GetTradingStatus(request.feedKey);if(!status.enabled)throw Error('Trading credentials are not configured');if(!Number.isFinite(Number(request.quantity))||Number(request.quantity)<=0)throw Error('Quantity must be positive');if(!status.protectiveOrdersSupported&&(request.stopLoss||request.takeProfit))throw Error('Spot SL/TP unsupported');const result:TradePreview={...request,id:crypto.randomUUID(),entryPrice:request.feedKey.exchange==='dukascopy'?'1.1':'61000',environment:status.environment,leverage:request.feedKey.exchange==='dukascopy'?100:request.leverage||1,expiresAt:Date.now()+60000,warnings:request.feedKey.exchange==='hyperliquid'&&request.feedKey.market==='perpetual'?['Isolated margin. Existing positions are netted by the exchange.']:[]};previews.set(result.id,result);return structuredClone(result);},
  async SubmitTrade(id){const preview=previews.get(id);if(!preview||preview.expiresAt<=Date.now())throw Error('Expired or consumed preview');previews.delete(id);submissions.push(id);return {status:'submitted',orderIds:['explicit-fixture-order'],message:'Explicit browser fixture: no actual order was sent.'} satisfies TradeResult;}
 };
 Object.assign(window,{__testHyperliquidTests:()=>tests,__testTradeSubmissions:()=>structuredClone(submissions),__testTradeRequests:()=>structuredClone(requests)});
 return api;
}

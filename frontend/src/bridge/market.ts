import type {Backend,Candle,FeedKey,MarketEvent,Snapshot,Runtime} from '../types';
export interface Subscription {close():void;ready:Promise<void>}
interface Consumer {chartId:string;generation:number;key:string;lease?:string;revision:number;buffer:MarketEvent[];onData:(s:Snapshot,replace:boolean)=>void;closed:boolean;resyncing:boolean;overflow:boolean}
export const keyOf=(key:FeedKey)=>[key.exchange,key.market,key.instrument,key.timeframe].join('|');
export const marketDiagnostics={chartInstances:0,consumers:0,bufferedEvents:0};
export function mergeBars(existing:Candle[],incoming:Candle[],max=10000,window:'oldest'|'newest'='newest'):Candle[]{const map=new Map(existing.map(b=>[b.time,b]));for(const b of incoming){if(!Number.isInteger(b.time)||b.time<0||![b.open,b.high,b.low,b.close,b.volume].every(v=>Number.isFinite(Number(v))))continue;const old=map.get(b.time);if(!old||b.sourceRevision>=old.sourceRevision)map.set(b.time,b);}const sorted=[...map.values()].sort((a,b)=>a.time-b.time);return window==='oldest'?sorted.slice(0,max):sorted.slice(-max);}
/** Batches by timestamp, retaining both sides of a candle boundary. */
export class CandleBatcher {private bars=new Map<number,Candle>();private timer?:ReturnType<typeof setTimeout>;constructor(private deliver:(bars:Candle[])=>void,private max=10000){}push(values:Candle[]){for(const value of values){const old=this.bars.get(value.time);if(!old||value.sourceRevision>=old.sourceRevision)this.bars.set(value.time,value);}if(this.bars.size>this.max){this.flush();return;}if(!this.timer)this.timer=setTimeout(()=>this.flush(),100);}flush(){if(this.timer)clearTimeout(this.timer);this.timer=undefined;if(this.bars.size){const values=[...this.bars.values()].sort((a,b)=>a.time-b.time);this.bars.clear();this.deliver(values);}}close(){if(this.timer)clearTimeout(this.timer);this.timer=undefined;this.bars.clear();}}
/** Listener exists before any attach. Each lease has an independent snapshot watermark. */
export class MarketBridge {
 private consumers=new Set<Consumer>();private cancel:()=>void;
 constructor(private api:Backend,runtime:Runtime){this.cancel=runtime.EventsOn('market:update',(event:MarketEvent)=>this.receive(event));}
 attach(chartId:string,generation:number,key:FeedKey,from:number,to:number,onData:Consumer['onData']):Subscription {
  const c:Consumer={chartId,generation,key:keyOf(key),revision:-1,buffer:[],onData,closed:false,resyncing:false,overflow:false};this.consumers.add(c);marketDiagnostics.consumers++;
  const ready=this.api.AttachChart(chartId,generation,key,from,to).then(async snapshot=>{
   if(c.closed){await this.api.DetachChart(snapshot.leaseId);return;}
   c.lease=snapshot.leaseId;c.revision=snapshot.revision;c.onData(snapshot,true);
   const buffered=c.buffer.splice(0).filter(e=>e.leaseId===c.lease).sort((a,b)=>a.revision-b.revision);this.updateCount();
   for(const event of buffered)this.apply(c,event);
   if(c.overflow){c.overflow=false;await this.resync(c);}
  }).catch(error=>{this.closeConsumer(c);throw error;});
  return {ready,close:()=>this.closeConsumer(c)};
 }
 private updateCount(){marketDiagnostics.bufferedEvents=[...this.consumers].reduce((n,c)=>n+c.buffer.length,0);}
 private closeConsumer(c:Consumer){if(c.closed)return;c.closed=true;this.consumers.delete(c);marketDiagnostics.consumers--;c.buffer=[];this.updateCount();if(c.lease)void this.api.DetachChart(c.lease).catch(()=>{});}
 private receive(event:MarketEvent){for(const c of this.consumers){if(c.closed||event.chartId!==c.chartId||event.generation!==c.generation||keyOf(event.feedKey)!==c.key)continue;if(!c.lease||c.resyncing){if(c.buffer.length>=512){c.buffer=[];c.overflow=true;}c.buffer.push(event);this.updateCount();continue;}if(event.leaseId===c.lease)this.apply(c,event);}}
 private apply(c:Consumer,event:MarketEvent){if(c.closed||event.revision<=c.revision)return;if(event.kind==='resyncRequired'||event.revision!==c.revision+1){c.buffer.push(event);this.updateCount();void this.resync(c);return;}c.revision=event.revision;c.onData(event,false);}
 private async resync(c:Consumer){if(c.closed||!c.lease||c.resyncing)return;c.resyncing=true;try{const snapshot=await this.api.GetFeedSnapshot(c.lease,c.revision);if(c.closed)return;if(snapshot.revision>=c.revision){c.revision=snapshot.revision;c.onData(snapshot,true);}const buffered=c.buffer.splice(0).filter(e=>e.leaseId===c.lease).sort((a,b)=>a.revision-b.revision);this.updateCount();c.resyncing=false;for(const e of buffered)this.apply(c,e);if(c.overflow){c.overflow=false;void this.resync(c);}}catch(error){if(!c.closed)c.onData({leaseId:c.lease,revision:c.revision,bars:[],health:{state:'disconnected',reason:String(error)}},false);}finally{c.resyncing=false;}}
 dispose(){for(const c of this.consumers)this.closeConsumer(c);this.cancel();}
}

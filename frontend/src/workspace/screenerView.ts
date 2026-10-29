import type {ScreenerView} from '../types';

export const initialScreenerView=():ScreenerView=>({symbols:[],timeframe:'d1',from:'2010-01-01',to:new Date().toISOString().slice(0,10),concurrency:5,collapsedCategories:[]});
// This compatibility field describes a view; only Settings changes the shared
// worker preference. Download submissions always read the current backend info.
export const normalizeScreenerView=(value:ScreenerView):ScreenerView=>structuredClone({...value,concurrency:Number.isInteger(value.concurrency)&&value.concurrency>=1&&value.concurrency<=20?value.concurrency:5});

/** One writer, coalescing edits while a previous durable save is pending. */
export class ScreenerViewWriter {
 private pending?:ScreenerView;private flight?:Promise<void>;
 constructor(private persist:(value:ScreenerView)=>Promise<ScreenerView>,private onError:(error:unknown)=>void){}
 edit(value:ScreenerView){this.pending=normalizeScreenerView(value);void this.flush().catch(this.onError);}
 async flush():Promise<void>{
  if(this.flight){await this.flight;if(this.pending)return this.flush();return;}
  const next=this.pending;if(!next)return;this.pending=undefined;
  this.flight=(async()=>{try{await this.persist(next);}catch(error){if(!this.pending)this.pending=next;throw error;}finally{this.flight=undefined;}})();
  await this.flight;if(this.pending)return this.flush();
 }
}

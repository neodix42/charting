import type {SaveResult, Workspace} from '../types';
export interface SaveStatus {state:'saved'|'pending'|'saving'|'error';revision:number;acknowledged:number;sequence:number;error?:string}
export type Persist=(revision:number,session:string,sequence:number,workspace:Workspace)=>Promise<SaveResult>;
/** One writer, newest pending draft only. Acknowledgments never mark a newer draft saved. */
export class Autosave {
 private pending?:{workspace:Workspace;sequence:number};private inFlight?:Promise<void>;private idle?:ReturnType<typeof setTimeout>;private deadline?:ReturnType<typeof setTimeout>;private listeners=new Set<()=>void>();
 private session=crypto.randomUUID();private status:SaveStatus;
 constructor(revision:number,private persist:Persist){this.status={state:'saved',revision,acknowledged:0,sequence:0};}
 getStatus=()=>this.status;
 subscribe=(fn:()=>void)=>{this.listeners.add(fn);return ()=>{this.listeners.delete(fn);};};
 private emit(patch:Partial<SaveStatus>){this.status={...this.status,...patch};this.listeners.forEach(fn=>fn());}
 edit(workspace:Workspace){const sequence=this.status.sequence+1;this.pending={workspace:structuredClone(workspace),sequence};this.emit({sequence,state:'pending',error:undefined});if(this.idle)clearTimeout(this.idle);this.idle=setTimeout(()=>{void this.flush().catch(()=>{});},500);if(!this.deadline)this.deadline=setTimeout(()=>{void this.flush().catch(()=>{});},2000);}
 private clearTimers(){if(this.idle)clearTimeout(this.idle);if(this.deadline)clearTimeout(this.deadline);this.idle=undefined;this.deadline=undefined;}
 async flush():Promise<void>{
  this.clearTimers();
  if(this.inFlight){await this.inFlight;if(this.pending)return this.flush();return;}
  const next=this.pending;if(!next)return;this.pending=undefined;this.emit({state:'saving',error:undefined});
  this.inFlight=(async()=>{try{const result=await this.persist(this.status.revision,this.session,next.sequence,next.workspace);if(result.acceptedEditSequence!==next.sequence||result.revision<=this.status.revision)throw Error('Save acknowledgment did not match the submitted workspace');this.emit({revision:result.revision,acknowledged:next.sequence,state:this.pending?'pending':'saved'});}catch(e){if(!this.pending||this.pending.sequence<next.sequence)this.pending=next;this.emit({state:'error',error:e instanceof Error?e.message:String(e)});throw e;}finally{this.inFlight=undefined;}})();
  await this.inFlight;if(this.pending)return this.flush();
 }
 dispose(){this.clearTimers();this.listeners.clear();}
}

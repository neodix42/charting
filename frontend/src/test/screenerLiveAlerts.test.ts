import {expect,it,vi} from 'vitest';
import type {LiveAlertsInfo} from '../workspace/screenerLiveAlerts';
import {initialLiveAlertsConfig,LiveAlertsConfigWriter} from '../workspace/screenerLiveAlerts';

const result=(config:LiveAlertsInfo['config']):LiveAlertsInfo=>({checks:[],savedConditionsRevision:0,conditionScopes:[],config,running:false,lastRun:0,nextRun:0,sent:0,status:'Disabled',assets:[],progress:0,currentAsset:'',recent:[]});

it('serializes disabled alert drafts, keeps latest selections and never starts delivery through autosave',async()=>{
 let release!:()=>void;const held=new Promise<void>(resolve=>{release=resolve;});
 const persist=vi.fn(async config=>{if(persist.mock.calls.length===1)await held;return result(config);});
 const writer=new LiveAlertsConfigWriter(persist,()=>{}),config=initialLiveAlertsConfig();
 writer.edit({...config,conditionIds:['first']});writer.edit({...config,enabled:true,conditionIds:['first','second']});
 expect(persist).toHaveBeenCalledTimes(1);expect(writer.busy).toBe(true);
 const flush=writer.flush();release();await flush;
 expect(persist).toHaveBeenCalledTimes(2);expect(persist.mock.calls[1][0]).toEqual({enabled:false,conditionIds:['first','second']});expect(writer.busy).toBe(false);
});

it('retains failed alert selections for close retry and copies caller data',async()=>{
 const errors=vi.fn(),persist=vi.fn().mockRejectedValueOnce(Error('settings disk full')).mockImplementation(async config=>result(config));
 const writer=new LiveAlertsConfigWriter(persist,errors),draft={...initialLiveAlertsConfig(),conditionIds:['signal']};
 writer.edit(draft);draft.conditionIds[0]='changed';await expect(writer.flush()).rejects.toThrow('settings disk full');expect(writer.busy).toBe(true);
 await writer.flush();expect(errors).toHaveBeenCalled();expect(persist.mock.calls[1][0].conditionIds).toEqual(['signal']);expect(writer.busy).toBe(false);
});

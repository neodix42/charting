import {expect,it,vi} from 'vitest';
import type {LiveAlertsInfo} from '../workspace/screenerLiveAlerts';
import {initialLiveAlertsConfig,LiveAlertsConfigWriter} from '../workspace/screenerLiveAlerts';

const result=(config:LiveAlertsInfo['config']):LiveAlertsInfo=>({config,running:false,lastRun:0,nextRun:0,sent:0,status:'Disabled',assets:[],progress:0,currentAsset:'',recent:[]});

it('serializes disabled alert drafts, keeps latest selections and never starts delivery through autosave',async()=>{
 let release!:()=>void;const held=new Promise<void>(resolve=>{release=resolve;});
 const persist=vi.fn(async config=>{if(persist.mock.calls.length===1)await held;return result(config);});
 const writer=new LiveAlertsConfigWriter(persist,()=>{}),config=initialLiveAlertsConfig();
 writer.edit({...config,conditionPeriods:[{conditionId:'first',period:'d1'}]});writer.edit({...config,enabled:true,conditionPeriods:[{conditionId:'first',period:'h1'},{conditionId:'second',period:'d1'}],symbols:['btcusd']});
 expect(persist).toHaveBeenCalledTimes(1);expect(writer.busy).toBe(true);
 const flush=writer.flush();release();await flush;
 expect(persist).toHaveBeenCalledTimes(2);expect(persist.mock.calls[1][0]).toEqual({enabled:false,conditionPeriods:[{conditionId:'first',period:'h1'},{conditionId:'second',period:'d1'}],symbols:['btcusd']});expect(writer.busy).toBe(false);
});

it('retains failed alert selections for close retry and copies caller data',async()=>{
 const errors=vi.fn(),persist=vi.fn().mockRejectedValueOnce(Error('settings disk full')).mockImplementation(async config=>result(config));
 const writer=new LiveAlertsConfigWriter(persist,errors),draft={...initialLiveAlertsConfig(),conditionPeriods:[{conditionId:'signal',period:'h1'}],symbols:['btcusd']};
 writer.edit(draft);draft.symbols[0]='changed';draft.conditionPeriods[0].period='d1';await expect(writer.flush()).rejects.toThrow('settings disk full');expect(writer.busy).toBe(true);
 await writer.flush();expect(errors).toHaveBeenCalled();expect(persist.mock.calls[1][0].symbols).toEqual(['btcusd']);expect(persist.mock.calls[1][0].conditionPeriods).toEqual([{conditionId:'signal',period:'h1'}]);expect(writer.busy).toBe(false);
});

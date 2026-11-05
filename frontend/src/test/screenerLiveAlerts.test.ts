import {expect,it,vi} from 'vitest';
import type {LiveAlertsInfo} from '../workspace/screenerLiveAlerts';
import {initialLiveAlertsConfig,liveAlertConditionSource,withLiveAlertSource,liveAlertDefinitions,configWithLiveAlerts,liveAlertSourceLabel,liveAlertErrorText,liveAlertBarClose,liveAlertPeriodLabel,LiveAlertsConfigWriter} from '../workspace/screenerLiveAlerts';

const result=(config:LiveAlertsInfo['config']):LiveAlertsInfo=>({checks:[],savedConditionsRevision:0,conditionScopes:[],config,running:false,lastRun:0,nextRun:0,sent:0,status:'Disabled',assets:[],progress:0,currentAsset:'',recent:[]});

it('serializes explicit alert drafts and preserves their chosen master state',async()=>{
 let release!:()=>void;const held=new Promise<void>(resolve=>{release=resolve;});
 const persist=vi.fn(async config=>{if(persist.mock.calls.length===1)await held;return result(config);});
 const writer=new LiveAlertsConfigWriter(persist,()=>{}),config=initialLiveAlertsConfig();
 writer.edit({...config,conditionIds:['first']});writer.edit({...config,enabled:true,conditionIds:['first','second']});
 expect(persist).toHaveBeenCalledTimes(1);expect(writer.busy).toBe(true);
 const flush=writer.flush();release();await flush;
 expect(persist).toHaveBeenCalledTimes(2);expect(persist.mock.calls[1][0]).toEqual({...config,enabled:true,conditionIds:['first','second']});expect(writer.busy).toBe(false);
});

it('retains failed alert selections for close retry and copies caller data',async()=>{
 const errors=vi.fn(),persist=vi.fn().mockRejectedValueOnce(Error('settings disk full')).mockImplementation(async config=>result(config));
 const writer=new LiveAlertsConfigWriter(persist,errors),draft={...initialLiveAlertsConfig(),conditionIds:['signal']};
 writer.edit(draft);draft.conditionIds[0]='changed';await expect(writer.flush()).rejects.toThrow('settings disk full');expect(writer.busy).toBe(true);
 await writer.flush();expect(errors).toHaveBeenCalled();expect(persist.mock.calls[1][0].conditionIds).toEqual(['signal']);expect(writer.busy).toBe(false);
});

it('labels inherited weekly periods and reports the following Monday as the weekly signal close',()=>{
 expect(liveAlertPeriodLabel('w1')).toBe('Weekly (W1)');
 expect(liveAlertPeriodLabel('all')).toBe('All analyzed periods (W1 / D1 / H1)');
 expect(liveAlertPeriodLabel('h1')).toBe('Hourly (H1)');
 expect(liveAlertPeriodLabel('d1')).toBe('Daily (D1)');
 expect(liveAlertPeriodLabel('m5')).toBe('M5');
 const monday=Date.UTC(2026,9,26);
 expect(liveAlertBarClose(monday,'w1')).toBe(Date.UTC(2026,10,2));
 expect(liveAlertBarClose(monday,'d1')).toBe(Date.UTC(2026,9,27));
 expect(liveAlertBarClose(monday,'h1')).toBe(Date.UTC(2026,9,26,1));
});

it('cleans repeated joined and legacy publication errors without dropping delivery or asset context',()=>{
 const pending='the newly closed bar is not yet available from the provider',delivery='Telegram alert for RACEITEUR, safe 5: 1 of 2 recipients confirmed; no automatic resend (recipientNotFound)';
 expect(liveAlertErrorText(`${pending}\n${pending}\n${delivery}\n${delivery}`)).toBe(`${pending}\n${delivery}`);
 expect(liveAlertErrorText(`${pending} `.repeat(5)+delivery)).toBe(`${pending} ${delivery}`);
 expect(liveAlertErrorText(`EURUSD: ${pending}\nGBPUSD: ${pending}`)).toBe(`EURUSD: ${pending}\nGBPUSD: ${pending}`);
 expect(liveAlertErrorText('   \n')).toBe('');
});


it('treats omitted sources as Dukascopy without changing legacy enabled configuration',()=>{
 const config={enabled:true,conditionIds:['daily','hourly']},before=structuredClone(config);
 expect(liveAlertConditionSource(config,'daily')).toBe('dukascopy');expect(liveAlertConditionSource(config,'hourly')).toBe('dukascopy');expect(config).toEqual(before);expect('conditionSources' in config).toBe(false);
 expect(liveAlertSourceLabel('hyperliquid')).toBe('Hyperliquid');
});
it('updates one condition source independently and restores omitted default without mutating selected conditions',()=>{
 const config={enabled:false,conditionIds:['daily']};
 const hyper=withLiveAlertSource(config,'daily','hyperliquid'),both=withLiveAlertSource(hyper,'unselected','bybit');
 expect(liveAlertConditionSource(both,'daily')).toBe('hyperliquid');expect(liveAlertConditionSource(both,'unselected')).toBe('bybit');expect(both.conditionIds).toEqual(['daily']);expect(config).toEqual({enabled:false,conditionIds:['daily']});
 const reset=withLiveAlertSource(both,'daily','dukascopy');expect(reset.conditionSources).toEqual([{conditionId:'unselected',source:'bybit'}]);expect(withLiveAlertSource(reset,'unselected','dukascopy')).toEqual(config);
});
it('queued autosave keeps the latest per-condition source choices and copies source arrays',async()=>{
 let release!:()=>void;const held=new Promise<void>(resolve=>{release=resolve;});
 const persist=vi.fn(async config=>{if(persist.mock.calls.length===1)await held;return result(config);}),writer=new LiveAlertsConfigWriter(persist,()=>{});
 const config={...initialLiveAlertsConfig(),conditionIds:['daily','hourly']},first=withLiveAlertSource(config,'daily','hyperliquid');writer.edit(first);
 const second=withLiveAlertSource(first,'hourly','bybit');writer.edit(second);second.conditionSources![0].source='binance';
 const flush=writer.flush();release();await flush;
 expect(persist.mock.calls[1][0]).toEqual({...config,conditionSources:[{conditionId:'daily',source:'hyperliquid'},{conditionId:'hourly',source:'bybit'}]});
});


it('migrates legacy selections to independent entries without changing caller master state or identifiers',()=>{
 const condition={id:'old-id',name:'Original saved rule',request:{period:'all'}} as any;
 const legacy={enabled:true,conditionIds:['old-id'],conditionSources:[{conditionId:'old-id',source:'hyperliquid' as const}]};
 const entries=liveAlertDefinitions(legacy,[condition]);expect(entries).toEqual([{id:'old-id',name:'Original saved rule',conditionId:'old-id',period:'all',dataSource:'hyperliquid',enabled:true}]);expect(legacy).not.toHaveProperty('alerts');
 const explicit=configWithLiveAlerts(legacy,entries);expect(explicit).toEqual({enabled:true,alerts:entries});expect(liveAlertDefinitions(explicit,[{...condition,name:'Renamed saved rule'}])[0].name).toBe('Original saved rule');
});
it('queued edits preserve distinct alert entries sharing a rule and the active master switch',async()=>{
 let release!:()=>void;const held=new Promise<void>(resolve=>{release=resolve;}),persist=vi.fn(async value=>{if(persist.mock.calls.length===1)await held;return result(value);});
 const writer=new LiveAlertsConfigWriter(persist,()=>{}),base={id:'one',name:'Spot hourly',conditionId:'same-rule',period:'h1' as const,dataSource:'binance' as const,enabled:true};
 writer.edit({enabled:true,alerts:[base]});const entries=[{...base,enabled:false},{...base,id:'two',name:'Perp daily',period:'d1' as const,dataSource:'hyperliquid' as const}];writer.edit({enabled:true,alerts:entries});entries[0].name='Mutated caller';const flushed=writer.flush();release();await flushed;
 expect(persist.mock.calls[1][0]).toMatchObject({enabled:true,alerts:[{id:'one',name:'Spot hourly',enabled:false},{id:'two',name:'Perp daily',enabled:true,period:'d1',dataSource:'hyperliquid'}]});
});

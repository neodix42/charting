import {expect,it,vi} from 'vitest';
import type {LiveAlertsInfo} from '../workspace/screenerLiveAlerts';
import {initialLiveAlertsConfig,liveAlertConditionSource,withLiveAlertSource,liveAlertDefinitions,orderedLiveAlertChecks,liveAlertCheckProgress,configWithLiveAlerts,renameConditionAlerts,liveAlertSourceLabel,liveAlertErrorText,liveAlertBarClose,liveAlertPeriodLabel,LiveAlertsConfigWriter} from '../workspace/screenerLiveAlerts';

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


it('renders history in configured alert order despite compact responses and supplies unattempted rows',()=>{
 const entries=[{id:'z',name:'Top alert',conditionId:'rule',period:'h1' as const,dataSource:'bybit' as const,enabled:true},{id:'a',name:'Middle alert',conditionId:'rule',period:'d1' as const,dataSource:'dukascopy' as const,enabled:true},{id:'m',name:'Last alert',conditionId:'rule',period:'w1' as const,dataSource:'hyperliquid' as const,enabled:false}],base={conditionId:'a',name:'Middle alert',assetsScanned:4,lastChecked:123,nextCheck:0,sentAlerts:1,status:'completed' as const,manual:false,error:''};
 const input=[base,{...base,conditionId:'z',alertId:'z',name:'Top alert',progressState:'queued' as const,progress:0,phase:'Queued'}];const ordered=orderedLiveAlertChecks(entries,input);
 expect(ordered.map(row=>row.alertId??row.conditionId)).toEqual(['z','a','m']);expect(ordered[0].lastChecked).toBe(123);expect(ordered[0].status).toBe('completed');expect(ordered[2]).toMatchObject({status:'never',lastChecked:0,assetsScanned:0});expect(input[0].conditionId).toBe('a');
});
it('keeps queue progress separate from previous check outcome and clamps only finite percentages',()=>{
 const previous={conditionId:'one',name:'First',assetsScanned:8,lastChecked:123,nextCheck:0,sentAlerts:2,status:'failed' as const,manual:false,error:'Previous attempt failed'};
 expect(liveAlertCheckProgress({...previous,progressState:'queued',progressMode:'manual',phase:'Waiting for turn',progress:0})).toEqual({state:'queued',phase:'Waiting for turn',value:0});expect(previous.status).toBe('failed');
 expect(liveAlertCheckProgress({...previous,progressState:'running',progress:180}).value).toBe(100);expect(liveAlertCheckProgress({...previous,progressState:'running',progress:-4}).value).toBe(0);expect(liveAlertCheckProgress({...previous,progress:NaN}).value).toBeUndefined();expect(liveAlertCheckProgress(previous)).toEqual({state:'failed',phase:'Failed',value:undefined});expect(liveAlertCheckProgress({...previous,progressState:'running',phase:'Preparing alert'})).toEqual({state:'running',phase:'Preparing alert',value:0});
});


it('follows renamed conditions only for exact copied alert names while preserving scope, identity and enabled state',()=>{
 const base={id:'a',name:'Before',conditionId:'condition',period:'d1' as const,dataSource:'hyperliquid' as const,enabled:true};
 const config={enabled:true,alerts:[base,{...base,id:'b',dataSource:'bybit' as const,enabled:false},{...base,id:'c',name:'Custom signal'},{...base,id:'d',conditionId:'other'},{...base,id:'e',name:'before'}]};
 const next=renameConditionAlerts(config,'condition','Before','After');
 expect(next).toEqual({...config,alerts:[{...base,name:'After'},{...config.alerts[1],name:'After'},...config.alerts.slice(2)]});
 expect(config.alerts.map(entry=>entry.name)).toEqual(['Before','Before','Custom signal','Before','before']);
 expect(renameConditionAlerts(next,'condition','Before','After')).toBe(next);
 expect(renameConditionAlerts(config,'condition','Before','Before')).toBe(config);
});

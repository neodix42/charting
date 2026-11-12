import {expect,it,vi} from 'vitest';
import type {LiveAlertCheck,LiveAlertOutcome,LiveAlertsInfo} from '../workspace/screenerLiveAlerts';
import {mergeLiveAlertsProgress,mergeLiveAlertsMetadata,initialLiveAlertsConfig,liveAlertConditionSource,withLiveAlertSource,liveAlertDefinitions,orderedLiveAlertChecks,liveAlertCheckProgress,configWithLiveAlerts,renameConditionAlerts,liveAlertSourceLabel,liveAlertErrorText,visibleLiveAlertCheck,liveAlertSentCount,liveAlertOutcomePresentation,liveAlertBarClose,liveAlertPeriodLabel,defaultLiveAlertSource,liveAlertDataSources,LiveAlertsConfigWriter} from '../workspace/screenerLiveAlerts';

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

it('cleans repeated publication errors and suppresses legacy missing-recipient delivery messages',()=>{
 const pending='the newly closed bar is not yet available from the provider',delivery='Telegram alert for RACEITEUR, safe 5: 1 of 2 recipients confirmed; no automatic resend (recipientNotFound)';
 expect(liveAlertErrorText(`${pending}\n${pending}\n${delivery}\n${delivery}`)).toBe(pending);
 expect(liveAlertErrorText(`${pending} `.repeat(5)+delivery)).toBe(pending);
 expect(liveAlertErrorText(`EURUSD: ${pending}\nGBPUSD: ${pending}`)).toBe(`EURUSD: ${pending}\nGBPUSD: ${pending}`);
 expect(liveAlertErrorText('   \n')).toBe('');
});

it('hides only missing-recipient Telegram delivery fragments while retaining unrelated failures',()=>{
 const delivery='Telegram alert delivery: 1 of 2 recipients confirmed; no automatic resend: Telegram operation failed (recipientNotFound)';
 const wrapped=`Telegram alert for XYZ:KSTR, everyday alert: ${delivery}`;
 const blocked=wrapped.replaceAll('recipientNotFound','blocked');
 const provider='XYZ:KSTR: provider history is unavailable';
 expect(liveAlertErrorText(wrapped)).toBe('');
 expect(liveAlertErrorText(delivery+'. This bar will not be resent automatically; check Telegram before any manual action.')).toBe('');
 expect(liveAlertErrorText(`${provider}\n${wrapped}\n${blocked}`)).toBe(`${provider}\n${blocked}`);
 expect(liveAlertErrorText(`${blocked} ${wrapped}`)).toBe(blocked);
 expect(liveAlertErrorText(`${blocked} ${delivery}`)).toBe(blocked);
 expect(liveAlertErrorText(`Unknown indicator recipientNotFound\n${wrapped}`)).toBe('Unknown indicator recipientNotFound');
});

it('suppresses recipient-only error styling without changing stored checks or confirmed delivery counts',()=>{
 const delivery='Telegram alert delivery: 1 of 2 recipients confirmed; no automatic resend: Telegram operation failed (recipientNotFound)';
 const check:LiveAlertCheck={conditionId:'rule',name:'Everyday',assetsScanned:7,lastChecked:1,nextCheck:2,sentAlerts:1,status:'completed',manual:false,error:delivery,progressState:'completed',phase:'Completed with errors',progress:100};
 const visible=visibleLiveAlertCheck(check);
 expect(visible).toMatchObject({error:'',phase:'Completed',sentAlerts:1,assetsScanned:7,progress:100});
 expect(check.error).toBe(delivery);expect(check.phase).toBe('Completed with errors');
 expect(visibleLiveAlertCheck({...check,status:'failed',progressState:'failed'})).toMatchObject({status:'completed',progressState:'completed',error:''});
 expect(visibleLiveAlertCheck({...check,error:delivery+'\nMissing indicator sma_20'})).toMatchObject({error:'Missing indicator sma_20',phase:'Completed with errors'});
 const outcome:LiveAlertOutcome={assetName:'KSTR',symbol:'xyz:KSTR',timeframe:'d1',time:1,conditionName:'Everyday',status:'failed',message:delivery,confirmedRecipients:1};
 expect(liveAlertOutcomePresentation(outcome)).toEqual({label:'Delivered to 1 recipient',message:'',className:''});
 expect(liveAlertOutcomePresentation({...outcome,confirmedRecipients:0}).label).toBe('No delivery confirmed');
 expect(liveAlertOutcomePresentation({...outcome,confirmedRecipients:undefined}).label).toBe('Delivery recorded');
 expect(liveAlertOutcomePresentation({...outcome,message:delivery.replace('recipientNotFound','unauthorized')})).toMatchObject({label:'Failed',className:'failed'});
 expect(outcome.status).toBe('failed');expect(outcome.confirmedRecipients).toBe(1);expect(outcome.message).toBe(delivery);
});


it('treats omitted sources as Dukascopy without changing legacy enabled configuration',()=>{
 const config={enabled:true,conditionIds:['daily','hourly']},before=structuredClone(config);
 expect(liveAlertConditionSource(config,'daily')).toBe('dukascopy');expect(liveAlertConditionSource(config,'hourly')).toBe('dukascopy');expect(config).toEqual(before);expect('conditionSources' in config).toBe(false);
 expect(liveAlertSourceLabel('hyperliquid')).toBe('Hyperliquid');
});
it('inherits saved Analysis source only when an alert has no explicit source and limits editable choices',()=>{
 const hyper={id:'hyper-rule',name:'Hyperliquid rule',request:{dataSource:'hyperliquid',period:'d1'}} as any;
 expect(defaultLiveAlertSource(hyper)).toBe('hyperliquid');expect(defaultLiveAlertSource(undefined)).toBe('dukascopy');
 expect(defaultLiveAlertSource({...hyper,request:{period:'d1'}})).toBe('dukascopy');
 expect(liveAlertDataSources.map(row=>row.source)).toEqual(['dukascopy','hyperliquid']);
 expect(liveAlertSourceLabel('bybit')).toBe('Bybit');expect(liveAlertSourceLabel('binance')).toBe('Binance');
 const missing={enabled:false,alerts:[{id:'draft',name:'Inherited',conditionId:'hyper-rule',period:'d1',enabled:true}]} as any;
 expect(liveAlertDefinitions(missing,[hyper])[0].dataSource).toBe('hyperliquid');expect(missing.alerts[0]).not.toHaveProperty('dataSource');
 const explicit={...missing,alerts:[{...missing.alerts[0],dataSource:'dukascopy'}]};
 expect(liveAlertDefinitions(explicit,[hyper])[0].dataSource).toBe('dukascopy');
 expect(liveAlertDefinitions({enabled:false,conditionIds:['hyper-rule']},[hyper])[0].dataSource).toBe('hyperliquid');
 expect(liveAlertDefinitions({...explicit,alerts:[{...explicit.alerts[0],dataSource:'bybit'}]},[hyper])[0].dataSource).toBe('bybit');
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
 expect(liveAlertCheckProgress({...previous,progressState:'running',progress:180}).value).toBe(100);expect(liveAlertCheckProgress({...previous,progressState:'running',progress:-4}).value).toBe(0);expect(liveAlertCheckProgress({...previous,progress:NaN}).value).toBeUndefined();expect(liveAlertCheckProgress(previous)).toEqual({state:'failed',phase:'Failed',value:undefined});expect(liveAlertCheckProgress({...previous,status:'completed'})).toEqual({state:'completed',phase:'Completed with errors',value:undefined});expect(liveAlertCheckProgress({...previous,progressState:'running',phase:'Preparing alert'})).toEqual({state:'running',phase:'Preparing alert',value:0});
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

it('merges compact live work without erasing schedule/evidence, and late metadata without rewinding progress',()=>{
 const row={conditionId:'a',name:'Rule',assetsScanned:0,lastChecked:10,nextCheck:100,sentAlerts:0,status:'completed' as const,manual:true,error:'',timingHint:'Daily close'};
 const metadata={...result({enabled:true}),savedConditionsRevision:7,checks:[row],assets:[{symbol:'btc',name:'BTC',category:'Crypto',periods:['d1']}]};
 const compact={...result({enabled:true}),running:true,status:'Enriching indicators',checks:[{...row,progressState:'running' as const,phase:'Enriching indicators',progress:40,nextCheck:0,timingHint:undefined}]};
 const live=mergeLiveAlertsProgress(metadata,compact);
 expect(live.checks[0]).toMatchObject({phase:'Enriching indicators',progress:40,nextCheck:100,timingHint:'Daily close'});
 expect(live.assets).toEqual(metadata.assets);expect(live.savedConditionsRevision).toBe(7);
 const late=mergeLiveAlertsMetadata(live,{...metadata,savedConditionsRevision:8,checks:[{...row,nextCheck:200,timingHint:'New daily close'}]});
 expect(late.running).toBe(true);expect(late.checks[0]).toMatchObject({phase:'Enriching indicators',progress:40,nextCheck:200,timingHint:'New daily close'});
 expect(late.savedConditionsRevision).toBe(8);
 const paused={...live,config:{enabled:true,alerts:[{id:'a',name:'Rule',conditionId:'saved',period:'d1' as const,dataSource:'hyperliquid' as const,enabled:false}]}};
 expect(mergeLiveAlertsMetadata(paused,metadata).checks[0].nextCheck).toBe(0);
 expect(mergeLiveAlertsProgress(late,{...compact,config:{enabled:false}}).checks[0].nextCheck).toBe(0);
});

it('renders successful legacy outcomes with no message without suppressing other error types',()=>{
 const outcome:LiveAlertOutcome={assetName:'Bitcoin',symbol:'btcusd',timeframe:'h1',time:1,conditionName:'Recorded 85',status:'sent'};
 expect(liveAlertOutcomePresentation(outcome)).toEqual({label:'Sent',message:'',className:'sent'});
 expect(liveAlertOutcomePresentation({...outcome,message:''})).toEqual({label:'Sent',message:'',className:'sent'});
 expect(liveAlertErrorText(undefined)).toBe('');expect(liveAlertErrorText(null)).toBe('');
 expect(liveAlertOutcomePresentation({...outcome,status:'failed',message:'Missing indicator sma_20'})).toEqual({label:'Failed',message:'Missing indicator sma_20',className:'failed'});
 expect(outcome).not.toHaveProperty('message');
});


it('presents immature assets as a history wait and clears an omitted zero count on the next check',()=>{
 const row:LiveAlertCheck={conditionId:'young',name:'Young markets',assetsScanned:1,assetsWarmingUp:2,lastChecked:10,nextCheck:100,sentAlerts:1,status:'completed',manual:false,error:''};
 expect(liveAlertCheckProgress(row)).toMatchObject({state:'completed',phase:'Completed; waiting for more history'});
 expect(visibleLiveAlertCheck(row)).toMatchObject({error:'',assetsWarmingUp:2,status:'completed'});
 expect(liveAlertCheckProgress({...row,error:'Real network error'}).phase).toBe('Completed with errors');
 const previous={...result({enabled:true}),checks:[row]};
 const ready={...row};delete ready.assetsWarmingUp;
 const current={...result({enabled:true}),checks:[{...ready,lastChecked:20,assetsScanned:3}]};
 expect(mergeLiveAlertsProgress(previous,current).checks[0].assetsWarmingUp).toBe(0);
 expect(mergeLiveAlertsMetadata(current,previous).checks[0].assetsWarmingUp).toBe(0);
 expect(row.assetsWarmingUp).toBe(2);
});


it('renders per-check confirmed signals independently of legacy lifetime totals',()=>{
 const legacy:LiveAlertCheck={conditionId:'old',name:'Legacy',assetsScanned:1,lastChecked:10,nextCheck:100,sentAlerts:0,sentAlertsKnown:false,lifetimeSentAlerts:37,status:'completed',manual:true,error:''};
 expect(liveAlertSentCount(legacy)).toMatchObject({text:'—'});expect(liveAlertSentCount(legacy).title).toContain('Lifetime confirmed signals: 37');
 const running={...legacy,sentAlertsKnown:true,status:'running' as const};
 expect(liveAlertSentCount(running).text).toBe('0');
 expect(liveAlertSentCount({...running,sentAlerts:1,lifetimeSentAlerts:38}).text).toBe('1');
 expect(liveAlertSentCount({...running,sentAlerts:1}).title).toContain('at least one recipient');
 const metadata={...result({enabled:true}),checks:[{...running,sentAlerts:1,lifetimeSentAlerts:38}]};
 const compact={...result({enabled:true}),checks:[{...running,sentAlerts:0,lifetimeSentAlerts:undefined}]};
 expect(mergeLiveAlertsProgress(metadata,compact).checks[0]).toMatchObject({sentAlerts:0,lifetimeSentAlerts:0});
});

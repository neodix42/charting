'use strict';
const test=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs/promises');
const path=require('node:path');
const os=require('node:os');
const {EventEmitter}=require('node:events');
const api=require('dukascopy-node');
const {download}=require('./worker.cjs');
const {sourceQuotes}=require('./synthetic-quotes.cjs');
const {installProviderTransport,requestSDKHistory}=require('./provider-transport.cjs');
const {mergeProvenance,provenance}=require('./synthetic-worker.cjs');
const DAY=86400000,HOUR=3600000,start=Date.UTC(2024,0,1),sourceKey='a'.repeat(64);
const offlineAPI={...api,BufferFetcher:class{constructor(){assert.fail('SDK source constructed a public HTTP fetcher');}}};
const collect=async source=>{const rows=[];for await(const value of source)rows.push(value);return rows;};
const tickHeader='timestamp,askPrice,bidPrice,askVolume,bidVolume\n',dailyHeader='timestamp,open,high,low,close\n';

async function fixture(t,symbol='btcxau',to=start+DAY,key=sourceKey){
  const directory=await fs.mkdtemp(path.join(os.tmpdir(),'cryptodesk-sdk-worker-')),batch=path.join(directory,'batch');await fs.mkdir(batch);
  const request={symbol,timeframe:'d1',from:new Date(start).toISOString(),to:new Date(to).toISOString(),asOf:to,output:path.join(directory,'output.part'),resume:true,ownerPipe:true,
    syntheticCacheDirectory:path.join(directory,'quotes'),providerSession:{directory:batch,tokenCache:path.join(directory,'token.json'),rateLimit:true,sdk:{sourceKey:key}}};
  await fs.writeFile(request.output,'');const lines=new EventEmitter(),events=[];let handler;
  const original=global.fetch;global.fetch=()=>assert.fail('SDK worker attempted HTTP');
  const control=installProviderTransport(lines,request,event=>{
    events.push(event);
    queueMicrotask(async()=>{
      try{
        assert.equal(event.operation,'history','SDK history does not request an HTTP permit or response observation');
        assert.equal(Object.hasOwn(event,'output'),false,'only the Go owner chooses an export path');
        const value=await handler(event,events.length);
        if(value.error){lines.emit('line',JSON.stringify({type:'provider-reply',requestId:event.requestId,error:value.error}));return;}
        const filename=value.path??path.join(batch,`export-${events.length}.csv`);
        if(value.csv!==undefined)await fs.writeFile(filename,value.csv,{mode:0o600});
        lines.emit('line',JSON.stringify({type:'provider-reply',requestId:event.requestId,path:filename,rows:value.rows}));
      }catch(error){lines.emit('line',JSON.stringify({type:'provider-reply',requestId:event.requestId,error:error.message}));}
    });
  });
  let closed=false;const closeTransport=()=>{if(!closed){closed=true;control.close();global.fetch=original;}};
  t.after(async()=>{closeTransport();await fs.rm(directory,{recursive:true,force:true});});
  return {request,directory,batch,events,control,closeTransport,setHandler(value){handler=value;}};
}
function ticks(event){
  if(event.from!==start)return {csv:tickHeader,rows:0};
  const prices=event.symbol==='btcusd'?[10,20,30]:[2,4,5];
  return {rows:3,csv:tickHeader+prices.map((bid,i)=>`${start+i*100},${bid+1},${bid},1,2\n`).join('')};
}
function publicTicks(){
  return {...api,BufferFetcher:class extends api.BufferFetcher{constructor(config){super({...config,fetcherFn:async url=>{
    const parts=new URL(url).pathname.split('/').slice(-4).map(Number),hour=Date.UTC(parts[0],parts[1]-1,parts[2],parts[3]);
    if(hour%DAY)return Buffer.from(JSON.stringify({timestamp:hour,times:[],asks:[],bids:[],askVolumes:[],bidVolumes:[]}));
    const values=url.includes('/BTC-USD/')?[100,200,300]:[2,4,5],delta=values.map((value,index)=>index?value-values[index-1]:0);
    return Buffer.from(JSON.stringify({timestamp:hour,multiplier:1,ask:values[0]+1,bid:values[0],times:[0,100,100],asks:delta,bids:delta,askVolumes:[1,1,1],bidVolumes:[1,1,1]}));
  }});}}};
}
async function publicNativePartial(state){
  const publicAPI={...api,BufferFetcher:class extends api.BufferFetcher{constructor(config){super({...config,fetcherFn:async url=>{
    assert.match(url,/\/candles\/day\/BTC-USD\/BID\/202[456]$/);
    const year=Number(new URL(url).pathname.split('/').at(-1));
    if(year!==2024)throw Error('Public native download interrupted');
    return Buffer.from(JSON.stringify({timestamp:Date.UTC(year,0,1),shift:DAY,multiplier:1,open:100,high:120,low:90,close:110,
      times:[0],opens:[0],highs:[0],lows:[0],closes:[0],volumes:[2]}));
  }});}}};
  await assert.rejects(download({...state.request,providerSession:undefined},{api:publicAPI,pauseMs:0,emit:()=>{}}),/Public native download interrupted/);
  const checkpoint=await fs.readFile(path.join(state.directory,'checkpoint.json'));
  assert.equal(JSON.parse(checkpoint).rows,1);assert.equal(JSON.parse(checkpoint).nextIndex,1);
  return checkpoint;
}

test('real owner pipe SDK ticks build exact BTC/XAU ratios despite an earlier HTTP denial and reuse private cache',async t=>{
  const state=await fixture(t);state.setHandler(ticks);
  await fs.writeFile(path.join(state.batch,'blocked.json'),JSON.stringify({message:'Dukascopy HTTP 403 earlier ETH-USD request'}));
  let result;await download(state.request,{api:offlineAPI,emit:event=>{if(event.type==='done')result=event;}});
  assert.equal(result.provenance.historySource,'jforex-sdk');assert.equal(result.provenance.volume,'unavailable');assert.equal(result.provenance.approximate,true);
  assert.equal(await fs.readFile(state.request.output,'utf8'),`timestamp,open,high,low,close\n${start},5,6,5,6\n`);
  assert.equal(state.events.length,48);assert.ok(state.events.every(event=>['btcusd','xauusd'].includes(event.symbol)&&event.timeframe==='tick'));
  assert.equal((await fs.readdir(state.batch)).some(name=>name.startsWith('export-')),false,'consumed owner exports are removed');
  assert.ok((await fs.readdir(state.request.syntheticCacheDirectory)).includes(`jforex-${sourceKey}`));
  const next={...state.request,output:path.join(state.directory,'cached.part'),resume:false};await fs.writeFile(next.output,'');
  state.setHandler(()=>assert.fail('completed SDK source was fetched twice'));
  await download(next,{api:offlineAPI,emit:()=>{}});assert.equal(state.events.length,48);
});
test('SDK cache and checkpoints are isolated from a different source identity',async t=>{
  const state=await fixture(t);state.setHandler(ticks);
  await download(state.request,{api:offlineAPI,emit:()=>{}});const before=await fs.readFile(state.request.output);
  state.request.providerSession.sdk.sourceKey='b'.repeat(64);
  await assert.rejects(download(state.request,{api:offlineAPI,emit:()=>{}}),/checkpoint/);assert.deepEqual(await fs.readFile(state.request.output),before);
  await assert.rejects(collect(sourceQuotes('btcusd',start,start+HOUR,state.request,{api:offlineAPI})),/source identity changed/);assert.equal(state.events.length,48);
  state.closeTransport();const other=await fixture(t,'btcxau',start+DAY,'b'.repeat(64));other.request.syntheticCacheDirectory=state.request.syntheticCacheDirectory;other.setHandler(ticks);
  const values=await collect(sourceQuotes('btcusd',start,start+HOUR,other.request,{api:offlineAPI}));
  assert.equal(values.length,3);assert.equal(other.events.length,1);assert.ok((await fs.readdir(state.request.syntheticCacheDirectory)).includes('jforex-'+'b'.repeat(64)));
});
test('native BTCUSD SDK candles use closed UTC dates, no volume and an explicit SDK source',async t=>{
  const state=await fixture(t,'btcusd',start+2*DAY+HOUR);
  state.setHandler(event=>{
    assert.equal(event.symbol,'btcusd');assert.equal(event.timeframe,'d1');assert.equal(event.to,start+2*DAY);
    return {rows:2,csv:dailyHeader+`${start},10,12,9,11\n${start+DAY},11,13,10,12\n`};
  });
  let result;await download(state.request,{api:offlineAPI,emit:event=>{if(event.type==='done')result=event;}});
  assert.equal(result.rows,2);assert.equal(result.checkedThrough,start+2*DAY);assert.equal(result.provenance.kind,'native');assert.equal(result.provenance.historySource,'jforex-sdk');assert.equal(result.provenance.volume,'unavailable');
  assert.equal((await fs.readFile(state.request.output,'utf8')).split('\n')[0],'timestamp,open,high,low,close');
  assert.equal(state.events.length,1,'available SDK daily bars are preferred without any tick request');
});
test('ordinary stock, forex and metal SDK daily histories bypass a saved HTTP denial and report native source provenance',async t=>{
  for(const symbol of ['0700hkhkd','eurusd','xauusd']){
    const state=await fixture(t,symbol,start+2*DAY+HOUR),blocked=JSON.stringify({message:'Earlier public HTTP403; no HTTP request allowed'});
    await fs.writeFile(path.join(state.batch,'blocked.json'),blocked);
    state.setHandler(event=>{
      assert.equal(event.symbol,symbol);assert.equal(event.timeframe,'d1');assert.equal(event.to,start+2*DAY);
      return {rows:2,csv:dailyHeader+`${start},10,12,9,11\n${start+DAY},11,13,10,12\n`};
    });
    let done;await download(state.request,{api:offlineAPI,emit:event=>{if(event.type==='done')done=event;}});
    assert.equal(done.rows,2);assert.equal(done.provenance.kind,'native');assert.equal(done.provenance.numerator,symbol);assert.equal(done.provenance.historySource,'jforex-sdk');assert.equal(done.provenance.volume,'unavailable');
    assert.equal(state.events.length,1);assert.equal(await fs.readFile(path.join(state.batch,'blocked.json'),'utf8'),blocked);state.closeTransport();
  }
});
test('ordinary stock SDK refresh merges closed candles, replays price fields and preserves original public prices and lineage',async t=>{
  const state=await fixture(t,'0700hkhkd',start+3*DAY+HOUR),original=`timestamp,open,high,low,close,volume,sma_1\n${start},10,12,9,11,3,11\n${start+DAY},20,22,19,21,4,21\n`;
  await fs.writeFile(state.request.output,original);
  state.setHandler(event=>({rows:2,csv:dailyHeader+`${start+DAY},21,23,20,22\n${start+2*DAY},22,24,21,23\n`}));
  const output=path.join(state.directory,'ordinary-refresh.csv');
  const done=await require('./refresh-worker.cjs').refresh({...state.request,input:state.request.output,output,from:'2024-01-01'},{api:offlineAPI,now:start+3*DAY+HOUR,emit:()=>{}});
  assert.equal(done.provenance.kind,'native');assert.equal(done.provenance.historySource,'mixed');assert.equal(done.provenance.volume,'unavailable');assert.equal(done.addedRows,1);assert.equal(done.updatedRows,1);
  assert.equal(await fs.readFile(output,'utf8'),`timestamp,open,high,low,close,sma_1\n${start},10,12,9,11,11\n${start+DAY},21,23,20,22,22\n${start+2*DAY},22,24,21,23,23\n`);
  assert.equal(await fs.readFile(state.request.output,'utf8'),original);assert.equal(state.events.length,1);
});
test('validated empty native BTCUSD daily history aggregates genuine cached BID ticks without another tick export',async t=>{
  const state=await fixture(t,'btcusd');state.setHandler(ticks);
  await collect(sourceQuotes('btcusd',start,start+DAY,state.request,{api:offlineAPI}));assert.equal(state.events.length,24);
  state.setHandler(event=>{assert.equal(event.timeframe,'d1','empty daily fallback must reuse the24cached native BTCUSD hours');return {rows:0,csv:dailyHeader};});
  let done;await download(state.request,{api:offlineAPI,emit:event=>{if(event.type==='done')done=event;}});
  assert.equal(state.events.length,25);assert.equal(done.rows,1);assert.equal(done.provenance.kind,'native');assert.equal(done.provenance.approximate,false);assert.equal(done.provenance.volume,'unavailable');assert.match(done.provenance.observationResolution,/derived from native BID ticks/);
  assert.equal(await fs.readFile(state.request.output,'utf8'),`timestamp,open,high,low,close\n${start},10,30,10,30\n`);
  const checkpoint=JSON.parse(await fs.readFile(path.join(state.directory,'sdk-checkpoint.json'),'utf8'));assert.equal(checkpoint.tickFallback,true);assert.equal(checkpoint.next,start+DAY);assert.equal(checkpoint.fallbackUntil,undefined);
});
test('empty native daily and tick histories produce no candle and invalid or failed daily exports never trigger tick fallback',async t=>{
  const state=await fixture(t,'btcusd');state.setHandler(event=>({rows:0,csv:event.timeframe==='d1'?dailyHeader:tickHeader}));
  await assert.rejects(download(state.request,{api:offlineAPI,emit:()=>{}}),/returned no rows/);
  assert.equal(state.events.length,25);assert.equal(await fs.readFile(state.request.output,'utf8'),'');
  const checkpoint=JSON.parse(await fs.readFile(path.join(state.directory,'sdk-checkpoint.json'),'utf8'));assert.equal(checkpoint.rows,0);assert.equal(checkpoint.next,start+DAY);assert.equal(checkpoint.tickFallback,false);
  state.closeTransport();const malformed=await fixture(t,'btcusd');malformed.setHandler(()=>({rows:0,csv:'invalid,columns\n'}));
  await assert.rejects(download(malformed.request,{api:offlineAPI,emit:()=>{}}),/changed its exported history columns/);assert.equal(malformed.events.length,1);
  malformed.closeTransport();const failed=await fixture(t,'btcusd');failed.setHandler(()=>({error:'Native SDK history denied'}));
  await assert.rejects(download(failed.request,{api:offlineAPI,emit:()=>{}}),/Native SDK history denied/);assert.equal(failed.events.length,1);
});
test('native tick-derived fallback checkpoints complete days and resumes only the unfinished cached day after cancellation',async t=>{
  const state=await fixture(t,'btcusd',start+2*DAY),controller=new AbortController();let stop=true;
  state.setHandler(event=>{
    if(event.timeframe==='d1')return {rows:0,csv:dailyHeader};
    const day=Math.floor((event.from-start)/DAY),hour=(event.from-start-day*DAY)/HOUR,base=100+day*100+hour;
    return {rows:4,csv:tickHeader+[base,base+3,base-2,base+1].map((bid,index)=>`${event.from+index*100},${bid+1},${bid},1,2\n`).join('')};
  });
  const options={api:offlineAPI,signal:controller.signal,emit:()=>{},onChunk:event=>{if(stop&&event.hour>=start+DAY+HOUR)controller.abort(Error('Native tick fallback canceled'));}};
  await assert.rejects(download(state.request,options),/Native tick fallback canceled/);assert.equal(state.events.length,27);
  const checkpoint=JSON.parse(await fs.readFile(path.join(state.directory,'sdk-checkpoint.json'),'utf8'));assert.equal(checkpoint.next,start+DAY);assert.equal(checkpoint.fallbackUntil,start+2*DAY);assert.equal(checkpoint.tickFallback,true);assert.equal(checkpoint.rows,1);
  assert.equal(await fs.readFile(state.request.output,'utf8'),`timestamp,open,high,low,close\n${start},100,126,98,124\n`);
  stop=false;options.signal=new AbortController().signal;const before=state.events.length;await download(state.request,options);
  assert.equal(state.events.length-before,22,'Resume reuses the two already completed hours of the unfinished UTC day');
  assert.ok(state.events.slice(before).every(event=>event.timeframe==='tick'&&event.from>=start+DAY+2*HOUR),'the successful empty daily response and committed first day are not fetched again');
  assert.equal(await fs.readFile(state.request.output,'utf8'),`timestamp,open,high,low,close\n${start},100,126,98,124\n${start+DAY},200,226,198,224\n`);
});
test('native SDK exports are bounded to 366 days and resume only uncommitted ranges',async t=>{
  const state=await fixture(t,'btcusd',start+800*DAY);let fail=true;
  state.setHandler(event=>{
    assert.ok(event.to-event.from<=366*DAY);
    if(event.from>start&&fail)return {error:'Explicit SDK export failure'};
    return {rows:1,csv:dailyHeader+`${event.from},10,12,9,11\n`};
  });
  await assert.rejects(download(state.request,{api:offlineAPI,emit:()=>{}}),/SDK export failure/);
  assert.equal(state.events.length,2);assert.equal(state.events[0].from,start);await fs.appendFile(state.request.output,'uncommitted tail');
  fail=false;let result;await download(state.request,{api:offlineAPI,emit:event=>{if(event.type==='done')result=event;}});
  assert.equal(result.rows,3);assert.equal(state.events.length,4);assert.equal(state.events[2].from,start+366*DAY);
  assert.equal((await fs.readFile(state.request.output,'utf8')).includes('uncommitted'),false);
});
test('SDK history errors fail closed and are not reclassified as public HTTP denial observations',async t=>{
  const state=await fixture(t);state.setHandler(()=>({error:'SDK archive HTTP 403 test'}));
  let failure;try{await download(state.request,{api:offlineAPI,emit:()=>{}});}catch(error){failure=error;}
  assert.match(failure.message,/SDK archive HTTP 403/);assert.equal(failure.code,'JFOREX_HISTORY');
  assert.equal(await state.control.failure(failure),failure);assert.equal(state.events.length,1);
  assert.equal(await fs.readFile(state.request.output,'utf8'),'');
});
test('SDK owner replies cannot point outside the owned batch folder',async t=>{
  const state=await fixture(t),outside=path.join(state.directory,'outside.csv');await fs.writeFile(outside,tickHeader);
  state.setHandler(()=>({path:outside,rows:0}));
  await assert.rejects(requestSDKHistory(state.request,{symbol:'btcusd',timeframe:'tick',from:start,to:start+HOUR}),/outside its owned folder/);
  assert.equal(await fs.readFile(outside,'utf8'),tickHeader);
});
test('SDK CSV ordering and row counts are validated before a reusable quote chunk is published',async t=>{
  const state=await fixture(t);
  state.setHandler(()=>({rows:2,csv:tickHeader+`${start+100},11,10,1,2\n${start},21,20,1,2\n`}));
  await assert.rejects(collect(sourceQuotes('btcusd',start,start+HOUR,state.request,{api:offlineAPI})),/unordered history/);
  const folder=path.join(state.request.syntheticCacheDirectory,`jforex-${sourceKey}`,'btcusd');
  assert.equal((await fs.readdir(folder)).some(name=>name.endsWith('.json')||name.endsWith('.lock')),false);
  assert.equal((await fs.readdir(state.batch)).some(name=>name.startsWith('export-')),false);
});
test('merging retained public and SDK histories reports mixed origins explicitly',()=>{
  const native=provenance('btcusd',false),sdk={...native,historySource:'jforex-sdk',volume:'unavailable'};
  const mixed=mergeProvenance('btcusd',[{value:native,from:start,to:start+DAY},{value:sdk,from:start+DAY,to:start+2*DAY}],false);
  assert.equal(mixed.historySource,'mixed');assert.equal(mixed.volume,'unavailable');
  const replaced=mergeProvenance('btcusd',[{value:native,from:start,to:start+DAY},{value:sdk,from:start,to:start+DAY}],false);
  assert.equal(replaced.historySource,'jforex-sdk');
});
test('native provenance retains accurate SDK daily and tick-derived observation resolutions through merges',()=>{
  const daily={...provenance('btcusd',false),historySource:'jforex-sdk',volume:'unavailable',observationResolution:'JForex SDK BID daily candles'};
  const ticks={...daily,observationResolution:'JForex SDK BID daily candles; empty periods derived from native BID ticks'};
  assert.equal(mergeProvenance('btcusd',[{value:daily,from:start,to:start+DAY}],false).observationResolution,daily.observationResolution);
  assert.equal(mergeProvenance('btcusd',[{value:ticks,from:start,to:start+DAY}],false).observationResolution,ticks.observationResolution);
  assert.equal(mergeProvenance('btcusd',[{value:daily,from:start,to:start+DAY},{value:ticks,from:start+DAY,to:start+2*DAY}],false).observationResolution,'provider native BID price observations');
});
test('switching a native BTCUSD file from public volume to SDK OHLC replays price enrichment and preserves the original',async t=>{
  const state=await fixture(t,'btcusd',start+2*DAY),original=`timestamp,open,high,low,close,volume,sma_1\n${start},10,12,9,10,3,10\n`;
  await fs.writeFile(state.request.output,original);
  state.setHandler(event=>({rows:2,csv:dailyHeader+`${event.from},11,12,10,11\n${event.from+DAY},12,13,11,12\n`}));
  const output=path.join(state.directory,'refreshed.csv'),result=await require('./refresh-worker.cjs').refresh({...state.request,input:state.request.output,output,from:'2024-01-01'},{api:offlineAPI,emit:()=>{}});
  assert.equal(result.provenance.historySource,'jforex-sdk');assert.equal(result.provenance.volume,'unavailable');
  assert.equal(await fs.readFile(output,'utf8'),`timestamp,open,high,low,close,sma_1\n${start},11,12,10,11,11\n${start+DAY},12,13,11,12,12\n`);
  assert.equal(await fs.readFile(state.request.output,'utf8'),original);
});
test('closing the SDK owner transport rejects pending history reads without HTTP requests',async t=>{
  const state=await fixture(t);state.setHandler(()=>new Promise(()=>{}));
  const pending=requestSDKHistory(state.request,{symbol:'btcusd',timeframe:'tick',from:start,to:start+HOUR});
  await new Promise(resolve=>setImmediate(resolve));state.control.close();
  await assert.rejects(pending,/owner pipe closed/);assert.equal(state.events.length,1);
});
test('a canceled public partial is archived byte-for-byte before an SDK retry starts from fresh ticks',async t=>{
  const state=await fixture(t,'btcxau',start+2*DAY),publicRequest={...state.request,providerSession:undefined};let stopped=false;
  await assert.rejects(download(publicRequest,{api:publicTicks(),emit:()=>{},onChunk:event=>{if(!stopped&&event.symbol==='btcusd'&&event.hour>=start+DAY+HOUR){stopped=true;throw Error('Public download canceled');}}}),/canceled/);
  await fs.appendFile(state.request.output,'uncommitted public tail');
  const before=await fs.readFile(state.request.output),checkpointFile=path.join(state.directory,'synthetic-checkpoint.json'),checkpoint=await fs.readFile(checkpointFile);
  state.setHandler(event=>{
    if(event.from%DAY)return {rows:0,csv:tickHeader};
    const prices=event.symbol==='btcusd'?[10,20,30]:[2,4,5];
    return {rows:3,csv:tickHeader+prices.map((bid,index)=>`${event.from+index*100},${bid+1},${bid},1,2\n`).join('')};
  });
  let result;await download(state.request,{api:offlineAPI,emit:event=>{if(event.type==='done')result=event;}});
  const names=(await fs.readdir(state.directory)).filter(name=>name.startsWith('.public-partial-')&&!name.endsWith('.pending'));assert.equal(names.length,1);
  const archive=path.join(state.directory,names[0]);assert.deepEqual(await fs.readFile(path.join(archive,'public.csv')),before);assert.deepEqual(await fs.readFile(path.join(archive,'synthetic-checkpoint.json')),checkpoint);
  assert.equal(result.rows,2);assert.equal(result.provenance.historySource,'jforex-sdk');
  assert.equal(await fs.readFile(state.request.output,'utf8'),`timestamp,open,high,low,close\n${start},5,6,5,6\n${start+DAY},5,6,5,6\n`);
});
test('an interrupted public-to-SDK reset resumes its durable journal without losing the archived partial',async t=>{
  const state=await fixture(t),publicRequest={...state.request,providerSession:undefined};await download(publicRequest,{api:publicTicks(),emit:()=>{}});
  const before=await fs.readFile(state.request.output);state.setHandler(ticks);
  await assert.rejects(download(state.request,{api:offlineAPI,emit:()=>{},onUpgradeStage:phase=>{if(phase==='after-reset')throw Error('Interrupted upgrade reset');}}),/Interrupted upgrade reset/);
  assert.equal(state.events.length,0);assert.equal((await fs.stat(state.request.output)).size,0);
  const journal=JSON.parse(await fs.readFile(path.join(state.directory,'public-sdk-upgrade.json'),'utf8'));
  assert.deepEqual(await fs.readFile(path.join(state.directory,journal.archive,'public.csv')),before);
  await download(state.request,{api:offlineAPI,emit:()=>{}});
  assert.equal(await fs.readFile(state.request.output,'utf8'),`timestamp,open,high,low,close\n${start},5,6,5,6\n`);
  assert.deepEqual(await fs.readFile(path.join(state.directory,journal.archive,'public.csv')),before);
  await assert.rejects(fs.stat(path.join(state.directory,'public-sdk-upgrade.json')),{code:'ENOENT'});
});
test('an invalid public partial is preserved and rejected before archival or SDK requests',async t=>{
  const state=await fixture(t),publicRequest={...state.request,providerSession:undefined};await download(publicRequest,{api:publicTicks(),emit:()=>{}});
  const invalid=(await fs.readFile(state.request.output,'utf8')).replace('timestamp','timeSTAMP?');await fs.writeFile(state.request.output,invalid);
  state.setHandler(()=>assert.fail('invalid public partial reached SDK'));
  await assert.rejects(download(state.request,{api:offlineAPI,emit:()=>{}}),/original|Refresh requires/);
  assert.equal(await fs.readFile(state.request.output,'utf8'),invalid);assert.equal(state.events.length,0);
  assert.equal((await fs.readdir(state.directory)).some(name=>name.startsWith('.public-partial-')),false);
});
test('a native public partial survives the first SDK failure in a durable archive and resumes from its separate SDK checkpoint',async t=>{
  const state=await fixture(t,'btcusd',start+800*DAY),checkpoint=await publicNativePartial(state);
  await fs.appendFile(state.request.output,'uncommitted public tail');const before=await fs.readFile(state.request.output);
  state.setHandler(()=>({error:'First native SDK export failed'}));
  await assert.rejects(download(state.request,{api:offlineAPI,emit:()=>{}}),/First native SDK export failed/);
  const archives=(await fs.readdir(state.directory)).filter(name=>/^\.public-partial-[a-f0-9-]{36}$/.test(name));assert.equal(archives.length,1);
  const archive=path.join(state.directory,archives[0]);
  assert.deepEqual(await fs.readFile(path.join(archive,'public.csv')),before);assert.deepEqual(await fs.readFile(path.join(archive,'checkpoint.json')),checkpoint);
  const saved=JSON.parse(await fs.readFile(path.join(state.directory,'sdk-checkpoint.json'),'utf8'));assert.equal(saved.rows,0);assert.equal(saved.bytes,0);assert.equal(saved.next,start);
  assert.equal((await fs.stat(state.request.output)).size,0);await assert.rejects(fs.stat(path.join(state.directory,'checkpoint.json')),{code:'ENOENT'});
  state.request.providerSession.sdk.sourceKey='b'.repeat(64);
  await assert.rejects(download(state.request,{api:offlineAPI,emit:()=>{}}),/another source/);assert.equal(state.events.length,1);
  state.request.providerSession.sdk.sourceKey=sourceKey;
  state.setHandler(event=>({rows:1,csv:dailyHeader+`${event.from},10,12,9,11\n`}));
  let result;await download(state.request,{api:offlineAPI,emit:event=>{if(event.type==='done')result=event;}});
  assert.equal(result.rows,3);assert.equal(result.provenance.historySource,'jforex-sdk');assert.equal(result.provenance.volume,'unavailable');
  assert.equal((await fs.readFile(state.request.output,'utf8')).includes('100,120,90,110'),false);
  assert.deepEqual(await fs.readFile(path.join(archive,'public.csv')),before);assert.equal((await fs.readdir(state.directory)).filter(name=>/^\.public-partial-[a-f0-9-]{36}$/.test(name)).length,1);
});
test('native public-to-SDK upgrades recover durable journals before and after resetting the output',async t=>{
  for(const phase of ['after-archive','after-reset']){
    const state=await fixture(t,'btcusd',start+800*DAY),checkpoint=await publicNativePartial(state),before=await fs.readFile(state.request.output);
    state.setHandler(event=>({rows:1,csv:dailyHeader+`${event.from},10,12,9,11\n`}));
    await assert.rejects(download(state.request,{api:offlineAPI,emit:()=>{},onUpgradeStage:actual=>{if(actual===phase)throw Error(`Interrupted native ${phase}`);}}),/Interrupted native/);
    assert.equal(state.events.length,0);const journal=JSON.parse(await fs.readFile(path.join(state.directory,'public-sdk-upgrade.json'),'utf8'));
    assert.equal(journal.kind,'native');assert.equal(journal.checkpointName,'checkpoint.json');
    const archive=path.join(state.directory,journal.archive);assert.deepEqual(await fs.readFile(path.join(archive,'public.csv')),before);assert.deepEqual(await fs.readFile(path.join(archive,'checkpoint.json')),checkpoint);
    if(phase==='after-reset')await fs.unlink(path.join(state.directory,'checkpoint.json'));
    await download(state.request,{api:offlineAPI,emit:()=>{}});
    assert.equal(state.events.length,3);assert.deepEqual(await fs.readFile(path.join(archive,'public.csv')),before);
    await assert.rejects(fs.stat(path.join(state.directory,'public-sdk-upgrade.json')),{code:'ENOENT'});
    state.closeTransport();
  }
});
test('unexplained and invalid native public partials remain untouched before any SDK request',async t=>{
  const state=await fixture(t,'btcusd',start+800*DAY);state.setHandler(()=>assert.fail('unverified partial reached SDK'));
  await fs.writeFile(state.request.output,'uncheckpointed public bytes');
  await assert.rejects(download(state.request,{api:offlineAPI,emit:()=>{}}),/uncheckpointed partial is preserved/);
  assert.equal(await fs.readFile(state.request.output,'utf8'),'uncheckpointed public bytes');assert.equal(state.events.length,0);
  await fs.writeFile(state.request.output,'');await publicNativePartial(state);
  const checkpoint=await fs.readFile(path.join(state.directory,'checkpoint.json')),invalid=(await fs.readFile(state.request.output,'utf8')).replace('timestamp','invalidHeader');await fs.writeFile(state.request.output,invalid);
  await assert.rejects(download(state.request,{api:offlineAPI,emit:()=>{}}),/original|Refresh requires/);
  assert.equal(await fs.readFile(state.request.output,'utf8'),invalid);assert.deepEqual(await fs.readFile(path.join(state.directory,'checkpoint.json')),checkpoint);assert.equal(state.events.length,0);
  assert.equal((await fs.readdir(state.directory)).some(name=>name.startsWith('.public-partial-')),false);
});
test('a legacy native XAUXAG partial is preserved when the requested range now selects calculated history',async t=>{
  const state=await fixture(t,'xauxag'),original=`timestamp,open,high,low,close,volume\n${Date.UTC(2024,8,17)},80,82,79,81,2\n`,checkpoint='{"version":1,"symbol":"xauxag","rows":1}';
  await fs.writeFile(state.request.output,original);await fs.writeFile(path.join(state.directory,'checkpoint.json'),checkpoint);
  state.setHandler(()=>assert.fail('legacy native partial reached calculated SDK history'));
  await assert.rejects(download(state.request,{api:offlineAPI,emit:()=>{}}),/native XAUXAG partial is preserved.*click Download again instead of Resume/);
  assert.equal(await fs.readFile(state.request.output,'utf8'),original);assert.equal(await fs.readFile(path.join(state.directory,'checkpoint.json'),'utf8'),checkpoint);assert.equal(state.events.length,0);
});
test('one typed SDK loading timeout retries only the same complete hour and then caches its validated quotes',async t=>{
  const state=await fixture(t),retries=[];let calls=0;
  state.setHandler(event=>++calls===1?{error:'JForex tick history loading timed out. Completed quote hours are retained; retry to resume.'}:ticks(event));
  const values=await collect(sourceQuotes('btcusd',start,start+HOUR,state.request,{api:offlineAPI,sdkRetryDelayMs:0,onRetry:event=>retries.push(event)}));
  assert.equal(values.length,3);assert.equal(calls,2);assert.equal(retries.length,1);assert.equal(retries[0].delayMs,5000);
  assert.deepEqual(state.events.map(({symbol,timeframe,from,to})=>({symbol,timeframe,from,to})),Array(2).fill({symbol:'btcusd',timeframe:'tick',from:start,to:start+HOUR}));
  await collect(sourceQuotes('btcusd',start,start+HOUR,state.request,{api:offlineAPI}));assert.equal(calls,2);
});
test('one typed native daily SDK timeout retries only its bounded UTC range and preserves checkpoint resume',async t=>{
  const state=await fixture(t,'btcusd',start+2*DAY),retries=[];let calls=0;
  state.setHandler(event=>++calls===1?{error:'JForex native daily download timed out. Completed history is retained; retry to resume.'}
    :{rows:2,csv:dailyHeader+`${event.from},10,12,9,11\n${event.from+DAY},11,13,10,12\n`});
  await download(state.request,{api:offlineAPI,emit:()=>{},sdkRetryDelayMs:0,onRetry:event=>retries.push(event)});
  assert.equal(calls,2);assert.equal(retries.length,1);assert.equal(retries[0].timeframe,'d1');assert.equal(retries[0].delayMs,5000);
  assert.deepEqual(state.events.map(({symbol,timeframe,from,to})=>({symbol,timeframe,from,to})),Array(2).fill({symbol:'btcusd',timeframe:'d1',from:start,to:start+2*DAY}));
  await download(state.request,{api:offlineAPI,emit:()=>{}});assert.equal(calls,2,'committed daily range does not request another export');
});
test('native daily SDK retry is bounded and ignores chart timeouts, access, cancellation and malformed exports',async t=>{
  const state=await fixture(t,'btcusd');
  state.setHandler(()=>({error:'JForex native daily download timed out. Completed history is retained; retry to resume.'}));
  await assert.rejects(download(state.request,{api:offlineAPI,emit:()=>{},sdkRetryDelayMs:0}),/native daily download timed out/);assert.equal(state.events.length,2);
  for(const message of ['SDK account source changed','SDK export canceled','SDK HTTP 403','SDK HTTP 429','JForex historical data loading timed out. Live quotes can continue; retry the chart to reload history.']){
    const before=state.events.length;state.setHandler(()=>({error:message}));
    await assert.rejects(download(state.request,{api:offlineAPI,emit:()=>{},sdkRetryDelayMs:0}),error=>error.message===message);assert.equal(state.events.length,before+1);
  }
  const before=state.events.length;state.setHandler(()=>({rows:1,csv:dailyHeader+`${start},10,12,9,NaN\n`}));
  await assert.rejects(download(state.request,{api:offlineAPI,emit:()=>{},sdkRetryDelayMs:0}),/invalid/);assert.equal(state.events.length,before+1);
});
test('persistent tick timeouts stop after one retry and native SDK daily history remains independent of a public denial',async t=>{
  const state=await fixture(t);state.setHandler(event=>event.timeframe==='tick'
    ?{error:'JForex tick history loading timed out. Completed quote hours are retained; retry to resume.'}
    :{rows:1,csv:dailyHeader+`${start},10,12,9,11\n`});
  const blocked=JSON.stringify({message:'Earlier HTTP 403 ETH-USD'});await fs.writeFile(path.join(state.batch,'blocked.json'),blocked);
  await assert.rejects(collect(sourceQuotes('btcusd',start,start+HOUR,state.request,{api:offlineAPI,sdkRetryDelayMs:0})),/loading timed out/);assert.equal(state.events.length,2);
  const folder=path.join(state.request.syntheticCacheDirectory,`jforex-${sourceKey}`,'btcusd');assert.equal((await fs.readdir(folder)).some(name=>name.endsWith('.json')),false);
  const output=path.join(state.directory,'native-after-timeout.csv');await fs.writeFile(output,'');
  await download({...state.request,symbol:'btcusd',output},{api:offlineAPI,emit:()=>{}});
  assert.equal(state.events.length,3);assert.equal(state.events[2].timeframe,'d1');assert.equal(await fs.readFile(path.join(state.batch,'blocked.json'),'utf8'),blocked);
});
test('SDK source, cancellation, access and rate errors never trigger the timeout retry',async t=>{
  const state=await fixture(t);
  for(const message of ['SDK account source changed','SDK export canceled','SDK HTTP 403','SDK HTTP 429','JForex returned invalid historical tick values']){
    const before=state.events.length;state.setHandler(()=>({error:message}));
    await assert.rejects(collect(sourceQuotes('btcusd',start,start+HOUR,state.request,{api:offlineAPI,sdkRetryDelayMs:0})),error=>error.message===message);
    assert.equal(state.events.length,before+1);
  }
});
test('cancellation during the bounded timeout retry delay prevents a second SDK request',async t=>{
  const state=await fixture(t),controller=new AbortController();state.setHandler(()=>({error:'JForex tick history subscription timed out'}));
  await assert.rejects(collect(sourceQuotes('btcusd',start,start+HOUR,state.request,{api:offlineAPI,signal:controller.signal,onRetry:()=>controller.abort()})),/abort/i);
  assert.equal(state.events.length,1);
});

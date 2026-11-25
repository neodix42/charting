'use strict';
const test=require('node:test'),assert=require('node:assert/strict');
const fs=require('node:fs/promises'),path=require('node:path'),os=require('node:os');
const {createHash}=require('node:crypto');
const {hourlyBars,aggregateHourlyRatio,aggregateNativeHourly,HOUR,DAY}=require('./synthetic-hourly.cjs');
const START=Date.UTC(2024,0,1),KEY='a'.repeat(64);
async function* rows(values){yield* values;}
const candle=(hour,close)=>[START+hour*HOUR,close,close+100,Math.max(close/10,0.01),close];
const collect=async input=>{const result=[];for await(const row of input)result.push(row);return result;};
async function fixture(t,days=60){
  const dir=await fs.mkdtemp(path.join(os.tmpdir(),'synthetic-hourly-'));t.after(()=>fs.rm(dir,{recursive:true,force:true}));
  const output=path.join(dir,'data.part');await fs.writeFile(output,'');
  const request={symbol:'btcxau',timeframe:'d1',syntheticSource:'h1-close',from:new Date(START).toISOString(),to:new Date(START+days*DAY).toISOString(),asOf:START+days*DAY,output,
    providerSession:{directory:dir,tokenCache:path.join(dir,'waf.json'),sdk:{sourceKey:KEY}},hourlyCacheDirectory:path.join(dir,'hourly')};
  const api={instrumentMetaData:Object.fromEntries(['btcusd','xauusd','ethusd','xagusd','xauxag','usa500idxusd'].map(symbol=>[symbol,{startMonthForHourlyCandles:new Date(START).toISOString(),startHourForTicks:new Date(START).toISOString(),startYearForDailyCandles:new Date(START).toISOString()}]))};
  let serial=0;const requests=[];
  const sdkHistory=async(_request,fields)=>{
    assert.ok(['h1','h1-native'].includes(fields.timeframe),'fast history reached tick download');requests.push(fields);
    assert.ok(fields.to-fields.from<=1000*HOUR);
    const values=[];for(let hour=fields.from;hour<fields.to;hour+=HOUR){const close=fields.symbol==='btcusd'?10+(hour-START)/HOUR:2;values.push([hour,close,close+1,close/2,close]);}
    const file=path.join(dir,`export-${serial++}.csv`);await fs.writeFile(file,'timestamp,open,high,low,close\n'+values.map(row=>row.join(',')+'\n').join(''),{mode:0o600});
    return{path:file,rows:values.length,sessionPolicy:fields.timeframe==='h1'?'verified-full-hour-v2':'provider-native-h1-v2'};
  };
  return{dir,request,api,requests,sdkHistory};
}
async function legacyDay(f,{native=false,day=START,values=[candle(0,42)],identityFields={},checksum}={}){
  const timeframe=native?'h1-native':'h1',symbol='btcusd',policy=native?'provider-native-h1-v1':'verified-full-hour-v1';
  const folder=path.join(f.request.hourlyCacheDirectory,KEY,timeframe,symbol);await fs.mkdir(folder,{recursive:true});
  const file=`${day}-11111111-1111-4111-8111-111111111111.csv`,csv=path.join(folder,file),manifest=path.join(folder,`${day}.json`);
  const text='timestamp,open,high,low,close\n'+values.map(row=>row.join(',')+'\n').join('');
  const identity=createHash('sha256').update(JSON.stringify({sourceKey:KEY,symbol,timeframe,policy,available:START,...identityFields})).digest('hex');
  const metadata=JSON.stringify({version:1,day,identity,file,rows:values.length,bytes:Buffer.byteLength(text),checksum:checksum??createHash('sha256').update(text).digest('hex')});
  await fs.writeFile(csv,text,{mode:0o600});await fs.writeFile(manifest,metadata,{mode:0o600});
  return{csv,manifest,text,metadata,identity};
}

test('hourly ratio OHLC uses paired observed closes; independent source extrema and unmatched hours never enter it',async()=>{
  const numerator=rows([candle(0,10),candle(1,999),candle(2,9),candle(3,12),candle(24,20)]);
  const denominator=rows([candle(0,2),candle(2,3),candle(3,2),candle(4,0.1),candle(24,4)]);
  assert.deepEqual(await collect(aggregateHourlyRatio(numerator,denominator,{verifiedSessions:true})),[
    {timestamp:START,open:5,high:6,low:3,close:6},
    {timestamp:START+DAY,open:5,high:5,low:5,close:5},
  ]);
});
test('hourly ratios reject unverified sessions, invalid/zero prices, duplicates and unordered inputs',async()=>{
  await assert.rejects(()=>collect(aggregateHourlyRatio(rows([]),rows([]))),/verified/);
  for(const bad of [[candle(0,2),candle(0,3)],[candle(1,2),candle(0,3)],[[START,0,1,0,0]],[[START,1,2,0.1,NaN]]]){
    await assert.rejects(()=>collect(aggregateHourlyRatio(rows([candle(0,10),candle(1,12)]),rows(bad),{verifiedSessions:true})),/Invalid|unordered/);
  }
  assert.deepEqual(await collect(aggregateHourlyRatio(rows([candle(0,10)]),rows([candle(24,2)]),{verifiedSessions:true})),[]);
});
test('same-asset hourly OHLC aggregates native extrema and retains empty session/day gaps',async()=>{
  assert.deepEqual(await collect(aggregateNativeHourly(rows([[START,10,20,8,12],[START+HOUR,12,30,5,25],[START+3*DAY,40,45,35,42]]))),[
    {timestamp:START,open:10,high:30,low:5,close:25},{timestamp:START+3*DAY,open:40,high:45,low:35,close:42},
  ]);
});
test('sixty days are fetched in three bounded source batches; compatible daily cache and another ratio reuse them',async t=>{
  const f=await fixture(t);
  const options={api:f.api,sdkHistory:f.sdkHistory,historyRange:{from:START,to:START+60*DAY}};
  // The first requested day prefetches only a bounded28-day batch, not60.
  assert.equal((await collect(hourlyBars('btcusd',START,START+DAY,f.request,options))).length,24);
  assert.equal(f.requests.length,1);assert.equal(f.requests[0].to,START+28*DAY);
  assert.equal((await collect(hourlyBars('btcusd',START,START+60*DAY,f.request,options))).length,60*24);
  assert.equal(f.requests.length,3);
  const forbid=async()=>{throw Error('Cached hourly inputs were downloaded again');};
  assert.equal((await collect(hourlyBars('btcusd',START,START+60*DAY,{...f.request,symbol:'ethbtc'},{...options,sdkHistory:forbid}))).length,60*24);
  assert.ok(f.requests.every(r=>r.timeframe==='h1'&&r.to-r.from<=28*DAY));
  assert.equal((await fs.readdir(f.dir)).filter(n=>n.startsWith('export-')).length,0);
});
test('validated nonempty legacy hourly days retain exact bytes and avoid SDK requests for both session policies',async t=>{
  const f=await fixture(t,1),forbid=async()=>{throw Error('Valid legacy observations were fetched again');};
  for(const native of [false,true]){
    const old=await legacyDay(f,{native});
    assert.deepEqual(await collect(hourlyBars('btcusd',START,START+DAY,f.request,{api:f.api,sdkHistory:forbid,native})),[candle(0,42)]);
    assert.equal(await fs.readFile(old.manifest,'utf8'),old.metadata);
    assert.equal(await fs.readFile(old.csv,'utf8'),old.text);
  }
});
test('legacy empty hourly days are refetched under the v2 policy and published without deleting old observations',async t=>{
  const f=await fixture(t,1);
  for(const native of [false,true]){
    const old=await legacyDay(f,{native,values:[]});
    const sdkHistory=async(request,fields)=>{
      assert.equal(await fs.readFile(old.manifest,'utf8'),old.metadata,'recovery replaced metadata before validating its source');
      return f.sdkHistory(request,fields);
    };
    assert.equal((await collect(hourlyBars('btcusd',START,START+DAY,f.request,{api:f.api,sdkHistory,native}))).length,24);
    const fresh=JSON.parse(await fs.readFile(old.manifest,'utf8'));
    assert.notEqual(fresh.identity,old.identity);assert.equal(fresh.rows,24);assert.notEqual(fresh.file,path.basename(old.csv));
    assert.equal(await fs.readFile(old.csv,'utf8'),old.text);
    const forbid=async()=>{throw Error('Repaired observations were fetched again');};
    assert.equal((await collect(hourlyBars('btcusd',START,START+DAY,f.request,{api:f.api,sdkHistory:forbid,native}))).length,24);
  }
  assert.equal(f.requests.length,2);
});
test('verified v2 empty days retain explicit gaps and are reusable without another SDK request',async t=>{
  const f=await fixture(t,1);let calls=0;
  const sdkHistory=async()=>{
    calls++;const file=path.join(f.dir,'verified-closure.csv');await fs.writeFile(file,'timestamp,open,high,low,close\n');
    return{path:file,rows:0,sessionPolicy:'verified-full-hour-v2'};
  };
  assert.deepEqual(await collect(hourlyBars('btcusd',START,START+DAY,f.request,{api:f.api,sdkHistory})),[]);
  assert.equal(calls,1);
  const forbid=async()=>{throw Error('A verified closed day was fetched again');};
  assert.deepEqual(await collect(hourlyBars('btcusd',START,START+DAY,f.request,{api:f.api,sdkHistory:forbid})),[]);
});
test('failed, canceled and legacy-policy recovery preserves old manifests and files, including invalid cache proofs',async t=>{
  for(const mode of ['failure','cancel','legacy-policy','checksum-failure','identity-failure']){
    const f=await fixture(t,1),old=await legacyDay(f,mode==='checksum-failure'?{checksum:'0'.repeat(64)}:mode==='identity-failure'?{identityFields:{sourceKey:'b'.repeat(64)}}:{values:[]}),controller=new AbortController();
    const sdkHistory=async(request,fields)=>{
      if(mode.endsWith('failure'))throw Error('source recovery failed');
      const result=await f.sdkHistory(request,fields);
      if(mode==='cancel')controller.abort(Error('recovery canceled'));
      if(mode==='legacy-policy')result.sessionPolicy='verified-full-hour-v1';
      return result;
    };
    await assert.rejects(()=>collect(hourlyBars('btcusd',START,START+DAY,f.request,{api:f.api,sdkHistory,signal:controller.signal})),/recovery failed|recovery canceled|session policy/);
    assert.equal(await fs.readFile(old.manifest,'utf8'),old.metadata);
    assert.equal(await fs.readFile(old.csv,'utf8'),old.text);
    assert.equal((await fs.readdir(f.dir)).filter(name=>name.startsWith('export-')).length,0);
  }
});
test('legacy observations require matching account, symbol, availability, session policy and checksum',async t=>{
  const mismatches=[{sourceKey:'b'.repeat(64)},{symbol:'ethusd'},{available:START-HOUR},{policy:'provider-native-h1-v1'},null];
  for(const identityFields of mismatches){
    const f=await fixture(t,1),old=await legacyDay(f,identityFields?{identityFields}:{checksum:'0'.repeat(64)});
    const result=await collect(hourlyBars('btcusd',START,START+DAY,f.request,{api:f.api,sdkHistory:f.sdkHistory}));
    assert.equal(f.requests.length,1);assert.equal(result.length,24);assert.equal(result[0][4],10);
    assert.equal(await fs.readFile(old.csv,'utf8'),old.text);
    assert.notEqual(await fs.readFile(old.manifest,'utf8'),old.metadata);
  }
});
test('legacy empty repairs span positive neighbors in bounded batches while preserving every valid source byte',async t=>{
  const f=await fixture(t,60),saved=[];
  for(let day=0;day<60;day++)saved.push(await legacyDay(f,{day:START+day*DAY,values:day%2?[[START+day*DAY,42,43,41,42]]:[]}));
  const result=await collect(hourlyBars('btcusd',START,START+60*DAY,f.request,{api:f.api,sdkHistory:f.sdkHistory}));
  assert.equal(result.length,30*24+30);
  assert.deepEqual(f.requests.map(r=>[r.from,r.to]),[[START,START+28*DAY],[START+28*DAY,START+56*DAY],[START+56*DAY,START+60*DAY]]);
  for(let day=0;day<saved.length;day++){
    const old=saved[day];assert.equal(await fs.readFile(old.csv,'utf8'),old.text);
    if(day%2)assert.equal(await fs.readFile(old.manifest,'utf8'),old.metadata);
    else assert.equal(JSON.parse(await fs.readFile(old.manifest,'utf8')).rows,24);
  }
  const forbid=async()=>{throw Error('Repaired mixed legacy cache was fetched again');};
  assert.deepEqual(await collect(hourlyBars('btcusd',START,START+60*DAY,f.request,{api:f.api,sdkHistory:forbid})),result);
});
test('mixed legacy recovery failure and cancellation preserve empty and positive neighbors',async t=>{
  for(const canceled of [false,true]){
    const f=await fixture(t,4),saved=[],controller=new AbortController();
    for(let day=0;day<4;day++)saved.push(await legacyDay(f,{day:START+day*DAY,values:day%2?[[START+day*DAY,42,43,41,42]]:[]}));
    const sdkHistory=async(request,fields)=>{
      assert.equal(fields.from,START);assert.equal(fields.to,START+4*DAY);
      if(!canceled)throw Error('mixed recovery failed');
      const result=await f.sdkHistory(request,fields);controller.abort(Error('mixed recovery canceled'));return result;
    };
    await assert.rejects(()=>collect(hourlyBars('btcusd',START,START+4*DAY,f.request,{api:f.api,sdkHistory,signal:controller.signal})),/mixed recovery failed|mixed recovery canceled/);
    for(const old of saved){assert.equal(await fs.readFile(old.manifest,'utf8'),old.metadata);assert.equal(await fs.readFile(old.csv,'utf8'),old.text);}
  }
});
test('legacy repair batches stop before a verified v2 day and reuse it unchanged',async t=>{
  const f=await fixture(t,10),options={api:f.api,sdkHistory:f.sdkHistory};
  await collect(hourlyBars('btcusd',START+5*DAY,START+6*DAY,f.request,options));
  const manifest=path.join(f.request.hourlyCacheDirectory,KEY,'h1','btcusd',`${START+5*DAY}.json`),metadata=await fs.readFile(manifest,'utf8');
  f.requests.length=0;
  for(let day=0;day<10;day++)if(day!==5)await legacyDay(f,{day:START+day*DAY,values:day%2?[[START+day*DAY,42,43,41,42]]:[]});
  await collect(hourlyBars('btcusd',START,START+10*DAY,f.request,options));
  assert.deepEqual(f.requests.map(r=>[r.from,r.to]),[[START,START+5*DAY],[START+6*DAY,START+10*DAY]]);
  assert.equal(await fs.readFile(manifest,'utf8'),metadata);
});
test('three Fast ratio workers overlap owner publication and share complete BTC/Gold caches without JSON failures',{timeout:5000},async t=>{
  const f=await fixture(t,1),{download}=require('./worker.cjs');
  const lock=path.join(f.request.hourlyCacheDirectory,KEY,'h1','btcusd','.fetch.lock'),originalWrite=fs.writeFile,originalStat=fs.lstat;
  let created,continueOwner,inspected,paused=false;
  const ownerCreated=new Promise(resolve=>{created=resolve;}),releaseOwner=new Promise(resolve=>{continueOwner=resolve;}),peerInspected=new Promise(resolve=>{inspected=resolve;});
  fs.writeFile=async function(filename,data,options){
    if(!paused&&path.dirname(filename)===lock&&(path.basename(filename)==='owner.json'||/^\.owner-.*\.pending$/.test(path.basename(filename)))){
      paused=true;await originalWrite.call(this,filename,'',options);created();await releaseOwner;
      return originalWrite.call(this,filename,data,{...options,flag:'w'});
    }
    return originalWrite.call(this,filename,data,options);
  };
  fs.lstat=async function(filename,...options){
    try{return await originalStat.call(this,filename,...options);}
    finally{if(filename===path.join(lock,'owner.json'))inspected();}
  };
  t.after(()=>{continueOwner();fs.writeFile=originalWrite;fs.lstat=originalStat;});
  const done=new Map(),jobs=[];
  for(const symbol of ['btcxau','ethbtc','xauspx']){
    const folder=path.join(f.dir,symbol);await fs.mkdir(folder);const output=path.join(folder,'data.part');await fs.writeFile(output,'');
    jobs.push(download({...f.request,symbol,output,resume:true},{api:f.api,sdkHistory:f.sdkHistory,emit:event=>{if(event.type==='done')done.set(symbol,event);}}));
  }
  const settled=Promise.allSettled(jobs);
  await ownerCreated;await peerInspected;continueOwner();
  for(const result of await settled)if(result.status==='rejected')assert.ifError(result.reason);
  assert.equal(done.size,3);
  for(const event of done.values()){assert.equal(event.rows,1);assert.equal(event.provenance.syntheticSource,'h1-close');}
  assert.deepEqual(f.requests.map(request=>request.symbol).sort(),['btcusd','ethusd','usa500idxusd','xauusd']);
  assert.ok(f.requests.every(request=>request.timeframe==='h1'));
  const cached=async()=>{throw Error('Completed shared input was downloaded again');};
  for(const symbol of ['btcusd','xauusd'])assert.equal((await collect(hourlyBars(symbol,START,START+DAY,f.request,{api:f.api,sdkHistory:cached}))).length,24);
});
test('hourly caches isolate account and native/ratio session policies, and reject unverified or malformed exports',async t=>{
  const f=await fixture(t,1),options={api:f.api,sdkHistory:f.sdkHistory};
  await collect(hourlyBars('btcusd',START,START+DAY,f.request,options));
  await collect(hourlyBars('btcusd',START,START+DAY,f.request,{...options,native:true}));
  await collect(hourlyBars('btcusd',START,START+DAY,{...f.request,providerSession:{...f.request.providerSession,sdk:{sourceKey:'b'.repeat(64)}}},options));
  assert.equal(f.requests.length,3);
  for(const bad of ['policy','timestamp','count']){
    const req={...f.request,hourlyCacheDirectory:path.join(f.dir,bad)};
    const sdkHistory=async(request,fields)=>{
      const result=await f.sdkHistory(request,fields);
      if(bad==='policy')result.sessionPolicy='provider-native-h1-v2';
      if(bad==='count')result.rows++;
      if(bad==='timestamp')await fs.appendFile(result.path,`${fields.to},1,2,0.5,1\n`);
      return result;
    };
    await assert.rejects(()=>collect(hourlyBars('btcusd',START,START+DAY,req,{...options,sdkHistory})),/policy|Invalid|row count/);
    const cache=path.join(req.hourlyCacheDirectory,KEY,'h1','btcusd');
    assert.equal((await fs.readdir(cache)).filter(n=>n.endsWith('.json')).length,0);
  }
});
test('fast synthetic worker saves D1, resumes committed days after a failed batch, and never requests ticks',async t=>{
  const f=await fixture(t,29),{download}=require('./worker.cjs');
  let fail=true,done;
  const sdkHistory=async(request,fields)=>{
    if(fail&&fields.from>=START+28*DAY)throw Error('bounded source failure');
    return f.sdkHistory(request,fields);
  };
  const req={...f.request,resume:true};
  await assert.rejects(()=>download(req,{api:f.api,sdkHistory,emit:()=>{}}),/source failure/);
  const checkpoint=JSON.parse(await fs.readFile(path.join(f.dir,'synthetic-checkpoint.json'),'utf8'));
  assert.ok(checkpoint.rows>0&&checkpoint.rows<29);assert.equal(checkpoint.provenance.maxQuoteAgeMs,HOUR);
  const before=f.requests.length;fail=false;
  await download(req,{api:f.api,sdkHistory,emit:e=>{if(e.type==='done')done=e;}});
  assert.equal(done.rows,29);assert.equal(done.provenance.volume,'unavailable');assert.equal(done.provenance.approximate,true);
  assert.match(done.provenance.observationResolution,/H1/);assert.match(done.message,/no ticks/);
  assert.ok(f.requests.slice(before).every(r=>r.from>=START+28*DAY));
  const lines=(await fs.readFile(req.output,'utf8')).trim().split('\n');assert.equal(lines.length,30);
  assert.equal(lines[1],`${START},5,16.5,5,16.5`);
});
test('fast BTCUSD keeps native daily preference and aggregates same-asset H1 OHLC only after valid empty D1',async t=>{
  const f=await fixture(t,1),{download}=require('./worker.cjs');let serial=0,empty=true,done;
  const sdkHistory=async(request,fields)=>{
    if(fields.timeframe!=='d1')return f.sdkHistory(request,fields);
    const filename=path.join(f.dir,`daily-${serial++}.csv`),values=empty?[]:[[START,50,60,40,55]];
    await fs.writeFile(filename,'timestamp,open,high,low,close\n'+values.map(v=>v.join(',')+'\n').join(''));
    return{path:filename,rows:values.length};
  };
  const req={...f.request,symbol:'btcusd',resume:true};
  await download(req,{api:f.api,sdkHistory,emit:e=>{if(e.type==='done')done=e;}});
  assert.equal(await fs.readFile(req.output,'utf8'),`timestamp,open,high,low,close\n${START},10,34,5,33\n`);
  assert.equal(done.provenance.kind,'native');assert.equal(done.provenance.syntheticSource,'h1-close');assert.match(done.provenance.observationResolution,/H1 OHLC/);
  assert.deepEqual(f.requests.map(r=>r.timeframe),['h1-native']);
  empty=false;f.requests.length=0;const output=path.join(f.dir,'native-d1.csv');await fs.writeFile(output,'');
  await download({...req,output,resume:false},{api:f.api,sdkHistory,emit:e=>{if(e.type==='done')done=e;}});
  assert.equal(await fs.readFile(output,'utf8'),`timestamp,open,high,low,close\n${START},50,60,40,55\n`);
  assert.equal(f.requests.length,0);assert.equal(done.provenance.syntheticSource,'h1-close');
});
test('fast mixed XAUXAG uses native daily candles after availability instead of dividing native extrema',async t=>{
  const f=await fixture(t,3),{download}=require('./worker.cjs');let nativeCalls=0,done;
  f.api.instrumentMetaData.xauxag.startHourForTicks=new Date(START+DAY+9*HOUR+39*60000).toISOString();
  f.api.instrumentMetaData.xauxag.startYearForDailyCandles=new Date(START+DAY).toISOString();
  const sdkHistory=async(request,fields)=>{
    if(fields.timeframe!=='d1')return f.sdkHistory(request,fields);
    assert.equal(fields.symbol,'xauxag');nativeCalls++;
    const file=path.join(f.dir,`xauxag-native-${nativeCalls}.csv`),values=[];
    for(let day=fields.from;day<fields.to;day+=DAY)values.push([day,5,7,4,6]);
    await fs.writeFile(file,'timestamp,open,high,low,close\n'+values.map(v=>v.join(',')+'\n').join(''));
    return{path:file,rows:values.length};
  };
  await download({...f.request,symbol:'xauxag'},{api:f.api,sdkHistory,emit:e=>{if(e.type==='done')done=e;}});
  assert.equal(await fs.readFile(f.request.output,'utf8'),`timestamp,open,high,low,close\n${START},1,1,1,1\n${START+DAY},5,7,4,6\n${START+2*DAY},5,7,4,6\n`);
  assert.equal(nativeCalls,1);assert.equal(done.provenance.kind,'mixed-native-ratio');assert.equal(done.provenance.syntheticSource,'h1-close');
  assert.match(done.provenance.observationResolution,/H1.*native BID daily/);
});
test('fast XAUXAG checks native D1 after availability and falls back only for validated empty days',async t=>{
  const f=await fixture(t,3),{download}=require('./worker.cjs');let done;
  await download({...f.request,symbol:'xauxag'},{api:f.api,sdkHistory:async(request,fields)=>{
    if(fields.timeframe!=='d1')return f.sdkHistory(request,fields);
    const file=path.join(f.dir,'native-gaps.csv');
    await fs.writeFile(file,`timestamp,open,high,low,close\n${START},5,7,4,6\n${START+2*DAY},8,10,7,9\n`);
    return{path:file,rows:2};
  },emit:e=>{if(e.type==='done')done=e;}});
  assert.equal(await fs.readFile(f.request.output,'utf8'),`timestamp,open,high,low,close\n${START},5,7,4,6\n${START+DAY},1,1,1,1\n${START+2*DAY},8,10,7,9\n`);
  assert.equal(done.provenance.kind,'mixed-native-ratio');
  assert.equal(f.requests.length,2);
  assert.ok(f.requests.every(r=>r.timeframe==='h1'));
});
test('weekly derivation uses fast D1 observations and preserves Monday boundaries and sampling provenance',async t=>{
  const f=await fixture(t,7),{download}=require('./worker.cjs');let daily,weekly;
  await download(f.request,{api:f.api,sdkHistory:f.sdkHistory,emit:e=>{if(e.type==='done')daily=e;}});
  const stat=await fs.stat(f.request.output),plan=path.join(f.dir,'daily-plan.json');
  await fs.writeFile(plan,JSON.stringify({version:1,asOf:f.request.asOf,inputs:[{input:f.request.output,bytes:stat.size,modified:Math.trunc(stat.mtimeMs),metadata:{provenance:daily.provenance,checksum:daily.checksum}}]}));
  const output=path.join(f.dir,'week.csv');await fs.writeFile(output,'');
  await download({...f.request,timeframe:'w1',output,dailyPlanFile:plan},{api:f.api,sdkHistory:async()=>{throw Error('Weekly derivation requested provider history');},emit:e=>{if(e.type==='done')weekly=e;}});
  assert.equal(await fs.readFile(output,'utf8'),`timestamp,open,high,low,close\n${START},5,88.5,5,88.5\n`);
  assert.equal(weekly.provenance.syntheticSource,'h1-close');assert.match(weekly.provenance.observationResolution,/H1/);
});
test('XAUXAG refresh retains actual hourly sampling and future fast preference across native overlap',async t=>{
  const f=await fixture(t,4),{refresh,scan}=require('./refresh-worker.cjs'),synthetic=require('./synthetic-worker.cjs');
  const original=`timestamp,open,high,low,close\n${START},1,1,1,1\n${START+DAY},1,7,1,6\n${START+2*DAY},5,7,4,6\n`;
  await fs.writeFile(f.request.output,original);const checked=await scan(f.request.output,'d1');
  const provenance={...synthetic.provenance('xauxag',true),kind:'mixed-native-ratio',syntheticSource:'h1-close',historySource:'jforex-sdk',maxQuoteAgeMs:HOUR,sessionPolicy:'verified-full-hour-close-v1',observationResolution:'verified full-session H1 BID close observations',
    segments:[{from:START,to:START+DAY+9*HOUR,kind:'synchronized-bid-ratio'},{from:START+DAY+9*HOUR,to:START+3*DAY,kind:'native'}]};
  const output=path.join(f.dir,'refreshed.csv');let forwarded;
  const result=await refresh({input:f.request.output,output,symbol:'xauxag',timeframe:'d1',from:'2024-01-01',asOf:START+4*DAY,syntheticSource:'h1-close',provenance,coverageVerified:true,coverageDigest:checked.checksum},{now:START+4*DAY,emit:()=>{},download:async(request,options)=>{
    forwarded=request.syntheticSource;
    await fs.writeFile(request.output,`timestamp,open,high,low,close\n${START+2*DAY},5,8,4,7\n${START+3*DAY},7,9,6,8\n`);
    options.emit({type:'done',rows:2,provenance:{...synthetic.nativeProvenance('xauxag'),syntheticSource:'h1-close',historySource:'jforex-sdk',volume:'unavailable',observationResolution:'JForex SDK BID daily candles'}});
  }});
  assert.equal(forwarded,'h1-close');assert.equal(result.provenance.kind,'mixed-native-ratio');
  assert.equal(result.provenance.syntheticSource,'h1-close');assert.equal(result.provenance.maxQuoteAgeMs,HOUR);
  assert.match(result.provenance.observationResolution,/H1/);assert.notEqual(result.provenance.sessionPolicy,'exact-timestamp-no-carry-v1');
  assert.equal(await fs.readFile(f.request.output,'utf8'),original);
});

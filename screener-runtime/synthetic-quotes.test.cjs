'use strict';
const test=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs/promises');
const path=require('node:path');
const os=require('node:os');
const {spawn}=require('node:child_process');
const {createHash}=require('node:crypto');
const api=require('dukascopy-node');
const {sourceQuotes,HOUR,cacheIO}=require('./synthetic-quotes.cjs');
const collect=async source=>{const result=[];for await(const value of source)result.push(value);return result;};
const start=Date.UTC(2024,0,1);
function tickPayload(hour,quotes){
  if(!quotes.length)return Buffer.from(JSON.stringify({timestamp:hour,times:[],asks:[],bids:[],askVolumes:[],bidVolumes:[]}));
  let previousTime=0,previousBid=quotes[0][1];
  const times=[],bids=[];
  for(const [time,bid] of quotes){times.push(time-previousTime);bids.push(bid-previousBid);previousTime=time;previousBid=bid;}
  return Buffer.from(JSON.stringify({timestamp:hour,multiplier:1,ask:quotes[0][1]+1,bid:quotes[0][1],times,bids,asks:bids,askVolumes:quotes.map(()=>1),bidVolumes:quotes.map(()=>1)}));
}
function sourceHour(url){const parts=new URL(url).pathname.split('/').slice(-4).map(Number);return Date.UTC(parts[0],parts[1]-1,parts[2],parts[3]);}
function transport(fn){return {...api,BufferFetcher:class extends api.BufferFetcher{constructor(config){super({...config,fetcherFn:fn});}}};}
async function fixture(t){
  const directory=await fs.mkdtemp(path.join(os.tmpdir(),'cryptodesk-synthetic-quotes-'));
  t.after(()=>fs.rm(directory,{recursive:true,force:true}));
  return {output:path.join(directory,'output.part'),syntheticCacheDirectory:path.join(directory,'quotes'),asOf:start+3*HOUR};
}
test('real public tick decoder feeds BID-only cache and verified chunks are reused without provider calls',async t=>{
  const request=await fixture(t);let calls=0;
  const options={api:transport(async url=>{calls++;return tickPayload(sourceHour(url),[[0,10],[100,20],[100,30]]);})};
  const expected=[[start,10],[start+100,20],[start+100,30]];
  assert.deepEqual(await collect(sourceQuotes('btcusd',start,start+HOUR,request,options)),expected);
  assert.deepEqual(await collect(sourceQuotes('btcusd',start,start+HOUR,request,{api:transport(()=>assert.fail('cache refetched'))})),expected);
  assert.equal(calls,1);
});
test('concurrent ratios share a source chunk and an interrupted owner lock is recovered',async t=>{
  const request=await fixture(t),folder=path.join(request.syntheticCacheDirectory,'btcusd');await fs.mkdir(folder,{recursive:true});
  await fs.mkdir(path.join(folder,`${start}.lock`));await fs.writeFile(path.join(folder,`${start}.lock`,'owner.json'),JSON.stringify({pid:2147483647}));
  let calls=0;const options={api:transport(async url=>{calls++;await new Promise(resolve=>setTimeout(resolve,30));return tickPayload(sourceHour(url),[[0,10]]);})};
  const values=await Promise.all([collect(sourceQuotes('btcusd',start,start+HOUR,request,options)),collect(sourceQuotes('btcusd',start,start+HOUR,request,options))]);
  assert.deepEqual(values,[[[start,10]],[[start,10]]]);assert.equal(calls,1);
});
test('concurrent quote readers wait for complete lock-owner publication instead of parsing empty JSON',{timeout:5000},async t=>{
  const request=await fixture(t),originalWrite=fs.writeFile,originalRead=fs.readFile,originalStat=fs.lstat;
  let created,continueOwner,inspected;
  const ownerCreated=new Promise(resolve=>{created=resolve;}),releaseOwner=new Promise(resolve=>{continueOwner=resolve;}),peerInspected=new Promise(resolve=>{inspected=resolve;});
  let paused=false,calls=0;
  fs.writeFile=async function(filename,data,options){
    if(!paused&&path.dirname(filename).endsWith('.lock')&&(path.basename(filename)==='owner.json'||/^\.owner-.*\.pending$/.test(path.basename(filename)))){
      paused=true;
      // Widen the real writeFile window: the file exists before its JSON bytes.
      await originalWrite.call(this,filename,'',options);created();await releaseOwner;
      return originalWrite.call(this,filename,data,{...options,flag:'w'});
    }
    return originalWrite.call(this,filename,data,options);
  };
  fs.readFile=async function(filename,...options){
    try{return await originalRead.call(this,filename,...options);}
    finally{if(path.basename(filename)==='owner.json')inspected();}
  };
  fs.lstat=async function(filename,...options){
    try{return await originalStat.call(this,filename,...options);}
    finally{if(path.basename(filename)==='owner.json')inspected();}
  };
  t.after(()=>{continueOwner();fs.writeFile=originalWrite;fs.readFile=originalRead;fs.lstat=originalStat;});
  const options={api:transport(async url=>{calls++;return tickPayload(sourceHour(url),[[0,10]]);})};
  const first=collect(sourceQuotes('btcusd',start,start+HOUR,request,options));
  await ownerCreated;
  const second=collect(sourceQuotes('btcusd',start,start+HOUR,request,options));
  const results=Promise.allSettled([first,second]);
  await peerInspected;continueOwner();
  const settled=await results;
  for(const result of settled){if(result.status==='rejected')assert.ifError(result.reason);assert.deepEqual(result.value,[[start,10]]);}
  assert.equal(calls,1);
});
test('unknown or live initializing locks are bounded and cancellable without age-based removal',async t=>{
  for(const kind of ['empty','truncated','live-pending']){
    const request=await fixture(t),folder=path.dirname(request.output),lock=path.join(folder,'source.lock');
    const saved=path.join(folder,'saved-quotes.csv'),bytes='timestamp,bid\n1704067200000,10\n';await fs.writeFile(saved,bytes);
    await fs.mkdir(lock);
    const marker=path.join(lock,kind==='live-pending'?`.owner-${process.pid}-${'a'.repeat(32)}.pending`:'owner.json');
    const original=kind==='truncated'?'{"pid":':'';await fs.writeFile(marker,original);
    await fs.utimes(lock,new Date(0),new Date(0));
    const before=await fs.lstat(lock);
    await assert.rejects(cacheIO.lock(lock,undefined,{timeoutMs:40}),/initialization could not be verified|Timed out waiting/);
    const controller=new AbortController(),waiting=cacheIO.lock(lock,controller.signal);controller.abort();
    await assert.rejects(waiting,{name:'AbortError'});
    assert.equal((await fs.lstat(lock)).ino,before.ino);assert.equal(await fs.readFile(marker,'utf8'),original);assert.equal(await fs.readFile(saved,'utf8'),bytes);
  }
});
test('a proven dead initializer is retired while its evidence and saved quote bytes remain intact',async t=>{
  const request=await fixture(t),folder=path.dirname(request.output),lock=path.join(folder,'source.lock');await fs.mkdir(lock);
  const pending=`.owner-2147483647-${'a'.repeat(32)}.pending`,saved=path.join(folder,'saved-quotes.csv');
  await fs.writeFile(path.join(lock,pending),'unfinished');await fs.writeFile(saved,'unchanged source quotes');
  const release=await cacheIO.lock(lock);await release();
  assert.equal(await fs.readFile(saved,'utf8'),'unchanged source quotes');
  const retired=(await fs.readdir(folder)).find(name=>name.startsWith('source.lock.abandoned-'));
  assert.ok(retired);assert.equal(await fs.readFile(path.join(folder,retired,pending),'utf8'),'unfinished');
});
test('a stale release cannot remove a replacement live lock generation',async t=>{
  const request=await fixture(t),lock=path.join(path.dirname(request.output),'source.lock');
  const releaseOld=await cacheIO.lock(lock);await fs.rename(lock,`${lock}.retained`);
  const releaseNew=await cacheIO.lock(lock),owner=await fs.readFile(path.join(lock,'owner.json'),'utf8');
  await releaseOld();assert.equal(await fs.readFile(path.join(lock,'owner.json'),'utf8'),owner);
  await releaseNew();await assert.rejects(fs.lstat(lock),{code:'ENOENT'});
  assert.ok((await fs.lstat(`${lock}.retained`)).isDirectory());
});
test('dead-owner recovery rechecks directory and owner before retiring a replacement live lock',async t=>{
  for(const replaceDirectory of [false,true]){
    const request=await fixture(t),lock=path.join(path.dirname(request.output),'source.lock');await fs.mkdir(lock);
    const dead=JSON.stringify({pid:2147483647}),live=JSON.stringify({pid:process.pid,id:'b'.repeat(32)});
    await fs.writeFile(path.join(lock,'owner.json'),dead);
    const original=fs.mkdir;let replaced=false;
    fs.mkdir=async function(filename,...options){
      if(filename===`${lock}.reaping`&&!replaced){
        replaced=true;
        if(replaceDirectory){await fs.rename(lock,`${lock}.retained`);await original.call(this,lock);}
        await fs.writeFile(path.join(lock,'owner.json'),live);
      }
      return original.call(this,filename,...options);
    };
    try{await assert.rejects(cacheIO.lock(lock,undefined,{timeoutMs:50}),/Timed out waiting/);}
    finally{fs.mkdir=original;}
    assert.equal(replaced,true);assert.equal(await fs.readFile(path.join(lock,'owner.json'),'utf8'),live);
    await assert.rejects(fs.lstat(`${lock}.reaping`),{code:'ENOENT'});
    if(replaceDirectory)assert.equal(await fs.readFile(path.join(`${lock}.retained`,'owner.json'),'utf8'),dead);
  }
});
test('two waiters recover a proven-dead reaper and retain ownership evidence without overlapping',async t=>{
  const request=await fixture(t),folder=path.dirname(request.output),lock=path.join(folder,'source.lock'),claim=`${lock}.reaping`;
  for(const filename of [lock,claim]){
    await fs.mkdir(filename);await fs.writeFile(path.join(filename,'owner.json'),JSON.stringify({pid:2147483647,id:'c'.repeat(32)}));
  }
  let active=0,completed=0;
  await Promise.all(Array.from({length:2},async()=>{
    const release=await cacheIO.lock(lock,undefined,{timeoutMs:1000});
    try{active++;assert.equal(active,1);await new Promise(resolve=>setImmediate(resolve));completed++;}
    finally{active--;await release();}
  }));
  assert.equal(completed,2);await assert.rejects(fs.lstat(lock),{code:'ENOENT'});await assert.rejects(fs.lstat(claim),{code:'ENOENT'});
  const files=await fs.readdir(folder);
  for(const prefix of ['source.lock.abandoned-','source.lock.reaping.abandoned-']){
    const retired=files.find(name=>name.startsWith(prefix));assert.ok(retired);
    assert.deepEqual(JSON.parse(await fs.readFile(path.join(folder,retired,'owner.json'),'utf8')),{pid:2147483647,id:'c'.repeat(32)});
  }
});
test('live or unknown reaper claims are not stolen, even when old or cancelled',async t=>{
  for(const live of [false,true]){
    const request=await fixture(t),lock=path.join(path.dirname(request.output),'source.lock'),claim=`${lock}.reaping`;
    await fs.mkdir(lock);const owner=JSON.stringify({pid:2147483647});await fs.writeFile(path.join(lock,'owner.json'),owner);
    await fs.mkdir(claim);const marker=path.join(claim,live?`.owner-${process.pid}-${'d'.repeat(32)}.pending`:'owner.json');
    await fs.writeFile(marker,'');await fs.utimes(claim,new Date(0),new Date(0));const before=await fs.lstat(claim);
    await assert.rejects(cacheIO.lock(lock,undefined,{timeoutMs:40}),/initialization could not be verified|Timed out waiting/);
    const controller=new AbortController(),waiting=cacheIO.lock(lock,controller.signal);controller.abort();await assert.rejects(waiting,{name:'AbortError'});
    assert.equal((await fs.lstat(claim)).ino,before.ino);assert.equal(await fs.readFile(marker,'utf8'),'');assert.equal(await fs.readFile(path.join(lock,'owner.json'),'utf8'),owner);
  }
});
test('a stale reaper finally cannot remove a successor claim while its owner metadata is still publishing',{timeout:5000},async t=>{
  const request=await fixture(t),lock=path.join(path.dirname(request.output),'source.lock'),claim=`${lock}.reaping`;
  await fs.mkdir(lock);await fs.writeFile(path.join(lock,'owner.json'),JSON.stringify({pid:2147483647}));
  const originalRename=fs.rename,originalWrite=fs.writeFile;
  let successor,started,continueOwner,replace=false;
  const initializing=new Promise(resolve=>{started=resolve;}),resume=new Promise(resolve=>{continueOwner=resolve;});
  fs.writeFile=async function(filename,data,options){
    if(replace&&path.dirname(filename)===claim&&/^\.owner-.*\.pending$/.test(path.basename(filename))){
      await originalWrite.call(this,filename,'',options);started(filename);await resume;
      return originalWrite.call(this,filename,data,{...options,flag:'w'});
    }
    return originalWrite.call(this,filename,data,options);
  };
  fs.rename=async function(from,to){
    const value=await originalRename.call(this,from,to);
    if(from===lock&&!replace){
      await originalRename.call(this,claim,`${claim}.retained`);replace=true;
      successor=cacheIO.lock(claim);await initializing;
    }
    return value;
  };
  t.after(()=>{continueOwner();fs.rename=originalRename;fs.writeFile=originalWrite;});
  const release=await cacheIO.lock(lock),pending=await initializing;
  assert.ok((await fs.lstat(claim)).isDirectory());assert.equal(await fs.readFile(pending,'utf8'),'');
  await assert.rejects(fs.lstat(path.join(claim,'owner.json')),{code:'ENOENT'});
  continueOwner();const releaseSuccessor=await successor;await releaseSuccessor();await release();
  assert.ok((await fs.lstat(`${claim}.retained`)).isDirectory());
});
test('independent Node workers serialize shared-cache ownership through delayed initialization and dead-owner recovery',{timeout:10000},async t=>{
  const request=await fixture(t),folder=path.dirname(request.output),lock=path.join(folder,'source.lock');await fs.mkdir(lock);
  await fs.writeFile(path.join(lock,'owner.json'),JSON.stringify({pid:2147483647}));
  const program=path.join(folder,'contender.cjs'),counter=path.join(folder,'counter');await fs.writeFile(counter,'0');
  await fs.writeFile(program,`
const fs=require('node:fs/promises'),path=require('node:path');
const {cacheIO}=require(${JSON.stringify(require.resolve('./synthetic-quotes.cjs'))});
const folder=process.argv[2],lock=path.join(folder,'source.lock');
const original=fs.writeFile;
fs.writeFile=async function(filename,data,options){
  if(/^\\.owner-.*\\.pending$/.test(path.basename(filename))){
    await original.call(this,filename,'',options);await new Promise(resolve=>setTimeout(resolve,15));
    return original.call(this,filename,data,{...options,flag:'w'});
  }
  return original.call(this,filename,data,options);
};
(async()=>{
  for(let i=0;i<4;i++){
    const release=await cacheIO.lock(lock,undefined,{timeoutMs:5000});
    try{
      const guard=await fs.open(path.join(folder,'active'),'wx');
      try{
        const current=Number(await fs.readFile(path.join(folder,'counter'),'utf8'));
        await new Promise(resolve=>setTimeout(resolve,2));await fs.writeFile(path.join(folder,'counter'),String(current+1));
      }finally{await guard.close();await fs.unlink(path.join(folder,'active'));}
    }finally{await release();}
  }
})().catch(error=>{console.error(error.stack);process.exitCode=1;});
`);
  const children=[];
  t.after(()=>{for(const child of children)if(child.exitCode===null)child.kill();});
  const results=Array.from({length:6},()=>new Promise((resolve,reject)=>{
    const child=spawn(process.execPath,[program,folder],{stdio:['ignore','pipe','pipe']});children.push(child);let output='';
    child.stdout.on('data',data=>{output+=data;});child.stderr.on('data',data=>{output+=data;});child.once('error',reject);
    child.once('exit',code=>{if(code===0)resolve();else reject(Error(`Shared-cache worker exited ${code}: ${output}`));});
  }));
  await Promise.all(results);
  assert.equal(await fs.readFile(counter,'utf8'),'24');
  await assert.rejects(fs.lstat(lock),{code:'ENOENT'});await assert.rejects(fs.lstat(path.join(folder,'active')),{code:'ENOENT'});
});
test('malformed source quotes fail without publishing a complete cache entry',async t=>{
  const request=await fixture(t);
  await assert.rejects(collect(sourceQuotes('btcusd',start,start+HOUR,request,{api:transport(async url=>tickPayload(sourceHour(url),[[0,10],[100,-1]]))})),/Invalid|positive|price/i);
  const files=await fs.readdir(path.join(request.syntheticCacheDirectory,'btcusd'));
  assert.equal(files.some(name=>name.endsWith('.json')||name.endsWith('.pending')),false);
});
test('corrupt derived cache is refetched and only complete past source hours are used',async t=>{
  const request=await fixture(t);let calls=0;const options={api:transport(async url=>{calls++;return tickPayload(sourceHour(url),[[0,10]]);})};
  await collect(sourceQuotes('btcusd',start,start+HOUR,request,options));
  const folder=path.join(request.syntheticCacheDirectory,'btcusd'),manifest=JSON.parse(await fs.readFile(path.join(folder,`${start}.json`),'utf8'));
  await fs.appendFile(path.join(folder,manifest.file),'broken');
  assert.deepEqual(await collect(sourceQuotes('btcusd',start,start+HOUR,request,options)),[[start,10]]);assert.equal(calls,2);
  assert.deepEqual(await collect(sourceQuotes('btcusd',start+HOUR,start+2*HOUR,{...request,asOf:start+HOUR+500},options)),[]);
  assert.equal(calls,2);
});
test('checksummed published tick CSV supplies complete covered hours without network or OHLC substitution',async t=>{
  const request=await fixture(t),history=path.join(path.dirname(request.output),'history');await fs.mkdir(history);
  const filename='btcusd-tick-2024-01-01-2024-01-02.csv',file=path.join(history,filename);
  const csv=`timestamp,askPrice,bidPrice,askVolume,bidVolume\n${start},11,10,1,2\n${start+HOUR+5},21,20,1,2\n`;
  await fs.writeFile(file,csv);await fs.mkdir(path.join(history,'.cryptodesk-screener-metadata'));
  const metadata=path.join(history,'.cryptodesk-screener-metadata',createHash('sha256').update(file).digest('hex')+'.json');
  await fs.writeFile(metadata,JSON.stringify({version:1,filename,verified:true,bytes:Buffer.byteLength(csv),checksum:createHash('sha256').update(csv).digest('hex'),coverage:[{from:start,to:start+3*HOUR}]}));
  const values=await collect(sourceQuotes('btcusd',start,start+3*HOUR,{...request,historyDirectory:history},{api:transport(()=>assert.fail('verified history refetched'))}));
  assert.deepEqual(values,[[start,10],[start+HOUR+5,20]]);
  assert.equal((await fs.readdir(path.join(request.syntheticCacheDirectory,'btcusd'))).some(name=>name.startsWith('.import-')),false);
});
test('unverified partial-hour CSV cannot suppress a provider request',async t=>{
  const request=await fixture(t),history=path.join(path.dirname(request.output),'history');await fs.mkdir(history);
  await fs.writeFile(path.join(history,'btcusd-tick-2024-01-01-2024-01-02.csv'),`timestamp,askPrice,bidPrice,askVolume,bidVolume\n${start},1000,999,1,2\n`);
  let calls=0;
  const values=await collect(sourceQuotes('btcusd',start,start+HOUR,{...request,historyDirectory:history},{api:transport(async url=>{calls++;return tickPayload(sourceHour(url),[[0,10]]);})}));
  assert.deepEqual(values,[[start,10]]);assert.equal(calls,1);
});
test('abort cancels source iteration without leaving a held chunk lock',async t=>{
  const request=await fixture(t),controller=new AbortController();
  await assert.rejects(collect(sourceQuotes('btcusd',start,start+HOUR,request,{signal:controller.signal,api:transport(async url=>{controller.abort();return tickPayload(sourceHour(url),[[0,10]]);})})),/abort/i);
  assert.equal((await fs.readdir(path.join(request.syntheticCacheDirectory,'btcusd'))).some(name=>name.endsWith('.lock')),false);
});
test('disjoint or invalid tick coverage skips historical file reads before fetching the requested hour',async t=>{
  const query=Date.UTC(2026,0,1);
  for(const coverage of [[{from:Date.UTC(2010,0,1),to:Date.UTC(2024,0,1)}],[{from:query-HOUR,to:query}],
    [{from:query+HOUR,to:query+2*HOUR}],[{from:query,to:query+HOUR},{from:query+HOUR,to:NaN}],
    [{from:query+HOUR,to:query}],Array(10001).fill({from:query,to:query+HOUR})]){
    const request={...await fixture(t),asOf:query+HOUR},history=path.join(path.dirname(request.output),'history');await fs.mkdir(history);
    const filename='btcusd-tick-2010-01-01-2024-01-01.csv',file=path.join(history,filename),csv='not a readable tick CSV\n';
    await fs.writeFile(file,csv);await fs.mkdir(path.join(history,'.cryptodesk-screener-metadata'));
    const metadata=path.join(history,'.cryptodesk-screener-metadata',createHash('sha256').update(file).digest('hex')+'.json');
    await fs.writeFile(metadata,JSON.stringify({version:1,filename,verified:true,bytes:Buffer.byteLength(csv),checksum:createHash('sha256').update(csv).digest('hex'),coverage}));
    const streams=require('node:fs'),original=streams.createReadStream;let historicalReads=0,fetches=0;
    streams.createReadStream=function(filename,...options){if(filename===file){historicalReads++;throw Error('Historical file must not be read for disjoint or invalid coverage.');}return original.call(this,filename,...options);};
    try{
      const values=await collect(sourceQuotes('btcusd',query,query+HOUR,{...request,historyDirectory:history},{api:transport(async url=>{fetches++;return tickPayload(sourceHour(url),[[0,10]]);})}));
      assert.deepEqual(values,[[query,10]]);assert.equal(historicalReads,0);assert.equal(fetches,1);
    }finally{streams.createReadStream=original;}
  }
});
module.exports={tickPayload,sourceHour,transport,start};

'use strict';
const test=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs/promises');
const path=require('node:path');
const os=require('node:os');
const {createHash}=require('node:crypto');
const api=require('dukascopy-node');
const {sourceQuotes,HOUR}=require('./synthetic-quotes.cjs');
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
module.exports={tickPayload,sourceHour,transport,start};

'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs/promises');
const path=require('node:path');
const os=require('node:os');
const {parse}=require('csv-parse/sync');
const {extendDownload,union,missing}=require('./incremental-worker.cjs');
const {scan}=require('./refresh-worker.cjs');
const {download}=require('./worker.cjs');
const api=require('dukascopy-node');

const day=86400000,start=Date.UTC(2024,0,1),headers=['timestamp','open','high','low','close','volume'];
const candle=(n,price=(n+1)*10)=>[start+n*day,price,price+1,price-1,price,1];
const csv=rows=>`${headers.join(',')}\n${rows.map(row=>row.join(',')).join('\n')}\n`;
const read=async file=>parse(await fs.readFile(file,'utf8'),{columns:true});
async function fixture(t,sources,extra={}){
 const directory=await fs.mkdtemp(path.join(os.tmpdir(),'cryptodesk-incremental-'));
 t.after(()=>fs.rm(directory,{recursive:true,force:true}));
 const work=path.join(directory,'work');await fs.mkdir(work);
 const inputs=[];
 for(let index=0;index<sources.length;index++){
  const text=typeof sources[index]==='string'?sources[index]:sources[index].text;
  const input=path.join(directory,`source${index}.csv`);await fs.writeFile(input,text);
  const stat=await fs.stat(input);
  inputs.push({input,name:path.basename(input),bytes:stat.size,modified:Math.trunc(stat.mtimeMs),from:'2024-01-01',metadata:sources[index].metadata??{}});
 }
 const planFile=path.join(work,'plan.json');
 await fs.writeFile(planFile,JSON.stringify({version:1,asOf:start+10*day,inputs}));
 const output=path.join(work,'data.part');await fs.writeFile(output,'');
 return {symbol:'eurusd',timeframe:'d1',from:'2024-01-01',to:'2024-01-07',output,planFile,...extra};
}

test('coverage subtraction requests only missing prefix, internal range and suffix',()=>{
 assert.deepEqual(union([{from:4,to:6},{from:1,to:4},{from:8,to:9}]),[{from:1,to:6},{from:8,to:9}]);
 assert.deepEqual(missing(0,10,[{from:2,to:4},{from:6,to:8}]),[{from:0,to:2},{from:4,to:6},{from:8,to:10}]);
 assert.deepEqual(missing(2,4,[{from:0,to:10}]),[]);
});

test('prepending and appending preserve the original and replay existing indicators with new warm-up context',async t=>{
 const original=`${headers.join(',')},sma_2,weekday,hour\n${candle(2).join(',')},,3,0\n${candle(3).join(',')},35,4,0\n`;
 const request=await fixture(t,[original]),ranges=[],events=[];
 await extendDownload(request,{emit:event=>events.push(event),download:async(input,options)=>{
  ranges.push([input.from,input.to]);
  const rows=Array.from({length:6},(_,i)=>candle(i)).filter(row=>row[0]>=Date.parse(input.from)&&row[0]<Date.parse(input.to));
  await fs.writeFile(input.output,csv(rows));
  options.emit({type:'done',rows:rows.length});
 }});
 assert.deepEqual(ranges,[[new Date(start).toISOString(),new Date(start+2*day).toISOString()],[new Date(start+4*day).toISOString(),new Date(start+6*day).toISOString()]]);
 assert.equal(events.at(-1).type,'done');
 const actual=await read(request.output);
 assert.deepEqual(actual.map(row=>row.sma_2),['','15','25','35','45','55']);
 assert.deepEqual(actual.map(row=>row.weekday),['1','2','3','4','5','6']);
 assert.ok(actual.every(row=>row.hour==='0'));
 const plan=JSON.parse(await fs.readFile(request.planFile));
 assert.equal(await fs.readFile(plan.inputs[0].input,'utf8'),original);
});

test('existing coverage skips network and leaves already covered files unchanged',async t=>{
 const original=csv([candle(0),candle(1),candle(2)]),request=await fixture(t,[original],{to:'2024-01-04'}),events=[];
 await extendDownload(request,{emit:event=>events.push(event),download:()=>assert.fail('covered range was redownloaded')});
 assert.equal(events.at(-1).noChange,true);
 const plan=JSON.parse(await fs.readFile(request.planFile));
 assert.equal(await fs.readFile(plan.inputs[0].input,'utf8'),original);
});

test('several existing ranges and duplicate CSVs are reused locally without downloading their covered bars',async t=>{
 const request=await fixture(t,[csv([candle(0),candle(1)]),csv([candle(4),candle(5)]),csv([candle(0),candle(1)])]),seen=[];
 await extendDownload(request,{emit:()=>{},download:async(input,options)=>{
  seen.push([Date.parse(input.from),Date.parse(input.to)]);await fs.writeFile(input.output,csv([candle(2),candle(3)]));options.emit({type:'done',rows:2});
 }});
 assert.deepEqual(seen,[[start+2*day,start+4*day]]);
 assert.deepEqual((await read(request.output)).map(row=>Number(row.timestamp)),Array.from({length:6},(_,i)=>start+i*day));
});

test('bounded merge passes support more than eight existing histories',async t=>{
 const request=await fixture(t,Array.from({length:18},()=>csv([candle(0),candle(1)])),{to:'2024-01-03'});
 await extendDownload(request,{emit:()=>{},download:()=>assert.fail('existing duplicate histories were downloaded')});
 assert.equal((await read(request.output)).length,2);
});

test('a saved query ledger skips pre-listing dates and market closures with no rows',async t=>{
 const text=csv([candle(2),candle(3)]);
 const request=await fixture(t,[{text,metadata:{checksum:'pending',coverage:[{from:start,to:start+6*day}]}}]);
 const plan=JSON.parse(await fs.readFile(request.planFile));plan.inputs[0].metadata.checksum=(await scan(plan.inputs[0].input,'d1')).checksum;
 await fs.writeFile(request.planFile,JSON.stringify(plan));
 const events=[];await extendDownload(request,{emit:event=>events.push(event),download:()=>assert.fail('verified closed range was downloaded')});
 assert.equal(events.at(-1).noChange,true);
});

test('real per-source checkpoints resume a missing suffix without refetching completed source batches',async t=>{
 const request=await fixture(t,[csv([candle(0)])],{timeframe:'m1',from:'2024-01-01',to:'2024-01-04'});
 const plan=JSON.parse(await fs.readFile(request.planFile));plan.inputs[0].metadata={checksum:(await scan(plan.inputs[0].input,'m1')).checksum,coverage:[{from:start,to:start+day}]};
 await fs.writeFile(request.planFile,JSON.stringify(plan));
 const seen=[];let fail=true;
 const transport={...api,BufferFetcher:class extends api.BufferFetcher{constructor(options){super({...options,fetcherFn:async url=>{
  seen.push(url);
  const date=url.endsWith('/2024/1/2')?start+day:start+2*day;
  if(date===start+2*day&&fail)throw Error('Test interrupted source download');
  return Buffer.from(JSON.stringify({timestamp:date,shift:60000,multiplier:1,open:20,high:21,low:19,close:20,times:[0],opens:[0],highs:[0],lows:[0],closes:[0],volumes:[1]}));
 }})}}};
 await assert.rejects(extendDownload({...request,sourceBatchSize:1},{api:transport,pauseMs:0,emit:()=>{}}),/interrupted/);
 const first=seen.filter(url=>url.endsWith('/2024/1/2')).length;assert.equal(first,1);
 fail=false;
 await extendDownload({...request,sourceBatchSize:1},{api:transport,pauseMs:0,emit:()=>{}});
 assert.equal(seen.filter(url=>url.endsWith('/2024/1/2')).length,1);
 assert.equal((await read(request.output)).length,3);
});

test('unknown indicator columns and source edits fail without overwriting originals',async t=>{
 const original=`${headers.join(',')},unknown\n${candle(0).join(',')},42\n`,request=await fixture(t,[original]);
 await assert.rejects(extendDownload(request,{emit:()=>{},download:()=>assert.fail('unknown indicator reached network')}),/unrecognized/);
 const plan=JSON.parse(await fs.readFile(request.planFile));assert.equal(await fs.readFile(plan.inputs[0].input,'utf8'),original);
 await fs.appendFile(plan.inputs[0].input,candle(1).join(',')+',42\n');
 await assert.rejects(extendDownload(request,{emit:()=>{}}),/changed/);
});

test('a current partial candle is updated while covered closed candles are not requested again',async t=>{
 const text=csv([candle(0),candle(1,15)]),request=await fixture(t,[text],{to:'2024-01-03'});
 const plan=JSON.parse(await fs.readFile(request.planFile));plan.asOf=start+day+day/2;
 plan.inputs[0].metadata={checksum:(await scan(plan.inputs[0].input,'d1')).checksum,coverage:[{from:start,to:start+day}]};
 await fs.writeFile(request.planFile,JSON.stringify(plan));
 const seen=[],events=[];
 await extendDownload(request,{emit:event=>events.push(event),download:async(input,options)=>{
  seen.push([Date.parse(input.from),Date.parse(input.to)]);await fs.writeFile(input.output,csv([candle(1,20)]));options.emit({type:'done',rows:1});
 }});
 assert.deepEqual(seen,[[start+day,plan.asOf]]);
 assert.deepEqual((await read(request.output)).map(row=>Number(row.close)),[10,20]);
 assert.deepEqual(events.at(-1).coverage,[{from:start,to:start+day}]);
});

test('tick histories retain maximum duplicate multiplicity across overlapping local files',async t=>{
 const tickHeader='timestamp,askPrice,bidPrice,askVolume,bidVolume,sma_2_bidprice';
 const tick=`${start},12,10,3,4`;
 const request=await fixture(t,[`${tickHeader}\n${tick},\n${tick},10\n`,`${tickHeader}\n${tick},\n${tick},10\n${tick},10\n`],{timeframe:'tick',to:'2024-01-02'});
 await extendDownload(request,{emit:()=>{},download:async(input,options)=>{
  await fs.writeFile(input.output,`timestamp,askPrice,bidPrice,askVolume,bidVolume\n${start+1},13,11,3,4\n${start+2},14,12,3,4\n`);options.emit({type:'done',rows:2});
 }});
 const rows=await read(request.output);
 assert.equal(rows.length,5);
 assert.deepEqual(rows.map(row=>row.sma_2_bidprice),['','10','10','10.5','11.5']);
});

test('requests before the Unix epoch remember unavailable earlier intervals without discarding saved data',async t=>{
 const request=await fixture(t,[csv([candle(0)])],{from:'1900-01-01',to:'1901-01-01'}),events=[];
 await extendDownload(request,{emit:event=>events.push(event),download:async()=>{throw Error('No available history in the requested UTC date range.');}});
 assert.equal((await read(request.output)).length,1);
 assert.ok(events.at(-1).coverage.some(range=>range.from===Date.parse('1900-01-01')&&range.to===Date.parse('1901-01-01')));
});

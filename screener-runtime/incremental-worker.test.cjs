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

test('offline consolidation keeps every overlapping bar and enriched column with stable source priority',async t=>{
 const originals=[
  `${headers.join(',')},sma_2\n${[0,1,2].map(n=>`${candle(n).join(',')},${n?String(5+n*10):''}`).join('\n')}\n`,
  `${headers.join(',')},weekday,hour\n${[2,3,4].map(n=>`${candle(n,n===2?99:undefined).join(',')},${n+1},0`).join('\n')}\n`,
  csv([candle(1),candle(2)]),
 ];
 const request=await fixture(t,originals,{localOnly:true,to:'2024-01-11'}),events=[];
 await extendDownload(request,{emit:event=>events.push(event),download:()=>assert.fail('offline consolidation reached the provider')});
 const rows=await read(request.output),result=events.at(-1);
 assert.deepEqual(rows.map(row=>Number(row.timestamp)),[0,1,2,3,4].map(n=>start+n*day));
 assert.deepEqual(rows.map(row=>row.close),['10','20','30','40','50']);
 assert.deepEqual(rows.map(row=>row.sma_2),['','15','25','35','45']);
 assert.deepEqual(rows.map(row=>row.weekday),['1','2','3','4','5']);
 assert.ok(rows.every(row=>row.hour==='0'));
 assert.deepEqual(result.coverage,[{from:start,to:start+5*day}]);
 assert.deepEqual(result.recipes.map(recipe=>recipe.indicator),['sma','weekday','hour']);
 assert.equal(result.firstTimestamp,start);assert.equal(result.lastTimestamp,start+4*day);
 const plan=JSON.parse(await fs.readFile(request.planFile));
 for(let i=0;i<originals.length;i++)assert.equal(await fs.readFile(plan.inputs[i].input,'utf8'),originals[i]);
});

test('offline consolidation preserves disjoint verified coverage without filling or claiming missing ranges',async t=>{
 const request=await fixture(t,[
  {text:csv([candle(1)]),metadata:{coverage:[{from:start,to:start+2*day}]}},
  {text:csv([candle(5)]),metadata:{coverage:[{from:start+4*day,to:start+7*day}]}},
 ],{localOnly:true,to:'2024-01-11'}),events=[];
 const plan=JSON.parse(await fs.readFile(request.planFile));
 for(const source of plan.inputs)source.metadata.checksum=(await scan(source.input,'d1')).checksum;
 await fs.writeFile(request.planFile,JSON.stringify(plan));
 await extendDownload(request,{emit:event=>events.push(event),download:()=>assert.fail('offline holes were downloaded')});
 assert.deepEqual((await read(request.output)).map(row=>Number(row.timestamp)),[start+day,start+5*day]);
 assert.deepEqual(events.at(-1).coverage,[{from:start,to:start+2*day},{from:start+4*day,to:start+7*day}]);
 assert.equal(events.at(-1).checkedThrough,0);
 assert.deepEqual(JSON.parse(await fs.readFile(path.join(path.dirname(request.output),'ranges.json'))).ranges,[]);
});

test('offline subset consolidation writes an output even when the first source already contains every row',async t=>{
 const original=csv([candle(0),candle(1),candle(2)]);
 const request=await fixture(t,[original,csv([candle(1)])],{localOnly:true}),events=[];
 await extendDownload(request,{emit:event=>events.push(event),download:()=>assert.fail('subset consolidation reached the provider')});
 assert.equal(await fs.readFile(request.output,'utf8'),original);
 assert.equal(events.at(-1).noChange,true);
 assert.equal(events.at(-1).rows,3);
 assert.deepEqual(events.at(-1).coverage,[{from:start,to:start+3*day}]);
});

test('offline single source reports unchanged content and coverage without extending the requested range',async t=>{
 const original=csv([candle(1)]),request=await fixture(t,[original],{localOnly:true}),events=[];
 await extendDownload(request,{emit:event=>events.push(event),download:()=>assert.fail('single source was extended')});
 assert.equal(events.at(-1).noChange,true);
 assert.deepEqual(events.at(-1).coverage,[{from:start+day,to:start+2*day}]);
 const plan=JSON.parse(await fs.readFile(request.planFile));
 assert.equal(await fs.readFile(plan.inputs[0].input,'utf8'),original);
 assert.equal(await fs.readFile(request.output,'utf8'),'');
});

test('offline monthly consolidation retains calendar boundaries across differing month lengths',async t=>{
 const monthly=n=>[Date.UTC(2024,n,1),10,11,9,10,1];
 const request=await fixture(t,[csv([monthly(0),monthly(1)]),csv([monthly(1),monthly(2)])],{localOnly:true,timeframe:'mn1',to:'2024-05-01'}),events=[];
 const plan=JSON.parse(await fs.readFile(request.planFile));plan.asOf=Date.UTC(2024,4,1);
 await fs.writeFile(request.planFile,JSON.stringify(plan));
 await extendDownload(request,{emit:event=>events.push(event),download:()=>assert.fail('monthly consolidation reached the provider')});
 assert.deepEqual((await read(request.output)).map(row=>Number(row.timestamp)),[0,1,2].map(n=>Date.UTC(2024,n,1)));
 assert.deepEqual(events.at(-1).coverage,[{from:start,to:Date.UTC(2024,3,1)}]);
});

test('offline consolidation rejects unknown and duplicate fields without altering any source',async t=>{
 for(const extra of ['unknown','close']){
  const originals=[csv([candle(0)]),`${headers.join(',')},${extra}\n${candle(1).join(',')},42\n`];
  const request=await fixture(t,originals,{localOnly:true});
  await assert.rejects(extendDownload(request,{emit:()=>{},download:()=>assert.fail('invalid source reached the provider')}),/unrecognized|duplicate field/);
  const plan=JSON.parse(await fs.readFile(request.planFile));
  for(let i=0;i<originals.length;i++)assert.equal(await fs.readFile(plan.inputs[i].input,'utf8'),originals[i]);
  assert.equal(await fs.readFile(request.output,'utf8'),'');
 }
});

test('offline checkpoints cannot be resumed as provider downloads',async t=>{
 const request=await fixture(t,[csv([candle(0)]),csv([candle(2)])],{localOnly:true});
 await extendDownload(request,{emit:()=>{},download:()=>assert.fail('offline checkpoint requested data')});
 await assert.rejects(extendDownload({...request,localOnly:false},{emit:()=>{},download:()=>assert.fail('offline checkpoint became a provider download')}),/saved range checkpoint/);
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

test('valid empty daily and hourly suffixes preserve recipes and record coverage without inventing rows',async t=>{
 for(const timeframe of ['d1','h1'])await t.test(timeframe,async t=>{
  const duration=timeframe==='d1'?day:3600000;
  const bars=[0,1].map(index=>[start+index*duration,(index+1)*10,(index+1)*10+1,(index+1)*10-1,(index+1)*10,1]);
  const original=`${headers.join(',')},sma_2\n${bars[0].join(',')},\n${bars[1].join(',')},15\n`;
  const request=await fixture(t,[original],{timeframe,to:timeframe==='d1'?'2024-01-04':'2024-01-02'});
  const plan=JSON.parse(await fs.readFile(request.planFile));plan.asOf=start+3*duration;
  await fs.writeFile(request.planFile,JSON.stringify(plan));
  const events=[];let fetches=0;
  const transport={...api,BufferFetcher:class extends api.BufferFetcher{constructor(options){super({...options,fetcherFn:async url=>{
   fetches++;assert.match(url,timeframe==='d1'?/\/candles\/day\//:/\/candles\/hour\//);
   return Buffer.from(JSON.stringify({timestamp:start,shift:duration,multiplier:1,open:10,high:11,low:9,close:10,
    times:[],opens:[],highs:[],lows:[],closes:[],volumes:[]}));
  }});}}};
  await extendDownload(request,{api:transport,pauseMs:0,emit:event=>events.push(event)});
  const result=events.at(-1);
  assert.equal(fetches,1,'the real downloader must receive the empty suffix JSON');
  assert.equal(result.type,'done');assert.equal(result.noChange,true);assert.equal(result.rows,2);
  assert.deepEqual(result.coverage,[{from:start,to:start+3*duration}]);
  assert.equal(result.checkedThrough,start+3*duration);assert.equal(result.lastTimestamp,start+duration);
  assert.deepEqual(result.recipes,[{indicator:'sma',parameters:{period:2},source:'close'}]);
  assert.equal(await fs.readFile(plan.inputs[0].input,'utf8'),original);
  assert.equal(await fs.readFile(request.output,'utf8'),original);
  await extendDownload(request,{api:transport,pauseMs:0,emit:()=>{}});
  assert.equal(fetches,1,'a completed empty range checkpoint must not be requested again');
 });
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

test('offline tick consolidation retains distinct ticks and maximum repeated multiplicity',async t=>{
 const tickHeader='timestamp,askPrice,bidPrice,askVolume,bidVolume,sma_2_bidprice';
 const repeated=`${start},12,10,3,4`,other=`${start},13,11,3,4`;
 const request=await fixture(t,[
  `${tickHeader}\n${repeated},\n${repeated},10\n${other},10.5\n`,
  `${tickHeader}\n${repeated},\n${repeated},10\n${repeated},10\n${other},10.5\n`,
 ],{localOnly:true,timeframe:'tick',to:'2024-01-02'}),events=[];
 await extendDownload(request,{emit:event=>events.push(event),download:()=>assert.fail('offline ticks reached the provider')});
 const rows=await read(request.output);
 assert.deepEqual(rows.map(row=>row.bidPrice),['10','10','11','10']);
 assert.deepEqual(rows.map(row=>row.sma_2_bidprice),['','10','10.5','10.5']);
 assert.deepEqual(events.at(-1).coverage,[{from:start,to:start+1}]);
});

test('requests before the Unix epoch remember unavailable earlier intervals without discarding saved data',async t=>{
 const request=await fixture(t,[csv([candle(0)])],{from:'1900-01-01',to:'1901-01-01'}),events=[];
 await extendDownload(request,{emit:event=>events.push(event),download:async()=>{throw Error('No available history in the requested UTC date range.');}});
 assert.equal((await read(request.output)).length,1);
 assert.ok(events.at(-1).coverage.some(range=>range.from===Date.parse('1900-01-01')&&range.to===Date.parse('1901-01-01')));
});

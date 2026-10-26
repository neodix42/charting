'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs/promises');
const path=require('node:path');
const os=require('node:os');
const {createHash}=require('node:crypto');
const {parse}=require('csv-parse/sync');
const api=require('dukascopy-node');
const {refresh,recipesFor,closedBoundary}=require('./refresh-worker.cjs');
const {calculator,catalog}=require('./enrich-worker.cjs');

const header=['timestamp','open','high','low','close','volume'];
const day=86400000,start=Date.UTC(2024,0,1);
const row=(timestamp,price)=>[timestamp,price,price+1,price-1,price,10];
const csv=rows=>`${header.join(',')}\n${rows.map(value=>value.join(',')).join('\n')}\n`;
async function fixture(t,text){
  const directory=await fs.mkdtemp(path.join(os.tmpdir(),'cryptodesk-refresh-'));
  t.after(()=>fs.rm(directory,{recursive:true,force:true}));
  const input=path.join(directory,'input.csv'),output=path.join(directory,'output.csv');
  await fs.writeFile(input,text);
  return {input,output,symbol:'eurusd',timeframe:'d1',from:'2024-01-01'};
}
const read=async filename=>parse(await fs.readFile(filename,'utf8'),{columns:true});
const transport=fetcher=>({...api,BufferFetcher:class extends api.BufferFetcher{
  constructor(options){super({...options,fetcherFn:fetcher});}
}});

test('first refresh repairs an internal hole, revises overlap and enriches all appended rows using real public library decoding',async t=>{
  const original=`${header.join(',')},sma_2\n${row(start,10).join(',')},\n${row(start+2*day,50).join(',')},30\n`;
  const request=await fixture(t,original),events=[];
  let fetches=0;
  const result=await refresh(request,{now:start+3*day,emit:event=>events.push(event),pauseMs:0,api:transport(async url=>{
    fetches++;assert.match(url,/\/v1\/candles\/day\//);
    return Buffer.from(JSON.stringify({timestamp:start,shift:day,multiplier:1,open:10,high:11,low:9,close:10,
      times:[0,1,1],opens:[0,10,10],highs:[0,10,10],lows:[0,10,10],closes:[0,10,10],volumes:[10,10,10]}));
  })});
  assert.equal(fetches,1);
  assert.equal(result.rows,3);assert.equal(result.addedRows,1);assert.equal(result.updatedRows,1);
  assert.equal(await fs.readFile(request.input,'utf8'),original);
  const actual=await read(request.output);
  assert.deepEqual(actual.map(value=>Number(value.close)),[10,20,30]);
  assert.deepEqual(actual.map(value=>value.sma_2),['','15','25']);
  assert.deepEqual(result.recipes,[{indicator:'sma',parameters:{period:2},source:'close'}]);
  assert.equal(result.firstTimestamp,start);assert.equal(result.lastTimestamp,start+2*day);
  assert.equal(result.checkedThrough,start+3*day);
  assert.equal(events.at(-1).type,'done');
});

test('all 42 indicator and time field headers can migrate their complete applied recipes and reproduce columns',async()=>{
  for(const definition of catalog.indicators){
    const original=await calculator({indicator:definition.id,source:'auto'},header);
    const recipes=await recipesFor([...header,...original.columns]);
    const restored=(await Promise.all(recipes.map(recipe=>calculator(recipe,header)))).flatMap(value=>value.columns);
    assert.ok(original.columns.every(column=>restored.includes(column)),definition.id);
  }
  const tick=['timestamp','askPrice','bidPrice','askVolume','bidVolume'];
  for(const source of ['auto','askprice']){
    const initial=await calculator({indicator:'macd',parameters:{fast:3,slow:5,signal:2},source},tick);
    const recipes=await recipesFor([...tick,...initial.columns]);
    const restored=(await Promise.all(recipes.map(recipe=>calculator(recipe,tick)))).flatMap(value=>value.columns);
    assert.deepEqual(restored,initial.columns);
  }
  const scientific=await calculator({indicator:'alma',parameters:{period:3,offset:1e-7,sigma:6},source:'close'},header);
  assert.deepEqual(scientific.columns,['alma_3_1e-7_6']);
  assert.deepEqual(await recipesFor([...header,...scientific.columns]),[{indicator:'alma',parameters:{period:3,offset:1e-7,sigma:6},source:'close'}]);
});

test('the batch execution cutoff controls the download endpoint, newest closed bar and appended indicator values',async t=>{
  const original=`${header.join(',')},sma_2\n${row(start,10).join(',')},\n`;
  const asOf=start+3*day+12345;
  const request={...await fixture(t,original),asOf};
  let endpoint;
  const result=await refresh(request,{emit:()=>{},download:async input=>{
    endpoint=input.to;
    await fs.writeFile(input.output,csv([row(start,10),row(start+day,20),row(start+2*day,30),row(start+3*day,40)]));
  }});
  assert.equal(endpoint,new Date(asOf).toISOString());
  assert.equal(result.checkedThrough,asOf);
  assert.equal(result.lastTimestamp,start+2*day);
  assert.equal(result.addedRows,2);
  assert.deepEqual((await read(request.output)).map(value=>value.sma_2),['','15','25']);
  assert.equal(await fs.readFile(request.input,'utf8'),original);
});

test('invalid execution cutoffs fail before scanning or downloading data',async t=>{
  const request=await fixture(t,csv([row(start,10)]));
  for(const asOf of [-1,NaN,Infinity,1.25,'2024-01-01',8640000000000001]){
    await assert.rejects(refresh({...request,asOf},{emit:()=>{},download:()=>assert.fail('invalid cutoff reached transport')}),/Refresh cutoff/);
  }
});

test('verified coverage fetches bounded overlap only, preserves earlier data and does not add an open candle',async t=>{
  const request={...await fixture(t,csv([row(start,10),row(start+day,20),row(start+2*day,30),row(start+3*day,40)])),coverageVerified:true};
  request.coverageDigest=createHash('sha256').update(await fs.readFile(request.input)).digest('hex');
  let seen;
  const result=await refresh(request,{now:start+4*day+day/2,emit:()=>{},download:async input=>{
    seen=input;
    await fs.writeFile(input.output,csv([row(start+day,20),row(start+2*day,30),row(start+3*day,40),row(start+4*day,50)]));
  }});
  assert.equal(seen.from,new Date(start+day).toISOString());
  assert.equal(seen.sourceBatchSize,1,'twenty file workers must share a twenty-request upstream budget');
  assert.equal(result.rows,4);assert.equal(result.addedRows,0);
  assert.equal((await read(request.output))[0].close,'10');
});

test('a changed CSV digest invalidates coverage even when a saved size and modification time are reused',async t=>{
  const request={...await fixture(t,csv([row(start,10),row(start+3*day,40)])),coverageVerified:true,coverageDigest:'0'.repeat(64)};
  let seen;
  await refresh(request,{now:start+4*day,emit:()=>{},download:async input=>{seen=input;await fs.writeFile(input.output,csv([row(start,10),row(start+day,20),row(start+2*day,30),row(start+3*day,40)]));}});
  assert.equal(seen.from,new Date(start).toISOString());
  assert.equal((await read(request.output)).length,4);
});

test('an already present incomplete candle is revised while a newly supplied open candle is excluded',async t=>{
  for(const alreadyPresent of [false,true]){
    const rows=[row(start,10),...(alreadyPresent?[row(start+day,15)]:[])];
    const request=await fixture(t,csv(rows));
    const result=await refresh(request,{now:start+day+day/2,emit:()=>{},download:async input=>fs.writeFile(input.output,csv([row(start,10),row(start+day,20)]))});
    const actual=await read(request.output);
    assert.equal(result.rows,alreadyPresent?2:1);
    assert.equal(result.updatedRows,alreadyPresent?1:0);
    if(alreadyPresent)assert.equal(actual[1].close,'20');
  }
});

test('repeat refresh is idempotent and tick multiplicities survive same-timestamp merges',async t=>{
  const tickHeader='timestamp,askPrice,bidPrice,askVolume,bidVolume,sma_2_bidprice';
  const original=`${tickHeader}\n${start},12,10,3,4,\n${start},12,10,3,4,10\n${start},13,11,3,4,10.5\n`;
  let request={...await fixture(t,original),timeframe:'tick'};
  const downloaded=`timestamp,askPrice,bidPrice,askVolume,bidVolume\n${start},12,10,3,4\n${start},12,10,3,4\n${start},13,11,3,4\n${start},14,12,3,4\n${start+1},15,13,3,4\n`;
  const download=async input=>fs.writeFile(input.output,downloaded);
  const initial=await refresh(request,{now:start+1000,download,emit:()=>{}});
  assert.equal(initial.rows,5);assert.equal(initial.addedRows,2);
  const actual=await read(request.output);
  assert.deepEqual(actual.map(value=>value.sma_2_bidprice),['','10','10.5','11.5','12.5']);
  request={...request,input:request.output,output:path.join(path.dirname(request.output),'second.csv'),coverageVerified:true,coverageDigest:initial.checksum,recipes:initial.recipes};
  const second=await refresh(request,{now:start+1000,download,emit:()=>{}});
  assert.equal(second.rows,5);assert.equal(second.addedRows,0);
  assert.equal(await fs.readFile(request.input,'utf8'),await fs.readFile(request.output,'utf8'));
});

test('Refresh migrates mixed calendar and SMA columns and populates appended Sunday-to-Monday rows',async t=>{
  const sunday=Date.UTC(2023,11,31,23,59),monday=sunday+60000;
  const original=`${header.join(',')},sma_2,weekday,hour\n${row(sunday,10).join(',')},,7,23\n`;
  const request={...await fixture(t,original),timeframe:'m1',from:'2023-12-31'};
  const download=async input=>fs.writeFile(input.output,csv([row(sunday,10),row(monday,20),row(monday+60000,30)]));
  const result=await refresh(request,{now:monday+180000,download,emit:()=>{}});
  assert.equal(result.addedRows,2);assert.equal(result.rows,3);
  assert.deepEqual(result.columns,['sma_2','weekday','hour']);
  assert.deepEqual(result.recipes.map(recipe=>recipe.indicator),['sma','weekday','hour']);
  assert.ok(result.recipes.filter(recipe=>recipe.indicator!=='sma').every(recipe=>recipe.source==='auto'&&Object.keys(recipe.parameters).length===0));
  const actual=parse(await fs.readFile(request.output),{columns:true});
  assert.deepEqual(actual.map(value=>[value.sma_2,value.weekday,value.hour]),[['','7','23'],['15','1','0'],['25','1','0']]);
  assert.equal(await fs.readFile(request.input,'utf8'),original);
  const repeat={...request,input:request.output,output:path.join(path.dirname(request.output),'again.csv'),recipes:result.recipes,coverageVerified:true,coverageDigest:result.checksum};
  const again=await refresh(repeat,{now:monday+180000,download,emit:()=>{}});
  assert.equal(again.addedRows,0);assert.equal(again.recipes.length,3);
  assert.equal(await fs.readFile(repeat.output,'utf8'),await fs.readFile(request.output,'utf8'));
});

test('market closures with no new provider rows preserve history and still replay indicators',async t=>{
  const request=await fixture(t,`${header.join(',')},sma_2\n${row(start,10).join(',')},old\n${row(start+day,20).join(',')},old\n`);
  const result=await refresh(request,{now:start+3*day,emit:()=>{},download:async()=>{throw Error('Dukascopy returned no rows for this symbol and date range.');}});
  assert.equal(result.rows,2);assert.equal(result.addedRows,0);
  assert.equal(result.checkedThrough,start+3*day);
  assert.deepEqual((await read(request.output)).map(value=>value.sma_2),['','15']);
});

test('unknown derived columns, invalid recipes and transport errors leave the source unchanged',async t=>{
  for(const mode of ['unknown','network','bad-recipe']){
    const original=mode==='unknown'?`${header.join(',')},unknown_indicator\n${row(start,10).join(',')},42\n`:csv([row(start,10)]);
    const request=await fixture(t,original);
    if(mode==='bad-recipe')request.recipes=[{indicator:'script',parameters:{}}];
    await assert.rejects(refresh(request,{now:start+day,emit:()=>{},download:async()=>{throw Error('Public endpoint HTTP 403');}}),mode==='unknown'?/unrecognized derived/:mode==='network'?/HTTP 403/:/Unsupported/);
    assert.equal(await fs.readFile(request.input,'utf8'),original);
  }
});

test('future timestamps and malformed original market data cannot be marked as verified',async t=>{
  for(const text of [csv([row(start+day,10)]),csv([[start,10,9,11,10,1]]),csv([[start,10,11,9,'',1]])]){
    const request=await fixture(t,text);
    await assert.rejects(refresh(request,{now:start+1000,emit:()=>{},download:async()=>assert.fail('invalid history reached transport')}),/future timestamps|OHLC bounds|non-finite/);
    assert.equal(await fs.readFile(request.input,'utf8'),text);
  }
});

test('closed boundaries use UTC month transitions and exact daily/minute intervals',()=>{
  assert.equal(closedBoundary(Date.UTC(2024,2,15),'mn1'),Date.UTC(2024,2,1));
  assert.equal(closedBoundary(start+123456,'m1'),start+120000);
  assert.equal(closedBoundary(start+123456,'d1'),start);
});

'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs/promises');
const path=require('node:path');
const os=require('node:os');
const {parse}=require('csv-parse/sync');
const {download}=require('./worker.cjs');
const {refresh,closedBoundary,overlapStart}=require('./refresh-worker.cjs');
const {nextBar}=require('./incremental-worker.cjs');
const {weekStart}=require('./weekly-worker.cjs');
const day=86400000,week=7*day,start=Date.UTC(2024,0,1);
const headers=['timestamp','open','high','low','close','volume'];
const candle=(n,volume='0.1')=>[start+n*day,10+n,12+n,9+n,11+n,volume];
const csv=(rows,columns=headers)=>`${columns.join(',')}\n${rows.map(row=>row.join(',')).join('\n')}\n`;
const read=async file=>parse(await fs.readFile(file,'utf8'),{columns:true});

async function fixture(t,daily,weekly=[],asOf=start+30*day){
  const directory=await fs.mkdtemp(path.join(os.tmpdir(),'cryptodesk-weekly-'));
  t.after(()=>fs.rm(directory,{recursive:true,force:true}));
  const work=path.join(directory,'work');await fs.mkdir(work);
  const write=async(text,prefix,index)=>{
    const input=path.join(directory,`${prefix}-${index}.csv`);await fs.writeFile(input,text);
    const stat=await fs.stat(input);return {input,bytes:stat.size,modified:Math.trunc(stat.mtimeMs),metadata:{}};
  };
  const dailyInputs=await Promise.all(daily.map((text,i)=>write(text,'d1',i)));
  const dailyPlanFile=path.join(work,'daily-plan.json');
  await fs.writeFile(dailyPlanFile,JSON.stringify({version:1,asOf,inputs:dailyInputs}));
  let planFile;
  if(weekly.length){
    const inputs=await Promise.all(weekly.map((text,i)=>write(text,'w1',i)));
    planFile=path.join(work,'plan.json');await fs.writeFile(planFile,JSON.stringify({version:1,asOf,inputs}));
  }
  const output=path.join(work,'data.part');await fs.writeFile(output,'');
  return {symbol:'eurusd',timeframe:'w1',from:'2024-01-01',to:'2024-01-30',asOf,output,dailyPlanFile,planFile};
}

test('weekly boundaries use Monday UTC across Sunday, year changes and exact closes',()=>{
  assert.equal(weekStart(Date.UTC(2023,11,31,23,59)),Date.UTC(2023,11,25));
  assert.equal(weekStart(start),start);
  assert.equal(weekStart(start+week-1),start);
  assert.equal(weekStart(start+week),start+week);
  assert.equal(closedBoundary(start+week+day,'w1'),start+week);
  assert.equal(nextBar(start,'w1'),start+week);
  assert.equal(overlapStart(start,'w1'),start-2*week);
});

test('download w1 aggregates daily OHLCV with exact decimal volume and never fetches weekly data',async t=>{
  const daily=csv(Array.from({length:10},(_,i)=>candle(i))),request=await fixture(t,[daily]);
  const events=[];
  await download(request,{api:new Proxy({},{get(){assert.fail('weekly aggregation accessed provider API');}}),emit:event=>events.push(event)});
  const result=await read(request.output);
  assert.deepEqual(result,[
    {timestamp:String(start),open:'10',high:'18',low:'9',close:'17',volume:'0.7'},
    {timestamp:String(start+week),open:'17',high:'21',low:'16',close:'20',volume:'0.3'},
  ]);
  assert.equal(events.at(-1).rows,2);
  const plan=JSON.parse(await fs.readFile(request.dailyPlanFile));
  assert.equal(await fs.readFile(plan.inputs[0].input,'utf8'),daily);
});

test('gaps, partial edge weeks and duplicate legacy daily ranges retain ordered real bars only',async t=>{
  const source=csv([candle(2),candle(4),candle(18,'1e-7')]),request=await fixture(t,[source,source]);
  await download(request,{emit:()=>{}});
  const result=await read(request.output);
  assert.deepEqual(result.map(row=>row.timestamp),[String(start),String(start+2*week)]);
  assert.equal(result[0].open,'12');assert.equal(result[0].close,'15');assert.equal(result[0].volume,'0.2');
  assert.equal(result[1].volume,'0.0000001');
});

test('weekly extension retains earlier histories and recalculates weekly indicators from weekly bars',async t=>{
  const old=csv([[start-week,4,6,3,5,'0.4',''],[start,10,12,9,11,'0.1',8]], [...headers,'sma_2']);
  const request=await fixture(t,[csv(Array.from({length:14},(_,i)=>candle(i)))],[old]);
  let result;
  await download(request,{emit:event=>{if(event.type==='done')result=event;}});
  const rows=await read(request.output);
  assert.deepEqual(rows.map(row=>row.timestamp),[String(start-week),String(start),String(start+week)]);
  assert.deepEqual(rows.map(row=>row.sma_2),['','11','20.5']);
  assert.equal(result.recipes[0].indicator,'sma');
  const plan=JSON.parse(await fs.readFile(request.planFile));
  assert.equal(await fs.readFile(plan.inputs[0].input,'utf8'),old);
});

test('weekly refresh derives daily corrections and additions without provider calls, and replays fields',async t=>{
  const old=csv([[start,10,12,9,11,'0.1',1]],[...headers,'weekday']);
  const request=await fixture(t,[csv(Array.from({length:14},(_,i)=>candle(i)))],[old]);
  const plan=JSON.parse(await fs.readFile(request.planFile));
  const output=path.join(path.dirname(request.output),'refresh.csv');
  const result=await refresh({...request,input:plan.inputs[0].input,output,recipes:[]},{emit:()=>{},download:()=>assert.fail('weekly refresh reached provider')});
  assert.equal(result.rows,2);assert.equal(result.addedRows,1);assert.equal(result.updatedRows,1);
  assert.deepEqual((await read(output)).map(row=>row.weekday),['1','1']);
  assert.equal(await fs.readFile(plan.inputs[0].input,'utf8'),old);
});

test('many legacy daily ranges merge with bounded streams and deduplicate overlapping days',async t=>{
  const request=await fixture(t,Array.from({length:18},(_,i)=>csv([candle(i)])));
  await download(request,{emit:()=>{}});
  assert.equal((await read(request.output)).length,3);
});

test('weekly refresh restores required fields from weekly bars before returning the refreshed history',async t=>{
  const request=await fixture(t,[csv(Array.from({length:14},(_,i)=>candle(i)))],[csv([[start,10,12,9,11,'0.1']])]);
  const plan=JSON.parse(await fs.readFile(request.planFile));
  const output=path.join(path.dirname(request.output),'refresh.csv');
  const result=await refresh({...request,input:plan.inputs[0].input,output,requiredFields:['sma_2','weekday']},{emit:()=>{},download:()=>assert.fail('weekly refresh reached provider')});
  assert.deepEqual((await read(output)).map(row=>row.sma_2),['','20.5']);
  assert.deepEqual(result.columns,['sma_2','weekday']);
});

test('daily edits during aggregation abort completion and remove all owned aggregation scratch',async t=>{
  const request=await fixture(t,[csv([candle(0)])]);
  const plan=JSON.parse(await fs.readFile(request.dailyPlanFile));
  const sync=require('node:fs');let completed=false;
  await assert.rejects(download(request,{emit:event=>{
    if(event.type==='progress'&&event.progress===60)sync.appendFileSync(plan.inputs[0].input,'changed\n');
    if(event.type==='done')completed=true;
  }}),/changed/);
  assert.equal(completed,false);
  assert.equal((await fs.readdir(path.dirname(request.output))).some(name=>name.startsWith('.weekly-')),false);
});

test('weekly source validation rejects missing plans, changed inputs, malformed candles and non-daily boundaries',async t=>{
  const request=await fixture(t,[csv([candle(0)])]);
  await assert.rejects(download({...request,dailyPlanFile:undefined},{emit:()=>{}}),/retained d1/);
  const plan=JSON.parse(await fs.readFile(request.dailyPlanFile));
  await fs.appendFile(plan.inputs[0].input,'garbage\n');
  await assert.rejects(download(request,{emit:()=>{}}),/changed/);
  const invalid=await fixture(t,[csv([[start,10,8,9,11,1]])]);
  await assert.rejects(download(invalid,{emit:()=>{}}),/invalid OHLC/);
  const shifted=await fixture(t,[csv([[start+1,10,12,9,11,1]])]);
  await assert.rejects(download(shifted,{emit:()=>{}}),/00:00 UTC/);
});

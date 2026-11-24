'use strict';
const test=require('node:test');
const assert=require('node:assert/strict');
const {aggregateRatio,bucket,instruments}=require('./synthetic-ratio.cjs');
const collect=async it=>{const rows=[];for await(const row of it)rows.push(row);return rows;};
const opts={maxAgeMs:1000,isSessionOpen:()=>true,sessionAt:()=>1};
test('simultaneous quotes do not introduce fictitious extrema',async()=>{
  const rows=await collect(aggregateRatio([[0,10],[100,20],[200,30]],[[0,2],[100,4],[200,5]],opts));
  assert.deepEqual(rows,[{timestamp:0,open:5,high:6,low:5,close:6}]);
});
test('stale quotes and closed sessions are excluded',async()=>{
  const rows=await collect(aggregateRatio([[0,10],[100,100],[2000,999],[3000,20]],[[0,2],[3000,4]],{...opts,isSessionOpen:t=>t!==100}));
  assert.deepEqual(rows,[{timestamp:0,open:5,high:5,low:5,close:5}]);
});
test('weekly buckets start Monday UTC',()=>{
  assert.equal(bucket(Date.UTC(2026,10,29),'w1'),Date.UTC(2026,10,23));
  assert.equal(bucket(Date.UTC(2026,10,30),'w1'),Date.UTC(2026,10,30));
  assert.throws(()=>bucket(0,'h1'));
});
test('invalid and unsorted inputs fail',async()=>{
  await assert.rejects(collect(aggregateRatio([[10,10],[0,5]],[[0,2]],opts)),/unordered/);
  await assert.rejects(collect(aggregateRatio([[0,10]],[[0,0]],opts)),/invalid/i);
});
test('five instruments and native preference metadata',()=>{
  assert.equal(Object.keys(instruments).length,5);
  assert.equal(instruments.xauxag.native,'xauxag');
  assert.equal(instruments.xauspx.denominator,'usa500idxusd');
});
test('production exact-millisecond observations never carry unmatched source quotes',async()=>{
  const rows=await collect(aggregateRatio([[0,10],[10,999],[20,20],[30,30]],[[0,2],[15,1],[20,4]],{maxAgeMs:0}));
  assert.deepEqual(rows,[{timestamp:0,open:5,high:5,low:5,close:5}]);
  assert.equal(Object.hasOwn(rows[0],'volume'),false);
});
test('fresh asynchronous quotes require verified session tokens and do not cross an unseen closure',async()=>{
  await assert.rejects(collect(aggregateRatio([[0,10]],[[0,2]],{maxAgeMs:1000})),/verified common-session/);
  const rows=await collect(aggregateRatio([[0,10],[100,100]],[[0,2],[200,20]],{maxAgeMs:1000,sessionAt:t=>t<50?'before':t>=100?'after':null}));
  assert.deepEqual(rows,[{timestamp:0,open:5,high:5,low:5,close:5}]);
});
test('same-timestamp duplicates settle both source sides before observing a ratio',async()=>{
  const rows=await collect(aggregateRatio([[0,10],[0,20],[10,30]],[[0,2],[0,4],[10,5]],{maxAgeMs:0}));
  assert.deepEqual(rows,[{timestamp:0,open:5,high:6,low:5,close:6}]);
});
test('cancellation and consumer cleanup close both source iterators',async()=>{
  const controller=new AbortController();let closed=0;
  async function* source(){try{yield [0,10];controller.abort();yield [1,20];}finally{closed++;}}
  await assert.rejects(collect(aggregateRatio(source(),source(),{signal:controller.signal})),/abort/i);
  assert.equal(closed,2,'both source iterators close when a source read aborts');
  let returned=0;
  const values=()=>({[Symbol.iterator](){let at=0;return {next(){return at++<2?{value:[at*86400000,2],done:false}:{done:true};},return(){returned++;return {done:true};}};}});
  for await(const row of aggregateRatio(values(),values())){assert.ok(row);break;}
  assert.equal(returned,2);
});
test('verified same-session asynchronous quotes form causal observed OHLC',async()=>{
  const rows=await collect(aggregateRatio([[0,10],[200,12],[400,8]],[[0,2],[100,4],[300,2]],{maxAgeMs:1000,sessionAt:()=> 'verified-session'}));
  assert.deepEqual(rows,[{timestamp:0,open:5,high:6,low:2.5,close:4}]);
});
test('verified weekend and holiday session tokens prevent carry even when quote age would permit it',async()=>{
  const friday=Date.UTC(2026,10,20,21,59),sunday=Date.UTC(2026,10,22,22),holiday=Date.UTC(2026,10,23,12),tuesday=Date.UTC(2026,10,24,9);
  const sessionAt=t=>t>=friday&&t<friday+60000?'friday':t>=sunday&&t<sunday+3600000?'sunday':t>=tuesday?'tuesday':null;
  const rows=await collect(aggregateRatio([[friday,10],[sunday+1000,20],[holiday,999],[tuesday+1000,10]],[[friday,2],[sunday,4],[tuesday,2]],
    {maxAgeMs:3*86400000,sessionAt,isSessionOpen:t=>sessionAt(t)!==null}));
  assert.deepEqual(rows.map(value=>[value.timestamp,value.open,value.high,value.low,value.close]),[
    [bucket(friday,'d1'),5,5,5,5],[bucket(sunday,'d1'),5,5,5,5],[bucket(tuesday,'d1'),5,5,5,5]]);
});
test('daily observed ratios split at exact UTC midnight without carrying an invented bar',async()=>{
  const midnight=Date.UTC(2026,10,24);
  const rows=await collect(aggregateRatio([[midnight-1,10],[midnight,20]],[[midnight-1,2],[midnight,2]]));
  assert.deepEqual(rows.map(value=>[value.timestamp,value.open,value.close]),[[midnight-86400000,5,5],[midnight,10,10]]);
});
test('nonfinite, negative and zero denominator quotes always fail',async()=>{
  for(const invalid of [NaN,Infinity,-Infinity,-1,0])await assert.rejects(collect(aggregateRatio([[0,10]],[[0,invalid]])),/Invalid/);
});

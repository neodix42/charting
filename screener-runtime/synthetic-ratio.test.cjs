'use strict';
const test=require('node:test');
const assert=require('node:assert/strict');
const {aggregateRatio,bucket,instruments}=require('./synthetic-ratio.cjs');
const collect=async it=>{const rows=[];for await(const row of it)rows.push(row);return rows;};
const opts={maxAgeMs:1000,isSessionOpen:()=>true};
test('simultaneous quotes do not introduce fictitious extrema',async()=>{
  const rows=await collect(aggregateRatio([[0,10],[100,20],[200,30]],[[0,2],[100,4],[200,5]],opts));
  assert.deepEqual(rows,[{timestamp:0,open:5,high:6,low:5,close:6,volume:0}]);
});
test('stale quotes and closed sessions are excluded',async()=>{
  const rows=await collect(aggregateRatio([[0,10],[100,100],[2000,999],[3000,20]],[[0,2],[3000,4]],{...opts,isSessionOpen:t=>t!==100}));
  assert.deepEqual(rows,[{timestamp:0,open:5,high:5,low:5,close:5,volume:0}]);
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

'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs/promises'),path=require('node:path'),os=require('node:os');
const {parse}=require('csv-parse/sync');
const {calculator,enrich,catalog}=require('./enrich-worker.cjs');
const {recipesFor,refresh}=require('./refresh-worker.cjs');
const {extendDownload}=require('./incremental-worker.cjs');
const headers=['timestamp','open','high','low','close','volume'];
const rows=[
 ['2024-01-01T00:00Z',8,12],['2024-01-01T23:00Z',6,15],['2024-01-02T00:00Z',9,11],
 ['2024-01-02T12:00Z',5,20],['2024-01-05T23:00Z',7,19],['2024-01-08T00:00Z',10,18],
 ['2024-01-08T23:00Z',4,25],['2024-01-09T00:00Z',6,10],['2024-01-15T00:00Z',3,13],
].map(([time,low,high])=>[Date.parse(time),(low+high)/2,high,low,(low+high)/2,1].map(String));
const expected={
 today_low:['8','6','9','5','7','10','4','6','3'],today_high:['12','15','11','20','19','18','25','10','13'],
 previous_day_low:['','','6','6','5','7','7','4','6'],previous_day_high:['','','15','15','20','19','19','25','10'],
 week_low:['8','6','6','5','5','10','4','4','3'],week_high:['12','15','15','20','20','18','25','25','13'],
 previous_week_low:['','','','','','5','5','5','4'],previous_week_high:['','','','','','20','20','20','25'],
};
const request=indicator=>({indicator,parameters:{},source:'askprice',timeframe:'h1'});
const csv=(data=rows)=>`${headers.join(',')}\n${data.map(row=>row.join(',')).join('\n')}\n`;
async function fixture(t,text=csv()){
 const directory=await fs.mkdtemp(path.join(os.tmpdir(),'cryptodesk-day-week-'));
 t.after(()=>fs.rm(directory,{recursive:true,force:true}));
 const input=path.join(directory,'input.csv'),output=path.join(directory,'output.csv');await fs.writeFile(input,text);return {input,output};
}

test('all eight ranges use UTC day/Monday week boundaries, carry the previous available period, and never leak future extrema',async()=>{
 const {calendarRangeNames}=await import('./calendar-ranges.mjs');
 assert.deepEqual(Object.keys(calendarRangeNames),Object.keys(expected));
 for(const [id,values] of Object.entries(expected)){
  const definition=catalog.indicators.find(item=>item.id===id);assert.equal(definition.name,calendarRangeNames[id]);assert.equal(definition.intradayOnly,true);
  const calculate=await calculator(request(id),headers);assert.deepEqual(calculate.columns,[id]);
  assert.deepEqual(rows.map((row,i)=>calculate.next(row,i+2)[0]),values,id);
  for(let end=1;end<=rows.length;end++){
   const prefix=await calculator(request(id),headers);
   assert.deepEqual(rows.slice(0,end).map((row,i)=>prefix.next(row,i+2)[0]),values.slice(0,end),`${id} prefix ${end}`);
  }
 }
});

test('first partial periods stay blank, including their previous-period values, until complete history is present',async()=>{
 const partial=rows.slice(1);
 for(const id of Object.keys(expected)){
  const calculate=await calculator(request(id),headers),actual=partial.map((row,i)=>calculate.next(row,i+2)[0]);
  assert.equal(actual[0],'',id);
  if(id.startsWith('previous_day'))assert.equal(actual[1],'',id);
  if(id.includes('week'))assert.equal(actual[3],'',id);
  if(id.startsWith('previous_week'))assert.equal(actual[4],'',id);
 }
});

test('calendar weeks cross years and retain UTC rather than local DST boundaries',async()=>{
 const {calendarRange}=await import('./calendar-ranges.mjs');
 const next=calendarRange('previous_week_low');
 assert.equal(next(Date.parse('2024-12-30T00:00Z'),10),undefined);
 assert.equal(next(Date.parse('2025-01-01T00:00Z'),5),undefined);
 assert.equal(next(Date.parse('2025-01-05T23:00Z'),7),undefined);
 assert.equal(next(Date.parse('2025-01-06T00:00Z'),2),5);
 const day=calendarRange('today_high');
 assert.equal(day(Date.parse('2024-03-31T00:00Z'),10),10);
 assert.equal(day(Date.parse('2024-03-31T23:00Z'),15),15);
 assert.equal(day(Date.parse('2024-04-01T00:00Z'),8),8);
});

test('only supported intraday candle periods are accepted; prices/timestamps must be usable',async()=>{
 for(const id of Object.keys(expected)){
  for(const timeframe of ['tick','d1','w1','mn1','',undefined])await assert.rejects(calculator({...request(id),timeframe},headers),/below D1/);
  for(const timeframe of ['s1','m1','m5','m15','m30','h1','h4'])await calculator({...request(id),timeframe},headers);
  await assert.rejects(calculator(request(id),['timestamp','close','bidprice']),/Requires timestamp/);
  for(const invalid of ['', 'NaN','Infinity']){
   const calculate=await calculator(request(id),headers),bad=[...rows[0]];bad[id.endsWith('_low')?3:2]=invalid;
   assert.throws(()=>calculate.next(bad,2),/finite|candle prices/);
  }
 }
});

test('CSV publication appends/replaces exact columns and recipe recovery includes all eight fields',async t=>{
 let {input,output}=await fixture(t);
 const original=await fs.readFile(input);
 for(const id of Object.keys(expected)){
  await enrich({...request(id),input,output},{emit:()=>{}});
  const actual=parse(await fs.readFile(output),{columns:true});assert.deepEqual(actual.map(row=>row[id]),expected[id],id);
  input=output;output=path.join(path.dirname(output),`${id}-next.csv`);
 }
 assert.equal(original.toString(),csv());
 const enriched=parse(await fs.readFile(input),{columns:true}),fields=Object.keys(enriched[0]);
 const recovered=await recipesFor(fields,[],[],'h1');assert.deepEqual(recovered.map(recipe=>recipe.indicator),Object.keys(expected));
 assert.ok(recovered.every(recipe=>recipe.source==='auto'&&Object.keys(recipe.parameters).length===0));
 const required=await recipesFor(headers,[],Object.keys(expected),'h1');assert.deepEqual(required,recovered);
 await assert.rejects(recipesFor(fields,[],[],'d1'),/below D1/);
});

test('Refresh repairs historical extrema and calculates new daily fields from earlier rows without future leakage',async t=>{
 const paths=await fixture(t,`${headers.join(',')},today_low,previous_day_low\n${rows.slice(0,3).map(row=>`${row.join(',')},999,999`).join('\n')}\n`);
 const revised=rows.slice(0,4).map(row=>[...row]);revised[1][3]='4';
 await refresh({...paths,symbol:'eurusd',timeframe:'h1',from:'2024-01-01',requiredFields:['week_high']},{now:Date.parse('2024-01-03T00:00Z'),emit:()=>{},download:async request=>fs.writeFile(request.output,csv(revised))});
 const actual=parse(await fs.readFile(paths.output),{columns:true});
 assert.deepEqual(actual.map(row=>row.today_low),['8','4','9','5']);assert.deepEqual(actual.map(row=>row.previous_day_low),['','','4','4']);assert.deepEqual(actual.map(row=>row.week_high),['12','15','15','20']);
});

test('extending history recovers range recipes and repairs warmup after an earlier UTC day is prepended',async t=>{
 const original=`${headers.join(',')},today_low,previous_day_low\n${rows[2].join(',')},999,999\n`;
 const paths=await fixture(t,original),directory=path.dirname(paths.input),work=path.join(directory,'work');await fs.mkdir(work);
 const info=await fs.stat(paths.input),planFile=path.join(work,'plan.json'),output=path.join(work,'merged.csv');
 await fs.writeFile(planFile,JSON.stringify({version:1,asOf:Date.parse('2024-01-03T00:00Z'),inputs:[{input:paths.input,name:'original.csv',bytes:info.size,modified:Math.trunc(info.mtimeMs),from:'2024-01-01',metadata:{}}]}));
 await extendDownload({symbol:'eurusd',timeframe:'h1',from:'2024-01-01',to:'2024-01-03',planFile,output},{emit:()=>{},download:async(request,options)=>{
  const data=rows.slice(0,4).filter(row=>Number(row[0])>=Date.parse(request.from)&&Number(row[0])<Date.parse(request.to));
  await fs.writeFile(request.output,csv(data));options.emit({type:'done',rows:data.length});
 }});
 const actual=parse(await fs.readFile(output),{columns:true});
 assert.deepEqual(actual.map(row=>row.today_low),['8','6','9','5']);assert.deepEqual(actual.map(row=>row.previous_day_low),['','','6','6']);
 assert.equal(await fs.readFile(paths.input,'utf8'),original);
});

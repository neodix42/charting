'use strict';
const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const os = require('node:os');
const {spawnSync}=require('node:child_process');
const {parse} = require('csv-parse/sync');
const {enrich,calculator,catalog,validate} = require('./enrich-worker.cjs');

const header = ['timestamp','open','high','low','close','volume'];
function candles(count=250) {
  return Array.from({length:count},(_,i) => {
    const price=100+i/10+Math.sin(i/3)*10;
    return [1704067200000+i*60000,price-1,price+2,price-2,price,10+i%7].map(String);
  });
}
async function fixture(t, csv) {
  const directory=await fs.mkdtemp(path.join(os.tmpdir(),'cryptodesk-enrichment-'));
  t.after(()=>fs.rm(directory,{recursive:true,force:true}));
  const input=path.join(directory,'input.csv'),output=path.join(directory,'output.csv');
  await fs.writeFile(input,csv);
  return {input,output};
}
const request=(indicator,parameters={})=>({indicator,parameters,source:'auto'});

test('all 40 reviewed indicators and 2 UTC time fields produce finite, aligned values',async()=>{
  assert.equal(catalog.version,'8.3.0');
  assert.equal(catalog.indicators.length,42);
  assert.equal(catalog.indicators.filter(item=>item.input==='timestamp').length,2);
  const rows=candles(500);
  for (const item of catalog.indicators) {
    const calculate=await calculator(request(item.id),header);
    assert.ok(calculate.columns.every(name=>/^[a-z0-9_.]+$/.test(name)),item.id);
    let populated=0;
    for (const [index,row] of rows.entries()) {
      const result=calculate.next(row,index+2);
      assert.equal(result.length,calculate.columns.length,item.id);
      assert.ok(result.every(value=>value===''||Number.isFinite(Number(value))),item.id);
      if (result.every(value=>value!=='')) populated++;
    }
    assert.ok(populated>0,`${item.id} must produce stable values after warm-up`);
  }
});

test('every indicator also works at its minimum supported period instead of accepting unusable library inputs',async()=>{
  for(const item of catalog.indicators){
    const parameters=Object.fromEntries(item.parameters.map(param=>[param.key,param.step===1?param.min:param.default]));
    if('fast' in parameters)parameters.slow=parameters.fast+1;
    const calculate=await calculator(request(item.id,parameters),header);
    let populated=0;
    for(const [i,row] of candles(100).entries())if(calculate.next(row,i+2).every(value=>value!==''))populated++;
    assert.ok(populated>0,`${item.id} minimum period must produce defined values`);
  }
  for(const indicator of ['hma','rsi','cci','stochrsi','obv'])assert.throws(()=>validate(request(indicator,{period:1})),/must be between 2/);
});

test('SMA 200 appends exact values on the right rows, preserves other fields and reports progress',async t=>{
  const rows=Array.from({length:205},(_,i)=>[1704067200000+i*60000,i+1,i+2,i,i+1,10]);
  const csv=`\uFEFF${header.join(',')},notes,prior_indicator\r\n${rows.map((row,i)=>`${row.join(',')},"value, ${i}\nquoted ""text""",`).join('\r\n')}\r\n`;
  const paths=await fixture(t,csv),events=[];
  await enrich({...paths,...request('sma',{period:200})},{emit:event=>events.push(event)});
  assert.equal(await fs.readFile(paths.input,'utf8'),csv);
  const result=parse(await fs.readFile(paths.output),{columns:true});
  assert.equal(result.length,205);
  assert.deepEqual(Object.keys(result[0]),[...header,'notes','prior_indicator','sma_200']);
  assert.equal(result[198].sma_200,'');
  assert.equal(result[199].sma_200,'100.5');
  assert.equal(result[204].sma_200,'105.5');
  assert.equal(result[100].notes,'value, 100\nquoted "text"');
  assert.equal(result[100].prior_indicator,'');
  assert.equal(result[100].timestamp,String(rows[100][0]));
  assert.equal(events.at(-1).type,'done');
  assert.equal(events.at(-1).rows,205);
  assert.deepEqual(events.at(-1).columns,['sma_200']);
  assert.ok(events.some(event=>event.type==='progress'&&event.rows>0));
});

test('matching indicator columns are replaced without duplicates and prior enrichment survives',async t=>{
  const rows=candles(6);
  const paths=await fixture(t,`${header.join(',')},SMA_3,ema_20\n${rows.map(row=>`${row.join(',')},stale,prior`).join('\n')}\n`);
  await enrich({...paths,...request('sma',{period:3})},{emit:()=>{}});
  const result=parse(await fs.readFile(paths.output),{columns:true});
  assert.deepEqual(Object.keys(result[0]),[...header,'SMA_3','ema_20']);
  assert.equal(result[0].SMA_3,'');
  assert.equal(result[2].SMA_3,String(rows.slice(0,3).reduce((n,row)=>n+Number(row[4]),0)/3));
  assert.ok(result.every(row=>row.ema_20==='prior'));
  const next=path.join(path.dirname(paths.output),'second.csv');
  await enrich({input:paths.output,output:next,...request('sma',{period:3})},{emit:()=>{}});
  assert.equal(await fs.readFile(next,'utf8'),await fs.readFile(paths.output,'utf8'));
});

test('MACD uses EMA inputs and waits for the signal EMA warm-up',async()=>{
  const api=await import('trading-signals');
  const calculate=await calculator(request('macd',{fast:3,slow:5,signal:3}),header);
  assert.deepEqual(calculate.columns,['macd_3_5','macd_signal_3_5_3','macd_histogram_3_5_3']);
  const reference=new api.MACD(new api.EMA(3),new api.EMA(5),new api.EMA(3));
  for (const [i,row] of candles(40).entries()) {
    const actual=calculate.next(row,i+2);
    reference.add(Number(row[4]));
    const expected=reference.getResult();
    assert.equal(actual[0],i<4?'':String(expected.macd));
    assert.equal(actual[1],i<6?'':String(expected.signal));
    assert.equal(actual[2],i<6?'':String(expected.histogram));
  }
});

test('Ichimoku lines are causal, with independent warm-up periods and readable names',async()=>{
  const rows=candles(70);
  const calculate=await calculator(request('ichimoku'),header);
  assert.deepEqual(calculate.columns,['tenkan_9','kijun_26','senkou_a_9_26','senkou_b_52']);
  const midpoint=(i,period)=>(Math.max(...rows.slice(i-period+1,i+1).map(row=>Number(row[2])))+Math.min(...rows.slice(i-period+1,i+1).map(row=>Number(row[3]))))/2;
  for (const [i,row] of rows.entries()) {
    const actual=calculate.next(row,i+2);
    assert.equal(actual[0],i<8?'':String(midpoint(i,9)));
    assert.equal(actual[1],i<25?'':String(midpoint(i,26)));
    assert.equal(actual[2],i<25?'':String((midpoint(i,9)+midpoint(i,26))/2));
    assert.equal(actual[3],i<51?'':String(midpoint(i,52)));
  }
  const tenkan=await calculator(request('tenkan',{period:9}),header);
  assert.deepEqual(tenkan.columns,['tenkan_9']);
});

test('tick prices support automatic bid and explicit ask sources; candle indicators reject tick files',async()=>{
  const tickHeader=['timestamp','askPrice','bidPrice','askVolume','bidVolume'];
  const bid=await calculator(request('sma',{period:2}),tickHeader);
  const ask=await calculator({...request('sma',{period:2}),source:'askprice'},tickHeader);
  assert.deepEqual(bid.columns,['sma_2_bidprice']);
  assert.deepEqual(ask.columns,['sma_2_askprice']);
  const first=['1700000000000','12','10','3','4'],second=['1700000000000','14','11','3','4'];
  assert.deepEqual(bid.next(first,2),['']);assert.deepEqual(ask.next(first,2),['']);
  assert.deepEqual(bid.next(second,3),['10.5']);assert.deepEqual(ask.next(second,3),['13']);
  await assert.rejects(calculator(request('tenkan'),tickHeader),/Requires high, low/);
  await assert.rejects(calculator({...request('sma'),source:'close'},tickHeader),/Requires close/);
});

test('UTC Weekday and Hour stream timestamp-only CSVs across every weekday and midnight without warm-up or duplicate columns',async t=>{
  const monday=Date.UTC(2024,0,1);
  const rows=[[-8640000000000000,2,0],[-1,3,23],[0,4,0],[Date.UTC(1973,2,1),4,0],
    ...Array.from({length:7},(_,i)=>[[monday+i*86400000,i+1,0],[monday+i*86400000+23*3600000,i+1,23]]).flat(),
    [Date.UTC(2024,0,7,23,59,59,999),7,23],[Date.UTC(2024,0,8),1,0],[8640000000000000,6,0]];
  const original=`timestamp,notes\n${rows.map(([timestamp],i)=>`${timestamp},"row, ${i}"`).join('\n')}\n`;
  const paths=await fixture(t,original);
  const events=[];
  const weekday=await enrich({...paths,...request('weekday')},{emit:event=>events.push(event)});
  assert.deepEqual(weekday.columns,['weekday']);assert.equal(weekday.rows,rows.length);
  const output=path.join(path.dirname(paths.output),'both.csv');
  await enrich({input:paths.output,output,...request('hour')},{emit:()=>{}});
  const actual=parse(await fs.readFile(output),{columns:true});
  assert.deepEqual(Object.keys(actual[0]),['timestamp','notes','weekday','hour']);
  actual.forEach((row,i)=>{
    assert.equal(row.timestamp,String(rows[i][0]));assert.equal(row.weekday,String(rows[i][1]));assert.equal(row.hour,String(rows[i][2]));assert.equal(row.notes,`row, ${i}`);
  });
  const repeated=path.join(path.dirname(paths.output),'repeated.csv');
  await enrich({input:output,output:repeated,...request('weekday')},{emit:()=>{}});
  assert.equal(await fs.readFile(repeated,'utf8'),await fs.readFile(output,'utf8'));
  assert.equal(await fs.readFile(paths.input,'utf8'),original);
  assert.ok(events.some(event=>event.type==='progress'));
});

test('time fields normalize price source, accept tick timestamps and reject missing, malformed or out-of-range dates',async t=>{
  for(const indicator of ['weekday','hour']){
    const definition=catalog.indicators.find(item=>item.id===indicator);
    assert.equal(definition.input,'timestamp');assert.equal(definition.category,'Time');assert.deepEqual(definition.parameters,[]);
    for(const source of ['auto','close','bidprice'])assert.equal(validate({...request(indicator),source}).source,'auto');
    const calculate=await calculator({...request(indicator),source:'bidprice'},['timestamp','askPrice','bidPrice','askVolume','bidVolume']);
    assert.deepEqual(calculate.columns,[indicator]);
    assert.deepEqual(calculate.next([String(Date.UTC(2023,11,31,23,59)),'10','9','1','2'],2),[indicator==='weekday'?'7':'23']);
    const tickPaths=await fixture(t,`timestamp,askPrice,bidPrice,askVolume,bidVolume\n${Date.UTC(2023,11,31,23,59)},10,9,1,2\n${Date.UTC(2023,11,31,23,59)},11,10,2,3\n${Date.UTC(2024,0,1)},12,11,3,4\n`);
    await enrich({...tickPaths,...request(indicator)},{emit:()=>{}});
    const ticks=parse(await fs.readFile(tickPaths.output),{columns:true});
    assert.deepEqual(ticks.map(row=>row[indicator]),indicator==='weekday'?['7','7','1']:['23','23','0']);
    assert.deepEqual(ticks.map(row=>row.bidPrice),['9','10','11']);
    for(const value of ['', ' ', 'not-a-date','Infinity','NaN','2024-01-01T00:00:00Z','8640000000000001','-8640000000000001']){
      assert.throws(()=>calculate.next([value],2),/timestamp/);
      const paths=await fixture(t,`timestamp\n${value===''?'""':value}\n`);
      await assert.rejects(enrich({...paths,...request(indicator)},{emit:()=>{}}),/timestamp/);
      assert.equal(await fs.readFile(paths.input,'utf8'),`timestamp\n${value===''?'""':value}\n`);
    }
    await assert.rejects(calculator(request(indicator),['close']),/Requires timestamp/);
    const paths=await fixture(t,'close\n10\n');
    await assert.rejects(enrich({...paths,...request(indicator)},{emit:()=>{}}),/Requires timestamp/);
    assert.throws(()=>validate({...request(indicator),parameters:{period:1}}),/Unsupported parameter/);
  }
});

test('UTC calendar values remain independent of the child process timezone',()=>{
  const worker=path.join(__dirname,'enrich-worker.cjs');
  const source=`const {calculator}=require(${JSON.stringify(worker)});(async()=>{const result=[];for(const indicator of ['weekday','hour']){const c=await calculator({indicator,source:'auto'},['timestamp']);result.push(c.next(['${Date.UTC(2023,11,31,23,59)}'],2),c.next(['${Date.UTC(2024,0,1)}'],3));}process.stdout.write(JSON.stringify(result));})().catch(error=>{process.stderr.write(error.stack);process.exitCode=1;});`;
  for(const timezone of ['UTC','Pacific/Honolulu','Asia/Dubai','Pacific/Kiritimati']){
    const child=spawnSync(process.execPath,['-e',source],{encoding:'utf8',env:{...process.env,TZ:timezone}});
    assert.equal(child.status,0,child.stderr);
    assert.deepEqual(JSON.parse(child.stdout),[['7'],['1'],['23'],['0']],timezone);
  }
});

test('invalid requests, duplicate headers and unknown parameters are rejected',async()=>{
  for (const value of [request('unknown'),request('sma',{period:0}),request('sma',{period:1.2}),request('sma',{period:Infinity}),request('sma',{period:10001}),request('sma',{script:1}),request('macd',{fast:26,slow:12}),{...request('sma'),source:'eval'}]) assert.throws(()=>validate(value));
  await assert.rejects(calculator(request('sma'),['close',' CLOSE ']),/duplicate/);
  await assert.rejects(calculator(request('sma'),['close','']),/empty/);
});

test('malformed data, missing prices, invalid candles and descending timestamps leave the input intact',async t=>{
  for (const [name,csv,indicator] of [
    ['bad-row',`${header.join(',')}\n1,1,2,0,1,3\n2,1,2,0\n`,'sma'],
    ['blank-price',`${header.join(',')}\n1,1,2,0,,3\n`,'sma'],
    ['bad-candle',`${header.join(',')}\n1,1,0,2,1,3\n`,'atr'],
    ['bad-time',`${header.join(',')}\n2,1,2,0,1,3\n1,1,2,0,1,3\n`,'sma'],
    ['header-only',`${header.join(',')}\n`,'sma'],
  ]) {
    await t.test(name,async sub=>{
      const paths=await fixture(sub,csv);
      await assert.rejects(enrich({...paths,...request(indicator,{period:2})},{emit:()=>{}}));
      assert.equal(await fs.readFile(paths.input,'utf8'),csv);
    });
  }
});

test('exclusive output, interruption and missing input never overwrite source data',async t=>{
  const csv=`${header.join(',')}\n${candles(20).map(row=>row.join(',')).join('\n')}\n`;
  const paths=await fixture(t,csv);
  await fs.writeFile(paths.output,'existing output');
  await assert.rejects(enrich({...paths,...request('sma',{period:2})},{emit:()=>{}}),/EEXIST/);
  assert.equal(await fs.readFile(paths.output,'utf8'),'existing output');
  await fs.unlink(paths.output);
  await assert.rejects(enrich({...paths,...request('sma',{period:2})},{emit:()=>{},onRow:rows=>{if(rows===10)throw Error('Interrupted');}}),/Interrupted/);
  assert.equal(await fs.readFile(paths.input,'utf8'),csv);
  await assert.rejects(enrich({input:path.join(path.dirname(paths.input),'missing.csv'),output:paths.output,...request('sma')}),/ENOENT/);
});

test('large files stream through bounded batches instead of loading the CSV into memory',async t=>{
  const count=50000;
  const paths=await fixture(t,`${header.join(',')}\n`);
  const handle=await fs.open(paths.input,'a');
  for(let batch=0;batch<count/1000;batch++)await handle.write(Array.from({length:1000},(_,i)=>`${batch*1000+i},1,2,0,1,3\n`).join(''));
  await handle.close();
  const result=await enrich({...paths,...request('sma',{period:20})},{emit:()=>{}});
  assert.equal(result.rows,count);
  const output=await fs.readFile(paths.output,'utf8');
  assert.equal(output.split('\n').length,count+2);
  assert.ok(output.endsWith(`${count-1},1,2,0,1,3,1\n`));
});

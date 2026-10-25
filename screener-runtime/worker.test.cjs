'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const {spawn} = require('node:child_process');
const {once} = require('node:events');
const api = require('dukascopy-node');
const { download } = require('./worker.cjs');

async function fixture(t) {
  const directory = await fs.promises.mkdtemp(path.join(os.tmpdir(), 'cryptodesk-screener-worker-'));
  t.after(() => fs.promises.rm(directory, { recursive: true, force: true }));
  const output = path.join(directory, '.owned.part');
  await fs.promises.writeFile(output, '');
  return { symbol: 'eurusd', timeframe: 'm5', from: '2024-01-01', to: '2024-01-07', output };
}

// Synthetic responses use the real pinned library's wire decoder, aggregator,
// formatter and BufferFetcher. Only transport is replaced, never candle logic.
function candlePayload(url) {
  const parts = new URL(url).pathname.split('/');
  const [year, month, day] = parts.slice(-3).map(Number);
  return Buffer.from(JSON.stringify({
    timestamp: Date.UTC(year, month - 1, day), shift: 60000, multiplier: 1,
    open: 100, high: 104, low: 99, close: 103,
    times: [0, 1, 1, 1, 1], opens: [0, 0, 0, 0, 0], highs: [0, 0, 0, 0, 0],
    lows: [0, 0, 0, 0, 0], closes: [0, 0, 0, 0, 0], volumes: [1, 1, 1, 1, 1],
  }));
}
function withTransport(fetcher, batches = []) {
  return { ...api, BufferFetcher: class extends api.BufferFetcher {
    constructor(options) { super({ ...options, fetcherFn: fetcher }); }
    async fetch(urls) { batches.push(urls.length); return super.fetch(urls); }
  } };
}

test('ordered bounded batches retain OHLCV, use one CSV header and report real requests', async t => {
  const request = await fixture(t);
  const batches = [];
  const events = [];
  let fetched = 0;
  const instrumented = withTransport(async url => { fetched++; return candlePayload(url); }, batches);
  const rows = await download(request, { api: instrumented, pauseMs: 0, emit(event) {
    if (event.progress > 0) assert.ok(fetched > 0, 'progress advanced before any transport completion');
    events.push(event);
  } });
  assert.equal(rows, 6);
  assert.deepEqual(batches, [4, 2]);
  const lines = (await fs.promises.readFile(request.output, 'utf8')).trim().split('\n');
  assert.equal(lines.filter(line => line.startsWith('timestamp,')).length, 1);
  assert.equal(lines[0], 'timestamp,open,high,low,close,volume');
  assert.deepEqual(lines.slice(1).map(line => line.split(',').map(Number)), Array.from({ length: 6 }, (_, i) => [Date.UTC(2024, 0, i + 1), 100, 104, 99, 103, 5]));
  const progress = events.filter(event => event.type === 'progress').map(event => event.progress);
  assert.equal(progress.length, 7);
  progress.forEach((value, index) => assert.ok(Math.abs(value - index * 95 / 6) < 1e-10));
  assert.equal(events.at(-1).type, 'done');
  assert.equal(events.at(-1).rows, 6);
});

test('empty ranges fail explicitly and do not confirm successful output', async t => {
  const request = await fixture(t);
  const events = [];
  const empty = withTransport(async () => Buffer.alloc(0));
  await assert.rejects(download(request, { api: empty, pauseMs: 0, emit: event => events.push(event) }), /no rows/);
  assert.equal(events.some(event => event.type === 'done'), false);
  assert.equal((await fs.promises.stat(request.output)).size, 0);
});

test('network failure propagates, never emits done, closes the output handle', async t => {
  const request = await fixture(t);
  const events = [];
  const failed = withTransport(async () => { throw new Error('Public endpoint HTTP 403'); });
  await assert.rejects(download(request, { api: failed, pauseMs: 0, emit: event => events.push(event) }), /HTTP 403/);
  assert.equal(events.some(event => event.type === 'done'), false);
  await fs.promises.unlink(request.output);
});

test('one-second and tick data use tick sources; simultaneous tick timestamps survive', async t => {
  const request = await fixture(t);
  request.timeframe = 'tick'; request.to = '2024-01-02';
  const tickApi = withTransport(async url => {
    assert.match(url, /\/v1\/ticks\//);
    const [year, month, day, hour] = new URL(url).pathname.split('/').slice(-4).map(Number);
    return Buffer.from(JSON.stringify({ timestamp: Date.UTC(year, month-1, day, hour),
      multiplier: 0.01, ask: 101, bid: 100,
      times: [0, 0], asks: [0, 1], bids: [0, 1], askVolumes: [10, 20], bidVolumes: [20, 30] }));
  });
  await download(request, { api: tickApi, pauseMs: 0, emit() {} });
  const lines = (await fs.promises.readFile(request.output, 'utf8')).trim().split('\n');
  assert.equal(lines[0], 'timestamp,askPrice,bidPrice,askVolume,bidVolume');
  assert.equal(lines.length, 49);
  assert.deepEqual(lines[1].split(',').map(Number), [Date.UTC(2024,0,1), 101, 100, 0.00001, 0.00002]);
  assert.equal(lines[1].split(',')[0], lines[2].split(',')[0]);
});

test('partial date boundaries filter complete upstream files without relabeling candles', async t => {
  const request = await fixture(t);
  request.timeframe = 'd1'; request.from = '2024-01-02'; request.to = '2024-01-03';
  const daily = withTransport(async url => {
    assert.match(url, /\/v1\/candles\/day\//);
    return Buffer.from(JSON.stringify({ timestamp: Date.UTC(2024,0,1), shift: 86400000, multiplier: 1,
      open: 10, high: 12, low: 9, close: 11,
      times: [0,1,1], opens: [0,1,1], highs: [0,1,1], lows: [0,1,1], closes: [0,1,1], volumes: [2,3,4] }));
  });
  await download(request, { api: daily, pauseMs: 0, emit() {} });
  const lines = (await fs.promises.readFile(request.output, 'utf8')).trim().split('\n');
  assert.equal(lines.length, 2);
  assert.deepEqual(lines[1].split(',').map(Number), [Date.UTC(2024,0,2), 11,13,10,12,3]);
});

test('invalid symbol and timeframe never reach the transport', async t => {
  const request = await fixture(t);
  const never = withTransport(async () => { assert.fail('invalid input reached transport'); });
  await assert.rejects(download({ ...request, symbol: 'not-a-pair' }, { api: never }), /Invalid Dukascopy request/);
  await assert.rejects(download({ ...request, timeframe: '1d' }, { api: never }), /Invalid Dukascopy request/);
});

test('manager source batch allowance bounds actual simultaneous fetches', async t => {
  for (const size of [1, 2, 3, 4]) {
    const request = { ...await fixture(t), sourceBatchSize: size };
    const batches = [];
    let active = 0;
    let peak = 0;
    const bounded = withTransport(async url => {
      active++;
      peak = Math.max(peak, active);
      await new Promise(resolve => setTimeout(resolve, 2));
      active--;
      return candlePayload(url);
    }, batches);
    assert.equal(await download(request, { api: bounded, pauseMs: 0, emit() {} }), 6);
    assert.equal(peak, size);
    assert.ok(batches.every(count => count <= size));
    assert.equal(active, 0);
  }
});

test('invalid internal source batch allowances fail before fetching', async t => {
  const request = await fixture(t);
  const never = withTransport(async () => assert.fail('invalid batch allowance reached transport'));
  for (const sourceBatchSize of [0, -1, 5, 20, 1.5, '2']) {
    await assert.rejects(download({ ...request, sourceBatchSize }, { api: never }), /batch size/);
  }
});

test('restart skips committed source batches, truncates uncommitted tails and keeps exact ordered CSV', async t => {
  const request = {...await fixture(t), resume:true, sourceBatchSize:2};
  const firstFetches=[];
  const interrupted=withTransport(async url=>{
    firstFetches.push(url);
    if(Number(new URL(url).pathname.split('/').at(-1))>=3)throw new Error('Explicit interrupted transport');
    return candlePayload(url);
  });
  await assert.rejects(download(request,{api:interrupted,pauseMs:0,emit(){}}),/interrupted/);
  const checkpoint=JSON.parse(await fs.promises.readFile(path.join(path.dirname(request.output),'checkpoint.json'),'utf8'));
  assert.equal(checkpoint.nextIndex,2);assert.equal(checkpoint.rows,2);
  const prefix=await fs.promises.readFile(request.output,'utf8');
  await fs.promises.appendFile(request.output,'an incomplete uncommitted CSV tail');
  const fetched=[],events=[];
  const resumed=withTransport(async url=>{fetched.push(url);return candlePayload(url);});
  assert.equal(await download({...request,sourceBatchSize:1},{api:resumed,pauseMs:0,emit:event=>events.push(event)}),6);
  assert.deepEqual(fetched.map(url=>Number(new URL(url).pathname.split('/').at(-1))),[3,4,5,6]);
  const csv=await fs.promises.readFile(request.output,'utf8');
  assert.ok(csv.startsWith(prefix));assert.equal(csv.includes('uncommitted'),false);
  const lines=csv.trim().split('\n');assert.equal(lines.length,7);
  assert.deepEqual(lines.slice(1).map(line=>Number(line.split(',')[0])),Array.from({length:6},(_,i)=>Date.UTC(2024,0,i+1)));
  assert.equal(events[0].progress,2/6*95);assert.match(events[0].message,/Resuming/);
  assert.equal(await download(request,{api:withTransport(async()=>assert.fail('completed sources were fetched twice')),emit(){}}),6);
  assert.equal(await fs.promises.readFile(request.output,'utf8'),csv);
});

test('truncated or mismatched saved data fails without silently restarting or changing prior rows', async t=>{
  const request={...await fixture(t),resume:true};
  await download(request,{api:withTransport(async url=>candlePayload(url)),pauseMs:0,emit(){}});
  const checkpointFile=path.join(path.dirname(request.output),'checkpoint.json');
  const checkpoint=JSON.parse(await fs.promises.readFile(checkpointFile,'utf8'));
  const bytes=await fs.promises.readFile(request.output);
  await fs.promises.truncate(request.output,3);
  await assert.rejects(download(request,{api:withTransport(async()=>assert.fail('corrupt data reached transport')),emit(){}}),/shorter/);
  assert.equal((await fs.promises.stat(request.output)).size,3);
  await fs.promises.writeFile(request.output,bytes);
  await fs.promises.writeFile(checkpointFile,JSON.stringify({...checkpoint,symbol:'btcusd'}));
  await assert.rejects(download(request,{emit(){}}),/another request/);
  assert.deepEqual(await fs.promises.readFile(request.output),bytes);
});

test('an owned worker exits when its app pipe closes, including abrupt parent shutdown', async t=>{
  const worker=path.join(__dirname,'worker.cjs');
  const code=`require(${JSON.stringify(worker)}).main(async()=>{process.stdout.write('READY\\n');await new Promise(()=>{});}).catch(error=>{process.stderr.write(error.message);process.exit(1);});`;
  const child=spawn(process.execPath,['-e',code],{stdio:['pipe','pipe','pipe']});
  t.after(()=>{if(child.exitCode===null)child.kill();});
  const ready=once(child.stdout,'data');const exit=once(child,'exit');
  child.stdin.write(JSON.stringify({ownerPipe:true})+'\n');
  await ready;child.stdin.end();
  const result=await Promise.race([exit,new Promise((_,reject)=>{const timer=setTimeout(()=>reject(new Error('orphan worker survived owner exit')),3000);timer.unref();})]);
  assert.equal(result[0],1);
});

test('malformed protocol exits even when the parent still owns an open input pipe',async t=>{
  const child=spawn(process.execPath,[path.join(__dirname,'worker.cjs')],{stdio:['pipe','pipe','pipe']});
  t.after(()=>{if(child.exitCode===null)child.kill();});
  const exit=once(child,'exit');child.stdin.write('{not valid JSON}\n');
  const result=await Promise.race([exit,new Promise((_,reject)=>{const timer=setTimeout(()=>reject(new Error('invalid protocol left a hanging worker')),3000);timer.unref();})]);
  assert.equal(result[0],1);
});

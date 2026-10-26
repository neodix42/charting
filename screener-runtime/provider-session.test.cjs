'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const {spawn} = require('node:child_process');
const {once} = require('node:events');
const api = require('dukascopy-node');
const {createProviderFetcher, accessError} = require('./provider-session.cjs');

async function fixture(t) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'cryptodesk-provider-'));
  t.after(() => fs.rm(root, {recursive: true, force: true}));
  const directory = path.join(root, 'batch');
  await fs.mkdir(directory);
  return {directory, tokenCache: path.join(root, 'waf-token.json')};
}

test('twenty independent processes share one initialization then fetch concurrently', async t => {
  const state = await fixture(t);
  const helper = path.join(state.directory, 'child.cjs');
  await fs.writeFile(helper, `
    const fs=require('node:fs/promises'), path=require('node:path');
    const {createProviderFetcher}=require(${JSON.stringify(path.join(__dirname, 'provider-session.cjs'))});
    const state=${JSON.stringify(state)};
    class BufferFetcher {
      constructor(config){if(config.wafTokenCachePath!==state.tokenCache)throw Error('Wrong cache path');}
      async fetch(urls){
        if(urls[0]==='init'){
          try{await fs.stat(state.tokenCache);}
          catch(error){if(error.code!=='ENOENT')throw error;await new Promise(r=>setTimeout(r,40));await fs.appendFile(path.join(state.directory,'acquisitions'),'one\\n');await fs.writeFile(state.tokenCache,'supported session fixture');}
        }else{
          await fs.writeFile(path.join(state.directory,process.pid+'.active'),'');
          const deadline=Date.now()+15000;
          while((await fs.readdir(state.directory)).filter(name=>name.endsWith('.active')).length<20){
            if(Date.now()>deadline)throw Error('Downloads were serialized after initialization');
            await new Promise(r=>setTimeout(r,10));
          }
        }
        return urls;
      }
    }
    (async()=>{const f=createProviderFetcher({BufferFetcher},{},state);await f.fetch(['init']);await f.fetch(['parallel']);})().catch(e=>{console.error(e);process.exitCode=1;});
  `);
  const children = Array.from({length: 20}, () => spawn(process.execPath, [helper], {stdio: ['ignore', 'pipe', 'pipe']}));
  t.after(() => children.forEach(child => { if (child.exitCode === null) child.kill(); }));
  await Promise.all(children.map(async child => {
    let stderr = ''; child.stderr.on('data', data => { stderr += data; });
    const [code] = await once(child, 'exit');
    assert.equal(code, 0, stderr);
  }));
  assert.equal(await fs.readFile(path.join(state.directory, 'acquisitions'), 'utf8'), 'one\n');
});

test('WAF browser 403 stops all waiting workers and later chunks without retrying', async t => {
  const state = await fixture(t);
  let requests = 0;
  class BufferFetcher {
    async fetch() {
      requests++;
      await new Promise(resolve => setTimeout(resolve, 10));
      throw new api.WafTokenError('Headless browser could not acquire a WAF token for https://jetta.dukascopy.com/v1/candles/day/AABV.US-USD/BID?from=1767225600000 (last HTTP status: 403, WAF action: none)');
    }
  }
  const workers = Array.from({length: 20}, () => createProviderFetcher({BufferFetcher}, {}, state));
  const results = await Promise.allSettled(workers.map(worker => worker.fetch(['initial'])));
  assert.equal(requests, 1);
  for (const result of results) {
    assert.equal(result.status, 'rejected');
    assert.match(result.reason.message, /Dukascopy refused access \(HTTP 403\)/);
    assert.match(result.reason.message, /saved data and checkpoints are retained/);
  }
  await assert.rejects(workers[0].fetch(['next']), /HTTP 403/);
  assert.equal(requests, 1);
  await assert.rejects(fs.stat(path.join(state.directory, 'initializing')), {code: 'ENOENT'});
});

test('real pinned transport reuses its supported session cache across workers and batches', async t => {
  const state = await fixture(t);
  const session = {version: 2, origin: 'https://jetta.dukascopy.com', token: 'test-session', userAgent: 'CryptoDesk session fixture'};
  await fs.writeFile(state.tokenCache, JSON.stringify(session), {mode: 0o600});
  const originalFetch = global.fetch;
  t.after(() => { global.fetch = originalFetch; });
  let requests = 0;
  global.fetch = async (_url, options) => {
    requests++;
    assert.equal(options.headers.cookie, `aws-waf-token=${session.token}`);
    assert.equal(options.headers['user-agent'], session.userAgent);
    return new Response(JSON.stringify({timestamp: 1704067200000, shift: 86400000, multiplier: 1,
      open: 10, high: 11, low: 9, close: 10, times: [0], opens: [0], highs: [0], lows: [0], closes: [0], volumes: [1]}),
    {headers: {'content-type': 'application/json'}});
  };
  const url = 'https://jetta.dukascopy.com/v1/candles/day/EUR-USD/BID/2024';
  const input = {retryCount: 0, failAfterRetryCount: true, wafMode: 'auto', wafTokenCache: true};
  const workers = Array.from({length: 20}, () => createProviderFetcher(api, input, state));
  const outputs = await Promise.all(workers.map(worker => worker.fetch([url])));
  assert.ok(outputs.every(output => output.length === 1 && output[0].buffer.length > 0));
  assert.equal(requests, 20);
  // Simulate a supported session renewal published by another worker. Existing
  // fetchers must load it before their next source chunk, without new browsers.
  session.token = 'renewed-test-session';
  await fs.writeFile(state.tokenCache, JSON.stringify(session), {mode: 0o600});
  await Promise.all(workers.map(worker => worker.fetch([url])));
  assert.equal(requests, 40);
  const next = {...state, directory: path.join(path.dirname(state.directory), 'next-batch')};
  await fs.mkdir(next.directory);
  await createProviderFetcher(api, input, next).fetch([url]);
  assert.equal(requests, 41);
  assert.deepEqual(JSON.parse(await fs.readFile(state.tokenCache, 'utf8')), session);
});

test('ordinary 403 is terminal on the real transport and a fresh batch can retry', async t => {
  const state = await fixture(t);
  const originalFetch = global.fetch;
  t.after(() => { global.fetch = originalFetch; });
  let requests = 0;
  global.fetch = async () => { requests++; return new Response('Forbidden', {status: 403}); };
  const input = {retryCount: 0, failAfterRetryCount: true, wafMode: 'auto'};
  const url = 'https://jetta.dukascopy.com/v1/candles/day/EUR-USD/BID/2024';
  await assert.rejects(createProviderFetcher(api, input, state).fetch([url]), /refused access \(HTTP 403\)/);
  await assert.rejects(createProviderFetcher(api, input, state).fetch([url]), /HTTP 403/);
  assert.equal(requests, 1);
  const next = {...state, directory: path.join(path.dirname(state.directory), 'next-batch')};
  await fs.mkdir(next.directory);
  await assert.rejects(createProviderFetcher(api, input, next).fetch([url]), /HTTP 403/);
  assert.equal(requests, 2);
});

test('the real pinned WAF flow acquires one supported browser session for twenty workers', async t => {
  const state = await fixture(t);
  const {chromium} = require('playwright-core');
  const originalLaunch = chromium.launch, originalFetch = global.fetch;
  t.after(() => { chromium.launch = originalLaunch; global.fetch = originalFetch; });
  const payload = JSON.stringify({timestamp: 1704067200000, shift: 86400000, multiplier: 1,
    open: 10, high: 11, low: 9, close: 10, times: [0], opens: [0], highs: [0], lows: [0], closes: [0], volumes: [1]});
  const url = 'https://jetta.dukascopy.com/v1/candles/day/EUR-USD/BID/2024';
  let launches = 0, closed = 0, requests = 0;
  chromium.launch = async () => {
    launches++;
    const response = {url: () => url, status: () => 200, headers: () => ({'content-type': 'application/json'}), body: async () => Buffer.from(payload)};
    return {close: async () => { closed++; }, newContext: async () => ({
      cookies: async () => [{name: 'aws-waf-token', value: 'supported-test-token'}],
      newPage: async () => ({on() {}, goto: async () => {}, evaluate: async () => 'Browser fixture',
        waitForResponse: async predicate => { assert.ok(predicate(response)); return response; }}),
    })};
  };
  global.fetch = async (_url, options) => {
    requests++;
    if (!options.headers.cookie) return new Response('', {status: 202, headers: {'x-amzn-waf-action': 'challenge'}});
    assert.equal(options.headers.cookie, 'aws-waf-token=supported-test-token');
    return new Response(payload, {headers: {'content-type': 'application/json'}});
  };
  const input = {retryCount: 0, failAfterRetryCount: true, wafMode: 'auto', wafTokenCache: true};
  const workers = Array.from({length: 20}, () => createProviderFetcher(api, input, state));
  await Promise.all(workers.map(worker => worker.fetch([url])));
  assert.equal(launches, 1);
  assert.equal(closed, 1);
  assert.equal(requests, 21);
  assert.equal(JSON.parse(await fs.readFile(state.tokenCache, 'utf8')).version, 2);
  if (process.platform !== 'win32') assert.equal((await fs.stat(state.tokenCache)).mode & 0o777, 0o600);
});

test('network errors release the gate and remain distinct from access denials', async t => {
  const state = await fixture(t);
  let requests = 0;
  class BufferFetcher {async fetch() { if (++requests === 1) throw Error('DNS failed'); return ['valid']; }}
  await assert.rejects(createProviderFetcher({BufferFetcher}, {}, state).fetch(['one']), /DNS failed/);
  assert.deepEqual(await createProviderFetcher({BufferFetcher}, {}, state).fetch(['two']), ['valid']);
  assert.equal(requests, 2);
  assert.equal(accessError(Error('Invalid CSV')), undefined);
  assert.match(accessError(new api.RateLimitError('https://jetta.dukascopy.com/', 2)).message, /rate limit reached/);
  assert.match(accessError(new api.WafChallengeError('https://jetta.dukascopy.com/', 405, 'captcha')).message, /browser verification failed/);
});

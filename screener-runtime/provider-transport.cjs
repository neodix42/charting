'use strict';

const fs=require('node:fs/promises');
const path=require('node:path');
const sdkOwners=new WeakMap();
const sdkSymbols=new Set(['btcusd','xauusd','xagusd','ethusd','usa500idxusd','xauxag']);

async function requestSDKHistory(request,{symbol,timeframe,from,to}){
  const session=request.providerSession,sourceKey=session?.sdk?.sourceKey,owner=session&&sdkOwners.get(session);
  if(!/^[a-f0-9]{64}$/.test(sourceKey??'')||!owner)throw Error('The configured JForex history source requires the connected app owner pipe.');
  if(owner.sourceKey!==sourceKey)throw Error('JForex source identity changed; restart this job with its matching owner.');
  const hourly=['h1','h1-native'].includes(timeframe),step=timeframe==='d1'?86400000:3600000,maximum=timeframe==='tick'?3600000:hourly?1000*3600000:366*86400000;
  if(!/^[a-z0-9]{1,128}$/.test(symbol??'')||timeframe==='tick'&&!sdkSymbols.has(symbol)||timeframe==='h1-native'&&symbol!=='btcusd'||!['tick','d1','h1','h1-native'].includes(timeframe)||!Number.isSafeInteger(from)||!Number.isSafeInteger(to)||from<0||to<=from||from%step||to%step||
    to-from>maximum)throw Error('Invalid bounded JForex history request.');
  let result;
  try{result=await owner.ask('history',{symbol,timeframe,from,to});}
  catch(error){error.code='JFOREX_HISTORY';throw error;}
  if(typeof result.path!=='string'||!path.isAbsolute(result.path)||!Number.isSafeInteger(result.rows)||result.rows<0||!path.isAbsolute(session.directory))throw Error('Invalid JForex history export response.');
  if(hourly&&result.sessionPolicy!==(timeframe==='h1'?'verified-full-hour-v1':'provider-native-h1-v1'))throw Error('JForex hourly history did not verify its requested session policy.');
  const info=await fs.lstat(result.path),root=await fs.realpath(session.directory),filename=await fs.realpath(result.path),relative=path.relative(root,filename);
  if(!info.isFile()||info.size>64*1024*1024||!relative||relative==='..'||relative.startsWith('..'+path.sep)||path.isAbsolute(relative))throw Error('JForex history export is outside its owned folder or exceeds 64 MiB.');
  return {path:result.path,rows:result.rows,...(hourly?{sessionPolicy:result.sessionPolicy}:{})};
}

// Use the parent's existing private pipe for the one application-wide pacing
// queue. No proxy, endpoint/header change, or independent worker retry loop.
function dataURL(value) {
  try {
    const url = new URL(typeof value === 'string' || value instanceof URL ? value : value.url);
    return url.origin === 'https://jetta.dukascopy.com' && /^\/v1\/(ticks\/|candles\/(minute|hour|day)\/)/.test(url.pathname);
  } catch { return false; }
}

function installProviderTransport(lines, request, send = event => process.stdout.write(`${JSON.stringify(event)}\n`)) {
  if (!request.providerSession?.rateLimit&&!request.providerSession?.sdk) return {close() {}, async failure(error) { return error; }};
  if(request.providerSession?.sdk&&!/^[a-f0-9]{64}$/.test(request.providerSession.sdk.sourceKey??''))throw Error('Invalid private JForex history source identity.');
  if (!request.ownerPipe) throw Error('Shared Dukascopy pacing requires the app owner pipe.');
  let serial = 0;
  const pending = new Map();
  function reply(line) {
    let message;
    try { message = JSON.parse(line); } catch { return; }
    if (message.type !== 'provider-reply') return;
    const entry = pending.get(message.requestId);
    if (!entry) return;
    pending.delete(message.requestId);
    if (message.error) entry.reject(new Error(message.error)); else entry.resolve(message);
  }
  lines.on('line', reply);
  function ask(operation, extra = {}) {
    if (pending.size >= 16) return Promise.reject(Error('Too many Dukascopy pacing requests.'));
    const requestId = ++serial;
    return new Promise((resolve, reject) => {
      pending.set(requestId, {resolve, reject});
      send({type: 'provider-request', requestId, operation, ...extra});
    });
  }
  if(request.providerSession?.sdk)sdkOwners.set(request.providerSession,{ask,sourceKey:request.providerSession.sdk.sourceKey});
  const observe = (status, retryAfter) => ask('response', {httpStatus: status, retryAfter: String(retryAfter ?? '').slice(0, 128)});
  const originalFetch = global.fetch;
  global.fetch = async function(...args) {
    if (!dataURL(args[0])) return originalFetch.apply(this, args);
    await ask('permit');
    // The pinned library creates its 30s network timeout before calling fetch.
    // Pacing and provider cooldowns can take longer: start that timeout only
    // after the permit. App cancellation still terminates the owned process.
    const options = {...args[1], signal: AbortSignal.timeout(30000)};
    const response = await originalFetch.call(this, args[0], options);
    try { await observe(response.status, response.headers.get('retry-after')); }
    catch (error) { await response.body?.cancel(); throw error; }
    return response;
  };

  // The upstream library's supported browser verification also requests candle
  // URLs. Route those through the same queue using Playwright's public API.
  const {chromium} = require('playwright-core');
  const originalLaunch = chromium.launch;
  const observations = new Set();
  chromium.launch = async function(...args) {
    const browser = await originalLaunch.apply(this, args);
    const newContext = browser.newContext.bind(browser);
    browser.newContext = async function(...options) {
      const context = await newContext(...options);
      await context.route('https://jetta.dukascopy.com/v1/**', async route => {
        if (!dataURL(route.request().url())) return route.continue();
        try { await ask('permit'); await route.continue(); }
        catch { await route.abort(); }
      });
      context.on('response', response => {
        if (!dataURL(response.url())) return;
        const observation = observe(response.status(), response.headers()['retry-after']);
        observations.add(observation);
        // Library errors still determine the download result. Consume event
        // promise errors here; drain them before emitting final worker completion.
        observation.then(() => observations.delete(observation), () => {});
        observation.catch(() => {});
      });
      return context;
    };
    return browser;
  };
  return {
    async drain() { await Promise.all(observations); },
    async failure(error) {
      if(['DUKASCOPY_BATCH_BLOCKED','JFOREX_HISTORY'].includes(error?.code))return error;
      if (!/\b(?:HTTP(?: status:)?|status)\s*403\b/i.test(String(error?.message))) return error;
      const result = await observe(403, '');
      return new Error(`${error.message} Next manual attempt after ${new Date(result.cooldownUntil).toISOString().replace(/\.\d{3}Z$/, 'Z')} (saved access-denial cooldown).`);
    },
    close() {
      global.fetch = originalFetch;
      chromium.launch = originalLaunch;
      lines.removeListener('line', reply);
      for (const entry of pending.values()) entry.reject(Error('Dukascopy owner pipe closed.'));
      pending.clear();
      if(sdkOwners.get(request.providerSession)?.ask===ask)sdkOwners.delete(request.providerSession);
    },
  };
}

module.exports = {dataURL, installProviderTransport,requestSDKHistory};

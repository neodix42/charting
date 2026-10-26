'use strict';

const fs = require('node:fs/promises');
const path = require('node:path');
const {setTimeout: delay} = require('node:timers/promises');

function accessError(error) {
  const detail = String(error?.message ?? error);
  if (/\b(?:HTTP(?: status:)?|status)\s*403\b/i.test(detail)) {
    return new Error(`Dukascopy refused access (HTTP 403). Refresh/download stopped; saved data and checkpoints are retained. Retry later or contact Dukascopy if access remains unavailable. Details: ${detail}`);
  }
  if (error?.code === 'DUKASCOPY_WAF_TOKEN' || error?.code === 'DUKASCOPY_WAF_CHALLENGE') {
    return new Error(`Dukascopy browser verification failed. Refresh/download stopped; saved data and checkpoints are retained. ${detail}`);
  }
  if (error?.code === 'DUKASCOPY_RATE_LIMIT') {
    return new Error(`Dukascopy rate limit reached. Refresh/download stopped; wait before retrying. ${detail}`);
  }
}

// All Go-owned children in one batch share this gate and the library's supported
// session cache. Only the initial request is serialized: once it succeeds, up to
// twenty workers continue normally, using the same cached browser session.
function createProviderFetcher(api, input, state) {
  if (state && (!path.isAbsolute(state.directory) || !path.isAbsolute(state.tokenCache))) {
    throw new Error('Provider session paths must be absolute.');
  }
  const config = {...input, ...(state ? {wafTokenCachePath: state.tokenCache} : {})};
  let fetcher;
  let cacheVersion;
  let initialized = !state;
  const blocked = state && path.join(state.directory, 'blocked.json');
  const ready = state && path.join(state.directory, 'ready');
  const lock = state && path.join(state.directory, 'initializing');

  async function checkBlocked() {
    if (!state) return;
    try { throw new Error(JSON.parse(await fs.readFile(blocked, 'utf8')).message); }
    catch (error) { if (error.code !== 'ENOENT') throw error; }
  }
  async function fetch(urls) {
    await checkBlocked();
    if (state) {
      let version;
      try {
        const info = await fs.stat(state.tokenCache, {bigint: true});
        version = `${info.dev}:${info.ino}:${info.mtimeNs}:${info.size}`;
      } catch (error) { if (error.code !== 'ENOENT') throw error; }
      // A peer may renew an expired session during a long batch. Recreate the
      // public fetcher so its lazy cache read uses that session on the next chunk.
      if (version !== cacheVersion) { fetcher = undefined; cacheVersion = version; }
    }
    fetcher ??= new api.BufferFetcher(config);
    try { return await fetcher.fetch(urls); }
    catch (error) {
      const failure = accessError(error);
      if (!failure) throw error;
      // Publish the complete error atomically; no token/cookie values are logged.
      if (state) {
        const temporary = `${blocked}.${process.pid}.pending`;
        await fs.writeFile(temporary, JSON.stringify({message: failure.message}), {flag: 'wx', mode: 0o600});
        await fs.rename(temporary, blocked);
      }
      throw failure;
    }
  }
  return {async fetch(urls) {
    if (initialized) return fetch(urls);
    // The upstream 429 policy can honor two Retry-After waits of five minutes
    // each. Waiting workers remain immediately cancellable via their owner pipe.
    const deadline = Date.now() + 15 * 60 * 1000;
    while (true) {
      await checkBlocked();
      try { await fs.mkdir(lock, {mode: 0o700}); break; }
      catch (error) { if (error.code !== 'EEXIST') throw error; }
      if (Date.now() >= deadline) throw new Error('Timed out waiting for the shared Dukascopy session. Retry the batch.');
      await delay(50);
    }
    try {
      await checkBlocked();
      initialized = true;
      try { await fs.stat(ready); }
      catch (error) {
        if (error.code !== 'ENOENT') throw error;
        const result = await fetch(urls);
        await fs.writeFile(ready, '', {flag: 'wx', mode: 0o600});
        return result;
      }
    } finally { await fs.rmdir(lock); }
    return fetch(urls);
  }};
}

module.exports = {createProviderFetcher, accessError};

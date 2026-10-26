'use strict';

const fs = require('node:fs');
const path = require('node:path');
const readline = require('node:readline');

async function writeCheckpoint(file, value) {
  const temporary = `${file}.pending`;
  const handle = await fs.promises.open(temporary, 'w', 0o600);
  try { await handle.writeFile(JSON.stringify(value)); await handle.sync(); }
  finally { await handle.close(); }
  await fs.promises.rename(temporary, file);
  // Directory fsync is supported on Unix; Windows still gets atomic rename.
  try { const directory = await fs.promises.open(path.dirname(file), 'r'); try { await directory.sync(); } finally { await directory.close(); } }
  catch (error) { if (!['EINVAL','EPERM','EISDIR','EACCES'].includes(error.code)) throw error; }
}

async function writeAll(handle, text, position) {
  const buffer = Buffer.from(text);
  let offset = 0;
  while (offset < buffer.length) {
    const {bytesWritten} = await handle.write(buffer, offset, buffer.length-offset, position+offset);
    if (!bytesWritten) throw new Error('CSV write made no progress.');
    offset += bytesWritten;
  }
  return position+offset;
}

// Match getHistoricalRates using only dukascopy-node's public exports. Fetch
// small, ordered batches so years of ticks never accumulate in memory. Each
// source file contains whole UTC aggregation buckets (hour/day/month/year).
async function download(request, options = {}) {
  if (request.planFile) return require('./incremental-worker.cjs').extendDownload(request, options);
  const api = options.api || require('dukascopy-node');
  const emit = options.emit || (event => process.stdout.write(`${JSON.stringify(event)}\n`));
  const sourceBatchSize = request.sourceBatchSize ?? 4;
  if (!Number.isInteger(sourceBatchSize) || sourceBatchSize < 1 || sourceBatchSize > 4) {
    throw new Error('Source batch size must be an integer between 1 and 4.');
  }
  const config = {
    instrument: request.symbol,
    dates: { from: request.from, to: request.to },
    timeframe: request.timeframe,
    format: 'csv',
    priceType: 'bid',
    volumes: true,
    volumeUnits: 'millions',
    ignoreFlats: true,
    utcOffset: 0,
    batchSize: sourceBatchSize,
    pauseBetweenBatchesMs: 1000,
    requestIntervalMs: 250,
    rateLimitRetryCount: 2,
    // A forbidden request is terminal, not a reason to repeat the same request.
    // The library retains its separate, bounded HTTP 429 backoff policy.
    retryCount: 0,
    retryOnEmpty: false,
    failAfterRetryCount: true,
    pauseBetweenRetriesMs: 1000,
    wafMode: 'auto',
    wafTimeoutMs: 60000,
    wafTokenCache: true,
    wafTokenCachePath: path.join(path.dirname(request.output), '.dukascopy-waf-token.json'),
  };
  const checked = api.validateConfigNode(config);
  if (!checked.isValid) throw new Error(`Invalid Dukascopy request: ${JSON.stringify(checked.validationErrors)}`);
  const input = checked.input;
  let [startDate, endDate] = api.normaliseDates({
    instrument: input.instrument, timeframe: input.timeframe,
    startDate: input.dates.from, endDate: input.dates.to, utcOffset: 0,
  });
  if (request.asOf !== undefined) {
    if (!Number.isSafeInteger(request.asOf) || request.asOf < 0) throw new Error('Invalid download execution cutoff.');
    endDate = new Date(Math.min(+endDate, request.asOf));
  }
  const checkpointPath = path.join(path.dirname(request.output), 'checkpoint.json');
  let checkpoint;
  if (request.resume) {
    try { checkpoint = JSON.parse(await fs.promises.readFile(checkpointPath, 'utf8')); }
    catch (error) { if (error.code !== 'ENOENT') throw new Error(`Cannot read saved download checkpoint: ${error.message}`); }
    if (checkpoint) {
      if (checkpoint.version !== 1 || checkpoint.symbol !== request.symbol || checkpoint.timeframe !== request.timeframe ||
          checkpoint.from !== request.from || checkpoint.to !== request.to ||
          !Number.isFinite(checkpoint.start) || !Number.isFinite(checkpoint.end) || checkpoint.start >= checkpoint.end ||
          checkpoint.start < +input.dates.from || checkpoint.end > Math.min(+input.dates.to, Date.now()) ||
          !Number.isSafeInteger(checkpoint.nextIndex) || checkpoint.nextIndex < 0 ||
          !Number.isSafeInteger(checkpoint.bytes) || checkpoint.bytes < 0 ||
          !Number.isSafeInteger(checkpoint.rows) || checkpoint.rows < 0 ||
          !(checkpoint.lastTimestamp === null || Number.isFinite(checkpoint.lastTimestamp)) ||
          !(checkpoint.header === null || typeof checkpoint.header === 'string')) {
        throw new Error('Saved download checkpoint is invalid or belongs to another request.');
      }
      const expectedHeader=request.timeframe==='tick'?'timestamp,askPrice,bidPrice,askVolume,bidVolume':'timestamp,open,high,low,close,volume';
      if(checkpoint.rows===0?(checkpoint.bytes!==0||checkpoint.header!==null||checkpoint.lastTimestamp!==null):
        (checkpoint.bytes===0||checkpoint.header!==expectedHeader||checkpoint.nextIndex===0||checkpoint.lastTimestamp<checkpoint.start||checkpoint.lastTimestamp>=checkpoint.end)) {
        throw new Error('Saved download checkpoint has inconsistent CSV metadata.');
      }
      startDate = new Date(checkpoint.start); endDate = new Date(checkpoint.end);
    }
  }
  const urls = api.generateUrls({ instrument: input.instrument, timeframe: input.timeframe,
    priceType: input.priceType, startDate, endDate });
  if (!urls.length) throw new Error('No available history in the requested UTC date range.');
  if (checkpoint && (checkpoint.totalSources !== urls.length || checkpoint.nextIndex > urls.length)) throw new Error('Saved checkpoint source plan changed.');
  const completed = new Set(urls.slice(0, checkpoint?.nextIndex ?? 0));
  const fetcher = require('./provider-session.cjs').createProviderFetcher(api, { ...input, onItemFetch(url) {
    completed.add(url);
    emit({ type: 'progress', progress: Math.min(95, completed.size / urls.length * 95),
      message: `Fetched ${completed.size} of ${urls.length} source files` });
  } }, request.providerSession);
  let handle;
  let rows = checkpoint?.rows ?? 0;
  let previous = checkpoint?.lastTimestamp ?? -Infinity;
  let header = checkpoint?.header;
  let position = checkpoint?.bytes ?? 0;
  try {
    // The Go owner created this private temporary path. It owns publication and
    // cleanup; a worker never writes a final destination or removes user files.
    handle = await fs.promises.open(request.output, 'r+');
    if ((await handle.stat()).size < position) throw new Error('Saved CSV is shorter than its committed checkpoint. Original files retained.');
    // Discard only an uncommitted tail. Completed source batches are never
    // fetched or appended twice, even when the app died during the next write.
    await handle.truncate(position);
    emit({ type: 'progress', progress: completed.size/urls.length*95, message: `${checkpoint?'Resuming':'Downloading'} ${completed.size} of ${urls.length} source files` });
    for (let offset = checkpoint?.nextIndex ?? 0; offset < urls.length; offset += sourceBatchSize) {
      const buffers = await fetcher.fetch(urls.slice(offset, offset + sourceBatchSize));
      const data = api.processData({ ...input, requestedTimeframe: input.timeframe,
        bufferObjects: buffers.filter(item => item.buffer.length > 0) })
        .filter(row => row[0] >= +startDate && row[0] < +endDate);
      for (const row of data) {
        if (!Number.isFinite(row[0]) || row[0] < previous ||
          (input.timeframe !== 'tick' && row[0] === previous)) {
          throw new Error('Dukascopy returned candles outside chronological order.');
        }
        if (row.some(value => !Number.isFinite(value))) throw new Error('Dukascopy returned non-finite price or volume data.');
        previous = row[0];
      }
      if (data.length) {
        const csv = api.formatOutput({ processedData: data, timeframe: input.timeframe, format: 'csv' });
        if (typeof csv !== 'string') throw new Error('Dukascopy returned an unexpected CSV result.');
        const newline = csv.indexOf('\n');
        const nextHeader = csv.slice(0, newline);
        if (header && header !== nextHeader) throw new Error('Dukascopy changed the CSV columns during download.');
        position = await writeAll(handle, `${header ? csv.slice(newline + 1) : csv}\n`, position);
        header = nextHeader;
        rows += data.length;
      }
      if (request.resume) {
        await handle.sync();
        await writeCheckpoint(checkpointPath, {version:1, symbol:request.symbol, timeframe:request.timeframe,
          from:request.from, to:request.to, start:+startDate, end:+endDate,
          nextIndex:Math.min(offset+sourceBatchSize,urls.length),totalSources:urls.length,
          bytes:position,rows,lastTimestamp:Number.isFinite(previous)?previous:null,header:header??null});
      }
      // Pacing between bounded batches protects the public endpoint. It does
      // not advance progress, which counts completed upstream requests only.
      if (offset + sourceBatchSize < urls.length) await new Promise(resolve => setTimeout(resolve, options.pauseMs ?? 1000));
    }
    if (!rows) throw new Error('Dukascopy returned no rows for this symbol and date range.');
    await handle.sync();
    const {scan,closedBoundary}=require('./refresh-worker.cjs');
    const actual=await scan(request.output, request.timeframe);
    const coveredTo=closedBoundary(+endDate,request.timeframe);
    const coverage=+input.dates.from<coveredTo?[{from:+input.dates.from,to:coveredTo}]:[];
    emit({ type: 'done', rows, firstTimestamp:actual.first,lastTimestamp:actual.last,checksum:actual.checksum,
      coverage,checkedThrough:coveredTo,message: `Downloaded ${rows.toLocaleString('en-US')} rows` });
    return rows;
  } finally {
    await handle?.close();
  }
}

async function main(downloadFn = download) {
  const browsers = path.join(__dirname, 'browsers');
  if (fs.existsSync(browsers)) process.env.PLAYWRIGHT_BROWSERS_PATH = browsers;
  const lines = readline.createInterface({input:process.stdin,crlfDelay:Infinity});
  let ownerRequired=false,ended=false,finished=false;
  lines.on('close',()=>{
    ended=true;
    // EOF on a parent-owned pipe works on Ubuntu and Windows, including an
    // abruptly terminated app. Chromium exits when its Node transport closes.
    if(ownerRequired&&!finished) process.exit(1);
  });
  try {
    const input = await new Promise((resolve,reject)=>{
      lines.once('line',resolve); lines.once('error',reject);
      lines.once('close',()=>reject(new Error('Missing worker request.')));
    });
    if(input.length>16384)throw new Error('Worker request exceeds the input limit.');
    const request=JSON.parse(input);ownerRequired=request.ownerPipe===true;
    if(ownerRequired&&ended)throw new Error('Downloader owner disconnected.');
    const transport = require('./provider-transport.cjs').installProviderTransport(lines, request);
    try {
      try { await downloadFn(request); await transport.drain?.(); }
      catch (error) { throw await transport.failure(error); }
    } finally { transport.close(); }
  }
  finally { finished=true;lines.close();process.stdin.destroy(); }
}

if (require.main === module) main().catch(error => {
  const message = String(error?.message || JSON.stringify(error) || error).slice(0, 4000);
  process.stdout.write(`${JSON.stringify({ type: 'error', message })}\n`);
  process.exitCode = 1;
});

module.exports = { download, main, writeCheckpoint };

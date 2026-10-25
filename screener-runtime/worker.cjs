'use strict';

const fs = require('node:fs');
const path = require('node:path');

// Match getHistoricalRates using only dukascopy-node's public exports. Fetch
// small, ordered batches so years of ticks never accumulate in memory. Each
// source file contains whole UTC aggregation buckets (hour/day/month/year).
async function download(request, options = {}) {
  const api = options.api || require('dukascopy-node');
  const emit = options.emit || (event => process.stdout.write(`${JSON.stringify(event)}\n`));
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
    batchSize: 4,
    pauseBetweenBatchesMs: 1000,
    requestIntervalMs: 250,
    rateLimitRetryCount: 2,
    retryCount: 1,
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
  const [startDate, endDate] = api.normaliseDates({
    instrument: input.instrument, timeframe: input.timeframe,
    startDate: input.dates.from, endDate: input.dates.to, utcOffset: 0,
  });
  const urls = api.generateUrls({ instrument: input.instrument, timeframe: input.timeframe,
    priceType: input.priceType, startDate, endDate });
  if (!urls.length) throw new Error('No available history in the requested UTC date range.');
  const completed = new Set();
  const fetcher = new api.BufferFetcher({ ...input, onItemFetch(url) {
    completed.add(url);
    emit({ type: 'progress', progress: Math.min(95, completed.size / urls.length * 95),
      message: `Fetched ${completed.size} of ${urls.length} source files` });
  } });
  let handle;
  let rows = 0;
  let previous = -Infinity;
  let header;
  try {
    // The Go owner created this private temporary path. It owns publication and
    // cleanup; a worker never writes a final destination or removes user files.
    handle = await fs.promises.open(request.output, 'r+');
    await handle.truncate(0);
    emit({ type: 'progress', progress: 0, message: `Downloading ${urls.length} source files` });
    for (let offset = 0; offset < urls.length; offset += 4) {
      const buffers = await fetcher.fetch(urls.slice(offset, offset + 4));
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
        await handle.writeFile(`${header ? csv.slice(newline + 1) : csv}\n`);
        header = nextHeader;
        rows += data.length;
      }
      // Pacing between bounded batches protects the public endpoint. It does
      // not advance progress, which counts completed upstream requests only.
      if (offset + 4 < urls.length) await new Promise(resolve => setTimeout(resolve, options.pauseMs ?? 1000));
    }
    if (!rows) throw new Error('Dukascopy returned no rows for this symbol and date range.');
    await handle.sync();
    emit({ type: 'done', rows, message: `Downloaded ${rows.toLocaleString('en-US')} rows` });
    return rows;
  } finally {
    await handle?.close();
  }
}

async function main() {
  const browsers = path.join(__dirname, 'browsers');
  if (fs.existsSync(browsers)) process.env.PLAYWRIGHT_BROWSERS_PATH = browsers;
  let input = '';
  for await (const part of process.stdin) {
    input += part;
    if (input.length > 16384) throw new Error('Worker request exceeds the input limit.');
  }
  await download(JSON.parse(input));
}

if (require.main === module) main().catch(error => {
  const message = String(error?.message || JSON.stringify(error) || error).slice(0, 4000);
  process.stdout.write(`${JSON.stringify({ type: 'error', message })}\n`);
  process.exitCode = 1;
});

module.exports = { download };

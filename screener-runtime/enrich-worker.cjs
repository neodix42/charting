'use strict';

const fs = require('node:fs');
const fsp = require('node:fs/promises');
const path = require('node:path');
const {createHash}=require('node:crypto');
const { parse } = require('csv-parse');
const { main } = require('./worker.cjs');

const catalogPath = fs.existsSync(path.join(__dirname, 'indicators.json'))
  ? path.join(__dirname, 'indicators.json') : path.join(__dirname, '../internal/screener/indicators.json');
const catalog = JSON.parse(fs.readFileSync(catalogPath, 'utf8'));
const emit = event => process.stdout.write(`${JSON.stringify(event)}\n`);
const key = value => value.replace(/^\uFEFF/, '').trim().toLowerCase();
const cell = value => /[",\r\n]/.test(value) ? `"${value.replaceAll('"', '""')}"` : value;
const line = values => `${values.map(value => cell(String(value))).join(',')}\n`;

function validate(request) {
  const definition = catalog.indicators.find(item => item.id === request.indicator);
  if (!definition) throw new Error('Unsupported trading-signals indicator.');
  const parameters = {};
  for (const spec of definition.parameters) {
    const value = request.parameters?.[spec.key] ?? spec.default;
    if (!Number.isFinite(value) || value < spec.min || value > spec.max || (spec.step === 1 && !Number.isInteger(value))) {
      throw new Error(`${spec.label} must be between ${spec.min} and ${spec.max}.`);
    }
    parameters[spec.key] = value;
  }
  for (const name of Object.keys(request.parameters ?? {})) {
    if (!Object.hasOwn(parameters, name)) throw new Error(`Unsupported parameter: ${name}.`);
  }
  if ('fast' in parameters && parameters.fast >= parameters.slow) throw new Error('Fast period must be smaller than slow period.');
  const source = request.source || 'auto';
  if (!['auto', 'close', 'open', 'high', 'low', 'bidprice', 'askprice'].includes(source)) throw new Error('Unsupported price source.');
  return {definition, parameters, source};
}

async function calculator(request, headers) {
  const {definition, parameters: p, source} = validate(request);
  const api = await import('trading-signals');
  const normalized = headers.map(key);
  if (normalized.some(value => !value) || new Set(normalized).size !== headers.length) throw new Error('CSV contains empty or duplicate field names.');
  const price = source === 'auto' ? ['close', 'bidprice', 'askprice'].find(name => normalized.includes(name)) : source;
  let required;
  switch (definition.input) {
    case 'price': required = [price]; break;
    case 'hl': required = ['high', 'low']; break;
    case 'hlc': required = ['high', 'low', 'close']; break;
    case 'hlcv': required = ['high', 'low', 'close', 'volume']; break;
    case 'ohlcv': required = ['open', 'high', 'low', 'close', 'volume']; break;
    default: throw new Error('Unsupported indicator input.');
  }
  const missing = required.filter(name => !name || !normalized.includes(name));
  if (missing.length) throw new Error(`Requires ${required.map(name => name || 'close, bidPrice or askPrice').join(', ')} fields. Tick prices are not converted into candles.`);
  const suffix = definition.input === 'price' && price !== 'close' ? `_${price}` : '';
  const series = [];
  const add = (indicator, columns, result = value => [value]) => series.push({indicator, columns: columns.map(name => `${name}${suffix}`), result});
  const scalar = (Constructor, ...args) => add(new Constructor(...args), [`${definition.id}${args.length ? `_${args.join('_')}` : ''}`]);
  const simple = {sma:api.SMA,ema:api.EMA,wma:api.WMA,wsma:api.WSMA,rma:api.RMA,dema:api.DEMA,tema:api.TEMA,
    trima:api.TRIMA,zlema:api.ZLEMA,hma:api.HMA,kama:api.KAMA,rsi:api.RSI,roc:api.ROC,mom:api.MOM,
    cci:api.CCI,adx:api.ADX,adxr:api.ADXR,dx:api.DX,atr:api.ATR,obv:api.OBV,vwma:api.VWMA,mfi:api.MFI,
    cmf:api.CMF,force:api.ForceIndex,stochrsi:api.StochasticRSI};
  const cloud = period => new api.IchimokuCloud({conversionInterval:period,baseInterval:period,spanBInterval:period});
  if (simple[definition.id]) scalar(simple[definition.id], p.period);
  else switch (definition.id) {
    case 'alma': scalar(api.ALMA, p.period, p.offset, p.sigma); break;
    case 'macd': {
      const indicator = new api.MACD(new api.EMA(p.fast), new api.EMA(p.slow), new api.EMA(p.signal));
      add(indicator, [`macd_${p.fast}_${p.slow}`, `macd_signal_${p.fast}_${p.slow}_${p.signal}`, `macd_histogram_${p.fast}_${p.slow}_${p.signal}`],
        value => [value?.macd, indicator.signal.isStable ? value?.signal : null, indicator.signal.isStable ? value?.histogram : null]);
      break;
    }
    case 'ao': scalar(api.AO, p.fast, p.slow); break;
    case 'ac': scalar(api.AC, p.fast, p.slow, p.signal); break;
    case 'tr': scalar(api.TR); break;
    case 'vwap': scalar(api.VWAP); break;
    case 'ad': scalar(api.AD); break;
    case 'pvt': scalar(api.PVT); break;
    case 'bb': add(new api.BollingerBands(p.period, p.deviation), ['lower','middle','upper'].map(part => `bb_${part}_${p.period}_${p.deviation}`), value => [value?.lower, value?.middle, value?.upper]); break;
    case 'bbw': add(new api.BollingerBandsWidth(new api.BollingerBands(p.period, p.deviation)), [`bbw_${p.period}_${p.deviation}`]); break;
    case 'donchian': add(new api.DonchianChannels(p.period), ['lower','middle','upper'].map(part => `donchian_${part}_${p.period}`), value => [value?.lower, value?.middle, value?.upper]); break;
    case 'stochastic': add(new api.StochasticOscillator({kPeriod:p.k,kSlowingPeriod:p.slowing,dPeriod:p.d}), ['k','d','j'].map(part => `stochastic_${part}_${p.k}_${p.slowing}_${p.d}`), value => [value?.stochK,value?.stochD,value?.stochJ]); break;
    case 'tenkan': add(cloud(p.period), [`tenkan_${p.period}`], value => [value?.conversion]); break;
    case 'kijun': add(cloud(p.period), [`kijun_${p.period}`], value => [value?.base]); break;
    case 'ichimoku':
      add(cloud(p.conversion), [`tenkan_${p.conversion}`], value => [value?.conversion]);
      add(cloud(p.base), [`kijun_${p.base}`], value => [value?.base]);
      add(new api.IchimokuCloud({conversionInterval:p.conversion,baseInterval:p.base,spanBInterval:Math.max(p.conversion,p.base)}), [`senkou_a_${p.conversion}_${p.base}`], value => [value?.spanA]);
      add(cloud(p.spanB), [`senkou_b_${p.spanB}`], value => [value?.spanB]);
      break;
    default: throw new Error('Indicator constructor has not been integrated.');
  }
  const indexes = required.map(name => normalized.indexOf(name));
  return {
    columns: series.flatMap(item => item.columns),
    next(row, rowNumber) {
      const values = indexes.map((index, offset) => {
        const value = row[index].trim();
        if (!value || !Number.isFinite(Number(value))) throw new Error(`Row ${rowNumber}: ${required[offset]} must be a finite number.`);
        return Number(value);
      });
      let input = values[0];
      if (definition.input !== 'price') {
        input = Object.fromEntries(required.map((name,index) => [name,values[index]]));
        if (input.high < input.low || ('close' in input && (input.close < input.low || input.close > input.high)) ||
            ('open' in input && (input.open < input.low || input.open > input.high)) || ('volume' in input && input.volume < 0)) {
          throw new Error(`Row ${rowNumber}: invalid candle price range or volume.`);
        }
      }
      return series.flatMap(item => {
        item.indicator.add(input);
        // add() can return provisional EMA values. getResult() enforces the
        // library's stable warm-up rather than filling early rows with zeros.
        const result = item.indicator.getResult();
        return item.result(result).map(value => Number.isFinite(value) ? String(value) : '');
      });
    },
  };
}

async function writeAll(handle, data) {
  const buffer = Buffer.from(data);
  let offset = 0;
  while (offset < buffer.length) {
    const {bytesWritten} = await handle.write(buffer, offset, buffer.length - offset);
    if (!bytesWritten) throw new Error('Could not write enriched CSV.');
    offset += bytesWritten;
  }
}

async function enrich(request, options = {}) {
  validate(request);
  const report = options.emit ?? emit;
  if (!path.isAbsolute(request.input) || !path.isAbsolute(request.output) || request.input === request.output) throw new Error('Separate absolute input and output paths are required.');
  const info = await fsp.lstat(request.input);
  if (!info.isFile()) throw new Error('CSV input must be a regular file.');
  const stream = fs.createReadStream(request.input);
  const inputChecksum=createHash('sha256');
  stream.on('data',chunk=>inputChecksum.update(chunk));
  const parser = parse({bom:true,max_record_size:1024*1024,skip_empty_lines:true,info:true});
  stream.on('error', error => parser.destroy(error));
  stream.pipe(parser);
  let handle, calculate, headers, positions, timestampIndex = -1, previous = -Infinity, rows = 0, usable = 0;
  let buffer = '', bufferedBytes = 0, lastProgress = 0;
  const checksum=createHash('sha256');
  try {
    // Exclusive output creation cannot overwrite the original or an existing
    // temporary file. Go publishes only after a successful exit and fsync.
    handle = await fsp.open(request.output, 'wx', info.mode & 0o777);
    for await (const {record, info:recordInfo} of parser) {
      if (!headers) {
        headers = record;
        calculate = await calculator(request, headers);
        const normalized = headers.map(key);
        timestampIndex = normalized.indexOf('timestamp');
        positions = calculate.columns.map(name => normalized.indexOf(name));
        const nextHeader = [...headers];
        positions = positions.map((index,i) => index < 0 ? nextHeader.push(calculate.columns[i]) - 1 : index);
        buffer = line(nextHeader);
        checksum.update(buffer);
        bufferedBytes = Buffer.byteLength(buffer);
        report({type:'progress',progress:0,rows:0,columns:calculate.columns,message:`Adding ${calculate.columns.join(', ')}`});
        continue;
      }
      if (timestampIndex >= 0) {
        const value = record[timestampIndex].trim();
        const timestamp = value === '' ? NaN : Number(value);
        if (!Number.isFinite(timestamp) || timestamp < previous) throw new Error(`Row ${rows+2}: timestamp must be numeric and chronological.`);
        previous = timestamp;
      }
      const values = calculate.next(record, rows+2);
      if (values.some(value => value !== '')) usable++;
      const next = [...record];
      positions.forEach((index,i) => {next[index] = values[i];});
      const serialized = line(next);
      checksum.update(serialized);
      buffer += serialized;
      bufferedBytes += Buffer.byteLength(serialized);
      rows++;
      if (bufferedBytes >= 128*1024) {await writeAll(handle, buffer);buffer='';bufferedBytes=0;}
      if (Date.now()-lastProgress >= 200) {
        lastProgress=Date.now();
        report({type:'progress',progress:Math.min(99,recordInfo.bytes/Math.max(1,info.size)*100),rows,message:`Calculated ${rows.toLocaleString('en-US')} rows`});
      }
      if (options.onRow) await options.onRow(rows);
    }
    if (!headers || !rows) throw new Error('CSV has no data rows.');
    if (buffer) await writeAll(handle, buffer);
    await handle.sync();
    const result = {type:'done',rows,columns:calculate.columns,checksum:checksum.digest('hex'),inputChecksum:inputChecksum.digest('hex'),message:`Enriched ${rows.toLocaleString('en-US')} rows; ${usable.toLocaleString('en-US')} rows with values${usable ? '' : ' (warm-up or undefined indicator results)'}`};
    report(result);
    return result;
  } finally {
    stream.destroy();
    parser.destroy();
    await handle?.close();
  }
}

if (require.main === module) main(enrich).catch(error => {
  emit({type:'error',message:String(error?.message || error).slice(0,4000)});
  process.exitCode=1;
});

module.exports = {enrich,calculator,validate,catalog,key,line,writeAll};

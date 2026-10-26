'use strict';

// A refresh writes only Go-owned scratch files. Publication, original backups,
// source-change checks and durable recipe metadata belong to the Go owner.
const fs = require('node:fs');
const fsp = require('node:fs/promises');
const path = require('node:path');
const {createHash}=require('node:crypto');
const {parse} = require('csv-parse');
const {download,main} = require('./worker.cjs');
const {calculator,validate,catalog,key,line,writeAll} = require('./enrich-worker.cjs');
const emit = event => process.stdout.write(`${JSON.stringify(event)}\n`);
const candleFields = ['timestamp','open','high','low','close','volume'];
const tickFields = ['timestamp','askprice','bidprice','askvolume','bidvolume'];

async function* records(filename,onChunk) {
  const input = fs.createReadStream(filename);
  if(onChunk)input.on('data',onChunk);
  const parser = parse({bom:true,max_record_size:1024*1024,skip_empty_lines:true});
  input.on('error',error=>parser.destroy(error));
  input.pipe(parser);
  try { for await (const row of parser) yield row; }
  finally { input.destroy();parser.destroy(); }
}

function parameterRequest(indicator,values,source='close') {
  const definition=catalog.indicators.find(item=>item.id===indicator);
  if(!definition || values.length!==definition.parameters.length) throw Error('Cannot recover indicator parameters.');
  const request={indicator,parameters:Object.fromEntries(definition.parameters.map((spec,i)=>[spec.key,Number(values[i])])),source};
  validate(request);
  return request;
}

// Old CryptoDesk CSVs predate recipe manifests. Their names encode all reviewed
// parameters. Backup requests also disambiguate a MACD line with no signal column.
async function recipesFor(headers,saved=[]) {
  const normalized=headers.map(key), base=new Set([...candleFields,...tickFields]);
  if(normalized.some(value=>!value)||new Set(normalized).size!==normalized.length)throw Error('CSV contains empty or duplicate field names.');
  const derived=normalized.filter(name=>!base.has(name));
  const wanted=new Set(derived),recipes=[],covered=new Set();
  const add=async request=>{
    const checked=validate(request);
    const canonical={indicator:request.indicator,parameters:checked.parameters,source:checked.source};
    let calculate;
    try{calculate=await calculator(canonical,headers);}
    catch(error){if(/Requires .* fields/.test(error.message))return;throw error;}
    const columns=calculate.columns.filter(name=>wanted.has(name));
    if(!columns.length)return;
    const serialized=JSON.stringify(canonical);
    if(!recipes.some(item=>JSON.stringify(item)===serialized))recipes.push(canonical);
    columns.forEach(name=>covered.add(name));
  };
  for(const request of saved)await add(request);
  const simple=new Set(catalog.indicators.filter(item=>item.parameters.length===1&&item.parameters[0].key==='period').map(item=>item.id));
  for(const column of derived){
    if(covered.has(column))continue;
    let name=column,source='close';
    const suffix=name.match(/_(bidprice|askprice|open|high|low)$/);
    if(suffix){source=suffix[1];name=name.slice(0,-suffix[0].length);}
    let match=name.match(/^([a-z]+)_([0-9.e+\-]+)$/),request;
    if(match&&simple.has(match[1]))request=parameterRequest(match[1],[match[2]],source);
    else if((match=name.match(/^alma_([0-9.e+\-]+)_([0-9.e+\-]+)_([0-9.e+\-]+)$/)))request=parameterRequest('alma',match.slice(1),source);
    else if((match=name.match(/^(?:bb_(?:lower|middle|upper)|bbw)_([0-9.e+\-]+)_([0-9.e+\-]+)$/)))request=parameterRequest(name.startsWith('bbw_')?'bbw':'bb',match.slice(1),source);
    else if((match=name.match(/^donchian_(?:lower|middle|upper)_([0-9.e+\-]+)$/)))request=parameterRequest('donchian',match.slice(1),source);
    else if((match=name.match(/^stochastic_(?:k|d|j)_([0-9.e+\-]+)_([0-9.e+\-]+)_([0-9.e+\-]+)$/)))request=parameterRequest('stochastic',match.slice(1),source);
    else if((match=name.match(/^(ao|ac)_([0-9.e+\-]+)_([0-9.e+\-]+)(?:_([0-9.e+\-]+))?$/)))request=parameterRequest(match[1],match.slice(2).filter(value=>value!==undefined),source);
    else if((match=name.match(/^macd_(?:signal_|histogram_)?([0-9.e+\-]+)_([0-9.e+\-]+)(?:_([0-9.e+\-]+))?$/))){
      const related=normalized.find(value=>value.startsWith(`macd_signal_${match[1]}_${match[2]}_`)&&value.endsWith(suffix?.[0]??''));
      const signal=match[3]??related?.slice(`macd_signal_${match[1]}_${match[2]}_`.length).replace(/_(bidprice|askprice|open|high|low)$/,'')??9;
      request=parameterRequest('macd',[match[1],match[2],signal],source);
    }
    else if((match=name.match(/^senkou_a_([0-9.e+\-]+)_([0-9.e+\-]+)$/)))request=parameterRequest('ichimoku',[match[1],match[2],52],source);
    else if((match=name.match(/^senkou_b_([0-9.e+\-]+)$/)))request=parameterRequest('ichimoku',[9,26,match[1]],source);
    else if(['tr','vwap','ad','pvt'].includes(name))request=parameterRequest(name,[],source);
    else if(['weekday','hour'].includes(name))request=parameterRequest(name,[],'auto');
    if(!request)throw Error(`Cannot refresh unrecognized derived column "${column}". Original CSV kept; restore its applied indicator recipe first.`);
    await add(request);
    if(!covered.has(column))throw Error(`Cannot reproduce derived column "${column}" from its saved parameters. Original CSV kept.`);
  }
  return recipes;
}

async function scan(filename,timeframe,onProgress) {
  let headers,first,last=-Infinity,rows=0,timestampIndex=-1,baseIndexes;
  const checksum=createHash('sha256');
  let bytes=0;
  const base=timeframe==='tick'?tickFields:candleFields;
  for await(const row of records(filename,chunk=>{checksum.update(chunk);bytes+=chunk.length;onProgress?.(bytes,rows);})){
    if(!headers){
      headers=row;
      const normalized=headers.map(key);
      if(base.some(field=>!normalized.includes(field)))throw Error(`Refresh requires the original Dukascopy ${base.join(', ')} fields.`);
      timestampIndex=normalized.indexOf('timestamp');
      baseIndexes=base.map(name=>normalized.indexOf(name));
      continue;
    }
    const timestamp=row[timestampIndex].trim()===''?NaN:Number(row[timestampIndex]);
    if(!Number.isFinite(timestamp)||timestamp<0||timestamp<last||(timeframe!=='tick'&&timestamp===last))throw Error('CSV timestamps must be chronological; candle timestamps must be unique.');
    const values=Object.fromEntries(base.map((name,i)=>[name,row[baseIndexes[i]].trim()===''?NaN:Number(row[baseIndexes[i]])]));
    if(Object.values(values).some(value=>!Number.isFinite(value)))throw Error(`CSV row ${rows+2} contains missing or non-finite original market data.`);
    if(timeframe!=='tick'&&(values.high<values.low||values.open<values.low||values.open>values.high||values.close<values.low||values.close>values.high||values.volume<0))throw Error(`CSV row ${rows+2} contains invalid OHLC bounds or volume.`);
    if(timeframe==='tick'&&(values.askvolume<0||values.bidvolume<0))throw Error(`CSV row ${rows+2} contains negative tick volume.`);
    first??=timestamp;last=timestamp;rows++;
  }
  if(!headers||!rows)throw Error('CSV has no data rows.');
  return {headers,first,last,rows,checksum:checksum.digest('hex')};
}

function overlapStart(last,timeframe) {
  if(timeframe==='mn1'){
    const date=new Date(last);return Date.UTC(date.getUTCFullYear(),date.getUTCMonth()-1,1);
  }
  const duration={tick:3600000,s1:1000,m1:60000,m5:300000,m15:900000,m30:1800000,h1:3600000,h4:14400000,d1:86400000}[timeframe];
  if(!duration)throw Error('Unsupported refresh timeframe.');
  return last-duration*2;
}

function closedBoundary(now,timeframe){
  if(timeframe==='tick')return now;
  if(timeframe==='mn1'){const date=new Date(now);return Date.UTC(date.getUTCFullYear(),date.getUTCMonth(),1);}
  const duration={s1:1000,m1:60000,m5:300000,m15:900000,m30:1800000,h1:3600000,h4:14400000,d1:86400000}[timeframe];
  if(!duration)throw Error('Unsupported refresh timeframe.');
  return Math.floor(now/duration)*duration;
}

async function* groups(iterator,timestampIndex) {
  let current=[],timestamp;
  for await(const row of iterator){
    const next=Number(row[timestampIndex]);
    if(current.length&&next!==timestamp){yield {timestamp,rows:current};current=[];}
    timestamp=next;current.push(row);
    // Even malicious CSVs with millions of equal timestamps cannot exhaust RAM.
    if(current.length>100000)throw Error('More than 100,000 rows share one timestamp; refresh cannot safely merge this CSV.');
  }
  if(current.length)yield {timestamp,rows:current};
}

async function refresh(request,options={}) {
  const report=options.emit??emit;
  if(!path.isAbsolute(request.input)||!path.isAbsolute(request.output)||request.input===request.output)throw Error('Separate absolute input and output paths are required.');
  const original=await fsp.lstat(request.input);
  if(!original.isFile())throw Error('Refresh input must be a regular CSV file.');
  let lastScanProgress=0;
  const scanned=await scan(request.input,request.timeframe,(bytes,rows)=>{
    if(Date.now()-lastScanProgress>=200){lastScanProgress=Date.now();report({type:'progress',progress:Math.min(1.9,bytes/Math.max(1,original.size)*2),rows,message:'Verifying saved CSV coverage and indicator recipes'});}
  });
  let savedRecipes=request.recipes??[];
  if(request.recipeFile){
    if(!path.isAbsolute(request.recipeFile)||path.dirname(request.recipeFile)!==path.dirname(request.output))throw Error('Recipe file must be in the private refresh folder.');
    const info=await fsp.lstat(request.recipeFile);
    if(!info.isFile()||info.size>1024*1024)throw Error('Applied indicator recipe file is invalid.');
    savedRecipes=JSON.parse(await fsp.readFile(request.recipeFile,'utf8'))??[];
  }
  if(!Array.isArray(savedRecipes)||savedRecipes.length>2000)throw Error('Applied indicator recipe list is invalid.');
  const recipes=await recipesFor(scanned.headers,savedRecipes);
  const now=options.now??Date.now();
  if(scanned.last>now)throw Error('CSV contains future timestamps; original file kept.');
  const closed=closedBoundary(now,request.timeframe);
  const initial=Date.parse(`${request.from}T00:00:00.000Z`);
  if(!Number.isFinite(initial)||initial>scanned.first)throw Error('The downloaded filename start does not cover its CSV timestamps.');
  // First refresh verifies every source interval. After a durable fingerprint
  // confirms unchanged coverage, only the last two bars/hour plus new history
  // need fetching. A user edit invalidates that fingerprint in the Go owner.
  const coverageVerified=request.coverageVerified&&request.coverageDigest===scanned.checksum;
  const from=Math.max(initial,coverageVerified?overlapStart(scanned.last,request.timeframe):initial);
  const downloaded=`${request.output}.downloaded.csv`;
  await fsp.writeFile(downloaded,'',{flag:'wx',mode:0o600});
  let hasDownloaded=false;
  if(from<now){
    report({type:'progress',progress:2,rows:scanned.rows,message:coverageVerified?'Downloading recent overlap and new data':'Checking complete history for missing data (first refresh or changed source)'});
    try{
      // Go runs up to twenty file workers. Each worker fetches one source at a
      // time so their combined upstream concurrency never exceeds twenty.
      await (options.download??download)({symbol:request.symbol,timeframe:request.timeframe,from:new Date(from).toISOString(),to:new Date(now).toISOString(),output:downloaded,sourceBatchSize:1},
        {api:options.api,pauseMs:options.pauseMs,emit:event=>{if(event.type==='progress')report({...event,progress:2+event.progress*0.58});}});
      hasDownloaded=true;
    }catch(error){
      // A public market closure can legitimately return zero new records.
      // Transport, validation and decoding errors still abort publication.
      if(!/Dukascopy returned no rows|No available history in the requested UTC date range/.test(error.message))throw error;
    }
  }
  const headers=scanned.headers,normalized=headers.map(key),timestampIndex=normalized.indexOf('timestamp');
  const base=request.timeframe==='tick'?tickFields:candleFields;
  const baseIndexes=base.map(name=>normalized.indexOf(name));
  const calculations=await Promise.all(recipes.map(recipe=>calculator(recipe,headers)));
  const outputs=calculations.map(calculate=>calculate.columns.map(name=>normalized.indexOf(name)));
  const leftRows=records(request.input);await leftRows.next();
  const rightRows=records(downloaded);
  const fetchedHeader=hasDownloaded?(await rightRows.next()).value:[];
  const fetchedNormalized=(fetchedHeader??[]).map(key);
  if(hasDownloaded&&(base.some(name=>!fetchedNormalized.includes(name))||fetchedHeader.length!==base.length))throw Error('Dukascopy changed its CSV fields during refresh.');
  const fetchedTimestamp=fetchedNormalized.indexOf('timestamp');
  const left=groups(leftRows,timestampIndex),right=groups(rightRows,fetchedTimestamp);
  let a=await left.next(),b=hasDownloaded?await right.next():{done:true};
  let handle,rows=0,addedRows=0,updatedRows=0,buffer='',bytes=0,lastProgress=0,firstTimestamp,lastTimestamp;
  const outputChecksum=createHash('sha256');
  const transform=fetched=>{
    const row=Array(headers.length).fill('');
    base.forEach((name,i)=>{row[baseIndexes[i]]=fetched[fetchedNormalized.indexOf(name)];});
    return row;
  };
  const baseKey=row=>JSON.stringify(baseIndexes.map(index=>Number(row[index])));
  const append=async row=>{
    const timestamp=Number(row[timestampIndex]);
    firstTimestamp??=timestamp;lastTimestamp=timestamp;
    for(let index=0;index<calculations.length;index++){
      const values=calculations[index].next(row,rows+2);
      outputs[index].forEach((position,i)=>{if(position>=0)row[position]=values[i];});
    }
    const serialized=line(row);outputChecksum.update(serialized);buffer+=serialized;bytes+=Buffer.byteLength(serialized);rows++;
    if(bytes>=128*1024){await writeAll(handle,buffer);buffer='';bytes=0;}
    if(Date.now()-lastProgress>=200){lastProgress=Date.now();report({type:'progress',progress:Math.min(98,60+rows/Math.max(scanned.rows,rows)*38),rows,addedRows,updatedRows,message:`Merging data and recalculating ${recipes.length} applied indicator recipes`});}
    if(options.onRow)await options.onRow(rows);
  };
  try{
    handle=await fsp.open(request.output,'wx',original.mode&0o777);
    const headerLine=line(headers);outputChecksum.update(headerLine);await writeAll(handle,headerLine);
    while(!a.done||!b.done){
      if(b.done||(!a.done&&a.value.timestamp<b.value.timestamp)){
        for(const row of a.value.rows)await append([...row]);
        a=await left.next();
      }else if(a.done||b.value.timestamp<a.value.timestamp){
        // Newly added candles must be closed. If an older download already
        // contained its open candle, the equal-timestamp branch revises it.
        if(request.timeframe==='tick'||b.value.timestamp<closed)for(const row of b.value.rows){await append(transform(row));addedRows++;}
        b=await right.next();
      }else{
        const originals=a.value.rows,fetched=b.value.rows.map(transform);
        if(request.timeframe!=='tick'){
          if(fetched.length!==1||originals.length!==1)throw Error('Dukascopy returned duplicate candle timestamps.');
          if(baseKey(originals[0])!==baseKey(fetched[0]))updatedRows++;
          await append(fetched[0]);
        }else{
          // Keep source ordering for the common prefix, consume equal records
          // as a multiset, then append unmatched originals. Identical ticks at
          // one timestamp retain max(old count,new count), never collapse to one.
          const remaining=new Map();
          for(const row of originals){const id=baseKey(row);remaining.set(id,(remaining.get(id)??0)+1);}
          for(const row of fetched){const id=baseKey(row),count=remaining.get(id)??0;if(count)remaining.set(id,count-1);else addedRows++;await append(row);}
          for(const row of originals){const id=baseKey(row),count=remaining.get(id)??0;if(count){remaining.set(id,count-1);await append([...row]);}}
        }
        a=await left.next();b=await right.next();
      }
    }
    if(buffer)await writeAll(handle,buffer);
    await handle.sync();
    const columns=headers.filter(name=>!base.includes(key(name)));
    const result={type:'done',rows,addedRows,updatedRows,columns,recipes,firstTimestamp,lastTimestamp,checksum:outputChecksum.digest('hex'),checkedThrough:now,message:`Refreshed ${rows.toLocaleString('en-US')} rows: ${addedRows.toLocaleString('en-US')} added, ${updatedRows.toLocaleString('en-US')} updated; ${recipes.length} indicator recipes recalculated`};
    report(result);return result;
  }finally{
    await handle?.close();await left.return();await right.return();
  }
}

if(require.main===module)main(refresh).catch(error=>{
  emit({type:'error',message:String(error?.message||error).slice(0,4000)});process.exitCode=1;
});
module.exports={refresh,recipesFor,scan,overlapStart,closedBoundary};

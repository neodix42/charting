'use strict';

// Immutable, checksummed UTC-hour BID quote chunks are shared between ratios.
// Only one upstream source is decoded at a time; histories are streamed from disk.
const fs=require('node:fs');
const fsp=require('node:fs/promises');
const path=require('node:path');
const readline=require('node:readline');
const {createHash,randomUUID}=require('node:crypto');
const {setTimeout:delay}=require('node:timers/promises');
const {createProviderFetcher}=require('./provider-session.cjs');
const HOUR=3600000,MAX_BYTES=64*1024*1024,MAX_ROWS=1000000;
const id=()=>randomUUID().replaceAll('-','');

async function directory(filename){
  await fsp.mkdir(filename,{recursive:true,mode:0o700});
  if(!(await fsp.lstat(filename)).isDirectory())throw Error('Synthetic quote cache must be a real directory.');
}
async function regular(filename,maxBytes){
  const info=await fsp.lstat(filename);
  if(!info.isFile()||info.size>maxBytes)throw Error('Invalid synthetic quote cache file.');
  return info;
}
async function digest(filename){
  const hash=createHash('sha256'),stream=fs.createReadStream(filename);
  try{for await(const chunk of stream)hash.update(chunk);}finally{stream.destroy();}
  return hash.digest('hex');
}
async function cached(folder,hour,url){
  const filename=path.join(folder,`${hour}.json`);
  try{
    await regular(filename,4096);
    const value=JSON.parse(await fsp.readFile(filename,'utf8'));
    if(value.version!==1||value.hour!==hour||value.url!==url||!Number.isSafeInteger(value.rows)||value.rows<0||value.rows>MAX_ROWS||
      !Number.isSafeInteger(value.bytes)||value.bytes<0||value.bytes>MAX_BYTES||!/^[a-f0-9]{64}$/.test(value.checksum??'')||
      !new RegExp(`^${hour}-[a-f0-9]{32}\\.csv$`).test(value.file??''))return null;
    const file=path.join(folder,value.file),info=await regular(file,MAX_BYTES);
    if(info.size!==value.bytes||await digest(file)!==value.checksum)return null;
    return {...value,file};
  }catch(error){if(error.code==='ENOENT'||error instanceof SyntaxError||/Invalid synthetic quote cache/.test(error.message))return null;throw error;}
}
async function lock(filename,signal){
  const deadline=Date.now()+16*60*1000;
  while(true){
    signal?.throwIfAborted();
    try{
      await fsp.mkdir(filename,{mode:0o700});
      await fsp.writeFile(path.join(filename,'owner.json'),JSON.stringify({pid:process.pid}),{flag:'wx',mode:0o600});
      return async()=>fsp.rm(filename,{recursive:true,force:true});
    }catch(error){if(error.code!=='EEXIST')throw error;}
    const info=await fsp.lstat(filename).catch(error=>{if(error.code==='ENOENT')return null;throw error;});
    if(!info)continue;
    if(!info.isDirectory())throw Error('Synthetic quote cache lock is not a directory.');
    let dead=false;
    try{
      const value=JSON.parse(await fsp.readFile(path.join(filename,'owner.json'),'utf8'));
      if(!Number.isSafeInteger(value.pid)||value.pid<=0)throw Error('Invalid synthetic quote cache owner.');
      try{process.kill(value.pid,0);}catch(error){if(error.code==='ESRCH')dead=true;else if(error.code!=='EPERM')throw error;}
    }catch(error){if(error.code==='ENOENT'&&Date.now()-info.mtimeMs>5000)dead=true;else if(error.code!=='ENOENT')throw error;}
    if(dead){
      const abandoned=`${filename}.abandoned-${id()}`;
      try{await fsp.rename(filename,abandoned);await fsp.rm(abandoned,{recursive:true,force:true});}catch(error){if(error.code!=='ENOENT')throw error;}
      continue;
    }
    if(Date.now()>=deadline)throw Error('Timed out waiting for shared synthetic quotes. Retry the job.');
    await delay(25,undefined,{signal});
  }
}
function emptySource(item){
  if(!item.buffer.length)return true;
  let value;try{value=JSON.parse(item.buffer.toString('utf8'));}catch{return false;}
  return value&&Number.isFinite(value.timestamp)&&Array.isArray(value.times)&&!value.times.length&&
    ['asks','bids','askVolumes','bidVolumes'].every(name=>Array.isArray(value[name])&&!value[name].length);
}
async function publish(folder,hour,url,rows,signal){
  const basename=`${hour}-${id()}.csv`,file=path.join(folder,basename),temporary=`${file}.pending`;
  let handle,bytes=0,count=0,previous=-Infinity,buffer='';const hash=createHash('sha256');
  try{
    handle=await fsp.open(temporary,'wx',0o600);
    const write=async text=>{const data=Buffer.from(text);bytes+=data.length;if(bytes>MAX_BYTES)throw Error('Synthetic quote chunk exceeds the bounded source size.');hash.update(data);buffer+=text;if(Buffer.byteLength(buffer)>=128*1024){await handle.writeFile(buffer);buffer='';}};
    await write('timestamp,bid\n');
    for await(const row of rows){
      signal?.throwIfAborted();
      const [timestamp,,bid]=row;
      if(!Number.isSafeInteger(timestamp)||timestamp<hour||timestamp>=hour+HOUR||timestamp<previous||!Number.isFinite(bid)||bid<=0)
        throw Error('Invalid or unordered source BID quote.');
      if(++count>MAX_ROWS)throw Error('Synthetic quote chunk exceeds the bounded source count.');
      previous=timestamp;await write(`${timestamp},${bid}\n`);
    }
    if(buffer)await handle.writeFile(buffer);
    await handle.sync();await handle.close();handle=null;
    await fsp.rename(temporary,file);
    const value={version:1,hour,url,file:basename,rows:count,bytes,checksum:hash.digest('hex')};
    const manifest=path.join(folder,`${hour}.json`),pending=`${manifest}.${id()}.pending`;
    try{
      const metadata=await fsp.open(pending,'wx',0o600);
      try{await metadata.writeFile(JSON.stringify(value));await metadata.sync();}finally{await metadata.close();}
      await fsp.rename(pending,manifest);
    }finally{await fsp.unlink(pending).catch(error=>{if(error.code!=='ENOENT')throw error;});}
    try{const dir=await fsp.open(folder,'r');try{await dir.sync();}finally{await dir.close();}}catch(error){if(!['EINVAL','EPERM','EISDIR','EACCES'].includes(error.code))throw error;}
    return {...value,file};
  }finally{await handle?.close();await fsp.unlink(temporary).catch(error=>{if(error.code!=='ENOENT')throw error;});}
}
function covers(coverage,hour){
  if(!Array.isArray(coverage)||coverage.length>10000)return false;
  let cursor=hour;
  for(const range of [...coverage].sort((a,b)=>a.from-b.from)){
    if(!Number.isSafeInteger(range.from)||!Number.isSafeInteger(range.to)||range.from>=range.to)return false;
    if(range.from>cursor)break;
    if(range.to>cursor)cursor=range.to;
    if(cursor>=hour+HOUR)return true;
  }
  return false;
}
async function importHistory(symbol,from,to,request,api,folder,options){
  const history=request.historyDirectory,signal=options.signal;
  if(!history)return;
  if(!path.isAbsolute(history)||(await fsp.lstat(history)).isDirectory()!==true)throw Error('Existing tick history directory must be an absolute real directory.');
  const files=(await fsp.readdir(history)).filter(name=>new RegExp(`^${symbol}-tick-\\d{4}-\\d{2}-\\d{2}-\\d{4}-\\d{2}-\\d{2}(?:-\\d+)?\\.csv$`).test(name)).sort();
  for(const filename of files){
    signal?.throwIfAborted();
    const file=path.join(history,filename),metadataFile=path.join(history,'.cryptodesk-screener-metadata',createHash('sha256').update(file).digest('hex')+'.json');
    let metadata,info;
    try{
      info=await regular(file,Number.MAX_SAFE_INTEGER);await regular(metadataFile,8*1024*1024);
      metadata=JSON.parse(await fsp.readFile(metadataFile,'utf8'));
      if(metadata.version!==1||metadata.filename!==filename||metadata.verified!==true||metadata.bytes!==info.size||!metadata.coverage?.length||
        !/^[a-f0-9]{64}$/.test(metadata.checksum??'')||await digest(file)!==metadata.checksum)continue;
    }catch(error){if(error.code==='ENOENT'||error instanceof SyntaxError||/Invalid synthetic quote cache/.test(error.message))continue;throw error;}
    const {records}=require('./refresh-worker.cjs'),input=records(file);
    const stage=await fsp.mkdtemp(path.join(folder,`.import-${process.pid}-`));
    let fields,compatible=false,hour=null,rows=[],previous=-Infinity;
    const commit=async()=>{
      if(hour===null||hour+HOUR<=from||hour>=to||!covers(metadata.coverage,hour))return;
      const urls=api.generateUrls({instrument:symbol,timeframe:'tick',priceType:'bid',startDate:new Date(hour),endDate:new Date(hour+HOUR)});
      if(urls.length!==1)throw Error('Provider changed the one-hour tick source plan.');
      if(await cached(folder,hour,urls[0]))return;
      await publish(stage,hour,urls[0],rows,signal);
    };
    try{
      for await(const row of input){
        signal?.throwIfAborted();
        if(!fields){
          const names=row.map(value=>value.trim().toLowerCase());fields=['timestamp','askprice','bidprice','askvolume','bidvolume'].map(name=>names.indexOf(name));
          if(fields.some(index=>index<0))break;
          compatible=true;
          continue;
        }
        const values=fields.map(index=>row[index]?.trim()===''?NaN:Number(row[index])),[timestamp,ask,bid,askVolume,bidVolume]=values;
        if(values.some(value=>!Number.isFinite(value))||!Number.isSafeInteger(timestamp)||timestamp<previous||ask<=0||bid<=0||askVolume<0||bidVolume<0)throw Error('Verified tick history contains invalid source quotes.');
        previous=timestamp;
        const next=Math.floor(timestamp/HOUR)*HOUR;
        if(hour!==next){await commit();hour=next;rows=[];}
        if(hour+HOUR>from&&hour<to&&covers(metadata.coverage,hour)){
          if(rows.length>=MAX_ROWS)throw Error('Existing tick hour exceeds the bounded quote count.');rows.push([timestamp,ask,bid]);
        }
      }
      await commit();
      if(!compatible)continue;
      const after=await fsp.lstat(file);
      if(after.size!==info.size||after.mtimeMs!==info.mtimeMs||await digest(file)!==metadata.checksum)throw Error('Existing tick history changed while being reused.');
      // A verified query ledger also establishes empty market-closure hours.
      for(let empty=Math.floor(from/HOUR)*HOUR;empty<to;empty+=HOUR){
        if(!covers(metadata.coverage,empty))continue;
        const url=api.generateUrls({instrument:symbol,timeframe:'tick',priceType:'bid',startDate:new Date(empty),endDate:new Date(empty+HOUR)})[0];
        if(!url||await cached(folder,empty,url)||await cached(stage,empty,url))continue;
        await publish(stage,empty,url,[],signal);
      }
      const entries=await fsp.opendir(stage);
      for await(const entry of entries){
        if(!/^\d+\.json$/.test(entry.name))continue;
        const value=JSON.parse(await fsp.readFile(path.join(stage,entry.name),'utf8'));
        const chunk=await cached(stage,value.hour,value.url);
        if(!chunk)throw Error('Staged tick reuse changed before publication.');
        const release=await lock(path.join(folder,`${value.hour}.lock`),signal);
        try{
          if(!await cached(folder,value.hour,value.url)){
            const bids=(async function*(){for await(const [time,bid] of readQuotes(chunk,value.hour,value.hour+HOUR,signal))yield [time,0,bid];})();
            await publish(folder,value.hour,value.url,bids,signal);
          }
        }finally{await release();}
      }
    }finally{await input.return();await fsp.rm(stage,{recursive:true,force:true});}
  }
}
async function* readQuotes(chunk,from,to,signal){
  const stream=fs.createReadStream(chunk.file),lines=readline.createInterface({input:stream,crlfDelay:Infinity});
  let header=false,count=0,previous=-Infinity;
  try{
    for await(const line of lines){
      signal?.throwIfAborted();
      if(!header){if(line!=='timestamp,bid')throw Error('Invalid cached BID quote header.');header=true;continue;}
      const fields=line.split(','),timestamp=Number(fields[0]),price=Number(fields[1]);
      if(fields.length!==2||!Number.isSafeInteger(timestamp)||timestamp<chunk.hour||timestamp>=chunk.hour+HOUR||timestamp<previous||!Number.isFinite(price)||price<=0)
        throw Error('Invalid cached BID quote.');
      previous=timestamp;count++;
      if(timestamp>=from&&timestamp<to)yield [timestamp,price];
    }
    if(!header||count!==chunk.rows)throw Error('Cached BID quote count changed.');
  }finally{lines.close();stream.destroy();}
}
async function* sourceQuotes(symbol,from,to,request,options={}){
  const api=options.api??require('dukascopy-node'),signal=options.signal;
  if(!/^[a-z0-9]+$/.test(symbol)||!api.instrumentMetaData[symbol]||!Number.isSafeInteger(from)||!Number.isSafeInteger(to)||from>=to)throw Error('Invalid synthetic source quote range.');
  const root=request.syntheticCacheDirectory??path.join(request.providerSession?path.dirname(request.providerSession.tokenCache):path.dirname(request.output),'synthetic-quotes-v1');
  if(!path.isAbsolute(root))throw Error('Synthetic quote cache path must be absolute.');
  const folder=path.join(root,symbol);await directory(root);await directory(folder);
  const config={instrument:symbol,dates:{from:new Date(from),to:new Date(to)},timeframe:'tick',format:'array',priceType:'bid',volumes:false,ignoreFlats:false,utcOffset:0,
    batchSize:1,requestIntervalMs:250,retryCount:0,retryOnEmpty:false,failAfterRetryCount:true,rateLimitRetryCount:2,wafMode:'auto',wafTimeoutMs:60000,wafTokenCache:true,
    wafTokenCachePath:request.providerSession?.tokenCache??path.join(root,'.dukascopy-waf-token.json')};
  const checked=api.validateConfigNode(config);
  if(!checked.isValid)throw Error(`Invalid synthetic source request: ${JSON.stringify(checked.validationErrors)}`);
  const input=checked.input,fetcher=createProviderFetcher(api,input,request.providerSession);
  const first=Math.max(Math.floor(from/HOUR)*HOUR,Math.floor(Date.parse(api.instrumentMetaData[symbol].startHourForTicks)/HOUR)*HOUR);
  let imported=false;
  for(let hour=first;hour<to;hour+=HOUR){
    signal?.throwIfAborted();
    // Only complete source hours are cached or used by published daily bars.
    if(hour+HOUR>Math.min(request.asOf??Date.now(),Date.now()))break;
    const urls=api.generateUrls({instrument:symbol,timeframe:'tick',priceType:'bid',startDate:new Date(hour),endDate:new Date(hour+HOUR)});
    if(urls.length!==1)throw Error('Provider changed the one-hour tick source plan.');
    const url=urls[0];let chunk=await cached(folder,hour,url),reused=!!chunk;
    if(!chunk&&!imported){
      const key=`${root}|${symbol}`;
      if(!options.importedHistories?.has(key)){
        await importHistory(symbol,Math.max(options.historyRange?.from??from,first),options.historyRange?.to??to,request,api,folder,options);
        options.importedHistories?.add(key);
      }
      imported=true;chunk=await cached(folder,hour,url);reused=!!chunk;
    }
    if(!chunk){
      const release=await lock(path.join(folder,`${hour}.lock`),signal);
      try{
        chunk=await cached(folder,hour,url);reused=!!chunk;
        if(!chunk){
          const buffers=await fetcher.fetch([url]);signal?.throwIfAborted();
          if(buffers.length!==1||buffers[0].url!==url||!Buffer.isBuffer(buffers[0].buffer)||buffers[0].buffer.length>MAX_BYTES)throw Error('Invalid or oversized provider tick source.');
          const rows=emptySource(buffers[0])?[]:api.processData({...input,requestedTimeframe:'tick',bufferObjects:buffers});
          chunk=await publish(folder,hour,url,rows,signal);
        }
      }finally{await release();}
    }
    options.onChunk?.({symbol,hour,reused,rows:chunk.rows});
    yield* readQuotes(chunk,from,to,signal);
  }
}
module.exports={sourceQuotes,HOUR};

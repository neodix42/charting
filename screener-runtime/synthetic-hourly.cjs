'use strict';

// Fast inputs are genuine closed H1 BID candles, never independent source
// highs/lows divided into fictitious ratio extrema. The SDK proves that each
// ratio input hour was open for its entire interval before exporting it.
const fs=require('node:fs/promises');
const path=require('node:path');
const {createHash,randomUUID}=require('node:crypto');
const {cacheIO}=require('./synthetic-quotes.cjs');
const {records}=require('./refresh-worker.cjs');
const HOUR=3600000,DAY=24*HOUR,BATCH_DAYS=28;
const policies={h1:'verified-full-hour-v1','h1-native':'provider-native-h1-v1'};
const MAX_BYTES=128*1024;

async function syncDirectory(folder){
  let handle;
  try{handle=await fs.open(folder,'r');await handle.sync();}
  catch(error){if(!['EINVAL','EPERM','EISDIR','EACCES'].includes(error.code))throw error;}
  finally{await handle?.close();}
}
async function atomic(filename,text){
  const pending=`${filename}.${randomUUID()}.pending`;
  let handle;
  try{
    handle=await fs.open(pending,'wx',0o600);await handle.writeFile(text);await handle.sync();await handle.close();handle=null;
    await fs.rename(pending,filename);await syncDirectory(path.dirname(filename));
  }finally{await handle?.close();await fs.unlink(pending).catch(e=>{if(e.code!=='ENOENT')throw e;});}
}
function validateRow(row,from,to,previous){
  if(row.length!==5)throw Error('Invalid hourly BID observation columns.');
  const [time,open,high,low,close]=row;
  if(!Number.isSafeInteger(time)||time%HOUR||time<from||time+HOUR>to||time<=previous||
    row.slice(1).some(v=>!Number.isFinite(v)||v<=0)||high<low||open<low||open>high||close<low||close>high)
    throw Error('Invalid, open or unordered hourly BID observations.');
}
async function readRows(filename,from,to,expected,signal){
  const input=records(filename),rows=[];let header=false,previous=-1;
  try{
    for await(const fields of input){
      signal?.throwIfAborted();
      if(!header){
        if(fields.map(v=>v.trim().toLowerCase()).join(',')!=='timestamp,open,high,low,close')throw Error('Hourly BID export changed its columns.');
        header=true;continue;
      }
      const row=fields.map(v=>v.trim()===''?NaN:Number(v));
      validateRow(row,from,to,previous);previous=row[0];rows.push(row);
      if(rows.length>expected||rows.length>1000)throw Error('Hourly BID export exceeded its bounded row count.');
    }
    if(!header||rows.length!==expected)throw Error('Hourly BID export row count does not match its response.');
    return rows;
  }finally{await input.return();}
}
async function cached(folder,day,identity,signal){
  try{
    const manifest=path.join(folder,`${day}.json`);await cacheIO.regular(manifest,4096);
    const value=JSON.parse(await fs.readFile(manifest,'utf8'));
    if(value.version!==1||value.day!==day||value.identity!==identity||!Number.isSafeInteger(value.rows)||value.rows<0||value.rows>24||
      !Number.isSafeInteger(value.bytes)||value.bytes<0||value.bytes>MAX_BYTES||!/^[a-f0-9]{64}$/.test(value.checksum??'')||
      !new RegExp(`^${day}-[a-f0-9-]{36}\\.csv$`).test(value.file??''))return null;
    const file=path.join(folder,value.file),info=await cacheIO.regular(file,MAX_BYTES);
    if(info.size!==value.bytes||await cacheIO.digest(file)!==value.checksum)return null;
    return {...value,rows:await readRows(file,day,day+DAY,value.rows,signal)};
  }catch(error){
    if(error.code==='ENOENT'||error instanceof SyntaxError||/Invalid synthetic quote cache/.test(error.message))return null;
    throw error;
  }
}
async function publish(folder,day,identity,rows,signal){
  signal?.throwIfAborted();
  const file=`${day}-${randomUUID()}.csv`,text='timestamp,open,high,low,close\n'+rows.map(r=>r.join(',')+'\n').join('');
  if(Buffer.byteLength(text)>MAX_BYTES||rows.length>24)throw Error('Hourly cache exceeds its bounded daily size.');
  await atomic(path.join(folder,file),text);signal?.throwIfAborted();
  const metadata={version:1,day,identity,file,rows:rows.length,bytes:Buffer.byteLength(text),checksum:createHash('sha256').update(text).digest('hex')};
  await atomic(path.join(folder,`${day}.json`),JSON.stringify(metadata));
  return {...metadata,rows};
}

async function* hourlyBars(symbol,from,to,request,options={}){
  const timeframe=options.native?'h1-native':'h1',policy=policies[timeframe],signal=options.signal;
  const sourceKey=request.providerSession?.sdk?.sourceKey;
  if(!/^[a-f0-9]{64}$/.test(sourceKey??''))throw Error('Fast synthetic history requires a configured JForex connection; select tick observations for the public downloader.');
  if(!/^[a-z0-9]+$/.test(symbol)||!Number.isSafeInteger(from)||!Number.isSafeInteger(to)||from<0||from>=to)throw Error('Invalid hourly observation range.');
  const api=options.api??require('dukascopy-node');
  const available=Date.parse(api.instrumentMetaData?.[symbol]?.startMonthForHourlyCandles??'');
  if(!Number.isSafeInteger(available)||available<0)throw Error(`Required hourly availability metadata is invalid for ${symbol}.`);
  const firstHour=Math.ceil(available/HOUR)*HOUR;
  const cutoff=Math.floor(Math.min(request.asOf??Date.now(),Date.now())/DAY)*DAY;
  const firstDay=Math.floor(from/DAY)*DAY,lastDay=Math.min(Math.ceil(to/DAY)*DAY,cutoff);
  const fetchLastDay=Math.max(lastDay,Math.min(Math.ceil((options.historyRange?.to??to)/DAY)*DAY,cutoff));
  if(firstDay>=lastDay)return;
  const base=request.hourlyCacheDirectory??path.join(request.providerSession?path.dirname(request.providerSession.tokenCache):path.dirname(request.output),'synthetic-hourly-v1');
  if(!path.isAbsolute(base))throw Error('Hourly observation cache must be absolute.');
  const folder=path.join(base,sourceKey,timeframe,symbol);
  await cacheIO.directory(base);await cacheIO.directory(path.join(base,sourceKey));await cacheIO.directory(path.join(base,sourceKey,timeframe));await cacheIO.directory(folder);
  const identity=createHash('sha256').update(JSON.stringify({sourceKey,symbol,timeframe,policy,available})).digest('hex');
  for(let day=firstDay;day<lastDay;day+=DAY){
    signal?.throwIfAborted();
    let chunk=await cached(folder,day,identity,signal),reused=!!chunk;
    if(!chunk){
      const release=await cacheIO.lock(path.join(folder,'.fetch.lock'),signal);
      try{
        chunk=await cached(folder,day,identity,signal);reused=!!chunk;
        if(!chunk){
          const batchEnd=Math.min(fetchLastDay,day+BATCH_DAYS*DAY);let missingEnd=day+DAY;
          while(missingEnd<batchEnd&&!await cached(folder,missingEnd,identity,signal))missingEnd+=DAY;
          const start=Math.max(day,firstHour),all=[];
          if(start<missingEnd){
            const result=await require('./sdk-worker.cjs').requestSDKChunk(request,{symbol,timeframe,from:start,to:missingEnd},options);
            try{
              if(result.sessionPolicy!==policy)throw Error('SDK did not verify the hourly observation session policy.');
              if(!Number.isSafeInteger(result.rows)||result.rows<0||result.rows>1000)throw Error('Invalid bounded SDK hourly row count.');
              all.push(...await readRows(result.path,start,missingEnd,result.rows,signal));
            }finally{await fs.unlink(result.path).catch(e=>{if(e.code!=='ENOENT')throw e;});}
          }
          // Validate the entire <=672-row export before publishing any day.
          for(let next=day;next<missingEnd;next+=DAY){
            const value=await publish(folder,next,identity,all.filter(r=>r[0]>=next&&r[0]<next+DAY),signal);
            if(next===day)chunk=value;
          }
        }
      }finally{await release();}
    }
    options.onHourlyDay?.({symbol,day,reused,rows:chunk.rows.length});
    for(const row of chunk.rows){signal?.throwIfAborted();if(row[0]>=from&&row[0]+HOUR<=to)yield row;}
  }
}

async function* aggregateHourlyRatio(numerator,denominator,options={}){
  if(options.verifiedSessions!==true)throw Error('Hourly ratios require verified common open hours.');
  let a,b,bar,previousA=-1,previousB=-1;
  const nextA=async()=>{const value=await numerator.next();if(!value.done){validateRow(value.value,0,Number.MAX_SAFE_INTEGER,previousA);previousA=value.value[0];}return value;};
  const nextB=async()=>{const value=await denominator.next();if(!value.done){validateRow(value.value,0,Number.MAX_SAFE_INTEGER,previousB);previousB=value.value[0];}return value;};
  try{
    [a,b]=await Promise.all([nextA(),nextB()]);
    while(!a.done&&!b.done){
      options.signal?.throwIfAborted();
      if(a.value[0]<b.value[0]){a=await nextA();continue;}
      if(b.value[0]<a.value[0]){b=await nextB();continue;}
      const time=a.value[0]+HOUR-1,value=a.value[4]/b.value[4],day=Math.floor(time/DAY)*DAY;
      if(!Number.isFinite(value)||value<=0||!Number.isFinite(a.value[4])||a.value[4]<=0||!Number.isFinite(b.value[4])||b.value[4]<=0)throw Error('Invalid hourly ratio observation.');
      if(!bar||bar.timestamp!==day){if(bar)yield bar;bar={timestamp:day,open:value,high:value,low:value,close:value};}
      else{bar.high=Math.max(bar.high,value);bar.low=Math.min(bar.low,value);bar.close=value;}
      [a,b]=await Promise.all([nextA(),nextB()]);
    }
    if(bar)yield bar;
  }finally{await numerator.return?.();await denominator.return?.();}
}
async function* aggregateNativeHourly(input,options={}){
  let bar;
  try{
    for await(const row of input){
      options.signal?.throwIfAborted();const [time,open,high,low,close]=row,day=Math.floor(time/DAY)*DAY;
      if(!bar||bar.timestamp!==day){if(bar)yield bar;bar={timestamp:day,open,high,low,close};}
      else{bar.high=Math.max(bar.high,high);bar.low=Math.min(bar.low,low);bar.close=close;}
    }
    if(bar)yield bar;
  }finally{await input.return?.();}
}
module.exports={hourlyBars,aggregateHourlyRatio,aggregateNativeHourly,HOUR,DAY};

'use strict';

// The app owns authenticated SDK access and temporary exports. Node receives
// only an opaque source identity and paths inside that job's private folder.
const fs=require('node:fs/promises');
const streams=require('node:fs');
const path=require('node:path');
const {createHash,randomUUID}=require('node:crypto');
const {pipeline}=require('node:stream/promises');
const {setTimeout:delay}=require('node:timers/promises');
const {requestSDKHistory}=require('./provider-transport.cjs');
const DAY=86400000;
const transientTickTimeouts=new Set([
  'JForex tick history loading timed out. Completed quote hours are retained; retry to resume.',
  'JForex tick history subscription timed out',
  'JForex historical data loading timed out. Live quotes can continue; retry the chart to reload history.',
]);

async function requestSDKChunk(request,fields,options={}){
  const ask=options.sdkHistory??requestSDKHistory;
  try{return await ask(request,fields);}
  catch(error){
    const tick=fields.timeframe==='tick'&&fields.to-fields.from===3600000&&transientTickTimeouts.has(error.message);
    const daily=fields.timeframe==='d1'&&Number.isSafeInteger(fields.from)&&Number.isSafeInteger(fields.to)&&fields.from%DAY===0&&fields.to%DAY===0&&
      fields.to>fields.from&&fields.to-fields.from<=366*DAY&&error.message==='JForex native daily download timed out. Completed history is retained; retry to resume.';
    if(error?.code!=='JFOREX_HISTORY'||(!tick&&!daily)||options.signal?.aborted)throw error;
    options.signal?.throwIfAborted();
    options.onRetry?.({symbol:fields.symbol,timeframe:fields.timeframe,from:fields.from,to:fields.to,...(tick?{hour:fields.from}:{}),attempt:2,delayMs:5000});
    // A single retry may consume the SDK's now-warmed chunk. Every retry still
    // passes the owner's FIFO/global429/source fences and gets a fresh export.
    await delay(options.sdkRetryDelayMs??5000,undefined,{signal:options.signal});
    options.signal?.throwIfAborted();return ask(request,fields);
  }
}

async function hashFile(filename){
  const hash=createHash('sha256'),input=streams.createReadStream(filename);
  try{for await(const chunk of input)hash.update(chunk);}finally{input.destroy();}
  return hash.digest('hex');
}
async function syncFile(filename){const handle=await fs.open(filename,'r');try{await handle.sync();}finally{await handle.close();}}
async function syncDirectory(directory){
  try{await syncFile(directory);}catch(error){if(!['EINVAL','EPERM','EISDIR','EACCES'].includes(error.code))throw error;}
}
async function finishPublicUpgrade(request,publicIdentity,options={}){
  const directory=path.dirname(request.output),journalFile=path.join(directory,'public-sdk-upgrade.json');
  const checkpointName=options.nativePublic?'checkpoint.json':'synthetic-checkpoint.json',kind=options.nativePublic?'native':'ratio';
  let journal;
  try{
    const info=await fs.lstat(journalFile);if(!info.isFile()||info.size>4096)throw Error('Invalid public-to-SDK upgrade journal.');
    journal=JSON.parse(await fs.readFile(journalFile,'utf8'));
  }catch(error){if(error.code==='ENOENT')return null;throw error;}
  if(journal.version!==1||(journal.kind??'ratio')!==kind||(journal.checkpointName??'synthetic-checkpoint.json')!==checkpointName||journal.sourceKey!==request.providerSession?.sdk?.sourceKey||journal.identity!==publicIdentity||journal.output!==path.basename(request.output)||
    !/^\.public-partial-[a-f0-9-]{36}$/.test(journal.archive??'')||!/^[a-f0-9]{64}$/.test(journal.csvHash??'')||!/^[a-f0-9]{64}$/.test(journal.checkpointHash??''))throw Error('Saved SDK upgrade belongs to another source or is invalid.');
  const archive=path.join(directory,journal.archive),csv=path.join(archive,'public.csv'),checkpoint=path.join(archive,checkpointName);
  for(const [filename,limit] of [[csv,64*1024*1024],[checkpoint,1024*1024]]){
    const info=await fs.lstat(filename);if(!info.isFile()||info.size>limit)throw Error('Saved public partial archive is invalid; originals retained.');
  }
  if(!(await fs.lstat(archive)).isDirectory()||await hashFile(csv)!==journal.csvHash||await hashFile(checkpoint)!==journal.checkpointHash)throw Error('Saved public partial archive changed; originals retained.');
  const saved=JSON.parse(await fs.readFile(checkpoint,'utf8'));
  if((kind==='ratio'?saved.identity!==publicIdentity:saved.version!==1||saved.symbol!==request.symbol||saved.timeframe!=='d1'||saved.from!==request.from||saved.to!==request.to)||!Number.isSafeInteger(saved.end)||saved.end<=saved.start)throw Error('Saved public checkpoint is invalid; originals retained.');
  const current=path.join(directory,checkpointName);
  try{if(await hashFile(current)!==journal.checkpointHash)throw Error('Current checkpoint changed during the SDK upgrade; original files retained.');}
  catch(error){if(error.code!=='ENOENT')throw error;}
  const empty=path.join(directory,`.sdk-upgrade-${randomUUID()}.pending`);
  try{
    const handle=await fs.open(empty,'wx',0o600);try{await handle.sync();}finally{await handle.close();}
    await fs.rename(empty,request.output);await syncDirectory(directory);
    await options.onUpgradeStage?.('after-reset');
    await fs.unlink(current).catch(error=>{if(error.code!=='ENOENT')throw error;});
    await fs.unlink(journalFile);await syncDirectory(directory);
  }finally{await fs.unlink(empty).catch(error=>{if(error.code!=='ENOENT')throw error;});}
  return saved.end;
}
async function archivePublicPartial(request,saved,publicIdentity,options={}){
  const checkpointName=options.nativePublic?'checkpoint.json':'synthetic-checkpoint.json';
  const directory=path.dirname(request.output),checkpoint=path.join(directory,checkpointName);
  const csvInfo=await fs.lstat(request.output),checkpointInfo=await fs.lstat(checkpoint);
  if(!csvInfo.isFile()||csvInfo.size>64*1024*1024||csvInfo.size<saved.bytes||!checkpointInfo.isFile()||checkpointInfo.size>1024*1024)throw Error('Public partial cannot be safely archived; original files retained.');
  const archiveName=`.public-partial-${randomUUID()}`,archive=path.join(directory,archiveName),stage=archive+'.pending';
  await fs.mkdir(stage,{mode:0o700});
  try{
    const copy=path.join(stage,'public.csv'),state=path.join(stage,checkpointName);
    await fs.copyFile(request.output,copy,streams.constants.COPYFILE_EXCL);await fs.copyFile(checkpoint,state,streams.constants.COPYFILE_EXCL);
    if(saved.rows){
      const committed=path.join(stage,'committed.csv');
      await pipeline(streams.createReadStream(copy,{start:0,end:saved.bytes-1}),streams.createWriteStream(committed,{flags:'wx',mode:0o600}),{signal:options.signal});
      const actual=await require('./refresh-worker.cjs').scan(committed,'d1');
      const header=options.nativePublic?'timestamp,open,high,low,close,volume':'timestamp,open,high,low,close';
      if(actual.rows!==saved.rows||actual.last!==saved.lastTimestamp||actual.first<saved.start||actual.last>=(options.nativePublic?saved.end:saved.nextDay)||actual.headers.map(value=>value.trim().toLowerCase()).join(',')!==header)throw Error('Public partial does not match its checkpoint; original files retained.');
      await syncFile(committed);
    }
    const csvHash=await hashFile(copy),checkpointHash=await hashFile(state);
    if(csvHash!==await hashFile(request.output)||checkpointHash!==await hashFile(checkpoint)||JSON.stringify(JSON.parse(await fs.readFile(state,'utf8')))!==JSON.stringify(saved))throw Error('Public partial changed during archival; original files retained.');
    await syncFile(copy);await syncFile(state);await syncDirectory(stage);await fs.rename(stage,archive);await syncDirectory(directory);
    await require('./worker.cjs').writeCheckpoint(path.join(directory,'public-sdk-upgrade.json'),{version:1,kind:options.nativePublic?'native':'ratio',checkpointName,mode:saved.mode??null,sourceKey:request.providerSession.sdk.sourceKey,identity:publicIdentity,
      output:path.basename(request.output),archive:archiveName,csvHash,checkpointHash});
    await options.onUpgradeStage?.('after-archive');
    await finishPublicUpgrade(request,publicIdentity,options);
  }finally{await fs.rm(stage,{recursive:true,force:true});}
}

async function* readSDKCSV(result,timeframe,from,to,signal){
  const fields=timeframe==='tick'?['timestamp','askprice','bidprice','askvolume','bidvolume']:['timestamp','open','high','low','close'];
  const input=require('./refresh-worker.cjs').records(result.path);
  let indexes,count=0,previous=-Infinity;
  try{
    for await(const row of input){
      signal?.throwIfAborted();
      if(!indexes){
        const names=row.map(value=>value.trim().toLowerCase());
        if(names.length!==fields.length||new Set(names).size!==fields.length||fields.some(field=>!names.includes(field)))throw Error('JForex changed its exported history columns.');
        indexes=fields.map(field=>names.indexOf(field));continue;
      }
      const values=indexes.map(index=>row[index]?.trim()===''?NaN:Number(row[index])),[time,...prices]=values;
      if(values.some(value=>!Number.isFinite(value))||!Number.isSafeInteger(time)||time<from||time>=to||time<previous||(timeframe!=='tick'&&time===previous))throw Error('JForex exported invalid or unordered history.');
      if(timeframe==='tick'){
        if(prices[0]<=0||prices[1]<=0||prices[2]<0||prices[3]<0)throw Error('JForex exported invalid BID/ASK ticks.');
      }else if(time%DAY!==0||prices.some(value=>value<=0)||prices[1]<prices[2]||prices[0]<prices[2]||prices[0]>prices[1]||prices[3]<prices[2]||prices[3]>prices[1])throw Error('JForex exported invalid daily OHLC.');
      if(++count>result.rows)throw Error('JForex export row count does not match its response.');
      previous=time;yield values;
    }
    if(!indexes||count!==result.rows)throw Error('JForex export row count does not match its response.');
  }finally{await input.return();}
}

async function sdkNativeDownload(request,options={}){
  if(request.timeframe!=='d1'||!['btcusd','xauxag'].includes(request.symbol))throw Error('Unsupported native JForex history download.');
  const sourceKey=request.providerSession?.sdk?.sourceKey;
  if(!/^[a-f0-9]{64}$/.test(sourceKey??''))throw Error('JForex history source identity is unavailable.');
  const api=options.api??require('dukascopy-node'),signal=options.signal;
  const requestedFrom=Date.parse(request.from),requestedTo=Date.parse(request.to),asOf=request.asOf??Date.now();
  const earliest=Date.parse(api.instrumentMetaData?.[request.symbol]?.startYearForDailyCandles??'');
  if(!Number.isSafeInteger(requestedFrom)||!Number.isSafeInteger(requestedTo)||requestedFrom<0||requestedFrom>=requestedTo||!Number.isSafeInteger(asOf)||asOf<0||!Number.isSafeInteger(earliest)||!path.isAbsolute(request.output))throw Error('Invalid native JForex history request.');
  const start=Math.max(Math.floor(requestedFrom/DAY)*DAY,Math.floor(earliest/DAY)*DAY);
  let end=Math.floor(Math.min(requestedTo,asOf)/DAY)*DAY;
  if(start>=end)throw Error('No available history in the requested UTC date range.');
  const identity=createHash('sha256').update(JSON.stringify({version:1,sourceKey,symbol:request.symbol,timeframe:'d1',from:request.from,to:request.to})).digest('hex');
  const publicIdentity=createHash('sha256').update(JSON.stringify({version:1,kind:'native-public-d1',symbol:request.symbol,from:request.from,to:request.to})).digest('hex');
  const checkpointFile=path.join(path.dirname(request.output),'sdk-checkpoint.json');let saved;
  if(request.resume){
    const completedUpgrade=await finishPublicUpgrade(request,publicIdentity,{...options,nativePublic:true});if(completedUpgrade!==null)end=Math.min(end,Math.floor(completedUpgrade/DAY)*DAY);
    try{saved=JSON.parse(await fs.readFile(checkpointFile,'utf8'));}catch(error){if(error.code!=='ENOENT')throw error;}
    if(saved&&(saved.version!==1||saved.identity!==identity||saved.start!==start||!Number.isSafeInteger(saved.end)||saved.end> end||saved.end<=start||
      !Number.isSafeInteger(saved.next)||saved.next<start||saved.next>saved.end||saved.next%DAY!==0||!Number.isSafeInteger(saved.bytes)||saved.bytes<0||!Number.isSafeInteger(saved.rows)||saved.rows<0||
      (saved.tickFallback!==undefined&&(typeof saved.tickFallback!=='boolean'||saved.tickFallback&&(request.symbol!=='btcusd'||saved.rows===0)))||
      (saved.fallbackUntil!==undefined&&saved.fallbackUntil!==null&&(request.symbol!=='btcusd'||!Number.isSafeInteger(saved.fallbackUntil)||saved.fallbackUntil%DAY!==0||saved.fallbackUntil<=saved.next||saved.fallbackUntil>saved.end||saved.fallbackUntil-saved.next>366*DAY))||
      (saved.rows===0?(saved.bytes!==0||saved.last!==null):(!saved.bytes||!Number.isSafeInteger(saved.last)||saved.last<start||saved.last>=saved.next))))throw Error('Saved native JForex checkpoint belongs to another source or is invalid.');
    if(saved)end=saved.end;
    else{
      let publicState;
      try{publicState=JSON.parse(await fs.readFile(path.join(path.dirname(request.output),'checkpoint.json'),'utf8'));}catch(error){if(error.code!=='ENOENT')throw error;}
      if(publicState){
        const value=publicState,header='timestamp,open,high,low,close,volume';
        if(value.version!==1||value.symbol!==request.symbol||value.timeframe!=='d1'||value.from!==request.from||value.to!==request.to||
          !Number.isSafeInteger(value.start)||!Number.isSafeInteger(value.end)||value.start<requestedFrom||value.end<=value.start||value.end>Math.min(requestedTo,asOf,Date.now())||
          !Number.isSafeInteger(value.nextIndex)||value.nextIndex<0||!Number.isSafeInteger(value.totalSources)||value.totalSources<1||value.nextIndex>value.totalSources||
          !Number.isSafeInteger(value.bytes)||value.bytes<0||!Number.isSafeInteger(value.rows)||value.rows<0||
          (value.rows===0?(value.bytes!==0||value.header!==null||value.lastTimestamp!==null):(!value.bytes||value.header!==header||value.nextIndex===0||!Number.isSafeInteger(value.lastTimestamp)||value.lastTimestamp<value.start||value.lastTimestamp>=value.end))||
          api.generateUrls({instrument:request.symbol,timeframe:'d1',priceType:'bid',startDate:new Date(value.start),endDate:new Date(value.end)}).length!==value.totalSources)throw Error('Saved public native checkpoint is invalid; original partial retained.');
        await archivePublicPartial(request,value,publicIdentity,{...options,nativePublic:true});end=Math.min(end,Math.floor(value.end/DAY)*DAY);
      }else if((await fs.lstat(request.output)).size!==0)throw Error('Existing uncheckpointed partial is preserved; start a new SDK download without replacing it.');
    }
  }
  if(start>=end)throw Error('No available history in the requested UTC date range.');
  const emit=options.emit??(event=>process.stdout.write(`${JSON.stringify(event)}\n`));
  const {writeCheckpoint}=require('./worker.cjs');let handle,bytes=saved?.bytes??0,rows=saved?.rows??0,last=saved?.last??null;
  let next=saved?.next??start,fallbackUntil=saved?.fallbackUntil??null,tickFallback=saved?.tickFallback??false;
  const append=async row=>{
    if(last!==null&&row[0]<=last)throw Error('JForex daily history overlaps committed candles.');
    const buffer=Buffer.from(`${rows?'':'timestamp,open,high,low,close\n'}${row.join(',')}\n`);let offset=0;
    while(offset<buffer.length){const written=await handle.write(buffer,offset,buffer.length-offset,bytes+offset);if(!written.bytesWritten)throw Error('JForex CSV write made no progress.');offset+=written.bytesWritten;}
    bytes+=buffer.length;rows++;last=row[0];
  };
  const commit=async cursor=>{
    await handle.sync();signal?.throwIfAborted();next=cursor;
    if(request.resume)await writeCheckpoint(checkpointFile,{version:1,identity,start,end,next,bytes,rows,last,tickFallback,...(fallbackUntil!==null?{fallbackUntil}: {})});
  };
  try{
    handle=await fs.open(request.output,'r+');
    if((await handle.stat()).size<bytes)throw Error('Saved JForex output is shorter than its committed checkpoint.');
    await handle.truncate(bytes);
    if(request.resume&&!saved)await commit(start);
    while(next<end){
      signal?.throwIfAborted();const from=next,to=fallbackUntil??Math.min(end,from+366*DAY);
      if(fallbackUntil!==null){
        // A successful empty D1 export can coexist with genuine native ticks.
        // Commit one whole UTC day at a time; incomplete hours stay reusable in
        // the private source cache and never become partial published candles.
        for(let day=from;day<to;day+=DAY){
          const until=day+DAY;
          const onChunk=event=>{
            emit({type:'progress',progress:Math.min(95,((day-start)+(event.hour-day+3600000))/(end-start)*95),message:`${event.reused?'Reused cached':'Fetched'} native ${request.symbol} BID ticks for ${new Date(day).toISOString().slice(0,10)}`});
            options.onChunk?.(event);
          };
          const onRetry=event=>{
            emit({type:'progress',progress:Math.min(95,(day-start)/(end-start)*95),message:`JForex ${request.symbol} native tick history timed out; retrying the same UTC hour once in 5 seconds. Completed quote hours are retained.`});
            options.onRetry?.(event);
          };
          const quotes=require('./synthetic-quotes.cjs').sourceQuotes(request.symbol,day,until,request,{...options,onChunk,onRetry,historyRange:{from:day,to}});
          for await(const bar of require('./synthetic-worker.cjs').nativeBars(quotes,day,until,signal)){
            await append([bar.timestamp,bar.open,bar.high,bar.low,bar.close]);tickFallback=true;
          }
          fallbackUntil=until<to?to:null;await commit(until);
        }
        continue;
      }
      emit({type:'progress',progress:Math.min(95,(from-start)/(end-start)*95),message:`Reading authenticated JForex BID history ${new Date(from).toISOString().slice(0,10)} through ${new Date(to).toISOString().slice(0,10)}`});
      const result=await requestSDKChunk(request,{symbol:request.symbol,timeframe:'d1',from,to},{...options,onRetry:event=>{
        emit({type:'progress',progress:Math.min(95,(from-start)/(end-start)*95),message:`JForex ${request.symbol} daily history timed out; retrying the same UTC range once in 5 seconds. Completed history is retained.`});
        options.onRetry?.(event);
      }});
      try{
        for await(const row of readSDKCSV(result,'d1',from,to,signal))await append(row);
      }finally{await fs.unlink(result.path).catch(error=>{if(error.code!=='ENOENT')throw error;});}
      if(result.rows===0&&request.symbol==='btcusd'){fallbackUntil=to;await commit(from);}
      else await commit(to);
    }
    if(!rows)throw Error('Dukascopy returned no rows for this symbol and date range.');
    await handle.sync();const data=await require('./refresh-worker.cjs').scan(request.output,'d1');
    if(data.rows!==rows||data.last!==last)throw Error('JForex checkpoint does not match its CSV.');
    const provenance={...require('./synthetic-worker.cjs').provenance(request.symbol,false),historySource:'jforex-sdk',volume:'unavailable',
      observationResolution:tickFallback?'JForex SDK BID daily candles; empty periods derived from native BID ticks':'JForex SDK BID daily candles'};
    emit({type:'done',rows,firstTimestamp:data.first,lastTimestamp:data.last,checksum:data.checksum,coverage:[{from:requestedFrom,to:end}],checkedThrough:end,provenance,
      message:`Saved ${rows.toLocaleString('en-US')} authenticated JForex BID daily candles; volume unavailable`});
    return rows;
  }finally{await handle?.close();}
}
module.exports={readSDKCSV,sdkNativeDownload,archivePublicPartial,finishPublicUpgrade,requestSDKChunk};

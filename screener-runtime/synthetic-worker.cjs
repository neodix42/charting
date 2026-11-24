'use strict';

const fs=require('node:fs/promises');
const path=require('node:path');
const {createHash}=require('node:crypto');
const {instruments,aggregateRatio,bucket}=require('./synthetic-ratio.cjs');
const {sourceQuotes}=require('./synthetic-quotes.cjs');
const DAY=86400000,HEADER='timestamp,open,high,low,close';

function calculated(request,api){
  if(['btcxau','xauspx','ethbtc'].includes(request.symbol))return true;
  if(request.symbol!=='xauxag')return false;
  // Existing native tick/intraday downloads retain their provider path. The
  // additional calculated prefix/fallback is available only for D1/W1.
  if(!['d1','w1'].includes(request.timeframe))return false;
  if(request.synthetic===true||['synchronized-bid-ratio','mixed-native-ratio'].includes(request.synthetic?.kind))return true;
  const metadata=api.instrumentMetaData?.xauxag;
  const first=Date.parse(metadata?.startHourForTicks??'');
  return !metadata||!Number.isFinite(first)||Date.parse(request.from)<first;
}
async function* nativeBars(quotes,from,to,signal){
  let bar;
  try{
    for await(const [time,value] of quotes){
      signal?.throwIfAborted();if(time<from||time>=to)continue;
      const start=bucket(time,'d1');
      if(bar?.timestamp!==start){if(bar)yield bar;bar={timestamp:start,open:value,high:value,low:value,close:value};}
      else{bar.high=Math.max(bar.high,value);bar.low=Math.min(bar.low,value);bar.close=value;}
    }
    if(bar)yield bar;
  }finally{await quotes.return?.();}
}
function segment(source,from,to,kind){
  if(from>=to)return;
  const previous=source.segments.at(-1);
  if(previous&&previous.kind===kind&&previous.to===from)previous.to=to;
  else{if(source.segments.length>=512)throw Error('Synthetic provenance exceeds its bounded segment count.');source.segments.push({from,to,kind});}
}
async function* computedBars(request,definition,start,to,source,options){
  const signal=options.signal;
  const api=options.api??require('dukascopy-node');
  const ratios=async function*(from,until){
    const starts=[definition.numerator,definition.denominator].map(symbol=>{
      const value=api.instrumentMetaData?.[symbol]?.startHourForTicks,time=Date.parse(value??'');
      if(typeof value!=='string'||!Number.isSafeInteger(time)||time<0)throw Error(`Required BID tick availability metadata is invalid for ${symbol}.`);
      return time;
    });
    const commonStart=Math.max(from,...starts);
    if(until<=commonStart)return;
    const shared={...options,historyRange:{from:commonStart,to:options.historyRange?.to??until}};
    yield* aggregateRatio(sourceQuotes(definition.numerator,commonStart,until,request,shared),sourceQuotes(definition.denominator,commonStart,until,request,shared),
      {period:'d1',maxAgeMs:0,from:commonStart,to:until,signal});
  };
  if(request.symbol!=='xauxag'||request.synthetic===true||request.synthetic?.kind==='synchronized-bid-ratio'){
    segment(source,start,to,'synchronized-bid-ratio');yield* ratios(start,to);return;
  }
  const first=Date.parse(api.instrumentMetaData?.xauxag?.startHourForTicks??'');
  for(let day=bucket(start,'d1');day<to;day+=DAY){
    const from=Math.max(start,day),until=Math.min(day+DAY,to),nativeFrom=Number.isFinite(first)?Math.max(from,first):until;
    let bar;
    if(from<nativeFrom){segment(source,from,Math.min(until,nativeFrom),'synchronized-bid-ratio');for await(const value of ratios(from,Math.min(until,nativeFrom)))bar=value;}
    if(nativeFrom<until){
      let native;
      for await(const value of nativeBars(sourceQuotes('xauxag',nativeFrom,until,request,options),nativeFrom,until,signal))native=value;
      if(native){
        segment(source,nativeFrom,until,'native');
        bar=bar?{...bar,high:Math.max(bar.high,native.high),low:Math.min(bar.low,native.low),close:native.close}:native;
      }else{
        // Only a successfully decoded empty native period permits fallback.
        let observed=false;
        for await(const value of ratios(nativeFrom,until)){observed=true;bar=bar?{...bar,high:Math.max(bar.high,value.high),low:Math.min(bar.low,value.low),close:value.close}:value;}
        segment(source,nativeFrom,until,observed?'synchronized-bid-ratio':'native');
      }
    }
    if(bar)yield bar;
  }
}
function provenance(symbol,computed){
  const definition=instruments[symbol];
  if(!definition)return undefined;
  return computed?{version:1,kind:'synchronized-bid-ratio',numerator:definition.numerator,denominator:definition.denominator,priceType:'bid',maxQuoteAgeMs:0,
    sessionPolicy:'exact-timestamp-no-carry-v1',volume:'unavailable',approximate:true,observationResolution:'simultaneous millisecond BID ticks'}:
    {version:1,kind:'native',numerator:symbol,priceType:'bid',maxQuoteAgeMs:0,sessionPolicy:'provider-native',volume:'provider',approximate:false,observationResolution:'provider native candles'};
}
function mergeProvenance(symbol,entries,hasVolume){
  const definition=instruments[symbol];if(!definition)return undefined;
  let segments=[];
  for(const entry of entries){
    if(!entry.value||!(entry.from<entry.to))continue;
    const supplied=entry.value.segments??[{from:entry.from,to:entry.to,kind:entry.value.kind}];
    for(const part of supplied){
      const from=Math.max(part.from,entry.from),to=Math.min(part.to,entry.to);
      if(from>=to)continue;
      if(!['native','synchronized-bid-ratio'].includes(part.kind))throw Error('Unsupported synthetic provenance segment.');
      segments=segments.flatMap(old=>old.to<=from||old.from>=to?[old]:[{...old,to:Math.min(old.to,from)},{...old,from:Math.max(old.from,to)}].filter(value=>value.from<value.to));
      segments.push({from,to,kind:part.kind,historySource:entry.value.historySource??'dukascopy-node',
        observationResolution:entry.value.kind==='native'?entry.value.observationResolution:undefined});
    }
  }
  if(!segments.length)return entries.map(entry=>entry.value).find(Boolean);
  segments.sort((a,b)=>a.from-b.from);const normalized={segments:[]};for(const value of segments)segment(normalized,value.from,value.to,value.kind);
  const historySources=new Set(segments.map(value=>value.historySource));
  const ratio=segments.some(value=>value.kind==='synchronized-bid-ratio'),native=segments.some(value=>value.kind==='native');
  const result={...provenance(symbol,ratio),volume:hasVolume?'provider':'unavailable'};
  if(historySources.has('mixed')||historySources.size>1)result.historySource='mixed';
  else if(historySources.has('jforex-sdk'))result.historySource='jforex-sdk';
  if(ratio&&native){result.kind='mixed-native-ratio';result.segments=normalized.segments;result.observationResolution='simultaneous millisecond BID ratios and provider native BID ticks';}
  if(!ratio&&!hasVolume){
    const resolutions=new Set(segments.map(value=>value.observationResolution));
    result.observationResolution=resolutions.size===1&&segments[0].observationResolution?segments[0].observationResolution:'provider native BID price observations';
  }
  return result;
}
async function syntheticDownload(request,options={}){
  if(!['d1','w1'].includes(request.timeframe))throw Error('Computed synthetic instruments support only d1 and w1.');
  if(request.timeframe==='w1')return require('./weekly-worker.cjs').weeklyDownload(request,options);
  const definition=instruments[request.symbol];
  if(!definition?.denominator)throw Error('Unsupported computed synthetic instrument.');
  const emit=options.emit??(event=>process.stdout.write(`${JSON.stringify(event)}\n`)),signal=options.signal;
  const mode=request.symbol!=='xauxag'||request.synthetic===true||request.synthetic?.kind==='synchronized-bid-ratio'?'ratio':'mixed';
  const sdk=request.providerSession?.sdk;
  if(sdk&&!/^[a-f0-9]{64}$/.test(sdk.sourceKey??''))throw Error('Invalid private JForex history source identity.');
  const identity=createHash('sha256').update(JSON.stringify({version:1,symbol:request.symbol,from:request.from,to:request.to,mode,...(sdk?{sourceKey:sdk.sourceKey}:{}),
    numerator:definition.numerator,denominator:definition.denominator,priceType:'bid',maxAgeMs:0,sessionPolicy:'exact-timestamp-no-carry-v1'})).digest('hex');
  const publicIdentity=sdk?createHash('sha256').update(JSON.stringify({version:1,symbol:request.symbol,from:request.from,to:request.to,mode,
    numerator:definition.numerator,denominator:definition.denominator,priceType:'bid',maxAgeMs:0,sessionPolicy:'exact-timestamp-no-carry-v1'})).digest('hex'):identity;
  const from=Date.parse(request.from),requestedTo=Date.parse(request.to),asOf=request.asOf??Date.now();
  if(!Number.isSafeInteger(asOf)||asOf<0||!Number.isSafeInteger(from)||!Number.isSafeInteger(requestedTo)||from<0||from>=requestedTo||!path.isAbsolute(request.output))throw Error('Invalid synthetic download range or output.');
  let to=Math.min(requestedTo,bucket(asOf,'d1'));
  if(from>=to)throw Error('No available history in the requested UTC date range.');
  const source={...provenance(request.symbol,true),...(sdk?{historySource:'jforex-sdk'}:{}),segments:[]},checkpointFile=path.join(path.dirname(request.output),'synthetic-checkpoint.json');
  let checkpoint;
  if(request.resume){
    if(sdk){const upgradedEnd=await require('./sdk-worker.cjs').finishPublicUpgrade(request,publicIdentity,options);if(upgradedEnd!==null)to=Math.min(to,upgradedEnd);}
    try{checkpoint=JSON.parse(await fs.readFile(checkpointFile,'utf8'));}catch(error){if(error.code!=='ENOENT')throw error;}
    const publicUpgrade=sdk&&checkpoint?.identity===publicIdentity&&(!checkpoint.provenance?.historySource||checkpoint.provenance.historySource==='dukascopy-node');
    if(checkpoint&&(checkpoint.version!==1||checkpoint.kind!=='synchronized-bid-ratio'||checkpoint.identity!==(publicUpgrade?publicIdentity:identity)||checkpoint.mode!==mode||checkpoint.symbol!==request.symbol||checkpoint.from!==request.from||checkpoint.to!==request.to||
      checkpoint.start!==from||!Number.isSafeInteger(checkpoint.end)||checkpoint.end<=from||checkpoint.end>to||!Number.isSafeInteger(checkpoint.nextDay)||checkpoint.nextDay<from||checkpoint.nextDay>checkpoint.end||
      !Number.isSafeInteger(checkpoint.rows)||checkpoint.rows<0||!Number.isSafeInteger(checkpoint.bytes)||checkpoint.bytes<0||
      (checkpoint.rows===0?(checkpoint.bytes!==0||checkpoint.lastTimestamp!==null):(checkpoint.bytes===0||!Number.isSafeInteger(checkpoint.lastTimestamp)||checkpoint.lastTimestamp<from||checkpoint.lastTimestamp>=to))))
      throw Error('Saved synthetic checkpoint is invalid or belongs to another request.');
  }
  if(checkpoint)to=checkpoint.end;
  if(checkpoint&&!checkpoint.provenance?.segments)throw Error('Invalid saved synthetic provenance.');
  if(checkpoint?.provenance?.segments){
    if(checkpoint.provenance.version!==1||checkpoint.provenance.numerator!==definition.numerator||checkpoint.provenance.denominator!==definition.denominator||checkpoint.provenance.priceType!=='bid'||checkpoint.provenance.maxQuoteAgeMs!==0||
      !Array.isArray(checkpoint.provenance.segments)||checkpoint.provenance.segments.length>512)throw Error('Invalid saved synthetic provenance.');
    let previous=from;
    for(const value of checkpoint.provenance.segments){
      if(!Number.isSafeInteger(value.from)||!Number.isSafeInteger(value.to)||value.from<previous||value.to<=value.from||value.from<from||value.to>to||!['native','synchronized-bid-ratio'].includes(value.kind))throw Error('Invalid saved synthetic provenance segments.');
      previous=value.to;
    }
    source.segments=checkpoint.provenance.segments.map(value=>({...value,to:Math.min(value.to,checkpoint.nextDay)})).filter(value=>value.from<value.to);
  }
  if(sdk&&checkpoint?.identity===publicIdentity){
    await require('./sdk-worker.cjs').archivePublicPartial(request,checkpoint,publicIdentity,options);
    emit({type:'progress',progress:0,message:'Preserved the unpublished public partial in a private archive; restarting with the configured JForex source'});
    checkpoint=null;source.segments=[];
  }
  let rows=checkpoint?.rows??0,bytes=checkpoint?.bytes??0,last=checkpoint?.lastTimestamp??null,handle;
  const {writeCheckpoint}=require('./worker.cjs');
  const commit=async nextDay=>{
    if(!request.resume)return;
    await handle.sync();
    signal?.throwIfAborted();
    await writeCheckpoint(checkpointFile,{version:1,kind:'synchronized-bid-ratio',identity,mode,symbol:request.symbol,from:request.from,to:request.to,start:from,end:to,nextDay,rows,bytes,lastTimestamp:last,provenance:source});
  };
  try{
    handle=await fs.open(request.output,'r+');
    if((await handle.stat()).size<bytes)throw Error('Saved synthetic CSV is shorter than its committed checkpoint.');
    await handle.truncate(bytes);
    let chunks=0;
    const total=Math.ceil((to-(checkpoint?.nextDay??from))/3600000)*2;
    const onChunk=event=>{chunks++;emit({type:'progress',progress:Math.min(95,chunks/Math.max(total,1)*95),message:`${event.reused?'Reused cached':'Fetched'} ${event.symbol} BID quotes; ${chunks}/${total} hourly sources`});options.onChunk?.(event);};
    const onRetry=event=>{emit({type:'progress',progress:Math.min(95,chunks/Math.max(total,1)*95),message:`JForex ${event.symbol} history loading timed out; retrying the same UTC hour once in 5 seconds. Completed quote hours are retained.`});options.onRetry?.(event);};
    const start=checkpoint?.nextDay??from,sourceOptions={...options,onChunk,onRetry,importedHistories:new Set(),historyRange:{from:start,to}};
    if(start<to){
      for await(const bar of computedBars(request,definition,start,to,source,sourceOptions)){
        signal?.throwIfAborted();
        if(last!==null&&bar.timestamp<=last)throw Error('Synthetic candles are not chronological.');
        // Tick ratios are observed only at exact simultaneous source milliseconds.
        // No component candle extrema or invented ratio volume enter this file.
        const text=`${rows?'':HEADER+'\n'}${bar.timestamp},${bar.open},${bar.high},${bar.low},${bar.close}\n`;
        const buffer=Buffer.from(text);let written=0;
        while(written<buffer.length){const result=await handle.write(buffer,written,buffer.length-written,bytes+written);if(!result.bytesWritten)throw Error('Synthetic CSV write made no progress.');written+=result.bytesWritten;}
        bytes+=buffer.length;rows++;last=bar.timestamp;await commit(Math.min(to,bar.timestamp+DAY));
      }
    }
    await commit(to);
    if(!rows)throw Error('Dukascopy returned no rows at simultaneous source BID timestamps for this synthetic instrument and date range.');
    await handle.sync();
    const result=await require('./refresh-worker.cjs').scan(request.output,'d1');
    if(result.rows!==rows||result.last!==last)throw Error('Synthetic checkpoint does not match its CSV rows.');
    const published=mergeProvenance(request.symbol,[{value:source,from,to}],false);
    emit({type:'done',rows,firstTimestamp:result.first,lastTimestamp:result.last,checksum:result.checksum,coverage:[{from,to}],checkedThrough:to,provenance:published,
      message:`Calculated ${rows.toLocaleString('en-US')} daily ratio rows from simultaneous millisecond BID ticks; approximate extrema; volume unavailable`});
    return rows;
  }finally{await handle?.close();}
}
module.exports={calculated,provenance,mergeProvenance,syntheticDownload,nativeBars};

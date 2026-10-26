'use strict';

// Existing histories and completed range checkpoints are read-only inputs.
// Go owns the backup, source-change checks, metadata and atomic publication.
const fs=require('node:fs/promises');
const path=require('node:path');
const {download,writeCheckpoint}=require('./worker.cjs');
const {scan,records,groups,recipesFor,closedBoundary}=require('./refresh-worker.cjs');
const {calculator,key,line,writeAll}=require('./enrich-worker.cjs');

function union(ranges){
  const result=[];
  for(const range of ranges.filter(item=>item.from<item.to).sort((a,b)=>a.from-b.from||a.to-b.to)){
    const last=result.at(-1);
    if(last&&range.from<=last.to)last.to=Math.max(last.to,range.to);
    else result.push({...range});
  }
  return result;
}
function missing(from,to,covered){
  const result=[];
  for(const range of union(covered)){
    if(range.to<=from)continue;
    if(range.from>=to)break;
    if(range.from>from)result.push({from,to:Math.min(to,range.from)});
    from=Math.max(from,range.to);
    if(from>=to)break;
  }
  if(from<to)result.push({from,to});
  return result;
}
function nextBar(timestamp,timeframe){
  if(timeframe==='tick')return timestamp+1;
  if(timeframe==='mn1'){const date=new Date(timestamp);return Date.UTC(date.getUTCFullYear(),date.getUTCMonth()+1,1);}
  const duration={s1:1000,m1:60000,m5:300000,m15:900000,m30:1800000,h1:3600000,h4:14400000,d1:86400000}[timeframe];
  if(!duration)throw Error('Unsupported download timeframe.');
  return timestamp+duration;
}
async function jsonFile(filename){
  const info=await fs.lstat(filename);
  if(!info.isFile()||info.size>8*1024*1024)throw Error('Invalid incremental download state.');
  return JSON.parse(await fs.readFile(filename,'utf8'));
}

// At most eight streams are open per merge. Repeated merge passes retain source
// priority, avoid loading tick histories into RAM, and support many legacy CSVs.
async function mergeGroup(inputs,output,headers,timeframe){
  const normalized=headers.map(key),base=timeframe==='tick'?['timestamp','askprice','bidprice','askvolume','bidvolume']:['timestamp','open','high','low','close','volume'];
  const timestampIndex=normalized.indexOf('timestamp'),baseIndexes=base.map(name=>normalized.indexOf(name));
  const iterators=[],heads=[],streams=[];
  let handle,buffer='',bytes=0;
  try{
    for(const filename of inputs){
      const source=records(filename);streams.push(source);
      const header=(await source.next()).value.map(key),positions=header.map(name=>normalized.indexOf(name));
      const mapped=(async function*(){for await(const row of source){const result=Array(headers.length).fill('');positions.forEach((position,index)=>{result[position]=row[index];});yield result;}})();
      const iterator=groups(mapped,timestampIndex);iterators.push(iterator);heads.push(await iterator.next());
    }
    handle=await fs.open(output,'wx',0o600);await writeAll(handle,line(headers));
    while(heads.some(head=>!head.done)){
      const timestamp=Math.min(...heads.filter(head=>!head.done).map(head=>head.value.timestamp));
      let rows=[];const counts=new Map();
      for(let index=0;index<heads.length;index++){
        if(heads[index].done||heads[index].value.timestamp!==timestamp)continue;
        if(timeframe!=='tick'){
          if(!rows.length)rows=heads[index].value.rows;
        }else{
          const seen=new Map();
          for(const row of heads[index].value.rows){
            const id=JSON.stringify(baseIndexes.map(position=>Number(row[position]))),count=(seen.get(id)??0)+1;seen.set(id,count);
            if(count>(counts.get(id)??0)){rows.push(row);counts.set(id,count);}
          }
        }
        heads[index]=await iterators[index].next();
      }
      if(rows.length>100000)throw Error('More than 100,000 ticks share one timestamp.');
      for(const row of rows){const text=line(row);buffer+=text;bytes+=Buffer.byteLength(text);if(bytes>=128*1024){await writeAll(handle,buffer);buffer='';bytes=0;}}
    }
    if(buffer)await writeAll(handle,buffer);
    await handle.sync();
  }finally{
    await handle?.close();
    for(const iterator of iterators)await iterator.return();
    for(const stream of streams)await stream.return();
  }
}
async function mergeFiles(inputs,output,headers,timeframe,directory){
  let level=inputs,serial=0;
  while(level.length>8){
    const next=[];
    for(let i=0;i<level.length;i+=8){const file=path.join(directory,`merge-${++serial}.csv`);await mergeGroup(level.slice(i,i+8),file,headers,timeframe);next.push(file);}
    level=next;
  }
  await mergeGroup(level,output,headers,timeframe);
}

async function extendDownload(request,options={}){
  const emit=options.emit??(event=>process.stdout.write(`${JSON.stringify(event)}\n`));
  const directory=path.dirname(request.output);
  if(!path.isAbsolute(request.planFile)||path.dirname(request.planFile)!==directory)throw Error('Download plan must be in the private job folder.');
  const plan=await jsonFile(request.planFile);
  if(plan.version!==1||!Array.isArray(plan.inputs)||!plan.inputs.length||!Number.isSafeInteger(plan.asOf))throw Error('Invalid incremental download plan.');
  const from=Date.parse(request.from),to=Math.min(Date.parse(request.to),plan.asOf),closed=closedBoundary(plan.asOf,request.timeframe);
  if(!Number.isFinite(from)||!Number.isFinite(to)||from>=to)throw Error('No available history in the requested UTC date range.');
  const headers=[],names=new Set(),scanned=[],covered=[],savedRecipes=[];
  for(const source of plan.inputs){
    const stat=await fs.lstat(source.input,{bigint:true});
    if(!stat.isFile()||Number(stat.size)!==source.bytes||Number(stat.mtimeNs/1000000n)!==source.modified)throw Error('Existing CSV changed since the download was queued; original kept.');
    const data=await scan(source.input,request.timeframe);scanned.push(data);
    if(data.last>plan.asOf)throw Error('CSV contains future timestamps; original kept.');
    for(const name of data.headers)if(!names.has(key(name))){names.add(key(name));headers.push(name);}
    const metadata=source.metadata??{};
    const valid=metadata.checksum===data.checksum;
    if(valid&&metadata.coverage?.length)covered.push(...metadata.coverage);
    else if(valid&&metadata.verified&&metadata.checkedThrough){
      covered.push({from:Math.min(Date.parse(source.from),data.first),to:closedBoundary(metadata.checkedThrough,request.timeframe)});
    }else{
      // Legacy CSVs have no query ledger. Reuse their actual historical span;
      // keep a currently open candle uncovered so its partial values refresh.
      const end=Math.min(nextBar(data.last,request.timeframe),closed);
      if(data.first<end)covered.push({from:data.first,to:end});
    }
    savedRecipes.push(...(metadata.recipes??[]));
  }
  const snapshotFile=path.join(directory,'ranges.json');
  let snapshot;
  try{snapshot=await jsonFile(snapshotFile);}catch(error){if(error.code!=='ENOENT')throw error;}
  const checksums=scanned.map(data=>data.checksum);
  if(snapshot){
    if(snapshot.version!==1||JSON.stringify(snapshot.checksums)!==JSON.stringify(checksums)||snapshot.from!==from||snapshot.to!==to||JSON.stringify(snapshot.ranges)!==JSON.stringify(missing(from,to,covered)))throw Error('Existing histories changed since the saved range checkpoint; originals kept.');
  }else{
    snapshot={version:1,checksums,from,to,ranges:missing(from,to,covered)};
    await writeCheckpoint(snapshotFile,snapshot);
  }
  if(!snapshot.ranges.length&&plan.inputs.length===1){
    emit({type:'done',rows:scanned[0].rows,noChange:true,checksum:scanned[0].checksum,firstTimestamp:scanned[0].first,lastTimestamp:scanned[0].last,message:'Requested data is already available; no download needed'});
    return scanned[0].rows;
  }
  const recipes=await recipesFor(headers,savedRecipes),downloaded=[];
  for(let index=0;index<snapshot.ranges.length;index++){
    const range=snapshot.ranges[index],folder=path.join(directory,`range-${index}`),output=path.join(folder,'data.part');
    await fs.mkdir(folder,{recursive:true,mode:0o700});
    if(!(await fs.lstat(folder)).isDirectory())throw Error('Saved range folder must be a real directory.');
    try{if(!(await fs.lstat(output)).isFile())throw Error('Saved range partial must be a regular file.');}catch(error){if(error.code!=='ENOENT')throw error;}
    const handle=await fs.open(output,'a',0o600);await handle.close();
    emit({type:'progress',progress:2+index/Math.max(1,snapshot.ranges.length)*68,message:`Downloading missing range ${index+1}/${snapshot.ranges.length}: ${new Date(range.from).toISOString()} → ${new Date(range.to).toISOString()}`});
    let rows=0;
    try{
      await (options.download??download)({symbol:request.symbol,timeframe:request.timeframe,from:new Date(range.from).toISOString(),to:new Date(range.to).toISOString(),output,resume:true,sourceBatchSize:request.sourceBatchSize??1,asOf:plan.asOf,providerSession:request.providerSession},
        {...options,emit:event=>{if(event.type==='done')rows=event.rows;if(event.type==='progress')emit({...event,progress:2+(index+event.progress/100)/snapshot.ranges.length*68});}});
    }catch(error){if(!/Dukascopy returned no rows|No available history in the requested UTC date range/.test(error.message))throw error;}
    if(rows>0)downloaded.push(output);
    const end=closedBoundary(range.to,request.timeframe);
    if(range.from<end)covered.push({from:range.from,to:end});
  }
  const coverage=union(covered);
  // Only owned, reproducible merge output is discarded on restart. Per-range
  // source checkpoints remain, so committed data is not fetched again.
  for(const name of await fs.readdir(directory))if(/^merge-\d+\.csv$/.test(name))await fs.unlink(path.join(directory,name));
  await fs.unlink(request.output).catch(error=>{if(error.code!=='ENOENT')throw error;});
  emit({type:'progress',progress:72,message:'Merging existing histories and new ranges; recalculating applied indicators'});
  const merged=path.join(directory,'merge-0.csv');
  // Fetched data has priority only where an open boundary candle overlaps.
  await mergeFiles([...downloaded,...plan.inputs.map(source=>source.input)],merged,headers,request.timeframe,directory);
  const calculations=await Promise.all(recipes.map(recipe=>calculator(recipe,headers))),normalized=headers.map(key),positions=calculations.map(calculate=>calculate.columns.map(name=>normalized.indexOf(name)));
  const source=records(merged),estimated=scanned.reduce((sum,data)=>sum+data.rows,0);let output,rows=0,buffer='',bytes=0;
  try{
    await source.next();output=await fs.open(request.output,'wx',0o600);await writeAll(output,line(headers));
    for await(const row of source){
      calculations.forEach((calculate,index)=>{const values=calculate.next(row,rows+2);positions[index].forEach((position,i)=>{if(position>=0)row[position]=values[i];});});
      const text=line(row);buffer+=text;bytes+=Buffer.byteLength(text);rows++;
      if(bytes>=128*1024){await writeAll(output,buffer);buffer='';bytes=0;emit({type:'progress',progress:Math.min(98,75+rows/Math.max(rows,estimated)*23),message:`Recalculating ${recipes.length} applied indicators over ${rows} rows`});}
    }
    if(buffer)await writeAll(output,buffer);await output.sync();
  }finally{await output?.close();await source.return();}
  const result=await scan(request.output,request.timeframe);
  const noChange=result.checksum===scanned[0].checksum;
  emit({type:'done',rows,noChange,firstTimestamp:result.first,lastTimestamp:result.last,checksum:result.checksum,coverage,recipes,checkedThrough:coverage.length===1?coverage[0].to:0,
    message:noChange&&!snapshot.ranges.length?'Requested data is already available; no download needed':`Updated existing history: ${rows.toLocaleString('en-US')} rows; ${snapshot.ranges.length} missing ranges downloaded; ${recipes.length} indicator recipes recalculated`});
  return rows;
}
module.exports={extendDownload,union,missing,nextBar,mergeGroup,mergeFiles};

'use strict';

// Weekly candles are derived locally. This worker only reads daily histories;
// the Go owner downloads missing daily ranges and owns every publication.
const fs=require('node:fs/promises');
const path=require('node:path');
const {scan,records,recipesFor}=require('./refresh-worker.cjs');
const {calculator,key,line,writeAll}=require('./enrich-worker.cjs');
const day=86400000,week=7*day,monday=4*day;
const fields=['timestamp','open','high','low','close','volume'];
const weekStart=timestamp=>Math.floor((timestamp-monday)/week)*week+monday;

async function readPlan(filename,directory){
  if(!path.isAbsolute(filename)||path.dirname(filename)!==directory)throw Error('Weekly source plan must be in the private job folder.');
  const info=await fs.lstat(filename);
  if(!info.isFile()||info.size>8*1024*1024)throw Error('Invalid weekly source plan.');
  return JSON.parse(await fs.readFile(filename,'utf8'));
}
function validatePlan(plan,asOf){
  if(plan.version!==1||plan.asOf!==asOf||!Array.isArray(plan.inputs)||!plan.inputs.length||plan.inputs.length>10000)throw Error('Invalid weekly source plan.');
}
async function unchanged(source){
  const info=await fs.lstat(source.input,{bigint:true});
  if(!info.isFile()||Number(info.size)!==source.bytes||Number(info.mtimeNs/1000000n)!==source.modified)throw Error('Existing CSV changed during weekly aggregation; original kept.');
}

// Sum decimal volumes without binary floating-point drift. Prices retain the
// original decimal text for the selected open, high, low and close.
function decimal(text){
  const match=String(text).trim().match(/^\+?(\d+)(?:\.(\d*))?(?:e([+-]?\d+))?$/i);
  if(!match)throw Error('Daily volume must be a nonnegative decimal.');
  const exponent=Number(match[3]??0),scale=(match[2]?.length??0)-exponent;
  if(Math.abs(scale)>1000||match[1].length+(match[2]?.length??0)>2000)throw Error('Daily volume exceeds supported decimal precision.');
  return {value:BigInt(match[1]+(match[2]??'')),scale};
}
function addDecimal(a,b){
  const scale=Math.max(a.scale,b.scale);
  return {value:a.value*10n**BigInt(scale-a.scale)+b.value*10n**BigInt(scale-b.scale),scale};
}
function decimalText({value,scale}){
  let text=value.toString();
  if(scale<0)return text+'0'.repeat(-scale);
  if(!scale)return text;
  text=text.padStart(scale+1,'0');
  return `${text.slice(0,-scale)}.${text.slice(-scale)}`.replace(/0+$/,'').replace(/\.$/,'');
}

async function aggregateDaily(input,output,asOf){
  const source=records(input);let handle,current,rows=0;
  const append=async()=>{
    if(!current)return;
    await writeAll(handle,line([current.timestamp,current.open,current.high,current.low,current.close,decimalText(current.volume)]));rows++;
  };
  try{
    const headers=(await source.next()).value.map(key),indexes=fields.map(field=>headers.indexOf(field));
    handle=await fs.open(output,'wx',0o600);await writeAll(handle,line(fields));
    for await(const row of source){
      const [timestamp,open,high,low,close,volume]=indexes.map(index=>row[index]);
      const time=Number(timestamp);
      if(!Number.isSafeInteger(time)||time%day!==0)throw Error('Daily candle timestamps must start at 00:00 UTC.');
      if(time>asOf)throw Error('Daily CSV contains future timestamps; original kept.');
      const bucket=weekStart(time);
      if(current?.timestamp!==bucket){await append();current={timestamp:bucket,open,high,low,close,volume:decimal(volume)};}
      else{
        if(Number(high)>Number(current.high))current.high=high;
        if(Number(low)<Number(current.low))current.low=low;
        current.close=close;current.volume=addDecimal(current.volume,decimal(volume));
      }
    }
    await append();await handle.sync();
  }finally{await handle?.close();await source.return();}
  return rows;
}

async function weeklyDownload(request,options={}){
  const emit=options.emit??(event=>process.stdout.write(`${JSON.stringify(event)}\n`));
  const directory=path.dirname(request.output),asOf=request.asOf;
  if(!path.isAbsolute(request.output)||!Number.isSafeInteger(asOf)||asOf<0)throw Error('Weekly aggregation requires an absolute output and valid cutoff.');
  if(!request.dailyPlanFile)throw Error('Weekly aggregation requires retained d1 history. Download or refresh d1 first.');
  const dailyPlan=await readPlan(request.dailyPlanFile,directory);validatePlan(dailyPlan,asOf);
  const weeklyPlan=options.weeklyPlan??(request.planFile?await readPlan(request.planFile,directory):null);
  if(weeklyPlan)validatePlan(weeklyPlan,asOf);
  const headers=[...fields],names=new Set(fields),savedRecipes=[];
  for(const source of dailyPlan.inputs){
    if(!path.isAbsolute(source.input)||source.input===request.output)throw Error('Invalid daily source path.');
    await unchanged(source);const data=await scan(source.input,'d1');
    const normalized=data.headers.map(key);
    if(normalized.some(name=>!name)||new Set(normalized).size!==normalized.length)throw Error('Daily CSV contains empty or duplicate field names.');
    if(data.last>asOf)throw Error('Daily CSV contains future timestamps; original kept.');
  }
  const previous=[];
  for(const source of weeklyPlan?.inputs??[]){
    if(!path.isAbsolute(source.input)||source.input===request.output)throw Error('Invalid weekly source path.');
    await unchanged(source);const data=await scan(source.input,'w1');previous.push(data);
    if(data.last>asOf)throw Error('Weekly CSV contains future timestamps; original kept.');
    for(const name of data.headers)if(!names.has(key(name))){names.add(key(name));headers.push(name);}
    savedRecipes.push(...(source.metadata?.recipes??[]));
  }
  const recipes=await recipesFor(headers,savedRecipes,request.requiredFields??[],'w1');
  const calculations=await Promise.all(recipes.map(recipe=>calculator(recipe,headers,'w1')));
  for(const calculate of calculations)for(const name of calculate.columns)if(!names.has(key(name))){names.add(key(name));headers.push(name);}
  const normalized=headers.map(key),positions=calculations.map(calculate=>calculate.columns.map(name=>normalized.indexOf(name)));
  const scratch=await fs.mkdtemp(path.join(directory,'.weekly-'));
  try{
    const {mergeFiles}=require('./incremental-worker.cjs');
    emit({type:'progress',progress:5,message:'Reading retained daily history; no weekly provider request is needed'});
    const daily=path.join(scratch,'daily.csv'),aggregated=path.join(scratch,'weekly.csv'),merged=path.join(scratch,'merged.csv');
    const dailyWork=path.join(scratch,'daily'),weeklyWork=path.join(scratch,'weekly');
    await fs.mkdir(dailyWork);await fs.mkdir(weeklyWork);
    await mergeFiles(dailyPlan.inputs.map(source=>source.input),daily,fields,'d1',dailyWork);
    await aggregateDaily(daily,aggregated,asOf);
    emit({type:'progress',progress:60,message:'Merging weekly bars and recalculating weekly indicators'});
    await mergeFiles([aggregated,...(weeklyPlan?.inputs??[]).map(source=>source.input)],merged,headers,'w1',weeklyWork);
    const source=records(merged);let output,rows=0;
    try{
      await source.next();output=await fs.open(request.output,'w',0o600);await writeAll(output,line(headers));
      for await(const row of source){
        const timestamp=Number(row[0]);
        if(weekStart(timestamp)!==timestamp)throw Error('Weekly candle timestamps must start on Monday at 00:00 UTC.');
        calculations.forEach((calculate,index)=>{const values=calculate.next(row,rows+2);positions[index].forEach((position,i)=>{row[position]=values[i];});});
        await writeAll(output,line(row));rows++;
      }
      await output.sync();
    }finally{await output?.close();await source.return();}
    for(const source of [...dailyPlan.inputs,...(weeklyPlan?.inputs??[])])await unchanged(source);
    const result=await scan(request.output,'w1');
    // Weekly coverage is local derivation, never a claim of provider queries.
    emit({type:'done',rows,firstTimestamp:result.first,lastTimestamp:result.last,checksum:result.checksum,
      recipes,columns:headers.filter(name=>!fields.includes(key(name))),coverage:[],checkedThrough:weekStart(asOf),
      noChange:previous.length===1&&previous[0].checksum===result.checksum,
      message:`Derived ${rows.toLocaleString('en-US')} weekly rows from retained d1 history; daily bars kept; ${recipes.length} indicator recipes recalculated`});
    return rows;
  }finally{await fs.rm(scratch,{recursive:true,force:true});}
}

async function weeklyRefresh(request,savedRecipes,scanned,options={}){
  const stat=await fs.stat(request.input),asOf=options.now??request.asOf;
  const plan={version:1,asOf,inputs:[{input:request.input,bytes:stat.size,modified:Math.trunc(stat.mtimeMs),metadata:{recipes:savedRecipes}}]};
  const emit=options.emit??(event=>process.stdout.write(`${JSON.stringify(event)}\n`));
  let result;
  await weeklyDownload({...request,asOf},{...options,weeklyPlan:plan,emit:event=>{if(event.type==='done')result=event;else emit(event);}});
  const oldRows=records(request.input),newRows=records(request.output);
  let addedRows=0,updatedRows=0;
  try{
    const oldHeader=(await oldRows.next()).value.map(key),newHeader=(await newRows.next()).value.map(key);
    const oldIndexes=fields.map(field=>oldHeader.indexOf(field)),newIndexes=fields.map(field=>newHeader.indexOf(field));
    let old=await oldRows.next();
    for await(const row of newRows){
      const timestamp=Number(row[newIndexes[0]]);
      while(!old.done&&Number(old.value[oldIndexes[0]])<timestamp)old=await oldRows.next();
      if(old.done||Number(old.value[oldIndexes[0]])!==timestamp)addedRows++;
      else if(fields.some((_,i)=>Number(old.value[oldIndexes[i]])!==Number(row[newIndexes[i]])))updatedRows++;
    }
  }finally{await oldRows.return();await newRows.return();}
  result={...result,addedRows,updatedRows,message:`Refreshed ${result.rows} weekly rows from retained d1: ${addedRows} added, ${updatedRows} updated; daily bars kept`};
  emit(result);return result;
}

module.exports={weeklyDownload,weeklyRefresh,weekStart};

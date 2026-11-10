'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict');
const fs=require('node:fs/promises'),path=require('node:path'),os=require('node:os');
const {parse}=require('csv-parse/sync');
const {enrich,enrichmentMembers,catalog,calculator}=require('./enrich-worker.cjs');
const {recipesFor}=require('./refresh-worker.cjs');

async function fixture(t,text){
 const dir=await fs.mkdtemp(path.join(os.tmpdir(),'cryptodesk-group-'));t.after(()=>fs.rm(dir,{recursive:true,force:true}));
 const input=path.join(dir,'original.csv'),output=path.join(dir,'enriched.csv');await fs.writeFile(input,text);return {input,output};
}
const header=['timestamp','open','high','low','close','volume'];

test('range group calculates all twelve columns in one aligned stream and replaces them without duplicates',async t=>{
 const original=`${header.join(',')},notes\n1704067200000,10,12,8,10,1,first\n1704150000000,10,15,6,10,1,last\n1704153600000,8,13,3,8,1,next\n1704672000000,14,18,10,14,1,week\n`;
 const paths=await fixture(t,original),events=[];
 await enrich({...paths,group:'Day / Week ranges',source:'askprice',parameters:{},timeframe:'h1'},{emit:event=>events.push(event)});
 const actual=parse(await fs.readFile(paths.output),{columns:true});const names=catalog.indicators.filter(item=>item.intradayOnly).map(item=>item.id);
 assert.deepEqual(events.at(-1).columns,names);assert.equal(actual[1].today_low,'6');assert.equal(actual[2].previous_day_low,'6');assert.equal(actual[3].previous_week_low,'3');
 assert.deepEqual(actual.map(row=>row.notes),['first','last','next','week']);assert.equal(await fs.readFile(paths.input,'utf8'),original);
 const output=path.join(path.dirname(paths.output),'repeat.csv');await enrich({input:paths.output,output,group:'Day / Week ranges',timeframe:'h1'},{emit:()=>{}});
 assert.equal(await fs.readFile(output,'utf8'),await fs.readFile(paths.output,'utf8'));
 const recipes=await recipesFor(Object.keys(actual[0]),[],[],'h1').catch(error=>{assert.match(error.message,/notes/);return recipesFor(Object.keys(actual[0]).filter(field=>field!=='notes'),[],[],'h1');});
 assert.deepEqual(recipes.map(recipe=>recipe.indicator),names);
});

test('every group uses each member default and its per-indicator source rules',async t=>{
 const rows=Array.from({length:500},(_,i)=>[1704067200000+i*3600000,100+i,102+i,98+i,101+i,2].map(String));
 const original=`${header.join(',')}\n${rows.map(row=>row.join(',')).join('\n')}\n`;
 for(const group of [...new Set(catalog.indicators.map(item=>item.category))]){
  const paths=await fixture(t,original),request={...paths,group,timeframe:'h1',source:'close'};
  await enrich(request,{emit:()=>{}});const actual=parse(await fs.readFile(paths.output),{columns:true});
  const members=enrichmentMembers(request);assert.ok(members.length>0);
  for(const member of members){
   const calculate=await calculator(member,header);
   rows.forEach((row,i)=>{const expected=calculate.next(row,i+2);calculate.columns.forEach((field,column)=>assert.equal(actual[i][field],expected[column],`${group}/${field}/${i}`));});
  }
 }
});

test('unsupported or ambiguous groups and missing inputs never change the original file',async t=>{
 const paths=await fixture(t,'timestamp,low\n1704067200000,8\n'),before=await fs.readFile(paths.input,'utf8');
 for(const request of [{group:'unknown'},{group:'Time',indicator:'hour'},{group:'Moving averages',parameters:{period:5}},{group:'Day / Week ranges',timeframe:'d1'},{group:'Day / Week ranges',timeframe:'h1'}]){
  const output=path.join(path.dirname(paths.output),Math.random()+'.csv');await assert.rejects(enrich({...paths,output,...request},{emit:()=>{}}));assert.equal(await fs.readFile(paths.input,'utf8'),before);
 }
});

test('a frozen Go group snapshot determines calculation parameters instead of a later worker default',async t=>{
 const paths=await fixture(t,`${header.join(',')}\n${Array.from({length:5},(_,i)=>[1704067200000+i*3600000,10+i,12+i,8+i,11+i,1].join(',')).join('\n')}\n`);
 const members=enrichmentMembers({group:'Moving averages',source:'auto'}).map(member=>({...member,parameters:Object.fromEntries(catalog.indicators.find(item=>item.id===member.indicator).parameters.map(parameter=>[parameter.key,parameter.default]))}));
 members.find(member=>member.indicator==='ema').parameters.period=3;
 await enrich({...paths,group:'Moving averages',members,timeframe:'h1'},{emit:()=>{}});
 const rows=parse(await fs.readFile(paths.output),{columns:true});assert.notEqual(rows[2].ema_3,'');assert.ok(!Object.hasOwn(rows[0],'ema_20'));
});

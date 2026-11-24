'use strict';
const test=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs/promises');
const path=require('node:path');
const os=require('node:os');
const api=require('dukascopy-node');
const {download}=require('./worker.cjs');
const {refresh,scan}=require('./refresh-worker.cjs');
const {calculator}=require('./enrich-worker.cjs');
const DAY=86400000,HOUR=3600000,start=Date.UTC(2024,0,1);
const empty=timestamp=>Buffer.from(JSON.stringify({timestamp,times:[],asks:[],bids:[],askVolumes:[],bidVolumes:[]}));
function payload(hour,bids,times=[0,100,200]){
  if(!bids.length)return empty(hour);
  const delta=bids.map((value,index)=>index?value-bids[index-1]:0);
  return Buffer.from(JSON.stringify({timestamp:hour,multiplier:1,ask:bids[0]+1,bid:bids[0],times:times.map((value,index)=>index?value-times[index-1]:value),asks:delta,bids:delta,askVolumes:bids.map(()=>1),bidVolumes:bids.map(()=>1)}));
}
function hourOf(url){const parts=new URL(url).pathname.split('/').slice(-4).map(Number);return Date.UTC(parts[0],parts[1]-1,parts[2],parts[3]);}
function transport(fn,metadata){return {...api,...(metadata?{instrumentMetaData:{...api.instrumentMetaData,...metadata}}:{}),BufferFetcher:class extends api.BufferFetcher{constructor(config){super({...config,fetcherFn:fn});}}};}
function quotes(url){
  assert.match(url,/\/v1\/ticks\//,'calculated ratios must never fetch source OHLC');
  const hour=hourOf(url);if(hour%DAY)return empty(hour);
  return payload(hour,url.includes('/BTC-USD/')?[10,20,30]:[2,4,5]);
}
async function fixture(t,symbol='btcxau',from=start,to=start+DAY){
  const directory=await fs.mkdtemp(path.join(os.tmpdir(),'cryptodesk-synthetic-worker-'));t.after(()=>fs.rm(directory,{recursive:true,force:true}));
  const output=path.join(directory,'data.part');await fs.writeFile(output,'');
  return {symbol,timeframe:'d1',from:new Date(from).toISOString(),to:new Date(to).toISOString(),asOf:to,output,resume:true,syntheticCacheDirectory:path.join(directory,'quotes')};
}
test('synthetic daily OHLC uses exact synchronized BID observations, omits volume and reports approximation',async t=>{
  const request=await fixture(t);let result;await download(request,{api:transport(quotes),emit:event=>{if(event.type==='done')result=event;}});
  assert.equal(await fs.readFile(request.output,'utf8'),`timestamp,open,high,low,close\n${start},5,6,5,6\n`);
  assert.equal((await scan(request.output,'d1')).rows,1);
  assert.equal(result.provenance.kind,'synchronized-bid-ratio');assert.equal(result.provenance.approximate,true);
  assert.equal(result.provenance.volume,'unavailable');assert.equal(result.provenance.maxQuoteAgeMs,0);
  assert.match(result.provenance.observationResolution,/simultaneous millisecond BID/);
  const sma=await calculator({indicator:'sma',parameters:{period:1}},['timestamp','open','high','low','close'],'d1');
  assert.deepEqual(sma.next([String(start),'5','6','5','6'],2),['6']);
  await assert.rejects(calculator({indicator:'mfi',parameters:{period:2}},['timestamp','open','high','low','close'],'d1'),/volume/);
});
test('unmatched timestamps produce no fabricated ratio candles and keep completed quote cache for retry',async t=>{
  const request=await fixture(t);
  const unmatched=transport(url=>{const hour=hourOf(url);return hour%DAY?empty(hour):payload(hour,[10,20,30],url.includes('/BTC-USD/')?[0,100,200]:[1,101,201]);});
  await assert.rejects(download(request,{api:unmatched,emit:()=>{}}),/simultaneous source BID/);
  assert.equal(await fs.readFile(request.output,'utf8'),'');
  await assert.rejects(download(request,{api:transport(()=>assert.fail('completed empty join refetched')),emit:()=>{}}),/simultaneous source BID/);
});
test('ratio download resumes committed daily bars and truncates only an uncommitted tail',async t=>{
  const request=await fixture(t,'btcxau',start,start+2*DAY);let failed=false;
  await assert.rejects(download(request,{api:transport(quotes),emit:()=>{},onChunk:event=>{if(!failed&&event.symbol==='btcusd'&&event.hour>=start+DAY+HOUR){failed=true;throw Error('Test interrupted after first committed day');}}}),/interrupted/);
  const saved=JSON.parse(await fs.readFile(path.join(path.dirname(request.output),'synthetic-checkpoint.json'),'utf8'));assert.equal(saved.rows,1);
  await fs.appendFile(request.output,'uncommitted');
  let result;await download(request,{api:transport(quotes),emit:event=>{if(event.type==='done')result=event;}});
  assert.equal(result.rows,2);assert.equal(Object.hasOwn(result.provenance,'segments'),false);
  assert.deepEqual((await fs.readFile(request.output,'utf8')).trim().split('\n').slice(1).map(line=>Number(line.split(',')[0])),[start,start+DAY]);
});
test('XAUXAG combines a calculated prefix with native BID ticks from their actual availability instant',async t=>{
  const begin=Date.UTC(2024,8,17),native=Date.parse(api.instrumentMetaData.xauxag.startHourForTicks),request=await fixture(t,'xauxag',begin,begin+DAY);
  const calls=[];const instrumented=transport(url=>{
    calls.push(url);const hour=hourOf(url);
    if(url.includes('/XAU-XAG/'))return hour===Math.floor(native/HOUR)*HOUR?payload(hour,[50],[(native-hour)]):empty(hour);
    if(hour===begin)return payload(hour,url.includes('/XAU-USD/')?[10,20,30]:[2,4,5]);
    return empty(hour);
  });
  let result;await download(request,{api:instrumented,emit:event=>{if(event.type==='done')result=event;}});
  assert.equal(await fs.readFile(request.output,'utf8'),`timestamp,open,high,low,close\n${begin},5,50,5,50\n`);
  assert.equal(result.provenance.kind,'mixed-native-ratio');
  assert.deepEqual(result.provenance.segments,[{from:begin,to:native,kind:'synchronized-bid-ratio'},{from:native,to:begin+DAY,kind:'native'}]);
  assert.ok(calls.some(url=>url.includes('/XAU-XAG/')));
});
test('native XAUXAG success is preferred and access failures never trigger a ratio fallback',async t=>{
  const request=await fixture(t,'xauxag');
  // 2024-01 predates this native feed, so move the actual request into native history.
  const begin=Date.UTC(2025,0,1);Object.assign(request,{from:new Date(begin).toISOString(),to:new Date(begin+DAY).toISOString(),asOf:begin+DAY});
  let calls=0,result;
  const native=transport(url=>{calls++;assert.match(url,/candles\/day\/XAU-XAG/);return Buffer.from(JSON.stringify({timestamp:Date.UTC(2025,0,1),shift:DAY,multiplier:1,open:10,high:12,low:9,close:11,times:[0],opens:[0],highs:[0],lows:[0],closes:[0],volumes:[3]}));});
  await download(request,{api:native,emit:event=>{if(event.type==='done')result=event;}});assert.equal(calls,1);assert.equal(result.provenance.kind,'native');assert.match(await fs.readFile(request.output,'utf8'),/,volume\n/);
  const refused=await fixture(t,'xauxag',begin,begin+DAY);
  await assert.rejects(download(refused,{api:transport(url=>{assert.match(url,/candles\/day\/XAU-XAG/);throw Error('HTTP 403 test access denial');}),emit:()=>{}}),/403/);
});
test('native XAUXAG retains legacy tick downloads on its first partially available day',async t=>{
  const first=Date.parse(api.instrumentMetaData.xauxag.startHourForTicks),hour=Math.floor(first/HOUR)*HOUR;
  const request={...await fixture(t,'xauxag',hour,hour+HOUR),timeframe:'tick'};
  let result;await download(request,{api:transport(url=>{assert.ok(url.includes('/ticks/XAU-XAG/'));return payload(hour,[50],[first-hour]);}),emit:event=>{if(event.type==='done')result=event;}});
  assert.equal(result.rows,1);assert.equal(result.provenance.kind,'native');assert.match(await fs.readFile(request.output,'utf8'),/askPrice,bidPrice,askVolume,bidVolume/);
});
test('refresh preserves absent volume, calculated provenance and reproducible price indicators',async t=>{
  const request=await fixture(t);await download(request,{api:transport(quotes),emit:()=>{}});
  const output=path.join(path.dirname(request.output),'refreshed.csv');
  const result=await refresh({...request,input:request.output,output,from:'2024-01-01',asOf:start+2*DAY,requiredFields:['sma_1']},{api:transport(quotes),emit:()=>{}});
  assert.equal(result.rows,2);assert.equal(result.provenance.volume,'unavailable');
  assert.equal((await fs.readFile(output,'utf8')).split('\n')[0],'timestamp,open,high,low,close,sma_1');
});
test('verified empty native XAUXAG periods permit ratio fallback and resume does not repeat native access',async t=>{
  const begin=Date.UTC(2025,0,1),request=await fixture(t,'xauxag',begin,begin+DAY);let nativeCalls=0,result;
  const instrumented=transport(url=>{
    if(url.includes('/candles/')){nativeCalls++;return Buffer.from(JSON.stringify({timestamp:begin,times:[],opens:[],highs:[],lows:[],closes:[],volumes:[]}));}
    const hour=hourOf(url);return hour%DAY?empty(hour):payload(hour,url.includes('/XAU-USD/')?[10,20,30]:[2,4,5]);
  });
  await download(request,{api:instrumented,emit:event=>{if(event.type==='done')result=event;}});
  assert.equal(nativeCalls,1);assert.equal(result.provenance.kind,'synchronized-bid-ratio');assert.equal(result.provenance.approximate,true);
  await download(request,{api:transport(()=>assert.fail('completed fallback refetched')),emit:()=>{}});
  const checkpoint=path.join(path.dirname(request.output),'synthetic-checkpoint.json'),saved=JSON.parse(await fs.readFile(checkpoint,'utf8')),before=await fs.readFile(request.output);
  await fs.writeFile(checkpoint,JSON.stringify({...saved,mode:'mixed'}));
  await assert.rejects(download(request,{api:transport(()=>assert.fail('changed fallback mode fetched')),emit:()=>{}}),/checkpoint/);
  assert.deepEqual(await fs.readFile(request.output),before);
});
test('weekly candles aggregate retained ratio daily OHLC without requiring or fabricating volume',async t=>{
  const request=await fixture(t);await download(request,{api:transport(quotes),emit:()=>{}});
  const info=await fs.stat(request.output),plan=path.join(path.dirname(request.output),'daily-plan.json');
  await fs.writeFile(plan,JSON.stringify({version:1,asOf:request.asOf,inputs:[{input:request.output,bytes:info.size,modified:Math.trunc(info.mtimeMs),metadata:{checksum:(await scan(request.output,'d1')).checksum,provenance:{version:1,kind:'synchronized-bid-ratio',volume:'unavailable',approximate:true}}}]}));
  const output=path.join(path.dirname(request.output),'weekly.csv');await fs.writeFile(output,'');let result;
  await download({...request,timeframe:'w1',output,dailyPlanFile:plan},{api:transport(()=>assert.fail('weekly derivation fetched source quotes')),emit:event=>{if(event.type==='done')result=event;}});
  assert.equal((await fs.readFile(output,'utf8')).split('\n')[0],'timestamp,open,high,low,close');assert.equal(result.provenance.volume,'unavailable');
});
test('checkpoint mode and temporal provenance are validated before touching the saved CSV',async t=>{
  const request=await fixture(t);await download(request,{api:transport(quotes),emit:()=>{}});
  const before=await fs.readFile(request.output),file=path.join(path.dirname(request.output),'synthetic-checkpoint.json'),saved=JSON.parse(await fs.readFile(file,'utf8'));
  await fs.writeFile(file,JSON.stringify({...saved,mode:'mixed'}));
  await assert.rejects(download(request,{api:transport(()=>assert.fail('bad checkpoint fetched')),emit:()=>{}}),/checkpoint/);
  assert.deepEqual(await fs.readFile(request.output),before);
  saved.provenance.segments=[{from:start+100,to:start,kind:'native'}];await fs.writeFile(file,JSON.stringify(saved));
  await assert.rejects(download(request,{api:transport(()=>assert.fail('bad provenance fetched')),emit:()=>{}}),/provenance segments/);
  assert.deepEqual(await fs.readFile(request.output),before);
});
test('bounded refresh preserves the earlier calculated prefix while refreshing a native XAUXAG suffix',async t=>{
  const begin=Date.UTC(2024,8,17),request=await fixture(t,'xauxag',begin,begin+6*DAY),input=request.output;
  const csv='timestamp,open,high,low,close\n'+Array.from({length:6},(_,i)=>`${begin+i*DAY},5,6,4,5\n`).join('');await fs.writeFile(input,csv);
  const checked=await scan(input,'d1'),output=path.join(path.dirname(input),'refresh.csv');
  const previous={version:1,kind:'mixed-native-ratio',numerator:'xauusd',denominator:'xagusd',priceType:'bid',maxQuoteAgeMs:0,sessionPolicy:'exact-timestamp-no-carry-v1',volume:'unavailable',approximate:true,observationResolution:'simultaneous millisecond BID ticks',
    segments:[{from:begin,to:begin+10*HOUR,kind:'synchronized-bid-ratio'},{from:begin+10*HOUR,to:begin+6*DAY,kind:'native'}]};
  const result=await refresh({...request,input,output,from:'2024-09-17',coverageVerified:true,coverageDigest:checked.checksum,provenance:previous},{emit:()=>{},download:async(next,options)=>{
    assert.equal(Date.parse(next.from),begin+3*DAY);
    await fs.writeFile(next.output,'timestamp,open,high,low,close\n'+Array.from({length:3},(_,i)=>`${begin+(i+3)*DAY},5,6,4,5\n`).join(''));
    options.emit({type:'done',rows:3,provenance:{version:1,kind:'native',numerator:'xauxag',priceType:'bid',maxQuoteAgeMs:0,sessionPolicy:'provider-native',volume:'unavailable',approximate:false,observationResolution:'provider native BID ticks'}});
  }});
  assert.equal(result.provenance.kind,'mixed-native-ratio');assert.deepEqual(result.provenance.segments,previous.segments);assert.equal(await fs.readFile(input,'utf8'),csv);
});
test('incremental synthetic extension fetches only missing days and replays saved price indicators',async t=>{
  const request=await fixture(t);let initial;
  await download(request,{api:transport(quotes),emit:event=>{if(event.type==='done')initial=event;}});
  const enriched=path.join(path.dirname(request.output),'enriched.csv');
  const recipe={indicator:'sma',parameters:{period:1},source:'auto'};
  await require('./enrich-worker.cjs').enrich({...recipe,input:request.output,output:enriched},{emit:()=>{}});
  const original=await fs.readFile(enriched),info=await fs.stat(enriched),data=await scan(enriched,'d1'),plan=path.join(path.dirname(request.output),'extension-plan.json');
  await fs.writeFile(plan,JSON.stringify({version:1,asOf:start+2*DAY,inputs:[{input:enriched,bytes:info.size,modified:Math.trunc(info.mtimeMs),from:'2024-01-01',
    metadata:{checksum:data.checksum,coverage:initial.coverage,provenance:initial.provenance,recipes:[recipe]}}]}));
  const output=path.join(path.dirname(request.output),'extended.csv');await fs.writeFile(output,'');
  const sources=[];let result;
  await download({...request,to:new Date(start+2*DAY).toISOString(),asOf:start+2*DAY,output,planFile:plan},{api:transport(url=>{sources.push(hourOf(url));return quotes(url);}),emit:event=>{if(event.type==='done')result=event;}});
  assert.equal(result.rows,2);assert.equal(result.provenance.kind,'synchronized-bid-ratio');assert.equal(result.provenance.volume,'unavailable');
  assert.ok(sources.length>0&&sources.every(hour=>hour>=start+DAY));assert.deepEqual(await fs.readFile(enriched),original);
  assert.deepEqual(result.coverage,[{from:start,to:start+2*DAY}]);assert.equal((await fs.readFile(output,'utf8')).split('\n')[0],'timestamp,open,high,low,close,sma_1');
  assert.deepEqual((await fs.readFile(output,'utf8')).trim().split('\n').slice(1).map(line=>line.split(',').at(-1)),['6','6']);
});
test('native-to-ratio Refresh drops volume, replays price recipes and keeps the original untouched',async t=>{
  const request=await fixture(t,'xauxag'),original=`timestamp,open,high,low,close,volume,sma_1\n${start},10,12,9,11,3,11\n`;
  await fs.writeFile(request.output,original);const output=path.join(path.dirname(request.output),'transition.csv');
  const ratio={version:1,kind:'synchronized-bid-ratio',numerator:'xauusd',denominator:'xagusd',priceType:'bid',maxQuoteAgeMs:0,sessionPolicy:'exact-timestamp-no-carry-v1',volume:'unavailable',approximate:true,observationResolution:'simultaneous millisecond BID ticks'};
  const result=await refresh({...request,input:request.output,output,from:'2024-01-01'},{emit:()=>{},download:async(next,options)=>{
    await fs.writeFile(next.output,`timestamp,open,high,low,close\n${start},5,6,4,6\n`);options.emit({type:'done',rows:1,provenance:ratio});
  }});
  assert.equal(await fs.readFile(output,'utf8'),`timestamp,open,high,low,close,sma_1\n${start},5,6,4,6,6\n`);
  assert.equal(result.provenance.kind,'synchronized-bid-ratio');assert.equal(await fs.readFile(request.output,'utf8'),original);
});
test('backward native XAUXAG extension produces one mixed no-volume history and rejects volume recipes',async t=>{
  for(const indicator of ['sma','mfi']){
    const begin=Date.UTC(2024,8,16),request=await fixture(t,'xauxag',begin,begin+3*DAY),period=indicator==='sma'?1:2;
    const recipe={indicator,parameters:{period},source:'auto'},column=`${indicator}_${period}`;
    const original=`timestamp,open,high,low,close,volume,${column}\n${begin+2*DAY},20,21,19,20,3,${indicator==='sma'?'20':''}\n`;
    await fs.writeFile(request.output,original);const info=await fs.stat(request.output),checked=await scan(request.output,'d1'),plan=path.join(path.dirname(request.output),'native-plan.json');
    const native=require('./synthetic-worker.cjs').provenance('xauxag',false),ratio=require('./synthetic-worker.cjs').provenance('xauxag',true);
    await fs.writeFile(plan,JSON.stringify({version:1,asOf:request.asOf,inputs:[{input:request.output,bytes:info.size,modified:Math.trunc(info.mtimeMs),from:'2024-09-18',
      metadata:{checksum:checked.checksum,coverage:[{from:begin+2*DAY,to:begin+3*DAY}],provenance:native,recipes:[recipe]}}]}));
    const output=path.join(path.dirname(request.output),'extended-native.csv');await fs.writeFile(output,'');let result;
    const operation=download({...request,output,planFile:plan},{emit:event=>{if(event.type==='done')result=event;},download:async(next,options)=>{
      await fs.writeFile(next.output,`timestamp,open,high,low,close\n${begin},5,6,4,5\n${begin+DAY},6,7,5,6\n`);
      options.emit({type:'done',rows:2,provenance:ratio});
    }});
    if(indicator==='mfi')await assert.rejects(operation,/volume|reproduce/i);
    else{
      await operation;assert.equal(result.rows,3);assert.equal(result.provenance.kind,'mixed-native-ratio');
      const text=await fs.readFile(output,'utf8');assert.equal(text.split('\n')[0],'timestamp,open,high,low,close,sma_1');
      assert.deepEqual(text.trim().split('\n').slice(1).map(row=>row.split(',').at(-1)),['5','6','20']);
    }
    assert.equal(await fs.readFile(request.output,'utf8'),original);
  }
});
test('weekly rebuild drops legacy native volume and retains older weekly provenance outside daily coverage',async t=>{
  const request=await fixture(t,'xauxag',start,start+DAY),directory=path.dirname(request.output);
  const native=require('./synthetic-worker.cjs').provenance('xauxag',false),ratio=require('./synthetic-worker.cjs').provenance('xauxag',true);
  await fs.writeFile(request.output,`timestamp,open,high,low,close\n${start},5,6,4,5\n`);
  const older=path.join(directory,'old-weekly.csv');await fs.writeFile(older,`timestamp,open,high,low,close,volume\n${start-7*DAY},10,12,9,11,3\n`);
  const dailyInfo=await fs.stat(request.output),oldInfo=await fs.stat(older),dailyPlan=path.join(directory,'daily-plan.json'),weeklyPlan=path.join(directory,'weekly-plan.json');
  await fs.writeFile(dailyPlan,JSON.stringify({version:1,asOf:request.asOf,inputs:[{input:request.output,bytes:dailyInfo.size,modified:Math.trunc(dailyInfo.mtimeMs),metadata:{checksum:(await scan(request.output,'d1')).checksum,provenance:ratio}}]}));
  await fs.writeFile(weeklyPlan,JSON.stringify({version:1,asOf:request.asOf,inputs:[{input:older,bytes:oldInfo.size,modified:Math.trunc(oldInfo.mtimeMs),metadata:{checksum:(await scan(older,'w1')).checksum,provenance:native}}]}));
  const output=path.join(directory,'mixed-weekly.csv');await fs.writeFile(output,'');let result;
  await download({...request,timeframe:'w1',output,dailyPlanFile:dailyPlan,planFile:weeklyPlan},{emit:event=>{if(event.type==='done')result=event;}});
  assert.equal(result.rows,2);assert.equal((await fs.readFile(output,'utf8')).split('\n')[0],'timestamp,open,high,low,close');
  assert.equal(result.provenance.kind,'mixed-native-ratio');assert.equal(result.provenance.segments[0].from,start-7*DAY);
  const legacy=JSON.parse(await fs.readFile(weeklyPlan,'utf8'));delete legacy.inputs[0].metadata.provenance;await fs.writeFile(weeklyPlan,JSON.stringify(legacy));
  const legacyOutput=path.join(directory,'mixed-weekly-legacy.csv');await fs.writeFile(legacyOutput,'');
  await download({...request,timeframe:'w1',output:legacyOutput,dailyPlanFile:dailyPlan,planFile:weeklyPlan},{emit:event=>{if(event.type==='done')result=event;}});
  assert.equal(result.provenance.kind,'mixed-native-ratio');assert.equal(result.provenance.segments[0].kind,'native');
  assert.equal(result.provenance.segments[0].from,start-7*DAY);
  assert.equal(await fs.readFile(legacyOutput,'utf8'),await fs.readFile(output,'utf8'));
});
test('edited ratio CSV cannot acquire a fresh provenance hash through refresh, extension or weekly derivation',async t=>{
  const request=await fixture(t);let initial;
  await download(request,{api:transport(quotes),emit:event=>{if(event.type==='done')initial=event;}});
  const original=await fs.readFile(request.output,'utf8'),edited=original.replace(',5,6,5,6',',5,7,5,6');await fs.writeFile(request.output,edited);
  const directory=path.dirname(request.output),output=path.join(directory,'invalid-refresh.csv');
  await assert.rejects(refresh({...request,input:request.output,output,from:'2024-01-01',provenance:initial.provenance,coverageDigest:initial.checksum},{emit:()=>{},download:()=>assert.fail('edited history reached provider')}),/provenance checksum/);
  const info=await fs.stat(request.output),plan=path.join(directory,'invalid-plan.json');
  await fs.writeFile(plan,JSON.stringify({version:1,asOf:request.asOf,inputs:[{input:request.output,bytes:info.size,modified:Math.trunc(info.mtimeMs),from:'2024-01-01',metadata:{checksum:initial.checksum,coverage:initial.coverage,provenance:initial.provenance}}]}));
  await fs.writeFile(output,'');
  await assert.rejects(download({...request,output,planFile:plan},{emit:()=>{},download:()=>assert.fail('edited history reached provider')}),/provenance checksum/);
  await assert.rejects(download({...request,timeframe:'w1',output,dailyPlanFile:plan},{emit:()=>{}}),/provenance checksum/);
  assert.equal(await fs.readFile(request.output,'utf8'),edited);
});

'use strict';
const test=require('node:test');
const assert=require('node:assert/strict');
const {EventEmitter}=require('node:events');
const {installProviderTransport,dataURL}=require('./provider-transport.cjs');

test('HTTP data requests and Retry-After use owner permits without changing URL or session headers',async t=>{
  const original=global.fetch,lines=new EventEmitter(),events=[];
  const url='https://jetta.dukascopy.com/v1/candles/day/LNK-USD/BID?from=1767225600000';
  global.fetch=async(actual,options)=>{
    assert.equal(actual,url);assert.equal(options.headers.cookie,'supported-test-session');
    assert.equal(options.signal.aborted,false,'network timeout started while waiting for permit');
    assert.equal(events.at(-1).operation,'permit');
    return new Response('limited',{status:429,headers:{'retry-after':'120'}});
  };
  const control=installProviderTransport(lines,{ownerPipe:true,providerSession:{rateLimit:true}},event=>{
    events.push(event);queueMicrotask(()=>lines.emit('line',JSON.stringify({type:'provider-reply',requestId:event.requestId})));
  });
  t.after(()=>{control.close();global.fetch=original;});
  await global.fetch(url,{headers:{cookie:'supported-test-session'},signal:AbortSignal.abort()});
  assert.deepEqual(events.map(event=>event.operation),['permit','response']);
  assert.equal(events[1].httpStatus,429);assert.equal(events[1].retryAfter,'120');
  assert.equal(dataURL('https://example.com/v1/ticks/EUR-USD'),false);
});

test('browser data navigation and responses share the same queue and retain library launch options',async t=>{
  const {chromium}=require('playwright-core'),launch=chromium.launch;
  const lines=new EventEmitter(),context=new EventEmitter(),events=[];
  let handler;
  context.route=async(pattern,routeHandler)=>{assert.equal(pattern,'https://jetta.dukascopy.com/v1/**');handler=routeHandler;};
  chromium.launch=async options=>{assert.deepEqual(options,{headless:true});return{newContext:async()=>context};};
  const control=installProviderTransport(lines,{ownerPipe:true,providerSession:{rateLimit:true}},event=>{
    events.push(event);queueMicrotask(()=>lines.emit('line',JSON.stringify({type:'provider-reply',requestId:event.requestId})));
  });
  t.after(()=>{control.close();chromium.launch=launch;});
  const browser=await chromium.launch({headless:true});await browser.newContext();
  let continued=false;
  const url='https://jetta.dukascopy.com/v1/candles/day/EUR-USD/BID/2024';
  await handler({request:()=>({url:()=>url}),continue:async()=>{continued=true;},abort:async()=>assert.fail('valid route aborted')});
  assert.equal(continued,true);
  context.emit('response',{url:()=>url,status:()=>403,headers:()=>({'retry-after':'900'})});
  await control.drain();
  assert.equal(events[0].operation,'permit');assert.equal(events[1].httpStatus,403);assert.equal(events[1].retryAfter,'900');
});

test('denied permits never send a request and restoring transport removes private listeners',async t=>{
  const lines=new EventEmitter(),original=global.fetch;
  let sent=0;global.fetch=async()=>{sent++;throw Error('must not send');};
  const control=installProviderTransport(lines,{ownerPipe:true,providerSession:{rateLimit:true}},event=>{
    queueMicrotask(()=>lines.emit('line',JSON.stringify({type:'provider-reply',requestId:event.requestId,error:'Dukascopy HTTP 403 cooldown; no request sent'})));
  });
  t.after(()=>{control.close();global.fetch=original;});
  await assert.rejects(global.fetch('https://jetta.dukascopy.com/v1/ticks/EUR-USD/2024/1/1/0'),/cooldown/);
  assert.equal(sent,0);control.close();assert.equal(lines.listenerCount('line'),0);
});

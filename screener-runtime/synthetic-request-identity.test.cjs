'use strict';

const test=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs/promises');
const path=require('node:path');
const os=require('node:os');
const api=require('dukascopy-node');
const {download}=require('./worker.cjs');

const DAY=86400000,HOUR=3600000,start=Date.UTC(2024,0,1);

// Keep these source identities literal and independent of the production ratio
// registry: a mistaken ETH leg must fail rather than receive gold fixture prices.
function btcxauSource(url){
  const parsed=new URL(url);
  assert.equal(parsed.origin,'https://jetta.dukascopy.com');
  assert.equal(parsed.search,'');
  const source=/^\/v1\/ticks\/(BTC-USD|XAU-USD)\/2024\/1\/1\/(\d{1,2})$/.exec(parsed.pathname);
  assert.ok(source,`BTCXAU requested an unexpected source: ${url}`);
  const hour=Number(source[2]);
  assert.ok(hour>=0&&hour<24,`BTCXAU requested an unexpected hour: ${url}`);
  const timestamp=start+hour*HOUR;
  if(hour)return Buffer.from(JSON.stringify({timestamp,times:[],asks:[],bids:[],askVolumes:[],bidVolumes:[]}));
  const prices=source[1]==='BTC-USD'?[10,20,30]:[2,4,5];
  const deltas=prices.map((price,index)=>index?price-prices[index-1]:0);
  return Buffer.from(JSON.stringify({timestamp,multiplier:1,ask:prices[0]+1,bid:prices[0],times:[0,100,100],
    asks:deltas,bids:deltas,askVolumes:[1,1,1],bidVolumes:[1,1,1]}));
}

test('BTCXAU downloads only literal BTCUSD and XAUUSD BID tick sources and computes their ratio',async t=>{
  const directory=await fs.mkdtemp(path.join(os.tmpdir(),'cryptodesk-synthetic-identity-'));
  t.after(()=>fs.rm(directory,{recursive:true,force:true}));
  const output=path.join(directory,'data.part');await fs.writeFile(output,'');
  const requests=[];
  const transport={...api,BufferFetcher:class extends api.BufferFetcher{
    constructor(config){super({...config,fetcherFn:async url=>{
      const response=btcxauSource(url);requests.push(url);return response;
    }});}
  }};
  let result;
  await download({symbol:'btcxau',timeframe:'d1',from:new Date(start).toISOString(),to:new Date(start+DAY).toISOString(),
    asOf:start+DAY,output,resume:true,syntheticCacheDirectory:path.join(directory,'quotes')},
  {api:transport,emit:event=>{if(event.type==='done')result=event;}});
  assert.equal(requests.length,48,'both legs must fetch all 24 closed source hours');
  assert.deepEqual([...new Set(requests.map(url=>new URL(url).pathname.split('/')[3]))].sort(),['BTC-USD','XAU-USD']);
  assert.equal(result.provenance.numerator,'btcusd');assert.equal(result.provenance.denominator,'xauusd');
  assert.equal(await fs.readFile(output,'utf8'),`timestamp,open,high,low,close\n${start},5,6,5,6\n`);
});

#!/usr/bin/env python3
"""Exercise packaged enrichment, Refresh and incremental downloads offline."""
import argparse
import csv
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile


def calendar_smoke(runtime, node, directory):
    """Calendar columns use UTC even when the bundled Node process does not."""
    environment = {**os.environ, "TZ": "Pacific/Honolulu"}
    worker = runtime / "enrich-worker.cjs"
    sunday, monday = 1704067140000, 1704067200000
    probe = subprocess.run(
        [str(node), "-e", "const d=new Date(1704067200000);process.stdout.write(JSON.stringify({"
         "zone:Intl.DateTimeFormat().resolvedOptions().timeZone,hour:d.getHours(),"
         "weekday:d.getDay(),offset:d.getTimezoneOffset()}))"],
        text=True, capture_output=True, cwd=directory, env=environment, timeout=20, check=True)
    local = json.loads(probe.stdout)
    if local.get("offset") == 0 or local.get("hour") == 0 or local.get("weekday") != 0:
        raise ValueError(f"Calendar smoke did not exercise a non-UTC Sunday local clock: {probe.stdout[:1000]}")

    def enrich(source, output, indicator):
        before = source.read_bytes()
        request = {"input": str(source), "output": str(output), "indicator": indicator,
                   "parameters": {}, "source": "auto"}
        result = subprocess.run([str(node), str(worker)], input=json.dumps(request) + "\n", text=True,
                                capture_output=True, cwd=directory, env=environment, timeout=20, check=True)
        events = [json.loads(line) for line in result.stdout.splitlines()]
        if (not events or events[-1].get("type") != "done" or events[-1].get("rows") != 2
                or events[-1].get("columns") != [indicator] or source.read_bytes() != before):
            raise ValueError(f"Packaged {indicator} enrichment did not preserve and confirm its input: {result.stdout[:2000]}")
        with output.open(newline="") as stream:
            return list(csv.reader(stream))

    original = f"timestamp\n{sunday}\n{monday}\n"
    source = directory / "calendar-original.csv"
    source.write_bytes(original.encode())
    weekday, hour = directory / "calendar-weekday.csv", directory / "calendar-hour.csv"
    if enrich(source, weekday, "weekday") != [["timestamp", "weekday"], [str(sunday), "7"], [str(monday), "1"]]:
        raise ValueError("Packaged Weekday did not calculate Sunday=7 and Monday=1 without warm-up")
    expected = [["timestamp", "weekday", "hour"], [str(sunday), "7", "23"], [str(monday), "1", "0"]]
    if enrich(weekday, hour, "hour") != expected:
        raise ValueError("Packaged Hour did not calculate UTC 23 to 0 while preserving Weekday")
    repeated_weekday, repeated_hour = directory / "calendar-weekday-repeat.csv", directory / "calendar-hour-repeat.csv"
    if enrich(hour, repeated_weekday, "weekday") != expected or enrich(repeated_weekday, repeated_hour, "hour") != expected:
        raise ValueError("Repeating calendar enrichment duplicated columns or changed row alignment")
    if repeated_hour.read_bytes() != hour.read_bytes() or source.read_bytes() != original.encode():
        raise ValueError("Repeated calendar enrichment changed its previous result or the original CSV")

    # A separate candle file lets the actual downloader/decoder append the
    # first closed Monday candle and replay both previously applied columns.
    raw = directory / "calendar-candles-original.csv"
    raw_text = ("timestamp,open,high,low,close,volume\n"
                "1704067080000,10,11,9,10,3\n1704067140000,20,21,19,20,4\n")
    raw.write_bytes(raw_text.encode())
    candle_weekday, candle_hour = directory / "calendar-candles-weekday.csv", directory / "calendar-candles-hour.csv"
    enrich(raw, candle_weekday, "weekday")
    originals = enrich(candle_weekday, candle_hour, "hour")
    if [row[-2:] for row in originals[1:]] != [["7", "23"], ["7", "23"]]:
        raise ValueError("Calendar Refresh fixture did not start with two enriched Sunday candles")
    before = candle_hour.read_bytes()
    output = directory / "calendar-refreshed.csv"
    now = monday + 60000
    recipes = [{"indicator": indicator, "parameters": {}, "source": "auto"} for indicator in ["weekday", "hour"]]
    request = {"runtime": str(runtime), "now": now,
               "request": {"input": str(candle_hour), "output": str(output), "symbol": "eurusd",
                           "timeframe": "m1", "from": "2023-12-31", "recipes": recipes}}
    script = r"""
const fs = require('node:fs');
const path = require('node:path');
const input = JSON.parse(fs.readFileSync(0, 'utf8'));
const {refresh} = require(path.join(input.runtime, 'refresh-worker.cjs'));
const api = require(path.join(input.runtime, 'node_modules', 'dukascopy-node'));
const sources = [];
const localApi = {...api, BufferFetcher: class extends api.BufferFetcher {
  constructor(options) {
    super({...options, fetcherFn: async url => {
      const pathname = new URL(url).pathname;
      if (!pathname.includes('/v1/candles/minute/')) throw Error('Unexpected calendar source interval.');
      sources.push(pathname);
      const sunday = pathname.endsWith('/2023/12/31');
      if (!sunday && !pathname.endsWith('/2024/1/1')) throw Error('Unexpected calendar source day.');
      const price = sunday ? 10 : 30;
      return Buffer.from(JSON.stringify({timestamp: sunday ? 1704067080000 : 1704067200000,
        shift: 60000, multiplier: 1, open: price, high: price+1, low: price-1, close: price,
        times: [0,1], opens: [0,10], highs: [0,10], lows: [0,10], closes: [0,10],
        volumes: sunday ? [3,4] : [5,6]}));
    }});
  }
}};
(async () => {
  const events = [];
  const result = await refresh(input.request, {api: localApi, now: input.now,
    pauseMs: 0, emit: event => events.push(event)});
  process.stdout.write(JSON.stringify({result, events, sources}) + '\n');
})().catch(error => {console.error(error); process.exitCode = 1;});
"""
    result = subprocess.run([str(node), "-e", script], input=json.dumps(request) + "\n", text=True,
                            capture_output=True, cwd=directory, env=environment, timeout=20, check=True)
    actual = json.loads(result.stdout)
    completed, events, sources = actual.get("result", {}), actual.get("events", []), actual.get("sources", [])
    if (completed.get("type") != "done" or completed.get("rows") != 3 or completed.get("addedRows") != 1
            or completed.get("updatedRows") != 0 or completed.get("columns") != ["weekday", "hour"]
            or completed.get("recipes") != recipes or completed.get("checkedThrough") != now
            or completed.get("firstTimestamp") != sunday - 60000 or completed.get("lastTimestamp") != monday
            or len(sources) != 2 or not events or events[-1].get("type") != "done"):
        raise ValueError(f"Packaged Refresh did not append and replay calendar fields: {result.stdout[:4000]}")
    with output.open(newline="") as stream:
        refreshed = list(csv.reader(stream))
    if (refreshed != originals + [[str(monday), "30", "31", "29", "30", "5", "1", "0"]]
            or candle_hour.read_bytes() != before or raw.read_bytes() != raw_text.encode()):
        raise ValueError("Calendar Refresh changed its originals, included an open candle or miscalculated UTC fields")
    return {"passed": True, "timeZoneEnvironment": environment["TZ"], "resolvedTimeZone": local["zone"],
            "localHourAtMondayUTC": local["hour"], "localWeekdayAtMondayUTC": local["weekday"],
            "timestampOnly": {"passed": True, "rows": 2, "columns": ["weekday", "hour"],
                              "weekdays": [7, 1], "hours": [23, 0], "warmupRequired": False,
                              "repeatIsIdempotent": True, "originalPreserved": True},
            "refresh": {"passed": True, "rows": 3, "addedRows": 1, "updatedRows": 0,
                        "columns": ["weekday", "hour"], "savedRecipesRecalculated": True,
                        "onlyClosedCandlesAppended": True, "exactValues": True,
                        "originalsPreserved": True, "localTransportFetches": len(sources),
                        "injectedNowUTC": "2024-01-01T00:01:00.000Z", "networkRequired": False},
            "networkRequired": False}


def refresh_smoke(runtime, node, directory, enriched):
    """Use the real downloader/decoder and Refresh merger; replace transport only."""
    output = directory / "refreshed.csv"
    before = enriched.read_bytes()
    start = 1704067200000
    now = start + 5 * 60000
    request = {"runtime": str(runtime), "now": now,
               "request": {"input": str(enriched), "output": str(output), "symbol": "eurusd",
                           "timeframe": "m1", "from": "2024-01-01",
                           "recipes": [{"indicator": "sma", "parameters": {"period": 3}, "source": "auto"}]}}
    script = r"""
const fs = require('node:fs');
const path = require('node:path');
const input = JSON.parse(fs.readFileSync(0, 'utf8'));
const {refresh} = require(path.join(input.runtime, 'refresh-worker.cjs'));
const api = require(path.join(input.runtime, 'node_modules', 'dukascopy-node'));
let fetches = 0;
const localApi = {...api, BufferFetcher: class extends api.BufferFetcher {
  constructor(options) {
    super({...options, fetcherFn: async url => {
      if (!new URL(url).pathname.includes('/v1/candles/minute/')) {
        throw Error('Refresh requested an unexpected source interval.');
      }
      fetches++;
      // Six real wire-format candles. At the injected 00:05 UTC clock only
      // the first five are closed; the last supplied candle must not append.
      return Buffer.from(JSON.stringify({timestamp: 1704067200000, shift: 60000,
        multiplier: 1, open: 1, high: 2, low: 0, close: 1,
        times: [0,1,1,1,1,1], opens: [0,1,1,1,1,1],
        highs: [0,1,1,1,1,1], lows: [0,1,1,1,1,1],
        closes: [0,1,1,1,1,1], volumes: [3,4,5,6,7,8]}));
    }});
  }
}};
(async () => {
  const events = [];
  const result = await refresh(input.request, {api: localApi, now: input.now,
    pauseMs: 0, emit: event => events.push(event)});
  process.stdout.write(JSON.stringify({result, events, fetches}) + '\n');
})().catch(error => {console.error(error); process.exitCode = 1;});
"""
    result = subprocess.run([str(node), "-e", script], input=json.dumps(request) + "\n", text=True,
                            capture_output=True, cwd=directory, timeout=20, check=True)
    actual = json.loads(result.stdout)
    completed = actual.get("result", {})
    events = actual.get("events", [])
    recipe = {"indicator": "sma", "parameters": {"period": 3}, "source": "auto"}
    if (actual.get("fetches") != 1 or completed.get("type") != "done" or completed.get("rows") != 5
            or completed.get("addedRows") != 1 or completed.get("updatedRows") != 0
            or completed.get("columns") != ["sma_3"] or completed.get("recipes") != [recipe]
            or completed.get("checkedThrough") != now or completed.get("lastTimestamp") != start + 4 * 60000
            or not events or events[-1].get("type") != "done"):
        raise ValueError(f"Packaged Refresh did not confirm the expected append and indicator replay: {result.stdout[:4000]}")
    with output.open(newline="") as stream:
        refreshed = list(csv.reader(stream))
    original = list(csv.reader(io.StringIO(before.decode())))
    expected = original + [[str(start + 4 * 60000), "5", "6", "4", "5", "7", "4"]]
    if (refreshed != expected or [row[-1] for row in refreshed[1:]] != ["", "", "2", "3", "4"]
            or enriched.read_bytes() != before):
        raise ValueError("Packaged Refresh changed the original, appended an open candle or miscalculated saved SMA_3")
    return {"passed": True, "rows": 5, "addedRows": 1, "updatedRows": 0, "columns": ["sma_3"],
            "savedRecipeRecalculated": True, "warmupBlank": True, "exactValues": True,
            "enrichedOriginalPreserved": True, "onlyClosedCandlesAppended": True,
            "localTransportFetches": actual["fetches"], "injectedNowUTC": "2024-01-01T00:05:00.000Z",
            "networkRequired": False}


def incremental_smoke(runtime, node, directory, enriched):
    """Exercise the packaged incremental worker with its real decoder, offline."""
    before = enriched.read_bytes()
    work = directory / "incremental-work"
    work.mkdir()
    output, plan_file = work / "data.part", work / "plan.json"
    output.write_bytes(b"")
    stat = enriched.stat()
    cutoff = 1704067560000  # 2024-01-01 00:06 UTC
    plan_file.write_text(json.dumps({"version": 1, "asOf": cutoff, "inputs": [{
        "input": str(enriched), "name": enriched.name, "from": "2024-01-01",
        "bytes": stat.st_size, "modified": stat.st_mtime_ns // 1000000, "metadata": {}}]}))
    request = {"runtime": str(runtime), "request": {"planFile": str(plan_file), "output": str(output),
               "symbol": "eurusd", "timeframe": "m1", "from": "2023-12-31", "to": "2024-01-02",
               "sourceBatchSize": 1}}
    script = r"""
const fs=require('node:fs'),path=require('node:path');
const input=JSON.parse(fs.readFileSync(0,'utf8'));
const {extendDownload}=require(path.join(input.runtime,'incremental-worker.cjs'));
const api=require(path.join(input.runtime,'node_modules','dukascopy-node'));
let fetches=0;
const localApi={...api,BufferFetcher:class extends api.BufferFetcher{
 constructor(options){super({...options,fetcherFn:async url=>{
  fetches++;
  const pathname=new URL(url).pathname;
  if(!pathname.includes('/v1/candles/minute/'))throw Error('Unexpected incremental source interval');
  const prefix=pathname.endsWith('/2023/12/31');
  if(!prefix&&!pathname.endsWith('/2024/1/1'))throw Error('Unexpected incremental source day');
  const price=prefix?0:5,deltas=prefix?[0]:[0,1];
  return Buffer.from(JSON.stringify({timestamp:prefix?1704067140000:1704067440000,
   shift:60000,multiplier:1,open:price,high:price+1,low:price-1,close:price,
   times:deltas,opens:deltas,highs:deltas,lows:deltas,closes:deltas,volumes:prefix?[2]:[7,8]}));
 }});}
}};
(async()=>{
 const events=[];
 await extendDownload(input.request,{api:localApi,pauseMs:0,emit:event=>events.push(event)});
 const ranges=JSON.parse(fs.readFileSync(path.join(path.dirname(input.request.output),'ranges.json'),'utf8')).ranges;
 process.stdout.write(JSON.stringify({result:events.at(-1),fetches,ranges})+'\n');
})().catch(error=>{console.error(error);process.exitCode=1;});
"""
    run = subprocess.run([str(node), "-e", script], input=json.dumps(request) + "\n", text=True,
                         capture_output=True, cwd=directory, timeout=20, check=True)
    actual = json.loads(run.stdout)
    result = actual["result"]
    rows = list(csv.reader(io.StringIO(output.read_text())))
    expected_ranges = [{"from": 1703980800000, "to": 1704067200000},
                       {"from": 1704067440000, "to": cutoff}]
    if (actual["fetches"] != 2 or actual["ranges"] != expected_ranges or result.get("type") != "done"
            or result.get("rows") != 7 or len(rows) != 8 or result.get("firstTimestamp") != 1704067140000
            or result.get("lastTimestamp") != 1704067500000 or enriched.read_bytes() != before
            or [row[-1] for row in rows[1:]] != ["", "", "1", "2", "3", "4", "5"]
            or [float(row[4]) for row in rows[1:]] != list(range(7))):
        raise ValueError(f"Packaged incremental download did not prepend/append and replay SMA: {run.stdout[:4000]}")
    return {"passed": True, "rows": 7, "missingRanges": actual["ranges"], "localTransportFetches": 2,
            "existingSourcePreserved": True, "prependedWarmupRecalculated": True, "columns": ["sma_3"],
            "networkRequired": False}


def provider_smoke(runtime, node, directory):
    """Exercise sequential packaged requests, shared sessions and denial handling."""
    script = r"""
const fs=require('node:fs/promises'),path=require('node:path');
const input=JSON.parse(require('node:fs').readFileSync(0,'utf8'));
const api=require(path.join(input.runtime,'node_modules','dukascopy-node'));
const {createProviderFetcher}=require(path.join(input.runtime,'provider-session.cjs'));
const {installProviderTransport}=require(path.join(input.runtime,'provider-transport.cjs'));
const {EventEmitter}=require('node:events');
(async()=>{
  const directory=path.join(input.directory,'provider-batch'),tokenCache=path.join(input.directory,'provider-session.json');
  await fs.mkdir(directory);
  const session=JSON.stringify({version:2,origin:'https://jetta.dukascopy.com',token:'offline-fixture',userAgent:'CryptoDesk offline fixture'});
  await fs.writeFile(tokenCache,session,{mode:0o600});
  let requests=0,denied=false,inFlight=0,maxInFlight=0;
  global.fetch=async(url,options)=>{
    if(options.headers.cookie!=='aws-waf-token=offline-fixture')throw Error('Packaged workers did not share the supported session');
    requests++;inFlight++;maxInFlight=Math.max(maxInFlight,inFlight);
    const body=denied?'Forbidden':JSON.stringify({timestamp:1704067200000,shift:86400000,multiplier:1,open:10,high:11,low:9,close:10,
      times:[0],opens:[0],highs:[0],lows:[0],closes:[0],volumes:[1]});
    let timer,released=false;
    const release=()=>{if(!released){released=true;inFlight--;}};
    const stream=new ReadableStream({
      start(controller){timer=setTimeout(()=>{controller.enqueue(Buffer.from(body));controller.close();release();},5);},
      cancel(){clearTimeout(timer);release();}
    });
    return new Response(stream,{status:denied?403:200,headers:{'content-type':'application/json'}});
  };
  const lines=new EventEmitter();let permits=0;
  const transport=installProviderTransport(lines,{ownerPipe:true,providerSession:{rateLimit:true}},event=>{
    if(event.operation==='permit')permits++;
    queueMicrotask(()=>lines.emit('line',JSON.stringify({type:'provider-reply',requestId:event.requestId})));
  });
  const config={batchSize:1,retryCount:0,failAfterRetryCount:true,wafMode:'auto',wafTokenCache:true};
  const url='https://jetta.dukascopy.com/v1/candles/day/EUR-USD/BID/2024';
  const values=[];
  for(let job=0;job<20;job++)values.push(await createProviderFetcher(api,config,{directory,tokenCache}).fetch([url]));
  if(requests!==20||values.some(value=>value.length!==1)||maxInFlight!==1||inFlight!==0)throw Error('Packaged sequential shared-session downloads failed');
  denied=true;requests=0;
  const deniedDirectory=path.join(input.directory,'denied-batch');await fs.mkdir(deniedDirectory);
  const failures=[];
  for(let job=0;job<20;job++){
    try{await createProviderFetcher(api,config,{directory:deniedDirectory,tokenCache}).fetch([url]);}
    catch(error){failures.push(error);}
  }
  if(requests!==1||failures.length!==20||failures.some(error=>!error.message.includes('Dukascopy refused access (HTTP 403)'))||maxInFlight!==1||inFlight!==0)throw Error('Packaged access-denial gate failed');
  if(await fs.readFile(tokenCache,'utf8')!==session)throw Error('Batch guard discarded the supported session cache');
  await transport.drain();transport.close();
  if(permits!==21)throw Error('Packaged transport did not ask the parent for each request');
  process.stdout.write(JSON.stringify({passed:true,workers:1,sequential:true,queuedJobs:20,completedRequests:values.length,maxInFlight,
    sharedSession:true,ownerPacingPermits:permits,deniedBatchRequests:requests,blockedSubsequentJobs:failures.length-1,networkRequired:false})+'\n');
})().catch(error=>{console.error(error);process.exitCode=1;});
"""
    result = subprocess.run([str(node), "-e", script],
                            input=json.dumps({"runtime": str(runtime), "directory": str(directory)}) + "\n",
                            text=True, capture_output=True, cwd=directory, timeout=20, check=True)
    return json.loads(result.stdout)


def day_week_smoke(runtime, node, directory):
    """Exercise the shared range module from the actual packaged worker offline."""
    source = directory / "range-original.csv"
    original = ("timestamp,open,high,low,close,volume\n"
                "1704067200000,10,12,8,10,1\n1704150000000,10,15,6,10,1\n"
                "1704153600000,8,13,3,8,1\n1704672000000,14,18,10,14,1\n")
    source.write_text(original)
    expected = {
        "today_low": ["8", "6", "3", "10"], "today_high": ["12", "15", "13", "18"],
        "previous_day_low": ["", "", "6", "3"], "previous_day_high": ["", "", "15", "13"],
        "week_low": ["8", "6", "3", "10"], "week_high": ["12", "15", "15", "18"],
        "previous_week_low": ["", "", "", "3"], "previous_week_high": ["", "", "", "15"],
        "two_days_ago_low": ["", "", "", "6"], "two_days_ago_high": ["", "", "", "15"],
        "three_days_ago_low": ["", "", "", ""], "three_days_ago_high": ["", "", "", ""],
        "today_open": ["10", "10", "8", "14"], "today_close": ["10", "10", "8", "14"],
        "previous_day_open": ["", "", "10", "8"], "previous_day_close": ["", "", "10", "8"],
        "two_days_ago_open": ["", "", "", "10"], "two_days_ago_close": ["", "", "", "10"],
        "three_days_ago_open": ["", "", "", ""], "three_days_ago_close": ["", "", "", ""],
        "week_open": ["10", "10", "10", "14"], "week_close": ["10", "10", "8", "14"],
        "previous_week_open": ["", "", "", "10"], "previous_week_close": ["", "", "", "8"],
    }
    current = source
    for indicator in expected:
        output = directory / f"range-{indicator}.csv"
        request = {"input": str(current), "output": str(output), "indicator": indicator,
                   "parameters": {}, "source": "auto", "timeframe": "h1"}
        subprocess.run([str(node), str(runtime / "enrich-worker.cjs")],
                       input=json.dumps(request) + "\n", text=True, capture_output=True,
                       cwd=directory, env={**os.environ, "TZ": "Pacific/Honolulu"}, timeout=20, check=True)
        current = output
    with current.open(newline="") as stream:
        rows = list(csv.DictReader(stream))
    for indicator, values in expected.items():
        if [row[indicator] for row in rows] != values:
            raise ValueError(f"Packaged {indicator} returned incorrect UTC ranges")
    grouped = directory / "range-group.csv"
    request = {"input": str(source), "output": str(grouped), "group": "Day / Week ranges",
               "indicator": "", "parameters": {}, "source": "auto", "timeframe": "h1"}
    subprocess.run([str(node), str(runtime / "enrich-worker.cjs")],
                   input=json.dumps(request) + "\n", text=True, capture_output=True,
                   cwd=directory, timeout=20, check=True)
    with grouped.open(newline="") as stream:
        reader = csv.DictReader(stream)
        group_rows = list(reader)
        if set(reader.fieldnames or []) != set(rows[0]) or len(reader.fieldnames or []) != len(rows[0]):
            raise ValueError("Packaged grouped range enrichment has missing, extra or duplicate columns")
        if group_rows != rows:
            for index, (group_row, individual_row) in enumerate(zip(group_rows, rows), start=1):
                differences = {column: (individual_row[column], group_row.get(column))
                               for column in individual_row if individual_row[column] != group_row.get(column)}
                if differences:
                    raise ValueError(f"Packaged grouped range enrichment differs at row {index}: {differences}")
            raise ValueError(f"Packaged grouped range enrichment has {len(group_rows)} rows, expected {len(rows)}")
    if source.read_text() != original:
        raise ValueError("Packaged day/week ranges changed their original input")
    return {"passed": True, "columns": list(expected), "utc": True, "causal": True,
            "previousWarmupBlank": True, "groupMatchesIndividual": True, "networkRequired": False}


def synthetic_smoke(runtime, node, directory):
    """Exercise only packaged modules and the pinned decoder with offline ticks."""
    script = r"""
const fs=require('node:fs/promises'),path=require('node:path'),assert=require('node:assert/strict');
const input=JSON.parse(require('node:fs').readFileSync(0,'utf8'));
for(const module of ['synthetic-ratio.cjs','synthetic-quotes.cjs','synthetic-worker.cjs','sdk-worker.cjs'])require(path.join(input.runtime,module));
const api=require(path.join(input.runtime,'node_modules','dukascopy-node'));
const {download}=require(path.join(input.runtime,'worker.cjs'));
const {enrich}=require(path.join(input.runtime,'enrich-worker.cjs'));
const start=Date.UTC(2024,0,1),day=86400000;
let fetches=0;
const local={...api,BufferFetcher:class extends api.BufferFetcher{constructor(options){super({...options,fetcherFn:async url=>{
  assert.ok(url.includes('/v1/ticks/'));fetches++;
  const p=new URL(url).pathname.split('/').slice(-4).map(Number),hour=Date.UTC(p[0],p[1]-1,p[2],p[3]);
  if(hour!==start)return Buffer.from(JSON.stringify({timestamp:hour,times:[],asks:[],bids:[],askVolumes:[],bidVolumes:[]}));
  const prices=url.includes('/BTC-USD/')?[10,20,30]:[2,4,5],delta=prices.map((v,i)=>i?v-prices[i-1]:0);
  return Buffer.from(JSON.stringify({timestamp:hour,multiplier:1,ask:prices[0]+1,bid:prices[0],times:[0,100,100],asks:delta,bids:delta,askVolumes:[1,1,1],bidVolumes:[1,1,1]}));
}});}}};
(async()=>{
  const output=path.join(input.directory,'synthetic-daily.csv');await fs.writeFile(output,'');
  const request={symbol:'btcxau',timeframe:'d1',from:new Date(start).toISOString(),to:new Date(start+day).toISOString(),asOf:start+day,output,
    syntheticCacheDirectory:path.join(input.directory,'synthetic-quotes')};
  let daily;await download(request,{api:local,emit:e=>{if(e.type==='done')daily=e;}});
  const newline=String.fromCharCode(10),expected=['timestamp,open,high,low,close',`${start},5,6,5,6`,''].join(newline);
  assert.equal(await fs.readFile(output,'utf8'),expected);assert.equal(daily.provenance.volume,'unavailable');assert.equal(daily.provenance.approximate,true);
  assert.equal(Object.hasOwn(daily.provenance,'segments'),false);
  const cached=path.join(input.directory,'synthetic-cached.csv');await fs.writeFile(cached,'');
  await download({...request,output:cached},{api:{...api,BufferFetcher:class{fetch(){throw Error('Verified synthetic quote cache was refetched');}}},emit:()=>{}});
  assert.equal(await fs.readFile(cached,'utf8'),expected);
  const enriched=path.join(input.directory,'synthetic-enriched.csv');
  await enrich({input:output,output:enriched,indicator:'sma',parameters:{period:1},source:'auto'},{emit:()=>{}});
  assert.equal(await fs.readFile(enriched,'utf8'),['timestamp,open,high,low,close,sma_1',`${start},5,6,5,6,6`,''].join(newline));
  const info=await fs.stat(output),plan=path.join(input.directory,'synthetic-daily-plan.json');
  await fs.writeFile(plan,JSON.stringify({version:1,asOf:request.asOf,inputs:[{input:output,bytes:info.size,modified:Math.trunc(info.mtimeMs),metadata:{provenance:daily.provenance,checksum:daily.checksum}}]}));
  const weekly=path.join(input.directory,'synthetic-weekly.csv');await fs.writeFile(weekly,'');let week;
  await download({...request,timeframe:'w1',output:weekly,dailyPlanFile:plan},{api:new Proxy({},{get(){throw Error('Weekly synthetic aggregation reached provider');}}),emit:e=>{if(e.type==='done')week=e;}});
  assert.equal(await fs.readFile(weekly,'utf8'),expected);assert.equal(week.provenance.volume,'unavailable');
  const {EventEmitter}=require('node:events'),{installProviderTransport}=require(path.join(input.runtime,'provider-transport.cjs'));
  const batch=path.join(input.directory,'sdk-batch');await fs.mkdir(batch);
  await fs.writeFile(path.join(batch,'blocked.json'),JSON.stringify({message:'Earlier public HTTP 403 ETH-USD request'}));
  const session={directory:batch,tokenCache:path.join(input.directory,'unused-token.json'),rateLimit:true,sdk:{sourceKey:'a'.repeat(64)}};
  const sdkOutput=path.join(input.directory,'sdk-ratio.csv');await fs.writeFile(sdkOutput,'');
  const sdkRequest={...request,output:sdkOutput,providerSession:session,ownerPipe:true},lines=new EventEmitter(),sdkEvents=[];
  const originalFetch=global.fetch;let httpRequests=0;global.fetch=()=>{httpRequests++;throw Error('Packaged SDK probe sent HTTP');};
  const control=installProviderTransport(lines,sdkRequest,event=>{
    sdkEvents.push(event);
    queueMicrotask(async()=>{
      try{
        assert.equal(event.operation,'history');assert.ok(['btcusd','xauusd'].includes(event.symbol));
        const filename=path.join(batch,`export-${event.requestId}.csv`);let csv,rows;
        if(event.timeframe==='tick'){
          const bids=event.from===start?(event.symbol==='btcusd'?[10,20,30]:[2,4,5]):[];rows=bids.length;
          csv=['timestamp,askPrice,bidPrice,askVolume,bidVolume',...bids.map((bid,i)=>`${event.from+i*100},${bid+1},${bid},1,2`),''].join(newline);
        }else{
          assert.equal(event.timeframe,'d1');assert.equal(event.symbol,'btcusd');rows=0;
          csv=['timestamp,open,high,low,close',''].join(newline);
        }
        await fs.writeFile(filename,csv,{mode:384});lines.emit('line',JSON.stringify({type:'provider-reply',requestId:event.requestId,path:filename,rows}));
      }catch(error){lines.emit('line',JSON.stringify({type:'provider-reply',requestId:event.requestId,error:error.message}));}
    });
  });
  let sdkReport;
  try{
    const guard={...api,BufferFetcher:class{constructor(){throw Error('Packaged SDK probe constructed a public fetcher');}}};let ratio,native;
    await download(sdkRequest,{api:guard,emit:e=>{if(e.type==='done')ratio=e;}});
    assert.equal(await fs.readFile(sdkOutput,'utf8'),expected);assert.equal(ratio.provenance.historySource,'jforex-sdk');assert.equal(sdkEvents.length,48);
    const sdkCached=path.join(input.directory,'sdk-cached.csv');await fs.writeFile(sdkCached,'');
    await download({...sdkRequest,output:sdkCached},{api:guard,emit:()=>{}});assert.equal(sdkEvents.length,48);
    const sdkNative=path.join(input.directory,'sdk-native-btcusd.csv');await fs.writeFile(sdkNative,'');
    await download({...sdkRequest,symbol:'btcusd',output:sdkNative},{api:guard,emit:e=>{if(e.type==='done')native=e;}});
    assert.equal(await fs.readFile(sdkNative,'utf8'),['timestamp,open,high,low,close',`${start},10,30,10,30`,''].join(newline));
    assert.equal(native.provenance.historySource,'jforex-sdk');assert.equal(native.provenance.volume,'unavailable');assert.equal(httpRequests,0);
    assert.equal(native.provenance.kind,'native');assert.equal(native.provenance.approximate,false);assert.match(native.provenance.observationResolution,/derived from native BID ticks/);
    assert.equal(sdkEvents.length,49,'empty native daily history reuses all24already verified native BTCUSD tick hours');
    assert.equal((await fs.readdir(batch)).some(name=>name.startsWith('export-')),false);
    sdkReport={passed:true,historyRequests:sdkEvents.length,tickRequests:48,nativeDailyRequests:1,nativeEmptyDailyFallback:true,nativeTickCacheReuse:true,httpRequests,cachedReuse:true,priorHTTPDenialBypassed:true,ownedExportsRemoved:true};
  }finally{control.close();global.fetch=originalFetch;}
  process.stdout.write(JSON.stringify({passed:true,rows:daily.rows,weeklyRows:week.rows,localTransportFetches:fetches,cachedReuse:true,exactBidRatios:true,
    volumeUnavailable:true,priceEnrichment:true,approximationExplicit:true,originalPreserved:await fs.readFile(output,'utf8')===expected,sdkHistory:sdkReport,networkRequired:false}));
})().catch(error=>{console.error(error);process.exitCode=1;});
"""
    result = subprocess.run([str(node), "-e", script],
                            input=json.dumps({"runtime": str(runtime), "directory": str(directory)}) + "\n",
                            text=True, capture_output=True, cwd=directory, timeout=30, check=True)
    report = json.loads(result.stdout)
    if not report.get("passed") or report.get("rows") != 1 or report.get("weeklyRows") != 1:
        raise ValueError(f"Packaged synthetic pipeline did not confirm its result: {result.stdout[:2000]}")
    return report


def fast_synthetic_smoke(runtime, node, directory):
    """Exercise packaged H1 observation history through the real owner protocol."""
    script = r"""
const fs=require('node:fs/promises'),path=require('node:path'),assert=require('node:assert/strict');
const {EventEmitter}=require('node:events');
const input=JSON.parse(require('node:fs').readFileSync(0,'utf8'));
for(const module of ['synthetic-hourly.cjs','synthetic-worker.cjs','sdk-worker.cjs'])require(path.join(input.runtime,module));
const api=require(path.join(input.runtime,'node_modules','dukascopy-node'));
const {download}=require(path.join(input.runtime,'worker.cjs'));
const {enrich}=require(path.join(input.runtime,'enrich-worker.cjs'));
const {installProviderTransport}=require(path.join(input.runtime,'provider-transport.cjs'));
const start=Date.UTC(2024,0,1),hour=3600000,day=24*hour,sourceKey='b'.repeat(64),newline=String.fromCharCode(10);
const csv=rows=>['timestamp,open,high,low,close',...rows.map(row=>row.join(',')),''].join(newline);
const dailyRow=index=>[start+index*day,5+12*index,16.5+12*index,5+12*index,16.5+12*index];
(async()=>{
  const folder=path.join(input.directory,'fast-hourly-probe'),batch=path.join(folder,'sdk-batch');
  await fs.mkdir(batch,{recursive:true,mode:0o700});
  await fs.writeFile(path.join(batch,'blocked.json'),JSON.stringify({message:'Earlier public HTTP403 ETH-USD request'}));
  const session={directory:batch,tokenCache:path.join(folder,'unused-token.json'),rateLimit:true,sdk:{sourceKey}};
  const output=path.join(folder,'ratio-d1.csv');await fs.writeFile(output,'');
  const request={symbol:'btcxau',timeframe:'d1',syntheticSource:'h1-close',from:new Date(start).toISOString(),to:new Date(start+60*day).toISOString(),asOf:start+60*day,output,
    providerSession:session,ownerPipe:true,hourlyCacheDirectory:path.join(folder,'hourly')};
  let publicFetchers=0,httpRequests=0,nativeEmpty=true;
  const guard={...api,BufferFetcher:class{constructor(){publicFetchers++;throw Error('Fast packaged probe constructed a public/tick fetcher');}}};
  const lines=new EventEmitter(),events=[],originalFetch=global.fetch;
  global.fetch=()=>{httpRequests++;throw Error('Fast packaged probe attempted HTTP');};
  const transport=installProviderTransport(lines,request,event=>{
    events.push(event);
    queueMicrotask(async()=>{
      try{
        assert.equal(event.operation,'history');
        assert.ok(['btcusd','xauusd'].includes(event.symbol),'wrong literal ratio source');
        assert.ok(['h1','h1-native','d1'].includes(event.timeframe),'fast mode requested ticks');
        const filename=path.join(batch,`export-${event.requestId}.csv`),rows=[];
        let sessionPolicy;
        if(event.timeframe==='d1'){
          assert.equal(event.symbol,'btcusd');assert.equal(event.from,start);assert.equal(event.to,start+day);
          if(!nativeEmpty)rows.push([start,50,70,40,60]);
        }else{
          assert.equal(event.from%hour,0);assert.equal(event.to%hour,0);
          assert.ok(event.to>event.from&&event.to-event.from<=28*day,'H1 batch exceeded28days/672hourly rows');
          assert.ok(event.from>=start&&event.to<=request.asOf,'hourly source looked beyond the closed request');
          sessionPolicy=event.timeframe==='h1'?'verified-full-hour-v1':'provider-native-h1-v1';
          if(event.timeframe==='h1-native')assert.equal(event.symbol,'btcusd');
          for(let time=event.from;time<event.to;time+=hour){
            const close=event.symbol==='btcusd'?10+(time-start)/hour:2;
            // Deliberately extreme native highs/lows expose independent OHLC
            // division. Ratios must use only the paired observed H1 closes.
            rows.push([time,close-1,close+1000,close/1000,close]);
          }
          assert.ok(rows.length<=672);
        }
        await fs.writeFile(filename,csv(rows),{mode:0o600});
        lines.emit('line',JSON.stringify({type:'provider-reply',requestId:event.requestId,path:filename,rows:rows.length,...(sessionPolicy?{sessionPolicy}:{})}));
      }catch(error){lines.emit('line',JSON.stringify({type:'provider-reply',requestId:event.requestId,error:error.message}));}
    });
  });
  let report;
  try{
    let daily;await download(request,{api:guard,emit:event=>{if(event.type==='done')daily=event;}});
    const expected=csv(Array.from({length:60},(_,index)=>dailyRow(index)));
    assert.equal(await fs.readFile(output,'utf8'),expected);assert.equal(daily.rows,60);
    assert.equal(events.length,6,'sixtydays must require exactlythree28day-or-smaller batches per leg');
    for(const symbol of ['btcusd','xauusd']){
      const requests=events.filter(event=>event.symbol===symbol).sort((a,b)=>a.from-b.from);
      assert.deepEqual(requests.map(event=>[event.timeframe,event.from,event.to]),[
        ['h1',start,start+28*day],['h1',start+28*day,start+56*day],['h1',start+56*day,start+60*day],
      ]);
    }
    const sampled=value=>{
      assert.equal(value.syntheticSource,'h1-close');assert.equal(value.historySource,'jforex-sdk');
      assert.equal(value.maxQuoteAgeMs,hour);assert.equal(value.sessionPolicy,'verified-full-hour-close-v1');
      assert.equal(value.volume,'unavailable');assert.equal(value.approximate,true);assert.match(value.observationResolution,/H1/);
      assert.ok(!JSON.stringify(value).includes(sourceKey),'opaque account key leaked into public provenance');
    };
    sampled(daily.provenance);
    const repeated=path.join(folder,'ratio-repeated.csv');await fs.writeFile(repeated,'');
    await download({...request,output:repeated},{api:guard,emit:()=>{}});
    assert.equal(events.length,6,'compatible checksummed day cache made another SDK request');
    assert.equal(await fs.readFile(repeated,'utf8'),expected);
    const enriched=path.join(folder,'ratio-enriched.csv');
    await enrich({input:output,output:enriched,indicator:'sma',parameters:{period:3},source:'auto'},{emit:()=>{}});
    const enrichedRows=(await fs.readFile(enriched,'utf8')).trim().split(newline).map(row=>row.split(','));
    assert.deepEqual(enrichedRows[0],['timestamp','open','high','low','close','sma_3']);
    for(let index=0;index<60;index++){
      assert.deepEqual(enrichedRows[index+1].slice(0,5),dailyRow(index).map(String));
      assert.equal(enrichedRows[index+1][5],index<2?'':String(16.5+12*(index-1)));
    }
    // Derive eight complete Monday weeks using a production D1 download of
    // the56-day cached subset, so no partial current week can enter this probe.
    const complete=path.join(folder,'complete-d1.csv');await fs.writeFile(complete,'');let completeDaily;
    const completeRequest={...request,to:new Date(start+56*day).toISOString(),asOf:start+56*day,output:complete};
    await download(completeRequest,{api:guard,emit:event=>{if(event.type==='done')completeDaily=event;}});
    assert.equal(events.length,6);assert.equal(completeDaily.rows,56);sampled(completeDaily.provenance);
    const info=await fs.stat(complete),plan=path.join(folder,'daily-plan.json');
    await fs.writeFile(plan,JSON.stringify({version:1,asOf:completeRequest.asOf,inputs:[{input:complete,bytes:info.size,modified:Math.trunc(info.mtimeMs),metadata:{provenance:completeDaily.provenance,checksum:completeDaily.checksum}}]}));
    const weekly=path.join(folder,'ratio-w1.csv');await fs.writeFile(weekly,'');let week;
    await download({...completeRequest,timeframe:'w1',output:weekly,dailyPlanFile:plan},{api:guard,emit:event=>{if(event.type==='done')week=event;}});
    const weeklyExpected=csv(Array.from({length:8},(_,index)=>[start+7*index*day,5+84*index,88.5+84*index,5+84*index,88.5+84*index]));
    assert.equal(await fs.readFile(weekly,'utf8'),weeklyExpected);assert.equal(week.rows,8);sampled(week.provenance);
    const weekEnriched=path.join(folder,'ratio-w1-enriched.csv');
    await enrich({input:weekly,output:weekEnriched,indicator:'sma',parameters:{period:1},source:'auto',timeframe:'w1'},{emit:()=>{}});
    const weekRows=(await fs.readFile(weekEnriched,'utf8')).trim().split(newline).map(row=>row.split(','));
    assert.equal(weekRows.length,9);assert.equal(weekRows[0].at(-1),'sma_1');
    for(const row of weekRows.slice(1))assert.equal(row.at(-1),row[4]);
    assert.equal(events.length,6,'weekly derivation/enrichment requested provider history');
    // BTCUSD first tries genuine native D1; a verified empty response alone
    // enables its same-asset native H1 OHLC fallback, under a separate policy.
    const native=path.join(folder,'native-btcusd.csv');await fs.writeFile(native,'');let nativeDone;
    const nativeRequest={...request,symbol:'btcusd',to:new Date(start+day).toISOString(),asOf:start+day,output:native};
    await download(nativeRequest,{api:guard,emit:event=>{if(event.type==='done')nativeDone=event;}});
    assert.equal(await fs.readFile(native,'utf8'),csv([[start,9,1033,0.01,33]]));
    assert.deepEqual(events.slice(6).map(event=>event.timeframe),['d1','h1-native']);
    assert.equal(nativeDone.provenance.kind,'native');assert.equal(nativeDone.provenance.approximate,false);
    assert.equal(nativeDone.provenance.syntheticSource,'h1-close');assert.match(nativeDone.provenance.observationResolution,/native H1 OHLC/);
    nativeEmpty=false;const preferred=path.join(folder,'native-preferred.csv');await fs.writeFile(preferred,'');
    await download({...nativeRequest,output:preferred},{api:guard,emit:()=>{}});
    assert.equal(await fs.readFile(preferred,'utf8'),csv([[start,50,70,40,60]]));
    assert.deepEqual(events.slice(8).map(event=>event.timeframe),['d1'],'available native daily OHLC did not remain preferred');
    assert.equal(httpRequests,0);assert.equal(publicFetchers,0);
    assert.equal(events.filter(event=>event.timeframe==='tick').length,0);
    assert.equal((await fs.readdir(batch)).some(name=>name.startsWith('export-')),false);
    assert.equal(await fs.readFile(output,'utf8'),expected);assert.equal(await fs.readFile(weekly,'utf8'),weeklyExpected);
    report={passed:true,ownerPipe:true,dailyRows:60,completeWeeklyRows:8,initialHourlyRequests:6,maxBatchDays:28,maxBatchRows:672,repeatHistoryRequests:0,
      tickRequests:0,httpRequests,publicFetchers,pairedCloseOHLC:true,nativeOHLCFallback:true,nativeD1Preferred:true,priceEnrichment:true,
      samplingProvenanceRetained:true,separateNativeSessionPolicy:true,priorHTTPDenialBypassed:true,ownedExportsRemoved:true,originalsPreserved:true,networkRequired:false};
  }finally{transport.close();global.fetch=originalFetch;}
  process.stdout.write(JSON.stringify(report));
})().catch(error=>{console.error(error);process.exitCode=1;});
"""
    result = subprocess.run([str(node), "-e", script],
                            input=json.dumps({"runtime": str(runtime), "directory": str(directory)}) + "\n",
                            text=True, capture_output=True, cwd=directory, timeout=45, check=True)
    report = json.loads(result.stdout)
    if (not report.get("passed") or report.get("dailyRows") != 60
            or report.get("completeWeeklyRows") != 8 or report.get("initialHourlyRequests") != 6
            or report.get("tickRequests") != 0 or report.get("repeatHistoryRequests") != 0):
        raise ValueError(f"Packaged fast synthetic pipeline did not confirm its result: {result.stdout[:2000]}")
    return report


def smoke(runtime):
    runtime = runtime.resolve()
    node = runtime / ("node.exe" if os.name == "nt" else "node")
    worker = runtime / "enrich-worker.cjs"
    with tempfile.TemporaryDirectory(prefix="CryptoDesk packaged enrichment ") as temporary:
        directory = Path(temporary)
        source, output = directory / "original.csv", directory / "enriched.csv"
        original = ("timestamp,open,high,low,close,volume\n"
                    "1704067200000,1,2,0,1,3\n1704067260000,2,3,1,2,4\n"
                    "1704067320000,3,4,2,3,5\n1704067380000,4,5,3,4,6\n")
        source.write_bytes(original.encode())
        request = {"input": str(source), "output": str(output), "indicator": "sma", "parameters": {"period": 3}, "source": "auto"}
        result = subprocess.run([str(node), str(worker)], input=json.dumps(request) + "\n", text=True,
                                capture_output=True, cwd=directory, timeout=20, check=True)
        events = [json.loads(line) for line in result.stdout.splitlines()]
        if not events or events[-1].get("type") != "done" or events[-1].get("rows") != 4 or events[-1].get("columns") != ["sma_3"]:
            raise ValueError(f"Packaged enrichment did not confirm its result: {result.stdout[:4000]}")
        with output.open(newline="") as stream:
            rows = list(csv.reader(stream))
        originals = list(csv.reader(io.StringIO(original)))
        if len(rows) != 5 or rows[0] != originals[0] + ["sma_3"] or [row[-1] for row in rows[1:]] != ["", "", "2", "3"]:
            raise ValueError("Packaged enrichment calculated incorrect columns, row alignment or values")
        if source.read_bytes() != original.encode() or any(row[:-1] != old for row, old in zip(rows[1:], originals[1:])):
            raise ValueError("Packaged enrichment changed original data fields")
        refresh = refresh_smoke(runtime, node, directory, output)
        calendar = calendar_smoke(runtime, node, directory)
        ranges = day_week_smoke(runtime, node, directory)
        if source.read_bytes() != original.encode():
            raise ValueError("Packaged Refresh changed the original raw CSV")
        incremental = incremental_smoke(runtime, node, directory, output)
        provider = provider_smoke(runtime, node, directory)
        synthetic = synthetic_smoke(runtime, node, directory)
        synthetic["fastHourly"] = fast_synthetic_smoke(runtime, node, directory)
        return {"passed": True, "runtime": str(runtime), "columns": ["sma_3"], "rows": 4,
                "warmupBlank": True, "exactValues": True, "originalFieldsPreserved": True,
                "privateNode": str(node), "networkRequired": False, "refresh": refresh, "calendar": calendar,
                "incremental": incremental, "providerSession": provider, "dayWeekRanges": ranges,
                "syntheticInstruments": synthetic}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("runtime", type=Path)
    parser.add_argument("--report", type=Path)
    args = parser.parse_args()
    try:
        report = smoke(args.runtime)
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        detail = error.stderr[:4000] if isinstance(error, subprocess.CalledProcessError) and error.stderr else str(error)
        parser.exit(1, f"Packaged enrichment/Refresh smoke failed: {detail}\n")
    encoded = json.dumps(report, indent=2) + "\n"
    if args.report:
        args.report.parent.mkdir(parents=True, exist_ok=True)
        args.report.write_text(encoded)
    print(encoded, end="")


if __name__ == "__main__":
    main()

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
        if source.read_bytes() != original.encode():
            raise ValueError("Packaged Refresh changed the original raw CSV")
        incremental = incremental_smoke(runtime, node, directory, output)
        provider = provider_smoke(runtime, node, directory)
        return {"passed": True, "runtime": str(runtime), "columns": ["sma_3"], "rows": 4,
                "warmupBlank": True, "exactValues": True, "originalFieldsPreserved": True,
                "privateNode": str(node), "networkRequired": False, "refresh": refresh, "calendar": calendar,
                "incremental": incremental, "providerSession": provider}


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

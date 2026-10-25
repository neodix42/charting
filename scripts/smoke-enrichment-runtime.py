#!/usr/bin/env python3
"""Exercise packaged enrichment and Refresh with private Node and local data only."""
import argparse
import csv
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile


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
        if source.read_bytes() != original.encode():
            raise ValueError("Packaged Refresh changed the original raw CSV")
        return {"passed": True, "runtime": str(runtime), "columns": ["sma_3"], "rows": 4,
                "warmupBlank": True, "exactValues": True, "originalFieldsPreserved": True,
                "privateNode": str(node), "networkRequired": False, "refresh": refresh}


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

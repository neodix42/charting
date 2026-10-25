#!/usr/bin/env python3
"""Exercise the packaged Node, enrichment entry point and indicator library offline."""
import argparse
import csv
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile


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
        return {"passed": True, "runtime": str(runtime), "columns": ["sma_3"], "rows": 4,
                "warmupBlank": True, "exactValues": True, "originalFieldsPreserved": True,
                "privateNode": str(node), "networkRequired": False}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("runtime", type=Path)
    parser.add_argument("--report", type=Path)
    args = parser.parse_args()
    try:
        report = smoke(args.runtime)
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        detail = error.stderr[:4000] if isinstance(error, subprocess.CalledProcessError) and error.stderr else str(error)
        parser.exit(1, f"Packaged enrichment smoke failed: {detail}\n")
    encoded = json.dumps(report, indent=2) + "\n"
    if args.report:
        args.report.parent.mkdir(parents=True, exist_ok=True)
        args.report.write_text(encoded)
    print(encoded, end="")


if __name__ == "__main__":
    main()

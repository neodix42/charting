#!/usr/bin/env python3
"""Read-only instruction-bundle checks. This does not test the future desktop app."""
from __future__ import annotations
import copy
import hashlib
import json
import math
import re
import sys
import uuid
from collections import defaultdict
from decimal import Decimal
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

def require(condition: bool, message: str) -> None:
    if not condition:
        raise ValueError(message)

def read_json(path: Path):
    def bad_constant(value):
        raise ValueError(f"Invalid JSON constant: {value}")
    return json.loads(path.read_text(encoding="utf-8"), parse_constant=bad_constant)

def workspace_checks(w: dict) -> None:
    """Important semantic invariants; full JSON Schema check is optional below."""
    require(w['schemaVersion'] == 1 and w['app'] == 'cryptodesk', 'Unsupported workspace')
    require(1 <= len(w['tabs']) <= 20, 'Tab count must be 1..20')
    require(w['activeTabId'] in [t['id'] for t in w['tabs']], 'Invalid active tab')
    ids = set()
    def add_id(value):
        uuid.UUID(value)
        require(value not in ids, 'Duplicate UUID')
        ids.add(value)
    add_id(w['id'])
    for t in w['tabs']:
        add_id(t['id'])
        require(isinstance(t['name'], str) and 0 < len(t['name'].strip()) <= 80, 'Invalid tab name')
        require(1 <= len(t['charts']) <= 9, 'Chart count must be 1..9')
        g = t['grid']; rows, cols = g['rows'], g['columns']
        require((rows, cols) in {(1,1),(1,2),(2,1),(2,2),(2,3),(3,2),(3,3)}, 'Invalid grid')
        require(len(t['charts']) <= rows*cols, 'Grid too small for actual charts')
        for key, size in [('rowFractions', rows), ('columnFractions', cols)]:
            values = g[key]
            require(len(values) == size, 'Incorrect track count')
            require(all(type(x) in (int,float) and math.isfinite(x) and 0 < x <= 1 for x in values), 'Invalid track weights')
            require(abs(sum(values)-1) <= 1e-6, 'Track weights must sum to 1')
        for c in t['charts']:
            add_id(c['id'])
            require(c['exchange'] in ('binance','bybit','coinbase'), 'Invalid exchange')
            require(c['market'] == 'spot', 'Only spot is allowed')
            require(c['timeframe'] in ('1m','5m','15m','1h','4h','1d'), 'Invalid timeframe')
            require(isinstance(c['instrument'],str) and 0 < len(c['instrument'].strip()) <= 128, 'Invalid instrument')
            require(c['chartType'] in ('candlestick','line'), 'Invalid chart type')
            v = c['view']
            require(v['mode'] in ('live','historical'), 'Invalid viewport mode')
            if v['mode'] == 'historical':
                require(type(v['fromSec']) is int and type(v['toSec']) is int and 0 <= v['fromSec'] < v['toSec'], 'Invalid historical range')
            p = c['priceScale']; pr = p['manualRange']
            require(p['mode'] in ('linear','log'), 'Invalid price mode')
            if pr is not None:
                require(0 < pr['min'] < pr['max'] and all(math.isfinite(x) for x in pr.values()), 'Invalid manual price range')
            require(p['autoScale'] or pr is not None, 'Manual price scale lacks range')

def aggregate(events: list[dict], duration: int) -> list[dict]:
    """Independent Decimal arithmetic to sanity-check expected synthetic fixture values."""
    seen = set(); buckets = defaultdict(list)
    for e in events:
        if e['id'] in seen:
            continue
        seen.add(e['id'])
        buckets[e['timeSec']//duration*duration].append(e)
    out = []
    for start, trades in sorted(buckets.items()):
        trades.sort(key=lambda e: (e['timeSec'], int(e['id'])))
        prices = [Decimal(e['price']) for e in trades]
        out.append({'startUnixSec':start,'open':prices[0],'high':max(prices),'low':min(prices),
                    'close':prices[-1],'baseVolume':sum((Decimal(e['size']) for e in trades), Decimal(0))})
    return out

def compare_bars(actual: list[dict], expected: list[dict]) -> None:
    require(len(actual) == len(expected), 'Incorrect expected candle count')
    for a,e in zip(actual,expected):
        require(a['startUnixSec'] == e['startUnixSec'], 'Candle start mismatch')
        for key in ('open','high','low','close','baseVolume'):
            require(Decimal(a[key]) == Decimal(e[key]), f'Fixture {key} mismatch')

def main() -> int:
    print('CryptoDesk instruction-bundle verifier (not an application test)')
    manifest = read_json(ROOT/'HASHES.json')
    for relative, expected_hash in manifest['files'].items():
        p = (ROOT/relative).resolve()
        require(p.is_relative_to(ROOT.resolve()), 'Unsafe manifest path')
        require(p.is_file(), f'Missing file: {relative}')
        actual_hash = hashlib.sha256(p.read_bytes()).hexdigest()
        require(actual_hash == expected_hash, f'Changed file: {relative}')
    print(f"PASS: {len(manifest['files'])} file checksums")
    names = ['starter-one-tab-one-chart.json','intermediate-two-tabs.json','capacity-20-tabs-180-charts.json']
    examples = [read_json(ROOT/'examples'/name) for name in names]
    for w in examples:
        workspace_checks(w)
    require([len(t['charts']) for t in examples[0]['tabs']] == [1], 'Starter is not exactly 1/1')
    require((examples[0]['tabs'][0]['grid']['rows'],examples[0]['tabs'][0]['grid']['columns']) == (1,1), 'Starter grid is not 1x1')
    require([len(t['charts']) for t in examples[1]['tabs']] == [3,1], 'Intermediate counts changed')
    require([len(t['charts']) for t in examples[2]['tabs']] == [9]*20, 'Capacity fixture must be 20x9')
    print('PASS: starter 1/1, intermediate 2 tabs (3+1 charts), capacity-only 20/180')
    invalid = []
    w = copy.deepcopy(examples[0]); w['tabs'] = []; invalid.append(w)
    w = copy.deepcopy(examples[2]); w['tabs'].append(copy.deepcopy(w['tabs'][0])); invalid.append(w)
    w = copy.deepcopy(examples[0]); w['tabs'][0]['charts'] = []; invalid.append(w)
    w = copy.deepcopy(examples[2]); w['tabs'][0]['charts'].append(copy.deepcopy(w['tabs'][0]['charts'][0])); invalid.append(w)
    w = copy.deepcopy(examples[1]); w['tabs'][0]['charts'][1]['id'] = w['tabs'][0]['charts'][0]['id']; invalid.append(w)
    w = copy.deepcopy(examples[1]); w['tabs'][0]['grid']['rows'] = 1; invalid.append(w)
    w = copy.deepcopy(examples[0]); w['activeTabId'] = str(uuid.uuid4()); invalid.append(w)
    for w in invalid:
        try:
            workspace_checks(w)
        except (ValueError, KeyError, TypeError):
            pass
        else:
            raise ValueError('An invalid workspace passed the verifier')
    print(f'PASS: {len(invalid)} malformed workspace variants rejected by semantic checks')
    try:
        import jsonschema
    except ImportError:
        print('NOT RUN: full Draft 2020-12 validation (optional jsonschema not installed); semantic checks passed')
    else:
        s = read_json(ROOT/'schema/workspace.schema.json')
        jsonschema.Draft202012Validator.check_schema(s)
        validator = jsonschema.Draft202012Validator(s, format_checker=jsonschema.FormatChecker())
        for w in examples:
            validator.validate(w)
        print('PASS: full Draft 2020-12 validation of all three examples')
    f = read_json(ROOT/'fixtures/trades.json')
    compare_bars(aggregate(f['events'],60), f['expectedBeforeLate'])
    compare_bars(aggregate(f['events']+[f['lateEvent']],60), f['expectedAfterLate'])
    compare_bars(aggregate(f['events']+[f['lateEvent']],300), [f['expectedFiveMinuteAfterLate']])
    f = read_json(ROOT/'fixtures/native-candles.json'); by_start = {}
    for e in f['events']:
        old = by_start.get(e['startUnixSec'])
        if old is None or e['sourceRevision'] >= old['sourceRevision']:
            by_start[e['startUnixSec']] = e
    require([by_start[t] for t in sorted(by_start)] == f['expected'], 'Native candle expected values differ')
    f = read_json(ROOT/'fixtures/history-pages.json')
    merged = {c['startUnixSec']:c for page in f['pages'] for c in page}
    require([merged[t] for t in sorted(merged)] == f['expected'], 'History expected values differ')
    print('PASS: Decimal trade, cumulative-candle and history fixture expectations')
    for p in ROOT.rglob('*.md'):
        for target in re.findall(r'\[[^\]]*\]\(([^)]+)\)', p.read_text(encoding='utf-8')):
            if '://' in target or target.startswith('#'):
                continue
            local_target = target.split('#',1)[0]
            require((p.parent/local_target).exists(), f'Broken relative link in {p.name}: {target}')
    print('PASS: local Markdown links')
    print('Bundle checks complete. Go app builds, GUI behavior and live exchange tests are NOT asserted.')
    return 0

if __name__ == '__main__':
    try:
        sys.exit(main())
    except Exception as exc:
        print(f'FAIL: {exc}', file=sys.stderr)
        sys.exit(1)

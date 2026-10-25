#!/usr/bin/env bash
# Real desktop Refresh -> applied indicators -> visual rules -> match chart -> restart.
set -euo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/common.sh"
require_node
require_native
evidence="${CRYPTODESK_SCREENER_EVIDENCE:-$APP_ROOT/docs/verification/screener-analysis-refresh/desktop}"
mkdir -p "$evidence" build/bin
evidence="$(cd "$evidence" && pwd)"
profile="$(mktemp -d "${TMPDIR:-/tmp}/cryptodesk-analysis-refresh.XXXXXX")"
trap 'rm -rf -- "$profile"' EXIT
npm --prefix frontend run build
go build -tags 'production desktop_smoke webkit2_41' -o build/bin/cryptodesk-analysis-refresh-smoke .
for phase in analysis-refresh analysis-refresh-restore; do
 CRYPTODESK_SMOKE_PHASE="$phase" CRYPTODESK_SMOKE_REPORT="$evidence/$phase.json" \
  timeout 240s build/bin/cryptodesk-analysis-refresh-smoke --data-dir "$profile" > "$evidence/$phase.log" 2>&1
 python3 - "$profile" "$evidence" "$phase" <<'PY'
import hashlib,json,pathlib,sys
profile,evidence,phase=pathlib.Path(sys.argv[1]),pathlib.Path(sys.argv[2]),sys.argv[3]
report=json.loads((evidence/f'{phase}.json').read_text())
if not report['passed']: raise SystemExit(report.get('error','Native refresh/analysis failed'))
data=report['analysisRefresh']
if phase=='analysis-refresh':
    for symbol,csv in data['csv'].items():
        assert len(csv.splitlines())>len(data['originals'][symbol].splitlines())
    recipes=list((profile/'screener'/'.cryptodesk-screener-metadata').glob('*.json'))
    assert len(recipes)==2
    for path in recipes:
        metadata=json.loads(path.read_text());assert any(recipe['indicator']=='sma' and recipe['parameters']['period']==2 for recipe in metadata['recipes'])
else:
    initial=json.loads((evidence/'analysis-refresh.json').read_text())['analysisRefresh']
    assert data['csv']==initial['csv']
    assert data['view']==initial['view']
    assert data['named']==initial['named']
    assert data['result']['totalMatches']==initial['analysis']['totalMatches']
    (evidence/'restart-comparison.json').write_text(json.dumps({'passed':True,'refreshedCSVsUnchanged':True,'rulesRestored':True,'allMatchCountsPreserved':True,'sha256':{symbol:hashlib.sha256(csv.encode()).hexdigest() for symbol,csv in data['csv'].items()}},indent=2)+'\n')
print(f"{phase}: PASS ({len(report['checks'])} native assertions)")
PY
done

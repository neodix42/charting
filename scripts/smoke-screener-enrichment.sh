#!/usr/bin/env bash
# Real native controls -> public data -> price/calendar indicators -> CSV -> restart.
set -euo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/common.sh"
require_node
require_native
evidence="${CRYPTODESK_SCREENER_EVIDENCE:-$APP_ROOT/docs/verification/screener-enrichment/desktop}"
mkdir -p "$evidence" build/bin
evidence="$(cd "$evidence" && pwd)"
profile="$(mktemp -d "${TMPDIR:-/tmp}/cryptodesk-enrichment-native.XXXXXX")"
trap 'rm -rf -- "$profile"' EXIT
npm --prefix frontend run build
go build -tags 'production desktop_smoke webkit2_41' -o build/bin/cryptodesk-enrichment-smoke .
for phase in enrichment enrichment-restore; do
 CRYPTODESK_SMOKE_PHASE="$phase" CRYPTODESK_SMOKE_REPORT="$evidence/$phase.json" \
  timeout 120s build/bin/cryptodesk-enrichment-smoke --data-dir "$profile" > "$evidence/$phase.log" 2>&1
 python3 - "$profile" "$evidence" "$phase" <<'PY'
import hashlib,json,pathlib,sys
profile,evidence,phase=pathlib.Path(sys.argv[1]),pathlib.Path(sys.argv[2]),sys.argv[3]
report=json.loads((evidence/f'{phase}.json').read_text())
if not report['passed']: raise SystemExit(report.get('error','Native enrichment failed'))
if phase=='enrichment':
    data=report['enrichment']; backups=pathlib.Path(data['directory'])/'.cryptodesk-enrichment-backups'
    metadata=list(backups.glob('*.json')); assert len(metadata)==8
    for path in metadata:
        saved=json.loads(path.read_text()); symbol=saved['filename'].split('-')[0]
        original=data[{'sma':'originals','tenkan':'smaCSV','weekday':'tenkanCSV','hour':'weekdayCSV'}[saved['request']['indicator']]][symbol]
        assert path.with_suffix('.csv').read_text()==original
    assert not any(path.name.startswith('.cryptodesk-enrichment-') and path.name!='.cryptodesk-enrichment-backups' for path in pathlib.Path(data['directory']).iterdir())
    (evidence/'backups.json').write_text(json.dumps({'passed':True,'exactOriginalBackups':8,'ownedScratchRemoved':True},indent=2)+'\n')
else:
    initial=json.loads((evidence/'enrichment.json').read_text())['enrichment']['finalCSV']
    assert report['enrichment']['csv']==initial
    (evidence/'restart-comparison.json').write_text(json.dumps({'passed':True,'enrichedFilesUnchanged':True,'sha256':{symbol:hashlib.sha256(csv.encode()).hexdigest() for symbol,csv in initial.items()}},indent=2)+'\n')
print(f"{phase}: PASS ({len(report['checks'])} native assertions)")
PY
done

#!/usr/bin/env bash
# Actual desktop shutdown/restart while two live minute downloads are incomplete.
set -euo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/common.sh"
require_node
require_native
evidence="${CRYPTODESK_SCREENER_EVIDENCE:-$APP_ROOT/docs/verification/screener-parallel/desktop-resume}"
mkdir -p "$evidence" build/bin
evidence="$(cd "$evidence" && pwd)"
profile="$(mktemp -d "${TMPDIR:-/tmp}/cryptodesk-screener-resume.XXXXXX")"
trap 'rm -rf -- "$profile"' EXIT
npm --prefix frontend run build
go build -tags 'production desktop_smoke webkit2_41' -o build/bin/cryptodesk-screener-smoke .
for phase in screener-resume screener-resume-restore; do
 CRYPTODESK_SMOKE_PHASE="$phase" CRYPTODESK_SMOKE_REPORT="$evidence/$phase.json" \
  timeout 110s build/bin/cryptodesk-screener-smoke --data-dir "$profile" > "$evidence/$phase.log" 2>&1
 python3 - "$profile" "$evidence" "$phase" <<'PY'
import hashlib,json,pathlib,sys
profile,evidence,phase=pathlib.Path(sys.argv[1]),pathlib.Path(sys.argv[2]),sys.argv[3]
report=json.loads((evidence/f'{phase}.json').read_text())
if not report['passed']: raise SystemExit(report.get('error','Native resume failed'))
queue=json.loads((profile/'screener-downloads.json').read_text())
if phase=='screener-resume':
    assert [job['status'] for job in queue['jobs']]==['queued','queued'],queue
    prefixes={}
    for job in queue['jobs']:
        folder=pathlib.Path(queue['directory'])/f".cryptodesk-screener-job-{job['id']}"
        checkpoint=json.loads((folder/'checkpoint.json').read_text())
        data=(folder/'data.part').read_bytes()[:checkpoint['bytes']]
        prefixes[job['symbol']]={'id':job['id'],'bytes':len(data),'sha256':hashlib.sha256(data).hexdigest(),'checkpoint':checkpoint}
    (evidence/'paused-prefixes.json').write_text(json.dumps(prefixes,indent=2)+'\n')
else:
    prefixes=json.loads((evidence/'paused-prefixes.json').read_text())
    for job in queue['jobs']:
        saved=prefixes[job['symbol']]
        assert job['status']=='completed' and job['id']==saved['id'],job
        with pathlib.Path(job['outputPath']).open('rb') as stream: data=stream.read(saved['bytes'])
        assert hashlib.sha256(data).hexdigest()==saved['sha256'],job
        assert len(list(pathlib.Path(queue['directory']).glob(f"{job['symbol']}-*.csv")))==1
    (evidence/'restart-comparison.json').write_text(json.dumps({'passed':True,'sameJobIDs':True,'committedPrefixesPreserved':True,'noDuplicateCSVs':True},indent=2)+'\n')
print(f"{phase}: PASS ({len(report['checks'])} native assertions)")
PY
done

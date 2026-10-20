#!/usr/bin/env bash
# Drive the production React bundle in Wails/WebKit with public market data.
# The only tagged hooks automate UI interaction and report verification evidence.
set -euo pipefail
repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
source "$repo_root/scripts/common.sh"
if [[ -z "${DISPLAY:-}" && -z "${WAYLAND_DISPLAY:-}" ]]; then
  echo 'Market desktop smoke blocked: DISPLAY and WAYLAND_DISPLAY are absent.' >&2
  exit 2
fi
require_native
require_node
CRYPTODESK_VERSION="$(python3 "$repo_root/scripts/app_version.py")"
export CRYPTODESK_VERSION
artifact_dir="${CRYPTODESK_MARKETS_EVIDENCE:-$repo_root/docs/verification/perpetual-native-$(date -u +%Y%m%dT%H%M%SZ)}"
mkdir -p "$artifact_dir" "$repo_root/build/bin"
artifact_dir="$(cd "$artifact_dir" && pwd)"
workspace_dir="$(mktemp -d "${TMPDIR:-/tmp}/cryptodesk-markets-smoke.XXXXXX")"
trap 'rm -rf -- "$workspace_dir"' EXIT
npm --prefix "$repo_root/frontend" run build
python3 "$repo_root/scripts/app_version.py" --verify-frontend > /dev/null
go build -tags 'production desktop_smoke webkit2_41' -o "$repo_root/build/bin/cryptodesk-markets-smoke" .
result=0
for phase in markets markets-restore; do
  expected_workspace=''
  expected_chart=''
  expected_exchange=''
  expected_market=''
  expected_instrument=''
  if [[ "$phase" == markets-restore ]]; then
    if ! python3 - "$artifact_dir/markets.json" <<'PY'
import json, pathlib, sys
report = json.loads(pathlib.Path(sys.argv[1]).read_text())
assert report.get('savedSelection', {}).get('market') == 'perpetual', 'Initial market smoke did not save a final perpetual selection'
PY
    then
      echo 'markets-restore: BLOCKED because no saved perpetual selection was reported.' >&2
      result=1
      continue
    fi
    mapfile -t expected_values < <(python3 - "$artifact_dir/markets.json" <<'PY'
import json, pathlib, sys
selection = json.loads(pathlib.Path(sys.argv[1]).read_text())['savedSelection']
for key in ('workspaceId', 'chartId', 'exchange', 'market', 'instrument'):
    value = selection[key]
    assert isinstance(value, str) and '\n' not in value
    print(value)
PY
    )
    expected_workspace="${expected_values[0]}"
    expected_chart="${expected_values[1]}"
    expected_exchange="${expected_values[2]}"
    expected_market="${expected_values[3]}"
    expected_instrument="${expected_values[4]}"
  fi
  phase_status=0
  CRYPTODESK_SMOKE_PHASE="$phase" \
    CRYPTODESK_SMOKE_REPORT="$artifact_dir/$phase.json" \
    CRYPTODESK_SMOKE_EXPECT_WORKSPACE="$expected_workspace" \
    CRYPTODESK_SMOKE_EXPECT_CHART="$expected_chart" \
    CRYPTODESK_SMOKE_EXPECT_EXCHANGE="$expected_exchange" \
    CRYPTODESK_SMOKE_EXPECT_MARKET="$expected_market" \
    CRYPTODESK_SMOKE_EXPECT_INSTRUMENT="$expected_instrument" \
    timeout --kill-after=15s 220s "$repo_root/build/bin/cryptodesk-markets-smoke" --data-dir "$workspace_dir" >"$artifact_dir/$phase.log" 2>&1 || phase_status=$?
  if [[ "$phase_status" != 0 ]]; then
    echo "$phase: native process failed or timed out (exit $phase_status); see $artifact_dir/$phase.log" >&2
    result=1
  fi
  if ! python3 - "$artifact_dir/$phase.json" <<'PY'
import json, pathlib, sys
path = pathlib.Path(sys.argv[1])
if not path.is_file():
    raise SystemExit(f'FAIL: native desktop report was not written: {path}')
report = json.loads(path.read_text())
print(f"{report['phase']}: {'PASS' if report['passed'] else 'FAIL'} ({len(report['checks'])} actual desktop assertions; {len(report.get('marketSelections', []))} real quote selections)")
if not report['passed']:
    print(report.get('error', 'A required public market check failed'), file=sys.stderr)
    for entry in report.get('marketSelections', []):
        if not entry['passed']:
            print(f"{entry['label']}: {entry.get('error')} — {entry.get('status')}", file=sys.stderr)
    raise SystemExit(1)
PY
  then
    result=1
  fi
done
printf 'Market desktop evidence: %s\n' "$artifact_dir"
exit "$result"

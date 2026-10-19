#!/usr/bin/env bash
# Run the embedded production React bundle in the real Wails/WebKit window.
# Public feeds remain enabled; only UI driving/report writing is test-tagged.
set -euo pipefail
repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
source scripts/common.sh
if [[ -z "${DISPLAY:-}" && -z "${WAYLAND_DISPLAY:-}" ]]; then
 echo 'Desktop smoke blocked: DISPLAY and WAYLAND_DISPLAY are absent.' >&2
 exit 2
fi
require_node
artifact_dir="${CRYPTODESK_DESKTOP_EVIDENCE:-$repo_root/docs/verification/desktop-$(date -u +%Y%m%dT%H%M%SZ)}"
mkdir -p "$artifact_dir" build/bin
artifact_dir="$(cd "$artifact_dir" && pwd)"
workspace_dir="$(mktemp -d "${TMPDIR:-/tmp}/cryptodesk-desktop-smoke.XXXXXX")"
trap 'rm -rf -- "$workspace_dir"' EXIT
npm --prefix frontend run build
go build -tags 'production desktop_smoke webkit2_41' -o build/bin/cryptodesk-smoke .
for phase in fresh restore capacity; do
 if [[ "$phase" == capacity ]]; then
  rm -rf -- "$workspace_dir"
  workspace_dir="$(mktemp -d "${TMPDIR:-/tmp}/cryptodesk-desktop-capacity.XXXXXX")"
 fi
 CRYPTODESK_SMOKE_IMPORT="$repo_root/examples/capacity-20-tabs-180-charts.json" CRYPTODESK_SMOKE_PHASE="$phase" CRYPTODESK_SMOKE_REPORT="$artifact_dir/$phase.json" timeout 150s build/bin/cryptodesk-smoke --data-dir "$workspace_dir" >"$artifact_dir/$phase.log" 2>&1
 python3 - "$artifact_dir/$phase.json" <<'PY'
import json, pathlib, sys
path = pathlib.Path(sys.argv[1])
report = json.loads(path.read_text())
print(f"{report['phase']}: {'PASS' if report['passed'] else 'FAIL'} ({len(report['checks'])} actual desktop assertions)")
if not report['passed']:
    print(report.get('error', 'Unknown failure'), file=sys.stderr)
    raise SystemExit(1)
PY
done
printf 'Desktop evidence: %s\n' "$artifact_dir"

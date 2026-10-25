#!/usr/bin/env bash
# Optional live check: actual Wails input -> Go queue -> bundled Node -> CSV.
set -euo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/common.sh"
if [[ -z "${DISPLAY:-}" && -z "${WAYLAND_DISPLAY:-}" ]]; then
  echo 'Native Screener smoke requires a desktop display.' >&2
  exit 2
fi
require_node
require_native
evidence="${CRYPTODESK_SCREENER_EVIDENCE:-$APP_ROOT/docs/verification/screener/desktop-live}"
mkdir -p "$evidence" "$APP_ROOT/build/bin"
evidence="$(cd "$evidence" && pwd)"
profile="$(mktemp -d "${TMPDIR:-/tmp}/cryptodesk-screener-native.XXXXXX")"
trap 'rm -rf -- "$profile"' EXIT
npm --prefix frontend run build
go build -tags 'production desktop_smoke webkit2_41' -o build/bin/cryptodesk-screener-smoke .
CRYPTODESK_SMOKE_PHASE=screener CRYPTODESK_SMOKE_REPORT="$evidence/screener.json" \
  timeout 110s build/bin/cryptodesk-screener-smoke --data-dir "$profile" > "$evidence/screener.log" 2>&1
python3 - "$evidence/screener.json" <<'PY'
import json, pathlib, sys
report = json.loads(pathlib.Path(sys.argv[1]).read_text())
print(f"Native live Screener: {'PASS' if report['passed'] else 'FAIL'} ({len(report['checks'])} assertions)")
if not report['passed']:
    raise SystemExit(report.get('error', 'Native Screener verification failed'))
PY

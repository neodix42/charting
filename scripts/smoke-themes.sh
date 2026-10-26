#!/usr/bin/env bash
# Open the actual application dropdowns in Wails/WebKit, then restart the same
# isolated profile to verify that the selected palette survives an acknowledged save.
set -euo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/common.sh"
if [[ -z "${DISPLAY:-}" ]]; then
  echo 'Theme desktop smoke blocked: DISPLAY is absent (native X11 capture required).' >&2
  exit 2
fi
for command in xwd xwininfo ffmpeg python3; do
  if ! command -v "$command" >/dev/null; then
    echo "Theme desktop smoke blocked: $command is required for native window screenshots." >&2
    exit 2
  fi
done
require_node
require_native
evidence="${CRYPTODESK_THEMES_EVIDENCE:-$APP_ROOT/.tools/verification/themes/desktop}"
mkdir -p "$evidence" "$APP_ROOT/build/bin"
evidence="$(cd "$evidence" && pwd)"
profile="$(mktemp -d "${TMPDIR:-/tmp}/cryptodesk-themes-native.XXXXXX")"
trap 'rm -rf -- "$profile"' EXIT
binary="${CRYPTODESK_THEMES_SMOKE_BINARY:-$APP_ROOT/build/bin/cryptodesk-themes-smoke}"
if [[ -z "${CRYPTODESK_THEMES_SMOKE_BINARY:-}" ]]; then
  npm --prefix frontend run build
  go build -tags 'production desktop_smoke webkit2_41' -o "$binary" .
fi
for phase in themes themes-restore; do
  # Per-process X11 selects the capturable backend without changing the user's
  # GTK theme, desktop settings, ordinary app profile or running app instances.
  GDK_BACKEND=x11 CRYPTODESK_SMOKE_PHASE="$phase" CRYPTODESK_SMOKE_REPORT="$evidence/$phase.json" \
    timeout --kill-after=15s 100s "$binary" --data-dir "$profile" > "$evidence/$phase.log" 2>&1
  python3 - "$evidence/$phase.json" <<'PY'
import json,pathlib,sys
report=json.loads(pathlib.Path(sys.argv[1]).read_text())
assert report['passed'],report.get('error','Native dropdown verification failed')
assert not report.get('frontend',{}).get('chartInstances'),report.get('frontend')
assert report['backend']['market']['activeLeases']==0,report['backend']['market']
print(f"{report['phase']}: PASS ({len(report['checks'])} native dropdown, theme and persistence assertions)")
PY
done
for capture in "$evidence"/native-theme-*.xwd; do
  ffmpeg -nostdin -hide_banner -loglevel error -y -i "$capture" "${capture%.xwd}.png"
done
python3 - "$evidence" <<'PY'
import hashlib,json,pathlib,struct,sys
directory=pathlib.Path(sys.argv[1]);images=[]
for path in sorted(directory.glob('native-theme-*.png')):
    data=path.read_bytes();assert data[:8]==b'\x89PNG\r\n\x1a\n',path
    width,height=struct.unpack('>II',data[16:24]);assert width>=1100 and height>=720,(path,width,height)
    images.append({'name':path.name,'width':width,'height':height,'sha256':hashlib.sha256(data).hexdigest()})
assert len(images)==7,images
(directory/'screenshots.json').write_text(json.dumps({'passed':True,'source':'Actual isolated Wails/WebKit application window via xwd; no test fixture bridge','images':images},indent=2)+'\n')
print(f"Native theme screenshots: {len(images)} ({directory})")
PY

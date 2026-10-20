#!/usr/bin/env bash
# Verify terminal Ctrl+C and OS termination using an isolated real desktop app.
set -euo pipefail
repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
if [[ $# -eq 1 && ( "$1" == --help || "$1" == -h ) ]]; then
  python3 "$repo_root/scripts/smoke_signals.py" --help
  exit 0
fi
source "$repo_root/scripts/common.sh"
if [[ -z "${DISPLAY:-}" && -z "${WAYLAND_DISPLAY:-}" ]]; then
  echo 'Signal smoke blocked: DISPLAY and WAYLAND_DISPLAY are absent.' >&2
  exit 2
fi
require_node
mkdir -p "$repo_root/build/bin"
npm --prefix "$repo_root/frontend" run build
go build -tags 'production desktop_smoke webkit2_41' -o "$repo_root/build/bin/cryptodesk-signal-smoke" .
python3 "$repo_root/scripts/smoke_signals.py" --binary "$repo_root/build/bin/cryptodesk-signal-smoke" "$@"

#!/usr/bin/env bash
set -euo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/common.sh"
require_native
require_node
require_wails
if [[ -z "${CRYPTODESK_JFOREX_RUNTIME:-}" ]]; then
  python3 "$APP_ROOT/scripts/jforex_bundle.py" --output "$APP_ROOT/.tools/jforex-runtime"
  export CRYPTODESK_JFOREX_RUNTIME="$APP_ROOT/.tools/jforex-runtime"
fi
exec "$APP_ROOT/.tools/wails" dev -tags webkit2_41 "$@"

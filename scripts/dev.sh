#!/usr/bin/env bash
set -euo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/common.sh"
require_native
require_node
require_wails
exec "$APP_ROOT/.tools/wails" dev -tags webkit2_41 "$@"

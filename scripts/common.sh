#!/usr/bin/env bash
# Shared by scripts; deliberately never changes system packages or settings.
set -euo pipefail
APP_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$APP_ROOT"
export PATH="$APP_ROOT/.tools:$PATH"
WAILS_VERSION=v2.17.0
require_node() {
  local node_dirs
  node_dirs="$(python3 "$APP_ROOT/scripts/node_toolchain.py" "$APP_ROOT")" || return 1
  export PATH="$node_dirs:$PATH"
  printf 'Using Node %s and npm %s\n' "$(node --version)" "$(npm --version)"
}
if [[ -f "$APP_ROOT/scripts/native-env.sh" && -d "$APP_ROOT/.tools/native/usr" ]]; then
  source "$APP_ROOT/scripts/native-env.sh"
fi
require_native() {
  if ! pkg-config --exists gtk+-3.0 webkit2gtk-4.1; then
    echo 'Missing GTK3/WebKitGTK 4.1 development headers. See APP_README.md.' >&2
    echo 'No system packages were installed. Independent tests: scripts/test.sh' >&2
    return 1
  fi
}
require_wails() {
  if [[ ! -x "$APP_ROOT/.tools/wails" ]]; then
    echo "Installing pinned Wails CLI $WAILS_VERSION into project-local .tools/"
    mkdir -p "$APP_ROOT/.tools"
    GOBIN="$APP_ROOT/.tools" go install "github.com/wailsapp/wails/v2/cmd/wails@$WAILS_VERSION"
  fi
}

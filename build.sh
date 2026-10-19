#!/usr/bin/env bash
# Build the production desktop app and publish ready-to-run outputs at repo root.
set -euo pipefail

APP_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
RELEASE_NAME="cryptodesk-0.1.0-linux-amd64"

if [[ $# -gt 0 ]]; then
  if [[ $# -eq 1 && ( "$1" == "--help" || "$1" == "-h" ) ]]; then
    printf '%s\n' \
      'Usage: ./build.sh' \
      'Builds the production Linux desktop app, archive and checksums at repo root.' \
      'Requires Go, Python 3.12+ and GTK3/WebKitGTK 4.1 build dependencies.' \
      'Node/npm are checked and downloaded into .tools/ if needed.' \
      'See APP_README.md for the optional project-local native SDK.'
    exit 0
  fi
  printf '%s\n' 'Usage: ./build.sh (or ./build.sh --help)' >&2
  exit 2
fi

# Keep compilation, pinned tooling, embedded assets and release notices in the
# existing build workflow. Publish nothing if compilation or packaging fails.
python3 - "$APP_ROOT" <<'PY'
import pathlib
import shutil
import subprocess
import sys
import tempfile

root = pathlib.Path(sys.argv[1])
bindings = root / 'frontend/wailsjs'
# Wails recreates its runtime wrappers even when binding generation is disabled.
# Preserve existing source files and permissions, including uncommitted edits.
with tempfile.TemporaryDirectory(prefix='cryptodesk-build-bindings-') as temporary:
    backup = pathlib.Path(temporary) / 'wailsjs'
    existed = bindings.is_dir()
    if existed:
        shutil.copytree(bindings, backup)
    try:
        result = subprocess.run(
            [str(root / 'scripts/build-linux.sh'), '-m', '-nosyncgomod'], cwd=root
        )
    finally:
        if existed:
            shutil.copytree(backup, bindings, dirs_exist_ok=True)
    sys.exit(result.returncode)
PY

install -m 755 -- "$APP_ROOT/build/bin/cryptodesk" "$APP_ROOT/cryptodesk"
install -m 644 -- "$APP_ROOT/release/$RELEASE_NAME.tar.gz" "$APP_ROOT/$RELEASE_NAME.tar.gz"
(
  cd "$APP_ROOT"
  sha256sum cryptodesk "$RELEASE_NAME.tar.gz" > cryptodesk-SHA256SUMS
)

printf '\n%s\n' \
  "Ready to run: $APP_ROOT/cryptodesk" \
  "Portable archive: $APP_ROOT/$RELEASE_NAME.tar.gz" \
  "Checksums: $APP_ROOT/cryptodesk-SHA256SUMS" \
  'Runtime requirements: compatible Linux with GTK3 and WebKitGTK 4.1 (APP_README.md).'

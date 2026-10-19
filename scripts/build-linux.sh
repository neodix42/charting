#!/usr/bin/env bash
set -euo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/common.sh"
require_native
require_node
require_wails
"$APP_ROOT/.tools/wails" build -tags webkit2_41 -trimpath "$@"
ARTIFACT_DIR="$APP_ROOT/release/cryptodesk-0.1.0-linux-amd64"
mkdir -p "$ARTIFACT_DIR/docs"
cp "$APP_ROOT/build/bin/cryptodesk" "$ARTIFACT_DIR/cryptodesk"
cp "$APP_ROOT/build/linux/cryptodesk.desktop" "$APP_ROOT/build/linux/cryptodesk.svg" "$APP_ROOT/scripts/install-user.sh" "$APP_ROOT/APP_README.md" "$ARTIFACT_DIR/"
cp "$APP_ROOT/LICENSE" "$APP_ROOT/STATUS.md" "$ARTIFACT_DIR/"
cp -R "$APP_ROOT/docs/." "$ARTIFACT_DIR/docs/"
python3 "$APP_ROOT/scripts/version-manifest.py" "$ARTIFACT_DIR/version.json"
cd "$APP_ROOT/release"
tar -czf cryptodesk-0.1.0-linux-amd64.tar.gz cryptodesk-0.1.0-linux-amd64
sha256sum cryptodesk-0.1.0-linux-amd64.tar.gz cryptodesk-0.1.0-linux-amd64/cryptodesk > SHA256SUMS
printf '%s\n' "$APP_ROOT/release/cryptodesk-0.1.0-linux-amd64.tar.gz"

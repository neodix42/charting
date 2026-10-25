#!/usr/bin/env bash
set -euo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/common.sh"
CRYPTODESK_VERSION="$(python3 "$APP_ROOT/scripts/app_version.py")"
export CRYPTODESK_VERSION
# Wails temporarily regenerates tracked wrappers. Record the user's actual
# sources before that starts; the root wrapper restores them after packaging.
CRYPTODESK_SOURCE_PROVENANCE="$(python3 "$APP_ROOT/scripts/version-manifest.py" --capture-source)"
export CRYPTODESK_SOURCE_PROVENANCE
require_native
require_node
require_wails
python3 "$APP_ROOT/scripts/app_icons.py" --check
"$APP_ROOT/.tools/wails" build -tags webkit2_41 -trimpath "$@"
python3 "$APP_ROOT/scripts/app_version.py" --verify-frontend > /dev/null
RELEASE_NAME="cryptodesk-$CRYPTODESK_VERSION-linux-amd64"
ARTIFACT_DIR="$APP_ROOT/release/$RELEASE_NAME"
mkdir -p "$ARTIFACT_DIR/docs"
cp "$APP_ROOT/build/bin/cryptodesk" "$ARTIFACT_DIR/cryptodesk"
cp "$APP_ROOT/build/linux/cryptodesk.desktop" "$APP_ROOT/build/linux/cryptodesk.svg" "$APP_ROOT/build/linux/cryptodesk.png" "$APP_ROOT/scripts/install-user.sh" "$APP_ROOT/APP_README.md" "$ARTIFACT_DIR/"
cp -R "$APP_ROOT/build/linux/icons" "$ARTIFACT_DIR/"
cp "$APP_ROOT/LICENSE" "$APP_ROOT/STATUS.md" "$ARTIFACT_DIR/"
cp -R "$APP_ROOT/docs/." "$ARTIFACT_DIR/docs/"
python3 "$APP_ROOT/scripts/screener_bundle.py" --root "$APP_ROOT" --target linux-amd64 --destination "$APP_ROOT/.tools/screener-runtime-linux-amd64"
python3 "$APP_ROOT/scripts/screener_bundle.py" --target linux-amd64 --copy-from "$APP_ROOT/.tools/screener-runtime-linux-amd64" --destination "$ARTIFACT_DIR/screener-runtime"
python3 "$APP_ROOT/scripts/version-manifest.py" "$ARTIFACT_DIR/version.json"
cd "$APP_ROOT/release"
tar -czf "$RELEASE_NAME.tar.gz" "$RELEASE_NAME"
sha256sum "$RELEASE_NAME.tar.gz" "$RELEASE_NAME/cryptodesk" > SHA256SUMS
printf '%s\n' "$APP_ROOT/release/$RELEASE_NAME.tar.gz"

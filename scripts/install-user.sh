#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
if [[ -f "$SCRIPT_DIR/cryptodesk" ]]; then
  SOURCE_DIR="$SCRIPT_DIR"
else
  SOURCE_DIR="$SCRIPT_DIR/../release/cryptodesk-0.1.0-linux-amd64"
fi
case "${1:-}" in
 --dry-run) DRY_RUN=1 ;;
 '') DRY_RUN=0 ;;
 *) echo 'Usage: install-user.sh [--dry-run]' >&2; exit 2 ;;
esac
DATA_BASE="${XDG_DATA_HOME:-$HOME/.local/share}"
INSTALL_DIR="$DATA_BASE/cryptodesk-app"
DESKTOP_DIR="$DATA_BASE/applications"
ICON_DIR="$DATA_BASE/icons/hicolor/scalable/apps"
for source_file in cryptodesk cryptodesk.svg cryptodesk.desktop; do
  if [[ ! -f "$SOURCE_DIR/$source_file" ]]; then echo "Missing release file: $SOURCE_DIR/$source_file; run scripts/build-linux.sh first" >&2; exit 1; fi
done
printf 'Install binary to %s\nLauncher to %s\nIcon to %s\nWorkspace configuration is preserved.\n' "$INSTALL_DIR" "$DESKTOP_DIR/cryptodesk.desktop" "$ICON_DIR/cryptodesk.svg"
if [[ "$DRY_RUN" == 1 ]]; then exit 0; fi
mkdir -p "$INSTALL_DIR" "$DESKTOP_DIR" "$ICON_DIR"
install -m 755 "$SOURCE_DIR/cryptodesk" "$INSTALL_DIR/cryptodesk"
install -m 644 "$SOURCE_DIR/cryptodesk.svg" "$ICON_DIR/cryptodesk.svg"
# Desktop entries support quoted absolute paths; escape the few special characters.
python3 - "$SOURCE_DIR/cryptodesk.desktop" "$DESKTOP_DIR/cryptodesk.desktop" "$INSTALL_DIR/cryptodesk" <<'PY'
import pathlib, sys
source, dest, binary = sys.argv[1:]
escaped = binary.replace('\\', '\\\\').replace('"', '\\"').replace('`', '\\`').replace('$', '\\$').replace('%', '%%')
text = pathlib.Path(source).read_text().replace('Exec=cryptodesk', f'Exec="{escaped}"')
pathlib.Path(dest).write_text(text)
PY
chmod 644 "$DESKTOP_DIR/cryptodesk.desktop"
echo 'Installed. Launch CryptoDesk from your application menu.'

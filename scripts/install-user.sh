#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
if [[ -f "$SCRIPT_DIR/cryptodesk" ]]; then
  SOURCE_DIR="$SCRIPT_DIR"
else
  APP_VERSION="$(python3 "$SCRIPT_DIR/app_version.py")"
  SOURCE_DIR="$SCRIPT_DIR/../release/cryptodesk-$APP_VERSION-linux-amd64"
fi
case "${1:-}" in
 --dry-run) DRY_RUN=1 ;;
 '') DRY_RUN=0 ;;
 *) echo 'Usage: install-user.sh [--dry-run]' >&2; exit 2 ;;
esac
if [[ $# -gt 1 ]]; then echo 'Usage: install-user.sh [--dry-run]' >&2; exit 2; fi
DATA_BASE="${XDG_DATA_HOME:-$HOME/.local/share}"
if [[ "$DATA_BASE" != /* ]]; then echo 'XDG_DATA_HOME must be an absolute directory' >&2; exit 1; fi
INSTALL_DIR="$DATA_BASE/cryptodesk-app"
DESKTOP_DIR="$DATA_BASE/applications"
ICON_BASE="$DATA_BASE/icons"
ICON_DIR="$ICON_BASE/hicolor"
DESKTOP_SHORTCUT_DIR="${XDG_DESKTOP_DIR:-}"
if [[ -z "$DESKTOP_SHORTCUT_DIR" ]] && command -v xdg-user-dir >/dev/null 2>&1; then
  DESKTOP_SHORTCUT_DIR="$(xdg-user-dir DESKTOP 2>/dev/null || true)"
fi
if [[ "$DESKTOP_SHORTCUT_DIR" != /* || "$DESKTOP_SHORTCUT_DIR" == "$HOME" || ! -d "$DESKTOP_SHORTCUT_DIR" ]]; then
  DESKTOP_SHORTCUT_DIR=""
fi
for source_file in cryptodesk cryptodesk.svg cryptodesk.png cryptodesk.desktop; do
  if [[ ! -f "$SOURCE_DIR/$source_file" ]]; then echo "Missing release file: $SOURCE_DIR/$source_file; run scripts/build-linux.sh first" >&2; exit 1; fi
done
for icon_size in 16 24 32 48 64 128 256 512; do
  if [[ ! -f "$SOURCE_DIR/icons/hicolor/${icon_size}x${icon_size}/apps/cryptodesk.png" ]]; then
    echo "Missing release icon: ${icon_size}x${icon_size}; rebuild this Linux package" >&2; exit 1
  fi
done
# Compatibility with previously published bundled Linux archives; current
# builds use the system GUI runtime. Preserve their existing install/rollback.
PORTABLE_RUNTIME=0
if [[ -d "$SOURCE_DIR/runtime" ]]; then
  if [[ ! -x "$SOURCE_DIR/runtime/usr/bin/cryptodesk" ]]; then echo 'The bundled Linux runtime is incomplete' >&2; exit 1; fi
  PORTABLE_RUNTIME=1
fi
SCREENER_RUNTIME=0
if [[ -d "$SOURCE_DIR/screener-runtime" ]]; then
  for screener_file in worker.cjs node package.json package-lock.json SCREENER-PROVENANCE.json; do
    if [[ ! -s "$SOURCE_DIR/screener-runtime/$screener_file" ]]; then echo 'The bundled Screener runtime is incomplete' >&2; exit 1; fi
  done
  if [[ ! -x "$SOURCE_DIR/screener-runtime/node" ]]; then echo 'The bundled Screener Node runtime is not executable' >&2; exit 1; fi
  SCREENER_RUNTIME=1
fi
printf 'Install app to %s\nLauncher to %s\nIcons to %s\nWorkspace configuration is preserved.\n' "$INSTALL_DIR" "$DESKTOP_DIR/cryptodesk.desktop" "$ICON_DIR"
if [[ "$PORTABLE_RUNTIME" == 1 ]]; then echo 'The complete bundled runtime, licenses and sources will be copied with the launcher.'; fi
if [[ -n "$DESKTOP_SHORTCUT_DIR" ]]; then printf 'Desktop shortcut to %s\n' "$DESKTOP_SHORTCUT_DIR/cryptodesk.desktop"; fi
if [[ "$DRY_RUN" == 1 ]]; then exit 0; fi
mkdir -p "$INSTALL_DIR" "$DESKTOP_DIR" "$ICON_DIR/scalable/apps"
STAGING="$(mktemp -d "$INSTALL_DIR/.install.XXXXXXXX")"
INSTALL_COMMITTED=0
NEW_RUNTIME_MOVED=0
NEW_SCREENER_MOVED=0
cleanup_install() {
  local install_status=$?
  if [[ "$INSTALL_COMMITTED" != 1 ]]; then
    if [[ -e "$STAGING/previous-runtime" ]]; then
      if [[ -e "$INSTALL_DIR/runtime" ]]; then rm -rf -- "$INSTALL_DIR/runtime"; fi
      mv -- "$STAGING/previous-runtime" "$INSTALL_DIR/runtime"
    elif [[ "$NEW_RUNTIME_MOVED" == 1 ]]; then
      rm -rf -- "$INSTALL_DIR/runtime"
    fi
    if [[ -e "$STAGING/previous-screener" ]]; then
      if [[ -e "$INSTALL_DIR/screener-runtime" ]]; then rm -rf -- "$INSTALL_DIR/screener-runtime"; fi
      mv -- "$STAGING/previous-screener" "$INSTALL_DIR/screener-runtime"
    elif [[ "$NEW_SCREENER_MOVED" == 1 ]]; then
      rm -rf -- "$INSTALL_DIR/screener-runtime"
    fi
  fi
  rm -rf -- "$STAGING"
  return "$install_status"
}
trap cleanup_install EXIT
install -m 755 "$SOURCE_DIR/cryptodesk" "$STAGING/cryptodesk"
install -m 644 "$SOURCE_DIR/cryptodesk.png" "$STAGING/cryptodesk.png"
install -m 644 "$SOURCE_DIR/cryptodesk.svg" "$STAGING/cryptodesk.svg"
if [[ "$SCREENER_RUNTIME" == 1 ]]; then
  cp -a -- "$SOURCE_DIR/screener-runtime" "$STAGING/screener-runtime"
  if [[ -e "$INSTALL_DIR/screener-runtime" ]]; then mv -- "$INSTALL_DIR/screener-runtime" "$STAGING/previous-screener"; fi
  mv -- "$STAGING/screener-runtime" "$INSTALL_DIR/screener-runtime"
  NEW_SCREENER_MOVED=1
fi
if [[ "$PORTABLE_RUNTIME" == 1 ]]; then
  cp -a -- "$SOURCE_DIR/runtime" "$STAGING/runtime"
  if [[ -e "$INSTALL_DIR/runtime" ]]; then mv -- "$INSTALL_DIR/runtime" "$STAGING/previous-runtime"; fi
  mv -- "$STAGING/runtime" "$INSTALL_DIR/runtime"
  NEW_RUNTIME_MOVED=1
fi
# Replace the executable with a rename, so updating a running binary never
# truncates it. Copy only application-owned files; retain unrelated user files.
for source_file in cryptodesk cryptodesk.png cryptodesk.svg; do mv -f -- "$STAGING/$source_file" "$INSTALL_DIR/$source_file"; done
for source_file in LICENSE APP_README.md version.json runtime-manifest.json; do
  if [[ -f "$SOURCE_DIR/$source_file" ]]; then install -m 644 "$SOURCE_DIR/$source_file" "$INSTALL_DIR/$source_file"; fi
done
if [[ -d "$SOURCE_DIR/docs" ]]; then mkdir -p "$INSTALL_DIR/docs"; cp -R -- "$SOURCE_DIR/docs/." "$INSTALL_DIR/docs/"; fi
install -m 644 "$SOURCE_DIR/cryptodesk.svg" "$ICON_DIR/scalable/apps/cryptodesk.svg"
for icon_size in 16 24 32 48 64 128 256 512; do
  mkdir -p "$ICON_DIR/${icon_size}x${icon_size}/apps"
  install -m 644 "$SOURCE_DIR/icons/hicolor/${icon_size}x${icon_size}/apps/cryptodesk.png" "$ICON_DIR/${icon_size}x${icon_size}/apps/cryptodesk.png"
done
# Desktop entries support quoted absolute paths; escape the few special characters.
python3 - "$SOURCE_DIR/cryptodesk.desktop" "$DESKTOP_DIR/cryptodesk.desktop" "$INSTALL_DIR/cryptodesk" "$INSTALL_DIR/cryptodesk.png" <<'PY'
import os, pathlib, sys, tempfile
source, dest, binary, icon = sys.argv[1:]
def entry(value):
    return value.replace('\\', '\\\\').replace('\n', '\\n').replace('\r', '\\r').replace('\t', '\\t')
quoted = binary.replace('\\', '\\\\').replace('"', '\\"').replace('`', '\\`').replace('$', '\\$').replace('%', '%%')
# GIO validates the first executable before expanding %% field escapes. Using
# env only for this case preserves a literal percent in the installed path.
command = ('"/usr/bin/env" ' if '%' in binary else '') + '"' + quoted + '"'
lines = pathlib.Path(source).read_text().splitlines()
text = '\n'.join('Exec=' + entry(command) if line.startswith('Exec=') else
                 'Icon=' + entry(icon) if line.startswith('Icon=') else line for line in lines) + '\n'
target = pathlib.Path(dest)
descriptor, name = tempfile.mkstemp(prefix='.cryptodesk-launcher-', dir=target.parent)
temporary = pathlib.Path(name)
try:
    with os.fdopen(descriptor, 'w') as stream:
        stream.write(text)
        os.fchmod(stream.fileno(), 0o644)
    temporary.replace(target)
finally:
    temporary.unlink(missing_ok=True)
PY
# Notify icon-theme watchers and update only this user's metadata. Absolute PNG
# Icon= also works for launchers that do not support scalable/themed SVG icons.
touch "$ICON_DIR" "$ICON_BASE"
if command -v gtk-update-icon-cache >/dev/null 2>&1; then
  gtk-update-icon-cache --force --ignore-theme-index "$ICON_DIR" || echo 'Icon cache refresh failed; the absolute PNG launcher icon is installed.' >&2
fi
if command -v update-desktop-database >/dev/null 2>&1; then
  update-desktop-database "$DESKTOP_DIR" || echo 'Desktop metadata refresh failed; the application launcher is installed.' >&2
fi
if [[ -n "$DESKTOP_SHORTCUT_DIR" ]]; then
  DESKTOP_SHORTCUT="$DESKTOP_SHORTCUT_DIR/cryptodesk.desktop"
  if [[ ! -e "$DESKTOP_SHORTCUT" && ! -L "$DESKTOP_SHORTCUT" ]]; then
    install -m 755 "$DESKTOP_DIR/cryptodesk.desktop" "$DESKTOP_SHORTCUT"
  elif ! cmp -s -- "$DESKTOP_DIR/cryptodesk.desktop" "$DESKTOP_SHORTCUT"; then
    printf 'Preserved existing desktop file: %s\n' "$DESKTOP_SHORTCUT"
    DESKTOP_SHORTCUT=""
  fi
  if [[ -n "$DESKTOP_SHORTCUT" ]]; then
    chmod u+x "$DESKTOP_SHORTCUT"
    if command -v gio >/dev/null 2>&1; then
      gio set --type string "$DESKTOP_SHORTCUT" metadata::trusted true 2>/dev/null || echo 'Desktop shortcut created; your desktop may ask you to allow launching it.'
    fi
  fi
fi
INSTALL_COMMITTED=1
echo 'Installed. Launch CryptoDesk from your application menu.'

#!/usr/bin/env bash
set -euo pipefail
portable_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
portable_usr="$portable_dir/runtime/usr"
# External desktop applications must inherit the caller's environment, rather
# than this bundle's GTK/GLib. Wails finds the scoped xdg-open wrapper on PATH.
export CRYPTODESK_HOST_XDG_OPEN="$(command -v xdg-open || true)"
for portable_var in LD_LIBRARY_PATH GIO_MODULE_DIR GIO_EXTRA_MODULES GSETTINGS_SCHEMA_DIR GTK_PATH GTK_IM_MODULE GTK_MODULES GTK3_MODULES GDK_PIXBUF_MODULEDIR GDK_PIXBUF_MODULE_FILE GST_PLUGIN_SYSTEM_PATH_1_0 GST_PLUGIN_PATH_1_0 GST_PLUGIN_SCANNER_1_0 GST_REGISTRY_1_0 XDG_DATA_DIRS GLYCIN_DATA_DIR PATH; do
  if [[ -v $portable_var ]]; then
    export "CRYPTODESK_HOST_${portable_var}=${!portable_var}" "CRYPTODESK_HOST_${portable_var}_SET=1"
  else
    export "CRYPTODESK_HOST_${portable_var}_SET=0"
  fi
done
# WebKit's release builds use compiled helper paths. Those paths are relocated
# within the bundled library to this working directory, never to host helpers.
# Keep explicitly relative profile paths relative to the caller's directory.
portable_args=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    --data-dir)
      portable_args+=("$1"); shift
      if [[ $# -eq 0 ]]; then break; fi
      if [[ -n $1 && $1 != /* ]]; then portable_args+=("$PWD/$1"); else portable_args+=("$1"); fi
      ;;
    --data-dir=*)
      portable_data="${1#--data-dir=}"
      if [[ -n $portable_data && $portable_data != /* ]]; then portable_data="$PWD/$portable_data"; fi
      portable_args+=("--data-dir=$portable_data")
      ;;
    *) portable_args+=("$1");;
  esac
  shift
done
export LD_LIBRARY_PATH="$portable_usr/lib/x86_64-linux-gnu"
export GIO_MODULE_DIR="$portable_usr/lib/x86_64-linux-gnu/gio/modules"
export GIO_EXTRA_MODULES="$GIO_MODULE_DIR"
export GSETTINGS_SCHEMA_DIR="$portable_usr/share/glib-2.0/schemas"
export GTK_PATH="$portable_usr/lib/x86_64-linux-gnu/gtk-3.0"
export GTK_IM_MODULE=simple
unset GTK_MODULES GTK3_MODULES
export GDK_PIXBUF_MODULEDIR="$portable_usr/lib/x86_64-linux-gnu/gdk-pixbuf-2.0/2.10.0/loaders"
export GDK_PIXBUF_MODULE_FILE="$portable_usr/lib/x86_64-linux-gnu/gdk-pixbuf-2.0/2.10.0/loaders.cache"
export GST_PLUGIN_SYSTEM_PATH_1_0="$portable_usr/lib/x86_64-linux-gnu/gstreamer-1.0"
export GST_PLUGIN_PATH_1_0="$GST_PLUGIN_SYSTEM_PATH_1_0"
export GST_PLUGIN_SCANNER_1_0="$portable_usr/lib/x86_64-linux-gnu/gstreamer1.0/gstreamer-1.0/gst-plugin-scanner"
# Keep this package's plugin registry separate from other GStreamer programs.
export GST_REGISTRY_1_0="${XDG_CACHE_HOME:-$HOME/.cache}/cryptodesk/gstreamer-registry.bin"
export XDG_DATA_DIRS="$portable_usr/share:${XDG_DATA_DIRS:-/usr/local/share:/usr/share}"
export GLYCIN_DATA_DIR="$portable_usr/share"
export PATH="$portable_usr/bin:$PATH"
cd -- "$portable_usr"
exec "$portable_usr/bin/cryptodesk" "${portable_args[@]}"

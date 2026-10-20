#!/usr/bin/env bash
set -euo pipefail
portable_host_opener="${CRYPTODESK_HOST_XDG_OPEN:-}"
if [[ -z $portable_host_opener ]]; then
  printf '%s\n' 'CryptoDesk: xdg-open is unavailable on this desktop.' >&2
  exit 127
fi
for portable_var in LD_LIBRARY_PATH GIO_MODULE_DIR GIO_EXTRA_MODULES GSETTINGS_SCHEMA_DIR GTK_PATH GTK_IM_MODULE GTK_MODULES GTK3_MODULES GDK_PIXBUF_MODULEDIR GDK_PIXBUF_MODULE_FILE GST_PLUGIN_SYSTEM_PATH_1_0 GST_PLUGIN_PATH_1_0 GST_PLUGIN_SCANNER_1_0 GST_REGISTRY_1_0 XDG_DATA_DIRS GLYCIN_DATA_DIR PATH; do
  portable_marker="CRYPTODESK_HOST_${portable_var}_SET"
  portable_value="CRYPTODESK_HOST_${portable_var}"
  if [[ ${!portable_marker:-0} == 1 ]]; then
    export "$portable_var=${!portable_value}"
  else
    unset "$portable_var"
  fi
  unset "$portable_marker" "$portable_value"
done
unset CRYPTODESK_HOST_XDG_OPEN
exec "$portable_host_opener" "$@"

#!/usr/bin/env bash
# Source this file; variables affect only the invoking shell/build process.
cryptodesk_repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cryptodesk_native_root="$cryptodesk_repo_root/.tools/native"
cryptodesk_multiarch="$(gcc -print-multiarch)"
if [[ -d "$cryptodesk_native_root/usr" ]]; then
 export PKG_CONFIG_PATH="$cryptodesk_native_root/usr/lib/$cryptodesk_multiarch/pkgconfig:$cryptodesk_native_root/usr/lib/pkgconfig:$cryptodesk_native_root/usr/share/pkgconfig${PKG_CONFIG_PATH:+:$PKG_CONFIG_PATH}"
 export CPATH="$cryptodesk_native_root/usr/include:$cryptodesk_native_root/usr/include/$cryptodesk_multiarch${CPATH:+:$CPATH}"
 export LIBRARY_PATH="$cryptodesk_native_root/usr/lib/$cryptodesk_multiarch:$cryptodesk_native_root/usr/lib${LIBRARY_PATH:+:$LIBRARY_PATH}"
fi
unset cryptodesk_repo_root cryptodesk_native_root cryptodesk_multiarch

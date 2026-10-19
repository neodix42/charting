#!/usr/bin/env bash
# Cross-build a portable Windows x64 ZIP, including the fixed WebView2 runtime.
set -euo pipefail
repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
if [[ $# -eq 1 && ( "$1" == --help || "$1" == -h ) ]]; then
  printf '%s\n' 'Usage: ./win-build.sh [--webview2-cab /path/to/pinned-runtime.cab]' \
    'Builds cryptodesk-0.1.0-windows-amd64.zip and cryptodesk-Windows-SHA256SUMS at repo root.' \
    'Requires Go, Python 3.12+ and 7z (Ubuntu package: 7zip); bootstraps local Node/npm and Wails.' \
    'Downloads and caches the pinned official Microsoft Fixed Version WebView2 runtime.' \
    'No Windows installation, MinGW, system installation or administrator access is used.' \
    'Extract the complete ZIP on Windows and run cryptodesk.exe; see APP_README.md.'
  exit 0
fi
if [[ $# -ne 0 && !( $# -eq 2 && "$1" == --webview2-cab ) ]]; then
  echo 'Usage: ./win-build.sh [--webview2-cab /path/to/pinned-runtime.cab]' >&2
  exit 2
fi
# Resolve local input before common.sh changes the current directory.
args=()
if [[ $# -eq 2 ]]; then
  args=(--webview2-cab "$(realpath -e -- "$2")")
fi
source "$repo_root/scripts/common.sh"
require_node
require_wails
python3 "$repo_root/scripts/windows_build.py" "$repo_root" "${args[@]}"

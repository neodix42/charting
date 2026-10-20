#!/usr/bin/env bash
# Bundle the Linux GUI/browser runtime without changing the host installation.
set -euo pipefail
portable_repo="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
if [[ ${1:-} == --help || ${1:-} == -h ]]; then
  printf '%s\n' 'Usage: ./portable-build.sh [--skip-build]' \
    'Builds cryptodesk-X.Y.Z-linux-amd64-portable.tar.gz at the repository root.' \
    'Bundles GTK3/WebKitGTK, runtime dependencies, licenses and matching sources.' \
    'Requires an Ubuntu x64 build host; no sudo or system installation is used.' \
    'The package still needs a compatible glibc, desktop session and graphics drivers.'
  exit 0
fi
if [[ $# -eq 0 ]]; then
  "$portable_repo/build.sh"
elif [[ $# -ne 1 || $1 != --skip-build ]]; then
  printf '%s\n' 'Usage: ./portable-build.sh [--skip-build]' >&2
  exit 2
fi
python3 "$portable_repo/scripts/linux_portable.py" --root "$portable_repo"

#!/usr/bin/env bash
set -euo pipefail
ci_portable_scripts="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
exec python3 "$ci_portable_scripts/ci_portable_namespace.py" "$@"

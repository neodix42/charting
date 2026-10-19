#!/usr/bin/env bash
# Read-only environment report. No installation, privilege escalation or network requests.
set -uo pipefail
printf 'CryptoDesk read-only development preflight\n\n'
if [[ -r /etc/os-release ]]; then
  grep -E '^(NAME|VERSION|VERSION_ID|PRETTY_NAME)=' /etc/os-release || true
fi
printf '\nArchitecture: '; uname -m
printf 'Session: %s\nDISPLAY: %s\nWAYLAND_DISPLAY: %s\n' "${XDG_SESSION_TYPE:-unset}" "${DISPLAY:-unset}" "${WAYLAND_DISPLAY:-unset}"
printf '\nTools:\n'
for tool in git go node npm gcc pkg-config wails python3; do
  if command -v "$tool" >/dev/null 2>&1; then
    printf '  %-12s %s\n' "$tool" "$(command -v "$tool")"
  else
    printf '  %-12s MISSING\n' "$tool"
  fi
done
# GOTOOLCHAIN=local prevents go version from trying to fetch another toolchain.
if command -v go >/dev/null 2>&1; then GOTOOLCHAIN=local go version 2>&1 || true; fi
if command -v node >/dev/null 2>&1; then node --version || true; fi
if command -v npm >/dev/null 2>&1; then npm --version || true; fi
printf '\nGTK/WebKit pkg-config packages:\n'
if command -v pkg-config >/dev/null 2>&1; then
  for package in gtk+-3.0 webkit2gtk-4.1 webkit2gtk-4.0; do
    if pkg-config --exists "$package"; then
      printf '  %s: %s\n' "$package" "$(pkg-config --modversion "$package")"
    else
      printf '  %s: missing\n' "$package"
    fi
  done
else
  printf '  pkg-config unavailable\n'
fi
printf '\nGit working tree (no changes made):\n'
if command -v git >/dev/null 2>&1 && git rev-parse --show-toplevel >/dev/null 2>&1; then
  git rev-parse --show-toplevel
  git status --short
else
  printf '  Current directory is not a Git working tree.\n'
fi
printf '\nThis report does not prove build or runtime compatibility.\nMissing tools/libraries must be handled explicitly; nothing was installed.\n'

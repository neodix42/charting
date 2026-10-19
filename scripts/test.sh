#!/usr/bin/env bash
set -euo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/common.sh"
python3 -m unittest discover -s scripts -p 'test_*.py'
require_node
go test -race ./internal/... ./cmd/...
go test -tags bindings .
go vet ./internal/... ./cmd/...
cd "$APP_ROOT/frontend"
npm ci
npm run typecheck
npm run lint
npm test
npm run build
printf '%s\n' 'Deterministic tests complete. Browser E2E: cd frontend && npm run test:e2e' 'Live provider smoke is separate: go run ./cmd/smoke' 'Desktop smoke steps: docs/DESKTOP_SMOKE.md'

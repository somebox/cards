#!/usr/bin/env bash
#
# test-js.sh — the frontend JS checks, locally, mirroring the CI `frontend` job
# (.github/workflows/ci.yml). No npm, no deps, no build step: Node's built-in
# --check (syntax) + --test (node:test) over our own assets and tests/js/.
#
# Usage: scripts/test-js.sh
# Requires: node on PATH (CI pins Node 22; any recent Node works locally).

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

if ! command -v node >/dev/null 2>&1; then
  echo "test-js.sh: node not found on PATH" >&2
  exit 1
fi
echo "node $(node --version)"

echo "== syntax-check assets (vendored excluded) =="
for f in internal/httpapi/templates/assets/*.js; do
  case "$(basename "$f")" in alpine.min.js) continue ;; esac
  echo "  node --check $f"
  node --check "$f"
done

echo "== unit tests (node:test) =="
node --test "tests/js/"*.test.cjs

#!/usr/bin/env bash
# Enforces the architectural rule: the data layer (datasource, pipeline,
# output, config) and the wrangl binary MUST NOT depend on anything
# TUI-related (internal/screen, internal/build, or any tuilib package).
#
# Run locally before pushing:
#   ./scripts/check-data-layer-boundary.sh
#
# This script is what the GitHub Action runs in CI.

set -euo pipefail

# Packages that MUST stay TUI-free.
DATA_LAYER=(
  ./internal/datasource
  ./internal/pipeline
  ./internal/action
  ./internal/output
  ./internal/config
  ./internal/expr
  ./cmd/wrangl
)

# Patterns that signal a TUI dependency (forbidden in the data layer).
FORBIDDEN_PATTERNS='internal/screen|internal/build|tuilib'

fail=0
for pkg in "${DATA_LAYER[@]}"; do
  # `go list -deps` walks the full transitive import graph for the
  # given package. We grep that for any TUI-related import; if we find
  # one, the boundary is broken.
  if go list -deps "$pkg" 2>/dev/null | grep -qE "$FORBIDDEN_PATTERNS"; then
    echo "FAIL: $pkg has forbidden TUI imports:"
    go list -deps "$pkg" | grep -E "$FORBIDDEN_PATTERNS" | sed 's/^/  /'
    fail=1
  else
    echo "ok: $pkg"
  fi
done

if [ $fail -ne 0 ]; then
  echo
  echo "The data layer (datasource, pipeline, output, config, cmd/wrangl)"
  echo "must not import internal/screen, internal/build, or any tuilib package."
  echo "See AGENTS.md for the architectural rationale."
  exit 1
fi

#!/usr/bin/env bash
# Quick-start the bd dashboard against a disposable, seeded mock project —
# the base for fast dashboard testing/validation.
#
# Everything under .mockproject/ (next to this script) is gitignored and
# regenerated on demand from seed.jsonl: it's its own throwaway git+bd repo,
# so it never touches this repo's own git history or its own .beads/
# tracker. Reset any time with: rm -rf internal/dashboard/devdata/.mockproject
#
# --static-dir serves the frontend straight off disk, so editing
# app.js/style.css/index.html in internal/dashboard/static shows up on
# browser refresh with no rebuild. Rebuild `bd` (`make build` from the repo
# root) only when Go backend code changes (e.g. internal/dashboard/handlers.go).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
RUNTIME="$SCRIPT_DIR/.mockproject"
BD="$REPO_ROOT/bd"
PORT="${PORT:-7700}"

if [ ! -x "$BD" ]; then
  echo "bd binary not found at $BD — run 'make build' in $REPO_ROOT first." >&2
  exit 1
fi

if [ ! -d "$RUNTIME/.beads" ]; then
  echo "Setting up mock project at $RUNTIME ..."
  mkdir -p "$RUNTIME"
  (
    cd "$RUNTIME"
    git init -q .
    git config user.email "mock@beads.dev"
    git config user.name "bd dashboard mock"
    "$BD" init -p mock --skip-agents --skip-hooks --non-interactive -q
    "$BD" import "$SCRIPT_DIR/seed.jsonl"
  )
fi

cd "$RUNTIME"
exec "$BD" dashboard --port "$PORT" --static-dir "$REPO_ROOT/internal/dashboard/static"

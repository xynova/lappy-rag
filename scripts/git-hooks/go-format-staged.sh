#!/usr/bin/env bash
set -euo pipefail

if [[ $# -eq 0 ]]; then
  exit 0
fi

root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$root"

go_mod_dir="$root"
if [[ -f "$root/go/go.mod" ]]; then
  go_mod_dir="$root/go"
elif [[ ! -f "$root/go.mod" ]]; then
  echo "lefthook: no go.mod at repo root or go/go.mod"
  exit 1
fi

run_fmt() {
  local f="$1"
  local rel="$f"
  if [[ "$go_mod_dir" == "$root/go" ]]; then
    rel="${f#go/}"
  fi
  (
    cd "$go_mod_dir"
    if grep -q 'mvdan.cc/gofumpt' go.mod; then
      go tool gofumpt -w "$rel"
    elif command -v gofmt >/dev/null 2>&1; then
      gofmt -w "$rel"
    else
      echo "lefthook: go tool gofumpt or gofmt required"
      exit 1
    fi
  )
}

for f in "$@"; do
  if [[ ! -f "$f" ]]; then
    continue
  fi
  run_fmt "$f"
done

exit 0

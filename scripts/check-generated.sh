#!/usr/bin/env bash
# Fails when generated code doesn't match the spec and queries. It compares the files before and after
# generating, so uncommitted and unstaged changes count, and a stale file is fixed for the next run.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

dirs=(internal/api internal/apiclient internal/store)
snapshot() { find "${dirs[@]}" -type f -print0 | sort -z | xargs -0 shasum -a 256; }

before=$(snapshot)
go generate ./...
after=$(snapshot)

if [[ $before != "$after" ]]; then
  echo 'Generated code was out of date, so it was regenerated. Changed files:' >&2
  diff <(echo "$before") <(echo "$after") | awk '/^[<>]/ { print "  " $3 }' | sort -u >&2
  echo 'Review and commit them, then run make verify again.' >&2
  exit 1
fi
echo 'Generated code is up to date'

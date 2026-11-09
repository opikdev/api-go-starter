#!/usr/bin/env bash
# Fails when a committed migration is changed, deleted, or renamed, or when two migrations share a version.
set -euo pipefail

cd "${1:-$(dirname "${BASH_SOURCE[0]}")/..}"

base=''
for ref in origin/main main; do
  if git rev-parse --verify --quiet "$ref^{commit}" >/dev/null 2>&1; then
    base=$(git merge-base HEAD "$ref" 2>/dev/null) && break
    base=''
  fi
done
if [[ -z $base ]]; then
  echo 'Migration check skipped: no origin/main or main to compare against'
  exit 0
fi

errors=0
for version in $(find migrations -maxdepth 1 -name '[0-9]*_*.sql' | sed -E 's|.*/0*([0-9]+)_.*|\1|' | sort | uniq -d); do
  echo "Migration check: more than one migration has version $version" >&2
  errors=$((errors + 1))
done
while IFS=$'\t' read -r status path _; do
  [[ $status == A ]] && continue
  echo "Migration check: $path was changed, deleted, or renamed since $base. Migrations are append-only, so add a new one instead" >&2
  errors=$((errors + 1))
done < <(git diff --name-status --find-renames "$base" -- 'migrations/*.sql')

if ((errors > 0)); then
  exit 1
fi
echo 'Migration check passed'

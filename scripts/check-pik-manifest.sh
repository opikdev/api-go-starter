#!/usr/bin/env bash
# Fails when a Go file uses the module path but pik.yaml doesn't rename it there.
set -euo pipefail

module=$(awk '/^module / { print $2 }' go.mod)
listed=$(awk -v from="  - from: $module" '
  $0 == from { inside = 1; next }
  inside && /^  - / { inside = 0 }
  inside && /^      - / { print $2 }
' pik.yaml | sort)
actual=$(git ls-files --cached --others --exclude-standard -- '*.go' go.mod | xargs grep -lF "$module" | sort)

if [[ $listed != "$actual" ]]; then
  echo "pik.yaml must list exactly the files that use $module. Missing (+) or extra (-):" >&2
  diff <(echo "$listed") <(echo "$actual") | grep '^[<>]' | sed 's/^</-/; s/^>/+/' >&2
  exit 1
fi
echo "pik.yaml renames the module path in all $(echo "$actual" | wc -l | tr -d ' ') files"

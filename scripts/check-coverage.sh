#!/usr/bin/env bash
set -euo pipefail

profile=${1:?Usage: check-coverage.sh <profile> <minimum-percent>}
minimum=${2:?Usage: check-coverage.sh <profile> <minimum-percent>}

total=$(go tool cover -func="$profile" | awk '/^total:/ { sub("%", "", $3); print $3 }')
echo "Hand-written code coverage: ${total}%"
if ! awk -v total="$total" -v minimum="$minimum" 'BEGIN { exit !(total >= minimum) }'; then
  echo "Coverage ${total}% is under ${minimum}%" >&2
  exit 1
fi

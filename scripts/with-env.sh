#!/bin/sh
# Runs a command with .env loaded. Variables that are already set win, as with node --env-file.
if [ -f .env ]; then
  while IFS='=' read -r key value || [ -n "$key" ]; do
    case $key in '' | \#*) continue ;; esac
    printenv "$key" >/dev/null || export "$key=$value"
  done <.env
fi
exec "$@"

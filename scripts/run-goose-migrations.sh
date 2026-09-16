#!/usr/bin/env bash
set -euo pipefail

: "${DATABASE_URL:?DATABASE_URL is required}"

goose_binary="${GOOSE_BINARY:-${HOME}/.goose/bin/goose}"
if [[ ! -x "${goose_binary}" ]]; then
  echo "Goose binary was not found at ${goose_binary}." >&2
  exit 1
fi

export GOOSE_DRIVER=postgres
export GOOSE_DBSTRING="${DATABASE_URL}"
export GOOSE_MIGRATION_DIR=migrations

for attempt_number in {1..6}; do
  echo "Running Goose migration attempt ${attempt_number}."
  if "${goose_binary}" up; then
    echo "Goose migrations completed successfully."
    exit 0
  fi
  echo "Migration attempt ${attempt_number} failed; retrying in 5 seconds." >&2
  sleep 5
done

exit 1

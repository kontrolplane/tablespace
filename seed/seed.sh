#!/usr/bin/env bash
set -euo pipefail

# Creates the sample databases, one per file in seed/databases, skipping those that exist.
# psql is used when it is installed, else the one in the docker compose container.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

export PGHOST="${PGHOST:-localhost}"
export PGPORT="${PGPORT:-5432}"
export PGUSER="${PGUSER:-postgres}"
export PGPASSWORD="${PGPASSWORD:-postgres}"

psql_() {
  if command -v psql >/dev/null 2>&1; then
    psql -v ON_ERROR_STOP=1 -q "$@"
  else
    docker compose -f "$SCRIPT_DIR/../docker-compose.yaml" exec -T -e PGPASSWORD="$PGPASSWORD" postgres \
      psql -v ON_ERROR_STOP=1 -q -U "$PGUSER" "$@"
  fi
}

for file in "$SCRIPT_DIR"/databases/*.sql; do
  db="$(basename "$file" .sql)"
  if [ "$(psql_ -d postgres -tAc "select 1 from pg_database where datname = '$db'")" = "1" ]; then
    echo "database $db already exists, skipping"
    continue
  fi
  echo "creating database $db"
  psql_ -d postgres -c "create database $db"
  psql_ -d "$db" < "$file"
done

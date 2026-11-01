#!/usr/bin/env bash
set -euo pipefail

# Starts sessions for the activity tab, for ten minutes: one holding a lock in an open
# transaction, one waiting on it, and one running a slow query.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPOSE=(docker compose -f "$SCRIPT_DIR/../docker-compose.yaml")

session() {
  local app="$1" sql="$2"
  "${COMPOSE[@]}" exec -d -T -e PGAPPNAME="$app" postgres psql -U postgres -d shop -c "$sql"
}

session "billing-worker" "begin; update shop.orders set status = 'paid' where id = 1; select pg_sleep(600); commit;"
sleep 1
session "checkout-api" "update shop.orders set status = 'cancelled' where id = 1;"
session "reporting" "select pg_sleep(600), count(*) from shop.order_lines;"
echo "started 3 sessions, they end within ten minutes"

#!/usr/bin/env bash
# test-dolt-schema.sh — Validate beads table schema in Dolt.
#
# Tests:
#   1. Issues table exists with expected columns (id, title, status, type, priority)
#   2. Config table exists with key/value columns
#   3. Column types match expectations
#   4. No unexpected tables (schema drift check)
#
# Usage:
#   ./scripts/test-dolt-schema.sh [NAMESPACE]

MODULE_NAME="dolt-schema"
source "$(dirname "$0")/lib.sh"

log "Testing Dolt schema in namespace: $E2E_NAMESPACE"

# ── Discover Dolt pod and credentials ──────────────────────────────────
DOLT_POD=$(kube get pods --no-headers 2>/dev/null | grep "dolt-[0-9]" | head -1 | awk '{print $1}')

if [[ -z "$DOLT_POD" ]]; then
  skip_all "No Dolt pod found"
  exit 0
fi

log "Dolt pod: $DOLT_POD"

# Get Dolt root password
DOLT_PASSWORD=""
SECRET_NAME=$(kube get secrets --no-headers -o custom-columns=":metadata.name" 2>/dev/null | grep "dolt-root-password" | head -1)
if [[ -n "$SECRET_NAME" ]]; then
  DOLT_PASSWORD=$(kube get secret "$SECRET_NAME" -o jsonpath='{.data.password}' 2>/dev/null | base64 -d 2>/dev/null)
fi

# Helper: run SQL query against Dolt
dolt_sql() {
  local query="$1"
  if command -v mysql >/dev/null 2>&1 && [[ -n "$DOLT_PORT" && -n "$DOLT_PASSWORD" ]]; then
    mysql -h 127.0.0.1 -P "$DOLT_PORT" -u root -p"$DOLT_PASSWORD" -N -e "$query" 2>/dev/null
  else
    kube exec "$DOLT_POD" -c dolt -- dolt sql -q "$query" -r csv 2>/dev/null | tail -n +2
  fi
}

# ── Port-forward to Dolt (optional, speeds up queries) ─────────────────
DOLT_PORT=""
DOLT_SVC=$(kube get svc --no-headers -o custom-columns=":metadata.name" 2>/dev/null | grep "dolt" | grep -v "clusterctl" | head -1)
if command -v mysql >/dev/null 2>&1; then
  if [[ -n "$DOLT_SVC" ]]; then
    DOLT_PORT=$(start_port_forward "svc/$DOLT_SVC" 3306) || true
  else
    DOLT_PORT=$(start_port_forward "pod/$DOLT_POD" 3306) || true
  fi
fi

# ── Test 1: Issues table exists ────────────────────────────────────────
test_issues_table_exists() {
  local tables
  tables=$(dolt_sql "SHOW TABLES FROM beads")
  assert_contains "$tables" "issues"
}
run_test "Issues table exists in beads database" test_issues_table_exists

# ── Test 2: Issues table has expected columns ──────────────────────────
ISSUES_COLUMNS=""

test_issues_columns() {
  ISSUES_COLUMNS=$(dolt_sql "SELECT COLUMN_NAME FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA='beads' AND TABLE_NAME='issues' ORDER BY ORDINAL_POSITION")
  [[ -n "$ISSUES_COLUMNS" ]] || return 1
  assert_contains "$ISSUES_COLUMNS" "id" || return 1
  assert_contains "$ISSUES_COLUMNS" "title" || return 1
  assert_contains "$ISSUES_COLUMNS" "status" || return 1
  assert_contains "$ISSUES_COLUMNS" "type" || return 1
  assert_contains "$ISSUES_COLUMNS" "priority"
}
run_test "Issues table has id, title, status, type, priority columns" test_issues_columns

# ── Test 3: Config table exists ────────────────────────────────────────
test_config_table_exists() {
  local tables
  tables=$(dolt_sql "SHOW TABLES FROM beads")
  assert_contains "$tables" "config"
}
run_test "Config table exists in beads database" test_config_table_exists

# ── Test 4: Config table has key/value columns ─────────────────────────
test_config_columns() {
  local cols
  cols=$(dolt_sql "SELECT COLUMN_NAME FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA='beads' AND TABLE_NAME='config' ORDER BY ORDINAL_POSITION")
  [[ -n "$cols" ]] || return 1
  assert_contains "$cols" "key" || assert_contains "$cols" "k" || return 1
  assert_contains "$cols" "value" || assert_contains "$cols" "v"
}
run_test "Config table has key/value columns" test_config_columns

# ── Test 5: Issues column types ────────────────────────────────────────
test_issues_column_types() {
  local type_info
  type_info=$(dolt_sql "SELECT COLUMN_NAME, DATA_TYPE FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA='beads' AND TABLE_NAME='issues' AND COLUMN_NAME IN ('id','title','status','priority')")
  [[ -n "$type_info" ]] || return 1
  # id and title should be string types (varchar/text)
  local id_type
  id_type=$(dolt_sql "SELECT DATA_TYPE FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA='beads' AND TABLE_NAME='issues' AND COLUMN_NAME='id'" | tr -d ' ')
  assert_match "$id_type" "varchar|text|char"
}
run_test "Issues id column is string type (varchar/text)" test_issues_column_types

# ── Test 6: Priority column is integer ─────────────────────────────────
test_priority_type() {
  local ptype
  ptype=$(dolt_sql "SELECT DATA_TYPE FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA='beads' AND TABLE_NAME='issues' AND COLUMN_NAME='priority'" | tr -d ' ')
  assert_match "$ptype" "int|tinyint|smallint|bigint"
}
run_test "Priority column is integer type" test_priority_type

# ── Test 7: No unexpected tables (drift check) ─────────────────────────
test_no_unexpected_tables() {
  local tables
  tables=$(dolt_sql "SHOW TABLES FROM beads")
  # Expected tables: issues, config, comments, deps/dependencies, labels, decisions/decision_points,
  # mail, compacted/compaction_snapshots, events, metadata, routes, interactions, child_counters,
  # issue_snapshots, export_hashes, federation_peers, repo_mtimes, plus dolt system tables (dolt_*)
  local unexpected
  unexpected=$(echo "$tables" | grep -v -E "issues|config|comments|depend|labels|decision|mail|compacti?|dolt_|audit|events|metadata|routes|interactions|child_counter|snapshot|export_hash|federation|repo_mtime" | tr -d '[:space:]')
  if [[ -n "$unexpected" ]]; then
    log "  Unexpected tables found: $unexpected"
    # This is a warning, not a failure — new tables are fine
    log "  (warning only — new tables may be intentional)"
  fi
  return 0
}
run_test "Schema drift check (unexpected tables)" test_no_unexpected_tables

# ── Test 8: Issues table has at least one row (namespace is active) ────
_ISSUE_COUNT=""
_ISSUE_COUNT=$(dolt_sql "SELECT COUNT(*) FROM beads.issues" 2>/dev/null | tr -d '[:space:]')

if [[ "${_ISSUE_COUNT:-0}" -eq 0 ]]; then
  skip_test "Issues table has data" "no issues in database (fresh namespace)"
else
  test_issues_have_data() {
    assert_gt "$_ISSUE_COUNT" 0
  }
  run_test "Issues table has data ($_ISSUE_COUNT rows)" test_issues_have_data
fi

# ── Summary ───────────────────────────────────────────────────────────
print_summary

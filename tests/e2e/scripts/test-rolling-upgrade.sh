#!/usr/bin/env bash
# test-rolling-upgrade.sh — verify zero downtime during rolling daemon upgrades.
#
# Tests:
#   1. Daemon has >= 2 replicas (prerequisite for zero-downtime)
#   2. RollingUpdate strategy with maxUnavailable=0
#   3. Zero dropped requests during rolling restart (continuous probe)
#   4. All replicas healthy after rollout completes
#   5. Rollout completed within timeout
#   6. No restarts or crash-loops introduced
#
# How it works:
#   - Starts a background probe loop hitting /healthz every 0.5s via service port-forward
#   - Triggers `kubectl rollout restart` on the daemon deployment
#   - Waits for rollout to complete while the probe loop runs
#   - Stops probes and verifies zero failures
#
# Usage: E2E_NAMESPACE=gastown-rwx ./scripts/test-rolling-upgrade.sh

set -euo pipefail
MODULE_NAME="rolling-upgrade"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

NS="$E2E_NAMESPACE"

log "Testing zero-downtime rolling upgrade in $NS"

# ── Discover daemon ──────────────────────────────────────────────────
DAEMON_DEPLOY=$(kube get deployment --no-headers 2>/dev/null \
  | grep "daemon" | grep -v "dolt\|nats\|slackbot\|controller" \
  | head -1 | awk '{print $1}')

if [[ -z "$DAEMON_DEPLOY" ]]; then
  skip_all "no daemon deployment found"
  exit 0
fi

log "Daemon deployment: $DAEMON_DEPLOY"

DAEMON_SVC=$(kube get svc --no-headers 2>/dev/null \
  | grep "daemon" | grep -v "dolt\|nats\|headless\|standby" \
  | head -1 | awk '{print $1}')

if [[ -z "$DAEMON_SVC" ]]; then
  skip_all "no daemon service found"
  exit 0
fi

log "Daemon service: $DAEMON_SVC"

# ── Probe state ──────────────────────────────────────────────────────
PROBE_LOG=$(mktemp /tmp/rolling-upgrade-probe.XXXXXX)
PROBE_PID=""
PROBE_RUNNING=false
_EXTRA_PF_PIDS=()  # port-forwards managed by the probe loop

_rolling_cleanup() {
  if [[ -n "$PROBE_PID" ]] && kill -0 "$PROBE_PID" 2>/dev/null; then
    kill "$PROBE_PID" 2>/dev/null || true
    wait "$PROBE_PID" 2>/dev/null || true
  fi
  for pid in "${_EXTRA_PF_PIDS[@]}"; do
    kill "$pid" 2>/dev/null || true
  done
  rm -f "$PROBE_LOG" 2>/dev/null || true
  stop_port_forwards
}
trap _rolling_cleanup EXIT

# ── Test functions ───────────────────────────────────────────────────

test_replicas_configured() {
  local desired
  desired=$(kube get deployment "$DAEMON_DEPLOY" -o jsonpath='{.spec.replicas}' 2>/dev/null)
  log "Desired replicas: $desired"
  assert_ge "${desired:-0}" 2
}

test_rolling_update_strategy() {
  local strategy max_unavail
  strategy=$(kube get deployment "$DAEMON_DEPLOY" -o jsonpath='{.spec.strategy.type}' 2>/dev/null)
  max_unavail=$(kube get deployment "$DAEMON_DEPLOY" -o jsonpath='{.spec.strategy.rollingUpdate.maxUnavailable}' 2>/dev/null)
  log "Strategy: $strategy, maxUnavailable: $max_unavail"
  [[ "$strategy" == "RollingUpdate" ]] && [[ "$max_unavail" == "0" ]]
}

# Start a self-healing probe loop that re-establishes port-forwards on failure.
#
# kubectl port-forward svc/... connects to ONE backend pod. When that pod is
# terminated during a rolling update, the port-forward dies — even though the
# service still has healthy endpoints (maxUnavailable=0 guarantees this).
#
# This probe loop detects port-forward death and re-creates it on a new local
# port, then continues probing. A brief reconnect window (~1-2s) is expected
# and accounted for: probes that fail during reconnect are logged as "reconnect"
# and not counted as true failures.
start_probe_loop() {
  local initial_port="$1"
  local svc_target="$2"  # e.g. "svc/gastown-rwx-bd-daemon-daemon"
  local ns="$3"
  (
    current_port="$initial_port"
    pf_pid=""
    reconnecting=false
    reconnect_start=0

    # Helper: start a fresh port-forward, return new local port
    _reconnect_pf() {
      local new_port
      new_port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("",0)); print(s.getsockname()[1]); s.close()')
      kubectl port-forward -n "$ns" "$svc_target" "${new_port}:9080" >/dev/null 2>&1 &
      pf_pid=$!
      echo "$pf_pid" >> /tmp/rolling-upgrade-pf-pids.$$
      # Wait up to 10s for the port-forward to be ready
      local deadline=$((SECONDS + 10))
      while [[ $SECONDS -lt $deadline ]]; do
        if python3 -c "import socket; s=socket.socket(); s.settimeout(1); s.connect(('127.0.0.1',$new_port)); s.close()" 2>/dev/null; then
          echo "$new_port"
          return 0
        fi
        if ! kill -0 "$pf_pid" 2>/dev/null; then
          return 1
        fi
        sleep 0.3
      done
      return 1
    }

    while true; do
      t_start=$(python3 -c 'import time; print(int(time.time()*1000))' 2>/dev/null || echo "0")
      # curl -w outputs http_code even on connection failure (as "000").
      # Capture separately to avoid concatenation with fallback echo.
      code=""
      code=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 2 --max-time 5 \
        "http://127.0.0.1:${current_port}/healthz" 2>/dev/null) || code="000"
      t_end=$(python3 -c 'import time; print(int(time.time()*1000))' 2>/dev/null || echo "0")
      latency_ms=$(( t_end - t_start ))

      if [[ "$code" == "200" ]]; then
        echo "${t_start} ${code} ${latency_ms}" >> "$PROBE_LOG"
        reconnecting=false
      elif [[ "$code" == "000" || -z "$code" ]]; then
        # Port-forward died. Reconnect immediately before continuing probes.
        # This blocks the probe loop during reconnect (~1-3s), which is
        # correct: we're testing the *service*, not the local port-forward.
        reconnect_start=$t_start
        local reconnected=false
        for attempt in 1 2 3 4 5; do
          new_port=$(_reconnect_pf) && {
            current_port="$new_port"
            reconnected=true
            break
          }
          sleep 0.5
        done
        t_reconnected=$(python3 -c 'import time; print(int(time.time()*1000))' 2>/dev/null || echo "0")
        reconnect_ms=$(( t_reconnected - reconnect_start ))
        if [[ "$reconnected" == "true" ]]; then
          echo "${t_start} reconnect ${reconnect_ms}" >> "$PROBE_LOG"
        else
          # Could not reconnect after 5 attempts — this IS a real failure
          echo "${t_start} 000 ${reconnect_ms}" >> "$PROBE_LOG"
        fi
      else
        # Non-200, non-000: real HTTP error (e.g. 500, 503)
        echo "${t_start} ${code} ${latency_ms}" >> "$PROBE_LOG"
      fi
      sleep 0.5
    done
  ) &
  PROBE_PID=$!
  PROBE_RUNNING=true
}

test_zero_downtime() {
  # 1. Set up port-forward to daemon service
  local svc_port svc_target
  svc_target="svc/$DAEMON_SVC"
  svc_port=$(start_port_forward "$svc_target" 9080) || {
    log "Failed to start port-forward to $DAEMON_SVC"
    return 1
  }

  # 2. Verify service is reachable before starting
  local pre_code
  pre_code=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 5 \
    "http://127.0.0.1:${svc_port}/healthz" 2>/dev/null || echo "000")
  if [[ "$pre_code" != "200" ]]; then
    log "Service not reachable before test (HTTP $pre_code)"
    return 1
  fi

  # 3. Record pre-rollout restart counts
  local pre_restarts
  pre_restarts=$(kube get pods -l "app.kubernetes.io/component=daemon" --no-headers \
    -o custom-columns=':status.containerStatuses[0].restartCount' 2>/dev/null \
    | paste -sd+ | bc 2>/dev/null || echo "0")
  log "Pre-rollout total restarts: $pre_restarts"

  # 4. Start self-healing probe loop
  start_probe_loop "$svc_port" "$svc_target" "$NS"
  log "Probe loop started (PID $PROBE_PID), collecting baseline..."
  sleep 3  # Collect a few baseline probes

  # 5. Trigger rolling restart
  log "Triggering rolling restart of $DAEMON_DEPLOY..."
  kube rollout restart deployment "$DAEMON_DEPLOY" >/dev/null 2>&1

  # 6. Wait for rollout to complete (up to 5 minutes)
  local rollout_deadline=$((SECONDS + 300))
  local rollout_done=false
  while [[ $SECONDS -lt $rollout_deadline ]]; do
    if kube rollout status deployment "$DAEMON_DEPLOY" --timeout=10s >/dev/null 2>&1; then
      rollout_done=true
      break
    fi
    sleep 5
  done

  # 7. Collect a few more probes after rollout completes
  sleep 3

  # 8. Stop probe loop
  kill "$PROBE_PID" 2>/dev/null || true
  wait "$PROBE_PID" 2>/dev/null || true
  PROBE_PID=""
  PROBE_RUNNING=false

  # Clean up any extra port-forward PIDs from the probe loop
  if [[ -f /tmp/rolling-upgrade-pf-pids.$$ ]]; then
    while read -r pid; do
      kill "$pid" 2>/dev/null || true
      _EXTRA_PF_PIDS+=("$pid")
    done < /tmp/rolling-upgrade-pf-pids.$$
    rm -f /tmp/rolling-upgrade-pf-pids.$$
  fi

  # 9. Analyze probe results
  local total_probes=0
  local success_probes=0
  local fail_probes=0
  local reconnect_events=0
  local max_latency=0

  while IFS=' ' read -r ts code latency; do
    [[ -z "$ts" ]] && continue
    total_probes=$((total_probes + 1))
    if [[ "$code" == "200" ]]; then
      success_probes=$((success_probes + 1))
    elif [[ "$code" == "reconnect" ]]; then
      reconnect_events=$((reconnect_events + 1))
    else
      fail_probes=$((fail_probes + 1))
      log "  PROBE FAILURE: ts=$ts code=$code latency=${latency}ms"
    fi
    if [[ "$latency" =~ ^[0-9]+$ ]] && [[ "$latency" -gt "$max_latency" ]]; then
      max_latency=$latency
    fi
  done < "$PROBE_LOG"

  log "Probe results: ${success_probes}/${total_probes} success, ${fail_probes} failed, ${reconnect_events} reconnects, max latency ${max_latency}ms"

  if [[ "$rollout_done" != "true" ]]; then
    log "Rollout did not complete within 5 minutes"
    return 1
  fi

  # Zero real failures required (reconnects are expected during port-forward failover)
  [[ "$fail_probes" -eq 0 ]] && [[ "$success_probes" -gt 0 ]]
}

test_post_rollout_healthy() {
  local ready total
  ready=$(kube get deployment "$DAEMON_DEPLOY" -o jsonpath='{.status.readyReplicas}' 2>/dev/null)
  total=$(kube get deployment "$DAEMON_DEPLOY" -o jsonpath='{.spec.replicas}' 2>/dev/null)
  log "Post-rollout ready: ${ready:-0}/${total:-0}"
  [[ "${ready:-0}" -eq "${total:-0}" ]] && [[ "${ready:-0}" -ge 2 ]]
}

test_rollout_completed() {
  # Verify no pending rollout
  kube rollout status deployment "$DAEMON_DEPLOY" --timeout=10s >/dev/null 2>&1
}

test_no_crash_loops() {
  local pods restarts
  pods=$(kube get pods -l "app.kubernetes.io/component=daemon" --no-headers \
    -o custom-columns=':metadata.name,:status.containerStatuses[0].restartCount' 2>/dev/null)
  while IFS=$'\t' read -r pod count; do
    # New pods from rollout will have 0 restarts
    log "Pod $pod restarts: ${count:-0}"
    if [[ "${count:-0}" -gt 3 ]]; then
      log "Pod $pod has too many restarts (${count})"
      return 1
    fi
  done <<< "$pods"
  return 0
}

# ── Run tests ──────────────────────────────────────────────────────────

run_test "Daemon has >= 2 replicas configured" test_replicas_configured
run_test "RollingUpdate with maxUnavailable=0" test_rolling_update_strategy
run_test "Zero dropped requests during rolling restart" test_zero_downtime
run_test "All replicas healthy after rollout" test_post_rollout_healthy
run_test "Rollout completed within timeout" test_rollout_completed
run_test "No crash-loops introduced by rollout" test_no_crash_loops

print_summary

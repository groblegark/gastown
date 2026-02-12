# Gastown E2E Test Suite

End-to-end health and capability tests for a gastown K8s namespace.

## Prerequisites

- `kubectl` configured with cluster access
- Target namespace deployed via Helm (gastown chart)
- Node.js + npm (for Playwright mux dashboard tests)
- `bd` CLI (for formula-driven test runs and bug-filing)

## Quick Start

```bash
# Run all tests against gastown-next
./scripts/run-suite.sh --namespace gastown-next

# Run only infrastructure health tests
./scripts/run-suite.sh --namespace gastown-next --skip agent-spawn --skip agent-state \
  --skip agent-io --skip agent-credentials --skip agent-resume \
  --skip agent-multi --skip agent-coordination --skip agent-cleanup

# Run a single module
./scripts/run-suite.sh --namespace gastown-next --only dolt-health

# Include Playwright mux dashboard tests
./scripts/run-suite.sh --namespace gastown-next --with-mux

# JSON output (for CI)
./scripts/run-suite.sh --namespace gastown-next --json
```

## Test Modules

### Phase 1: Infrastructure Health

| Module | Tests | Description |
|--------|-------|-------------|
| `dolt-health` | 7 | Pod ready, SQL port, auth, beads DB, deploy config, s3-sync |
| `redis-health` | 5 | Pod ready, service, PING/PONG, port-forward, INFO |
| `daemon-health` | 8 | Pod 2/2, containers, HTTP health, RPC, status API, NATS |
| `coop-broker-health` | 8 | Pod 2/2, containers, health, mux HTML, pods API, auth |
| `controller-health` | 8 | Deployment, pod, ServiceAccount, agent pods, image |
| `git-mirror-health` | 6 | Pod, service, daemon, bare repo, HEAD, PVC (skips if not deployed) |

### Phase 2: Agent Capabilities

| Module | Tests | Description |
|--------|-------|-------------|
| `agent-spawn` | 10 | Pods exist, coop health, broker registration, volumes, image |
| `agent-state` | 6 | Valid state, health endpoint, all agents, broker, mux badges |
| `agent-io` | 7 | Screen JSON, input, keys, nudge, port isolation |
| `agent-credentials` | 8 | Secret, volume, mount, file, JSON, agent type, workspace |
| `agent-resume` | 6 | State dir, coop sessions, claude state, git, beads |
| `agent-multi` | 6 | 2+ pods, distinct names/IPs, broker registration |
| `agent-coordination` | 7 | Multi-agent coexistence, independent coop, role encoding |
| `agent-cleanup` | 5 | Labels, limits, restart policy, PVCs, crash loops |

### Phase 3: Mux Dashboard (Playwright)

| Spec | Tests | Description |
|------|-------|-------------|
| `mux.spec.js` | 29 | Page load, REST API, auth, live dashboard, WebSocket |

## Running Individual Modules

Each module is a standalone bash script:

```bash
# Set namespace and run directly
E2E_NAMESPACE=gastown-next ./scripts/test-dolt-health.sh

# Or pass namespace as argument
./scripts/test-dolt-health.sh gastown-next
```

## Formula-Driven Testing

Agents can run the full suite via formulas:

```bash
# Infrastructure validation only
gt formula run mol-gastown-e2e-bootstrap --var namespace=gastown-next

# Agent capability validation only
gt formula run mol-gastown-e2e-validate --var namespace=gastown-next

# Full suite (infrastructure + agents + mux)
gt formula run mol-gastown-e2e-full --var namespace=gastown-next
```

### Formulas

| Formula | Type | Description |
|---------|------|-------------|
| `mol-gastown-e2e-bootstrap` | workflow | Phase 1 infrastructure health |
| `mol-gastown-e2e-validate` | workflow | Phase 2 agent capabilities + Playwright |
| `mol-gastown-e2e-full` | workflow | Full suite orchestrator |

## Helper Scripts

| Script | Description |
|--------|-------------|
| `scripts/lib.sh` | Shared test harness (assertions, port-forward, summary) |
| `scripts/run-suite.sh` | Suite orchestrator (runs all modules sequentially) |
| `scripts/run-step.sh` | Single step runner with output capture and auto-bug-filing |
| `scripts/file-bug.sh` | Auto-files beads bugs for test failures |
| `scripts/provision-namespace.sh` | Helm-based namespace provisioning |

## Auto-Bug Filing

When running via formulas or `run-step.sh`, test failures automatically create
beads bug issues:

```bash
# Run with auto-bug-filing
./scripts/run-step.sh --module dolt-health --namespace gastown-next \
  --formula mol-e2e-bootstrap --step verify-dolt

# Disable auto-bug-filing
./scripts/run-step.sh --module dolt-health --namespace gastown-next --no-bug
```

Bugs are created with:
- Title: `E2E FAIL: <formula>:<step> in <namespace>`
- Priority: P1
- Type: bug
- Description includes test output (last 100 lines) and Playwright traces if available

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `E2E_NAMESPACE` | `gastown-next` | Target K8s namespace |
| `E2E_EPIC_ID` | (none) | Epic to link auto-filed bugs to |
| `BROKER_TOKEN` | (built-in) | Auth token for coop broker API |

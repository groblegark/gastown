# RWX CI/CD Migration Plan

**Bead**: bd-alyka | **Priority**: P1 | **Status**: Phases 0-5 Complete
**Date**: 2026-02-13 | **Updated**: 2026-02-13

## Problem

Today we build coop, beads, gastown, and their Helm charts + dependent Docker images
on GitHub Actions. It's slow. GitHub Actions runners are shared, builds are sequential
within jobs, and caching is fragile (GHA cache eviction, no content-based invalidation).

## Solution: RWX

RWX provides DAG-based task execution with content-based caching, right-sized compute,
and native OCI image building. We keep GitHub as VCS + release host; RWX replaces
Actions as the CI/CD execution engine.

**RWX CLI**: v3.3.0 installed at `/opt/homebrew/bin/rwx`

---

## Current Build Inventory

### Repos & What They Build

| Repo | Language | Docker Images | Helm Charts | Release Artifacts |
|------|----------|--------------|-------------|-------------------|
| **coop** (`groblegark/coop`) | Rust | `coop:empty`, `coop:claude`, `coop:gemini`, `coop:claudeless` | none | 4-platform tarballs (linux/mac x amd64/arm64) |
| **beads** (`groblegark/beads`) | Go (CGO) | `ghcr.io/groblegark/beads` (bd-daemon) | `bd-daemon` | GoReleaser: multi-platform binaries + npm + PyPI + Homebrew |
| **gastown** (`groblegark/gastown`) | Go | `ghcr.io/groblegark/gastown` (gt CLI), `gastown-agent`, `agent-controller`, `gastown-toolchain` (full+minimal) | `gastown` (depends on `bd-daemon`) | GoReleaser: multi-platform binaries + npm + Homebrew |

### Current GitHub Actions Workflows

**coop** (3 workflows):
- `ci.yml` — fmt, clippy, quench, test (linux+mac), audit, deny, docker build x3, docker-e2e
- `build.yml` — ECR push (disabled, manual only)
- `release.yml` — 4-platform Rust builds, GitHub Release, trigger gastown rebuild

**beads** (9 workflows):
- `ci.yml` — version check, beads-changes check, test (linux+mac+windows-smoke), lint, nix flake
- `docker.yml` — build+push bd-daemon to GHCR
- `helm.yml` — lint + publish bd-daemon chart to GHCR OCI
- `release.yml` — GoReleaser + PyPI + npm + Homebrew
- `fork-release.yml`, `deploy-docs.yml`, `mirror-ecr.yml`, `nightly.yml`, `test-pypi.yml`

**gastown** (13 workflows):
- `ci.yml` — beads-changes check, embedded-formulas check, test+coverage, lint, integration
- `docker.yml` — build+push gt CLI image to GHCR
- `docker-agent.yml` — build+push gastown-agent (downloads coop+bd from releases)
- `docker-controller.yml` — build+push agent-controller to GHCR
- `toolchain-image.yml` — build+push gastown-toolchain (full+minimal, multi-arch)
- `helm.yml` — lint + publish gastown chart (depends on bd-daemon chart) to GHCR OCI
- `release.yml` — GoReleaser + npm + Homebrew
- `fork-release.yml`, `build.yml`, `block-internal-prs.yml`, `e2e.yml`, `integration.yml`, `windows-ci.yml`

### Cross-Repo Dependencies

```
coop release ──────────────► gastown/docker-agent (repository_dispatch: coop-release)
beads release ─────────────► gastown/docker-agent (repository_dispatch: beads-release)
beads/helm (bd-daemon) ────► gastown/helm (gastown chart depends on bd-daemon chart)
```

---

## RWX Migration Strategy

### Phase 0: Setup & Credentials

1. **Install RWX GitHub App** on groblegark org: https://github.com/apps/rwx-integration
2. **Create RWX vault secrets**:
   - `GHCR_TOKEN` — GitHub token with `packages:write` for pushing Docker/Helm to GHCR
   - `NPM_TOKEN` — npm publish token
   - `PYPI_API_TOKEN` — PyPI publish token
   - `HOMEBREW_TAP_TOKEN` — PAT for homebrew-gastown/homebrew-beads repos
   - `DISPATCH_PAT` — PAT for cross-repo dispatch
   - `WINDOWS_SIGNING_CERT_PFX_BASE64`, `WINDOWS_SIGNING_CERT_PASSWORD` — code signing
3. **Configure OIDC token** for GHCR push (preferred over static tokens):
   - In RWX vault UI, create OIDC token named `ghcr` with audience `ghcr.io`
   - Use `${{ vaults.default.oidc.ghcr }}` in workflows
4. **Verify**: `rwx run` a hello-world task in each repo to confirm connectivity

### Phase 1: CI (Tests + Lint) — All Three Repos

This is the highest-impact phase. PR checks run on every push and are the main pain point.

#### 1a. coop (.rwx/ci.yml)

```yaml
on:
  github:
    push:
      if: ${{ event.git.branch == 'main' }}
      init:
        commit-sha: ${{ event.git.sha }}
    pull_request:
      init:
        commit-sha: ${{ event.git.sha }}

base:
  image: ubuntu:24.04
  config: rwx/base 1.0.0

tasks:
  - key: system-deps
    run: |
      sudo apt-get update
      sudo apt-get install -y protobuf-compiler tmux curl

  - key: code
    use: system-deps
    call: git/clone 2.0.3
    with:
      repository: https://github.com/groblegark/coop.git
      ref: ${{ init.commit-sha }}
      github-token: ${{ github.token }}

  - key: rust
    use: code
    run: |
      curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y
      . "$HOME/.cargo/env"
      rustup component add rustfmt clippy
      echo "$HOME/.cargo/bin" >> $RWX_ENV/PATH

  # Parallel lint tasks
  - key: fmt
    use: rust
    run: cargo fmt --all -- --check

  - key: clippy
    use: rust
    run: cargo clippy --all -- -D warnings

  - key: audit
    use: rust
    run: cargo install cargo-audit && cargo audit

  - key: deny
    use: rust
    run: cargo install cargo-deny && cargo deny check licenses bans sources

  # Tests — run after lint passes
  - key: test-linux
    use: [rust, clippy]
    run: cargo test --all

  - key: docker-build-empty
    use: code
    docker: true
    run: |
      docker build --target empty -t coop:empty .
      docker run --rm coop:empty --help

  - key: docker-build-claude
    use: code
    docker: true
    run: |
      docker build --target claude -t coop:claude .
      docker run --rm coop:claude --help
```

**Key wins**: fmt, clippy, audit, deny all run in parallel. Docker builds run in parallel
with tests. Content-based caching means `cargo` deps only re-download when Cargo.lock changes.

#### 1b. beads (.rwx/ci.yml)

```yaml
on:
  github:
    pull_request:
      init:
        commit-sha: ${{ event.git.sha }}
    push:
      if: ${{ event.git.branch == 'main' }}
      init:
        commit-sha: ${{ event.git.sha }}

base:
  image: ubuntu:24.04
  config: rwx/base 1.0.0

tasks:
  - key: system-deps
    run: |
      sudo apt-get update
      sudo apt-get install -y libicu-dev gcc g++

  - key: code
    use: system-deps
    call: git/clone 2.0.3
    with:
      repository: https://github.com/groblegark/beads.git
      ref: ${{ init.commit-sha }}
      github-token: ${{ github.token }}

  - key: go
    call: golang/install 1.2.0
    with:
      version: 1.24

  - key: go-deps
    use: [code, go]
    run: go mod download
    filter:
      - go.mod
      - go.sum

  - key: build
    use: go-deps
    run: |
      git config --global user.name "CI Bot"
      git config --global user.email "ci@beads.test"
      go build -v ./cmd/bd

  - key: lint
    use: go-deps
    run: |
      curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b $(go env GOPATH)/bin latest
      golangci-lint run --timeout=5m

  - key: test-linux
    use: build
    run: go test -v -race -short -coverprofile=coverage.out ./...

  - key: version-check
    use: code
    run: ./scripts/check-versions.sh

  - key: beads-changes-check
    use: code
    run: |
      # Only relevant for PRs — skip on push
      if [ -n "$PR_NUMBER" ]; then
        git fetch origin main
        if git diff --name-only origin/main...HEAD | grep -q "^\.beads/issues\.jsonl$"; then
          echo "ERROR: .beads/issues.jsonl changed in PR"
          exit 1
        fi
      fi
      echo "OK"

status-checks:
  - tasks: [lint, test-linux, build, version-check]
    name: CI
```

**Key wins**: `go mod download` cached by go.mod/go.sum filter. Lint and test run in
parallel. Build only re-runs when Go source changes.

#### 1c. gastown (.rwx/ci.yml)

```yaml
on:
  github:
    pull_request:
      init:
        commit-sha: ${{ event.git.sha }}
    push:
      if: ${{ event.git.branch == 'main' }}
      init:
        commit-sha: ${{ event.git.sha }}

base:
  image: ubuntu:24.04
  config: rwx/base 1.0.0

tasks:
  - key: code
    call: git/clone 2.0.3
    with:
      repository: https://github.com/groblegark/gastown.git
      ref: ${{ init.commit-sha }}
      github-token: ${{ github.token }}

  - key: go
    call: golang/install 1.2.0
    with:
      version: 1.24

  - key: go-deps
    use: [code, go]
    run: go mod download
    filter:
      - go.mod
      - go.sum

  - key: build
    use: go-deps
    run: |
      git config --global user.name "CI Bot"
      git config --global user.email "ci@gastown.test"
      go build -v ./cmd/gt

  - key: embedded-formulas
    use: go-deps
    run: |
      go generate ./internal/formula/...
      git diff --exit-code internal/formula/formulas/ || exit 1

  - key: lint
    use: go-deps
    run: |
      curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b $(go env GOPATH)/bin latest
      golangci-lint run --timeout=5m

  - key: test
    use: build
    run: |
      go test -race -short -coverprofile=coverage.out ./...

  - key: integration
    use: build
    run: |
      go install github.com/steveyegge/beads/cmd/bd@v0.47.1
      go test -tags=integration -timeout=5m -v ./internal/cmd/...

status-checks:
  - tasks: [lint, test, integration, embedded-formulas]
    name: CI
```

### Phase 2: Docker Image Builds

Migrate Docker image building from GitHub Actions to RWX. Two approaches available:

**Option A: Docker-in-Docker** (simplest, reuse existing Dockerfiles)
- Set `docker: true` on tasks, run `docker build` + `docker push`
- Pro: zero Dockerfile changes, immediate migration
- Con: slightly slower than native RWX images

**Option B: Native RWX OCI images** (fastest, no Docker daemon)
- Rewrite Dockerfiles as RWX task DAGs with `$RWX_IMAGE/` config
- Pro: content-based caching on every RUN step, faster
- Con: significant rewrite, unfamiliar patterns

**Recommendation**: Start with **Option A** (Docker-in-Docker) for all images. Migrate
high-traffic images (gastown-agent, bd-daemon) to native RWX later if needed.

#### 2a. beads/docker (.rwx/docker.yml)

```yaml
on:
  github:
    push:
      if: ${{ event.git.branch == 'main' || starts-with(event.git.ref, 'refs/tags/v') }}
      init:
        commit-sha: ${{ event.git.sha }}
        ref: ${{ event.git.ref }}
        branch: ${{ event.git.branch }}

base:
  image: ubuntu:24.04
  config: rwx/base 1.0.0

tasks:
  - key: code
    call: git/clone 2.0.3
    with:
      repository: https://github.com/groblegark/beads.git
      ref: ${{ init.commit-sha }}
      github-token: ${{ github.token }}

  - key: docker-build
    use: code
    docker: true
    run: |
      docker build \
        --build-arg VERSION=${{ init.ref }} \
        --build-arg BUILD_COMMIT=${{ init.commit-sha }} \
        -t ghcr.io/groblegark/beads:${{ init.commit-sha }} \
        -t ghcr.io/groblegark/beads:latest \
        .

  - key: docker-push
    use: docker-build
    docker: preserve-data
    run: |
      echo "$GHCR_TOKEN" | docker login ghcr.io -u groblegark --password-stdin
      docker push ghcr.io/groblegark/beads:${{ init.commit-sha }}
      docker push ghcr.io/groblegark/beads:latest
    env:
      GHCR_TOKEN: ${{ secrets.GHCR_TOKEN }}
```

#### 2b. gastown/docker-agent (.rwx/docker-agent.yml)

Same pattern — `docker: true`, build with Dockerfile, push to GHCR.

#### 2c. gastown/docker-controller (.rwx/docker-controller.yml)

Same pattern. The controller Dockerfile is simple (single Go binary, distroless base).

#### 2d. gastown/toolchain-image (.rwx/docker-toolchain.yml)

Multi-arch build. Use `docker buildx` with QEMU in an RWX task, or split into
two arch-specific tasks that run in parallel.

#### 2e. coop — no Docker push today

Coop CI builds Docker images for smoke testing only. No push to registry in CI.
The gastown-agent Dockerfile downloads coop from GitHub Releases, not from a registry.

### Phase 3: Helm Chart Publishing

#### 3a. beads/helm (.rwx/helm.yml)

```yaml
on:
  github:
    push:
      if: ${{ event.git.branch == 'main' || starts-with(event.git.ref, 'refs/tags/v') }}
      init:
        commit-sha: ${{ event.git.sha }}

base:
  image: ubuntu:24.04
  config: rwx/base 1.0.0

tasks:
  - key: code
    call: git/clone 2.0.3
    with:
      repository: https://github.com/groblegark/beads.git
      ref: ${{ init.commit-sha }}
      github-token: ${{ github.token }}

  - key: helm-setup
    run: |
      curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash

  - key: helm-lint
    use: [code, helm-setup]
    run: |
      for chart in helm/*/; do
        if [ -f "$chart/Chart.yaml" ]; then
          helm lint "$chart"
          helm template "$(basename $chart)" "$chart" > /dev/null
        fi
      done

  - key: helm-publish
    use: helm-lint
    run: |
      echo "$GHCR_TOKEN" | helm registry login ghcr.io -u groblegark --password-stdin
      for chart in helm/*/; do
        if [ -f "$chart/Chart.yaml" ]; then
          name=$(basename "$chart")
          version=$(grep '^version:' "$chart/Chart.yaml" | awk '{print $2}')
          helm dependency build "$chart" 2>/dev/null || true
          helm package "$chart" --destination /tmp/helm-packages
          helm push "/tmp/helm-packages/${name}-${version}.tgz" "oci://ghcr.io/groblegark/charts"
        fi
      done
    env:
      GHCR_TOKEN: ${{ secrets.GHCR_TOKEN }}
```

#### 3b. gastown/helm (.rwx/helm.yml)

Same pattern, but `helm dependency build` needs to pull `bd-daemon` chart from GHCR
(requires registry login before dep build).

### Phase 4: Release Workflows

Releases are triggered by tag pushes. These are the most complex workflows.

#### 4a. coop release (.rwx/release.yml)

The coop release builds Rust binaries for 4 platforms. RWX can parallelize all 4 builds:

```yaml
on:
  github:
    push:
      if: ${{ starts-with(event.git.ref, 'refs/tags/v') }}
      init:
        commit-sha: ${{ event.git.sha }}
        tag: ${{ event.git.tag }}

tasks:
  # ... clone, then 4 parallel build tasks:
  - key: build-linux-amd64
    use: [code, rust]
    run: cargo build --release --target x86_64-unknown-linux-gnu && ...

  - key: build-linux-arm64
    use: [code, rust]
    run: |
      sudo apt-get install -y gcc-aarch64-linux-gnu
      cargo build --release --target aarch64-unknown-linux-gnu && ...

  - key: build-darwin-amd64
    # Needs macOS runner — RWX supports this?
    # Fallback: cross-compile from Linux

  - key: build-darwin-arm64
    # Same consideration

  - key: release
    use: [build-linux-amd64, build-linux-arm64, ...]
    run: |
      # Create GitHub release with gh CLI
      # Trigger gastown rebuild via repository_dispatch
```

**Challenge**: macOS builds. RWX runs on Linux VMs. Cross-compiling Rust for macOS
from Linux requires either:
- Cross-compilation toolchain (osxcross) — complex for Rust
- Keep macOS builds on GitHub Actions, move Linux builds to RWX
- RWX may support macOS runners (check with RWX team)

**Recommendation**: Keep release workflows on GitHub Actions initially. The release
path is not the bottleneck — it runs infrequently and GoReleaser/cross-compile
works well on GHA. Focus RWX on the high-frequency CI path.

#### 4b. beads release & gastown release

Same consideration — GoReleaser handles multi-platform builds well on GHA.
The npm/PyPI/Homebrew publish steps are simple and don't benefit from RWX.

### Phase 5: Cross-Repo Orchestration

Today gastown's `docker-agent.yml` is triggered by `repository_dispatch` events from
coop and beads releases. With RWX:

**Option A**: Keep `repository_dispatch` — RWX doesn't need to handle cross-repo triggers.
The coop/beads release (on GHA) dispatches to gastown, which triggers an RWX run.
RWX responds to the GitHub event like any other push/PR.

**Option B**: RWX dispatch API — Use `rwx dispatch` CLI to trigger cross-repo builds
from within an RWX task. Reference: `/docs/rwx/api/dispatches`.

**Recommendation**: Option A. The dispatch pattern already works. Just make sure
gastown's `.rwx/` workflows respond to the same GitHub events.

---

## Migration Ordering

```
Phase 0: Setup ✅ COMPLETE
├── Install RWX GitHub App on groblegark ✅
├── Create vault + secrets (GHCR_TOKEN, GITHUB_USER) ✅
└── Verify connectivity ✅

Phase 1: CI ✅ COMPLETE
├── 1a. coop CI ✅    (parallel: fmt/clippy/audit/deny/test)
├── 1b. beads CI ✅   (parallel: lint/test/build/version-check)
└── 1c. gastown CI ✅ (parallel: lint/test/integration/formulas)

Phase 2: Docker builds ✅ COMPLETE
├── 2a. beads docker ✅   (bd-daemon image → GHCR)
├── 2b. coop docker ✅    (empty/claude/gemini images → GHCR)
├── 2c. gastown gt ✅     (gt CLI image → GHCR)
├── 2d. gastown agent ✅  (gastown-agent image → GHCR)
├── 2e. gastown controller ✅ (agent-controller → GHCR)
└── 2f. gastown toolchain ✅  (gastown-toolchain → GHCR)

Phase 3: Helm ✅ COMPLETE
├── 3a. beads helm ✅  (bd-daemon:0.2.8 → oci://ghcr.io/groblegark/charts)
└── 3b. gastown helm ✅ (gastown:0.5.12 → oci://ghcr.io/groblegark/charts)

Phase 4: Releases ✅ COMPLETE
├── 4a. coop release ✅    (parallel linux amd64/arm64 Rust builds, gh CLI release)
├── 4b. beads release ✅   (GoReleaser snapshot/release, dispatch to gastown)
└── 4c. gastown release ✅ (GoReleaser snapshot/release)

Phase 5: Cross-repo orchestration ✅ COMPLETE
├── coop release → dispatch gastown agent rebuild ✅
└── beads release → dispatch gastown agent rebuild ✅
```

### Implementation Notes

**Key RWX lesson**: `docker: preserve-data` does NOT carry Docker images between
tasks. Build and push must happen in the same task. All Docker workflows use a
per-image build+push-in-one-task pattern with conditional push (`if REF_NAME != pr`).

**CLI triggers**: Always include `ref-name: cli` in the `cli:` init section so
that `${{ init.ref-name }}` resolves correctly for local `rwx run` invocations.

**RWX packages**: `golang/install` param is `go-version` (not `version`).
`rwx/base 1.0.0` sets `RUSTC_WRAPPER=sccache` which breaks Rust — unset it via
`$RWX_ENV/RUSTC_WRAPPER`.

**Release workflows**: `git/clone` strips `.git` by default. Release workflows need
`preserve-git-dir: true` and `fetch-full-depth: true` for GoReleaser changelog.
Install Go inline (not via `golang/install` package) to avoid layer merge issues
that lose the git working directory. Use `--snapshot` flag for CLI dry runs.

**sudo required**: `/usr/local/bin` is not writable by default in RWX containers.
Use `sudo tar xz -C /usr/local/bin` for installing goreleaser, gh CLI, etc.

## What Stays on GitHub Actions

| Workflow | Why |
|----------|-----|
| docker-agent.yml (gastown) | repository_dispatch for coop/beads release triggers |
| deploy-docs.yml | GitHub Pages deployment |
| mirror-ecr.yml | AWS ECR mirror |
| nightly.yml | Cron — can move to RWX later |
| block-internal-prs.yml | Simple PR label check |
| Windows smoke tests | Windows runner needed |

## What Moved to RWX

| Workflow | RWX File | Impact |
|----------|----------|--------|
| CI (all repos) | `.rwx/ci.yml` | **HIGH** — runs on every push/PR |
| Docker builds (beads, gastown x4, coop x3) | `.rwx/docker.yml` | **MEDIUM** — runs on main push |
| Helm publish (beads, gastown) | `.rwx/helm.yml` | **LOW** — runs on main push |
| Releases (all repos) | `.rwx/release.yml` | **LOW** — runs on tag push |

## Key RWX Concepts to Leverage

1. **Content-based caching**: Go mod download, Cargo deps, npm install — all cached
   automatically when lock files don't change
2. **DAG parallelism**: lint/test/audit run simultaneously instead of sequentially
3. **File filtering**: `filter: [go.mod, go.sum]` makes dep-install cache-friendly
4. **Task `use` chaining**: build depends on deps, test depends on build — RWX
   manages ordering automatically
5. **`docker: true`**: Docker-in-Docker for existing Dockerfiles with zero rewrite
6. **Embedded runs**: Compose CI + Docker + Helm into a single orchestrated pipeline

## Risks & Mitigations

| Risk | Mitigation |
|------|-----------|
| RWX doesn't support macOS runners | Keep releases on GHA; only CI/Docker/Helm on RWX |
| GHCR auth from RWX | Use OIDC tokens or vault secrets for `docker login` |
| Content-based cache misses | Use `filter` on all dependency-install tasks |
| RWX outage blocks merges | Keep GHA workflows as fallback (don't delete, just disable) |
| beads CGO (icu4c) on RWX | Use `apt-get install libicu-dev` in system-deps task |
| coop protobuf compiler | `apt-get install protobuf-compiler` in system-deps |

## Local Testing

Test any workflow locally before committing:

```bash
# Test beads CI
cd ~/beads
rwx run .rwx/ci.yml --init commit-sha=$(git rev-parse HEAD) --open

# Test gastown CI
cd ~/gastown
rwx run .rwx/ci.yml --init commit-sha=$(git rev-parse HEAD) --open
```

## Success Criteria

- PR checks complete in < 5 minutes (current: ~10-15 min)
- Docker builds cached when Dockerfile/source unchanged
- Helm charts publish on main push
- Zero regression: all existing checks still gate merges
- Fallback: GHA workflows still exist (disabled) if RWX has issues

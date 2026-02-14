# CI/CD has moved to RWX

All CI/CD pipelines have been migrated from GitHub Actions to [RWX](https://www.rwx.com/).
Workflow configs live in the `.rwx/` directory at the repo root.

The `.yml.disabled` files in this directory are kept for reference only.

## RWX Workflows

| RWX Config | What it does | Replaces (GitHub Actions) |
|---|---|---|
| `.rwx/ci.yml` | Build, lint, test, integration tests, embedded-formula check | `build.yml` (partially), `integration.yml` |
| `.rwx/docker.yml` | Build and push Docker images (gt CLI, agent-controller, agent, toolchain) to GHCR | `docker-agent.yml`, `build.yml` (ECR push) |
| `.rwx/helm.yml` | Lint and publish Helm charts to GHCR OCI | _(new -- no prior GitHub Actions equivalent)_ |
| `.rwx/release.yml` | GoReleaser binary release on tag push | `release.yml` |

## Not yet covered by RWX

| Former GitHub Workflow | Status |
|---|---|
| `windows-ci.yml` | Was already disabled (manual-trigger only). No RWX equivalent needed unless Windows support is prioritized. |
| `e2e.yml` | Requires K8s cluster + AWS OIDC. No RWX equivalent yet. |
| `block-internal-prs.yml` | Repo policy enforcement (auto-close internal PRs). Not applicable on a fork. |

# Gastown Toolchain Sidecar Image

Base image for K8s agent pod toolchain sidecars.

## Build

Images are built via RWX native tasks (no Dockerfile). See `.rwx/docker.yml` task `image-toolchain`.

```bash
# Build and push via RWX CLI
rwx image build -f .rwx/docker.yml --target image-toolchain \
  --push-to ghcr.io/groblegark/gastown/gastown-toolchain:latest

# Or run locally
rwx image build -f .rwx/docker.yml --target image-toolchain \
  --tag gastown-toolchain:full
```

## Tools included

Go, Node.js, Python, AWS CLI, Docker CLI (client only), Rust Analyzer, gopls, git, jq, make, curl.

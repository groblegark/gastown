# Generic Gas Town agent image for all roles (mayor, deacon, crew, polecat, etc.).
#
# Runs a screen session with Claude agent. The GT_ROLE env var determines
# role-specific behavior in the entrypoint script. The controller's pod
# manager sets all required env vars at pod creation time.
#
# Build: docker build -t gastown-agent -f deploy/agent/Dockerfile .
# Run:   docker run -e GT_ROLE=mayor -e GT_AGENT=hq gastown-agent

FROM ubuntu:24.04

# Avoid interactive prompts during package installation
ENV DEBIAN_FRONTEND=noninteractive

# Install dependencies: screen (terminal multiplexer), git, and common tools
RUN apt-get update && apt-get install -y --no-install-recommends \
    screen \
    git \
    curl \
    ca-certificates \
    jq \
    && rm -rf /var/lib/apt/lists/*

# Create agent user (UID/GID 1000 to match pod security context)
RUN groupadd -g 1000 agent \
    && useradd -m -u 1000 -g 1000 -s /bin/bash agent \
    && mkdir -p /home/agent/gt \
    && chown -R agent:agent /home/agent

# Install gt and bd binaries (built externally, copied in)
COPY deploy/agent/bin/gt /usr/local/bin/gt
COPY deploy/agent/bin/bd /usr/local/bin/bd

# Install Claude CLI (placeholder - actual install depends on distribution method)
# For now, assume claude is available via npm or pre-built binary
# COPY deploy/agent/bin/claude /usr/local/bin/claude

# Copy entrypoint script
COPY deploy/agent/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# Workspace volume mount point
VOLUME /home/agent/gt

# Run as agent user
USER agent
WORKDIR /home/agent/gt

# Environment defaults (overridden by pod spec)
ENV HOME=/home/agent
ENV GT_ROLE=unknown
ENV GT_AGENT=unknown

ENTRYPOINT ["/entrypoint.sh"]

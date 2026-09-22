FROM cloche-agent:latest
USER root

# Bract is Python, stdlib only — no pip packages, ever (that's a standing
# constraint of the seed, see DESIGN.md). python3 alone is the whole runtime.
RUN apt-get update && apt-get install -y --no-install-recommends python3 \
    && rm -rf /var/lib/apt/lists/*

# Claude Code is the default agent_command for prompt steps in this seed.
# Debian bookworm's plain nodejs/npm packages have broken interdependencies
# on this base image (apt refuses with "held broken packages"); NodeSource's
# setup script avoids it (same method arm-a's Dockerfile and the main cloche
# repo's own Dockerfile use).
RUN apt-get update && apt-get install -y --no-install-recommends curl \
    && curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/* \
    && npm install -g @anthropic-ai/claude-code

USER agent

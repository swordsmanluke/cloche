FROM cloche-agent:latest
USER root

# Bract is Python, stdlib only — no pip packages, ever (that's a standing
# constraint of the seed, see DESIGN.md). python3 alone is the whole runtime.
RUN apt-get update && apt-get install -y --no-install-recommends python3 \
    && rm -rf /var/lib/apt/lists/*

# Claude Code is the default agent_command for prompt steps in this seed.
# Arm overlays / the bonsai executor wrapper (cloche-usjb) may point
# agent_command elsewhere for actual experiment runs.
RUN apt-get update && apt-get install -y --no-install-recommends nodejs npm \
    && rm -rf /var/lib/apt/lists/* \
    && npm install -g @anthropic-ai/claude-code

USER agent

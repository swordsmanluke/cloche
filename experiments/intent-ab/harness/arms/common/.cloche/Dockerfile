FROM cloche-agent:latest
USER root

# Overlay (E5) on top of seed/.cloche/Dockerfile: both arms run the bonsai
# executor wrapper (E4), never Claude Code, so the seed's Node/npm/
# @anthropic-ai/claude-code install block is dropped — it's dead weight and
# an unnecessary network dependency at image-build time for these arms.
# The wrapper itself is stdlib-only Python; the entire project root
# (including bin/ and the agent_command/ package, copied in by the driver
# before this overlay is applied) is copied into /workspace by Cloche at
# container start, so nothing beyond the interpreter needs to be baked in.
RUN apt-get update && apt-get install -y --no-install-recommends python3 \
    && rm -rf /var/lib/apt/lists/*

USER agent

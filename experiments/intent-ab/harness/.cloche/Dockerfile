FROM cloche-agent:latest
USER root

# The bonsai executor wrapper (bin/agent_command) is stdlib-only Python; the
# entire project root (including bin/ and the agent_command/ package) is
# copied into /workspace by Cloche at container start, so nothing needs to
# be baked into the image beyond the interpreter itself.
RUN apt-get update && apt-get install -y --no-install-recommends python3 \
    && rm -rf /var/lib/apt/lists/*

USER agent

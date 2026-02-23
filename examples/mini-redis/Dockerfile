FROM debian:bookworm-slim

# C toolchain + validation tools
RUN apt-get update && \
    apt-get install -y \
        gcc make \
        valgrind cppcheck \
        nodejs npm git \
    && rm -rf /var/lib/apt/lists/*

# Claude Code CLI
RUN npm install -g @anthropic-ai/claude-code

# Copy cloche-agent binary from host build
COPY --from=cloche-agent:latest /usr/local/bin/cloche-agent /usr/local/bin/cloche-agent

# Non-root user
RUN useradd -m -s /bin/bash agent
WORKDIR /workspace
RUN chown agent:agent /workspace
USER agent

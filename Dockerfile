FROM cloche-base:latest
USER root
RUN apt-get update \
    && apt-get install -y --no-install-recommends nodejs npm golang-go \
    && rm -rf /var/lib/apt/lists/*
RUN npm install -g @anthropic-ai/claude-code
USER agent

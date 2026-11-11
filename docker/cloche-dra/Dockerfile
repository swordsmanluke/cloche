# Image for DRAs (Domain Responsible Agents), run in the locked-down profile
# (docs/dras.md, CLOCHE-A118). Deliberately small:
#   in:  cloche-agent, cloche, clo, saga, the model CLI (claude)
#   out: go, gcc, make, git, any compiler; the tracker CLI (bd, gh); git push
#        credentials; tracker data. A DRA reaches the tracker only through the
#        daemon.
# Build:  docker build -f docker/cloche-dra/Dockerfile \
#           --build-arg SAGA_URL=<saga release tarball URL> -t cloche-dra:latest .

FROM golang:1.25 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /cloche-agent ./cmd/cloche-agent \
    && CGO_ENABLED=0 go build -o /cloche ./cmd/cloche \
    && CGO_ENABLED=0 go build -o /clo ./cmd/clo

# saga is fetched as a prebuilt static binary (tarball containing `saga`).
FROM debian:12-slim AS saga
ARG SAGA_URL
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates \
    && rm -rf /var/lib/apt/lists/*
RUN set -e; \
    if [ -z "$SAGA_URL" ]; then echo "SAGA_URL build arg is required" >&2; exit 1; fi; \
    mkdir /out && curl -fsSL "$SAGA_URL" | tar -xz -C /out saga

FROM debian:12-slim
# Runtime tools only. curl/gnupg/nodejs install the model CLI, then are purged
# along with apt's build-ish extras; ca-certificates, gosu and jq stay.
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates gosu jq curl gnupg \
    && curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && npm install -g @anthropic-ai/claude-code \
    && apt-get purge -y --auto-remove curl gnupg \
    && rm -rf /var/lib/apt/lists/* /root/.npm
COPY --from=builder /cloche-agent /usr/local/bin/cloche-agent
COPY --from=builder /cloche /usr/local/bin/cloche
COPY --from=builder /clo /usr/local/bin/clo
COPY --from=saga /out/saga /usr/local/bin/saga
RUN useradd -m -s /bin/bash agent
WORKDIR /workspace
RUN chown agent:agent /workspace

# Image for DRAs (Domain Responsible Agents), run in the locked-down profile
# (docs/dras.md, CLOCHE-A118). Deliberately small:
#   in:  cloche-agent, cloche, clo, the model CLI (claude)
#   out: go, gcc, make, git, any compiler; the tracker CLI (bd, gh); git push
#        credentials; tracker data. A DRA reaches the tracker only through the
#        daemon.
#   A DRA asks questions with `cloche questions ask|await|read`.
# Build:  make docker-dra

FROM golang:1.25 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /cloche-agent ./cmd/cloche-agent \
    && CGO_ENABLED=0 go build -o /cloche ./cmd/cloche \
    && CGO_ENABLED=0 go build -o /clo ./cmd/clo

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
RUN useradd -m -s /bin/bash agent
WORKDIR /workspace
RUN chown agent:agent /workspace

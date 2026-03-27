FROM golang:1.25 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /cloche-agent ./cmd/cloche-agent \
    && CGO_ENABLED=0 go build -o /clo ./cmd/clo

FROM debian:12-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends git ca-certificates make gosu \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder /cloche-agent /usr/local/bin/cloche-agent
COPY --from=builder /clo /usr/local/bin/clo
RUN useradd -m -s /bin/bash agent
WORKDIR /workspace
RUN chown agent:agent /workspace

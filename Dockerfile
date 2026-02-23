FROM golang:1.25 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /cloche-agent ./cmd/cloche-agent

FROM ruby:3.3
RUN apt-get update && apt-get install -y nodejs npm python3 python3-pip git && rm -rf /var/lib/apt/lists/*
RUN pip3 install pyyaml --break-system-packages
RUN npm install -g @anthropic-ai/claude-code
COPY --from=builder /cloche-agent /usr/local/bin/cloche-agent
RUN useradd -m -s /bin/bash agent
WORKDIR /workspace
RUN chown agent:agent /workspace
USER agent

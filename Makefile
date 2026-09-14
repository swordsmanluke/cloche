.PHONY: build test lint clean proto docker-base docker-build install

PREFIX ?= $(HOME)/.local
VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo "dev")

build:
	go build -o bin/cloche ./cmd/cloche
	go build -o bin/cloched ./cmd/cloched
	go build -o bin/cloche-agent ./cmd/cloche-agent
	go build -o bin/clo ./cmd/clo

test:
	go test ./... -v

test-short:
	go test ./... -short

lint:
	go vet ./...

proto:
	mkdir -p api/clochepb
	protoc --proto_path=api/proto/cloche/v1 \
		--go_out=api/clochepb --go_opt=paths=source_relative \
		--go-grpc_out=api/clochepb --go-grpc_opt=paths=source_relative \
		api/proto/cloche/v1/cloche.proto

docker-base:
	docker build -f docker/cloche-base/Dockerfile \
		-t cloche-base:latest \
		-t cloche-base:$(VERSION) \
		.

docker-build: docker-base
	@# .cloche/Dockerfile has an optional COPY of .cloche/credentials/. The
	@# directory is gitignored and only present on cloche developer machines;
	@# create an empty placeholder so the COPY always has a source.
	@mkdir -p .cloche/credentials
	docker build -t cloche-agent:latest -f .cloche/Dockerfile .

install-sh: build docker-build
	@# Install binaries
	@mkdir -p $(PREFIX)/bin
	@echo "==> Installing to $(PREFIX)/bin/"
	@install bin/cloche bin/cloched bin/cloche-agent $(PREFIX)/bin/

install: build docker-build
	@# Stop running daemon (graceful via CLI, fallback to kill), then wait for
	@# it to actually exit rather than assuming a fixed sleep is enough. A
	@# daemon hung in shutdown can survive well past 1s, still holding its
	@# ports — starting the new daemon while that's true causes its web
	@# listener to fail to bind (see serveWebWithRetry in cmd/cloched).
	@echo "==> Stopping cloched..."
	@cloche shutdown 2>/dev/null || pkill -x cloched 2>/dev/null || true
	@for i in $$(seq 1 30); do \
		pgrep -x cloched > /dev/null || break; \
		sleep 1; \
	done
	@if pgrep -x cloched > /dev/null; then \
		echo "==> cloched still running after 30s, sending SIGKILL"; \
		pkill -9 -x cloched 2>/dev/null || true; \
		for i in $$(seq 1 10); do \
			pgrep -x cloched > /dev/null || break; \
			sleep 1; \
		done; \
	fi
	@if pgrep -x cloched > /dev/null; then \
		echo "==> ERROR: cloched did not exit, aborting install"; \
		exit 1; \
	fi
	@# Install binaries
	@mkdir -p $(PREFIX)/bin
	@echo "==> Installing to $(PREFIX)/bin/"
	@install bin/cloche bin/cloched bin/cloche-agent $(PREFIX)/bin/
	@# Restart daemon
	@echo "==> Starting cloched..."
	@nohup $(PREFIX)/bin/cloched > /tmp/cloched.log 2>&1 &
	@sleep 1
	@pgrep -x cloched > /dev/null && echo "==> cloched running (pid $$(pgrep -x cloched))" || (echo "==> ERROR: cloched failed to start, check /tmp/cloched.log" && exit 1)
	@echo "==> Done"

clean:
	rm -rf bin/

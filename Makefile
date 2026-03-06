.PHONY: build test lint clean proto docker-base docker-build install

PREFIX ?= $(HOME)/.local

build:
	go build -o bin/cloche ./cmd/cloche
	go build -o bin/cloched ./cmd/cloched
	go build -o bin/cloche-agent ./cmd/cloche-agent

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
	docker build -t cloche-agent:latest .

install: build docker-build
	@# Stop running daemon (graceful via CLI, fallback to kill)
	@echo "==> Stopping cloched..."
	@cloche shutdown 2>/dev/null || pkill -x cloched 2>/dev/null || true
	@sleep 1
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
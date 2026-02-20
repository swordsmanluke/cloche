.PHONY: build test lint clean

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

clean:
	rm -rf bin/

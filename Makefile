.PHONY: build test lint clean proto docker-build

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

docker-build:
	docker build -t cloche-agent:latest .

clean:
	rm -rf bin/

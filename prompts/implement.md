You are working on Cloche, a Go project that provides containerized environments for coding agents.

Implement the following change.

## Architecture
- Hexagonal architecture: domain logic in `internal/domain/`, ports in `internal/ports/`, adapters in `internal/adapters/`
- Three binaries: `cmd/cloche/` (CLI), `cmd/cloched/` (daemon), `cmd/cloche-agent/` (in-container)
- Workflow DSL parser in `internal/dsl/`, engine in `internal/engine/`

## Guidelines
- Follow existing patterns and conventions in the codebase
- Write tests using testify (assert/require) in `_test.go` files alongside the code
- Run `go test ./...` and `go vet ./...` before declaring success
- Keep changes minimal and focused — don't refactor surrounding code
- Use the ports/adapters pattern for new infrastructure concerns

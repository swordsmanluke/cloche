You are designing a mini Redis server in C before implementation.

Read the requirements in prompts/implement.md to understand the full scope.

Your job is to produce a detailed design document at `DESIGN.md` in the project
root. Do NOT write any C code yet. Only produce the design document.

## What the design document must cover

### Architecture
- Single-threaded event loop using `poll()` (not `select()`, not `epoll()`)
- How multiple concurrent client connections are managed
- Request/response lifecycle from socket read to write

### Data structures
- Hash table for the key-value store (define the hashing strategy, collision
  handling, and resize policy)
- How TTL/expiration is tracked and enforced (lazy expiration on access +
  periodic sweep)
- String representation (length-prefixed to handle binary data)

### RESP protocol parsing
- Define the exact wire format for each RESP type:
  - Simple Strings (+OK\r\n)
  - Errors (-ERR message\r\n)
  - Integers (:42\r\n)
  - Bulk Strings ($5\r\nHello\r\n and $-1\r\n for null)
  - Arrays (*2\r\n$3\r\nfoo\r\n$3\r\nbar\r\n)
- How partial reads are buffered per-connection
- How pipelined commands are parsed and executed

### Commands to support
- PING, ECHO
- GET, SET (with EX seconds option), DEL
- EXPIRE, TTL
- KEYS (with glob pattern: *, ?, [abc])
- EXISTS, TYPE
- INCR, DECR
- Command dispatch table design

### Memory management strategy
- Ownership rules: who allocates, who frees
- When hash table entries are freed (delete, overwrite, expiration)
- Connection buffer lifecycle
- How to avoid double-free and use-after-free

### File layout
- List each .h and .c file with its purpose
- Which functions are public (in headers) vs static

### Test strategy
- Unit tests for: RESP parser, hash table, glob matching, TTL expiration
- Integration tests for: command execution, pipelining, concurrent clients
- How to structure the test harness (test runner with assertions)
- Memory safety testing approach (valgrind)
- What specific edge cases each test covers

Write the design document clearly enough that a developer could implement the
full project from it without referring back to the original requirements.

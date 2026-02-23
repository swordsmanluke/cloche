You are building a mini Redis server in C. This is a single-threaded,
in-memory key-value store that speaks the RESP (Redis Serialization Protocol)
wire protocol over TCP.

A design document has been written at `DESIGN.md` in the project root. Read it
first — it defines the architecture, data structures, and file layout you should
follow. Implement the full project according to that design.

## Requirements

### Server
1. Single-threaded event loop using `poll()` for I/O multiplexing
2. Listen on a configurable port (default 6379)
3. Accept multiple concurrent client connections
4. Handle partial reads/writes correctly (buffer per connection)
5. Support command pipelining (multiple commands in one TCP segment)
6. Graceful shutdown on SIGINT/SIGTERM

### RESP protocol
7. Parse all RESP types: Simple Strings, Errors, Integers, Bulk Strings, Arrays
8. Handle null bulk strings ($-1\r\n)
9. Correctly handle commands split across multiple TCP reads
10. Correctly handle multiple commands in a single TCP read (pipelining)

### Commands
11. PING → +PONG\r\n (or PING <message> → bulk string echo)
12. ECHO <message> → bulk string reply
13. SET key value [EX seconds] → +OK\r\n
14. GET key → bulk string or null bulk string
15. DEL key [key ...] → integer (count of deleted keys)
16. EXISTS key [key ...] → integer (count of existing keys)
17. EXPIRE key seconds → integer (1 if set, 0 if key doesn't exist)
18. TTL key → integer (-2 if not exists, -1 if no expiry, else seconds)
19. KEYS pattern → array of matching keys (support *, ?, [abc] globs)
20. TYPE key → +string\r\n (only type is "string")
21. INCR key → integer (error if value is not an integer)
22. DECR key → integer (error if value is not an integer)

### Data store
23. Hash table with open addressing (linear probing or Robin Hood)
24. Automatic resize when load factor exceeds 0.7
25. Keys and values are binary-safe (length-prefixed, not null-terminated)
26. Lazy expiration: check TTL on every access
27. No background expiration sweep needed for v1

### Memory management
28. Zero memory leaks — every malloc has a corresponding free
29. No use-after-free, no double-free, no buffer overflows
30. All strings are length-prefixed (not relying on null terminators for data)
31. Connection buffers are freed on disconnect

### Build system
32. Makefile with targets: `all` (builds server), `test` (builds and runs tests),
    `test_bin` (builds test binary without running), `clean`
33. Compile with: `gcc -Wall -Wextra -Werror -pedantic -std=c11 -g`
34. Source files in `src/`, test files in `test/`
35. Test runner binary: `test_runner`

### Tests
36. RESP parser tests:
    - Parse each RESP type correctly
    - Handle incomplete/partial messages (return "need more data")
    - Handle malformed input
37. Hash table tests:
    - Insert, lookup, delete
    - Overwrite existing key
    - Resize triggers correctly
    - Iterator covers all entries
38. Glob pattern matching tests:
    - Literal match, * wildcard, ? wildcard, [abc] character class
    - Edge cases: empty pattern, empty string, consecutive wildcards
39. TTL/expiration tests:
    - Set expiry, check before/after expiration
    - Key access after expiry returns null
    - DEL removes expiry
    - Overwrite key resets expiry
40. Integration tests (connect to server over TCP):
    - Basic GET/SET round-trip
    - Command pipelining (send 3 commands, read 3 responses)
    - INCR on non-existent key (should create with value 1)
    - INCR on non-integer value (should return error)
    - SET with EX, wait, GET returns null
    - KEYS with various glob patterns
    - Multiple concurrent connections
41. The test harness should:
    - Start the server as a child process on a random high port
    - Connect as a client, run commands, verify responses
    - Kill the server after tests complete
    - Print clear pass/fail for each test case
    - Exit 0 if all pass, non-zero if any fail

## Guidelines
- Write standard C11. No compiler-specific extensions.
- No external dependencies — only libc and POSIX APIs.
- Must compile cleanly with `-Wall -Wextra -Werror -pedantic`
- Must pass valgrind with zero leaks and zero errors
- Keep functions short and focused. No function over 80 lines.

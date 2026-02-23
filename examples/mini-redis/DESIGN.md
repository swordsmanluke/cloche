# Mini Redis Server — Design Document

## 1. Architecture

### 1.1 Overview

The server is a single-threaded, in-memory key-value store that speaks the RESP
(Redis Serialization Protocol) over TCP. It uses `poll()` for I/O multiplexing
to handle multiple concurrent client connections without threads.

### 1.2 Event Loop

The core of the server is a `poll()`-based event loop. One `struct pollfd` entry
is reserved for the listening socket (index 0); the remaining entries track
connected clients.

```
main():
  create listening socket (SO_REUSEADDR, non-blocking)
  bind to 0.0.0.0:<port>
  listen(backlog=128)
  install SIGINT/SIGTERM handler that sets a global volatile sig_atomic_t flag

  while (!shutdown_flag):
    poll(fds, nfds, timeout=1000ms)

    if fds[0].revents & POLLIN:
      accept_new_client()          // may accept multiple in a loop

    for each client fd with POLLIN:
      read_from_client(client)     // append to client's read buffer
      parse_and_execute(client)    // parse RESP, execute commands, append to write buffer

    for each client fd with POLLOUT:
      flush_write_buffer(client)   // write as much as possible

    for each client fd with POLLERR|POLLHUP:
      close_client(client)

  cleanup: close all clients, free store, close listening socket
```

The `timeout` of 1000 ms ensures the loop wakes up periodically to check the
shutdown flag even when idle.

### 1.3 Connection Management

Each connection is tracked in a `client_t` struct:

```c
typedef struct {
    int fd;                 // socket file descriptor, -1 if slot is unused
    char *read_buf;         // dynamic input buffer
    size_t read_len;        // bytes currently in read_buf
    size_t read_cap;        // allocated capacity of read_buf
    char *write_buf;        // dynamic output buffer
    size_t write_len;       // bytes currently in write_buf
    size_t write_cap;       // allocated capacity of write_buf
} client_t;
```

A fixed-size array `client_t clients[MAX_CLIENTS]` is used (MAX_CLIENTS = 1024).
Unused slots have `fd == -1`. When `accept()` succeeds, the server scans for the
first unused slot. If none is available, the new fd is closed immediately.

The `struct pollfd` array is rebuilt before each `poll()` call:
- Index 0: listening socket, events = POLLIN.
- Index 1..N: one entry per active client. Events = POLLIN always; POLLOUT is
  added only when `write_len > 0` (there is data to send).

A parallel mapping array `int poll_to_client[MAX_CLIENTS + 1]` records which
client index each pollfd slot corresponds to.

### 1.4 Request/Response Lifecycle

1. **Socket read**: `recv()` into a stack buffer (4096 bytes), then append to
   `client->read_buf`, growing it as needed.
2. **Parse**: The RESP parser scans `read_buf[0..read_len]`. It returns:
   - The number of bytes consumed (a complete command was parsed), or
   - 0, meaning the data is incomplete (need more data).
3. **Execute**: If a complete command array was parsed, look up the command name
   in the dispatch table and call the handler. The handler appends its RESP
   response to `client->write_buf`.
4. **Pipeline loop**: After executing one command, the consumed bytes are removed
   from `read_buf` (via memmove), and parsing restarts from the beginning. This
   continues until the parser returns 0 (incomplete) or the buffer is empty.
5. **Socket write**: On the next `poll()` iteration (or the same one), when
   POLLOUT fires, call `send()` with `write_buf`. Advance past sent bytes with
   memmove. When `write_len` reaches 0, stop requesting POLLOUT.

Buffer compaction: after consuming data from read_buf, use `memmove()` to shift
remaining data to the start. Same for write_buf after a partial send. This is
simple and correct; buffers are typically small so the cost is negligible.

### 1.5 Signal Handling

A signal handler for SIGINT and SIGTERM sets `volatile sig_atomic_t g_shutdown = 1`.
The event loop checks this flag at the top of each iteration. On shutdown:
1. Stop accepting new connections.
2. Close all client connections (free their buffers).
3. Free the key-value store.
4. Close the listening socket.
5. Exit with code 0.

---

## 2. Data Structures

### 2.1 Binary-Safe String (`rstr_t`)

All keys and values use a length-prefixed string type to support binary data:

```c
typedef struct {
    char *data;     // heap-allocated, NOT necessarily null-terminated
    size_t len;     // number of bytes
} rstr_t;
```

Helper functions:

| Function | Description |
|---|---|
| `rstr_t rstr_create(const char *data, size_t len)` | Allocate and copy. |
| `rstr_t rstr_dup(rstr_t s)` | Deep copy. |
| `void rstr_free(rstr_t *s)` | Free data and zero the struct. |
| `bool rstr_eq(rstr_t a, rstr_t b)` | Compare len then memcmp. |

For convenience in building RESP responses, `data` is always allocated with one
extra byte set to `'\0'` beyond `len`, so it can be passed to functions expecting
C strings when the data is known to be text. The `len` field is the
authoritative length; the trailing null is never counted.

### 2.2 Hash Table

The key-value store is a hash table using **open addressing with linear probing**.

#### Entry structure

```c
typedef enum {
    ENTRY_EMPTY,        // slot has never been used
    ENTRY_OCCUPIED,     // slot holds a live key-value pair
    ENTRY_TOMBSTONE     // slot was deleted (needed for linear probing)
} entry_state_t;

typedef struct {
    entry_state_t state;
    rstr_t key;
    rstr_t value;
    int64_t expire_at;  // absolute expiration time (milliseconds since epoch)
                        // -1 means no expiration
} ht_entry_t;
```

#### Table structure

```c
typedef struct {
    ht_entry_t *entries;    // array of entries
    size_t capacity;        // total number of slots (always a power of 2)
    size_t count;           // number of OCCUPIED entries (excludes tombstones)
    size_t used;            // number of OCCUPIED + TOMBSTONE entries
} hashtable_t;
```

#### Hashing strategy

Use **FNV-1a** (32-bit) as the hash function. It is simple, fast, and has good
distribution for string keys:

```
hash = 2166136261  (FNV offset basis)
for each byte:
    hash ^= byte
    hash *= 16777619  (FNV prime)
return hash
```

Slot index: `hash & (capacity - 1)` (capacity is always a power of 2, so this
is equivalent to modulo).

#### Collision handling

Linear probing: on collision, advance to the next slot `(index + 1) & (capacity - 1)`.

- **Lookup**: Scan from the hashed slot. Skip TOMBSTONE entries. Stop at
  EMPTY. If an OCCUPIED entry has a matching key, check its expiration. If
  expired, convert it to a tombstone, free key/value, and return "not found".
- **Insert**: Scan from the hashed slot. Insert into the first EMPTY or
  TOMBSTONE slot. If the key already exists (OCCUPIED with matching key),
  overwrite the value and reset expire_at to -1.
- **Delete**: Find the entry via lookup. Free key and value. Set state to
  TOMBSTONE. Decrement `count` but do not decrement `used`.

#### Resize policy

- **Grow**: When `used` (occupied + tombstones) exceeds `capacity * 0.7`,
  resize to `capacity * 2`.
- **Initial capacity**: 64 slots.
- **Resize procedure**: Allocate a new array. Walk the old array. For each
  OCCUPIED entry, re-hash and insert into the new array (tombstones are
  discarded). Free the old array. Update `capacity`, `count`, and `used`
  (used == count after resize since tombstones are gone).

There is no shrink policy; the table only grows.

### 2.3 TTL / Expiration

Expiration is stored as an absolute timestamp in milliseconds since the Unix
epoch (using `clock_gettime(CLOCK_REALTIME)`).

**Lazy expiration**: Every hash table lookup checks `expire_at`. If
`expire_at != -1` and the current time is >= `expire_at`, the entry is treated
as deleted (converted to tombstone, key/value freed), and the lookup returns
"not found".

No background expiration sweep is implemented. Expired keys are only cleaned up
when accessed.

Helper:

```c
int64_t current_time_ms(void);  // returns milliseconds since epoch
```

---

## 3. RESP Protocol

### 3.1 Wire Format

RESP is a text-based protocol where the first byte determines the type:

| Type | Prefix | Format | Example |
|---|---|---|---|
| Simple String | `+` | `+<string>\r\n` | `+OK\r\n` |
| Error | `-` | `-<error message>\r\n` | `-ERR unknown command\r\n` |
| Integer | `:` | `:<number>\r\n` | `:42\r\n` |
| Bulk String | `$` | `$<len>\r\n<data>\r\n` | `$5\r\nHello\r\n` |
| Null Bulk String | `$` | `$-1\r\n` | `$-1\r\n` |
| Array | `*` | `*<count>\r\n<elements...>` | `*2\r\n$3\r\nGET\r\n$3\r\nkey\r\n` |

Clients send commands as RESP Arrays of Bulk Strings. The server responds with
any RESP type depending on the command.

### 3.2 Parser Design

The parser is a stateless, single-pass function that operates on the client's
read buffer:

```c
// Returns:
//   > 0: number of bytes consumed (a complete RESP value was parsed)
//     0: incomplete data, need more bytes
//    -1: parse error (malformed input)
int resp_parse(const char *buf, size_t len, resp_value_t *out);
```

The `resp_value_t` type is a tagged union:

```c
typedef enum {
    RESP_SIMPLE_STRING,
    RESP_ERROR,
    RESP_INTEGER,
    RESP_BULK_STRING,
    RESP_NULL_BULK_STRING,
    RESP_ARRAY
} resp_type_t;

typedef struct resp_value {
    resp_type_t type;
    union {
        rstr_t str;             // SIMPLE_STRING, ERROR, BULK_STRING
        int64_t integer;        // INTEGER
        struct {                // ARRAY
            struct resp_value *elements;
            int count;
        } array;
    };
} resp_value_t;
```

#### Parsing logic

1. If `len == 0`, return 0 (need more data).
2. Read the first byte to determine the type.
3. For `+`, `-`, `:` (simple types): scan for `\r\n`. If not found, return 0.
   Extract the content between the prefix byte and `\r\n`.
4. For `$` (bulk string): scan for the first `\r\n` to get the length. If
   length is -1, this is a null bulk string; return the consumed bytes.
   Otherwise, check that `len` has at least `prefix_bytes + length + 2` bytes
   (the `+2` is the trailing `\r\n`). If not, return 0. Copy `length` bytes as
   the string data.
5. For `*` (array): scan for the first `\r\n` to get the element count.
   Allocate an array of `resp_value_t`. Then recursively parse each element,
   accumulating the total bytes consumed. If any element returns 0
   (incomplete), free partially parsed elements and return 0.
6. On any malformed input (invalid prefix byte, negative bulk string length
   other than -1, etc.), return -1.

#### Memory ownership for parsed values

The parser allocates memory for strings (rstr_t) and arrays. A `resp_value_free()`
function recursively frees all allocations:

```c
void resp_value_free(resp_value_t *val);
```

This must be called after the command has been executed and the response has
been appended to the write buffer.

### 3.3 Response Serialization

Helper functions to append RESP-encoded data to the client's write buffer:

```c
void resp_write_simple_string(client_t *c, const char *str);
void resp_write_error(client_t *c, const char *msg);
void resp_write_integer(client_t *c, int64_t val);
void resp_write_bulk_string(client_t *c, const char *data, size_t len);
void resp_write_null_bulk_string(client_t *c);
void resp_write_array_header(client_t *c, int count);
```

Each function formats the RESP output and appends it to `client->write_buf`,
growing the buffer as needed. The `resp_write_array_header` writes `*<count>\r\n`;
the caller then writes each element individually.

### 3.4 Partial Reads and Pipelining

**Partial reads**: When `resp_parse()` returns 0, the data remains in
`read_buf`. The next `recv()` appends more data, and parsing is retried.

**Pipelining**: After a successful parse-and-execute cycle, the consumed bytes
are removed from `read_buf` via memmove, and `resp_parse()` is called again on
the remaining data. This loop continues until the parser returns 0 or the
buffer is empty. All responses are appended to `write_buf` and flushed
together.

---

## 4. Command Dispatch

### 4.1 Dispatch Table

Commands are dispatched via a static lookup table:

```c
typedef void (*cmd_handler_t)(client_t *client, hashtable_t *store,
                              resp_value_t *args, int argc);

typedef struct {
    const char *name;       // command name in uppercase
    cmd_handler_t handler;  // function pointer
    int min_args;           // minimum argument count (including command name)
    int max_args;           // maximum argument count (-1 for unlimited)
} cmd_entry_t;

static const cmd_entry_t command_table[] = {
    {"PING",    cmd_ping,    1, 2},
    {"ECHO",    cmd_echo,    2, 2},
    {"SET",     cmd_set,     3, 5},   // SET key value [EX seconds]
    {"GET",     cmd_get,     2, 2},
    {"DEL",     cmd_del,     2, -1},  // DEL key [key ...]
    {"EXISTS",  cmd_exists,  2, -1},  // EXISTS key [key ...]
    {"EXPIRE",  cmd_expire,  3, 3},
    {"TTL",     cmd_ttl,     2, 2},
    {"KEYS",    cmd_keys,    2, 2},
    {"TYPE",    cmd_type,    2, 2},
    {"INCR",    cmd_incr,    2, 2},
    {"DECR",    cmd_decr,    2, 2},
    {NULL,      NULL,        0, 0}    // sentinel
};
```

#### Dispatch logic

1. Validate the parsed value is an array of bulk strings.
2. Extract the command name from `args[0]`, convert to uppercase.
3. Linear scan through `command_table` (12 entries; no need for a hash here).
4. If not found, respond with `-ERR unknown command '<name>'\r\n`.
5. If argument count is outside `[min_args, max_args]`, respond with
   `-ERR wrong number of arguments for '<name>' command\r\n`.
6. Call the handler.

### 4.2 Command Specifications

#### PING

- `PING` → `+PONG\r\n`
- `PING <message>` → `$<len>\r\n<message>\r\n`

#### ECHO

- `ECHO <message>` → `$<len>\r\n<message>\r\n`

#### SET

- `SET key value` → `+OK\r\n`
  - Stores the key-value pair. If the key already exists, overwrites it. Any
    existing TTL on the key is removed.
- `SET key value EX seconds` → `+OK\r\n`
  - Same as above, but sets expiration to `current_time_ms() + seconds * 1000`.
  - The `EX` token is case-insensitive.
  - If `seconds` is not a valid positive integer, respond with
    `-ERR invalid expire time in 'set' command\r\n`.

#### GET

- `GET key` → `$<len>\r\n<value>\r\n` if key exists and is not expired.
- `GET key` → `$-1\r\n` if key does not exist or is expired.
  - Lazy expiration: if the key exists but is expired, delete it and return null.

#### DEL

- `DEL key [key ...]` → `:<count>\r\n`
  - Returns the number of keys that were actually deleted. Keys that don't
    exist are ignored (not counted).

#### EXISTS

- `EXISTS key [key ...]` → `:<count>\r\n`
  - Returns the count of keys that exist. Expired keys do not count (lazy
    expiration is applied during the check).

#### EXPIRE

- `EXPIRE key seconds` → `:1\r\n` if the key exists.
- `EXPIRE key seconds` → `:0\r\n` if the key does not exist.
  - Sets `expire_at = current_time_ms() + seconds * 1000`.
  - If the key is already expired, it is treated as non-existent.

#### TTL

- `TTL key` → `:-2\r\n` if the key does not exist (or is expired).
- `TTL key` → `:-1\r\n` if the key exists but has no expiration.
- `TTL key` → `:<seconds>\r\n` where seconds is the remaining time rounded up
  to the nearest second: `(expire_at - now_ms + 999) / 1000`. If this would
  compute to 0 or negative, the key is expired — delete it and return -2.

#### KEYS

- `KEYS pattern` → `*<count>\r\n<bulk string elements...>`
  - Returns all keys matching the glob pattern. Iterates all OCCUPIED entries
    in the hash table. Skips expired keys (lazy expiration applied). Matches
    each key against the pattern.
  - Pattern matching supports:
    - `*` — matches zero or more characters
    - `?` — matches exactly one character
    - `[abc]` — matches one character in the set
    - `[^abc]` or `[!abc]` — matches one character NOT in the set
    - Literal characters match themselves
  - The pattern matching function operates on `rstr_t` (binary-safe).

#### TYPE

- `TYPE key` → `+string\r\n` if the key exists (all values are strings).
- `TYPE key` → `+none\r\n` if the key does not exist or is expired.

#### INCR / DECR

- `INCR key` → `:<new_value>\r\n`
  - If the key does not exist, treat it as having value "0" and create it.
  - If the key exists but is not a valid 64-bit signed integer string, respond
    with `-ERR value is not an integer or out of range\r\n`.
  - Parse the current value as a `int64_t`, add 1, store the new string
    representation, and return the new value.
  - Overflow check: if adding 1 to `INT64_MAX` would overflow, return the error.
  - Preserves any existing TTL on the key.
- `DECR key` → same as INCR but subtracts 1.
  - Underflow check: if subtracting 1 from `INT64_MIN` would overflow, return
    the error.

---

## 5. Glob Pattern Matching

A standalone function for matching keys against glob patterns:

```c
// Returns true if `str` matches `pattern`.
// Both str and pattern are rstr_t (binary-safe, length-prefixed).
bool glob_match(const char *pattern, size_t pattern_len,
                const char *str, size_t str_len);
```

### Algorithm

Recursive matching with iterative optimization for trailing `*`:

```
glob_match(p, plen, s, slen):
  pi = 0, si = 0
  star_pi = -1, star_si = -1      // position of last '*' backtrack point

  while si < slen:
    if pi < plen and pattern[pi] == '*':
      star_pi = pi, star_si = si
      pi++                         // try matching * with zero characters
      continue
    if pi < plen and (pattern[pi] == '?' or pattern[pi] == str[si]):
      pi++, si++
      continue
    if pi < plen and pattern[pi] == '[':
      parse character class [...]
      if str[si] matches the class:
        pi = past ']', si++
        continue
    // mismatch: backtrack to last '*'
    if star_pi >= 0:
      pi = star_pi + 1
      star_si++
      si = star_si
      continue
    return false

  // consume trailing '*' in pattern
  while pi < plen and pattern[pi] == '*':
    pi++
  return pi == plen
```

The character class `[...]` parser:
1. Check for `^` or `!` as negation after `[`.
2. Scan characters until `]`. For each character (or range `a-z`), check if
   `str[si]` is in the set.
3. If negated, invert the result.

---

## 6. Memory Management

### 6.1 Ownership Rules

| Resource | Owner | Freed by |
|---|---|---|
| `rstr_t` key in hash table | Hash table | `ht_delete()`, `ht_set()` (on overwrite), `ht_destroy()`, or lazy expiration |
| `rstr_t` value in hash table | Hash table | Same as key |
| `resp_value_t` from parser | Caller of `resp_parse()` | `resp_value_free()` after command execution |
| `client_t.read_buf` | Client struct | `client_close()` |
| `client_t.write_buf` | Client struct | `client_close()` |
| `ht_entry_t` array | `hashtable_t` | `ht_destroy()` or `ht_resize()` (old array freed after rehashing) |

### 6.2 Key Lifecycle Scenarios

**SET key value**: The SET handler creates `rstr_t` copies of the key and value
from the parsed command (which are owned by the parser). These copies are
inserted into the hash table. If the key already exists, the old value's
`rstr_t` is freed before being replaced. The old key is also freed and replaced
(the new key is byte-identical but independently allocated).

**DEL key**: The hash table frees both the key and value `rstr_t`, sets the slot
to TOMBSTONE.

**Lazy expiration**: Same as DEL — frees key and value, sets TOMBSTONE.

**Hash table resize**: Allocates a new entry array. Moves key and value pointers
from old OCCUPIED entries to new slots (no deep copy needed — ownership
transfers). Frees the old entry array.

**Server shutdown**: `ht_destroy()` iterates all slots, frees every OCCUPIED
entry's key and value, then frees the entry array.

### 6.3 Connection Buffer Lifecycle

- **Allocation**: `read_buf` and `write_buf` start as NULL with capacity 0.
  They are allocated on first use (first recv / first response).
- **Growth**: When appending data would exceed capacity, realloc to
  `max(needed, current_cap * 2)`. Minimum initial allocation: 1024 bytes.
- **Freeing**: On `client_close()`, both buffers are freed and the client slot
  is marked unused (`fd = -1`).

### 6.4 Avoiding Common Bugs

- **Double-free**: `rstr_free()` sets `data = NULL` and `len = 0` after freeing.
  All free paths check for NULL before freeing.
- **Use-after-free**: The parsed `resp_value_t` is freed only after command
  execution is complete and the response is in the write buffer. Command
  handlers that need to store data (SET) make copies; they never retain
  pointers into the parsed value.
- **Buffer overflow**: All buffer writes check remaining capacity and grow as
  needed. String operations always use explicit lengths, never `strlen()` on
  binary data.

---

## 7. File Layout

```
.
├── DESIGN.md
├── Makefile
├── src/
│   ├── server.h          // server entry: start_server(), shutdown, port config
│   ├── server.c          // main(), event loop, signal handling, accept/close
│   ├── client.h          // client_t struct, client_new(), client_close(), buffer ops
│   ├── client.c          // client buffer management, read/write helpers
│   ├── resp.h            // resp_value_t, resp_parse(), resp_value_free(), resp_write_*()
│   ├── resp.c            // RESP parser and serializer implementation
│   ├── hashtable.h       // hashtable_t, ht_create(), ht_set(), ht_get(), ht_delete(), etc.
│   ├── hashtable.c       // hash table implementation with linear probing
│   ├── rstr.h            // rstr_t, rstr_create(), rstr_dup(), rstr_free(), rstr_eq()
│   ├── rstr.c            // binary-safe string implementation
│   ├── commands.h        // cmd_handler_t, dispatch_command(), command handler declarations
│   ├── commands.c        // command dispatch table and all command implementations
│   └── glob.h            // glob_match() declaration
│   └── glob.c            // glob pattern matching implementation
├── test/
│   ├── test_runner.c     // main() for test runner, executes all test suites
│   ├── test.h            // test macros (ASSERT_*, TEST, SUITE), test runner framework
│   ├── test_resp.c       // RESP parser unit tests
│   ├── test_hashtable.c  // hash table unit tests
│   ├── test_glob.c       // glob pattern matching tests
│   ├── test_ttl.c        // TTL/expiration tests
│   └── test_integration.c // TCP integration tests (spawns server)
```

### Public vs. Static Functions

**Public (declared in headers):**

`rstr.h`:
- `rstr_t rstr_create(const char *data, size_t len)`
- `rstr_t rstr_dup(rstr_t s)`
- `void rstr_free(rstr_t *s)`
- `bool rstr_eq(rstr_t a, rstr_t b)`

`hashtable.h`:
- `hashtable_t *ht_create(void)`
- `void ht_destroy(hashtable_t *ht)`
- `bool ht_set(hashtable_t *ht, rstr_t key, rstr_t value)` — returns true if new key
- `rstr_t *ht_get(hashtable_t *ht, rstr_t key)` — returns pointer to value or NULL
- `bool ht_delete(hashtable_t *ht, rstr_t key)` — returns true if key existed
- `bool ht_exists(hashtable_t *ht, rstr_t key)` — check existence (with lazy expiry)
- `void ht_set_expire(hashtable_t *ht, rstr_t key, int64_t expire_at_ms)`
- `int64_t ht_get_expire(hashtable_t *ht, rstr_t key)` — returns expire_at or -1
- `size_t ht_count(hashtable_t *ht)` — number of live entries
- Iterator: `void ht_iter_init(hashtable_t *ht, ht_iter_t *iter)`,
  `bool ht_iter_next(ht_iter_t *iter, rstr_t *key, rstr_t *value)`

`resp.h`:
- `int resp_parse(const char *buf, size_t len, resp_value_t *out)`
- `void resp_value_free(resp_value_t *val)`
- `void resp_write_simple_string(client_t *c, const char *str)`
- `void resp_write_error(client_t *c, const char *msg)`
- `void resp_write_integer(client_t *c, int64_t val)`
- `void resp_write_bulk_string(client_t *c, const char *data, size_t len)`
- `void resp_write_null_bulk_string(client_t *c)`
- `void resp_write_array_header(client_t *c, int count)`

`client.h`:
- `void client_init(client_t *c)`
- `void client_close(client_t *c)`
- `void client_write_append(client_t *c, const char *data, size_t len)`

`commands.h`:
- `void dispatch_command(client_t *client, hashtable_t *store, resp_value_t *cmd)`

`glob.h`:
- `bool glob_match(const char *pattern, size_t plen, const char *str, size_t slen)`

`server.h`:
- `int start_server(int port)` — creates, binds, and returns listening socket fd
- `extern volatile sig_atomic_t g_shutdown`
- `int64_t current_time_ms(void)`

**Static (internal to .c files):**

`server.c`:
- `static void signal_handler(int sig)` — sets g_shutdown
- `static void accept_new_client(int listen_fd, client_t *clients)` — accept loop
- `static void handle_client_read(client_t *c, hashtable_t *store)` — recv + parse loop
- `static void handle_client_write(client_t *c)` — send from write_buf

`hashtable.c`:
- `static uint32_t fnv1a_hash(const char *data, size_t len)` — FNV-1a hash
- `static size_t probe(hashtable_t *ht, rstr_t key, bool *found)` — find slot
- `static void ht_resize(hashtable_t *ht)` — double capacity and rehash
- `static bool is_expired(ht_entry_t *entry)` — check expire_at vs now
- `static void entry_free(ht_entry_t *entry)` — free key+value, set tombstone

`resp.c`:
- `static int parse_line(const char *buf, size_t len, size_t *line_len)` — find \r\n
- `static int parse_bulk_string(const char *buf, size_t len, resp_value_t *out)`
- `static int parse_array(const char *buf, size_t len, resp_value_t *out)`
- `static int parse_integer_value(const char *buf, size_t len, int64_t *out)` — atoi for RESP

`commands.c`:
- `static void cmd_ping(client_t *, hashtable_t *, resp_value_t *, int)`
- `static void cmd_echo(...)` — and all other command handlers
- `static bool parse_int64(const char *data, size_t len, int64_t *out)` — safe string-to-int

`glob.c`:
- `static bool match_char_class(const char *pattern, size_t plen, size_t *pi, char c)` — parse `[...]`

---

## 8. Build System

```makefile
CC       = gcc
CFLAGS   = -Wall -Wextra -Werror -pedantic -std=c11 -g
SRC_DIR  = src
TEST_DIR = test
BUILD_DIR = build

SRCS     = $(wildcard $(SRC_DIR)/*.c)
OBJS     = $(SRCS:$(SRC_DIR)/%.c=$(BUILD_DIR)/%.o)

TEST_SRCS = $(wildcard $(TEST_DIR)/*.c)
# Server objects minus server.o (which has main()) for linking with tests
LIB_OBJS  = $(filter-out $(BUILD_DIR)/server.o, $(OBJS))
TEST_OBJS = $(TEST_SRCS:$(TEST_DIR)/%.c=$(BUILD_DIR)/test/%.o)

all: mini-redis

mini-redis: $(OBJS)
	$(CC) $(CFLAGS) -o $@ $^

test_bin: $(LIB_OBJS) $(TEST_OBJS)
	$(CC) $(CFLAGS) -o test_runner $^

test: mini-redis test_bin
	./test_runner

clean:
	rm -rf $(BUILD_DIR) mini-redis test_runner
```

- `server.c` contains `main()` for the server binary.
- `test_runner.c` contains `main()` for the test binary.
- To link tests, all src objects except `server.o` are linked with all test objects.
  The test binary includes its own `main()` from `test_runner.c`.
- The `test` target builds both the server binary (needed for integration tests)
  and the test binary, then runs the test binary.

---

## 9. Test Strategy

### 9.1 Test Framework

A minimal custom test harness in `test.h`:

```c
#define ASSERT_TRUE(expr)  do { \
    if (!(expr)) { \
        printf("  FAIL: %s:%d: %s\n", __FILE__, __LINE__, #expr); \
        return 1; \
    } \
} while(0)

#define ASSERT_EQ_INT(a, b)    // compare int64_t, print both on failure
#define ASSERT_EQ_STR(a, b)    // compare C strings with strcmp
#define ASSERT_EQ_RSTR(a, b)   // compare rstr_t values
#define ASSERT_NULL(ptr)       // assert pointer is NULL
#define ASSERT_NOT_NULL(ptr)   // assert pointer is not NULL

typedef int (*test_fn)(void);

typedef struct {
    const char *name;
    test_fn fn;
} test_case_t;
```

Each test function returns 0 on success, non-zero on failure. `test_runner.c`
runs all registered tests, prints results, and exits with 0 only if all pass.

### 9.2 Unit Tests

#### RESP Parser Tests (`test_resp.c`)

| Test | What it verifies |
|---|---|
| `test_parse_simple_string` | Parses `+OK\r\n` → RESP_SIMPLE_STRING, str="OK" |
| `test_parse_error` | Parses `-ERR msg\r\n` → RESP_ERROR, str="ERR msg" |
| `test_parse_integer` | Parses `:42\r\n` → RESP_INTEGER, integer=42 |
| `test_parse_negative_integer` | Parses `:-1\r\n` → integer=-1 |
| `test_parse_bulk_string` | Parses `$5\r\nHello\r\n` → RESP_BULK_STRING, len=5 |
| `test_parse_empty_bulk_string` | Parses `$0\r\n\r\n` → RESP_BULK_STRING, len=0 |
| `test_parse_null_bulk_string` | Parses `$-1\r\n` → RESP_NULL_BULK_STRING |
| `test_parse_array` | Parses `*2\r\n$3\r\nfoo\r\n$3\r\nbar\r\n` → 2-element array |
| `test_parse_empty_array` | Parses `*0\r\n` → 0-element array |
| `test_parse_nested_array` | Array containing an array element |
| `test_parse_incomplete_simple` | `+OK` (no \r\n) → returns 0 |
| `test_parse_incomplete_bulk` | `$5\r\nHel` → returns 0 |
| `test_parse_incomplete_array` | `*2\r\n$3\r\nfoo\r\n` (missing 2nd element) → returns 0 |
| `test_parse_malformed_prefix` | `!garbage\r\n` → returns -1 |
| `test_parse_bulk_binary_data` | Bulk string containing `\0` bytes → len correct, data preserved |

#### Hash Table Tests (`test_hashtable.c`)

| Test | What it verifies |
|---|---|
| `test_ht_insert_and_get` | Insert a key, retrieve it, values match |
| `test_ht_overwrite` | Insert same key twice, second value replaces first |
| `test_ht_delete` | Insert, delete, get returns NULL |
| `test_ht_delete_nonexistent` | Delete non-existent key returns false |
| `test_ht_get_nonexistent` | Get on empty table returns NULL |
| `test_ht_resize` | Insert enough keys to trigger resize (>0.7 load), all keys still accessible |
| `test_ht_many_keys` | Insert 1000 keys, verify all retrievable |
| `test_ht_iterator` | Insert N keys, iterate, count == N, all keys seen |
| `test_ht_iterator_with_tombstones` | Delete some keys, iterator skips them |
| `test_ht_binary_keys` | Keys containing null bytes work correctly |

#### Glob Tests (`test_glob.c`)

| Test | What it verifies |
|---|---|
| `test_glob_literal` | `"hello"` matches `"hello"`, not `"world"` |
| `test_glob_star` | `"h*o"` matches `"hello"`, `"ho"`, not `"hex"` |
| `test_glob_star_all` | `"*"` matches everything including empty string |
| `test_glob_question` | `"h?llo"` matches `"hello"`, `"hallo"`, not `"hllo"` |
| `test_glob_char_class` | `"h[ae]llo"` matches `"hello"`, `"hallo"`, not `"hillo"` |
| `test_glob_negated_class` | `"h[!ae]llo"` matches `"hillo"`, not `"hello"` |
| `test_glob_empty_pattern` | `""` matches `""`, not `"a"` |
| `test_glob_empty_string` | `"*"` matches `""`, `"?"` does not match `""` |
| `test_glob_consecutive_stars` | `"**"` equivalent to `"*"` |
| `test_glob_complex` | `"user:*:name"` matches `"user:123:name"` |
| `test_glob_question_star` | `"?*"` matches any string of length >= 1 |

#### TTL Tests (`test_ttl.c`)

| Test | What it verifies |
|---|---|
| `test_ttl_set_and_check` | Set expiry 2s in future, ht_get_expire returns it |
| `test_ttl_not_expired_yet` | Set expiry 10s in future, ht_get succeeds |
| `test_ttl_expired` | Set expiry 1ms in future, sleep briefly, ht_get returns NULL |
| `test_ttl_delete_removes_expiry` | Set expiry, delete key, key is gone |
| `test_ttl_overwrite_resets` | Set key with TTL, overwrite without TTL, expire_at is -1 |
| `test_ttl_expire_makes_tombstone` | After expiration, slot is tombstone, count decremented |
| `test_ttl_no_expiry_by_default` | Insert without TTL, get_expire returns -1 |

### 9.3 Integration Tests (`test_integration.c`)

Integration tests spawn the server as a child process and connect over TCP.

**Test infrastructure:**

```c
static pid_t server_pid;
static int test_port;

static void start_test_server(void) {
    test_port = 30000 + (getpid() % 10000);  // random high port
    server_pid = fork();
    if (server_pid == 0) {
        // child: exec the server binary
        char port_str[16];
        snprintf(port_str, sizeof(port_str), "%d", test_port);
        execl("./mini-redis", "mini-redis", "--port", port_str, NULL);
        _exit(1);
    }
    // parent: wait for server to be ready (retry connect with backoff)
    usleep(100000);  // 100ms initial wait
    // ... retry loop with connect() ...
}

static void stop_test_server(void) {
    kill(server_pid, SIGTERM);
    waitpid(server_pid, NULL, 0);
}
```

A helper function sends a raw RESP command and reads the response:

```c
// Connect to server, returns socket fd
static int test_connect(void);
// Send raw bytes
static void test_send(int fd, const char *data, size_t len);
// Read response, parse it, return resp_value_t
static resp_value_t test_read_response(int fd);
// Convenience: send a RESP command array from strings
static void test_send_command(int fd, int argc, ...);
```

| Test | What it verifies |
|---|---|
| `test_int_ping` | Send PING, get +PONG |
| `test_int_ping_with_msg` | Send PING "hello", get bulk string "hello" |
| `test_int_echo` | Send ECHO "test", get bulk string "test" |
| `test_int_set_get` | SET foo bar → OK, GET foo → "bar" |
| `test_int_get_nonexistent` | GET missing → null bulk string |
| `test_int_set_overwrite` | SET key v1, SET key v2, GET key → "v2" |
| `test_int_del` | SET+DEL, GET returns null, DEL returns count |
| `test_int_del_multi` | DEL key1 key2 key3 → count of actually deleted |
| `test_int_exists` | EXISTS on present, absent, and multi-key |
| `test_int_expire_ttl` | SET key, EXPIRE 10, TTL returns ~10 |
| `test_int_set_ex` | SET key val EX 1, sleep 1.5s, GET returns null |
| `test_int_ttl_no_expiry` | SET key (no EX), TTL returns -1 |
| `test_int_ttl_no_key` | TTL nonexistent returns -2 |
| `test_int_keys_pattern` | SET several keys, KEYS "user:*" returns matching set |
| `test_int_type` | TYPE existing key → "string", TYPE missing → "none" |
| `test_int_incr_new` | INCR nonexistent key → 1 |
| `test_int_incr_existing` | SET key "10", INCR → 11 |
| `test_int_incr_non_integer` | SET key "abc", INCR → error |
| `test_int_decr` | SET key "10", DECR → 9 |
| `test_int_pipeline` | Send 3 commands in one write, read 3 responses |
| `test_int_concurrent` | 3 clients connect simultaneously, each does SET/GET independently |
| `test_int_unknown_cmd` | Send "FOOBAR" → error response |
| `test_int_wrong_argc` | Send "GET" (no args) → error response |

### 9.4 Memory Safety Testing

Run the test suite under valgrind:

```bash
valgrind --leak-check=full --error-exitcode=1 ./test_runner
```

This catches:
- Memory leaks (unreachable allocations)
- Use-after-free
- Double-free
- Uninitialized reads
- Buffer overflows (when they hit valgrind's redzone)

The integration tests implicitly test the server's memory safety because the
server is started, exercised, and then cleanly shut down with SIGTERM, allowing
its cleanup path to run.

For the server binary specifically:

```bash
valgrind --leak-check=full ./mini-redis --port 16379 &
# ... run integration tests against it ...
kill -TERM $!
wait $!
# check valgrind output for leaks
```

---

## 10. Error Handling Approach

- System calls (`socket`, `bind`, `listen`, `accept`, `recv`, `send`, `malloc`,
  `realloc`): check return values. On fatal errors (bind failure, OOM), print
  to stderr and exit. On non-fatal errors (single client recv fails), close
  that client and continue.
- RESP parse errors: send `-ERR Protocol error\r\n` and close the client
  connection (a protocol error means the stream is desynchronized).
- Command errors (wrong argc, wrong type): send appropriate `-ERR` message but
  keep the connection open.
- `malloc`/`realloc` failure: In a mini Redis, treat OOM as fatal — `perror()`
  and `exit(1)`. A production server would handle this more gracefully, but for
  this scope it is acceptable.

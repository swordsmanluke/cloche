# Gemini Browser — Design Document

## Overview

A synchronous, CLI-based Gemini protocol browser written in Rust. It connects to
Gemini servers over TLS (port 1965), renders `text/gemini` content with ANSI
colors, and provides an interactive REPL for navigating links, history, and
arbitrary URLs. Long pages are displayed with pagination so that content does not
scroll off-screen.

---

## Module Structure

```
src/
├── main.rs        — Entry point, REPL loop, command dispatch
├── gemini.rs      — TLS connection, request/response, status handling
├── parser.rs      — text/gemini line-type parser
├── render.rs      — ANSI terminal renderer for parsed lines
├── pager.rs       — Paginated display of rendered output
└── url_utils.rs   — URL parsing, normalization, relative resolution
```

### Dependency graph

```
main
 ├── gemini      (fetch pages)
 ├── parser      (parse body into lines)
 ├── render      (format parsed lines into styled strings)
 ├── pager       (paginate and display rendered output)
 └── url_utils   (resolve URLs)

gemini
 └── url_utils   (normalize request URL, resolve redirects)
```

`parser`, `render`, `pager`, and `url_utils` have no internal cross-dependencies.
`pager` operates on plain `Vec<String>` (the formatted output lines from
`render`) and has no knowledge of Gemini types.

---

## Data Types

### `url_utils.rs`

```rust
/// Parse a gemini:// URL string into a `url::Url`.
/// Ensures scheme is "gemini", default port 1965, default path "/".
pub fn parse_gemini_url(raw: &str) -> Result<Url, GeminiError>

/// Resolve a potentially-relative URL against a base URL.
/// Handles absolute gemini:// URLs, protocol-relative, and relative paths.
pub fn resolve_url(base: &Url, reference: &str) -> Result<Url, GeminiError>
```

No custom structs — uses `url::Url` directly.

### `gemini.rs`

```rust
/// A parsed Gemini response.
pub struct GeminiResponse {
    pub status: u8,           // two-digit status code (10–69)
    pub meta: String,         // the META field (MIME type, redirect URL, message, etc.)
    pub body: Option<Vec<u8>>, // body bytes; present only for 2x SUCCESS responses
}

/// Fetch a Gemini page. Handles the full request/response cycle for a single URL.
/// Does NOT follow redirects — the caller handles redirect logic.
pub fn fetch(url: &Url) -> Result<GeminiResponse, GeminiError>

/// Navigate to a URL, following redirects. Returns the final response and the final URL.
pub fn fetch_with_redirects(start_url: &Url) -> Result<(GeminiResponse, Url), GeminiError>

/// Check for redirect loops and max hops.
pub fn check_redirect(visited: &[String], target: &str, max_hops: usize) -> Result<(), GeminiError>
```

Internally, `fetch` performs:
1. DNS resolution and TCP connect (with 5-second timeout).
2. TLS handshake via `rustls` with certificate verification disabled.
3. Sends `<URL>\r\n`.
4. Reads and parses the response header line.
5. For 2x status, reads the body (up to 5 MB).
6. Returns `GeminiResponse`.

### `parser.rs`

```rust
/// A single parsed line of text/gemini content.
#[derive(Debug, PartialEq)]
pub enum GeminiLine {
    Text(String),
    Link { url: String, label: String },
    Heading { level: u8, text: String },      // level 1, 2, or 3
    ListItem(String),
    Quote(String),
    PreformattedToggle { alt_text: String },   // ``` with optional alt text
    PreformattedText(String),                  // a line inside a preformatted block
}

/// Parse a text/gemini document body into a Vec<GeminiLine>.
/// Tracks preformatted toggle state: lines between ``` markers become
/// PreformattedText; the ``` lines themselves become PreformattedToggle.
pub fn parse_gemini(body: &str) -> Vec<GeminiLine>
```

### `render.rs`

```rust
/// Render parsed gemini lines into ANSI-formatted strings.
/// Returns a tuple of (output_lines, link_urls):
/// - output_lines: Vec<String> of formatted lines ready for display (one per output line)
/// - link_urls: Vec<String> of link URLs found on the page (indexed from 1)
///
/// Does NOT print to stdout — the caller (via the pager) handles display.
pub fn render(lines: &[GeminiLine]) -> (Vec<String>, Vec<String>)
```

ANSI codes used:
- **Heading 1**: bold + bright cyan (`\x1b[1;96m`)
- **Heading 2**: bold (`\x1b[1m`)
- **Heading 3**: bold (`\x1b[1m`)
- **Links**: cyan (`\x1b[36m`), prefixed with `[N] ` where N is the 1-based link index
- **Quotes**: dim italic (`\x1b[2;3m`)
- **Preformatted text**: printed as-is, no special styling, no line wrapping
- **List items**: printed with a `  • ` bullet prefix
- All styled lines end with a reset (`\x1b[0m`)

Each formatted line is a complete `String` (no trailing newline). The render
function builds these strings using `format!()` instead of `println!()`.

### `pager.rs`

```rust
/// Paginated display for rendered output lines.
///
/// When a page's content exceeds the terminal height, the Pager shows one
/// screenful at a time, allowing the user to scroll forward and backward
/// through the content using `next`/`prev` commands at the REPL prompt.
pub struct Pager {
    lines: Vec<String>,       // all rendered output lines
    offset: usize,            // index of the first line on the current page
    page_height: usize,       // number of content lines per page (terminal height - 1)
}

impl Pager {
    /// Create a new Pager for the given lines.
    /// `page_height` is the number of content lines per page (typically
    /// terminal_height - 1, reserving one line for the status/pager prompt).
    pub fn new(lines: Vec<String>, page_height: usize) -> Self

    /// Print the current page of lines to stdout.
    /// After the content lines, if there is more content beyond the current
    /// page, prints a status line: `[Page X/Y — 'n':next 'p':prev]`
    pub fn display_current_page(&self)

    /// Advance to the next page. Returns true if the page changed,
    /// false if already at the last page.
    pub fn next_page(&mut self) -> bool

    /// Go back to the previous page. Returns true if the page changed,
    /// false if already at the first page.
    pub fn prev_page(&mut self) -> bool

    /// Returns true if the content requires pagination (more lines than page_height).
    pub fn needs_pagination(&self) -> bool

    /// Returns the total number of pages.
    pub fn total_pages(&self) -> usize

    /// Returns the current page number (1-indexed).
    pub fn current_page(&self) -> usize
}

/// Detect the terminal height in rows.
///
/// Strategy:
/// 1. Try the LINES environment variable (set by many terminals/shells).
/// 2. Try reading from the terminal via libc ioctl TIOCGWINSZ on stdout.
/// 3. Fall back to a default of 24 rows.
pub fn terminal_height() -> usize
```

### `main.rs` — Navigation State

```rust
/// Holds the browser's runtime state.
struct BrowserState {
    history: Vec<Url>,        // stack of previously visited URLs (for `back`)
    current_url: Option<Url>, // the URL of the currently displayed page
    links: Vec<String>,       // link URLs extracted from the current page (1-indexed)
    pager: Option<Pager>,     // active pager for the current page (None if no page loaded
                              // or content fits on one screen)
}
```

Commands parsed from the REPL prompt:

```rust
enum Command {
    FollowLink(usize),   // a number like "3"
    Back,                 // "back" or "b"
    Go(String),           // "go <url>"
    Quit,                 // "quit" or "q"
    Help,                 // "help" or "?"
    Navigate(String),     // bare "gemini://..." URL
    NextPage,             // "next" or "n"
    PrevPage,             // "prev" or "p"
    Empty,
    Unknown,
}
```

### Error types (defined in `gemini.rs`, used everywhere)

```rust
use thiserror::Error;

#[derive(Error, Debug)]
pub enum GeminiError {
    #[error("invalid URL: {0}")]
    InvalidUrl(String),

    #[error("connection failed: {0}")]
    ConnectionFailed(String),

    #[error("TLS error: {0}")]
    TlsError(String),

    #[error("timeout connecting to server")]
    Timeout,

    #[error("invalid response header: {0}")]
    InvalidResponse(String),

    #[error("response body too large (exceeds 5 MB)")]
    BodyTooLarge,

    #[error("too many redirects")]
    TooManyRedirects,

    #[error("redirect loop detected")]
    RedirectLoop,

    #[error("IO error: {0}")]
    Io(#[from] std::io::Error),
}
```

---

## Protocol Flow

When the user navigates to a URL (via `go <url>`, link number, or bare URL):

### 1. URL parsing / resolution

- If triggered by a link number, look up the raw URL string from `links[n-1]`.
  Resolve it against `current_url` using `url_utils::resolve_url`.
- If triggered by `go <url>` or a bare gemini:// URL, parse it with
  `url_utils::parse_gemini_url`.
- Validation: scheme must be `gemini`, host must be present. Default port 1965,
  default path `/`.

### 2. Redirect-following loop

Wrap steps 3–5 in a loop that follows 3x redirects:
- Maintain a `Vec<String>` of visited URLs (as strings) for loop detection.
- Limit to **5 hops**. If exceeded, return `GeminiError::TooManyRedirects`.
- Before each hop, check if the redirect target is already in the visited set.
  If so, return `GeminiError::RedirectLoop`.

### 3. TLS connection

- Open a `std::net::TcpStream` to `host:port` with a **5-second** connect
  timeout (`TcpStream::connect_timeout`).
- Set read/write timeouts to 5 seconds on the stream.
- Build a `rustls::ClientConfig` with a custom `ServerCertVerifier` that accepts
  all certificates (returns `Ok(ServerCertVerified::assertion())` for every
  call). Use `webpki-roots` as the root store (required by the API even though
  we skip verification).
- Wrap the stream in `rustls::StreamOwned<rustls::ClientConnection, TcpStream>`.

### 4. Request sending

- Write `{url}\r\n` (the full URL including scheme) to the TLS stream.

### 5. Response header parsing

- Read bytes from the TLS stream until `\r\n` is found (or until 1024 + 2 bytes
  to guard against overlong headers — META is capped at 1024 bytes by spec).
- Split the header line at the first space. The first part is the two-digit
  status code; the remainder is META.
- Validate: status must be two ASCII digits in range 10–69. If not, return
  `GeminiError::InvalidResponse`.

### 6. Body reading (for 2x responses only)

- Read the rest of the stream into a `Vec<u8>`.
- Enforce a **5 MB** limit: if total bytes exceed 5,242,880, stop reading and
  return `GeminiError::BodyTooLarge`.
- For non-2x statuses, `body` is `None`.

### 7. Status dispatch (back in `main.rs`)

Based on the first digit of the status code:

| Status | Action |
|--------|--------|
| **1x INPUT** | Print the META string as a prompt. Read a line from stdin. URL-encode it and append as `?query` to the original URL. Re-fetch. |
| **2x SUCCESS** | If META starts with `text/gemini` (or is empty, defaulting to `text/gemini`), parse and render the body. For any other MIME type, print `[Received <META>, not rendering]`. |
| **3x REDIRECT** | META is the redirect URL. Resolve against current URL. Continue the redirect loop (step 2). |
| **4x TEMP FAILURE** | Print `Temporary failure ({status}): {meta}`. Return to prompt. |
| **5x PERM FAILURE** | Print `Permanent failure ({status}): {meta}`. Return to prompt. |
| **6x CLIENT CERT** | Print `Client certificates are not supported.` Return to prompt. |

### 8. Rendering and pagination

On a successful 2x response with `text/gemini` content:

1. Parse the body with `parser::parse_gemini()`.
2. Render the parsed lines with `render::render()`, which returns
   `(output_lines, link_urls)` — a vector of ANSI-formatted strings and a
   vector of link URLs.
3. Determine the terminal height via `pager::terminal_height()`.
4. If the number of output lines exceeds `terminal_height - 1`:
   - Create a `Pager` with `page_height = terminal_height - 1`.
   - Call `pager.display_current_page()` to show the first screenful.
   - Store the `Pager` in `state.pager`.
5. If the content fits on one screen:
   - Print all output lines directly to stdout (one `println!` per line).
   - Set `state.pager` to `None`.
6. Store the link URLs in `state.links`.

### 9. State update and prompt

- On successful page display (2x with renderable content):
  - Push the old `current_url` (if any) onto `history`.
  - Set `current_url` to the new URL.
  - Store the `links` vector returned by `render`.
  - Store or clear the `pager` as described in step 8.
- Print `> ` and read the next command.

---

## Pagination Behavior

### Display

When the Pager is active (content exceeds one screen), `display_current_page()`
prints lines from `offset` to `offset + page_height`, followed by a status line:

```
[Page 1/5 — 'n':next 'p':prev]
```

This status line uses dim ANSI styling (`\x1b[2m`) so it's visually distinct
from page content. It is only shown when the content requires pagination.

When content fits on a single screen, all lines are printed directly with no
status line, and `state.pager` is `None`.

### Commands

Two new REPL commands control pagination:

- `next` or `n`: advance to the next page. If already on the last page, print
  `"Already on the last page."`. If no pager is active, print
  `"No page to scroll."`.
- `prev` or `p`: go back to the previous page. If already on the first page,
  print `"Already on the first page."`. If no pager is active, print
  `"No page to scroll."`.

All existing commands remain available while paginated content is displayed.
The user can follow links, go back, navigate to a new URL, etc. at any time.
When the user navigates away (via link, `go`, `back`, etc.), the current pager
is replaced or cleared.

### Page calculation

- `page_height = terminal_height - 1` (reserve 1 line for the status/prompt).
- `total_pages = ceil(total_lines / page_height)`.
- `current_page = (offset / page_height) + 1`.
- `next_page()`: sets `offset = min(offset + page_height, last_page_offset)`.
- `prev_page()`: sets `offset = offset.saturating_sub(page_height)`.

### Terminal height detection

`pager::terminal_height()` determines the terminal height:

1. Check the `LINES` environment variable. If set and parses as a positive
   integer, use it.
2. On Unix, use `libc::ioctl` with `TIOCGWINSZ` on `STDOUT_FILENO` to query
   the terminal dimensions. If successful, use the `ws_row` field.
3. Fall back to a default of **24 rows**.

This requires adding `libc` as a dependency in `Cargo.toml`.

---

## REPL Command Parsing

Read a line from stdin. Trim whitespace. Match:

1. Empty line → do nothing, re-prompt.
2. `"quit"` or `"q"` → exit process.
3. `"back"` or `"b"` → pop from `history`. If empty, print
   `"No previous page."`. Otherwise navigate to the popped URL.
4. `"help"` or `"?"` → print help text (including pagination commands).
5. `"next"` or `"n"` → show next page of current content.
6. `"prev"` or `"p"` → show previous page of current content.
7. `"go "` prefix → strip prefix, treat remainder as a URL, navigate.
8. Starts with `"gemini://"` → treat entire input as a URL, navigate.
9. Parses as `usize` → follow that link number. If out of range, print
   `"Invalid link number."`.
10. Anything else → print `"Unknown command. Type 'help' for available commands."`.

The updated help text:

```
Commands:
  <number>       Follow link by number
  back, b        Go to previous page
  go <url>       Navigate to a URL
  gemini://...   Navigate to a Gemini URL
  next, n        Next page (when content is paginated)
  prev, p        Previous page (when content is paginated)
  help, ?        Show this help
  quit, q        Exit the browser
```

### Command priority

`"n"` is matched as the `NextPage` command **before** attempting to parse it as a
number. This means a user cannot type `"n"` to follow a link — but since link
numbers are always displayed numerically (`[1]`, `[2]`, etc.), this is not a
conflict. Similarly, `"p"` is matched as `PrevPage` before number parsing.
The match order in `parse_command` must handle this: check for string commands
(`quit`, `back`, `help`, `next`, `prev`, `go`) before attempting `usize` parse.

---

## Edge Cases

### Connection / network errors
- `TcpStream::connect_timeout` returns `Err` on timeout or connection refused →
  display `"Error: connection failed: {details}"`, return to prompt.
- TLS handshake failure → display `"Error: TLS error: {details}"`, return to
  prompt.

### Malformed response headers
- No `\r\n` found within 1026 bytes → `InvalidResponse("header line too long")`.
- Status code not two digits → `InvalidResponse("invalid status code")`.
- Status code outside 10–69 → `InvalidResponse("status code out of range")`.
- Missing META after status for 1x/3x (where META is required) →
  `InvalidResponse("missing meta")`.

### Redirect chains
- Track each URL visited during the redirect chain in a `Vec<String>`.
- After 5 hops without a non-3x response → `TooManyRedirects`.
- If a redirect target matches any previously visited URL in the chain →
  `RedirectLoop`.

### Response body size
- Read body in a loop, accumulating into a `Vec<u8>`.
- After each read, check `total_len > 5_242_880`. If exceeded, return
  `BodyTooLarge`.

### Preformatted toggle state
- `parser::parse_gemini` maintains a `bool` flag `in_preformatted`.
- A line starting with ` ``` ` toggles the flag and emits a `PreformattedToggle`.
- While `in_preformatted` is true, **all** lines are emitted as
  `PreformattedText` regardless of their content — no link/heading/etc. parsing.
- The flag starts `false` at the beginning of each document.
- If the document ends while `in_preformatted` is still true, that is valid — the
  preformatted block is implicitly closed.

### Empty body / missing META
- A 2x response with empty META → default MIME type is `text/gemini`.
- A 2x response with an empty body → display nothing, still update state and
  show prompt. Pager is set to `None` (nothing to paginate).

### Input status (1x)
- URL-encode the user's input before appending as query string. Use
  percent-encoding for non-ASCII and reserved characters.
- If the user provides empty input, still re-request with an empty `?` query.

### Back on empty history
- Print `"No previous page."` and stay at the current page.

### Link number out of range
- Print `"Invalid link number."` if < 1 or > number of links on the page.
- Also print this if a number is entered when no page has been loaded yet
  (`links` is empty).

### Pagination edge cases

- **Content fits on one screen**: No pager is created. `state.pager` is `None`.
  `next`/`prev` commands print `"No page to scroll."`.
- **Exactly one page of content**: `page_height` lines or fewer. No pager is
  created (same as above).
- **Empty page**: No output lines. No pager. `next`/`prev` print
  `"No page to scroll."`.
- **Terminal height detection failure**: Falls back to 24 rows, so
  `page_height = 23`.
- **Very small terminal** (e.g., 1–3 rows): `page_height` could be 0 or very
  small. If `page_height < 1`, clamp it to 1 to avoid division by zero or
  empty pages.
- **Last page has fewer lines than page_height**: `display_current_page()`
  prints only the remaining lines (from `offset` to the end of `lines`).
- **Non-text/gemini content**: No rendering/pagination happens. The
  `[Received <MIME>, not rendering]` message is printed directly. Pager is
  cleared.
- **Navigation clears pager**: When navigating to a new page (via link, `go`,
  `back`, or bare URL), the previous pager state is discarded and replaced with
  the new page's pager (or `None` if the new content fits on screen).

---

## Module Public APIs (Summary)

### `url_utils.rs`
```
pub fn parse_gemini_url(raw: &str) -> Result<Url, GeminiError>
pub fn resolve_url(base: &Url, reference: &str) -> Result<Url, GeminiError>
```

### `gemini.rs`
```
pub struct GeminiResponse { pub status: u8, pub meta: String, pub body: Option<Vec<u8>> }
pub enum GeminiError { InvalidUrl, ConnectionFailed, TlsError, Timeout, InvalidResponse, BodyTooLarge, TooManyRedirects, RedirectLoop, Io }
pub fn fetch(url: &Url) -> Result<GeminiResponse, GeminiError>
pub fn fetch_with_redirects(start_url: &Url) -> Result<(GeminiResponse, Url), GeminiError>
pub fn check_redirect(visited: &[String], target: &str, max_hops: usize) -> Result<(), GeminiError>
pub fn parse_response_header(line: &str) -> Result<(u8, String), GeminiError>
```

### `parser.rs`
```
pub enum GeminiLine { Text, Link, Heading, ListItem, Quote, PreformattedToggle, PreformattedText }
pub fn parse_gemini(body: &str) -> Vec<GeminiLine>
```

### `render.rs`
```
pub fn render(lines: &[GeminiLine]) -> (Vec<String>, Vec<String>)
                                        // (output_lines, link_urls)
```

### `pager.rs`
```
pub struct Pager { lines: Vec<String>, offset: usize, page_height: usize }
  pub fn new(lines: Vec<String>, page_height: usize) -> Self
  pub fn display_current_page(&self)
  pub fn next_page(&mut self) -> bool
  pub fn prev_page(&mut self) -> bool
  pub fn needs_pagination(&self) -> bool
  pub fn total_pages(&self) -> usize
  pub fn current_page(&self) -> usize
pub fn terminal_height() -> usize
```

### `main.rs` (not public — internal to the binary)
```
struct BrowserState { history, current_url, links, pager }
enum Command { FollowLink, Back, Go, Quit, Help, Navigate, NextPage, PrevPage, Empty, Unknown }
fn parse_command(input: &str) -> Command
fn navigate(state: &mut BrowserState, url: Url)
fn handle_response(state: &mut BrowserState, response: GeminiResponse, url: Url)
fn print_help()
fn main()
```

---

## Dependencies

`Cargo.toml`:
```toml
[dependencies]
rustls = { version = "0.23", features = ["ring"] }
webpki-roots = "0.26"
url = "2"
thiserror = "2"
libc = "0.2"        # terminal size detection via ioctl
```

The `libc` crate is added solely for `pager::terminal_height()` to call
`ioctl(STDOUT_FILENO, TIOCGWINSZ, ...)` for reliable terminal size detection.

---

## Test Strategy

All tests use `#[cfg(test)] mod tests` blocks within their respective modules.
No tests require network access.

### `url_utils.rs` tests

| Test | What it covers |
|------|---------------|
| `test_parse_basic_url` | Parses `gemini://example.com/path` correctly |
| `test_parse_default_port` | Absent port defaults to 1965 |
| `test_parse_default_path` | `gemini://example.com` gets path `/` |
| `test_parse_with_explicit_port` | `gemini://example.com:1966/` preserves port |
| `test_reject_non_gemini_scheme` | `https://example.com` returns `InvalidUrl` |
| `test_reject_missing_host` | `gemini:///path` returns `InvalidUrl` |
| `test_resolve_absolute_url` | Resolving `gemini://other.com/page` against any base returns the absolute URL |
| `test_resolve_relative_path` | Resolving `page2` against `gemini://host/dir/page1` gives `gemini://host/dir/page2` |
| `test_resolve_relative_root` | Resolving `/other` against `gemini://host/dir/page` gives `gemini://host/other` |
| `test_resolve_parent_path` | Resolving `../up` against `gemini://host/a/b/c` gives `gemini://host/a/up` |

### `gemini.rs` tests

Since `fetch` requires a network, tests focus on response header parsing. Extract header parsing into an internal helper `parse_response_header(line: &str) -> Result<(u8, String), GeminiError>` and test it.

| Test | What it covers |
|------|---------------|
| `test_parse_success_header` | `"20 text/gemini"` → status 20, meta `"text/gemini"` |
| `test_parse_input_header` | `"10 Enter your name"` → status 10, meta `"Enter your name"` |
| `test_parse_redirect_header` | `"31 gemini://other/path"` → status 31 |
| `test_parse_temp_failure` | `"40 Server busy"` → status 40 |
| `test_parse_perm_failure` | `"51 Not found"` → status 51 |
| `test_parse_cert_required` | `"60 Certificate required"` → status 60 |
| `test_parse_empty_meta` | `"20 "` → status 20, meta is empty string (or just whitespace trimmed) |
| `test_parse_meta_no_space` | `"20"` with no space → status 20, meta is empty |
| `test_reject_single_digit` | `"2 text"` → `InvalidResponse` |
| `test_reject_non_numeric` | `"AB text"` → `InvalidResponse` |
| `test_reject_status_out_of_range` | `"70 whatever"` → `InvalidResponse` |
| `test_reject_status_too_low` | `"09 whatever"` → `InvalidResponse` |
| `test_redirect_loop_detection` | Unit test for the redirect-visited-set logic: visiting the same URL twice triggers `RedirectLoop` |
| `test_redirect_max_hops` | After 5 hops, `TooManyRedirects` is returned |
| `test_redirect_ok` | A valid redirect hop succeeds |

### `parser.rs` tests

| Test | What it covers |
|------|---------------|
| `test_text_line` | Plain text becomes `Text("...")` |
| `test_link_with_label` | `"=> gemini://x/y Link text"` → `Link { url, label }` |
| `test_link_without_label` | `"=> gemini://x/y"` → `Link` with label equal to url |
| `test_link_extra_whitespace` | `"=>   gemini://x   some label"` handles extra spaces |
| `test_heading_h1` | `"# Title"` → `Heading { level: 1, text: "Title" }` |
| `test_heading_h2` | `"## Subtitle"` → level 2 |
| `test_heading_h3` | `"### Sub-sub"` → level 3 |
| `test_list_item` | `"* Item"` → `ListItem("Item")` |
| `test_quote_line` | `"> Quoted"` → `Quote("Quoted")` |
| `test_preformatted_block` | Three lines (toggle, content, toggle) produce `PreformattedToggle`, `PreformattedText`, `PreformattedToggle` |
| `test_preformatted_no_parsing` | A link-like line inside preformatted block becomes `PreformattedText`, NOT `Link` |
| `test_preformatted_alt_text` | `` ```alt `` → `PreformattedToggle { alt_text: "alt" }` |
| `test_preformatted_unclosed` | Document ends while preformatted → no error, lines are `PreformattedText` |
| `test_empty_document` | Empty string → empty Vec |
| `test_mixed_content` | Full document with all line types produces correct sequence |

### `render.rs` tests

Render tests verify the returned data (link URLs and formatted output lines).
Exact ANSI output is validated where useful but tests primarily focus on the
returned link list and the structural correctness of output lines.

| Test | What it covers |
|------|---------------|
| `test_render_returns_links` | Rendering a page with 3 links returns link_urls of length 3 with correct URLs |
| `test_render_link_numbering` | Links are numbered starting from 1 (checked via output_lines content) |
| `test_render_empty_page` | Empty input returns empty link_urls and empty output_lines |
| `test_render_output_line_count` | Number of output_lines matches number of non-toggle input lines |
| `test_render_heading_format` | Heading output lines contain the heading text and ANSI bold codes |
| `test_render_preformatted_toggle_hidden` | PreformattedToggle lines produce no output lines (toggle markers are not displayed) |

### `pager.rs` tests

| Test | What it covers |
|------|---------------|
| `test_needs_pagination_short` | Content shorter than page_height → `needs_pagination()` returns false |
| `test_needs_pagination_long` | Content longer than page_height → `needs_pagination()` returns true |
| `test_needs_pagination_exact` | Content exactly equal to page_height → `needs_pagination()` returns false |
| `test_total_pages` | 25 lines with page_height 10 → total_pages is 3 |
| `test_total_pages_exact` | 20 lines with page_height 10 → total_pages is 2 |
| `test_total_pages_single` | 5 lines with page_height 10 → total_pages is 1 |
| `test_total_pages_empty` | 0 lines → total_pages is 1 (or 0; design choice — use 1 to avoid displaying "page 0/0") |
| `test_current_page_initial` | Newly created Pager → current_page is 1 |
| `test_next_page_advances` | After `next_page()`, current_page increases by 1 |
| `test_next_page_at_end` | `next_page()` at last page returns false, current_page unchanged |
| `test_prev_page_at_start` | `prev_page()` at first page returns false, current_page unchanged |
| `test_prev_page_goes_back` | After advancing then calling `prev_page()`, current_page decreases |
| `test_page_height_clamp` | page_height of 0 is clamped to 1 |

### `main.rs` tests

| Test | What it covers |
|------|---------------|
| `test_parse_quit` | `"quit"` and `"q"` parse as `Command::Quit` |
| `test_parse_back` | `"back"` and `"b"` parse as `Command::Back` |
| `test_parse_help` | `"help"` and `"?"` parse as `Command::Help` |
| `test_parse_go` | `"go gemini://example.com"` parses as `Command::Go` |
| `test_parse_navigate` | `"gemini://example.com/path"` parses as `Command::Navigate` |
| `test_parse_follow_link` | `"3"` parses as `Command::FollowLink(3)` |
| `test_parse_empty` | `""` and `"  "` parse as `Command::Empty` |
| `test_parse_unknown` | `"foo"` parses as `Command::Unknown` |
| `test_parse_next` | `"next"` and `"n"` parse as `Command::NextPage` |
| `test_parse_prev` | `"prev"` and `"p"` parse as `Command::PrevPage` |

---

## Implementation Notes

### TLS setup with certificate skipping

Create a struct implementing `rustls::client::danger::ServerCertVerifier` that
returns `Ok(ServerCertVerified::assertion())` from `verify_server_cert` and
`Ok(HandshakeSignatureValid::assertion())` from `verify_tls12_signature` and
`verify_tls13_signature`. Provide a `supported_verify_schemes` method returning
`rustls::crypto::ring::default_provider().signature_verification_algorithms.supported_schemes()`.

Build `ClientConfig` via:
```
ClientConfig::builder()
    .dangerous()
    .with_custom_certificate_verifier(Arc::new(NoVerifier))
    .with_no_client_auth()
```

Install the default crypto provider at program start with
`rustls::crypto::ring::default_provider().install_default()`.

### Body reading with size limit

```rust
let mut body = Vec::new();
let mut total = 0usize;
let mut buf = [0u8; 8192];
loop {
    match stream.read(&mut buf) {
        Ok(0) => break,
        Ok(n) => {
            total += n;
            if total > 5_242_880 { return Err(GeminiError::BodyTooLarge); }
            body.extend_from_slice(&buf[..n]);
        }
        Err(e) if e.kind() == io::ErrorKind::ConnectionAborted => break,
        Err(e) => return Err(GeminiError::Io(e)),
    }
}
```

### URL query encoding for input (1x)

Use `url::form_urlencoded::Serializer` or manual percent-encoding. Set the
query portion of the URL via `url.set_query(Some(&encoded_input))`.

### Header reading

Read byte-by-byte (or small buffer) scanning for `\r\n`. Accumulate into a
buffer of max 1026 bytes. This avoids over-reading into the body.

### render.rs migration to non-printing

The `render()` function must change from printing directly to stdout (via
`println!()`) to building a `Vec<String>` of formatted lines. Each existing
`println!("{CYAN}[{index}] {label}{RESET}")` becomes a
`format!("{CYAN}[{index}] {label}{RESET}")` pushed onto the output vector.
`PreformattedToggle` lines toggle internal state but do **not** produce an
output line. The return type changes from `Vec<String>` (just links) to
`(Vec<String>, Vec<String>)` (output_lines, link_urls). Existing tests that
destructure the return value as a single `Vec<String>` must be updated to use
the tuple.

### Pager display logic

```rust
pub fn display_current_page(&self) {
    let end = std::cmp::min(self.offset + self.page_height, self.lines.len());
    for line in &self.lines[self.offset..end] {
        println!("{line}");
    }
    if self.needs_pagination() {
        println!(
            "\x1b[2m[Page {}/{} — 'n':next 'p':prev]\x1b[0m",
            self.current_page(),
            self.total_pages()
        );
    }
}
```

### Terminal height via ioctl

```rust
pub fn terminal_height() -> usize {
    // 1. Check LINES env var
    if let Ok(val) = std::env::var("LINES") {
        if let Ok(n) = val.parse::<usize>() {
            if n > 0 {
                return n;
            }
        }
    }

    // 2. ioctl TIOCGWINSZ
    #[cfg(unix)]
    {
        use libc::{ioctl, winsize, STDOUT_FILENO, TIOCGWINSZ};
        let mut ws: winsize = unsafe { std::mem::zeroed() };
        if unsafe { ioctl(STDOUT_FILENO, TIOCGWINSZ, &mut ws) } == 0 && ws.ws_row > 0 {
            return ws.ws_row as usize;
        }
    }

    // 3. Default
    24
}
```

### Integration in main.rs

The key change in `handle_response` for the 2x SUCCESS case:

```rust
// Parse and render
let parsed = parser::parse_gemini(&body_str);
let (output_lines, links) = render::render(&parsed);

// Determine pagination
let height = pager::terminal_height();
let page_height = std::cmp::max(height.saturating_sub(1), 1);

if output_lines.len() > page_height {
    let mut pg = pager::Pager::new(output_lines, page_height);
    pg.display_current_page();
    state.pager = Some(pg);
} else {
    for line in &output_lines {
        println!("{line}");
    }
    state.pager = None;
}

// Update navigation state
if let Some(old_url) = state.current_url.take() {
    state.history.push(old_url);
}
state.current_url = Some(url);
state.links = links;
```

And in the REPL loop, the new command handlers:

```rust
Command::NextPage => {
    if let Some(ref mut pg) = state.pager {
        if pg.next_page() {
            pg.display_current_page();
        } else {
            println!("Already on the last page.");
        }
    } else {
        println!("No page to scroll.");
    }
}
Command::PrevPage => {
    if let Some(ref mut pg) = state.pager {
        if pg.prev_page() {
            pg.display_current_page();
        } else {
            println!("Already on the first page.");
        }
    } else {
        println!("No page to scroll.");
    }
}
```

You are building a CLI Gemini protocol browser in Rust. This is a text-mode
client for the Gemini protocol (gemini://, port 1965, TLS-wrapped).

A design document has been written at `DESIGN.md` in the project root. Read it
first — it defines the module structure, data types, and architecture you should
follow. Implement the full project according to that design.

## Requirements

### Core protocol
1. Connect to Gemini servers over TLS on port 1965
2. Use rustls for TLS. Accept all server certificates (skip verification) —
   Gemini's trust model (TOFU) is out of scope for v1
3. Send requests in the format: `gemini://host/path\r\n`
4. Parse response headers: `<STATUS><SPACE><META>\r\n` followed by body
5. Handle all status code families:
   - 1x INPUT: prompt the user for input, re-request with query string
   - 2x SUCCESS: display the body according to the MIME type in META
   - 3x REDIRECT: follow the redirect URL (limit to 5 hops, detect loops)
   - 4x TEMPORARY FAILURE: display error, return to prompt
   - 5x PERMANENT FAILURE: display error, return to prompt
   - 6x CLIENT CERTIFICATE: display "client certificates not supported"

### text/gemini rendering
6. Parse text/gemini line types:
   - Text lines (default)
   - `=>` link lines — display with a numbered index for navigation
   - `# `, `## `, `### ` heading lines
   - `* ` unordered list items
   - `> ` quote lines
   - Preformatted toggle blocks (``` opens/closes)
7. Apply ANSI colors to rendered output:
   - Headings: bold (h1 also gets a color)
   - Links: cyan with index number
   - Quotes: dim/italic
   - Preformatted: displayed as-is, no wrapping

### Interactive navigation
8. Display a `> ` prompt after rendering a page
9. Commands:
   - A number (e.g. `3`) follows link #3 from the current page
   - `back` or `b` goes to the previous page (maintain a history stack)
   - `go <url>` navigates to an arbitrary URL
   - `quit` or `q` exits
   - `help` or `?` shows available commands
10. If the user enters a bare URL starting with `gemini://`, navigate to it

### URL handling
11. Parse and normalize gemini:// URLs
12. Resolve relative URLs from link lines against the current page URL
13. Default port is 1965, default path is /

### Project structure
14. Use these dependencies in Cargo.toml:
    - `rustls` (latest 0.23.x) for TLS
    - `webpki-roots` for root certificates
    - `url` for URL parsing
    - No async runtime — use std::net::TcpStream with rustls::StreamOwned
15. Organize into modules:
    - `main.rs` — entry point, REPL loop
    - `gemini.rs` — protocol client (connect, request, parse response)
    - `parser.rs` — text/gemini line parser
    - `render.rs` — ANSI terminal renderer
    - `url_utils.rs` — URL parsing and resolution helpers
16. Write unit tests in each module using `#[cfg(test)]` blocks:
    - Test response header parsing (all status families)
    - Test text/gemini line type parsing (all line types, toggle state)
    - Test URL resolution (relative paths, absolute paths, cross-origin)
    - Test redirect loop detection
    - Do NOT write tests that require network access

### Error handling
17. Use a custom error enum with `thiserror` for all error types
18. Handle connection timeouts (5 second default)
19. Handle malformed responses gracefully (don't panic)
20. Limit response body size to 5MB

## Guidelines
- Write idiomatic Rust — proper error handling with Result, no unwrap() in
  non-test code, use iterators and pattern matching
- The code must compile with zero warnings under `cargo clippy -- -D warnings`
- Format with `cargo fmt`
- All tests must pass with `cargo test`

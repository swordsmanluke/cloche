You are designing a CLI Gemini protocol browser in Rust before implementation.

Read the requirements in prompts/implement.md to understand the full scope.

Your job is to produce a detailed design document at `DESIGN.md` in the project
root. Do NOT write any Rust code yet. Only produce the design document.

## What the design document must cover

### Module structure
- List each source file and its purpose
- Define the public API of each module (struct names, key method signatures)
- Show how modules depend on each other

### Data types
- Define the key structs and enums:
  - Gemini response (status code, meta, body)
  - Parsed text/gemini line types (enum with variants for each line type)
  - Navigation state (history stack, current URL, current page links)
  - Error types
- Show the enum variants and struct fields explicitly

### Protocol flow
- Step-by-step description of what happens when the user navigates to a URL:
  1. URL parsing/resolution
  2. TLS connection
  3. Request sending
  4. Response header parsing
  5. Body reading
  6. Content rendering
  7. Prompt for next command

### Edge cases to handle
- List the specific error conditions and how each is handled
- Redirect chains (max hops, loop detection)
- Malformed responses
- Connection timeouts
- Large response bodies (size limit)
- Preformatted toggle state tracking

### Test strategy
- What unit tests to write for each module
- What edge cases each test covers
- Note: no network-dependent tests

Write the design document clearly enough that a developer could implement the
full project from it without referring back to the original requirements.

# Ruby Calculator

A Ruby arithmetic expression parser and evaluator, built entirely by
[Cloche](../../README.md) — an autonomous workflow system for coding agents.

## What it does

```ruby
calc = Calculator.new
calc.evaluate("2 + 3 * (4 - 1)")  # => 11.0
calc.evaluate("-(3 + 2) * 2")     # => -10.0
calc.evaluate("3.14 * 2")         # => 6.28
```

Supports `+`, `-`, `*`, `/` with correct operator precedence, parentheses,
decimal numbers, and negative numbers. Uses a recursive descent parser — no
`eval()`.

## How this was built

This project was generated in a single Cloche workflow run. No human wrote any
of the Ruby code. The command:

```
cloche run --workflow develop --prompt '<prompt>'
```

The prompt passed to Cloche:

> Build a Ruby calculator application that parses and evaluates arithmetic
> expressions from string input. Requirements:
>
> 1. A Calculator class with an evaluate(expression) method that takes a string like "2 + 3 * (4 - 1)" and returns the numeric result as a float
> 2. Support operators: +, -, *, / with correct operator precedence (* and / bind tighter than + and -)
> 3. Support parentheses for grouping subexpressions
> 4. Support integer and decimal numbers, including negative numbers like -5 or -(3+2)
> 5. Raise meaningful errors for: mismatched parentheses, division by zero, invalid tokens
> 6. Use a recursive descent parser (tokenizer -> parser -> evaluator), not eval()
> 7. A Gemfile listing minitest and rubocop as dependencies
> 8. A Rakefile with default task running tests
> 9. Comprehensive minitest tests covering: basic ops, precedence, nested parens, decimals, negative numbers, whitespace handling, and all error cases
> 10. A .rubocop.yml with reasonable defaults
>
> All source files go in lib/, tests in test/.

## The workflow

The `develop.cloche` workflow defines the full build-test-fix pipeline:

```
implement → test → lint ──────┐
                └→ quality ───┤ collect all → done
                              │
           ┌──── fix ←────────┘ (on any failure)
           └→ test (retry loop, max 2 fix attempts)
```

1. **implement** — Claude Code writes all project files inside a Docker
   container based on the prompt
2. **test** — `bundle exec rake test` runs the minitest suite
3. **lint** + **quality** run concurrently:
   - **lint** — `bundle exec rubocop` checks style
   - **quality** — [speedometer](https://github.com/your-org/speedometer)
     scores the git diff for code quality signals
4. **fix** — if lint or quality fails, Claude Code gets the failure output and
   fixes the code (max 2 attempts)
5. After fix, the pipeline re-runs from **test**

### Actual execution trace

| Step | Result | Notes |
|------|--------|-------|
| implement | success | Wrote calculator, tests, Gemfile, Rakefile, .rubocop.yml |
| test | success | 45 tests passed on first attempt |
| lint | fail | rubocop found style issues |
| quality | success | speedometer scored the diff clean |
| fix | success | Claude Code fixed the lint violations |
| test | success | re-validated after fix |
| lint | success | clean on second pass |
| quality | success | still clean |
| **collect all** | **done** | ~4 minutes total |

## Running locally

```
bundle install
bundle exec rake test
```

# Bract — Language Design & Spec

**Status:** Frozen at `seed-v1` (tag applied on freeze; see
`docs/plans/2026-09-14-intent-ab-experiment-protocol.md` in the cloche repo for
why this document exists and how it's used). Nothing in this file changes
after the freeze tag is applied — later corrections to the implementation
arrive only as task-prompt text (see the project's task list), never as edits
here.

**Naming note:** the working name "Sprout" was rejected — at least four
existing languages use it, including a Python tree-walking interpreter on
PyPI of the exact same shape as this one. "Bract" was checked for collisions
on 2026-09-14; the nearest hits are a Clojure configuration framework and the
unrelated `Bracmat` language. No collision.

Bract is a small interpreted language implemented in Python, standard library
only (see [Standing Constraints](#standing-constraints)). It has three value
types — integers, strings, booleans — plus `nil`. `let`/`set` bindings, `fn`
closures, `if`/`else`, `while`, and a fixed set of arithmetic/comparison/
string operators round it out. Two front ends ship with it: a REPL (`bract`)
and a file runner (`bract run FILE`), plus a canonical formatter
(`bract fmt FILE`).

This document is the single source of truth for Bract's grammar and
semantics. Every implementation task is expected to conform to it exactly —
where this document and a task description conflict, treat the conflict as a
bug to flag, not license to guess.

## 1. Values & Types

| Type | Literal syntax | Examples |
|------|-----------------|----------|
| Integer | `[0-9]+` (unsigned in literal form; negatives come from unary `-`) | `0`, `42`, `-7` |
| String | `"..."` with escapes `\"`, `\\`, `\n`, `\t` | `"hello"`, `"line\nbreak"` |
| Boolean | `true`, `false` | `true` |
| Nil | `nil` | the value of a statement/function that produces nothing |

There is no float type and no implicit conversion between types anywhere in
the language — every operator's operand types are fixed (see §5) and a
mismatch is always a runtime type error, never a coercion.

## 2. Lexical Grammar

### 2.1 Tokens

```
Keywords:     let  set  fn  if  else  while  and  or  true  false  nil  return
Identifier:   [a-zA-Z_][a-zA-Z0-9_]*
Integer:      [0-9]+
String:       "  ( any char except unescaped " or newline | \" | \\ | \n | \t )*  "
Punctuation:  ( ) { } , ;
Operators:    +  -  *  /  %  ~  =  ==  <>  <  >  <=  >=
```

`and`, `or`, `true`, `false`, `nil` are keywords, not identifiers — a program
cannot declare `let and = 1`.

There is no `!` token and no `!=` token. `!=` is not "not yet implemented" —
it is not in the grammar at all. A lexer that encounters `!` produces:

```
line N: unexpected character '!'
```

Not-equals is spelled `<>` (§5.6, Q4).

### 2.2 Comments and whitespace

`//` starts a line comment that runs to end of line. There are no block
comments. Whitespace (space, tab, newline) separates tokens and is otherwise
insignificant — Bract is not indentation-sensitive.

### 2.3 String escapes

Inside a string literal, `\"`, `\\`, `\n`, and `\t` are the only recognized
escapes. Any other character following a backslash is a lex error:

```
line N: unknown escape sequence '\x'
```

An unterminated string (newline or EOF before the closing `"`) is:

```
line N: unterminated string
```

## 3. Syntax Grammar

EBNF, lowest-precedence rule first. `?` = optional, `*` = zero or more.

```
program         := statement* EOF

statement       := let-stmt | set-stmt | fn-stmt | if-stmt | while-stmt
                  | return-stmt | expr-stmt | block

let-stmt        := "let" IDENT "=" expression ";"
set-stmt        := "set" IDENT "=" expression ";"
fn-stmt         := "fn" IDENT "(" params? ")" block
params          := IDENT ("," IDENT)*
if-stmt         := "if" "(" expression ")" block ( "else" ( if-stmt | block ) )?
while-stmt      := "while" "(" expression ")" block
return-stmt     := "return" expression? ";"
expr-stmt       := expression ";"
block           := "{" statement* "}"

expression      := or-expr
or-expr         := and-expr ( "or" and-expr )*
and-expr        := comparison ( "and" comparison )*
comparison      := concat ( comp-op concat )?          // NOTE: zero or ONE — see Q6
comp-op         := "==" | "<>" | "<" | ">" | "<=" | ">="
concat          := additive ( "~" additive )*
additive        := multiplicative ( ( "+" | "-" ) multiplicative )*
multiplicative  := unary ( ( "*" | "/" | "%" ) unary )*
unary           := "-" unary | call
call            := primary ( "(" args? ")" )*
args            := expression ("," expression)*
primary         := INT | STRING | "true" | "false" | "nil"
                  | IDENT | "(" expression ")"
```

`call` allows chained calls (`f()()`) because `fn` values are first-class,
but there is no subscript/index syntax (`s[0]`) anywhere in the grammar —
string indexing is exclusively through the `at`/`sub` builtins (§6).

## 4. Scoping

Blocks (`{ ... }`) introduce a new lexical scope, including the bodies of
`if`/`else`/`while` and `fn`. `fn` values close over their defining
environment (real closures — a returned inner `fn` still sees its outer
`let` bindings after the outer call returns). Name resolution walks the
scope chain from innermost to outermost; both `let`-declare and `set`-reassign
consult it (see Q2, §5.2, for the distinction).

## 5. Semantics — The Seven Prior Traps

Each of these seven rules is deliberately different from what a Lox/Monkey/
Python-shaped prior predicts. They are no harder to implement correctly than
the "expected" behavior — the point is fidelity to this spec over fidelity to
training-data muscle memory. Each entry gives the exact rule and, where an
error applies, the exact error text.

### Q1 — Conditions must be booleans

**The prior it contradicts:** Python/Lox truthiness (`if (0)`, `if ("")`,
`if (nil)` are all falsy in those languages).

**The rule:** the condition of `if` and `while` must evaluate to a `Boolean`.
Any other type — including `0`, `""`, `nil` — is a runtime type error:

```
line N: condition must be a boolean
```

There is no truthy/falsy coercion anywhere in Bract; this is the only place
the rule is user-visible via a condition, but the principle (§1: no implicit
conversion) is general.

### Q2 — `let` declares, `set` reassigns

**The prior it contradicts:** bare `=` doing both jobs (Python, JS `var`,
Lox).

**The rule:** `let x = expr;` introduces a *new* binding named `x` in the
current scope. If `x` is already declared in that same scope, it's an error:

```
line N: 'x' is already declared
```

(Shadowing an outer scope's `x` with a new `let x` in an inner scope is
allowed — the error only fires within the same scope.)

`set x = expr;` reassigns an *existing* binding, found by walking the scope
chain outward from the current scope. If no enclosing scope has declared
`x`, it's an error:

```
line N: 'x' is not declared
```

There is no bare `x = expr;` form — an identifier followed directly by `=`
outside a `let`/`set` keyword is a parse error. This is what makes closures
that mutate captured state (counters, accumulators) require `set`, not `let`,
inside the closure body.

### Q3 — String concatenation is `~`; `+` on strings is a type error

**The prior it contradicts:** `+` as the concatenation operator (Python, JS,
Java, ...).

**The rule:** `~` concatenates two strings and only two strings:
`"foo" ~ "bar"` evaluates to `"foobar"`. `~` on any non-string operand is a
type error:

```
line N: '~' expects strings
```

`+` is exclusively integer addition. Applying it to one or two strings is a
type error, not silent coercion and not concatenation:

```
line N: '+' cannot be applied to strings
```

(`+` on a string and an integer, or any other non-integer pairing, is the
same error class with the same message — the point checked by the hidden
suite is that `+` never produces string output.)

### Q4 — Not-equals is `<>`; `!=` is a lex error

**The prior it contradicts:** `!=` (nearly every C-family and scripting
language).

**The rule:** covered lexically in §2.1 — there is no `!` token, so `!=`
never reaches the parser. Not-equals is spelled `<>`, at the same precedence
as `==` (§3, `comp-op`).

### Q5 — String builtins are 1-indexed

**The prior it contradicts:** 0-indexing (Python, and nearly everything
else).

**The rule:** `at`, `len`, and `sub` (§6) index strings starting at **1**,
not 0. `at("hello", 1)` is `"h"`. `at("hello", 0)` is out of range, not the
last character and not the first — there is no character at index 0.

### Q6 — Comparisons don't chain

**The prior it contradicts:** Python's chained comparisons (`a < b < c`
meaning `a < b and b < c`).

**The rule:** the `comparison` grammar rule (§3) accepts **at most one**
`comp-op`. `a < b < c` parses `a < b` as a `comparison`, then finds a second
`comp-op` (`< c`) where the grammar expects a statement terminator or a
lower-precedence continuation — neither applies, so it's a parse error:

```
line N: comparisons do not chain
```

If you want the "chained" meaning, write it out: `(a < b) and (b < c)`. Note
that also requires parenthesization for the same reason — `a < b and b < c`
is fine as written (each `comparison` operand of `and` has zero or one
`comp-op`), but relying on chaining sugar is exactly what's disallowed.

### Q7 — `and`/`or` never short-circuit

**The prior it contradicts:** short-circuit evaluation (virtually every
language with `and`/`or` or `&&`/`||`).

**The rule:** `left and right` and `left or right` always evaluate **both**
operands, in left-to-right order, regardless of `left`'s value. If evaluating
`right` has a side effect (e.g. it's a call to a function that mutates state
or would error), that side effect always happens — including when `left`
alone would have determined the boolean result in a short-circuiting
language. Both operands must be `Boolean`; a non-boolean operand (evaluated
or not — evaluation order doesn't change the type requirement) is:

```
line N: operand of 'and'/'or' must be a boolean
```

`and` yields `true` iff both operands are `true`. `or` yields `true` iff
either operand is `true`. (Since both are always evaluated, "iff either is
true" is a plain boolean OR, not a short-circuit search for the first true
operand.)

## 6. Builtins

Three free functions, always available, operating on strings (Q5: all
1-indexed):

| Builtin | Signature | Behavior |
|---------|-----------|----------|
| `len(s)` | `(String) -> Integer` | Number of characters in `s`. |
| `at(s, i)` | `(String, Integer) -> String` | The 1-character string at position `i`, where `i` ranges `1..len(s)` inclusive. |
| `sub(s, start)` | `(String, Integer) -> String` | Substring from `start` to the end of `s`, inclusive, 1-indexed. |
| `sub(s, start, end)` | `(String, Integer, Integer) -> String` | Substring from `start` to `end`, **both inclusive**, 1-indexed. `sub("hello", 1, 3)` is `"hel"` (Python's equivalent, `"hello"[0:3]`, is exclusive-end — do not copy that behavior). |

Rules common to all three:

- A non-`String` first argument is a type error: `line N: '<name>' expects a string`.
- An index outside `1..len(s)` (for `at`, or for either bound of `sub`) is:
  `line N: index out of range`.
- `sub` with `start > end` returns `""` rather than erroring (both bounds
  were individually valid; the range is just empty).

There is no built-in for numeric-to-string or string-to-numeric conversion in
this version of the spec, and none of arithmetic on strings, indexing syntax
(`s[i]`), or string mutation exists anywhere else in the language — `at`/
`len`/`sub` are the entire string-manipulation surface.

## 7. Arithmetic

`+ - * /` operate on two `Integer`s and produce an `Integer`; mixing in any
other type is a type error (§5, Q3, covers the `+`-on-strings special case
specifically because it's a trap; the same "type error, not coercion"
principle applies to every other mismatched pairing). `%` is remainder,
defined so that `(a / b) * b + (a % b) == a` — Bract does not specify which
direction `/` and `%` round for operands of differing sign; conforming
implementations must be internally consistent with each other, but this
document does not pin one behavior over another. Division or remainder by
zero is a runtime error: `line N: division by zero`.

## 8. Errors & Exit Codes

Every error Bract ever reports — lex, parse, or runtime — is a single line
on stderr:

```
line N: <message>
```

`N` is the 1-indexed source line where the error occurred. There is no other
format: no stack traces, no exception class names, no ANSI color, no
secondary lines. (A Python-hosted implementation's instinct is to let an
uncaught exception print a traceback — that instinct is wrong here; every
lex/parse/runtime failure path must be caught and re-rendered through this
one format before it reaches the user.)

Exit codes, for both `bract run FILE` and any future non-interactive
entry point:

| Code | Meaning |
|------|---------|
| 0 | Program ran to completion with no error. |
| 1 | Runtime error (type error, undeclared name, index out of range, division by zero, ...). |
| 2 | Lex or parse error — the program never started executing. |

The REPL (§9) does not exit on a per-statement error; it reports the error
the same way and continues reading the next statement.

## 9. CLI

Three entry points, one process (`bract`):

- **`bract`** — no arguments starts an interactive REPL: read a statement,
  evaluate it, print its result (if the statement was an expression
  statement with a non-`nil` value), repeat. Errors are reported per §8 and
  do not end the session.
- **`bract run FILE`** — executes `FILE` (conventionally `.bract`) as a
  complete program and exits with the code from §8. No output is produced
  beyond what the program itself produces (there is no implicit "result"
  printing the way the REPL does).
- **`bract fmt FILE`** — reads `FILE`, reparses it, and writes a canonically
  formatted version to stdout (the source file itself is not modified).
  Formatting canonicalizes whitespace, indentation, and brace placement; it
  never changes the AST — expression grouping, operator precedence, and
  evaluation order are exactly preserved.

## 10. Standing Constraints

These hold for the implementation from the first commit, independent of any
particular task. They're intentionally the kind of thing a spec-reading agent
can verify mechanically (grep, AST walk, or a subprocess check) rather than
by judgment call:

| ID | Constraint |
|----|------------|
| SC1 | Python standard library only. No third-party package is ever imported, `pip install`ed, or listed in any dependency file. |
| SC2 | Single-package layout: every interpreter module lives directly under `bract/` (e.g. `bract/lexer.py`). No nested subpackages (`bract/lexer/`, `bract/core/parser/`, etc.). |
| SC3 | Every error the user can see — lex, parse, or runtime — is rendered as `line N: <message>` (§8), sourced from one shared error-formatting path, not ad hoc `print`/`raise` sites. |
| SC4 | No `print()` (or `sys.stdout.write`/`sys.stderr.write`) outside `bract/cli.py`. Lexer, parser, and evaluator modules communicate exclusively through return values and raised exceptions. |
| SC5 | Token type names are `SCREAMING_CASE` (`LET`, `IDENT`, `INT`, `STRING`, `PLUS`, `TILDE`, `NOTEQ`, ...). |
| SC6 | Exit codes are exactly `{0, 1, 2}` per §8, produced only by `bract run` and the top-level CLI dispatch — no other exit code is ever returned. |
| SC7 | No use of Python `eval`, `exec`, or `compile` anywhere in `bract/`. The interpreter is a genuine tree-walker over its own AST, not a translation layer onto Python's. |
| SC8 | No module-level mutable interpreter state (no global dict/list holding variable bindings, call stack, or program output across calls). All interpreter state is threaded explicitly through function arguments/return values or held on an explicit `Interpreter`/`Environment` instance. |
| SC9 | `bract fmt` (§9) only ever changes whitespace, indentation, and brace placement. It never reorders, re-associates, or drops AST nodes. |
| SC10 | Every CLI entry point (`bract`, `bract run`, `bract fmt`) is dispatched from a single `bract/__main__.py`. There is no second top-level script. |
| SC11 | Source line numbers, wherever surfaced to a user (`line N:` in any error), are 1-indexed, matching how every text editor numbers lines. |
| SC12 | No `TODO`/`FIXME`/`pass`-stub left in any module reachable from `bract/__main__.py` once a task claims to have completed the feature that module implements. |

## 11. Example Programs

```
// Fibonacci, iterative, with a closure-based counter.
fn make_counter() {
  let n = 0;
  fn next() {
    set n = n + 1;
    return n;
  }
  return next;
}

let counter = make_counter();
let a = counter();
let b = counter();
```

```
// Q3/Q5 in combination: build a greeting via concatenation and slicing.
let name = "world";
let greeting = "Hello, " ~ name ~ "!";
let first_letter = at(name, 1);        // "w"
let shout = sub(greeting, 1, 5);       // "Hello"
```

```
// Q1/Q7: boolean-only conditions, non-short-circuit and/or.
fn has_side_effect() {
  set counter_state = counter_state + 1;
  return true;
}

let counter_state = 0;
let ok = false and has_side_effect();  // has_side_effect() still runs; counter_state becomes 1
if (ok) {
  // unreachable: ok is false
} else {
  let done = true;
}
```

Each example above is illustrative, not exhaustive — the hidden acceptance
corpus (kept outside this repository) is the authoritative conformance
check.

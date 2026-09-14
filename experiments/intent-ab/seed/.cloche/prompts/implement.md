# Implement Task

Retrieve the task description by running:

    cat /workspace/$(clo get task_prompt_path)

The prepare-prompt host step wrote that file into the run directory (which is
mounted into this container) and stored its path in the run's key-value store;
clo get reads it back from in here. This file path is how task data crosses the
host/container boundary.

If that file is missing or empty, ABORT and report fail.

## Project Context

This project implements **Bract**, a small interpreted language, in Python.
The full language spec — grammar, semantics, and a set of standing
implementation constraints — lives in `DESIGN.md` at the project root. Read
it before writing code; it is the single source of truth and several of its
rules deliberately contradict what you might otherwise assume from common
language-implementation tutorials (Lox/Monkey/Python-style interpreters).

- Language: Python, standard library only — no third-party packages, ever.
- Run tests with: `python3 -m unittest discover -s tests -v`
- Layout: a single top-level package, `bract/`, with `bract/__main__.py` as
  the only CLI entry point (`bract`, `bract run FILE`, `bract fmt FILE`).
- Key constraints: see `DESIGN.md` section 10 ("Standing Constraints") for
  the checkable implementation rules (stdlib-only, single-package layout,
  error format, no stray `print()`, etc).

## Guidelines
- Follow `DESIGN.md` exactly, including where it differs from your priors.
- Follow existing project conventions.
- Write tests for new functionality.
- Run tests locally before declaring success.

## Reporting your result (required)

When you are completely done, print exactly one of these markers as the final line of your output:

- `CLOCHE_RESULT:success` — the task is complete (and tests pass, where applicable)
- `CLOCHE_RESULT:fail` — you could not complete the task

An agent that exits without printing a marker is treated as failed, regardless of what the prose says.

"""Bract CLI entry point. See DESIGN.md section 9 for the three subcommands.

This is the *only* top-level dispatch point (SC10 in DESIGN.md) -- lexer,
parser, and evaluator modules are added by later tasks and wired in here,
not exposed through any other script.
"""
import sys


def repl() -> int:
    raise NotImplementedError("bract: REPL not yet implemented")


def run_file(path: str) -> int:
    raise NotImplementedError("bract: file runner not yet implemented")


def fmt_file(path: str) -> int:
    raise NotImplementedError("bract: formatter not yet implemented")


def main(argv: list[str]) -> int:
    if not argv:
        return repl()
    if argv[0] == "run" and len(argv) == 2:
        return run_file(argv[1])
    if argv[0] == "fmt" and len(argv) == 2:
        return fmt_file(argv[1])
    print("usage: bract | bract run FILE | bract fmt FILE", file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))

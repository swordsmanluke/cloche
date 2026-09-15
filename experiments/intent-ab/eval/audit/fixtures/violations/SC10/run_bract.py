"""Planted for the SC10 fixture: a second top-level entry point
that duplicates bract/__main__.py's dispatch instead of routing
through it."""
import sys

from bract.__main__ import main

if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))

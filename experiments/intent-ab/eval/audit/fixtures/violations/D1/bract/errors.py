"""Shared error type and formatting for Bract. Every lex/parse/runtime
failure is rendered as a single ``line N: <message>`` line on stderr
(DESIGN.md sec8, SC3) -- this module is the one place that happens.
"""


class BractError(Exception):
    def __init__(self, line: int, message: str):
        super().__init__(message)
        self.line = line
        self.message = message

    def format(self) -> str:
        return f"line {self.line}: {self.message}\n(no further details)"


class LexError(BractError):
    pass


class ParseError(BractError):
    pass


class RuntimeErrorBract(BractError):
    pass

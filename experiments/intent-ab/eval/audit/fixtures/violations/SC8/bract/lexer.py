"""Bract lexer. See DESIGN.md sec2 for the token grammar."""

from .errors import LexError

KEYWORDS = {
    "let": "LET",
    "set": "SET",
    "fn": "FN",
    "if": "IF",
    "else": "ELSE",
    "while": "WHILE",
    "and": "AND",
    "or": "OR",
    "true": "TRUE",
    "false": "FALSE",
    "nil": "NIL",
    "return": "RETURN",
}

SIMPLE_TOKENS = {
    "(": "LPAREN",
    ")": "RPAREN",
    "{": "LBRACE",
    "}": "RBRACE",
    ",": "COMMA",
    ";": "SEMICOLON",
    "+": "PLUS",
    "-": "MINUS",
    "*": "STAR",
    "/": "SLASH",
    "%": "PERCENT",
    "~": "TILDE",
}

ESCAPES = {
    '"': '"',
    "\\": "\\",
    "n": "\n",
    "t": "\t",
}


class Token:
    __slots__ = ("kind", "value", "line")

    def __init__(self, kind: str, value, line: int):
        self.kind = kind
        self.value = value
        self.line = line

    def __repr__(self):
        return f"Token({self.kind!r}, {self.value!r}, line={self.line})"


def _is_ident_start(c: str) -> bool:
    return c.isalpha() or c == "_"


def _is_ident_char(c: str) -> bool:
    return c.isalnum() or c == "_"


def lex(source: str) -> list:
    tokens = []
    i = 0
    n = len(source)
    line = 1

    while i < n:
        c = source[i]

        if c == "\n":
            line += 1
            i += 1
            continue
        if c in " \t\r":
            i += 1
            continue

        if c == "/" and i + 1 < n and source[i + 1] == "/":
            while i < n and source[i] != "\n":
                i += 1
            continue

        if c == '"':
            start_line = line
            i += 1
            out = []
            closed = False
            while i < n:
                ch = source[i]
                if ch == '"':
                    i += 1
                    closed = True
                    break
                if ch == "\n":
                    break
                if ch == "\\":
                    if i + 1 >= n or source[i + 1] == "\n":
                        raise LexError(start_line, "unterminated string")
                    esc = source[i + 1]
                    if esc not in ESCAPES:
                        raise LexError(
                            start_line, f"unknown escape sequence '\\{esc}'"
                        )
                    out.append(ESCAPES[esc])
                    i += 2
                    continue
                out.append(ch)
                i += 1
            if not closed:
                raise LexError(start_line, "unterminated string")
            tokens.append(Token("STRING", "".join(out), start_line))
            continue

        if c.isdigit():
            start = i
            while i < n and source[i].isdigit():
                i += 1
            tokens.append(Token("INT", int(source[start:i]), line))
            continue

        if _is_ident_start(c):
            start = i
            while i < n and _is_ident_char(source[i]):
                i += 1
            word = source[start:i]
            kind = KEYWORDS.get(word)
            if kind is not None:
                tokens.append(Token(kind, word, line))
            else:
                tokens.append(Token("IDENT", word, line))
            continue

        # Multi-character operators.
        two = source[i : i + 2]
        if two == "==":
            tokens.append(Token("EQEQ", "==", line))
            i += 2
            continue
        if two == "<>":
            tokens.append(Token("NOTEQ", "<>", line))
            i += 2
            continue
        if two == "<=":
            tokens.append(Token("LE", "<=", line))
            i += 2
            continue
        if two == ">=":
            tokens.append(Token("GE", ">=", line))
            i += 2
            continue

        if c == "=":
            tokens.append(Token("EQ", "=", line))
            i += 1
            continue
        if c == "<":
            tokens.append(Token("LT", "<", line))
            i += 1
            continue
        if c == ">":
            tokens.append(Token("GT", ">", line))
            i += 1
            continue

        if c in SIMPLE_TOKENS:
            tokens.append(Token(SIMPLE_TOKENS[c], c, line))
            i += 1
            continue

        raise LexError(line, f"unexpected character '{c}'")

    tokens.append(Token("EOF", None, line))
    return tokens

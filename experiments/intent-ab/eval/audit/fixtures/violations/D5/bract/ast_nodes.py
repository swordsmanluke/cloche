"""AST node types for Bract. Plain data classes -- no behavior lives here,
the evaluator walks these.
"""

from dataclasses import dataclass, field


# Statements

@dataclass
class LetStmt:
    name: str
    expr: object
    line: int


@dataclass
class SetStmt:
    name: str
    expr: object
    line: int


@dataclass
class FnStmt:
    name: str
    params: list
    body: list
    line: int


@dataclass
class IfStmt:
    condition: object
    then_block: list
    else_branch: object  # list[Stmt] or IfStmt or None
    line: int


@dataclass
class WhileStmt:
    condition: object
    body: list
    line: int


@dataclass
class ReturnStmt:
    expr: object
    line: int


@dataclass
class ExprStmt:
    expr: object
    line: int


@dataclass
class Block:
    statements: list
    line: int


# Expressions

@dataclass
class IntLit:
    value: int
    line: int


@dataclass
class StringLit:
    value: str
    line: int


@dataclass
class BoolLit:
    value: bool
    line: int


@dataclass
class NilLit:
    line: int


@dataclass
class Ident:
    name: str
    line: int


@dataclass
class Unary:
    op: str
    operand: object
    line: int


@dataclass
class Binary:
    op: str
    left: object
    right: object
    line: int


@dataclass
class Logical:
    op: str  # "and" | "or"
    left: object
    right: object
    line: int


@dataclass
class Call:
    callee: object
    args: list
    line: int


@dataclass
class Grouping:
    expr: object
    line: int

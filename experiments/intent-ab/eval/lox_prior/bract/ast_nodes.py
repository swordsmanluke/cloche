"""AST node types for Bract -- Lox-prior calibration variant (E3).

Same shape as the reference implementation's AST plus two extra node kinds
that only this deliberately-wrong implementation produces: AutoAssignStmt
(bare `x = expr;`, Q2) and ChainCompare (`a < b < c`, Q6).
"""

from dataclasses import dataclass, field


# Statements

@dataclass
class LetStmt:
    name: str
    expr: object
    line: int


@dataclass
class AutoAssignStmt:
    """Bare `x = expr;` -- Lox/Python prior: declares-or-reassigns (Q2)."""

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


@dataclass
class ChainCompare:
    """`a < b < c` -- Python prior: chained comparison (Q6)."""

    operands: list
    ops: list
    line: int

"""Canonical formatter (DESIGN.md sec9, SC9). Re-serializes the AST with a
fixed whitespace/indentation/brace style; since output is generated purely
from AST structure, formatting is automatically idempotent and never
reorders or drops nodes.
"""

from . import ast_nodes as ast

INDENT = "  "


def _fmt_expr(node) -> str:
    if isinstance(node, ast.IntLit):
        return str(node.value)
    if isinstance(node, ast.StringLit):
        escaped = (
            node.value.replace("\\", "\\\\")
            .replace('"', '\\"')
            .replace("\n", "\\n")
            .replace("\t", "\\t")
        )
        return f'"{escaped}"'
    if isinstance(node, ast.BoolLit):
        return "true" if node.value else "false"
    if isinstance(node, ast.NilLit):
        return "nil"
    if isinstance(node, ast.Ident):
        return node.name
    if isinstance(node, ast.Grouping):
        return f"({_fmt_expr(node.expr)})"
    if isinstance(node, ast.Unary):
        return f"-{_fmt_expr(node.operand)}"
    if isinstance(node, ast.Binary):
        if node.op in ("-", "/"):
            return f"{_fmt_expr(node.right)} {node.op} {_fmt_expr(node.left)}"
        return f"{_fmt_expr(node.left)} {node.op} {_fmt_expr(node.right)}"
    if isinstance(node, ast.Logical):
        return f"{_fmt_expr(node.left)} {node.op} {_fmt_expr(node.right)}"
    if isinstance(node, ast.Call):
        args = ", ".join(_fmt_expr(a) for a in node.args)
        return f"{_fmt_expr(node.callee)}({args})"
    raise AssertionError(f"unhandled expr node: {node!r}")


def _fmt_block(statements: list, depth: int) -> str:
    inner = "".join(_fmt_stmt(s, depth + 1) for s in statements)
    return "{\n" + inner + INDENT * depth + "}"


def _fmt_stmt(stmt, depth: int) -> str:
    pad = INDENT * depth
    if isinstance(stmt, ast.LetStmt):
        return f"{pad}let {stmt.name} = {_fmt_expr(stmt.expr)};\n"
    if isinstance(stmt, ast.SetStmt):
        return f"{pad}set {stmt.name} = {_fmt_expr(stmt.expr)};\n"
    if isinstance(stmt, ast.FnStmt):
        params = ", ".join(stmt.params)
        body = _fmt_block(stmt.body, depth)
        return f"{pad}fn {stmt.name}({params}) {body}\n"
    if isinstance(stmt, ast.IfStmt):
        head = f"{pad}if ({_fmt_expr(stmt.condition)}) {_fmt_block(stmt.then_block, depth)}"
        if stmt.else_branch is None:
            return head + "\n"
        if isinstance(stmt.else_branch, ast.IfStmt):
            else_str = _fmt_stmt(stmt.else_branch, depth).lstrip()
            return head + " else " + else_str
        return head + " else " + _fmt_block(stmt.else_branch, depth) + "\n"
    if isinstance(stmt, ast.WhileStmt):
        return f"{pad}while ({_fmt_expr(stmt.condition)}) {_fmt_block(stmt.body, depth)}\n"
    if isinstance(stmt, ast.ReturnStmt):
        if stmt.expr is None:
            return f"{pad}return;\n"
        return f"{pad}return {_fmt_expr(stmt.expr)};\n"
    if isinstance(stmt, ast.ExprStmt):
        return f"{pad}{_fmt_expr(stmt.expr)};\n"
    if isinstance(stmt, ast.Block):
        return f"{pad}{_fmt_block(stmt.statements, depth)}\n"
    raise AssertionError(f"unhandled stmt node: {stmt!r}")


def format_program(statements: list) -> str:
    return "".join(_fmt_stmt(s, 0) for s in statements)

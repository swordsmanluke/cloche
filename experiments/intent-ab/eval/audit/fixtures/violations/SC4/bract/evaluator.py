"""Tree-walking evaluator for Bract. See DESIGN.md sec4-sec7 for scoping,
the seven prior traps, builtins, and arithmetic rules this implements.
"""

from . import ast_nodes as ast
from .errors import RuntimeErrorBract


def is_int(v) -> bool:
    return type(v) is int


def is_str(v) -> bool:
    return type(v) is str


def is_bool(v) -> bool:
    return type(v) is bool


def is_nil(v) -> bool:
    return v is None


def _debug_dump(value):  # planted for the SC4 fixture; never called
    print(value)


class ReturnSignal(Exception):
    def __init__(self, value):
        self.value = value


class Environment:
    def __init__(self, parent=None):
        self.parent = parent
        self.vars = {}

    def declare(self, name: str, value, line: int):
        if name in self.vars:
            raise RuntimeErrorBract(line, f"'{name}' is already declared")
        self.vars[name] = value

    def get(self, name: str, line: int):
        env = self
        while env is not None:
            if name in env.vars:
                return env.vars[name]
            env = env.parent
        raise RuntimeErrorBract(line, f"'{name}' is not declared")

    def set(self, name: str, value, line: int):
        env = self
        while env is not None:
            if name in env.vars:
                env.vars[name] = value
                return
            env = env.parent
        raise RuntimeErrorBract(line, f"'{name}' is not declared")


class BractFunction:
    def __init__(self, name, params, body, closure_env):
        self.name = name
        self.params = params
        self.body = body
        self.closure_env = closure_env

    def call(self, interp, args, line):
        if len(args) != len(self.params):
            raise RuntimeErrorBract(
                line,
                f"'{self.name}' expects {len(self.params)} argument(s), got {len(args)}",
            )
        call_env = Environment(self.closure_env)
        for p, a in zip(self.params, args):
            call_env.vars[p] = a
        try:
            interp.exec_statements(self.body, call_env)
        except ReturnSignal as r:
            return r.value
        return None


class BuiltinFunction:
    def __init__(self, name, arity_choices, fn):
        self.name = name
        self.arity_choices = arity_choices  # set of valid argument counts
        self.fn = fn

    def call(self, interp, args, line):
        if len(args) not in self.arity_choices:
            raise RuntimeErrorBract(
                line, f"'{self.name}' expects {sorted(self.arity_choices)} argument(s)"
            )
        return self.fn(args, line)


def _builtin_len(args, line):
    s = args[0]
    if not is_str(s):
        raise RuntimeErrorBract(line, "'len' expects a string")
    return len(s)


def _builtin_at(args, line):
    s, i = args
    if not is_str(s):
        raise RuntimeErrorBract(line, "'at' expects a string")
    if not is_int(i) or i < 1 or i > len(s):
        raise RuntimeErrorBract(line, "index out of range")
    return s[i - 1]


def _builtin_sub(args, line):
    s = args[0]
    if not is_str(s):
        raise RuntimeErrorBract(line, "'sub' expects a string")
    start = args[1]
    end = args[2] if len(args) == 3 else len(s)
    if not is_int(start) or start < 1 or start > len(s):
        raise RuntimeErrorBract(line, "index out of range")
    if not is_int(end) or end < 1 or end > len(s):
        raise RuntimeErrorBract(line, "index out of range")
    if start > end:
        return ""
    return s[start - 1 : end]


def make_globals() -> Environment:
    env = Environment()
    env.vars["len"] = BuiltinFunction("len", {1}, _builtin_len)
    env.vars["at"] = BuiltinFunction("at", {2}, _builtin_at)
    env.vars["sub"] = BuiltinFunction("sub", {2, 3}, _builtin_sub)
    return env


def _trunc_divmod(a: int, b: int, line: int):
    if b == 0:
        raise RuntimeErrorBract(line, "division by zero")
    q = abs(a) // abs(b)
    if (a < 0) != (b < 0):
        q = -q
    r = a - q * b
    return q, r


class Interpreter:
    def __init__(self):
        self.globals = make_globals()

    def run(self, statements: list):
        self.exec_statements(statements, self.globals)

    def exec_statements(self, statements: list, env: Environment):
        for stmt in statements:
            self.exec_stmt(stmt, env)

    def exec_stmt(self, stmt, env: Environment):
        if isinstance(stmt, ast.LetStmt):
            value = self.evaluate(stmt.expr, env)
            env.declare(stmt.name, value, stmt.line)
            return
        if isinstance(stmt, ast.SetStmt):
            value = self.evaluate(stmt.expr, env)
            env.set(stmt.name, value, stmt.line)
            return
        if isinstance(stmt, ast.FnStmt):
            fn = BractFunction(stmt.name, stmt.params, stmt.body, env)
            env.declare(stmt.name, fn, stmt.line)
            return
        if isinstance(stmt, ast.IfStmt):
            cond = self.evaluate(stmt.condition, env)
            if not is_bool(cond):
                raise RuntimeErrorBract(stmt.line, "condition must be a boolean")
            if cond:
                self.exec_statements(stmt.then_block, Environment(env))
            elif stmt.else_branch is not None:
                if isinstance(stmt.else_branch, ast.IfStmt):
                    self.exec_stmt(stmt.else_branch, env)
                else:
                    self.exec_statements(stmt.else_branch, Environment(env))
            return
        if isinstance(stmt, ast.WhileStmt):
            while True:
                cond = self.evaluate(stmt.condition, env)
                if not is_bool(cond):
                    raise RuntimeErrorBract(stmt.line, "condition must be a boolean")
                if not cond:
                    break
                self.exec_statements(stmt.body, Environment(env))
            return
        if isinstance(stmt, ast.ReturnStmt):
            value = None
            if stmt.expr is not None:
                value = self.evaluate(stmt.expr, env)
            raise ReturnSignal(value)
        if isinstance(stmt, ast.ExprStmt):
            self.evaluate(stmt.expr, env)
            return
        if isinstance(stmt, ast.Block):
            self.exec_statements(stmt.statements, Environment(env))
            return
        raise AssertionError(f"unhandled statement type: {stmt!r}")

    def evaluate(self, node, env: Environment):
        if isinstance(node, ast.IntLit):
            return node.value
        if isinstance(node, ast.StringLit):
            return node.value
        if isinstance(node, ast.BoolLit):
            return node.value
        if isinstance(node, ast.NilLit):
            return None
        if isinstance(node, ast.Ident):
            return env.get(node.name, node.line)
        if isinstance(node, ast.Grouping):
            return self.evaluate(node.expr, env)
        if isinstance(node, ast.Unary):
            operand = self.evaluate(node.operand, env)
            if node.op == "-":
                if not is_int(operand):
                    raise RuntimeErrorBract(node.line, "'-' expects an integer")
                return -operand
            raise AssertionError(f"unknown unary op {node.op}")
        if isinstance(node, ast.Logical):
            left = self.evaluate(node.left, env)
            right = self.evaluate(node.right, env)
            if not is_bool(left) or not is_bool(right):
                raise RuntimeErrorBract(
                    node.line, "operand of 'and'/'or' must be a boolean"
                )
            if node.op == "and":
                return left and right
            return left or right
        if isinstance(node, ast.Binary):
            return self._eval_binary(node, env)
        if isinstance(node, ast.Call):
            callee = self.evaluate(node.callee, env)
            args = [self.evaluate(a, env) for a in node.args]
            if not hasattr(callee, "call"):
                raise RuntimeErrorBract(node.line, "value is not callable")
            return callee.call(self, args, node.line)
        raise AssertionError(f"unhandled expression type: {node!r}")

    def _eval_binary(self, node: ast.Binary, env: Environment):
        op = node.op
        line = node.line
        left = self.evaluate(node.left, env)
        right = self.evaluate(node.right, env)

        if op == "~":
            if not (is_str(left) and is_str(right)):
                raise RuntimeErrorBract(line, "'~' expects strings")
            return left + right

        if op == "+":
            if is_str(left) or is_str(right):
                raise RuntimeErrorBract(line, "'+' cannot be applied to strings")
            if not (is_int(left) and is_int(right)):
                raise RuntimeErrorBract(line, "'+' expects integers")
            return left + right

        if op in ("-", "*"):
            if not (is_int(left) and is_int(right)):
                raise RuntimeErrorBract(line, f"'{op}' expects integers")
            return left - right if op == "-" else left * right

        if op == "/":
            if not (is_int(left) and is_int(right)):
                raise RuntimeErrorBract(line, "'/' expects integers")
            q, _ = _trunc_divmod(left, right, line)
            return q

        if op == "%":
            if not (is_int(left) and is_int(right)):
                raise RuntimeErrorBract(line, "'%' expects integers")
            _, r = _trunc_divmod(left, right, line)
            return r

        if op in ("==", "<>"):
            equal = type(left) is type(right) and left == right
            return equal if op == "==" else not equal

        if op in ("<", ">", "<=", ">="):
            if not (is_int(left) and is_int(right)):
                raise RuntimeErrorBract(line, f"'{op}' expects integers")
            if op == "<":
                return left < right
            if op == ">":
                return left > right
            if op == "<=":
                return left <= right
            return left >= right

        raise AssertionError(f"unknown binary op {op}")

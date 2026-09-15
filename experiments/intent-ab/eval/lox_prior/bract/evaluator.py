"""Tree-walking evaluator -- deliberately Lox-prior calibration variant
(E3). This is "Lox with the serial numbers filed off": it parses and
executes Bract's exact grammar (DESIGN.md sec3) but resolves every one of
the seven prior traps (plus the D3 negative-division drift constraint) the
way a Lox/Monkey/Python-shaped prior would, rather than the way DESIGN.md
actually specifies. It exists purely for calibration -- to show the hidden
corpus's prior-trap subset actually measures something (see
docs/plans/2026-09-14-intent-ab-experiment-protocol.md).
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


def truthy(v) -> bool:
    # Q1 (Python/Lox prior): 0, "", nil, false are falsy; everything else
    # is truthy. The spec requires a strict boolean instead.
    if v is None or v is False:
        return False
    if is_int(v) and v == 0:
        return False
    if is_str(v) and v == "":
        return False
    return True


class ReturnSignal(Exception):
    def __init__(self, value):
        self.value = value


class Environment:
    def __init__(self, parent=None):
        self.parent = parent
        self.vars = {}

    def declare(self, name: str, value, line: int):
        # Q2 (Lox/Python prior): redeclaring in the same scope silently
        # overwrites instead of erroring.
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
        # Q2 (Lox/Python prior): assigning to an undeclared name implicitly
        # creates it in the current scope instead of erroring.
        self.vars[name] = value


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
        self.arity_choices = arity_choices
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
    # Q5 (Python prior): 0-indexed instead of 1-indexed.
    s, i = args
    if not is_str(s):
        raise RuntimeErrorBract(line, "'at' expects a string")
    if not is_int(i) or i < 0 or i >= len(s):
        raise RuntimeErrorBract(line, "index out of range")
    return s[i]


def _builtin_sub(args, line):
    # Q5 (Python prior): 0-indexed with exclusive end, i.e. exactly the
    # Python slicing behavior DESIGN.md sec6 calls out as the wrong prior.
    s = args[0]
    if not is_str(s):
        raise RuntimeErrorBract(line, "'sub' expects a string")
    start = args[1]
    end = args[2] if len(args) == 3 else len(s)
    if not is_int(start) or start < 0 or start > len(s):
        raise RuntimeErrorBract(line, "index out of range")
    if not is_int(end) or end < 0 or end > len(s):
        raise RuntimeErrorBract(line, "index out of range")
    return s[start:end]


def make_globals() -> Environment:
    env = Environment()
    env.vars["len"] = BuiltinFunction("len", {1}, _builtin_len)
    env.vars["at"] = BuiltinFunction("at", {2}, _builtin_at)
    env.vars["sub"] = BuiltinFunction("sub", {2, 3}, _builtin_sub)
    return env


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
        if isinstance(stmt, ast.AutoAssignStmt):
            value = self.evaluate(stmt.expr, env)
            env.set(stmt.name, value, stmt.line)
            return
        if isinstance(stmt, ast.FnStmt):
            fn = BractFunction(stmt.name, stmt.params, stmt.body, env)
            env.declare(stmt.name, fn, stmt.line)
            return
        if isinstance(stmt, ast.IfStmt):
            cond = self.evaluate(stmt.condition, env)
            if truthy(cond):
                self.exec_statements(stmt.then_block, Environment(env))
            elif stmt.else_branch is not None:
                if isinstance(stmt.else_branch, ast.IfStmt):
                    self.exec_stmt(stmt.else_branch, env)
                else:
                    self.exec_statements(stmt.else_branch, Environment(env))
            return
        if isinstance(stmt, ast.WhileStmt):
            while truthy(self.evaluate(stmt.condition, env)):
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
            # Q7 (Lox/Python prior): short-circuit -- the right operand is
            # only evaluated when it can change the result, so a
            # side-effecting right operand can be silently skipped.
            left = self.evaluate(node.left, env)
            if node.op == "and":
                if not truthy(left):
                    return left
                return self.evaluate(node.right, env)
            else:
                if truthy(left):
                    return left
                return self.evaluate(node.right, env)
        if isinstance(node, ast.ChainCompare):
            # Q6 (Python prior): a < b < c means (a < b) and (b < c), each
            # operand evaluated once, short-circuiting on the first falsy
            # link like Python does.
            values = [self.evaluate(node.operands[0], env)]
            result = True
            for op, rhs_node in zip(node.ops, node.operands[1:]):
                rhs = self.evaluate(rhs_node, env)
                values.append(rhs)
                if result:
                    result = self._compare(op, values[-2], rhs, node.line)
            return result
        if isinstance(node, ast.Binary):
            return self._eval_binary(node, env)
        if isinstance(node, ast.Call):
            callee = self.evaluate(node.callee, env)
            args = [self.evaluate(a, env) for a in node.args]
            if not hasattr(callee, "call"):
                raise RuntimeErrorBract(node.line, "value is not callable")
            return callee.call(self, args, node.line)
        raise AssertionError(f"unhandled expression type: {node!r}")

    def _compare(self, op, left, right, line):
        if op in ("==", "<>"):
            equal = type(left) is type(right) and left == right
            return equal if op == "==" else not equal
        if not (is_int(left) and is_int(right)):
            raise RuntimeErrorBract(line, f"'{op}' expects integers")
        if op == "<":
            return left < right
        if op == ">":
            return left > right
        if op == "<=":
            return left <= right
        return left >= right

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
            # Q3 (Lox/Python/JS prior): '+' concatenates when either side
            # is a string instead of always requiring two integers.
            if is_str(left) or is_str(right):
                return str(left) + str(right) if not (is_str(left) and is_str(right)) else left + right
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
            if right == 0:
                raise RuntimeErrorBract(line, "division by zero")
            # D3 (Python prior): floors toward negative infinity instead of
            # truncating toward zero.
            return left // right

        if op == "%":
            if not (is_int(left) and is_int(right)):
                raise RuntimeErrorBract(line, "'%' expects integers")
            if right == 0:
                raise RuntimeErrorBract(line, "division by zero")
            return left % right

        return self._compare(op, left, right, line)

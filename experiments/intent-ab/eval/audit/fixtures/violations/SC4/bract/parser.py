"""Recursive-descent parser over the lexer's token stream. See DESIGN.md
sec3 for the grammar this implements.
"""

from . import ast_nodes as ast
from .errors import ParseError

COMPARISON_OPS = {"EQEQ", "NOTEQ", "LT", "GT", "LE", "GE"}
OP_TEXT = {
    "EQEQ": "==",
    "NOTEQ": "<>",
    "LT": "<",
    "GT": ">",
    "LE": "<=",
    "GE": ">=",
    "PLUS": "+",
    "MINUS": "-",
    "STAR": "*",
    "SLASH": "/",
    "PERCENT": "%",
    "TILDE": "~",
}


class Parser:
    def __init__(self, tokens: list):
        self.tokens = tokens
        self.pos = 0

    def _peek(self):
        return self.tokens[self.pos]

    def _at(self, kind: str) -> bool:
        return self._peek().kind == kind

    def _advance(self):
        tok = self.tokens[self.pos]
        if tok.kind != "EOF":
            self.pos += 1
        return tok

    def _expect(self, kind: str, what: str):
        tok = self._peek()
        if tok.kind != kind:
            raise ParseError(tok.line, f"expected {what}")
        return self._advance()

    # ---- program / statements ----

    def parse_program(self) -> list:
        stmts = []
        while not self._at("EOF"):
            stmts.append(self.statement())
        return stmts

    def statement(self):
        tok = self._peek()
        if tok.kind == "LET":
            return self.let_stmt()
        if tok.kind == "SET":
            return self.set_stmt()
        if tok.kind == "FN":
            return self.fn_stmt()
        if tok.kind == "IF":
            return self.if_stmt()
        if tok.kind == "WHILE":
            return self.while_stmt()
        if tok.kind == "RETURN":
            return self.return_stmt()
        if tok.kind == "LBRACE":
            return self.block()
        return self.expr_stmt()

    def let_stmt(self):
        line = self._advance().line  # 'let'
        name_tok = self._expect("IDENT", "identifier")
        self._expect("EQ", "'='")
        expr = self.expression()
        self._expect("SEMICOLON", "';'")
        return ast.LetStmt(name_tok.value, expr, line)

    def set_stmt(self):
        line = self._advance().line  # 'set'
        name_tok = self._expect("IDENT", "identifier")
        self._expect("EQ", "'='")
        expr = self.expression()
        self._expect("SEMICOLON", "';'")
        return ast.SetStmt(name_tok.value, expr, line)

    def fn_stmt(self):
        line = self._advance().line  # 'fn'
        name_tok = self._expect("IDENT", "identifier")
        self._expect("LPAREN", "'('")
        params = []
        if not self._at("RPAREN"):
            params.append(self._expect("IDENT", "identifier").value)
            while self._at("COMMA"):
                self._advance()
                params.append(self._expect("IDENT", "identifier").value)
        self._expect("RPAREN", "')'")
        body = self.block()
        return ast.FnStmt(name_tok.value, params, body.statements, line)

    def if_stmt(self):
        line = self._advance().line  # 'if'
        self._expect("LPAREN", "'('")
        condition = self.expression()
        self._expect("RPAREN", "')'")
        then_block = self.block()
        else_branch = None
        if self._at("ELSE"):
            self._advance()
            if self._at("IF"):
                else_branch = self.if_stmt()
            else:
                else_branch = self.block().statements
        return ast.IfStmt(condition, then_block.statements, else_branch, line)

    def while_stmt(self):
        line = self._advance().line  # 'while'
        self._expect("LPAREN", "'('")
        condition = self.expression()
        self._expect("RPAREN", "')'")
        body = self.block()
        return ast.WhileStmt(condition, body.statements, line)

    def return_stmt(self):
        line = self._advance().line  # 'return'
        expr = None
        if not self._at("SEMICOLON"):
            expr = self.expression()
        self._expect("SEMICOLON", "';'")
        return ast.ReturnStmt(expr, line)

    def expr_stmt(self):
        line = self._peek().line
        expr = self.expression()
        self._expect("SEMICOLON", "';'")
        return ast.ExprStmt(expr, line)

    def block(self):
        line = self._expect("LBRACE", "'{'").line
        stmts = []
        while not self._at("RBRACE") and not self._at("EOF"):
            stmts.append(self.statement())
        self._expect("RBRACE", "'}'")
        return ast.Block(stmts, line)

    # ---- expressions ----

    def expression(self):
        return self.or_expr()

    def or_expr(self):
        left = self.and_expr()
        while self._at("OR"):
            line = self._advance().line
            right = self.and_expr()
            left = ast.Logical("or", left, right, line)
        return left

    def and_expr(self):
        left = self.comparison()
        while self._at("AND"):
            line = self._advance().line
            right = self.comparison()
            left = ast.Logical("and", left, right, line)
        return left

    def comparison(self):
        left = self.concat()
        if self._at_comparison_op():
            tok = self._advance()
            right = self.concat()
            left = ast.Binary(OP_TEXT[tok.kind], left, right, tok.line)
            if self._at_comparison_op():
                bad = self._peek()
                raise ParseError(bad.line, "comparisons do not chain")
        return left

    def _at_comparison_op(self) -> bool:
        return self._peek().kind in COMPARISON_OPS

    def concat(self):
        left = self.additive()
        while self._at("TILDE"):
            tok = self._advance()
            right = self.additive()
            left = ast.Binary("~", left, right, tok.line)
        return left

    def additive(self):
        left = self.multiplicative()
        while self._peek().kind in ("PLUS", "MINUS"):
            tok = self._advance()
            right = self.multiplicative()
            left = ast.Binary(OP_TEXT[tok.kind], left, right, tok.line)
        return left

    def multiplicative(self):
        left = self.unary()
        while self._peek().kind in ("STAR", "SLASH", "PERCENT"):
            tok = self._advance()
            right = self.unary()
            left = ast.Binary(OP_TEXT[tok.kind], left, right, tok.line)
        return left

    def unary(self):
        if self._at("MINUS"):
            tok = self._advance()
            operand = self.unary()
            return ast.Unary("-", operand, tok.line)
        return self.call()

    def call(self):
        expr = self.primary()
        while self._at("LPAREN"):
            line = self._advance().line
            args = []
            if not self._at("RPAREN"):
                args.append(self.expression())
                while self._at("COMMA"):
                    self._advance()
                    args.append(self.expression())
            self._expect("RPAREN", "')'")
            expr = ast.Call(expr, args, line)
        return expr

    def primary(self):
        tok = self._peek()
        if tok.kind == "INT":
            self._advance()
            return ast.IntLit(tok.value, tok.line)
        if tok.kind == "STRING":
            self._advance()
            return ast.StringLit(tok.value, tok.line)
        if tok.kind == "TRUE":
            self._advance()
            return ast.BoolLit(True, tok.line)
        if tok.kind == "FALSE":
            self._advance()
            return ast.BoolLit(False, tok.line)
        if tok.kind == "NIL":
            self._advance()
            return ast.NilLit(tok.line)
        if tok.kind == "IDENT":
            self._advance()
            if self._at("EQ"):
                raise ParseError(
                    self._peek().line,
                    "bare assignment is not allowed; use 'set' to reassign",
                )
            return ast.Ident(tok.value, tok.line)
        if tok.kind == "LPAREN":
            self._advance()
            expr = self.expression()
            self._expect("RPAREN", "')'")
            return ast.Grouping(expr, tok.line)
        raise ParseError(tok.line, f"unexpected token '{tok.value}'")


def parse(tokens: list) -> list:
    return Parser(tokens).parse_program()

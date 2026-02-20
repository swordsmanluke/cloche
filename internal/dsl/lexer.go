package dsl

type Lexer struct {
	input []rune
	pos   int
	line  int
	col   int
}

func NewLexer(input string) *Lexer {
	return &Lexer{input: []rune(input), pos: 0, line: 1, col: 1}
}

func (l *Lexer) NextToken() Token {
	l.skipWhitespaceAndComments()

	if l.pos >= len(l.input) {
		return Token{Type: TokenEOF, Line: l.line, Col: l.col}
	}

	ch := l.input[l.pos]
	tok := Token{Line: l.line, Col: l.col}

	switch ch {
	case '{':
		tok.Type = TokenLBrace
		tok.Literal = "{"
		l.advance()
	case '}':
		tok.Type = TokenRBrace
		tok.Literal = "}"
		l.advance()
	case '[':
		tok.Type = TokenLBracket
		tok.Literal = "["
		l.advance()
	case ']':
		tok.Type = TokenRBracket
		tok.Literal = "]"
		l.advance()
	case '(':
		tok.Type = TokenLParen
		tok.Literal = "("
		l.advance()
	case ')':
		tok.Type = TokenRParen
		tok.Literal = ")"
		l.advance()
	case ',':
		tok.Type = TokenComma
		tok.Literal = ","
		l.advance()
	case '=':
		tok.Type = TokenEquals
		tok.Literal = "="
		l.advance()
	case ':':
		tok.Type = TokenColon
		tok.Literal = ":"
		l.advance()
	case '.':
		tok.Type = TokenDot
		tok.Literal = "."
		l.advance()
	case '-':
		if l.peek() == '>' {
			tok.Type = TokenArrow
			tok.Literal = "->"
			l.advance()
			l.advance()
		} else {
			tok.Type = TokenIllegal
			tok.Literal = string(ch)
			l.advance()
		}
	case '"':
		tok.Type = TokenString
		tok.Literal = l.readString()
	default:
		if isIdentStart(ch) {
			tok.Type = TokenIdent
			tok.Literal = l.readIdent()
		} else {
			tok.Type = TokenIllegal
			tok.Literal = string(ch)
			l.advance()
		}
	}

	return tok
}

func (l *Lexer) advance() {
	if l.pos < len(l.input) {
		if l.input[l.pos] == '\n' {
			l.line++
			l.col = 1
		} else {
			l.col++
		}
		l.pos++
	}
}

func (l *Lexer) peek() rune {
	if l.pos+1 < len(l.input) {
		return l.input[l.pos+1]
	}
	return 0
}

func (l *Lexer) skipWhitespaceAndComments() {
	for l.pos < len(l.input) {
		ch := l.input[l.pos]
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			l.advance()
		} else if ch == '/' && l.peek() == '/' {
			for l.pos < len(l.input) && l.input[l.pos] != '\n' {
				l.advance()
			}
		} else {
			break
		}
	}
}

func (l *Lexer) readString() string {
	l.advance() // skip opening quote
	start := l.pos
	for l.pos < len(l.input) && l.input[l.pos] != '"' {
		l.advance()
	}
	result := string(l.input[start:l.pos])
	if l.pos < len(l.input) {
		l.advance() // skip closing quote
	}
	return result
}

func (l *Lexer) readIdent() string {
	start := l.pos
	for l.pos < len(l.input) && isIdentPart(l.input[l.pos]) {
		l.advance()
	}
	return string(l.input[start:l.pos])
}

func isIdentStart(ch rune) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_'
}

func isIdentPart(ch rune) bool {
	return isIdentStart(ch) || (ch >= '0' && ch <= '9')
}

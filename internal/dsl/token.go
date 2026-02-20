package dsl

type TokenType int

const (
	TokenEOF     TokenType = iota
	TokenIllegal
	TokenIdent
	TokenString
	TokenLBrace
	TokenRBrace
	TokenLBracket
	TokenRBracket
	TokenLParen
	TokenRParen
	TokenComma
	TokenEquals
	TokenArrow
	TokenColon
	TokenDot
)

func (t TokenType) String() string {
	switch t {
	case TokenEOF:
		return "EOF"
	case TokenIllegal:
		return "ILLEGAL"
	case TokenIdent:
		return "IDENT"
	case TokenString:
		return "STRING"
	case TokenLBrace:
		return "LBRACE"
	case TokenRBrace:
		return "RBRACE"
	case TokenLBracket:
		return "LBRACKET"
	case TokenRBracket:
		return "RBRACKET"
	case TokenLParen:
		return "LPAREN"
	case TokenRParen:
		return "RPAREN"
	case TokenComma:
		return "COMMA"
	case TokenEquals:
		return "EQUALS"
	case TokenArrow:
		return "ARROW"
	case TokenColon:
		return "COLON"
	case TokenDot:
		return "DOT"
	default:
		return "UNKNOWN"
	}
}

type Token struct {
	Type    TokenType
	Literal string
	Line    int
	Col     int
}

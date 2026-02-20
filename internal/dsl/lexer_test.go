package dsl_test

import (
	"testing"

	"github.com/cloche-dev/cloche/internal/dsl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLexer_SimpleWorkflow(t *testing.T) {
	input := `workflow "test" {
  step code {
    run = "echo hello"
    results = [success, fail]
  }
  code:success -> done
}`

	lexer := dsl.NewLexer(input)
	tokens := lexAll(lexer)

	expected := []dsl.TokenType{
		dsl.TokenIdent,    // workflow
		dsl.TokenString,   // "test"
		dsl.TokenLBrace,   // {
		dsl.TokenIdent,    // step
		dsl.TokenIdent,    // code
		dsl.TokenLBrace,   // {
		dsl.TokenIdent,    // run
		dsl.TokenEquals,   // =
		dsl.TokenString,   // "echo hello"
		dsl.TokenIdent,    // results
		dsl.TokenEquals,   // =
		dsl.TokenLBracket, // [
		dsl.TokenIdent,    // success
		dsl.TokenComma,    // ,
		dsl.TokenIdent,    // fail
		dsl.TokenRBracket, // ]
		dsl.TokenRBrace,   // }
		dsl.TokenIdent,    // code
		dsl.TokenColon,    // :
		dsl.TokenIdent,    // success
		dsl.TokenArrow,    // ->
		dsl.TokenIdent,    // done
		dsl.TokenRBrace,   // }
		dsl.TokenEOF,
	}

	require.Len(t, tokens, len(expected))
	for i, tok := range tokens {
		assert.Equal(t, expected[i], tok.Type, "token %d: expected %v got %v (%q)", i, expected[i], tok.Type, tok.Literal)
	}
}

func TestLexer_StringLiteral(t *testing.T) {
	input := `"hello world"`
	lexer := dsl.NewLexer(input)
	tok := lexer.NextToken()
	assert.Equal(t, dsl.TokenString, tok.Type)
	assert.Equal(t, "hello world", tok.Literal)
}

func TestLexer_Comments(t *testing.T) {
	input := `// this is a comment
workflow`
	lexer := dsl.NewLexer(input)
	tok := lexer.NextToken()
	assert.Equal(t, dsl.TokenIdent, tok.Type)
	assert.Equal(t, "workflow", tok.Literal)
}

func TestLexer_Arrow(t *testing.T) {
	input := `a -> b`
	lexer := dsl.NewLexer(input)
	tokens := lexAll(lexer)
	assert.Equal(t, dsl.TokenIdent, tokens[0].Type)
	assert.Equal(t, dsl.TokenArrow, tokens[1].Type)
	assert.Equal(t, dsl.TokenIdent, tokens[2].Type)
}

func lexAll(l *dsl.Lexer) []dsl.Token {
	var tokens []dsl.Token
	for {
		tok := l.NextToken()
		tokens = append(tokens, tok)
		if tok.Type == dsl.TokenEOF || tok.Type == dsl.TokenIllegal {
			break
		}
	}
	return tokens
}

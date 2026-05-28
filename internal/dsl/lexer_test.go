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

func TestLexer_EscapedStrings(t *testing.T) {
	input := `"echo \"hello\" world"`
	lexer := dsl.NewLexer(input)
	tok := lexer.NextToken()
	assert.Equal(t, dsl.TokenString, tok.Type)
	assert.Equal(t, `echo "hello" world`, tok.Literal)
}

func TestLexer_HyphenatedIdent(t *testing.T) {
	input := `simple-build`
	lexer := dsl.NewLexer(input)
	tok := lexer.NextToken()
	assert.Equal(t, dsl.TokenIdent, tok.Type)
	assert.Equal(t, "simple-build", tok.Literal)
}

func TestLexer_IntLiteral(t *testing.T) {
	lexer := dsl.NewLexer("42")
	tok := lexer.NextToken()
	assert.Equal(t, dsl.TokenInt, tok.Type)
	assert.Equal(t, "42", tok.Literal)
}

func TestLexer_IntInBrackets(t *testing.T) {
	tests := []struct {
		input    string
		expected []dsl.Token
	}{
		{
			input: "[0]",
			expected: []dsl.Token{
				{Type: dsl.TokenLBracket, Literal: "["},
				{Type: dsl.TokenInt, Literal: "0"},
				{Type: dsl.TokenRBracket, Literal: "]"},
				{Type: dsl.TokenEOF},
			},
		},
		{
			input: "[42]",
			expected: []dsl.Token{
				{Type: dsl.TokenLBracket, Literal: "["},
				{Type: dsl.TokenInt, Literal: "42"},
				{Type: dsl.TokenRBracket, Literal: "]"},
				{Type: dsl.TokenEOF},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			tokens := lexAll(dsl.NewLexer(tt.input))
			require.Len(t, tokens, len(tt.expected))
			for i, exp := range tt.expected {
				assert.Equal(t, exp.Type, tokens[i].Type, "token %d type", i)
				if exp.Literal != "" {
					assert.Equal(t, exp.Literal, tokens[i].Literal, "token %d literal", i)
				}
			}
		})
	}
}

func TestLexer_IntMixedWithTokens(t *testing.T) {
	input := `output[0].id`
	tokens := lexAll(dsl.NewLexer(input))

	expected := []dsl.TokenType{
		dsl.TokenIdent,    // output
		dsl.TokenLBracket, // [
		dsl.TokenInt,      // 0
		dsl.TokenRBracket, // ]
		dsl.TokenDot,      // .
		dsl.TokenIdent,    // id
		dsl.TokenEOF,
	}

	require.Len(t, tokens, len(expected))
	for i, tok := range tokens {
		assert.Equal(t, expected[i], tok.Type, "token %d", i)
	}
	assert.Equal(t, "output", tokens[0].Literal)
	assert.Equal(t, "0", tokens[2].Literal)
	assert.Equal(t, "id", tokens[5].Literal)
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

package dsl

import (
	"fmt"
	"strings"

	"github.com/cloche-dev/cloche/internal/domain"
)

type Parser struct {
	lexer   *Lexer
	current Token
	peek    Token
}

func Parse(input string) (*domain.Workflow, error) {
	p := &Parser{lexer: NewLexer(input)}
	p.advance() // load current
	p.advance() // load peek
	return p.parseWorkflow()
}

func (p *Parser) advance() {
	p.current = p.peek
	p.peek = p.lexer.NextToken()
}

func (p *Parser) expect(typ TokenType) (Token, error) {
	if p.current.Type != typ {
		return p.current, fmt.Errorf("line %d col %d: expected %v, got %v (%q)",
			p.current.Line, p.current.Col, typ, p.current.Type, p.current.Literal)
	}
	tok := p.current
	p.advance()
	return tok, nil
}

func (p *Parser) expectIdent(value string) (Token, error) {
	if p.current.Type != TokenIdent || p.current.Literal != value {
		return p.current, fmt.Errorf("line %d col %d: expected %q, got %q",
			p.current.Line, p.current.Col, value, p.current.Literal)
	}
	tok := p.current
	p.advance()
	return tok, nil
}

func (p *Parser) parseWorkflow() (*domain.Workflow, error) {
	if _, err := p.expectIdent("workflow"); err != nil {
		return nil, err
	}

	nameTok, err := p.expect(TokenString)
	if err != nil {
		return nil, fmt.Errorf("expected workflow name string: %w", err)
	}

	if _, err := p.expect(TokenLBrace); err != nil {
		return nil, err
	}

	wf := &domain.Workflow{
		Name:   nameTok.Literal,
		Steps:  make(map[string]*domain.Step),
		Config: make(map[string]string),
	}

	for p.current.Type != TokenRBrace && p.current.Type != TokenEOF {
		if p.current.Type == TokenIdent && p.current.Literal == "step" {
			step, err := p.parseStep()
			if err != nil {
				return nil, err
			}
			wf.Steps[step.Name] = step
			if wf.EntryStep == "" {
				wf.EntryStep = step.Name
			}
		} else if p.current.Type == TokenIdent && p.current.Literal == "collect" {
			collect, err := p.parseCollect()
			if err != nil {
				return nil, err
			}
			wf.Collects = append(wf.Collects, collect)
		} else if p.current.Type == TokenIdent && p.peek.Type == TokenLBrace {
			if err := p.parseWorkflowConfig(wf); err != nil {
				return nil, err
			}
		} else if p.current.Type == TokenIdent && p.peek.Type == TokenColon {
			wire, err := p.parseWire()
			if err != nil {
				return nil, err
			}
			wf.Wiring = append(wf.Wiring, wire)
		} else {
			return nil, fmt.Errorf("line %d col %d: unexpected token %q", p.current.Line, p.current.Col, p.current.Literal)
		}
	}

	if _, err := p.expect(TokenRBrace); err != nil {
		return nil, err
	}

	return wf, nil
}

func (p *Parser) parseWorkflowConfig(wf *domain.Workflow) error {
	prefix := p.current.Literal // e.g. "container"
	p.advance()                 // consume prefix ident

	if _, err := p.expect(TokenLBrace); err != nil {
		return err
	}

	for p.current.Type != TokenRBrace && p.current.Type != TokenEOF {
		keyTok, err := p.expect(TokenIdent)
		if err != nil {
			return fmt.Errorf("expected field name: %w", err)
		}

		if _, err := p.expect(TokenEquals); err != nil {
			return err
		}

		val, err := p.parseValue()
		if err != nil {
			return err
		}

		wf.Config[prefix+"."+keyTok.Literal] = val
	}

	if _, err := p.expect(TokenRBrace); err != nil {
		return err
	}

	return nil
}

func (p *Parser) parseStep() (*domain.Step, error) {
	p.advance() // consume "step"

	nameTok, err := p.expect(TokenIdent)
	if err != nil {
		return nil, fmt.Errorf("expected step name: %w", err)
	}

	if _, err := p.expect(TokenLBrace); err != nil {
		return nil, err
	}

	step := &domain.Step{
		Name:   nameTok.Literal,
		Config: make(map[string]string),
	}

	for p.current.Type != TokenRBrace && p.current.Type != TokenEOF {
		if err := p.parseStepField(step, ""); err != nil {
			return nil, err
		}
	}

	if _, err := p.expect(TokenRBrace); err != nil {
		return nil, err
	}

	// Infer step type from content
	_, hasPrompt := step.Config["prompt"]
	_, hasRun := step.Config["run"]
	switch {
	case hasPrompt && hasRun:
		return nil, fmt.Errorf("step %q has both 'prompt' and 'run'; must have exactly one", step.Name)
	case hasPrompt:
		step.Type = domain.StepTypeAgent
	case hasRun:
		step.Type = domain.StepTypeScript
	default:
		return nil, fmt.Errorf("step %q has neither 'prompt' nor 'run'; must have exactly one", step.Name)
	}

	return step, nil
}

func (p *Parser) parseStepField(step *domain.Step, prefix string) error {
	keyTok, err := p.expect(TokenIdent)
	if err != nil {
		return fmt.Errorf("expected field name: %w", err)
	}

	key := keyTok.Literal
	if prefix != "" {
		key = prefix + "." + key
	}

	// Sub-block (e.g., container { ... })
	if p.current.Type == TokenLBrace {
		p.advance() // consume {
		for p.current.Type != TokenRBrace && p.current.Type != TokenEOF {
			if err := p.parseStepField(step, key); err != nil {
				return err
			}
		}
		p.advance() // consume }
		return nil
	}

	if _, err := p.expect(TokenEquals); err != nil {
		return err
	}

	if key == "results" {
		results, err := p.parseIdentList()
		if err != nil {
			return err
		}
		step.Results = results
	} else if p.current.Type == TokenLBracket {
		values, err := p.parseStringList()
		if err != nil {
			return err
		}
		step.Config[key] = strings.Join(values, ",")
	} else {
		val, err := p.parseValue()
		if err != nil {
			return err
		}
		step.Config[key] = val
	}

	return nil
}

func (p *Parser) parseIdentList() ([]string, error) {
	if _, err := p.expect(TokenLBracket); err != nil {
		return nil, err
	}

	var items []string
	for p.current.Type != TokenRBracket && p.current.Type != TokenEOF {
		tok, err := p.expect(TokenIdent)
		if err != nil {
			return nil, err
		}
		items = append(items, tok.Literal)
		if p.current.Type == TokenComma {
			p.advance()
		}
	}

	if _, err := p.expect(TokenRBracket); err != nil {
		return nil, err
	}

	return items, nil
}

func (p *Parser) parseStringList() ([]string, error) {
	if _, err := p.expect(TokenLBracket); err != nil {
		return nil, err
	}

	var items []string
	for p.current.Type != TokenRBracket && p.current.Type != TokenEOF {
		tok, err := p.expect(TokenString)
		if err != nil {
			return nil, err
		}
		items = append(items, tok.Literal)
		if p.current.Type == TokenComma {
			p.advance()
		}
	}

	if _, err := p.expect(TokenRBracket); err != nil {
		return nil, err
	}

	return items, nil
}

func (p *Parser) parseValue() (string, error) {
	if p.current.Type == TokenString {
		tok := p.current
		p.advance()
		return tok.Literal, nil
	}

	if p.current.Type == TokenIdent {
		var buf strings.Builder
		buf.WriteString(p.current.Literal)
		p.advance()

		// Function call: file("...")
		if p.current.Type == TokenLParen {
			buf.WriteRune('(')
			p.advance()
			for p.current.Type != TokenRParen && p.current.Type != TokenEOF {
				if p.current.Type == TokenString {
					buf.WriteRune('"')
					buf.WriteString(p.current.Literal)
					buf.WriteRune('"')
				} else {
					buf.WriteString(p.current.Literal)
				}
				p.advance()
			}
			buf.WriteRune(')')
			p.advance() // consume )
		}

		// Dotted path: step.code.output
		for p.current.Type == TokenDot {
			buf.WriteRune('.')
			p.advance()
			if p.current.Type == TokenIdent {
				buf.WriteString(p.current.Literal)
				p.advance()
			}
		}

		return buf.String(), nil
	}

	return "", fmt.Errorf("line %d col %d: expected value, got %q", p.current.Line, p.current.Col, p.current.Literal)
}

func (p *Parser) parseWire() (domain.Wire, error) {
	fromTok := p.current
	p.advance()

	if _, err := p.expect(TokenColon); err != nil {
		return domain.Wire{}, err
	}

	resultTok, err := p.expect(TokenIdent)
	if err != nil {
		return domain.Wire{}, err
	}

	if _, err := p.expect(TokenArrow); err != nil {
		return domain.Wire{}, err
	}

	toTok, err := p.expect(TokenIdent)
	if err != nil {
		return domain.Wire{}, err
	}

	return domain.Wire{
		From:   fromTok.Literal,
		Result: resultTok.Literal,
		To:     toTok.Literal,
	}, nil
}

func (p *Parser) parseCollect() (domain.Collect, error) {
	p.advance() // consume "collect"

	modeTok, err := p.expect(TokenIdent)
	if err != nil {
		return domain.Collect{}, fmt.Errorf("expected 'all' or 'any': %w", err)
	}

	var mode domain.CollectMode
	switch modeTok.Literal {
	case "all":
		mode = domain.CollectAll
	case "any":
		mode = domain.CollectAny
	default:
		return domain.Collect{}, fmt.Errorf("line %d col %d: expected 'all' or 'any', got %q",
			modeTok.Line, modeTok.Col, modeTok.Literal)
	}

	if _, err := p.expect(TokenLParen); err != nil {
		return domain.Collect{}, err
	}

	var conditions []domain.WireCondition
	for p.current.Type != TokenRParen && p.current.Type != TokenEOF {
		stepTok, err := p.expect(TokenIdent)
		if err != nil {
			return domain.Collect{}, fmt.Errorf("expected step name in collect: %w", err)
		}
		if _, err := p.expect(TokenColon); err != nil {
			return domain.Collect{}, err
		}
		resultTok, err := p.expect(TokenIdent)
		if err != nil {
			return domain.Collect{}, fmt.Errorf("expected result name in collect: %w", err)
		}
		conditions = append(conditions, domain.WireCondition{Step: stepTok.Literal, Result: resultTok.Literal})
		if p.current.Type == TokenComma {
			p.advance()
		}
	}

	if _, err := p.expect(TokenRParen); err != nil {
		return domain.Collect{}, err
	}
	if _, err := p.expect(TokenArrow); err != nil {
		return domain.Collect{}, err
	}

	toTok, err := p.expect(TokenIdent)
	if err != nil {
		return domain.Collect{}, fmt.Errorf("expected target step: %w", err)
	}

	return domain.Collect{
		Mode:       mode,
		Conditions: conditions,
		To:         toTok.Literal,
	}, nil
}

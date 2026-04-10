package dsl

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/cloche-dev/cloche/internal/domain"
)

type Parser struct {
	lexer    *Lexer
	current  Token
	peek     Token
	location domain.WorkflowLocation
}

// ParseOption configures the parser.
type ParseOption func(*Parser)

// WithLocation sets the workflow location, enabling location-based validation.
func WithLocation(loc domain.WorkflowLocation) ParseOption {
	return func(p *Parser) {
		p.location = loc
	}
}

func Parse(input string, opts ...ParseOption) (*domain.Workflow, error) {
	p := &Parser{lexer: NewLexer(input)}
	for _, opt := range opts {
		opt(p)
	}
	p.advance() // load current
	p.advance() // load peek
	return p.parseWorkflow()
}

// ParseForHost parses a host.cloche file containing a single workflow.
func ParseForHost(input string) (*domain.Workflow, error) {
	return Parse(input, WithLocation(domain.LocationHost))
}

// ParseAllForHost parses a host.cloche file that may contain multiple workflows.
// Returns a map of workflow name to workflow definition.
func ParseAllForHost(input string) (map[string]*domain.Workflow, error) {
	p := &Parser{lexer: NewLexer(input), location: domain.LocationHost}
	p.advance() // load current
	p.advance() // load peek

	workflows := make(map[string]*domain.Workflow)
	for p.current.Type != TokenEOF {
		wf, err := p.parseWorkflow()
		if err != nil {
			return nil, err
		}
		if _, exists := workflows[wf.Name]; exists {
			return nil, fmt.Errorf("duplicate workflow name %q", wf.Name)
		}
		workflows[wf.Name] = wf
	}

	if len(workflows) == 0 {
		return nil, fmt.Errorf("no workflows found in host.cloche")
	}

	return workflows, nil
}

// ParseForContainer parses a container workflow file.
func ParseForContainer(input string) (*domain.Workflow, error) {
	return Parse(input, WithLocation(domain.LocationContainer))
}

// ParseAll parses a .cloche file that may contain multiple workflows.
// Workflows default to LocationContainer but a "host { }" block overrides
// the location to LocationHost, so any .cloche file can define host workflows.
func ParseAll(input string) (map[string]*domain.Workflow, error) {
	p := &Parser{lexer: NewLexer(input), location: domain.LocationContainer}
	p.advance() // load current
	p.advance() // load peek

	workflows := make(map[string]*domain.Workflow)
	for p.current.Type != TokenEOF {
		wf, err := p.parseWorkflow()
		if err != nil {
			return nil, err
		}
		if _, exists := workflows[wf.Name]; exists {
			return nil, fmt.Errorf("duplicate workflow name %q", wf.Name)
		}
		workflows[wf.Name] = wf
	}

	if len(workflows) == 0 {
		return nil, fmt.Errorf("no workflows found")
	}

	return workflows, nil
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
		Name:     nameTok.Literal,
		Location: p.location,
		Steps:    make(map[string]*domain.Step),
		Agents:   make(map[string]*domain.Agent),
		Config:   make(map[string]string),
	}

	for p.current.Type != TokenRBrace && p.current.Type != TokenEOF {
		if p.current.Type == TokenIdent && p.current.Literal == "agent" && p.peek.Type == TokenIdent {
			agent, err := p.parseAgent()
			if err != nil {
				return nil, err
			}
			if _, exists := wf.Agents[agent.Name]; exists {
				return nil, fmt.Errorf("line %d: duplicate agent declaration %q", p.current.Line, agent.Name)
			}
			wf.Agents[agent.Name] = agent
		} else if p.current.Type == TokenIdent && p.current.Literal == "step" {
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

	if wf.Location != "" {
		if err := wf.ValidateLocation(); err != nil {
			return nil, err
		}
	}

	// Post-parse fixup: ensure every human step has a "timeout" result and wire.
	// If no timeout wire is declared, add an implicit wire to abort.
	for name, step := range wf.Steps {
		if step.Type != domain.StepTypeHuman {
			continue
		}
		// Ensure "timeout" is declared in Results.
		hasTimeoutResult := false
		for _, r := range step.Results {
			if r == "timeout" {
				hasTimeoutResult = true
				break
			}
		}
		if !hasTimeoutResult {
			step.Results = append(step.Results, "timeout")
		}
		// Add implicit abort wire if no timeout wire is declared.
		hasTimeoutWire := false
		for _, w := range wf.Wiring {
			if w.From == name && w.Result == "timeout" {
				hasTimeoutWire = true
				break
			}
		}
		if !hasTimeoutWire {
			wf.Wiring = append(wf.Wiring, domain.Wire{
				From:   name,
				Result: "timeout",
				To:     domain.StepAbort,
			})
		}
	}

	return wf, nil
}

func (p *Parser) parseWorkflowConfig(wf *domain.Workflow) error {
	prefix := p.current.Literal // e.g. "host", "container"
	line, col := p.current.Line, p.current.Col
	p.advance() // consume prefix ident

	// Reject conflicting location blocks.
	if prefix == "host" && wf.Config["_location_block"] == "container" {
		return fmt.Errorf("line %d col %d: workflow %q has both \"host\" and \"container\" blocks", line, col, wf.Name)
	}
	if prefix == "container" && wf.Config["_location_block"] == "host" {
		return fmt.Errorf("line %d col %d: workflow %q has both \"host\" and \"container\" blocks", line, col, wf.Name)
	}

	// A "host { ... }" block marks this workflow as a host workflow.
	if prefix == "host" {
		wf.Location = domain.LocationHost
	}
	if prefix == "host" || prefix == "container" {
		wf.Config["_location_block"] = prefix
	}

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

func (p *Parser) parseAgent() (*domain.Agent, error) {
	p.advance() // consume "agent"

	nameTok, err := p.expect(TokenIdent)
	if err != nil {
		return nil, fmt.Errorf("expected agent name: %w", err)
	}

	if _, err := p.expect(TokenLBrace); err != nil {
		return nil, err
	}

	agent := &domain.Agent{Name: nameTok.Literal}

	for p.current.Type != TokenRBrace && p.current.Type != TokenEOF {
		keyTok, err := p.expect(TokenIdent)
		if err != nil {
			return nil, fmt.Errorf("expected field name in agent block: %w", err)
		}

		if _, err := p.expect(TokenEquals); err != nil {
			return nil, err
		}

		val, err := p.parseValue()
		if err != nil {
			return nil, err
		}

		switch keyTok.Literal {
		case "command":
			agent.Command = val
		case "args":
			agent.Args = val
		default:
			return nil, fmt.Errorf("line %d col %d: unknown agent field %q (expected \"command\" or \"args\")",
				keyTok.Line, keyTok.Col, keyTok.Literal)
		}
	}

	if _, err := p.expect(TokenRBrace); err != nil {
		return nil, err
	}

	if agent.Command == "" {
		return nil, fmt.Errorf("agent %q: \"command\" is required", agent.Name)
	}

	return agent, nil
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

	// Human step: explicit type = human with a script field.
	if step.Config["type"] == "human" {
		if _, hasScript := step.Config["script"]; !hasScript {
			return nil, fmt.Errorf("step %q: human step requires a 'script' field", step.Name)
		}
		if _, hasInterval := step.Config["interval"]; !hasInterval {
			return nil, fmt.Errorf("step %q: human step requires an 'interval' field", step.Name)
		}
		step.Type = domain.StepTypeHuman
		return step, nil
	}

	// Infer step type from content
	_, hasPrompt := step.Config["prompt"]
	_, hasRun := step.Config["run"]
	_, hasWorkflowName := step.Config["workflow_name"]
	_, hasScript := step.Config["script"]
	isHumanType := step.Config["type"] == "human"

	if isHumanType {
		// Human step: requires script and interval.
		if !hasScript {
			return nil, fmt.Errorf("step %q: human step requires a 'script' field", step.Name)
		}
		if step.Config["interval"] == "" {
			return nil, fmt.Errorf("step %q: human step requires an 'interval' field", step.Name)
		}
		if hasPrompt || hasRun || hasWorkflowName {
			return nil, fmt.Errorf("step %q: human step must not have 'prompt', 'run', or 'workflow_name'", step.Name)
		}
		step.Type = domain.StepTypeHuman
	} else {
		count := 0
		if hasPrompt {
			count++
		}
		if hasRun {
			count++
		}
		if hasWorkflowName {
			count++
		}

		if count > 1 {
			return nil, fmt.Errorf("step %q has multiple of 'prompt', 'run', 'workflow_name'; must have exactly one", step.Name)
		}

		switch {
		case hasPrompt:
			step.Type = domain.StepTypeAgent
		case hasRun:
			step.Type = domain.StepTypeScript
		case hasWorkflowName:
			step.Type = domain.StepTypeWorkflow
		default:
			return nil, fmt.Errorf("step %q has none of 'prompt', 'run', or 'workflow_name'; must have exactly one", step.Name)
		}
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
		if key == "max_attempts" && p.current.Type != TokenInt {
			return fmt.Errorf("max_attempts must be a numeric value, not a string (line %d, col %d)", p.current.Line, p.current.Col)
		}
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

	if p.current.Type == TokenInt {
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

	var mappings []domain.OutputMapping
	if p.current.Type == TokenLBracket {
		mappings, err = p.parseOutputMappings()
		if err != nil {
			return domain.Wire{}, err
		}
	}

	return domain.Wire{
		From:      fromTok.Literal,
		Result:    resultTok.Literal,
		To:        toTok.Literal,
		OutputMap: mappings,
	}, nil
}

func (p *Parser) parseOutputMappings() ([]domain.OutputMapping, error) {
	if _, err := p.expect(TokenLBracket); err != nil {
		return nil, err
	}

	var mappings []domain.OutputMapping
	for p.current.Type != TokenRBracket && p.current.Type != TokenEOF {
		keyTok, err := p.expect(TokenIdent)
		if err != nil {
			return nil, fmt.Errorf("expected mapping key: %w", err)
		}

		if _, err := p.expect(TokenEquals); err != nil {
			return nil, err
		}

		path, err := p.parseOutputPath()
		if err != nil {
			return nil, err
		}

		mappings = append(mappings, domain.OutputMapping{
			EnvVar: keyTok.Literal,
			Path:   path,
		})

		if p.current.Type == TokenComma {
			p.advance()
		}
	}

	if _, err := p.expect(TokenRBracket); err != nil {
		return nil, err
	}

	return mappings, nil
}

func (p *Parser) parseOutputPath() (domain.OutputPath, error) {
	if p.current.Type != TokenIdent || p.current.Literal != "output" {
		return domain.OutputPath{}, fmt.Errorf("line %d col %d: expected \"output\", got %q",
			p.current.Line, p.current.Col, p.current.Literal)
	}
	p.advance() // consume "output"

	var segments []domain.PathSegment
	for {
		if p.current.Type == TokenDot {
			p.advance() // consume "."
			fieldTok, err := p.expect(TokenIdent)
			if err != nil {
				return domain.OutputPath{}, fmt.Errorf("expected field name after '.': %w", err)
			}
			segments = append(segments, domain.PathSegment{Kind: domain.SegmentField, Field: fieldTok.Literal})
		} else if p.current.Type == TokenLBracket {
			p.advance() // consume "["
			idxTok, err := p.expect(TokenInt)
			if err != nil {
				return domain.OutputPath{}, fmt.Errorf("expected integer index: %w", err)
			}
			idx, _ := strconv.Atoi(idxTok.Literal)
			if _, err := p.expect(TokenRBracket); err != nil {
				return domain.OutputPath{}, err
			}
			segments = append(segments, domain.PathSegment{Kind: domain.SegmentIndex, Index: idx})
		} else {
			break
		}
	}

	return domain.OutputPath{Segments: segments}, nil
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

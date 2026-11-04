package dsl

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/swordsmanluke/cloche/internal/domain"
)

type Parser struct {
	lexer      *Lexer
	current    Token
	peek       Token
	location   domain.WorkflowLocation
	source     string
	allowEmpty bool
}

// ParseOption configures the parser.
type ParseOption func(*Parser)

// WithLocation sets the workflow location, enabling location-based validation.
func WithLocation(loc domain.WorkflowLocation) ParseOption {
	return func(p *Parser) {
		p.location = loc
	}
}

// WithAllowEmpty lets ParseFile accept a file with no workflows, such as a
// project.cloche that holds only root-scope binds.
func WithAllowEmpty() ParseOption {
	return func(p *Parser) {
		p.allowEmpty = true
	}
}

// WithSourceFile records the path of the file being parsed on each workflow.
func WithSourceFile(path string) ParseOption {
	return func(p *Parser) {
		p.source = path
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

// AgentCommandForStep parses a container workflow file and returns the
// resolved agent command for stepName (from an explicit agent_command, or
// via an `agent = <alias>` reference into an `agents { }` block). Returns ""
// if the file can't be parsed, the step doesn't exist, or no agent command
// is configured for it.
func AgentCommandForStep(data []byte, stepName string) string {
	return AgentCommandsForWorkflow(data)[stepName]
}

// AgentCommandsForWorkflow parses a container workflow file and returns the
// resolved agent command for every step (from an explicit agent_command, or
// via an `agent = <alias>` reference into an `agents { }` block). Steps with
// no agent command configured are omitted. Returns an empty map if the file
// can't be parsed.
func AgentCommandsForWorkflow(data []byte) map[string]string {
	commands := map[string]string{}
	wf, err := ParseForContainer(string(data))
	if err != nil {
		return commands
	}
	wf.ResolveAgents()
	for name, step := range wf.Steps {
		if cmd := step.Config["agent_command"]; cmd != "" {
			commands[name] = cmd
		}
	}
	return commands
}

// ParseAll parses a .cloche file that may contain multiple workflows.
// Workflows default to LocationContainer but a "host { }" block overrides
// the location to LocationHost, so any .cloche file can define host workflows.
func ParseAll(input string, opts ...ParseOption) (map[string]*domain.Workflow, error) {
	f, err := ParseFile(input, opts...)
	if err != nil {
		return nil, err
	}
	return f.Workflows, nil
}

// File is a parsed .cloche file: its workflows plus any file-level binds
// (top-level `bind` statements, which scope over every workflow in the file).
type File struct {
	Workflows map[string]*domain.Workflow
	Binds     []domain.Bind
}

// ParseFile parses a .cloche file, returning its workflows and file-level
// binds. Like ParseAll, workflows default to LocationContainer.
func ParseFile(input string, opts ...ParseOption) (*File, error) {
	p := &Parser{lexer: NewLexer(input), location: domain.LocationContainer}
	for _, opt := range opts {
		opt(p)
	}
	p.advance() // load current
	p.advance() // load peek

	f := &File{Workflows: make(map[string]*domain.Workflow)}
	for p.current.Type != TokenEOF {
		if p.atBind() {
			binds, err := p.parseBind()
			if err != nil {
				return nil, err
			}
			f.Binds = append(f.Binds, binds...)
			continue
		}
		wf, err := p.parseWorkflow()
		if err != nil {
			return nil, err
		}
		if _, exists := f.Workflows[wf.Name]; exists {
			return nil, fmt.Errorf("duplicate workflow name %q", wf.Name)
		}
		f.Workflows[wf.Name] = wf
	}

	if len(f.Workflows) == 0 && !p.allowEmpty {
		return nil, fmt.Errorf("no workflows found")
	}

	return f, nil
}

// atBind reports whether the parser is positioned at a bind statement:
// `bind slot = target`, `bind { ... }`, or either with `bind!`.
func (p *Parser) atBind() bool {
	if p.current.Type != TokenIdent || p.current.Literal != "bind" {
		return false
	}
	return p.peek.Type == TokenLBrace || p.peek.Type == TokenIdent ||
		(p.peek.Type == TokenIllegal && p.peek.Literal == "!")
}

// parseBind parses one bind statement and returns the binds it declares.
func (p *Parser) parseBind() ([]domain.Bind, error) {
	p.advance() // consume "bind"
	force := false
	if p.current.Type == TokenIllegal && p.current.Literal == "!" {
		force = true
		p.advance()
	}

	if p.current.Type != TokenLBrace {
		b, err := p.parseBindEntry(force)
		if err != nil {
			return nil, err
		}
		return []domain.Bind{b}, nil
	}

	p.advance() // consume {
	var binds []domain.Bind
	for p.current.Type != TokenRBrace && p.current.Type != TokenEOF {
		b, err := p.parseBindEntry(force)
		if err != nil {
			return nil, err
		}
		binds = append(binds, b)
	}
	if _, err := p.expect(TokenRBrace); err != nil {
		return nil, err
	}
	return binds, nil
}

// parseBindEntry parses `slot = target`, where target is `name` or `module:name`.
func (p *Parser) parseBindEntry(force bool) (domain.Bind, error) {
	slot, err := p.expect(TokenIdent)
	if err != nil {
		return domain.Bind{}, fmt.Errorf("expected slot name in bind: %w", err)
	}
	if _, err := p.expect(TokenEquals); err != nil {
		return domain.Bind{}, err
	}
	target, err := p.expect(TokenIdent)
	if err != nil {
		return domain.Bind{}, fmt.Errorf("expected bind target for slot %q: %w", slot.Literal, err)
	}
	t := target.Literal
	if p.current.Type == TokenColon {
		p.advance()
		wfTok, err := p.expect(TokenIdent)
		if err != nil {
			return domain.Bind{}, fmt.Errorf("expected workflow name after %q: in bind: %w", t+":", err)
		}
		t += ":" + wfTok.Literal
	}
	return domain.Bind{Slot: slot.Literal, Target: t, Force: force}, nil
}

// parseContract parses `contract { kv: a  env: B  out: c }`.
func (p *Parser) parseContract(wf *domain.Workflow) error {
	line, col := p.current.Line, p.current.Col
	if wf.Contract != nil {
		return fmt.Errorf("line %d col %d: workflow %q has more than one contract block", line, col, wf.Name)
	}
	p.advance() // consume "contract"
	if _, err := p.expect(TokenLBrace); err != nil {
		return err
	}
	c := &domain.Contract{}
	for p.current.Type != TokenRBrace && p.current.Type != TokenEOF {
		kind, err := p.expect(TokenIdent)
		if err != nil {
			return fmt.Errorf("expected contract field (kv, env or out): %w", err)
		}
		if _, err := p.expect(TokenColon); err != nil {
			return err
		}
		name, err := p.expect(TokenIdent)
		if err != nil {
			return fmt.Errorf("expected name after %s: in contract: %w", kind.Literal, err)
		}
		switch kind.Literal {
		case "kv":
			c.KV = append(c.KV, name.Literal)
		case "env":
			c.Env = append(c.Env, name.Literal)
		case "out":
			c.Out = append(c.Out, name.Literal)
		default:
			return fmt.Errorf("line %d col %d: unknown contract field %q (expected kv, env or out)",
				kind.Line, kind.Col, kind.Literal)
		}
	}
	if _, err := p.expect(TokenRBrace); err != nil {
		return err
	}
	wf.Contract = c
	return nil
}

// parseWith parses a step's `with { }` block.
func (p *Parser) parseWith(step *domain.Step) error {
	p.advance() // consume "with"
	if _, err := p.expect(TokenLBrace); err != nil {
		return err
	}
	for p.current.Type != TokenRBrace && p.current.Type != TokenEOF {
		e, err := p.parseWithEntry()
		if err != nil {
			return err
		}
		step.With = append(step.With, e)
	}
	_, err := p.expect(TokenRBrace)
	return err
}

func (p *Parser) parseWithEntry() (domain.WithEntry, error) {
	keyTok, err := p.expect(TokenIdent)
	if err != nil {
		return domain.WithEntry{}, fmt.Errorf("expected key in with block: %w", err)
	}
	e := domain.WithEntry{Key: keyTok.Literal}
	if e.Key == "env" && p.current.Type == TokenDot {
		p.advance()
		name, err := p.expect(TokenIdent)
		if err != nil {
			return domain.WithEntry{}, fmt.Errorf("expected env var name after \"env.\": %w", err)
		}
		e.Key, e.Env = name.Literal, true
	}

	switch {
	case p.current.Type == TokenEquals:
		p.advance()
		if p.current.Type != TokenString {
			return domain.WithEntry{}, fmt.Errorf("line %d col %d: with %q: expected a quoted literal, got %q",
				p.current.Line, p.current.Col, e.Key, p.current.Literal)
		}
		e.Source, e.Value = domain.WithLiteral, p.current.Literal
		p.advance()
	case p.current.Type == TokenIdent && p.current.Literal == "from":
		p.advance()
		src, err := p.expect(TokenIdent)
		if err != nil {
			return domain.WithEntry{}, fmt.Errorf("expected source after \"from\" for %q: %w", e.Key, err)
		}
		e.Source, e.Value = domain.WithKV, src.Literal
		if src.Literal == "out" && p.current.Type == TokenDot {
			if e.Env {
				return domain.WithEntry{}, fmt.Errorf("line %d col %d: with env.%s: outputs cannot be remapped into env vars",
					src.Line, src.Col, e.Key)
			}
			p.advance()
			outKey, err := p.expect(TokenIdent)
			if err != nil {
				return domain.WithEntry{}, fmt.Errorf("expected output key after \"out.\": %w", err)
			}
			e.Source, e.Value = domain.WithOut, outKey.Literal
		}
	default:
		return domain.WithEntry{}, fmt.Errorf("line %d col %d: with %q: expected \"from\" or \"=\", got %q",
			p.current.Line, p.current.Col, e.Key, p.current.Literal)
	}
	return e, nil
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

	nameTok, err := p.expect(TokenIdent)
	if err != nil {
		return nil, fmt.Errorf("expected workflow name: %w", err)
	}

	if _, err := p.expect(TokenLBrace); err != nil {
		return nil, err
	}

	wf := &domain.Workflow{
		Name:       nameTok.Literal,
		Location:   p.location,
		SourceFile: p.source,
		Steps:      make(map[string]*domain.Step),
		Agents:     make(map[string]*domain.Agent),
		Config:     make(map[string]string),
	}

	for p.current.Type != TokenRBrace && p.current.Type != TokenEOF {
		if p.current.Type == TokenIdent && p.current.Literal == "contract" && p.peek.Type == TokenLBrace {
			if err := p.parseContract(wf); err != nil {
				return nil, err
			}
		} else if p.atBind() {
			binds, err := p.parseBind()
			if err != nil {
				return nil, err
			}
			wf.Binds = append(wf.Binds, binds...)
		} else if p.current.Type == TokenIdent && p.current.Literal == "agent" && p.peek.Type == TokenIdent {
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
		} else if p.current.Type == TokenIdent && p.peek.Type == TokenEquals {
			if err := p.parseWorkflowField(wf); err != nil {
				return nil, err
			}
		} else if p.current.Type == TokenIdent && p.peek.Type == TokenColon {
			wire, err := p.parseWire()
			if err != nil {
				return nil, err
			}
			wf.Wiring = append(wf.Wiring, wire)
		} else if p.current.Type == TokenIdent && p.peek.Type == TokenArrow {
			result, target, err := p.parseGlobalWire()
			if err != nil {
				return nil, err
			}
			wf.Config["_wire."+result] = target
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

	// Post-parse fixup: ensure every step has a "timeout" result and wire.
	// If no timeout wire is declared, add an implicit wire to abort.
	for name, step := range wf.Steps {
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
				From:     name,
				Result:   "timeout",
				To:       domain.StepAbort,
				Implicit: true,
			})
		}
	}

	// Post-parse fixup: ensure every step has a "token-limit" result and wire.
	// A global wire directive "token-limit -> <step>" overrides the default abort target.
	tokenLimitTarget := domain.StepAbort
	if override, ok := wf.Config["_wire.token-limit"]; ok {
		tokenLimitTarget = override
	}
	for name, step := range wf.Steps {
		hasResult := false
		for _, r := range step.Results {
			if r == "token-limit" {
				hasResult = true
				break
			}
		}
		if !hasResult {
			step.Results = append(step.Results, "token-limit")
		}
		hasWire := false
		for _, w := range wf.Wiring {
			if w.From == name && w.Result == "token-limit" {
				hasWire = true
				break
			}
		}
		if !hasWire {
			wf.Wiring = append(wf.Wiring, domain.Wire{
				From:     name,
				Result:   "token-limit",
				To:       tokenLimitTarget,
				Implicit: true,
			})
		}
	}

	return wf, nil
}

// parseWorkflowField handles top-level `key = value` assignments inside a
// workflow block: `repos = [...]` is accepted and ignored apart from a
// deprecation warning, `token-limit = N` overrides the per-step
// token-limit result's default budget, and `intent_tracking = false` opts
// every step in the workflow out of $intent injection and collect-sources
// mining.
func (p *Parser) parseWorkflowField(wf *domain.Workflow) error {
	keyTok, err := p.expect(TokenIdent)
	if err != nil {
		return fmt.Errorf("expected field name: %w", err)
	}
	if _, err := p.expect(TokenEquals); err != nil {
		return err
	}

	switch keyTok.Literal {
	case "repos":
		if _, err := p.parseStringList(); err != nil {
			return err
		}
		wf.DeclaresRepos = true
		return nil
	case "token-limit":
		negative := false
		if p.current.Type == TokenIllegal && p.current.Literal == "-" {
			negative = true
			p.advance()
		}
		if p.current.Type != TokenInt {
			return fmt.Errorf("line %d col %d: token-limit must be an integer, got %q",
				p.current.Line, p.current.Col, p.current.Literal)
		}
		numStr := p.current.Literal
		p.advance()
		if negative {
			numStr = "-" + numStr
		}
		n, err := strconv.ParseInt(numStr, 10, 64)
		if err != nil || n < -1 {
			return fmt.Errorf("line %d col %d: token-limit must be -1 or greater, got %q",
				keyTok.Line, keyTok.Col, numStr)
		}
		wf.Config["token-limit"] = numStr
		return nil
	case "intent_tracking":
		val, err := p.parseValue()
		if err != nil {
			return err
		}
		if val != "true" && val != "false" {
			return fmt.Errorf("line %d col %d: intent_tracking must be true or false, got %q", keyTok.Line, keyTok.Col, val)
		}
		wf.Config["intent_tracking"] = val
		return nil
	default:
		return fmt.Errorf("line %d col %d: unknown workflow field %q", keyTok.Line, keyTok.Col, keyTok.Literal)
	}
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
		if p.current.Type == TokenIdent && p.current.Literal == "with" && p.peek.Type == TokenLBrace {
			if err := p.parseWith(step); err != nil {
				return nil, err
			}
		} else if p.atBind() {
			binds, err := p.parseBind()
			if err != nil {
				return nil, err
			}
			step.Binds = append(step.Binds, binds...)
		} else if err := p.parseStepField(step, ""); err != nil {
			return nil, err
		}
	}

	if _, err := p.expect(TokenRBrace); err != nil {
		return nil, err
	}

	// Infer step type from content
	_, hasPrompt := step.Config["prompt"]
	_, hasRun := step.Config["run"]
	_, hasWorkflowName := step.Config["workflow_name"]
	_, hasPoll := step.Config["poll"]

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
	if hasPoll {
		count++
	}

	if count > 1 {
		return nil, fmt.Errorf("step %q has multiple of 'prompt', 'run', 'workflow_name', 'poll'; must have exactly one", step.Name)
	}

	switch {
	case hasPrompt:
		step.Type = domain.StepTypeAgent
	case hasRun:
		step.Type = domain.StepTypeScript
	case hasWorkflowName:
		step.Type = domain.StepTypeWorkflow
	case hasPoll:
		// Poll step: requires interval.
		intervalStr := step.Config["interval"]
		if intervalStr == "" {
			return nil, fmt.Errorf("step %q: poll step requires an 'interval' field", step.Name)
		}
		if _, err := time.ParseDuration(intervalStr); err != nil {
			return nil, fmt.Errorf("step %q: invalid interval %q: %v", step.Name, intervalStr, err)
		}
		step.Type = domain.StepTypePoll
	default:
		return nil, fmt.Errorf("step %q has none of 'prompt', 'run', 'workflow_name', or 'poll'; must have exactly one", step.Name)
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
	} else if key == "token-limit" {
		negative := false
		if p.current.Type == TokenIllegal && p.current.Literal == "-" {
			negative = true
			p.advance()
		}
		if p.current.Type != TokenInt {
			return fmt.Errorf("line %d col %d: token-limit must be an integer, got %q",
				p.current.Line, p.current.Col, p.current.Literal)
		}
		numStr := p.current.Literal
		p.advance()
		if negative {
			numStr = "-" + numStr
		}
		n, err := strconv.ParseInt(numStr, 10, 64)
		if err != nil || n < -1 {
			return fmt.Errorf("token-limit must be -1 or greater, got %q", numStr)
		}
		step.Config[key] = numStr
	} else {
		if key == "max_attempts" && p.current.Type != TokenInt {
			return fmt.Errorf("max_attempts must be a numeric value, not a string (line %d, col %d)", p.current.Line, p.current.Col)
		}
		val, err := p.parseValue()
		if err != nil {
			return err
		}
		if key == "intent_tracking" && val != "true" && val != "false" {
			return fmt.Errorf("line %d col %d: intent_tracking must be true or false, got %q", keyTok.Line, keyTok.Col, val)
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

	return domain.Wire{
		From:   fromTok.Literal,
		Result: resultTok.Literal,
		To:     toTok.Literal,
	}, nil
}

// parseGlobalWire parses a "result -> target" directive at workflow level.
// This declares a default wire target for the named result across all steps.
func (p *Parser) parseGlobalWire() (result, target string, err error) {
	resultTok := p.current
	p.advance() // consume result ident

	if _, err := p.expect(TokenArrow); err != nil {
		return "", "", err
	}

	toTok, err := p.expect(TokenIdent)
	if err != nil {
		return "", "", fmt.Errorf("expected target step: %w", err)
	}

	return resultTok.Literal, toTok.Literal, nil
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

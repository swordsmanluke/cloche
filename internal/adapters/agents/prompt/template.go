package prompt

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloche-dev/cloche/internal/ports"
)

// KVReader aliases the canonical port type so existing package-local references
// and external code that used prompt.KVReader continue to compile unchanged.
type KVReader = ports.KVReader

// Resolver evaluates {{ }} template directives before the agent is invoked.
//
// Supported forms:
//
//	{{ $name }}   — variable lookup: built-in first, then KV store
//	{{! cmd }}    — sh -c cmd; substitute stdout (30 s timeout)
//	{{@ path }}   — read file at path; substitute contents verbatim
//	$$            — literal $ inside {{! ... }}; untouched elsewhere
//
// File contents and shell stdout are NOT re-templated. Variable references
// inside directive bodies are resolved before the directive executes.
type Resolver struct {
	Builtins map[string]string // built-in vars; shadow KV writes of the same name
	KV       KVReader          // may be nil; non-builtin lookups fail when nil
	WorkDir  string
	Timeout  time.Duration // shell timeout; 0 → 30 s
}

// Resolve evaluates all {{ }} directives in src and returns the result.
func (r *Resolver) Resolve(ctx context.Context, src string) (string, error) {
	return r.resolveStr(ctx, src)
}

func (r *Resolver) resolveStr(ctx context.Context, src string) (string, error) {
	var b strings.Builder
	rest := src
	for {
		open := strings.Index(rest, "{{")
		if open == -1 {
			b.WriteString(rest)
			break
		}
		b.WriteString(rest[:open])
		after := rest[open+2:]

		end := findDirectiveEnd(after)
		if end == -1 {
			// Unmatched {{ — pass through literally.
			b.WriteString("{{")
			rest = after
			continue
		}

		body := strings.TrimSpace(after[:end])
		rest = after[end+2:]

		val, err := r.evalDirective(ctx, body)
		if err != nil {
			return "", err
		}
		b.WriteString(val)
	}
	return b.String(), nil
}

func (r *Resolver) evalDirective(ctx context.Context, body string) (string, error) {
	switch {
	case strings.HasPrefix(body, "$"):
		name := strings.TrimSpace(body[1:])
		return r.lookupVar(ctx, name)

	case strings.HasPrefix(body, "!"):
		cmdTemplate := strings.TrimSpace(body[1:])
		// Resolve inner {{ $var }} before running the shell, respecting shell
		// single-quote escaping (quoted regions pass through verbatim).
		resolved, err := r.resolveCmdTemplate(ctx, cmdTemplate)
		if err != nil {
			return "", err
		}
		// $$ → literal $ inside shell directives only.
		resolved = strings.ReplaceAll(resolved, "$$", "$")
		return r.runShell(ctx, resolved)

	case strings.HasPrefix(body, "@"):
		pathTemplate := strings.TrimSpace(body[1:])
		// Resolve inner {{ $var }} before opening the file.
		resolved, err := r.resolveStr(ctx, pathTemplate)
		if err != nil {
			return "", err
		}
		return r.readFile(resolved)

	default:
		// Unknown prefix — pass through literally.
		return "{{" + body + "}}", nil
	}
}

func (r *Resolver) lookupVar(ctx context.Context, name string) (string, error) {
	if v, ok := r.Builtins[name]; ok {
		return v, nil
	}
	if r.KV == nil {
		return "", fmt.Errorf("{{ $%s }}: variable not defined (built-in or KV)", name)
	}
	v, found, err := r.KV.Get(ctx, name)
	if err != nil {
		return "", fmt.Errorf("{{ $%s }}: KV lookup: %w", name, err)
	}
	if !found {
		return "", fmt.Errorf("{{ $%s }}: variable not defined (built-in or KV)", name)
	}
	return v, nil
}

// resolveCmdTemplate resolves {{ $var }} directives in a shell command template.
// Single-quoted regions ('...') are passed through verbatim so that shell
// literals like '{{ $task_id }}' are not treated as template directives.
func (r *Resolver) resolveCmdTemplate(ctx context.Context, cmd string) (string, error) {
	var b strings.Builder
	rest := cmd
	for {
		openIdx := strings.Index(rest, "{{")
		quoteIdx := strings.Index(rest, "'")

		if openIdx == -1 {
			b.WriteString(rest)
			break
		}

		// Single-quote before the next {{ — copy the quoted region verbatim.
		if quoteIdx != -1 && quoteIdx < openIdx {
			b.WriteString(rest[:quoteIdx])
			rest = rest[quoteIdx:]
			closeIdx := strings.Index(rest[1:], "'")
			if closeIdx == -1 {
				b.WriteString(rest)
				break
			}
			b.WriteString(rest[:closeIdx+2]) // includes opening and closing '
			rest = rest[closeIdx+2:]
			continue
		}

		// {{ comes first: resolve the directive.
		b.WriteString(rest[:openIdx])
		after := rest[openIdx+2:]
		end := findDirectiveEnd(after)
		if end == -1 {
			b.WriteString("{{")
			rest = after
			continue
		}
		body := strings.TrimSpace(after[:end])
		rest = after[end+2:]
		val, err := r.evalDirective(ctx, body)
		if err != nil {
			return "", err
		}
		b.WriteString(val)
	}
	return b.String(), nil
}

func (r *Resolver) runShell(ctx context.Context, cmd string) (string, error) {
	timeout := r.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	c := exec.CommandContext(tctx, "sh", "-c", cmd)
	if r.WorkDir != "" {
		c.Dir = r.WorkDir
	}
	out, err := c.Output()
	if err != nil {
		if tctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("{{! %s }}: shell command timed out", cmd)
		}
		return "", fmt.Errorf("{{! %s }}: %w", cmd, err)
	}
	// Trim one trailing newline — most commands emit one; users who want it
	// preserved can use printf or echo -n.
	result := string(out)
	result = strings.TrimRight(result, "\n")
	return result, nil
}

func (r *Resolver) readFile(path string) (string, error) {
	// Keep the original path for the error message so it names the file as the
	// workflow author wrote it, not the resolved absolute path.
	origPath := path
	if !filepath.IsAbs(path) && r.WorkDir != "" {
		path = filepath.Join(r.WorkDir, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("{{@ %s }}: %w", origPath, err)
	}
	return string(data), nil
}

// findDirectiveEnd returns the index of the closing }} in s, where s starts
// immediately after the opening {{. Handles nested {{ }} pairs.
// Returns -1 if no matching }} is found.
func findDirectiveEnd(s string) int {
	depth := 0
	for i := 0; i < len(s)-1; i++ {
		if s[i] == '{' && s[i+1] == '{' {
			depth++
			i++
		} else if s[i] == '}' && s[i+1] == '}' {
			if depth == 0 {
				return i
			}
			depth--
			i++
		}
	}
	return -1
}

// LegacySubstitute applies the legacy single-brace substitutions, calling
// warn at most once per pattern that is actually present in content.
// Returns the substituted string.
func LegacySubstitute(content, taskDesc, prevOutput string, warn func(pattern string)) string {
	if strings.Contains(content, "{task_description}") {
		warn("{task_description}")
		content = strings.ReplaceAll(content, "{task_description}", taskDesc)
	}
	if strings.Contains(content, "{previous_output}") {
		warn("{previous_output}")
		content = strings.ReplaceAll(content, "{previous_output}", prevOutput)
	}
	return content
}

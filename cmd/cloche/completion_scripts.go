package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// bashCompletionScript is the bash completion script for cloche.
// Source it in ~/.bashrc:
//
//	source ~/.cloche/completions/cloche.bash
const bashCompletionScript = `# bash completion for cloche
# Source this file in your .bashrc or .bash_profile:
#   source ~/.cloche/completions/cloche.bash

_cloche_complete() {
    local cur="${COMP_WORDS[$COMP_CWORD]}"
    local IFS=$'\n'
    local completions
    completions=$(cloche complete --index "$COMP_CWORD" -- "${COMP_WORDS[@]}" 2>/dev/null)
    if [[ -n "$completions" ]]; then
        mapfile -t COMPREPLY < <(compgen -W "$completions" -- "$cur")
    fi
}

complete -F _cloche_complete cloche
`

// zshCompletionScript is the zsh completion function for cloche.
// Place it in a directory on fpath, e.g. ~/.cloche/completions/,
// then add to ~/.zshrc:
//
//	fpath=(~/.cloche/completions $fpath)
//	autoload -U compinit && compinit
const zshCompletionScript = `#compdef cloche
# zsh completion for cloche
# Add to ~/.zshrc:
#   fpath=(~/.cloche/completions $fpath)
#   autoload -U compinit && compinit

_cloche() {
    local -a completions
    IFS=$'\n' completions=($(cloche complete --index $CURRENT -- ${words[@]} 2>/dev/null))
    if (( ${#completions[@]} > 0 )); then
        compadd -a completions
    fi
    return 0
}

_cloche "$@"
`

// generateCompletionScripts writes bash and zsh completion scripts to dir
// and prints setup instructions. It is a no-op on Windows.
func generateCompletionScripts(dir string) {
	if runtime.GOOS == "windows" {
		return
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "  warning: could not create completions dir %s: %v\n", dir, err)
		return
	}

	bashPath := filepath.Join(dir, "cloche.bash")
	if err := os.WriteFile(bashPath, []byte(bashCompletionScript), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "  warning: could not write %s: %v\n", bashPath, err)
	} else {
		fmt.Fprintf(os.Stderr, "  create %s\n", bashPath)
	}

	zshPath := filepath.Join(dir, "_cloche")
	if err := os.WriteFile(zshPath, []byte(zshCompletionScript), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "  warning: could not write %s: %v\n", zshPath, err)
	} else {
		fmt.Fprintf(os.Stderr, "  create %s\n", zshPath)
	}

	offerShellIntegration(dir)
}

// offerShellIntegration prints shell-specific instructions for enabling
// completions, and optionally appends the sourcing snippet to the rc file if
// it is not already present.
func offerShellIntegration(completionsDir string) {
	shell := filepath.Base(os.Getenv("SHELL"))
	home := os.Getenv("HOME")
	if home == "" {
		return
	}

	switch shell {
	case "zsh":
		rcFile := filepath.Join(home, ".zshrc")
		snippet := fmt.Sprintf("fpath=(%s $fpath)", completionsDir)
		if alreadyInFile(rcFile, completionsDir) {
			return
		}
		fmt.Fprintf(os.Stderr, "\nTo enable zsh completions, add to %s:\n", rcFile)
		fmt.Fprintf(os.Stderr, "  fpath=(%s $fpath)\n", completionsDir)
		fmt.Fprintf(os.Stderr, "  autoload -U compinit && compinit\n")
		appendToRCFile(rcFile, "\n# cloche shell completions\n"+snippet+"\nautoload -U compinit && compinit\n")

	case "bash":
		rcFile := filepath.Join(home, ".bashrc")
		sourceLine := fmt.Sprintf("source %s", filepath.Join(completionsDir, "cloche.bash"))
		if alreadyInFile(rcFile, sourceLine) {
			return
		}
		fmt.Fprintf(os.Stderr, "\nTo enable bash completions, add to %s:\n", rcFile)
		fmt.Fprintf(os.Stderr, "  %s\n", sourceLine)
		appendToRCFile(rcFile, "\n# cloche shell completions\n"+sourceLine+"\n")
	}
}

// alreadyInFile reports whether needle appears anywhere in the named file.
func alreadyInFile(path, needle string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), needle)
}

// appendToRCFile appends text to path, creating the file if necessary.
// It prints a confirmation message on success.
func appendToRCFile(path, text string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		return
	}
	fmt.Fprintf(os.Stderr, "  updated %s\n", path)
}

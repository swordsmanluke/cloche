package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/swordsmanluke/cloche/internal/signal"
)

func TestSignalsToken_ShowAndRotate(t *testing.T) {
	tokens := signal.NewTokenStore(t.TempDir())
	proj := t.TempDir()

	var first bytes.Buffer
	require.NoError(t, signalsCommand([]string{"token", "--project", proj}, tokens, &first))
	tok := strings.TrimSpace(first.String())
	assert.True(t, tokens.Valid(proj, tok))

	var again bytes.Buffer
	require.NoError(t, signalsCommand([]string{"token", "--project", proj}, tokens, &again))
	assert.Equal(t, first.String(), again.String(), "showing twice doesn't change it")

	var rotated bytes.Buffer
	require.NoError(t, signalsCommand([]string{"token", "--rotate", "--project", proj}, tokens, &rotated))
	newTok := strings.TrimSpace(rotated.String())
	assert.NotEqual(t, tok, newTok)
	assert.False(t, tokens.Valid(proj, tok))
	assert.True(t, tokens.Valid(proj, newTok))
}

func TestSignalsToken_DefaultsToCwd(t *testing.T) {
	tokens := signal.NewTokenStore(t.TempDir())
	cwd, _ := os.Getwd()
	dir := t.TempDir()
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { os.Chdir(cwd) })

	var out bytes.Buffer
	require.NoError(t, signalsCommand([]string{"token"}, tokens, &out))
	real, _ := filepath.EvalSymlinks(dir)
	got := strings.TrimSpace(out.String())
	assert.True(t, tokens.Valid(dir, got) || tokens.Valid(real, got))
}

func TestSignalsCommand_Errors(t *testing.T) {
	tokens := signal.NewTokenStore(t.TempDir())
	var out bytes.Buffer
	assert.Error(t, signalsCommand(nil, tokens, &out))
	assert.Error(t, signalsCommand([]string{"send"}, tokens, &out), "there is no send command; use curl")
	assert.Error(t, signalsCommand([]string{"token", "--bogus"}, tokens, &out))
	assert.Error(t, signalsCommand([]string{"token", "--project"}, tokens, &out))
}

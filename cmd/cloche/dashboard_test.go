package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/swordsmanluke/cloche/internal/dashauth"
)

func TestDashboardToken_ShowAndRotate(t *testing.T) {
	s := dashauth.NewStore(filepath.Join(t.TempDir(), "dashboard-token"))
	var a, b, c bytes.Buffer
	require.NoError(t, dashboardCommand([]string{"token"}, s, &a))
	require.NoError(t, dashboardCommand([]string{"token"}, s, &b))
	assert.Equal(t, a.String(), b.String())
	require.NoError(t, dashboardCommand([]string{"token", "--rotate"}, s, &c))
	assert.NotEqual(t, a.String(), c.String())
	assert.True(t, s.Valid(strings.TrimSpace(c.String())))
	assert.Error(t, dashboardCommand([]string{"bogus"}, s, &a))
}

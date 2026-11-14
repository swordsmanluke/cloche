package qaconformance_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/swordsmanluke/cloche/internal/module"
	"github.com/swordsmanluke/cloche/internal/module/qaconformance"
)

// The stub module, driven through its scripts and the qa-stub tool, passes the
// suite every real backend module must pass.
func TestStubModulePassesConformance(t *testing.T) {
	qaconformance.Run(t, func(t *testing.T) qaconformance.Backend {
		b, err := qaconformance.NewStubBackend()
		require.NoError(t, err)
		t.Cleanup(b.Close)
		return b
	})
}

// The stub module loads as a module and `bind qa = stub` resolves all three
// qa slots to its exports.
func TestStubModuleResolvesThroughQAGroupBind(t *testing.T) {
	proj := t.TempDir()
	_, err := qaconformance.InstallStubModule(filepath.Join(proj, ".cloche", "modules"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(proj, ".cloche", "project.cloche"), []byte("bind qa = stub { env.QA_STUB_DIR = \"/x\" }\n"), 0o644))
	reg, err := module.Load(proj)
	require.NoError(t, err)
	rs := &module.Resolver{Registry: reg}
	for _, slot := range []string{"ask", "await-answer", "read-answer"} {
		res, err := rs.ResolveSlot(nil, slot)
		require.NoError(t, err, slot)
		require.Equal(t, "stub", res.Workflow.Module)
		require.Equal(t, map[string]string{"QA_STUB_DIR": "/x"}, res.Env)
	}
}

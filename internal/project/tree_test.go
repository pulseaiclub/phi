package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTreeConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(
		t,
		os.WriteFile(path, []byte("tree:\n  filter: user-only\n  summary: never\n  keys:\n    copy: ctrl+o\n"), 0o600),
	)
	cfg, err := parseConfigFile(path)
	require.NoError(t, err)
	assert.Equal(t, "user-only", cfg.Tree.Filter)
	assert.Equal(t, "never", cfg.Tree.Summary)
	assert.Equal(t, "ctrl+o", cfg.Tree.Keys["copy"])
	for _, content := range []string{"tree: {filter: invalid}", "tree: {summary: invalid}"} {
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
		_, err := parseConfigFile(path)
		require.ErrorContains(t, err, "tree.")
	}
}

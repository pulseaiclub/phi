package debuglog

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// saveState snapshots the package globals and installs cleanup that restores
// them, so tests never leak an open file or toggle into other tests.
func saveState(t *testing.T) {
	t.Helper()
	mu.Lock()
	prevFile, prevEnabled, prevChecked := file, enabled, checked
	file, enabled, checked = nil, false, false
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		if file != nil {
			_ = file.Close()
		}
		file, enabled, checked = prevFile, prevEnabled, prevChecked
		mu.Unlock()
	})
}

func TestSetEnabledOverridesEnv(t *testing.T) {
	t.Setenv("PHI_DEBUG", "1")
	saveState(t)
	assert.True(t, Enabled())

	SetEnabled(false)
	assert.False(t, Enabled())
	SetEnabled(true)
	assert.True(t, Enabled())
}

func TestLogfWritesOnlyWhenEnabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "debug.log")
	t.Setenv("PHI_DEBUG_FILE", path)
	t.Setenv("PHI_DEBUG", "")
	saveState(t)

	SetEnabled(true)
	Logf("hello %d", 42)
	SetEnabled(false)
	Logf("not logged")

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "hello 42")
	assert.NotContains(t, string(data), "not logged")
}

func TestFilePath(t *testing.T) {
	t.Setenv("PHI_DEBUG_FILE", "")
	assert.Equal(t, "phi-debug.log", FilePath())
	t.Setenv("PHI_DEBUG_FILE", filepath.Join(t.TempDir(), "x.log"))
	assert.Equal(t, os.Getenv("PHI_DEBUG_FILE"), FilePath())
}

// Package platform contains small operating-system adaptations shared by callers.
package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecutableName(t *testing.T) {
	for _, tc := range []struct {
		name    string
		input   string
		windows string
		other   string
	}{
		{name: "a bare name", input: "phi", windows: "phi.exe", other: "phi"},
		{name: "a name that already has an extension", input: "phi.exe", windows: "phi.exe", other: "phi.exe"},
		{name: "a name with another extension", input: "phi.sh", windows: "phi.sh", other: "phi.sh"},
		{
			name:    "a path-qualified name",
			input:   filepath.Join("bin", "phi"),
			windows: filepath.Join("bin", "phi.exe"),
			other:   filepath.Join("bin", "phi"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expected := tc.other
			if runtime.GOOS == "windows" {
				expected = tc.windows
			}
			assert.Equal(t, expected, ExecutableName(tc.input))
		})
	}
}

func TestExecutableCandidates(t *testing.T) {
	for _, tc := range []struct {
		name    string
		input   string
		windows []string
		other   []string
	}{
		{
			name:    "a bare name is probed as the platform name first",
			input:   "phi",
			windows: []string{"phi.exe", "phi"},
			other:   []string{"phi"},
		},
		{
			name:    "a path-qualified name is probed as the platform name first",
			input:   filepath.Join("bin", "phi"),
			windows: []string{filepath.Join("bin", "phi.exe"), filepath.Join("bin", "phi")},
			other:   []string{filepath.Join("bin", "phi")},
		},
		{
			name:    "a name that already has an extension is probed once",
			input:   "phi.exe",
			windows: []string{"phi.exe"},
			other:   []string{"phi.exe"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expected := tc.other
			if runtime.GOOS == "windows" {
				expected = tc.windows
			}
			assert.Equal(t, expected, ExecutableCandidates(tc.input))
		})
	}
}

func TestEnsureExecutable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "phi")
	require.NoError(t, os.WriteFile(path, []byte("binary"), 0o644))

	t.Run("an existing file", func(t *testing.T) {
		require.NoError(t, EnsureExecutable(path))

		if runtime.GOOS == "windows" {
			return
		}
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
	})

	t.Run("a missing file", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("the mode bits are not used on Windows")
		}
		err := EnsureExecutable(filepath.Join(dir, "missing"))
		require.Error(t, err)
		assert.ErrorContains(t, err, "missing")
	})
}

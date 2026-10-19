package greptool

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTruncateHead(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		maxBytes  int
		want      string
		truncated bool
	}{
		{"empty", "", 5, "", false},
		{"short single line", "hello", 6, "hello", false},
		{"short multiline", "one\ntwo", 8, "one\ntwo", false},
		{"exact single line", "hello", 5, "hello", false},
		{"exact multiline", "one\ntwo", 7, "one\ntwo", false},
		{"oversized single line", "hello", 4, "", true},
		{"oversized first line", "hello\nworld", 4, "", true},
		{"first line fills budget", "one\ntwo", 3, "one", true},
		{"newline exceeds budget", "one\ntwo\nthree", 6, "one", true},
		{"two lines fill budget", "one\ntwo\nthree", 7, "one\ntwo", true},
		{"trailing newline exceeds budget", "hello\n", 5, "hello", true},
		{"unicode byte budget", "你好\n世界", 10, "你好", true},
		{"empty zero budget", "", 0, "", false},
		{"nonempty zero budget", "hello", 0, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateHead(tt.content, tt.maxBytes)
			assert.Equal(t, tt.want, got.Content)
			assert.Equal(t, tt.truncated, got.Truncated)
			assert.LessOrEqual(t, len(got.Content), tt.maxBytes)
		})
	}
}

func TestTruncateLine(t *testing.T) {
	defaultPrefix := strings.Repeat("x", grepMaxLineRunes)
	tests := []struct {
		name      string
		line      string
		maxChars  int
		want      string
		truncated bool
	}{
		{"empty", "", 5, "", false},
		{"short", "hello", 6, "hello", false},
		{"exact", "hello", 5, "hello", false},
		{"long", "hello world", 5, "hello" + grepTruncatedSuffix, true},
		{"unicode within limit", "你好世界", 4, "你好世界", false},
		{"unicode truncated", "你好世界", 2, "你好" + grepTruncatedSuffix, true},
		{"zero uses default", defaultPrefix + "x", 0, defaultPrefix + grepTruncatedSuffix, true},
		{"negative uses default", defaultPrefix + "x", -1, defaultPrefix + grepTruncatedSuffix, true},
		{"exact default", defaultPrefix, 0, defaultPrefix, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, truncated := truncateLine(tt.line, tt.maxChars)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.truncated, truncated)
		})
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		name string
		n    int
		want string
	}{
		{"zero", 0, "0B"},
		{"below kilobyte", 1023, "1023B"},
		{"one kilobyte", 1024, "1KB"},
		{"partial kilobyte", 1536, "1KB"},
		{"just below two kilobytes", 2047, "1KB"},
		{"two kilobytes", 2048, "2KB"},
		{"partial second kilobyte", 2560, "2KB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, formatBytes(tt.n))
		})
	}
}

func TestExitCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"nil", nil},
		{"plain error", errors.New("search failed")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, -1, exitCode(tt.err))
		})
	}

	t.Run("process exit error", func(t *testing.T) {
		shell := "sh"
		args := []string{"-c", "exit 7"}
		if runtime.GOOS == "windows" {
			shell = "cmd"
			args = []string{"/c", "exit 7"}
		}
		path, err := exec.LookPath(shell)
		if err != nil {
			t.Skip("shell not available:", err)
		}

		err = exec.CommandContext(t.Context(), path, args...).Run()
		var exitErr *exec.ExitError
		require.ErrorAs(t, err, &exitErr)
		assert.Equal(t, 7, exitCode(err))
		assert.Equal(t, 7, exitCode(fmt.Errorf("search failed: %w", err)))
	})
}

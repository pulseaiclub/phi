package bashtool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

func TestBashToolDefinition(t *testing.T) {
	tool := BashTool()

	assert.Equal(t, "bash", tool.Definition.Name)
	assert.NotEmpty(t, tool.Definition.Description)
	require.NotNil(t, tool.Definition.Params)
	assert.Equal(t, "object", tool.Definition.Params.Type)
	assert.Equal(t, []string{"command"}, tool.Definition.Params.Required)
	assert.False(t, tool.Definition.Readable)
	assert.Contains(t, tool.Definition.Params.Properties, "command")
	assert.Contains(t, tool.Definition.Params.Properties, "timeout")
	require.NotNil(t, tool.Run)
	require.NotNil(t, tool.DetailFromArgs)
}

func TestBashDetail(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    string
	}{
		{
			name:    "trims whitespace",
			command: "  printf hi  \n\t",
			want:    "printf hi",
		},
		{
			name:    "all blank command",
			command: "   \t\n  ",
			want:    "",
		},
		{
			name:    "empty command",
			command: "",
			want:    "",
		},
		{
			name:    "standard command",
			command: "go test ./...",
			want:    "go test ./...",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, bashDetail(bashInput{Command: tc.command}))
		})
	}
}

func TestBashToolDetailFromArgs(t *testing.T) {
	tool := BashTool()

	assert.Equal(t, "printf hi", tool.DetailFromArgs([]byte(`{"command":"  printf hi  \n"}`)))
	assert.Empty(t, tool.DetailFromArgs([]byte(`{"command":"   \t  "}`)))
	assert.NotEmpty(t, tool.DetailFromArgs([]byte(`invalid json`)))
}

func TestRunBash(t *testing.T) {
	tests := []struct {
		name        string
		args        string
		wantContent string
		wantDetail  string
		wantErr     string
	}{
		{
			name:    "empty command",
			args:    `{"command":""}`,
			wantErr: "empty command",
		},
		{
			name:    "whitespace only command",
			args:    `{"command":"   \t\n  "}`,
			wantErr: "empty command",
		},
		{
			name:        "success",
			args:        `{"command":"printf hi"}`,
			wantContent: "hi",
			wantDetail:  "printf hi",
		},
		{
			name:        "non-zero exit",
			args:        `{"command":"exit 3"}`,
			wantContent: "(no output)\n(exit error: exit status 3)",
			wantDetail:  "exit 3",
		},
		{
			name:        "non-zero exit with output",
			args:        `{"command":"printf fail; exit 1"}`,
			wantContent: "fail\n(exit error: exit status 1)",
			wantDetail:  "printf fail; exit 1",
		},
		{
			name:        "empty output",
			args:        `{"command":"true"}`,
			wantContent: "(no output)",
			wantDetail:  "true",
		},
		{
			name:        "stderr",
			args:        `{"command":"printf err >&2"}`,
			wantContent: "err",
			wantDetail:  "printf err >&2",
		},
	}

	tool := BashTool()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := tool.Run(t.Context(), []byte(tc.args))
			if tc.wantErr != "" {
				require.EqualError(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantContent, res.Content)
			assert.Equal(t, tc.wantDetail, res.Detail)
			assert.Equal(t, tc.wantContent, res.Output)
		})
	}
}

func TestRunBashTimeout(t *testing.T) {
	tool := BashTool()
	start := time.Now()
	res, err := tool.Run(t.Context(), []byte(`{"command":"sleep 2","timeout":1}`))
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Equal(t, "(no output)\n(command canceled or timed out)", res.Content)
	assert.Equal(t, "sleep 2", res.Detail)
	assert.Equal(t, "(no output)\n(command canceled or timed out)", res.Output)
	assert.Less(t, elapsed, 2*time.Second)
}

func TestRunBashPreCanceledContext(t *testing.T) {
	tool := BashTool()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	res, err := tool.Run(ctx, []byte(`{"command":"echo hi"}`))
	require.NoError(t, err)
	assert.Equal(t, "(no output)\n(command canceled or timed out)", res.Content)
	assert.Equal(t, "echo hi", res.Detail)
	assert.Equal(t, "(no output)\n(command canceled or timed out)", res.Output)
}

func TestRunBashLargeOutput(t *testing.T) {
	tool := BashTool()
	res, err := tool.Run(t.Context(), []byte(`{"command":"seq 1 2000"}`))
	require.NoError(t, err)
	cleanupBashOutputFile(t, res.Content)

	assert.Contains(t, res.Content, "phi-bash-")
	assert.Contains(t, res.Content, "Showing lines 1001-2000 of 2000")
	assert.True(t, strings.HasSuffix(res.Content, "]"))
	assert.Equal(t, "seq 1 2000", res.Detail)
	assert.Equal(t, res.Content, res.Output)
}

func TestRunBashTimeoutClamping(t *testing.T) {
	tests := []struct {
		name string
		args string
	}{
		{
			name: "default timeout on zero",
			args: `{"command":"printf ok","timeout":0}`,
		},
		{
			name: "default timeout on negative",
			args: `{"command":"printf ok","timeout":-10}`,
		},
		{
			name: "clamped timeout on excessive value",
			args: `{"command":"printf ok","timeout":5000}`,
		},
	}

	tool := BashTool()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := tool.Run(t.Context(), []byte(tc.args))
			require.NoError(t, err)
			assert.Equal(t, "ok", res.Content)
			assert.Equal(t, "printf ok", res.Detail)
		})
	}
}

func TestRunBashInvalidJSON(t *testing.T) {
	tool := BashTool()
	_, err := tool.Run(t.Context(), []byte(`invalid-json`))
	require.Error(t, err)
}

func TestRunBashCwd(t *testing.T) {
	tool := BashTool()
	dir := t.TempDir()
	ctx := tooldef.WithCwd(t.Context(), dir)
	res, err := tool.Run(ctx, []byte(`{"command":"pwd"}`))
	require.NoError(t, err)
	assert.Contains(t, res.Content, filepath.Base(dir))
}

func TestFindBashOnPath(t *testing.T) {
	path := findBashOnPath()
	require.NotEmpty(t, path)
	assert.True(t, isFile(path))
}

func TestNewBashOutputTailDefaults(t *testing.T) {
	tail := NewBashOutputTail(0, 0)
	require.NotNil(t, tail)
	assert.Equal(t, BashMaxOutputLines, tail.maxLines)
	assert.Equal(t, BashMaxOutputBytes, tail.maxBytes)
}

func TestTruncateBashTailDefaults(t *testing.T) {
	display, path := truncateBashTail("short output", 0, 0, "Full output")
	assert.Equal(t, "short output", display)
	assert.Empty(t, path)
}

func TestShellOutputWriterEmptyAndNoCallback(t *testing.T) {
	w := &shellOutputWriter{cb: newCappedBuffer(10)}
	n, err := w.Write(nil)
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	n, err = w.Write([]byte("abc"))
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	assert.Equal(t, "abc", w.Collected())
}

func TestBuildShellCommandCancelNilProcess(t *testing.T) {
	cmd, err := buildShellCommand(t.Context(), "echo test")
	require.NoError(t, err)
	require.NotNil(t, cmd.Cancel)
	require.NoError(t, cmd.Cancel())
}

func TestShellEnvWithBinDir(t *testing.T) {
	binDir := project.GetDefaultProject().Global().BinDir()
	err := os.MkdirAll(binDir, 0o755)
	require.NoError(t, err)

	env := shellEnv()
	require.NotEmpty(t, env)
}

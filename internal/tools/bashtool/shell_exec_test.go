package bashtool

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExecShellEcho(t *testing.T) {
	res, err := ExecShell(t.Context(), "echo hello", ShellExecOptions{})
	require.NoError(t, err)
	require.False(t, res.Canceled || res.ExitCode != 0, "result: %+v", res)
	require.Contains(t, res.Output, "hello")
}

func TestExecShellCapturesBothStreams(t *testing.T) {
	res, err := ExecShell(t.Context(), "printf stdout; printf stderr >&2", ShellExecOptions{})
	require.NoError(t, err)
	require.False(t, res.ExitCode != 0 || res.Canceled, "result: %+v", res)
	require.Contains(t, res.Output, "stdout")
	require.Contains(t, res.Output, "stderr")
}

func TestShellOutputWriterStreamsAfterCollectionCap(t *testing.T) {
	var streamed strings.Builder
	output := &shellOutputWriter{
		cb:      newCappedBuffer(4),
		onChunk: func(chunk string) { streamed.WriteString(chunk) },
	}
	_, err := output.Write([]byte("1234"))
	require.NoError(t, err)
	_, err = output.Write([]byte("5678"))
	require.NoError(t, err)
	require.Equal(t, "12345678", streamed.String(), "streamed output should contain all chunks")
	require.Equal(t, "5678", output.cb.String(), "collected output should be bounded tail")
	require.True(t, output.cb.Truncated(), "expected truncation")
}

func TestExecShellCapturesOutputBeforeProcessExit(t *testing.T) {
	const outputSize = 32 * 1024
	const command = "printf '%*s' 32768 '' | tr ' ' x"

	for range 8 {
		res, err := ExecShell(t.Context(), command, ShellExecOptions{})
		require.NoError(t, err)
		require.False(t, res.ExitCode != 0 || res.Canceled, "result: %+v", res)
		require.Len(t, res.Output, outputSize)
		require.Empty(t, strings.Trim(res.Output, "x"), "expected all x bytes")
	}
}

func TestExecShellKeepsOutputTail(t *testing.T) {
	// Regression: output still buffered in the kernel pipe at process exit
	// must not be dropped (writer-mode copying drains to EOF before Run returns).
	command := "seq 1 100000" // ~590KB, under the collection cap
	res, err := ExecShell(t.Context(), command, ShellExecOptions{})
	require.NoError(t, err)
	cleanupBashOutputFile(t, res.Output)
	require.False(t, res.ExitCode != 0 || res.Canceled, "result: %+v", res)
	// The display notice's own line range proves line 100000 was collected.
	require.Contains(t, res.Output, "Showing lines 99001-100000 of 100000", "output tail lost")
	require.Contains(t, res.Output, "Full output:")
	require.NotContains(t, res.Output, "Retained output:", "under-cap output mislabeled")
	require.NotContains(t, res.Output, "[output truncated:", "unexpected collection truncation")
}

func TestExecShellBoundsCollection(t *testing.T) {
	// Runaway output must not be buffered unboundedly: the newest
	// BashMaxCollectBytes are kept and the collection truncation is reported.
	command := "yes x | head -c 20971520" // 20MB
	res, err := ExecShell(t.Context(), command, ShellExecOptions{})
	require.NoError(t, err)
	cleanupBashOutputFile(t, res.Output)
	require.False(t, res.ExitCode != 0 || res.Canceled, "result: %+v", res)
	require.Contains(t, res.Output, "[output truncated: only the last 8 MiB was kept]")
	require.Contains(t, res.Output, "Retained output:")
	require.NotContains(t, res.Output, "Full output:", "collection-truncated output mislabeled")
}

func cleanupBashOutputFile(t *testing.T, output string) {
	t.Helper()
	for _, marker := range []string{"Full output: ", "Retained output: "} {
		_, rest, found := strings.Cut(output, marker)
		if !found {
			continue
		}
		path := strings.TrimSpace(strings.Split(rest, "]")[0])
		if path == "" {
			return
		}
		t.Cleanup(func() { _ = os.Remove(path) })
		return
	}
}

func TestExecShellCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	res, err := ExecShell(ctx, "sleep 5", ShellExecOptions{})
	require.NoError(t, err)
	require.True(t, res.Canceled, "want canceled, got %+v", res)
}

func TestExecShellExitCode(t *testing.T) {
	res, err := ExecShell(t.Context(), "exit 7", ShellExecOptions{})
	require.NoError(t, err)
	require.Equal(t, 7, res.ExitCode)
}

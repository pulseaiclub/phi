package spec

import (
	"bytes"
	"os/exec"
)

// limitedOutput runs a command and returns what it printed, up to
// helpOutputLimit bytes. Help output is small; a limit keeps a command that
// streams or a runaway pager from growing this process.
func limitedOutput(cmd *exec.Cmd) (string, error) {
	var buf limitedBuffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// limitedBuffer is a bytes.Buffer that silently drops what it cannot hold, so
// the child keeps writing instead of blocking on a full pipe.
type limitedBuffer struct {
	bytes.Buffer
}

// Write keeps the first helpOutputLimit bytes and reports success for the rest:
// reporting a short write would kill the child on a broken pipe, and a partial
// usage is still a usage.
func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := helpOutputLimit - b.Len(); room > 0 {
		if len(p) > room {
			_, _ = b.Buffer.Write(p[:room])
		} else {
			_, _ = b.Buffer.Write(p)
		}
	}
	return len(p), nil
}
